/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func sharedTridentBackend() *privatev1.StorageBackendsGetResponse {
	response := newTestStorageBackendGetResponseWithSecret("ontap", "unused-discovery-password")
	response.GetObject().GetSpec().SetOntap(privatev1.OntapBackendConfig_builder{
		Profiles: []*privatev1.OntapBackendProfile{privatev1.OntapBackendProfile_builder{
			Name: "shared-fc", Shared: privatev1.SharedTridentBackend_builder{TbcNamespace: "trident", TbcName: "customer-fc"}.Build(),
		}.Build()},
	}.Build())
	return response
}

func sharedTridentTier(name, performance string) *privatev1.StorageTier {
	obj := newTestStorageTier(name, "backend-1")
	obj.GetSpec().SetProtocol(privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK)
	assoc := obj.GetSpec().GetBackends()[0]
	assoc.SetMaxReadBandwidthMbs(0)
	assoc.SetMaxWriteBandwidthMbs(0)
	assoc.SetOntap(privatev1.OntapAssociationConfig_builder{Profile: "shared-fc", PoolSelector: map[string]string{"performance": performance}}.Build())
	return obj
}

var _ = Describe("Shared Trident tier resolution", func() {
	DescribeTable("retains healthy tiers when another shared configuration is invalid", func(problem string, invalidFirst bool) {
		healthy := sharedTridentTier("healthy", "extreme")
		invalid := sharedTridentTier("invalid", "value")
		invalidBackend := sharedTridentBackend()
		switch problem {
		case "profile":
			invalid.GetSpec().GetBackends()[0].GetOntap().SetProfile("missing")
		case "selector":
			invalid.GetSpec().GetBackends()[0].GetOntap().SetPoolSelector(nil)
		case "mode":
			invalid.GetSpec().GetBackends()[0].SetBackendId("backend-2")
			invalidBackend.GetObject().GetSpec().GetOntap().GetProfiles()[0].ClearShared()
		}
		items := []*privatev1.StorageTier{healthy, invalid}
		if invalidFirst {
			items[0], items[1] = items[1], items[0]
		}
		tiers := &mockStorageTiersLister{listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
			return privatev1.StorageTiersListResponse_builder{Items: items}.Build(), nil
		}}
		backends := &mockStorageBackendsClient{getFunc: func(_ context.Context, req *privatev1.StorageBackendsGetRequest, _ ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
			if req.GetId() == "backend-2" {
				return invalidBackend, nil
			}
			return sharedTridentBackend(), nil
		}}
		resolved, definitions, err := resolveAndInjectTierContext(context.Background(), tiers, backends, nil, "tenant", "tenant-a")
		Expect(err).To(HaveOccurred(), "tenant onboarding must still wait for a complete configuration")
		Expect(definitions).To(HaveLen(1))
		Expect(definitions[0].Name).To(Equal("healthy"))
		Expect(provisioning.StorageTierDefinitionsFromContext(resolved)).To(Equal(definitions))
		connections := provisioning.StorageBackendConnectionsFromContext(resolved)
		Expect(connections).To(HaveKey("backend-1"))
		Expect(connections).NotTo(HaveKey("backend-2"))
		expectedLookups := 1
		if problem == "mode" {
			expectedLookups = 2
		}
		Expect(backends.getCallCount).To(Equal(expectedLookups))
	},
		Entry("unknown profile after healthy tier", "profile", false),
		Entry("unknown profile before healthy tier", "profile", true),
		Entry("empty selector after healthy tier", "selector", false),
		Entry("empty selector before healthy tier", "selector", true),
		Entry("unsupported mode after healthy tier", "mode", false),
		Entry("unsupported mode before healthy tier", "mode", true),
	)
})

var _ = Describe("Shared Trident onboarding", func() {
	ctx := context.Background()
	var r *StorageReconciler
	var tenant *v1alpha1.Tenant
	var calls int
	var catalog []*privatev1.StorageTier
	BeforeEach(func() {
		name := fmt.Sprintf("shared-trident-%d", time.Now().UnixNano())
		createReadyTenantForStorage(ctx, name, "default")
		tenant = prepareStorageTenant(ctx, name)
		calls = 0
		provider := &mockProvisioningProvider{triggerProvisionFunc: func(ctx context.Context, _ client.Object) (*provisioning.ProvisionResult, error) {
			calls++
			connections := provisioning.StorageBackendConnectionsFromContext(ctx)
			Expect(connections).To(HaveLen(1))
			connection := connections["backend-1"]
			Expect(connection.Endpoint).To(BeEmpty())
			Expect(connection.Username).To(BeEmpty())
			Expect(connection.Password).To(BeEmpty())
			Expect(connection.TenantPreparation).To(BeNil())
			return &provisioning.ProvisionResult{JobID: fmt.Sprint(calls), InitialState: v1alpha1.JobStatePending}, nil
		}}
		r = NewStorageReconciler(testMcManager, "default", mcmanager.LocalCluster, provider, provider, time.Second, provisioning.DefaultMaxJobHistory)
		r.APIReader = preparationDeniedReader{r.APIReader}
		catalog = []*privatev1.StorageTier{sharedTridentTier("fast", "extreme"), sharedTridentTier("standard", "value")}
		r.TiersClient = &mockStorageTiersLister{listFunc: func(context.Context, *privatev1.StorageTiersListRequest, ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error) {
			return privatev1.StorageTiersListResponse_builder{Items: catalog}.Build(), nil
		}}
		backends := registeredBackendsClient(1)
		backends.getFunc = func(context.Context, *privatev1.StorageBackendsGetRequest, ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error) {
			return sharedTridentBackend(), nil
		}
		r.BackendsClient = backends
		r.SecretsClient = &mockSecretsClient{getFunc: func(context.Context, *privatev1.SecretsGetRequest, ...grpc.CallOption) (*privatev1.SecretsGetResponse, error) {
			Fail("shared onboarding must not fetch unused ONTAP passwords")
			return nil, nil
		}}
	})
	reconcile := func() {
		GinkgoHelper()
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(tenant), tenant)).To(Succeed())
		Eventually(func(g Gomega) {
			cached := &v1alpha1.Tenant{}
			g.Expect(r.Get(ctx, client.ObjectKeyFromObject(tenant), cached)).To(Succeed())
			g.Expect(cached.ResourceVersion).To(Equal(tenant.ResourceVersion))
		}, 5*time.Second, 10*time.Millisecond).Should(Succeed())
		_, err := r.Reconcile(ctx, storageReconcileRequest(client.ObjectKeyFromObject(tenant)))
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(tenant), tenant)).To(Succeed())
	}
	It("starts native backend verification without preparation or discovery password reads", func() {
		reconcile()
		Expect(calls).To(Equal(1))
		Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady).Status).To(Equal(metav1.ConditionFalse))
		Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady).Reason).To(Equal("BackendSetupPending"))
		reconcile()
		Expect(calls).To(Equal(1))
	})
	It("requires both current-configuration jobs and tenant classes before readiness", func() {
		createLabeledStorageClass(ctx, tenant.Name+"-fast", tenant.Name, "fast")
		createLabeledStorageClass(ctx, tenant.Name+"-standard", tenant.Name, "standard")
		for range 5 {
			reconcile()
		}
		Expect(calls).To(Equal(2))
		for _, typ := range []v1alpha1.TenantConditionType{v1alpha1.TenantConditionStorageBackendReady, v1alpha1.TenantConditionClusterStorageReady} {
			Expect(tenant.GetStatusCondition(typ).Status).To(Equal(metav1.ConditionTrue))
			Expect(tenant.GetStatusCondition(typ).ObservedGeneration).To(Equal(tenant.Generation))
		}
		catalog[0].GetSpec().GetBackends()[0].GetOntap().SetPoolSelector(map[string]string{"performance": "highwrite"})
		reconcile()
		Expect(calls).To(Equal(3))
		Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady).Status).To(Equal(metav1.ConditionFalse))
	})
	It("does not provision with an unresolved profile", func() {
		catalog[0].GetSpec().GetBackends()[0].GetOntap().SetProfile("missing")
		reconcile()
		Expect(calls).To(BeZero())
		Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady).Reason).To(Equal("ConfigurationUnavailable"))
	})
	It("backs off repeated successful repairs and discovers late classes after a restart", func() {
		for range 4 {
			reconcile()
		}
		Expect(calls).To(Equal(2))
		reconcile()
		Expect(calls).To(Equal(2))
		Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady).Reason).To(Equal("StorageClassRepairPending"))

		// Expire the persisted launch cooldown without sleeping.
		tenant.Status.ClusterStorageJobs[0].Timestamp = metav1.NewTime(time.Now().Add(-provisioning.BackoffBaseDelay - time.Second))
		Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())
		reconcile()
		Expect(calls).To(Equal(3))
		reconcile() // Observe the repair's successful completion.
		reconcile()
		Expect(calls).To(Equal(3), "a successful repair without classes must also back off")

		restarted := NewStorageReconciler(testMcManager, "default", mcmanager.LocalCluster,
			r.BackendProvider, r.ClusterStorageProvider, r.StatusPollInterval, r.MaxJobHistory)
		restarted.TiersClient, restarted.BackendsClient, restarted.SecretsClient = r.TiersClient, r.BackendsClient, r.SecretsClient
		restarted.APIReader = r.APIReader
		r = restarted
		reconcile()
		Expect(calls).To(Equal(3), "restart must retain the cooldown from persisted job history")

		createLabeledStorageClass(ctx, tenant.Name+"-fast", tenant.Name, "fast")
		createLabeledStorageClass(ctx, tenant.Name+"-standard", tenant.Name, "standard")
		Eventually(func(g Gomega) {
			reconcile()
			g.Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady).Status).To(Equal(metav1.ConditionTrue))
		}, 5*time.Second, 10*time.Millisecond).Should(Succeed())
		Expect(calls).To(Equal(3), "late class discovery must recover without waiting for another job")
	})
	It("starts a changed configuration immediately during a repair cooldown", func() {
		for range 5 {
			reconcile()
		}
		Expect(calls).To(Equal(2))
		catalog[0].GetSpec().GetBackends()[0].GetOntap().SetPoolSelector(map[string]string{"performance": "highwrite"})
		reconcile()
		Expect(calls).To(Equal(3))
		Expect(tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady).Reason).To(Equal("BackendSetupPending"))
	})
	It("retains the finalizer until class cleanup then backend cleanup complete", func() {
		reconcile()
		var order []string
		provider := func(stage string) *mockProvisioningProvider {
			return &mockProvisioningProvider{
				triggerDeprovisionFunc: func(ctx context.Context, _ client.Object, _ []v1alpha1.JobStatus) (*provisioning.DeprovisionResult, error) {
					connection := provisioning.StorageBackendConnectionsFromContext(ctx)["backend-1"]
					Expect(connection.ProviderConfig).To(HaveKey("profiles"))
					Expect(connection.Password).To(BeEmpty())
					Expect(connection.TenantPreparation).To(BeNil())
					order = append(order, stage)
					return &provisioning.DeprovisionResult{Action: provisioning.DeprovisionTriggered, JobID: stage}, nil
				},
				getDeprovisionStatusFunc: func(context.Context, client.Object, string) (provisioning.ProvisionStatus, error) {
					return provisioning.ProvisionStatus{State: v1alpha1.JobStateSucceeded}, nil
				},
			}
		}
		r.ClusterStorageProvider = provider("classes")
		r.BackendProvider = provider("backend")
		key := client.ObjectKeyFromObject(tenant)
		Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())
		reconcile()
		Expect(order).To(Equal([]string{"classes"}))
		Expect(tenant.Finalizers).To(ContainElement(storageFinalizer))
		Eventually(func(g Gomega) {
			_, err := r.Reconcile(ctx, storageReconcileRequest(key))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, &v1alpha1.Tenant{}))).To(BeTrue())
		}, 5*time.Second, 20*time.Millisecond).Should(Succeed())
		Expect(order).To(Equal([]string{"classes", "backend"}))
	})
})
