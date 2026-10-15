package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestExternalIPAllocationPublishesOnlyValidatedResults(t *testing.T) {
	cases := []struct {
		name               string
		address            string
		annotationAddress  string
		generation         int64
		operation          string
		includeAddressData bool
		mutateInputs       func(map[string]any)
		wantAccept         bool
	}{
		{name: "valid allocation without annotation", address: "198.51.100.1", generation: 2, operation: "external_ip.allocate", includeAddressData: true, wantAccept: true},
		{name: "artifact wins over conflicting annotation", address: "198.51.100.1", annotationAddress: "198.51.100.2", generation: 2, operation: "external_ip.allocate", includeAddressData: true, wantAccept: true},
		{name: "annotation cannot replace missing artifact address", annotationAddress: "198.51.100.1", generation: 2, operation: "external_ip.allocate"},
		{name: "out of pool", address: "203.0.113.1", generation: 2, operation: "external_ip.allocate", includeAddressData: true},
		{name: "noncanonical address", address: "198.51.100.01", generation: 2, operation: "external_ip.allocate", includeAddressData: true},
		{name: "stale generation", address: "198.51.100.1", generation: 1, operation: "external_ip.allocate", includeAddressData: true},
		{name: "wrong operation", address: "198.51.100.1", generation: 2, operation: "external_ip.release", includeAddressData: true},
		{
			name: "stale submitted pool UID", address: "198.51.100.1", generation: 2,
			operation: "external_ip.allocate", includeAddressData: true,
			mutateInputs: func(inputs map[string]any) {
				jobVars := inputs["osac_job_vars"].(map[string]any)
				poolSnapshot := jobVars["external_ip_pool"].(map[string]any)
				poolSnapshot["kubernetes_uid"] = "stale-pool-uid"
			},
		},
		{
			name: "stale submitted resource generation", address: "198.51.100.1", generation: 2,
			operation: "external_ip.allocate", includeAddressData: true,
			mutateInputs: func(inputs map[string]any) {
				jobVars := inputs["osac_job_vars"].(map[string]any)
				resource := jobVars["resource"].(map[string]any)
				metadata := resource["metadata"].(map[string]any)
				metadata["generation"] = float64(1)
			},
		},
		{
			name: "missing submitted inputs", address: "198.51.100.1", generation: 2,
			operation: "external_ip.allocate", includeAddressData: true,
			mutateInputs: func(inputs map[string]any) {
				delete(inputs, "osac_job_vars")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := v1alpha1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			pool := &v1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pool",
					Namespace: "networking",
					UID:       types.UID("33333333-3333-4333-8333-333333333333"),
					Labels:    map[string]string{osacExternalIPPoolIDLabel: "22222222-2222-4222-8222-222222222222"},
					Annotations: map[string]string{
						osacTenantKey: "tenant-a",
					},
				},
				Spec:   v1alpha1.ExternalIPPoolSpec{IPFamily: "IPv4", CIDRs: []string{"198.51.100.0/24"}},
				Status: v1alpha1.ExternalIPPoolStatus{Phase: v1alpha1.ExternalIPPoolPhaseReady},
			}
			job := v1alpha1.JobStatus{
				JobID:         "allocation-job",
				Type:          v1alpha1.JobTypeProvision,
				State:         v1alpha1.JobStateRunning,
				ConfigVersion: "current-config",
			}
			current := &v1alpha1.ExternalIP{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "external-ip",
					Namespace:  "networking",
					UID:        types.UID("11111111-1111-4111-8111-111111111111"),
					Generation: 2,
					Labels:     map[string]string{osacExternalIPIDLabel: "44444444-4444-4444-8444-444444444444"},
					Annotations: map[string]string{
						osacTenantKey:                           "tenant-a",
						osacExternalIPContractVersionAnnotation: networkmanager.ExternalIPContractVersionV1,
					},
				},
				Spec: v1alpha1.ExternalIPSpec{Pool: "22222222-2222-4222-8222-222222222222"},
				Status: v1alpha1.ExternalIPStatus{
					DesiredConfigVersion: "current-config",
					ProvisioningJobs:     []v1alpha1.JobStatus{job},
					Phase:                v1alpha1.ExternalIPPhaseProgressing,
					State:                v1alpha1.ExternalIPStatePending,
				},
			}
			if tc.annotationAddress != "" {
				current.Annotations[osacExternalIPAllocatedAddressAnnotation] = tc.annotationAddress
			}
			kubeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(pool, current).
				WithStatusSubresource(&v1alpha1.ExternalIP{}, &v1alpha1.ExternalIPPool{}).
				Build()
			reconciler := &ExternalIPReconciler{APIReader: kubeClient}
			snapshot := current.DeepCopy()
			allocationData := map[string]any{}
			if tc.includeAddressData {
				allocationData["address"] = tc.address
			}
			result := provisioning.ProvisionStatusWithExtraVars{
				ProvisionStatus: provisioning.ProvisionStatus{JobID: "allocation-job", State: v1alpha1.JobStateSucceeded},
				InputExtraVars:  externalIPJobInputsFixture(current, pool),
				ExtraVars: map[string]any{"osac_result": map[string]any{
					"schemaVersion": "v1", "operation": tc.operation,
					"resourceUID": string(current.UID), "observedGeneration": tc.generation,
					"data": allocationData,
				}},
			}
			if tc.mutateInputs != nil {
				tc.mutateInputs(result.InputExtraVars)
			}
			poolContext := provisioning.ExternalIPPoolContext{
				FulfillmentID: "22222222-2222-4222-8222-222222222222",
				KubernetesUID: string(pool.UID), TenantID: "tenant-a", CIDR: "198.51.100.0/24", Phase: "Ready",
			}

			err := reconciler.acceptExternalIPAllocation(context.Background(), snapshot, poolContext, result)
			if tc.wantAccept {
				if err != nil {
					t.Fatalf("valid result rejected: %v", err)
				}
				if snapshot.Status.Address != tc.address || snapshot.Status.State != v1alpha1.ExternalIPStateAllocated || snapshot.Status.Phase != v1alpha1.ExternalIPPhaseReady {
					t.Fatalf("unexpected accepted status: %#v", snapshot.Status)
				}
			} else {
				if err == nil {
					t.Fatal("expected result to be rejected")
				}
				if snapshot.Status.Address != "" || snapshot.Status.State != v1alpha1.ExternalIPStatePending {
					t.Fatalf("rejected result changed allocation status: %#v", snapshot.Status)
				}
			}
			if snapshot.Annotations[osacExternalIPAllocatedAddressAnnotation] != tc.annotationAddress {
				t.Fatalf("allocation acceptance changed the address annotation: %q", snapshot.Annotations[osacExternalIPAllocatedAddressAnnotation])
			}
		})
	}
}

func TestExternalIPReleaseResultRequiresCurrentDeletingResource(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	job := v1alpha1.JobStatus{JobID: "release-job", Type: v1alpha1.JobTypeDeprovision, State: v1alpha1.JobStateRunning}
	makeResource := func(deleting bool) (*v1alpha1.ExternalIP, *v1alpha1.ExternalIPPool, *ExternalIPReconciler) {
		pool := &v1alpha1.ExternalIPPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pool",
				Namespace: "networking",
				UID:       types.UID("33333333-3333-4333-8333-333333333333"),
				Labels:    map[string]string{osacExternalIPPoolIDLabel: "22222222-2222-4222-8222-222222222222"},
				Annotations: map[string]string{
					osacTenantKey: "tenant-a",
				},
			},
			Spec:   v1alpha1.ExternalIPPoolSpec{IPFamily: "IPv4", CIDRs: []string{"198.51.100.0/24"}},
			Status: v1alpha1.ExternalIPPoolStatus{Phase: v1alpha1.ExternalIPPoolPhaseProgressing},
		}
		resource := &v1alpha1.ExternalIP{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "external-ip",
				Namespace:  "networking",
				UID:        types.UID("11111111-1111-4111-8111-111111111111"),
				Generation: 2,
				Labels:     map[string]string{osacExternalIPIDLabel: "44444444-4444-4444-8444-444444444444"},
				Annotations: map[string]string{
					osacTenantKey:                           "tenant-a",
					osacExternalIPContractVersionAnnotation: networkmanager.ExternalIPContractVersionV1,
				},
				Finalizers: []string{"test-finalizer"},
			},
			Spec:   v1alpha1.ExternalIPSpec{Pool: "22222222-2222-4222-8222-222222222222"},
			Status: v1alpha1.ExternalIPStatus{ProvisioningJobs: []v1alpha1.JobStatus{job}},
		}
		if deleting {
			deletionTimestamp := metav1.NewTime(time.Now())
			resource.DeletionTimestamp = &deletionTimestamp
		}
		kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pool, resource).Build()
		return resource, pool, &ExternalIPReconciler{APIReader: kubeClient}
	}
	result := provisioning.ProvisionStatusWithExtraVars{
		ProvisionStatus: provisioning.ProvisionStatus{JobID: "release-job", State: v1alpha1.JobStateSucceeded},
		InputExtraVars:  nil,
		ExtraVars: map[string]any{"osac_result": map[string]any{
			"schemaVersion": "v1", "operation": "external_ip.release",
			"resourceUID": "11111111-1111-4111-8111-111111111111", "observedGeneration": int64(2), "data": map[string]any{},
		}},
	}
	live, livePool, liveReconciler := makeResource(false)
	result.InputExtraVars = externalIPJobInputsFixture(live, livePool)
	if err := liveReconciler.validateExternalIPReleaseResult(context.Background(), live, result); err == nil {
		t.Fatal("release proof for a live ExternalIP must be rejected")
	}
	deleting, deletingPool, deletingReconciler := makeResource(true)
	result.InputExtraVars = externalIPJobInputsFixture(deleting, deletingPool)
	if err := deletingReconciler.validateExternalIPReleaseResult(context.Background(), deleting, result); err != nil {
		t.Fatalf("valid release proof with a non-ready pool rejected: %v", err)
	}
	staleInputs := externalIPJobInputsFixture(deleting, deletingPool)
	jobVars := staleInputs["osac_job_vars"].(map[string]any)
	poolSnapshot := jobVars["external_ip_pool"].(map[string]any)
	poolSnapshot["kubernetes_uid"] = "stale-pool-uid"
	result.InputExtraVars = staleInputs
	if err := deletingReconciler.validateExternalIPReleaseResult(context.Background(), deleting, result); err == nil {
		t.Fatal("release proof with a stale submitted pool UID must be rejected")
	}
}

func externalIPJobInputsFixture(externalIP *v1alpha1.ExternalIP, pool *v1alpha1.ExternalIPPool) map[string]any {
	resourceBytes, err := json.Marshal(externalIP)
	if err != nil {
		panic(err)
	}
	var resource map[string]any
	if err := json.Unmarshal(resourceBytes, &resource); err != nil {
		panic(err)
	}
	return map[string]any{
		"osac_job_vars": map[string]any{
			"resource": resource,
			"external_ip_pool": map[string]any{
				"fulfillment_id": pool.Labels[osacExternalIPPoolIDLabel],
				"kubernetes_uid": string(pool.UID),
				"tenant_id":      pool.Annotations[osacTenantKey],
				"cidr":           pool.Spec.CIDRs[0],
				"phase":          string(pool.Status.Phase),
			},
		},
	}
}

func TestExternalIPManagerContractComesFromRegistration(t *testing.T) {
	gomega.RegisterTestingT(t)
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	managerConfig := newFabricManagerConfigMap("agentless", "networking", "agentless_net")
	managerConfig.Data[networkmanager.DataKeyExternalIPContractVersion] = networkmanager.ExternalIPContractVersionV1
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(managerConfig).Build()
	resolver, networkClasses := wireExternalIPDispatcher(kubeClient, "networking", []*privatev1.NetworkClass{{
		Id: "network-class", FabricManager: ptr.To("agentless_net"),
	}})
	reconciler := &ExternalIPReconciler{Resolver: resolver, networkClassesClient: networkClasses}
	resource := &v1alpha1.ExternalIP{}

	strategy, contractVersion, err := reconciler.resolveExternalIPManager(context.Background(), resource)
	if err != nil {
		t.Fatal(err)
	}
	if strategy != "agentless_net" || contractVersion != networkmanager.ExternalIPContractVersionV1 {
		t.Fatalf("got manager=%q contract=%q", strategy, contractVersion)
	}

	resolver, networkClasses = wireExternalIPDispatcher(kubeClient, "networking", []*privatev1.NetworkClass{{
		Id: "network-class",
	}})
	reconciler.Resolver = resolver
	reconciler.networkClassesClient = networkClasses
	resource.Annotations = map[string]string{osacImplementationStrategyAnnotation: "stale-manager"}

	strategy, _, err = reconciler.resolveExternalIPManager(context.Background(), resource)
	if err != nil {
		t.Fatal(err)
	}
	if strategy != "" {
		t.Fatalf("unconfigured manager fell back to the stale annotation %q", strategy)
	}
}
