package controller

import (
	"context"
	"testing"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestEVPNEligibilityRequiresPersistedOwner(test *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		test.Fatal(err)
	}
	if err := coordinationv1.AddToScheme(scheme); err != nil {
		test.Fatal(err)
	}
	for _, scenario := range []struct {
		name, decision, owner, cleaned string
		expected                       *bool
	}{
		{name: "unknown"},
		{name: "fabric-only", decision: "false", owner: "owner", expected: ptr.To(false)},
		{name: "owner", decision: "true", owner: "subnet", expected: ptr.To(true)},
		{name: "stale true", decision: "true", owner: "other"},
		{name: "cleanup completed", decision: "true", owner: "subnet", cleaned: "true"},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			subnet := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "subnet", Namespace: "test",
				Annotations: map[string]string{subnetComputeSupportedAnnotation: scenario.decision}},
				Spec: v1alpha1.SubnetSpec{VirtualNetwork: "vn"}}
			lease := &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: sharedCUDNLeaseName("vn"), Namespace: "test",
				Annotations: map[string]string{cudnOwnerAnnotation: scenario.owner, cudnOwnerCleanedAnnotation: scenario.cleaned}}}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(lease).Build()
			supported, _, err := readEVPNComputeEligibility(context.Background(), reader, subnet)
			if err != nil {
				test.Fatal(err)
			}
			if (supported == nil) != (scenario.expected == nil) || (supported != nil && *supported != *scenario.expected) {
				test.Fatalf("supported=%v expected=%v", supported, scenario.expected)
			}
		})
	}
}

func TestUnsupportedPlacementRemainsTerminalWithCachedNamespace(test *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		test.Fatal(err)
	}
	subnet := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "fabric-only", Namespace: "test",
		Annotations: map[string]string{osacK8sImplementationStrategyAnnotation: cudnEVPNManagerName,
			subnetComputeSupportedAnnotation: "true"}},
		Status: v1alpha1.SubnetStatus{Phase: v1alpha1.SubnetPhaseReady, ComputeInstanceSupported: ptr.To(false)}}
	instance := &v1alpha1.ComputeInstance{ObjectMeta: metav1.ObjectMeta{Name: "vm", Namespace: "test",
		Annotations: map[string]string{osacSubnetTargetNamespaceAnnotation: "cached-namespace"}}}
	instance.Spec.NetworkAttachments = []v1alpha1.ComputeNetworkAttachment{{SubnetRef: subnet.Name}}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(subnet).Build()
	reconciler := &ComputeInstanceReconciler{Client: reader, APIReader: reader}
	for attempt := 0; attempt < 3; attempt++ {
		instance.Spec.RestartRequestedAt = ptr.To(metav1.Now())
		result, err := reconciler.handleUpdate(context.Background(), reconcile.Request{}, instance)
		if err != nil {
			test.Fatal(err)
		}
		if result.RequeueAfter != 0 || instance.Status.Phase != v1alpha1.ComputeInstancePhaseFailed ||
			conditionReason(instance, v1alpha1.ComputeInstanceConditionProvisioned) != networkAttachmentFailedReason {
			test.Fatalf("unsupported placement retried or lost terminal status: %+v", instance.Status)
		}
		if len(instance.Status.ProvisioningJobs) != 0 {
			test.Fatal("unsupported VM dispatched provisioning")
		}
	}
}

func TestSubnetFeedbackPreservesOptionalEligibility(test *testing.T) {
	for _, supported := range []*bool{nil, ptr.To(false), ptr.To(true)} {
		subnet := &v1alpha1.Subnet{Status: v1alpha1.SubnetStatus{
			Phase: v1alpha1.SubnetPhaseReady, ComputeInstanceSupported: supported,
		}}
		remote := privatev1.Subnet_builder{Status: privatev1.SubnetStatus_builder{
			ComputeInstanceSupported: ptr.To(true), ComputeInstanceUnsupportedReason: "stale reason",
		}.Build()}.Build()
		if err := syncSubnetUpdate(context.Background(), subnet, remote); err != nil {
			test.Fatal(err)
		}
		status := remote.GetStatus()
		if status.HasComputeInstanceSupported() != (supported != nil) ||
			(supported != nil && status.GetComputeInstanceSupported() != *supported) ||
			status.GetComputeInstanceUnsupportedReason() != "" || status.GetState() != privatev1.SubnetState_SUBNET_STATE_READY {
			test.Fatalf("feedback lost optional capability or retained a stale reason: %v", status)
		}
	}
}

func TestSubnetCapabilityProjectionAndPlacementWait(test *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		test.Fatal(err)
	}
	for _, scenario := range []struct {
		name, manager string
		skip          bool
		expected      bool
	}{
		{name: "no Kubernetes manager"},
		{name: "other Kubernetes manager", manager: "k8s_only", expected: true},
		{name: "explicit opt out", manager: "k8s_only", skip: true},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			subnet := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "subnet", Namespace: "test", Annotations: map[string]string{}}}
			if scenario.skip {
				subnet.Annotations[osacSkipK8sManagerAnnotation] = "true"
			}
			var target *dispatcher.DispatchTarget
			if scenario.manager != "" {
				target = &dispatcher.DispatchTarget{Manager: networkmanager.Manager{Name: scenario.manager}}
			}
			if err := (&SubnetReconciler{}).syncComputeEligibility(context.Background(), subnet, target); err != nil {
				test.Fatal(err)
			}
			if subnet.Status.ComputeInstanceSupported == nil || *subnet.Status.ComputeInstanceSupported != scenario.expected {
				test.Fatalf("incorrect capability projection: %+v", subnet.Status)
			}
		})
	}
	for _, supported := range []*bool{nil, ptr.To(true)} {
		subnet := &v1alpha1.Subnet{ObjectMeta: metav1.ObjectMeta{Name: "subnet", Namespace: "test"},
			Status: v1alpha1.SubnetStatus{ComputeInstanceSupported: supported}}
		reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(subnet).Build()
		reconciler := &ComputeInstanceReconciler{Client: reader, APIReader: reader}
		instance := &v1alpha1.ComputeInstance{ObjectMeta: metav1.ObjectMeta{Namespace: "test"}}
		instance.Spec.NetworkAttachments = []v1alpha1.ComputeNetworkAttachment{{SubnetRef: "subnet"}}
		result, blocked, err := reconciler.checkSubnetPlacement(context.Background(), instance)
		if err != nil || !blocked || result.RequeueAfter == 0 || instance.Status.Phase == v1alpha1.ComputeInstancePhaseFailed {
			test.Fatalf("unknown or unready placement must wait, not fail: %+v, %v", result, err)
		}
	}
}
