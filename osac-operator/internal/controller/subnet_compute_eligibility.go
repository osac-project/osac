package controller

import (
	"context"
	"crypto/sha1"
	"fmt"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
)

const (
	subnetComputeSupportedAnnotation = "osac.openshift.io/compute-instance-supported"
	subnetComputeReasonAnnotation    = "osac.openshift.io/compute-instance-unsupported-reason"
	cudnOwnerAnnotation              = "osac.openshift.io/cudn-owner-subnet"
	cudnOwnerCleanedAnnotation       = "osac.openshift.io/cudn-owner-cleaned"
	networkAttachmentFailedReason    = "NetworkAttachmentFailed"
	subnetOptOutReason               = "Subnet explicitly opts out of Kubernetes networking"
)

func sharedCUDNLeaseName(virtualNetworkID string) string {
	return fmt.Sprintf("cudn-evpn-%x", sha1.Sum([]byte(virtualNetworkID)))
}

func readEVPNComputeEligibility(ctx context.Context, reader client.Reader, subnet *v1alpha1.Subnet) (*bool, string, error) {
	if subnet.Annotations[osacSkipK8sManagerAnnotation] == labelValueTrue {
		return ptr.To(false), subnetOptOutReason, nil
	}
	switch subnet.Annotations[subnetComputeSupportedAnnotation] {
	case "false":
		reason := subnet.Annotations[subnetComputeReasonAnnotation]
		if reason == "" {
			reason = "cudn_evpn supports VMs only on its VirtualNetwork's owner Subnet"
		}
		return ptr.To(false), reason, nil
	case labelValueTrue:
		lease := &coordinationv1.Lease{}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: subnet.Namespace, Name: sharedCUDNLeaseName(subnet.Spec.VirtualNetwork)}, lease); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, "Waiting for EVPN owner assignment", nil
			}
			return nil, "", err
		}
		if lease.Annotations[cudnOwnerAnnotation] != subnet.Name || lease.Annotations[cudnOwnerCleanedAnnotation] == labelValueTrue {
			return nil, "Waiting for consistent EVPN owner assignment", nil
		}
		return ptr.To(true), "", nil
	default:
		return nil, "Waiting for Subnet compute eligibility", nil
	}
}

func (r *SubnetReconciler) syncComputeEligibility(ctx context.Context, subnet *v1alpha1.Subnet, target *dispatcher.DispatchTarget) error {
	supported, reason := ptr.To(false), "NetworkClass has no Kubernetes manager for VM workloads"
	if target != nil {
		supported, reason = ptr.To(true), ""
		if target.Manager.Name == cudnEVPNManagerName {
			reader := r.APIReader
			if reader == nil {
				reader = r.Client
			}
			var err error
			supported, reason, err = readEVPNComputeEligibility(ctx, reader, subnet)
			if err != nil {
				return err
			}
		} else if subnet.Annotations[osacSkipK8sManagerAnnotation] == labelValueTrue {
			supported, reason = ptr.To(false), subnetOptOutReason
		}
	}
	subnet.Status.ComputeInstanceSupported = supported
	subnet.Status.ComputeInstanceUnsupportedReason = reason
	return nil
}

func (r *ComputeInstanceReconciler) checkSubnetPlacement(ctx context.Context, instance *v1alpha1.ComputeInstance) (ctrl.Result, bool, error) {
	if conditionReason(instance, v1alpha1.ComputeInstanceConditionProvisioned) == networkAttachmentFailedReason {
		setComputeInstancePhase(&instance.Status, v1alpha1.ComputeInstancePhaseFailed)
		return ctrl.Result{}, true, nil
	}
	reference := instance.Spec.PrimarySubnetRef()
	if reference == "" {
		return ctrl.Result{}, false, nil
	}
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	subnet := &v1alpha1.Subnet{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: instance.Namespace, Name: reference}, subnet); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{RequeueAfter: 30 * time.Second}, true, nil
		}
		return ctrl.Result{}, true, err
	}
	supported, reason := subnet.Status.ComputeInstanceSupported, subnet.Status.ComputeInstanceUnsupportedReason
	if subnet.Annotations[osacSkipK8sManagerAnnotation] == labelValueTrue {
		supported, reason = ptr.To(false), subnetOptOutReason
	} else if (supported == nil || *supported) && (subnet.Annotations[osacK8sImplementationStrategyAnnotation] == cudnEVPNManagerName || subnet.Annotations[osacK8sCleanupStrategyAnnotation] == cudnEVPNManagerName) {
		var err error
		supported, reason, err = readEVPNComputeEligibility(ctx, reader, subnet)
		if err != nil {
			return ctrl.Result{}, true, err
		}
	}
	if supported != nil && !*supported {
		if reason == "" {
			reason = "Subnet does not support compute instances"
		}
		message := fmt.Sprintf("Subnet %q: %s", subnet.Name, reason)
		setComputeInstancePhase(&instance.Status, v1alpha1.ComputeInstancePhaseFailed)
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionProvisioned, metav1.ConditionFalse, message, networkAttachmentFailedReason)
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionReady, metav1.ConditionFalse, message, networkAttachmentFailedReason)
		return ctrl.Result{}, true, nil
	}
	if supported == nil || subnet.Status.Phase != v1alpha1.SubnetPhaseReady || !subnet.DeletionTimestamp.IsZero() {
		return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, true, nil
	}
	return ctrl.Result{}, false, nil
}
