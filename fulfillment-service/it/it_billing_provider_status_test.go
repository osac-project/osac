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
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/billing"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingfake"
	"github.com/osac-project/osac/fulfillment-service/internal/testing/billingstub"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// The dev deployment configures no billing provider, so these specs exercise disabled mode end to end. They need no
// provider, hence no provider call can happen.
var _ = Describe("Billing provider status", Label("billing"), func() {
	It("is NOT_CONFIGURED over public gRPC when no provider is configured", func(ctx context.Context) {
		client := publicv1.NewBillingProviderStatusesClient(tool.ExternalView().AdminConn())
		response, err := client.Get(ctx, publicv1.BillingProviderStatusesGetRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetState()).To(Equal(
			publicv1.BillingProviderState_BILLING_PROVIDER_STATE_NOT_CONFIGURED))
		Expect(response.GetObject().GetProviderKey()).To(BeEmpty())
	})

	It("is shown by the CLI as NOT_CONFIGURED", func(ctx context.Context) {
		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
		_, _, exitCode := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(exitCode).To(Equal(0), "login should succeed")

		stdout, _, exitCode := tool.RunCLI(ctx, homeDir, "get", "billingproviderstatus", "-o", "json")
		Expect(exitCode).To(Equal(0))
		Expect(stdout).To(MatchJSON(`{
			"state": "BILLING_PROVIDER_STATE_NOT_CONFIGURED",
			"usage_delivery_state": "BILLING_USAGE_DELIVERY_STATE_UNKNOWN"
		}`))
	})
})

// These specs point the deployed grpc-server at a deterministic stub M360 server through the real M360 settings
// (OSAC_BILLING_PROVIDER=m360), mounted at the chart's paths, so they exercise the real adapter. The stub runs in this
// test process and serves TLS with a certificate for the host of its URL, signed by a test CA that the specs mount as
// OSAC_BILLING_CA_FILE. The cluster must reach the stub, which depends on the environment, so the specs are skipped
// unless both variables are set:
//
//	OSAC_IT_BILLING_STUB_LISTEN  address to bind, such as 0.0.0.0:18443
//	OSAC_IT_BILLING_STUB_URL     URL of that listener as seen from the cluster, such as https://172.18.0.1:18443
//
// They restart the shared grpc-server, so they must not overlap other specs.
var _ = Describe("Billing provider status with a stub M360", Label("billing", "billing-configured"), Serial, func() {
	const (
		namespace          = "osac"
		deployment         = "fulfillment-grpc-server"
		container          = "grpc-server"
		credentialFile     = "/etc/fulfillment-grpc-server/billing/credential"
		testStubCredential = "stub-token"
	)

	var (
		stub           *billingstub.Stub
		stubURL        string
		accounts       []billingfake.AccountSpec
		correlationIDs []string
	)

	// rollout waits until the deployment's current generation is fully rolled out.
	rollout := func(ctx context.Context) {
		GinkgoHelper()
		Eventually(ctx, func(g Gomega) {
			current := &appsv1.Deployment{}
			g.Expect(tool.KubeClient().Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: deployment},
				current)).To(Succeed())
			g.Expect(current.Status.ObservedGeneration).To(BeNumerically(">=", current.Generation))
			g.Expect(current.Status.UpdatedReplicas).To(Equal(*current.Spec.Replicas))
			g.Expect(current.Status.Replicas).To(Equal(*current.Spec.Replicas))
			g.Expect(current.Status.AvailableReplicas).To(Equal(*current.Spec.Replicas))
		}, 3*time.Minute, time.Second).Should(Succeed())
	}

	status := func(ctx context.Context) (*publicv1.BillingProviderStatus, error) {
		client := publicv1.NewBillingProviderStatusesClient(tool.ExternalView().AdminConn())
		response, err := client.Get(ctx, publicv1.BillingProviderStatusesGetRequest_builder{}.Build())
		return response.GetObject(), err
	}

	// expectCleanLog checks the grpc-server log of every current pod: the provider was configured, and neither the
	// credential, the base URL nor the credential path leaked into it.
	expectCleanLog := func(ctx context.Context) {
		GinkgoHelper()
		current := &appsv1.Deployment{}
		Expect(tool.KubeClient().Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: deployment},
			current)).To(Succeed())
		pods := &corev1.PodList{}
		Expect(tool.KubeClient().List(ctx, pods, crclient.InNamespace(namespace),
			crclient.MatchingLabels(current.Spec.Selector.MatchLabels))).To(Succeed())
		checked := 0
		for _, pod := range pods.Items {
			// A pod of the previous revision can still be terminating after the rollout.
			if pod.DeletionTimestamp != nil {
				continue
			}
			raw, err := tool.KubeClientSet().CoreV1().Pods(namespace).GetLogs(pod.Name,
				&corev1.PodLogOptions{Container: container}).DoRaw(ctx)
			Expect(err).ToNot(HaveOccurred())
			var entries []map[string]any
			for _, line := range strings.Split(string(raw), "\n") {
				var entry map[string]any
				if json.Unmarshal([]byte(line), &entry) == nil {
					entries = append(entries, entry)
				}
			}
			Expect(entries).To(ContainElement(SatisfyAll(HaveKeyWithValue("msg", "Billing provider configured"),
				HaveKeyWithValue("provider", "m360"))), "pod %s", pod.Name)
			absentValues := []string{"configured but unusable", "failed to build", testStubCredential, stubURL, credentialFile}
			for _, account := range accounts {
				absentValues = append(absentValues, account.ID)
				if account.ExternalID != "" {
					absentValues = append(absentValues, account.ExternalID)
				}
			}
			absentValues = append(absentValues, correlationIDs...)
			for _, absent := range absentValues {
				Expect(string(raw)).ToNot(ContainSubstring(absent), "pod %s", pod.Name)
			}
			checked++
		}
		Expect(checked).To(BeNumerically(">", 0))
	}

	BeforeEach(func(ctx context.Context) {
		listen := os.Getenv("OSAC_IT_BILLING_STUB_LISTEN")
		stubURL = os.Getenv("OSAC_IT_BILLING_STUB_URL")
		if listen == "" || stubURL == "" {
			Skip("OSAC_IT_BILLING_STUB_LISTEN and OSAC_IT_BILLING_STUB_URL are not set")
		}
		parsed, err := url.Parse(stubURL)
		Expect(err).ToNot(HaveOccurred())
		kube := tool.KubeClient()
		// Discovery retains OSAC account/history rows. Never reuse a provider reference, display name, or tenant
		// between specs or reruns, including when the same database is reused.
		suffix := uuid.New()
		correlationIDs = nil
		accounts = []billingfake.AccountSpec{
			{ID: uuid.New(), Name: "billing-stub-first-" + suffix, ExternalID: "billing-correlation-" + suffix,
				RawStatus: "Active", ApprovalStatus: "Approved"},
			// M360's substring search returns both accounts for the first value. Only one is an exact match.
			{ID: uuid.New(), Name: "billing-stub-second-" + suffix, ExternalID: "billing-correlation-" + suffix + "-other",
				RawStatus: "Active", ApprovalStatus: "Approved"},
		}
		stub = billingstub.New(billingfake.Scenario{Accounts: accounts})
		caPEM, err := stub.StartTLS(listen, parsed.Hostname())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(stub.Close)

		// createSecret creates a Secret that is deleted on cleanup.
		createSecret := func(name string, data map[string][]byte) {
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}, Data: data}
			Expect(kube.Create(ctx, secret)).To(Succeed())
			DeferCleanup(func(ctx context.Context) {
				Expect(crclient.IgnoreNotFound(kube.Delete(ctx, secret))).To(Succeed())
			})
		}
		secretName, caSecretName := "billing-stub-credential-"+suffix, "billing-stub-ca-"+suffix
		createSecret(secretName, map[string][]byte{"token": []byte(testStubCredential)})
		createSecret(caSecretName, map[string][]byte{"ca.crt": caPEM})

		key := crclient.ObjectKey{Namespace: namespace, Name: deployment}
		current := &appsv1.Deployment{}
		Expect(kube.Get(ctx, key, current)).To(Succeed())
		original := current.Spec.Template.Spec.DeepCopy()
		// Restore the original pod spec so later specs see a deployment without a billing provider.
		DeferCleanup(func(ctx context.Context) {
			latest := &appsv1.Deployment{}
			Expect(kube.Get(ctx, key, latest)).To(Succeed())
			latest.Spec.Template.Spec = *original
			Expect(kube.Update(ctx, latest)).To(Succeed())
			rollout(ctx)
			// Deployment availability doesn't mean the ingress has refreshed its headless-service DNS endpoints.
			Eventually(ctx, func(g Gomega) {
				object, err := status(ctx)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(object.GetState()).To(Equal(publicv1.BillingProviderState_BILLING_PROVIDER_STATE_NOT_CONFIGURED))
			}, time.Minute, time.Second).Should(Succeed())
		})

		// Hand-patched copies of the chart's mount paths and file names: this mirrors the chart and does not verify it.
		podSpec := &current.Spec.Template.Spec
		podSpec.Volumes = append(podSpec.Volumes,
			corev1.Volume{Name: "billing-credential", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName: secretName, Items: []corev1.KeyToPath{{Key: "token", Path: "credential"}}}}},
			corev1.Volume{Name: "billing-ca", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
				SecretName: caSecretName}}})
		for i := range podSpec.Containers {
			c := &podSpec.Containers[i]
			if c.Name != container {
				continue
			}
			c.VolumeMounts = append(c.VolumeMounts,
				corev1.VolumeMount{Name: "billing-credential", MountPath: "/etc/fulfillment-grpc-server/billing",
					ReadOnly: true},
				corev1.VolumeMount{Name: "billing-ca", MountPath: "/etc/fulfillment-grpc-server/billing-ca",
					ReadOnly: true})
			c.Env = append(c.Env,
				corev1.EnvVar{Name: "OSAC_BILLING_PROVIDER", Value: "m360"},
				// The base URL carries the whole path to the provider API; the stub speaks the M360 one.
				corev1.EnvVar{Name: "OSAC_BILLING_BASE_URL", Value: stubURL + "/api/v1/external"},
				corev1.EnvVar{Name: "OSAC_BILLING_CREDENTIAL_FILE", Value: credentialFile},
				corev1.EnvVar{Name: "OSAC_BILLING_CA_FILE", Value: "/etc/fulfillment-grpc-server/billing-ca/ca.crt"},
				// The chart's tuning defaults, which it always renders with a provider.
				corev1.EnvVar{Name: "OSAC_BILLING_RETRY_COUNT", Value: "3"},
				corev1.EnvVar{Name: "OSAC_BILLING_PRICING_BREAKER_FAILURE_THRESHOLD", Value: "5"},
				corev1.EnvVar{Name: "OSAC_BILLING_PRICING_BREAKER_FAILURE_WINDOW_SECONDS", Value: "30"},
				corev1.EnvVar{Name: "OSAC_BILLING_PRICING_BREAKER_OPEN_SECONDS", Value: "30"},
			)
		}
		Expect(kube.Update(ctx, current)).To(Succeed())
		rollout(ctx)
	})

	// Each List reaches the deployed public service; no database fixture inserts or fake adapter are involved.
	findAccount := func(ctx context.Context, name string) *publicv1.BillingAccount {
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
		}, time.Minute, time.Second).Should(Succeed())
		Expect(account.GetProviderKey()).To(Equal("m360"))
		Expect(account.GetState()).To(Equal(publicv1.BillingAccountState_BILLING_ACCOUNT_STATE_ACTIVE))
		Expect(account.GetStatusObservedAt()).ToNot(BeNil())
		if account.GetCorrelationId() != "" {
			correlationIDs = append(correlationIDs, account.GetCorrelationId())
		}
		return account
	}

	newTenant := func(ctx context.Context) string {
		GinkgoHelper()
		client := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		name := "billing-stub-" + uuid.New()
		response, err := client.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{Metadata: privatev1.Metadata_builder{Name: name}.Build()}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := response.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			// Billed tenants intentionally remain identity tombstones. Delete through the API (not direct SQL)
			// and wait for the controller to finish cleanup; do not require their retained history to disappear.
			Eventually(ctx, func(g Gomega) {
				_, err := client.Delete(ctx, privatev1.TenantsDeleteRequest_builder{Id: id}.Build())
				if grpcstatus.Code(err) == grpccodes.NotFound {
					return
				}
				g.Expect(err).ToNot(HaveOccurred())
			}, time.Minute, time.Second).Should(Succeed())
			Eventually(ctx, func(g Gomega) {
				response, err := client.Get(ctx, privatev1.TenantsGetRequest_builder{Id: id}.Build())
				if grpcstatus.Code(err) == grpccodes.NotFound {
					return
				}
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(response.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
				g.Expect(response.GetObject().GetMetadata().GetFinalizers()).To(BeEmpty())
			}, 3*time.Minute, time.Second).Should(Succeed())
		})
		return name // Tenant references are tenant names, not object UUIDs.
	}

	// Prove assignment-time resolution reads the selected account and then scans for the exact correlation value.
	// M360 has no correlation-write operation: discovery, assignment, and remediation must all remain read-only.
	expectReadOnlyResolution := func(nativeID, value string, since int) {
		GinkgoHelper()
		requests := stub.Requests()[since:]
		path := "/api/v1/external/accounts/" + nativeID
		readAccount, scanned := false, false
		for _, request := range requests {
			if request.Failed {
				continue
			}
			if request.Method == "GET" && request.Op == "GetAccount" && request.Path == path {
				readAccount = true
			}
			if readAccount && request.Method == "GET" && request.Op == "ListAccounts" &&
				request.Path == "/api/v1/external/accounts" {
				query, err := url.ParseQuery(request.Query)
				Expect(err).ToNot(HaveOccurred())
				if query.Get("q") == value {
					scanned = true
				}
			}
		}
		Expect(value).ToNot(BeEmpty())
		Expect(readAccount).To(BeTrue(), "resolution must read the selected account through the real adapter")
		Expect(scanned).To(BeTrue(), "resolution must scan provider accounts for the exact correlation value")
		Expect(stub.Writes()).To(BeEmpty(), "OSAC must never attempt an M360 account PUT or any other provider write")
	}

	It("discovers existing correlations and connects Create and reassignment Update using exact-match reads only", func(ctx context.Context) {
		first := findAccount(ctx, accounts[0].Name)
		second := findAccount(ctx, accounts[1].Name)
		Expect(first.GetId()).ToNot(BeEmpty())
		Expect(second.GetId()).ToNot(BeEmpty())
		Expect(first.GetId()).ToNot(BeElementOf(accounts[0].ID, accounts[1].ID))
		Expect(second.GetId()).ToNot(BeElementOf(accounts[0].ID, accounts[1].ID, first.GetId()))
		Expect(first.GetCorrelationId()).To(Equal(accounts[0].ExternalID))
		Expect(second.GetCorrelationId()).To(Equal(accounts[1].ExternalID))
		Expect(stub.Succeeded("ListAccounts")).To(BeNumerically(">", 0))
		Expect(stub.Writes()).To(BeEmpty(), "discovery must not perform account writes")
		tenant := newTenant(ctx)
		client := publicv1.NewTenantBillingAssignmentsClient(tool.ExternalView().AdminConn())
		beforeCreate := len(stub.Requests())
		created, err := client.Create(ctx, publicv1.TenantBillingAssignmentsCreateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Metadata: publicv1.Metadata_builder{Name: "billing-" + uuid.New()}.Build(),
				Spec:     publicv1.TenantBillingAssignmentSpec_builder{TenantId: tenant, BillingAccountId: first.GetId()}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		assignment := created.GetObject()
		Expect(assignment.GetId()).ToNot(BeEmpty())
		Expect(assignment.GetSpec().GetTenantId()).To(Equal(tenant))
		Expect(assignment.GetSpec().GetBillingAccountId()).To(Equal(first.GetId()))
		Expect(assignment.GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
		Expect(assignment.GetStatus().GetMessage()).To(BeEmpty())
		Expect(assignment.GetStatus().GetEffectiveFrom()).ToNot(BeNil())
		Expect(assignment.GetStatus().GetEffectiveTo()).To(BeNil())
		expectReadOnlyResolution(accounts[0].ID, first.GetCorrelationId(), beforeCreate)
		refreshed := findAccount(ctx, accounts[0].Name)
		Expect(refreshed.GetId()).To(Equal(first.GetId()))
		Expect(refreshed.GetCorrelationId()).To(Equal(accounts[0].ExternalID))
		Expect(first.GetCorrelationId()).ToNot(BeElementOf(tenant, accounts[0].ID, first.GetId()))

		beforeUpdate := len(stub.Requests())
		updated, err := client.Update(ctx, publicv1.TenantBillingAssignmentsUpdateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Id:   assignment.GetId(),
				Spec: publicv1.TenantBillingAssignmentSpec_builder{TenantId: tenant, BillingAccountId: second.GetId()}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetId()).ToNot(Equal(assignment.GetId()))
		Expect(updated.GetObject().GetSpec().GetTenantId()).To(Equal(tenant))
		Expect(updated.GetObject().GetSpec().GetBillingAccountId()).To(Equal(second.GetId()))
		Expect(updated.GetObject().GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
		Expect(updated.GetObject().GetStatus().GetMessage()).To(BeEmpty())
		Expect(updated.GetObject().GetStatus().GetEffectiveFrom()).ToNot(BeNil())
		Expect(updated.GetObject().GetStatus().GetEffectiveTo()).To(BeNil())
		expectReadOnlyResolution(accounts[1].ID, second.GetCorrelationId(), beforeUpdate)
		refreshed = findAccount(ctx, accounts[1].Name)
		Expect(refreshed.GetId()).To(Equal(second.GetId()))
		Expect(refreshed.GetCorrelationId()).To(Equal(accounts[1].ExternalID))
		Expect(second.GetCorrelationId()).ToNot(BeElementOf(tenant, accounts[1].ID, second.GetId(), first.GetCorrelationId()))
		old, err := client.Get(ctx, publicv1.TenantBillingAssignmentsGetRequest_builder{Id: assignment.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(old.GetObject().GetSpec().GetBillingAccountId()).To(Equal(first.GetId()))
		Expect(old.GetObject().GetStatus().GetEffectiveFrom()).To(Equal(assignment.GetStatus().GetEffectiveFrom()))
		Expect(old.GetObject().GetStatus().GetEffectiveTo()).To(Equal(updated.GetObject().GetStatus().GetEffectiveFrom()))
		history, err := client.List(ctx, publicv1.TenantBillingAssignmentsListRequest_builder{
			Filter: new(fmt.Sprintf("this.spec.tenant_id == %q", tenant)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(history.GetItems()).To(HaveLen(2), "reassignment must retain both contiguous intervals")
		active := 0
		for _, interval := range history.GetItems() {
			if interval.GetStatus().GetEffectiveTo() == nil {
				active++
				Expect(interval.GetId()).To(Equal(updated.GetObject().GetId()))
			}
		}
		Expect(active).To(Equal(1), "exactly one interval must remain active after reassignment")
		Expect(stub.Writes()).To(BeEmpty())
		expectCleanLog(ctx)
	})

	It("persists PENDING without a provider write and connects after external-admin remediation and Update", func(ctx context.Context) {
		accounts[0].ExternalID = ""
		stub.SetScenario(billingfake.Scenario{Accounts: accounts})
		account := findAccount(ctx, accounts[0].Name)
		Expect(account.GetCorrelationId()).To(BeEmpty())
		tenant := newTenant(ctx)
		client := publicv1.NewTenantBillingAssignmentsClient(tool.ExternalView().AdminConn())
		beforeCreate := len(stub.Requests())
		created, err := client.Create(ctx, publicv1.TenantBillingAssignmentsCreateRequest_builder{
			Object: publicv1.TenantBillingAssignment_builder{
				Metadata: publicv1.Metadata_builder{Name: "billing-" + uuid.New()}.Build(),
				Spec:     publicv1.TenantBillingAssignmentSpec_builder{TenantId: tenant, BillingAccountId: account.GetId()}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		pending := created.GetObject()
		Expect(pending.GetId()).ToNot(BeEmpty())
		Expect(pending.GetSpec().GetTenantId()).To(Equal(tenant))
		Expect(pending.GetSpec().GetBillingAccountId()).To(Equal(account.GetId()))
		Expect(pending.GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_PENDING))
		remediation := billing.ReasonProviderCorrelationWriteUnsupported +
			": set externalId on the account in the provider to the correlation_id shown by BillingAccounts.List"
		Expect(pending.GetStatus().GetMessage()).To(Equal(remediation))
		Expect(pending.GetStatus().GetEffectiveFrom()).ToNot(BeNil())
		Expect(pending.GetStatus().GetEffectiveTo()).To(BeNil())
		Expect(stub.Writes()).To(BeEmpty(), "unsupported capability must not attempt even a failed PUT")
		accountID := account.GetId()
		account = findAccount(ctx, accounts[0].Name)
		Expect(account.GetId()).To(Equal(accountID))
		Expect(account.GetCorrelationId()).ToNot(BeEmpty(), "PENDING must retain the OSAC-side correlation")
		Expect(account.GetCorrelationId()).ToNot(BeElementOf(tenant, accounts[0].ID, accountID, accounts[1].ExternalID))
		expectReadOnlyResolution(accounts[0].ID, account.GetCorrelationId(), beforeCreate)
		persisted, err := client.Get(ctx, publicv1.TenantBillingAssignmentsGetRequest_builder{Id: pending.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(persisted.GetObject().GetStatus().GetConnectionState()).To(Equal(pending.GetStatus().GetConnectionState()))
		Expect(persisted.GetObject().GetStatus().GetMessage()).To(Equal(remediation))
		Expect(persisted.GetObject().GetSpec().GetBillingAccountId()).To(Equal(accountID))

		// A retry without provider-side remediation stays PENDING and must not rotate the OSAC correlation or interval.
		beforeRetry := len(stub.Requests())
		retried, err := client.Update(ctx, publicv1.TenantBillingAssignmentsUpdateRequest_builder{Object: pending}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(retried.GetObject().GetId()).To(Equal(pending.GetId()))
		Expect(retried.GetObject().GetStatus().GetConnectionState()).To(Equal(pending.GetStatus().GetConnectionState()))
		Expect(retried.GetObject().GetStatus().GetMessage()).To(Equal(remediation))
		Expect(retried.GetObject().GetStatus().GetEffectiveFrom()).To(Equal(pending.GetStatus().GetEffectiveFrom()))
		Expect(retried.GetObject().GetStatus().GetEffectiveTo()).To(BeNil())
		expectReadOnlyResolution(accounts[0].ID, account.GetCorrelationId(), beforeRetry)
		Expect(findAccount(ctx, accounts[0].Name).GetCorrelationId()).To(Equal(account.GetCorrelationId()))

		// Simulate the external provider admin, not an OSAC API write. Preserve every native ID, name and state;
		// only this account's provider-side correlation changes. SetScenario keeps the HTTP request log intact.
		accounts[0].ExternalID = account.GetCorrelationId()
		stub.SetScenario(billingfake.Scenario{Accounts: accounts})
		persisted, err = client.Get(ctx, publicv1.TenantBillingAssignmentsGetRequest_builder{Id: pending.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(persisted.GetObject().GetStatus().GetConnectionState()).To(Equal(pending.GetStatus().GetConnectionState()),
			"provider remediation alone must not replace assignment-time revalidation")
		beforeUpdate := len(stub.Requests())
		updated, err := client.Update(ctx, publicv1.TenantBillingAssignmentsUpdateRequest_builder{Object: pending}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
		Expect(updated.GetObject().GetStatus().GetMessage()).To(BeEmpty())
		Expect(updated.GetObject().GetId()).To(Equal(pending.GetId()))
		Expect(updated.GetObject().GetSpec().GetBillingAccountId()).To(Equal(accountID))
		Expect(updated.GetObject().GetStatus().GetEffectiveFrom()).To(Equal(pending.GetStatus().GetEffectiveFrom()))
		Expect(updated.GetObject().GetStatus().GetEffectiveTo()).To(BeNil())
		expectReadOnlyResolution(accounts[0].ID, account.GetCorrelationId(), beforeUpdate)
		Expect(findAccount(ctx, accounts[0].Name).GetCorrelationId()).To(Equal(account.GetCorrelationId()))
		persisted, err = client.Get(ctx, publicv1.TenantBillingAssignmentsGetRequest_builder{Id: pending.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(persisted.GetObject().GetStatus().GetConnectionState()).To(Equal(
			publicv1.TenantBillingAssignmentConnectionState_TENANT_BILLING_ASSIGNMENT_CONNECTION_STATE_CONNECTED))
		Expect(persisted.GetObject().GetStatus().GetMessage()).To(BeEmpty())
		history, err := client.List(ctx, publicv1.TenantBillingAssignmentsListRequest_builder{
			Filter: new(fmt.Sprintf("this.spec.tenant_id == %q", tenant)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(history.GetItems()).To(HaveLen(1), "same-account revalidation must not create another interval")
		Expect(history.GetItems()[0].GetId()).To(Equal(pending.GetId()))
		Expect(stub.Writes()).To(BeEmpty())
		expectCleanLog(ctx)
	})

	It("is AVAILABLE when the stub provider answers, using reads only", func(ctx context.Context) {
		Eventually(ctx, func(g Gomega) {
			object, err := status(ctx)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(object.GetState()).To(Equal(publicv1.BillingProviderState_BILLING_PROVIDER_STATE_AVAILABLE))
		}, time.Minute, time.Second).Should(Succeed())
		Expect(stub.Succeeded("HealthCheck")).To(BeNumerically(">", 0))
		// The pod sends the mounted credential to the configured API path.
		Expect(stub.Requests()).To(ContainElement(SatisfyAll(HaveField("Op", "HealthCheck"),
			HaveField("Path", HavePrefix("/api/v1/external/")), HaveField("Authorization", "Bearer "+testStubCredential))))
		Expect(stub.Writes()).To(BeEmpty())
		expectCleanLog(ctx)
	})

	It("is UNAVAILABLE when the stub provider fails its health check", func(ctx context.Context) {
		stub.SetScenario(billingfake.Scenario{Failures: map[string]billingfake.Failure{
			"HealthCheck": {Reason: billing.ReasonUnavailable, Status: 503}}})
		Eventually(ctx, func(g Gomega) {
			object, err := status(ctx)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(object.GetState()).To(Equal(publicv1.BillingProviderState_BILLING_PROVIDER_STATE_UNAVAILABLE))
			g.Expect(object.GetMessage()).To(Equal("billing provider account API is unavailable"))
		}, time.Minute, time.Second).Should(Succeed())
		// A failed HealthCheck the stub served proves the probe reached it, not a config or TLS rejection.
		Expect(stub.Requests()).To(ContainElement(SatisfyAll(HaveField("Op", "HealthCheck"),
			HaveField("Failed", true))))
		Expect(stub.Writes()).To(BeEmpty())
		expectCleanLog(ctx)
	})
})
