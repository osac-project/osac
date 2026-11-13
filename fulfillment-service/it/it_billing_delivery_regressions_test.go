/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingstub"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// These are deployed component-integration regressions, not real-M360 or provider-provisioning E2E. The only
// provider double is the TLS contract endpoint; deployed Fulfillment uses its real M360 adapter and PostgreSQL.
var _ = Describe("Billing delivery regressions", Label("billing", "billing-configured", "billing-delivery"), Serial, func() {
	var stub *billingstub.Stub
	var accounts []billingfake.AccountSpec
	var assignments publicv1.TenantBillingAssignmentsClient
	var tenants privatev1.TenantsClient

	BeforeEach(func(ctx context.Context) {
		stubURL, listen := os.Getenv("OSAC_IT_BILLING_STUB_URL"), os.Getenv("OSAC_IT_BILLING_STUB_LISTEN")
		if stubURL == "" || listen == "" {
			Skip("OSAC_IT_BILLING_STUB_LISTEN and OSAC_IT_BILLING_STUB_URL are not set")
		}
		suffix := uuid.New()
		accounts = []billingfake.AccountSpec{
			{ID: uuid.New(), Name: "billing-delivery-first-" + suffix, ExternalID: "delivery-first-" + suffix,
				RawStatus: "Active", ApprovalStatus: "Approved"},
			{ID: uuid.New(), Name: "billing-delivery-second-" + suffix, ExternalID: "delivery-second-" + suffix,
				RawStatus: "Active", ApprovalStatus: "Approved"},
		}
		stub = billingstub.New(billingfake.Scenario{Accounts: accounts})
		configureBillingStub(ctx, stub, listen, stubURL)
		assignments = publicv1.NewTenantBillingAssignmentsClient(tool.ExternalView().AdminConn())
		tenants = privatev1.NewTenantsClient(tool.InternalView().AdminConn())
	})

	discover := func(ctx context.Context, name string) *publicv1.BillingAccount {
		GinkgoHelper()
		client := publicv1.NewBillingAccountsClient(tool.ExternalView().AdminConn())
		var account *publicv1.BillingAccount
		Eventually(ctx, func(g Gomega) {
			response, err := client.List(ctx, publicv1.BillingAccountsListRequest_builder{
				Filter: new(fmt.Sprintf("this.display_name == %q", name)),
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.GetItems()).To(HaveLen(1))
			account = response.GetItems()[0]
			g.Expect(account.GetState()).To(Equal(publicv1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE))
		}, time.Minute, time.Second).Should(Succeed())
		return account
	}
	createAssignment := func(ctx context.Context, tenant, account string) (*publicv1.TenantBillingAssignmentsCreateResponse, error) {
		return assignments.Create(ctx, publicv1.TenantBillingAssignmentsCreateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Metadata: publicv1.Metadata_builder{Name: "delivery-" + uuid.New()}.Build(),
				Spec: publicv1.TenantBillingAssignmentSpec_builder{
					TenantId: tenant, BillingAccountId: account,
				}.Build(),
			}.Build(),
		}.Build())
	}
	history := func(ctx context.Context, tenant string) []*publicv1.TenantBillingAssignment {
		GinkgoHelper()
		response, err := assignments.List(ctx, publicv1.TenantBillingAssignmentsListRequest_builder{
			Filter: new(fmt.Sprintf("this.spec.tenant_id == %q", tenant)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		return response.GetItems()
	}
	expectSameHistory := func(before, after []*publicv1.TenantBillingAssignment) {
		GinkgoHelper()
		Expect(after).To(HaveLen(len(before)))
		for _, expected := range before {
			found := false
			for _, actual := range after {
				if actual.GetId() == expected.GetId() {
					Expect(proto.Equal(actual, expected)).To(BeTrue(), "failed mutation must preserve each closed and current interval")
					found = true
				}
			}
			Expect(found).To(BeTrue(), "prior interval %s must remain present", expected.GetId())
		}
	}
	expectProviderRead := func(nativeID string, since int) {
		GinkgoHelper()
		Expect(stub.Requests()[since:]).To(ContainElement(SatisfyAll(
			HaveField("Method", "GET"), HaveField("Op", "GetAccount"), HaveField("Failed", false),
			HaveField("Path", "/api/v1/external/accounts/"+nativeID))))
		Expect(stub.Writes()).To(BeEmpty())
	}

	It("rejects Create after provider-only deactivation without rediscovery and recovers after reactivation", func(ctx context.Context) {
		account := discover(ctx, accounts[0].Name)
		tenant := newBillingTenant(ctx, nil, nil)
		// From this point through recovery, do not call BillingAccounts.List: the rejected Create stores the
		// provider's INACTIVE observation, so recovery must prove a fresh assignment-time read supersedes it.
		accounts[0].RawStatus = "Inactive"
		stub.SetScenario(billingfake.Scenario{Accounts: accounts})
		before := len(stub.Requests())
		_, err := createAssignment(ctx, tenant.GetMetadata().GetName(), account.GetId())
		expectBillingReason(err, "ACCOUNT_INACTIVE")
		expectProviderRead(accounts[0].ID, before)
		Expect(history(ctx, tenant.GetMetadata().GetName())).To(BeEmpty(), "rejected Create must not leave an interval")

		accounts[0].RawStatus = "Active"
		stub.SetScenario(billingfake.Scenario{Accounts: accounts})
		before = len(stub.Requests())
		created, err := createAssignment(ctx, tenant.GetMetadata().GetName(), account.GetId())
		Expect(err).ToNot(HaveOccurred())
		Expect(created.GetObject().GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
		expectProviderRead(accounts[0].ID, before)
		Expect(history(ctx, tenant.GetMetadata().GetName())).To(HaveLen(1))
	})

	It("rejects stale-ACTIVE replacement and same-account revalidation without changing history and recovers", func(ctx context.Context) {
		first, second := discover(ctx, accounts[0].Name), discover(ctx, accounts[1].Name)
		tenant := newBillingTenant(ctx, nil, nil)
		name := tenant.GetMetadata().GetName()
		initial, err := createAssignment(ctx, name, first.GetId())
		Expect(err).ToNot(HaveOccurred())
		seedReplacement := proto.Clone(initial.GetObject()).(*publicv1.TenantBillingAssignment)
		seedReplacement.GetSpec().SetBillingAccountId(second.GetId())
		created, err := assignments.Update(ctx, publicv1.TenantBillingAssignmentsUpdateRequest_builder{Object: seedReplacement}.Build())
		Expect(err).ToNot(HaveOccurred())
		original := created.GetObject()
		Expect(original.GetStatus().GetEffectiveTo()).To(BeNil())
		originalHistory := history(ctx, name)
		Expect(originalHistory).To(HaveLen(2), "exercise a closed interval as well as the current interval")
		// Neither mutation nor recovery below performs discovery. State comes only from assignment-time reads.
		accounts[0].RawStatus = "Inactive"
		stub.SetScenario(billingfake.Scenario{Accounts: accounts})
		before := len(stub.Requests())
		replacement := proto.Clone(original).(*publicv1.TenantBillingAssignment)
		replacement.GetSpec().SetBillingAccountId(first.GetId())
		_, err = assignments.Update(ctx, publicv1.TenantBillingAssignmentsUpdateRequest_builder{Object: replacement}.Build())
		expectBillingReason(err, "ACCOUNT_INACTIVE")
		expectProviderRead(accounts[0].ID, before)
		unchanged := history(ctx, name)
		expectSameHistory(originalHistory, unchanged)

		accounts[1].RawStatus = "Inactive"
		stub.SetScenario(billingfake.Scenario{Accounts: accounts})
		before = len(stub.Requests())
		_, err = assignments.Update(ctx, publicv1.TenantBillingAssignmentsUpdateRequest_builder{Object: original}.Build())
		expectBillingReason(err, "ACCOUNT_INACTIVE")
		expectProviderRead(accounts[1].ID, before)
		unchanged = history(ctx, name)
		expectSameHistory(originalHistory, unchanged)

		accounts[1].RawStatus = "Active"
		stub.SetScenario(billingfake.Scenario{Accounts: accounts})
		before = len(stub.Requests())
		revalidated, err := assignments.Update(ctx, publicv1.TenantBillingAssignmentsUpdateRequest_builder{Object: original}.Build())
		Expect(err).ToNot(HaveOccurred())
		expectProviderRead(accounts[1].ID, before)
		Expect(revalidated.GetObject().GetId()).To(Equal(original.GetId()))
		Expect(proto.Equal(revalidated.GetObject().GetStatus().GetEffectiveFrom(), original.GetStatus().GetEffectiveFrom())).To(BeTrue())
		Expect(revalidated.GetObject().GetStatus().GetEffectiveTo()).To(BeNil())
		Expect(history(ctx, name)).To(HaveLen(2))

		accounts[0].RawStatus = "Active"
		stub.SetScenario(billingfake.Scenario{Accounts: accounts})
		before = len(stub.Requests())
		updated, err := assignments.Update(ctx, publicv1.TenantBillingAssignmentsUpdateRequest_builder{Object: replacement}.Build())
		Expect(err).ToNot(HaveOccurred())
		expectProviderRead(accounts[0].ID, before)
		Expect(updated.GetObject().GetId()).ToNot(Equal(original.GetId()))
		Expect(history(ctx, name)).To(HaveLen(3))
		old, err := assignments.Get(ctx, publicv1.TenantBillingAssignmentsGetRequest_builder{Id: original.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(old.GetObject().GetStatus().GetEffectiveTo(), updated.GetObject().GetStatus().GetEffectiveFrom())).To(BeTrue())
	})

	It("releases billed tenant domains only after all cleanup finalizers complete and preserves history on reuse", func(ctx context.Context) {
		account := discover(ctx, accounts[0].Name)
		const hold = "it.osac.openshift.io/billing-domain-hold"
		domain := "billing-" + uuid.New() + ".example.com"
		tenant := newBillingTenant(ctx, []string{domain}, []string{hold})
		name := tenant.GetMetadata().GetName()
		// Remove only our own barrier during cleanup; production finalizers always belong to their controllers.
		DeferCleanup(func(ctx context.Context) { removeDeliveryTenantHold(ctx, tenants, tenant.GetId(), hold) })
		waitForTenantSyncedWithFinalizers(ctx, tenants, tenant.GetId())
		created, err := createAssignment(ctx, name, account.GetId())
		Expect(err).ToNot(HaveOccurred())
		_, err = tenants.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: tenant.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		// Wait for genuine deployed controller cleanup. Our barrier prevents this from racing domain release.
		Eventually(ctx, func(g Gomega) {
			_, err := tenants.Signal(ctx, privatev1.TenantsSignalRequest_builder{Id: tenant.GetId()}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			read, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenant.GetId()}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(read.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
			g.Expect(read.GetObject().GetMetadata().GetFinalizers()).To(ConsistOf(hold))
		}, 3*time.Minute, time.Second).Should(Succeed())
		closed := history(ctx, name)
		Expect(closed).To(HaveLen(1))
		Expect(closed[0].GetId()).To(Equal(created.GetObject().GetId()))
		Expect(closed[0].GetStatus().GetEffectiveTo()).ToNot(BeNil())

		collision, err := tenants.Create(ctx, privatev1.TenantsCreateRequest_builder{Object: privatev1.Tenant_builder{
			Metadata: privatev1.Metadata_builder{Name: "domain-collision-" + uuid.New()}.Build(),
			Spec:     privatev1.TenantSpec_builder{Domains: []string{domain}}.Build(),
		}.Build()}.Build())
		// Register cleanup before asserting: a collision regression means this test owns the tenant and the domain,
		// so a rerun must not inherit either.
		if err == nil {
			DeferCleanup(func(ctx context.Context) {
				cleanupBillingTenant(ctx, tenants, collision.GetObject().GetId())
			})
		}
		Expect(status.Code(err)).To(Equal(codes.AlreadyExists), "deletion acceptance is not completed cleanup")
		removeDeliveryTenantHold(ctx, tenants, tenant.GetId(), hold)
		retained, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenant.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(retained.GetObject().GetMetadata().GetFinalizers()).To(BeEmpty())
		Expect(retained.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		Expect(retained.GetObject().GetSpec().GetDomains()).To(ConsistOf(domain))

		replacement := newBillingTenant(ctx, []string{domain}, nil)
		Expect(replacement.GetId()).ToNot(Equal(tenant.GetId()))
		publicTenants := publicv1.NewTenantsClient(tool.ExternalView().AdminConn())
		listed, err := publicTenants.List(ctx, publicv1.TenantsListRequest_builder{
			Filter: new(fmt.Sprintf("%q in this.spec.domains", domain)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listed.GetItems()).To(HaveLen(1))
		Expect(listed.GetItems()[0].GetId()).To(Equal(replacement.GetId()))
		afterReuse := history(ctx, name)
		Expect(afterReuse).To(HaveLen(1))
		Expect(proto.Equal(afterReuse[0], closed[0])).To(BeTrue(), "domain reuse must not rewrite billing history")
		Expect(stub.Writes()).To(BeEmpty())
	})

	It("keeps a billed tenant live when an independent pending service account blocks direct deletion", func(ctx context.Context) {
		account := discover(ctx, accounts[0].Name)
		// Pause only the shared controller in this serial fixture. This deterministically creates a pending account
		// without a credential Secret or project references, so its tenant FK is the independent deletion blocker.
		resume := pauseDeliveryController(ctx)
		serviceAccounts := privatev1.NewServiceAccountsClient(tool.InternalView().AdminConn())
		var accountID string
		// One ordered cleanup: resume so reconciliation can finish, then delete the blocking account. The tenant
		// fixture's cleanup was registered earlier, so it runs after this one with a controller and no blocker.
		removeAccount := func(ctx context.Context) {
			GinkgoHelper()
			resume(ctx)
			if accountID == "" {
				return
			}
			_, err := serviceAccounts.Delete(ctx, privatev1.ServiceAccountsDeleteRequest_builder{Id: accountID}.Build())
			if status.Code(err) != codes.NotFound {
				Expect(err).ToNot(HaveOccurred())
			}
			Eventually(ctx, func() codes.Code {
				_, err := serviceAccounts.Get(ctx, privatev1.ServiceAccountsGetRequest_builder{Id: accountID}.Build())
				return status.Code(err)
			}, 3*time.Minute, time.Second).Should(Equal(codes.NotFound))
		}
		tenant := newBillingTenant(ctx, nil, nil)
		name := tenant.GetMetadata().GetName()
		createdAccount, err := serviceAccounts.Create(ctx, privatev1.ServiceAccountsCreateRequest_builder{
			Object: privatev1.ServiceAccount_builder{Metadata: privatev1.Metadata_builder{
				Name: "delivery-blocker", Tenant: name,
			}.Build()}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		accountID = createdAccount.GetObject().GetId()
		DeferCleanup(removeAccount)
		Expect(createdAccount.GetObject().GetStatus().GetClientSecret()).To(BeNil())
		projects := privatev1.NewProjectsClient(tool.InternalView().AdminConn())
		listed, err := projects.List(ctx, privatev1.ProjectsListRequest_builder{
			Filter: new(fmt.Sprintf("this.metadata.tenant == %q", name)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listed.GetItems()).ToNot(BeEmpty())
		for _, project := range listed.GetItems() {
			Expect(project.GetMetadata().GetFinalizers()).To(BeEmpty())
			_, err = projects.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{Id: project.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
		}
		listed, err = projects.List(ctx, privatev1.ProjectsListRequest_builder{
			Filter: new(fmt.Sprintf("this.metadata.tenant == %q", name)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listed.GetItems()).To(BeEmpty())
		created, err := createAssignment(ctx, name, account.GetId())
		Expect(err).ToNot(HaveOccurred())
		before, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenant.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(before.GetObject().GetMetadata().GetFinalizers()).To(BeEmpty())
		_, err = tenants.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: tenant.GetId()}.Build())
		Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		Expect(err.Error()).To(ContainSubstring("still referenced by other objects"),
			"the pending account's tenant foreign key, not another guard, must block the deletion")
		after, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenant.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(after.GetObject(), before.GetObject())).To(BeTrue(), "failed deletion must roll back all tenant metadata")
		unchanged := history(ctx, name)
		Expect(unchanged).To(HaveLen(1))
		Expect(proto.Equal(unchanged[0], created.GetObject())).To(BeTrue(), "failed deletion must not close the assignment")
		blocker, err := serviceAccounts.Get(ctx, privatev1.ServiceAccountsGetRequest_builder{Id: accountID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(blocker.GetObject().GetStatus().GetState()).To(Equal(
			privatev1.ServiceAccountState_SERVICE_ACCOUNT_STATE_PENDING))
		Expect(blocker.GetObject().GetMetadata().GetFinalizers()).To(ContainElement(finalizers.Controller),
			"the blocker must still hold its cleanup finalizer at the failure")
		Expect(blocker.GetObject().GetStatus().GetClientSecret()).To(BeNil())
		Expect(blocker.GetObject().GetStatus().GetKeycloakClientUuid()).To(BeEmpty())

		// Removing the blocker must recover deletion instead of leaving the tenant permanently undeletable.
		removeAccount(ctx)
		Eventually(ctx, func(g Gomega) {
			// A concurrent controller write can abort a retried delete; a lasting blocker fails this loop.
			_, err := tenants.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: tenant.GetId()}.Build())
			g.Expect(err).ToNot(HaveOccurred())
		}, time.Minute, time.Second).Should(Succeed())
		closed := history(ctx, name)
		Expect(closed).To(HaveLen(1), "accepted deletion must retain the interval instead of removing history")
		Expect(closed[0].GetId()).To(Equal(created.GetObject().GetId()))
		Expect(closed[0].GetStatus().GetEffectiveTo()).ToNot(BeNil(),
			"accepted deletion must close the interval even though the billed tenant tombstone remains")
		Eventually(ctx, func(g Gomega) {
			tombstone, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: tenant.GetId()}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(tombstone.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
			g.Expect(tombstone.GetObject().GetMetadata().GetFinalizers()).To(BeEmpty())
		}, 3*time.Minute, time.Second).Should(Succeed())
		// Normal deployed reconciliation is restored before cleanup. No claims about existing-token revocation or
		// an end-to-end Keycloak deletion journey follow from this database/API blocker regression.
	})
})

// pauseDeliveryController scales the shared fulfillment controller to zero and returns an idempotent resume function.
// The controller must resume before any cleanup that needs reconciliation, so the helper registers the resume as a
// safety cleanup; calling it again in the body is a no-op and never triggers another rollout.
func pauseDeliveryController(ctx context.Context) func(context.Context) {
	GinkgoHelper()
	kube := tool.KubeClient()
	key := crclient.ObjectKey{Namespace: "osac", Name: "fulfillment-controller"}
	current := &appsv1.Deployment{}
	Expect(kube.Get(ctx, key, current)).To(Succeed())
	Expect(current.Spec.Replicas).ToNot(BeNil())
	originalReplicas := *current.Spec.Replicas
	Expect(originalReplicas).To(BeNumerically(">", 0), "the controller must be running before pausing it")
	selector, err := metav1.LabelSelectorAsSelector(current.Spec.Selector)
	Expect(err).ToNot(HaveOccurred())
	expectReplicas := func(ctx context.Context, replicas int32) {
		GinkgoHelper()
		Eventually(ctx, func(g Gomega) {
			latest := &appsv1.Deployment{}
			g.Expect(kube.Get(ctx, key, latest)).To(Succeed())
			g.Expect(latest.Status.ObservedGeneration).To(BeNumerically(">=", latest.Generation))
			g.Expect(latest.Status.Replicas).To(Equal(replicas))
			g.Expect(latest.Status.ReadyReplicas).To(Equal(replicas))
			if replicas == 0 {
				pods := &corev1.PodList{}
				// Include terminating pods: they can still process events until they exit.
				g.Expect(kube.List(ctx, pods, crclient.InNamespace(key.Namespace),
					crclient.MatchingLabelsSelector{Selector: selector})).To(Succeed())
				g.Expect(pods.Items).To(BeEmpty(), "no terminating controller may race the deterministic fixture")
			}
		}, 3*time.Minute, time.Second).Should(Succeed())
	}
	scale := func(ctx context.Context, replicas int32) {
		GinkgoHelper()
		latest := &appsv1.Deployment{}
		Expect(kube.Get(ctx, key, latest)).To(Succeed())
		original := latest.DeepCopy()
		latest.Spec.Replicas = &replicas
		Expect(kube.Patch(ctx, latest, crclient.MergeFrom(original))).To(Succeed())
	}
	resumed := false
	resume := func(ctx context.Context) {
		GinkgoHelper()
		if resumed {
			return
		}
		scale(ctx, originalReplicas)
		expectReplicas(ctx, originalReplicas)
		resumed = true
	}
	DeferCleanup(resume)
	scale(ctx, 0)
	expectReplicas(ctx, 0)
	return resume
}

func removeDeliveryTenantHold(ctx context.Context, client privatev1.TenantsClient, id, hold string) {
	GinkgoHelper()
	Eventually(ctx, func(g Gomega) {
		read, err := client.Get(ctx, privatev1.TenantsGetRequest_builder{Id: id}.Build())
		if status.Code(err) == codes.NotFound {
			return
		}
		g.Expect(err).ToNot(HaveOccurred())
		object := read.GetObject()
		if !slices.Contains(object.GetMetadata().GetFinalizers(), hold) {
			return
		}
		object.GetMetadata().SetFinalizers(slices.DeleteFunc(object.GetMetadata().GetFinalizers(), func(value string) bool {
			return value == hold
		}))
		_, err = client.Update(ctx, privatev1.TenantsUpdateRequest_builder{
			Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}}, Lock: true,
		}.Build())
		g.Expect(err).ToNot(HaveOccurred())
	}, time.Minute, time.Second).Should(Succeed())
}
