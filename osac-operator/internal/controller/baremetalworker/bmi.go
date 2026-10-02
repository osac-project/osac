// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type bmiState struct {
	bmi    *privatev1.BareMetalInstance
	absent bool
}

const (
	systemBMITemplateID = "osac.templates.bm_host_provisioning"
)

// ensureBMI creates a single BareMetalInstance, handling the AlreadyExists race by re-listing.
// Returns the BMI, a non-zero result on unavailability backoff, or an error.
func (r *Reconciler) ensureBMI(
	ctx context.Context, co *v1alpha1.ClusterOrder, tenant string, nodeSet v1alpha1.NodeRequest,
	workerName string, image *privatev1.DiskImageReference, ignitionRaw, filter, fabricInterface string, observations ...*workerObservation,
) (*privatev1.BareMetalInstance, ctrl.Result, error) {
	req := r.buildBMICreateRequest(co, tenant, nodeSet, workerName, image, ignitionRaw, fabricInterface)
	created, err := r.fulfillment.CreateBareMetalInstance(ctx, req)
	if err == nil {
		for _, o := range observations {
			o.recordBMI(created)
		}
		return created, ctrl.Result{}, nil
	}

	if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
		bmi, listErr := r.findBMIByName(ctx, co, tenant, filter, workerName, observations...)
		if listErr != nil {
			return nil, ctrl.Result{}, listErr
		}
		return bmi, ctrl.Result{}, nil
	}

	res, err := r.handleFulfillmentError(ctx, co, fmt.Errorf("creating BMI %s: %w", workerName, err))
	return nil, res, err
}

// findBMIByName re-lists BMIs and returns the one matching the given name.
func (r *Reconciler) findBMIByName(ctx context.Context, co *v1alpha1.ClusterOrder, tenant, filter, name string, observations ...*workerObservation) (*privatev1.BareMetalInstance, error) {
	ctrllog.FromContext(ctx).Info("BMI create returned AlreadyExists, re-listing", "name", name)
	refreshed, err := r.fulfillment.ListBareMetalInstances(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("re-listing BMIs after AlreadyExists: %w", err)
	}
	indexed := indexWorkerBMIs(refreshed)
	bmi, err := indexed.exactOwnedName(co, tenant, name)
	if err != nil {
		return nil, r.rejectWorkerIdentity(co, err.Error())
	}
	if bmi != nil {
		if err := checkBMIRecoveryCandidate(bmi); err != nil {
			return nil, err
		}
		for _, o := range observations {
			o.recordBMI(bmi)
		}
		return bmi, nil
	}
	return nil, fmt.Errorf("BMI %s returned AlreadyExists but not found in re-list", name)
}

func (r *Reconciler) buildBMICreateRequest(
	co *v1alpha1.ClusterOrder, tenant string, nodeSet v1alpha1.NodeRequest, workerName string,
	image *privatev1.DiskImageReference, ignitionRaw, fabricInterface string,
) *privatev1.BareMetalInstance {
	labels := map[string]string{clusterOrderLabel: co.Name}
	annotations := map[string]string{ownerReferenceAnnotation: fmt.Sprintf("ClusterOrder/%s", co.Name)}

	var netAttachments []*privatev1.BareMetalNetworkAttachment
	if na := co.Spec.NetworkAttachment; na != nil && na.SubnetRef != "" {
		sgRefs := make([]*privatev1.SecurityGroupLocalReference, 0, len(na.SecurityGroupRefs))
		for _, sg := range na.SecurityGroupRefs {
			sgRefs = append(sgRefs, privatev1.SecurityGroupLocalReference_builder{Name: sg}.Build())
		}
		primary := true
		netAttachments = []*privatev1.BareMetalNetworkAttachment{
			privatev1.BareMetalNetworkAttachment_builder{
				Subnet:         privatev1.SubnetLocalReference_builder{Name: na.SubnetRef}.Build(),
				SecurityGroups: sgRefs,
				Interface:      &fabricInterface,
				Primary:        &primary,
			}.Build(),
		}
	}

	var instanceType *privatev1.BareMetalInstanceTypeReference
	if nodeSet.BareMetal.InstanceType != "" {
		instanceType = privatev1.BareMetalInstanceTypeReference_builder{
			Name: nodeSet.BareMetal.InstanceType, Shared: true,
		}.Build()
	}

	specBuilder := privatev1.BareMetalInstanceSpec_builder{
		Template: privatev1.BareMetalInstanceTemplateReference_builder{
			Id: systemBMITemplateID, Shared: true,
		}.Build(),
		DiskImage:          image,
		UserData:           &ignitionRaw,
		InstanceType:       instanceType,
		NetworkAttachments: netAttachments,
	}
	if sshKey := r.resolveSSHPublicKey(co); sshKey != "" {
		specBuilder.SshPublicKey = &sshKey
	}

	return privatev1.BareMetalInstance_builder{
		Metadata: privatev1.Metadata_builder{
			Tenant:      tenant,
			Name:        workerName,
			Labels:      labels,
			Annotations: annotations,
		}.Build(),
		Spec: specBuilder.Build(),
	}.Build()
}

func (r *Reconciler) resolveNodeSetInstanceType(
	ctx context.Context, co *v1alpha1.ClusterOrder, instanceTypeName string,
) (*privatev1.BareMetalInstanceType, ctrl.Result, error) {
	it, err := r.fulfillment.GetBareMetalInstanceType(ctx, instanceTypeName)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			ctrllog.FromContext(ctx).Info("BareMetalInstanceType not found, requeuing", "instanceType", instanceTypeName)
			return nil, ctrl.Result{RequeueAfter: infraEnvRequeueInterval}, nil
		}
		res, err := r.handleFulfillmentError(ctx, co, fmt.Errorf("getting BareMetalInstanceType %s: %w", instanceTypeName, err))
		return nil, res, err
	}
	if it == nil {
		ctrllog.FromContext(ctx).Info("BareMetalInstanceType is empty, requeuing", "instanceType", instanceTypeName)
		return nil, ctrl.Result{RequeueAfter: infraEnvRequeueInterval}, nil
	}
	return it, ctrl.Result{}, nil
}

// resolveFabricInterface returns the name of the first network port with role "fabric" from the
// BareMetalInstanceType. Returns an error if no fabric port is found.
func resolveFabricInterface(it *privatev1.BareMetalInstanceType) (string, error) {
	for _, port := range it.GetSpec().GetHardware().GetNetworkPorts() {
		if port.GetRole() == "fabric" {
			return port.GetName(), nil
		}
	}
	return "", fmt.Errorf("BareMetalInstanceType %s has no fabric-role network port", it.GetMetadata().GetName())
}

// checkBMIRecoveryCandidate applies only to creation/name recovery. Existing
// recorded IDs and finalization still need to observe deleting resources.
func checkBMIRecoveryCandidate(bmi *privatev1.BareMetalInstance) error {
	if bmi.GetMetadata().GetDeletionTimestamp() != nil || bmi.GetStatus().GetState() == privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_DELETING {
		return fmt.Errorf("BMI %s is deleting; cannot recover a created worker", bmi.GetId())
	}
	return nil
}

// verifyRecordedBMI checks owned identities even in protected lifecycle states.
// List omission requires one memoized authoritative Get. Unknown ownership is
// an error, not the old independent best-effort existence probe's permission to
// continue. Neither this lookup nor reconcileBMI persists status.
func (r *Reconciler) verifyRecordedBMI(ctx context.Context, co *v1alpha1.ClusterOrder, tenant string, w v1alpha1.WorkerStatus, o *workerObservation) (bmiState, error) {
	if w.BareMetalInstance.ID == "" {
		bmi, err := o.exactOwnedName(co, tenant, w.BareMetalInstance.Name)
		if err != nil {
			return bmiState{}, r.rejectWorkerIdentity(co, err.Error())
		}
		if bmi != nil && co.DeletionTimestamp.IsZero() && eligibleForBMIRecovery(w) {
			if err := checkBMIRecoveryCandidate(bmi); err != nil {
				return bmiState{}, err
			}
		}
		return bmiState{bmi: bmi}, nil
	}
	bmi, err := o.getBMI(ctx, r.fulfillment, w.BareMetalInstance.ID)
	if status.Code(err) == codes.NotFound {
		return bmiState{absent: true}, nil
	}
	if err != nil {
		return bmiState{}, fmt.Errorf("checking worker BMI %s: %w", w.BareMetalInstance.ID, err)
	}
	if err := checkRecordedWorkerBMI(co, tenant, w, bmi); err != nil {
		return bmiState{}, r.rejectWorkerIdentity(co, err.Error())
	}
	return bmiState{bmi: bmi}, nil
}

// reconcileBMI is one worker's identity decision. Nil removes only an active
// slot with confirmed absence; protected states stay owned by retry/teardown.
func reconcileBMI(w v1alpha1.WorkerStatus, state bmiState) *v1alpha1.WorkerStatus {
	if state.absent && eligibleForBMIAbsence(w) {
		return nil
	}
	if eligibleForBMIRecovery(w) && state.bmi != nil {
		w.BareMetalInstance.ID = state.bmi.GetId()
		if w.Phase == workerPhaseProvisioning {
			workerCreated(&w, w.BareMetalInstance.Name, state.bmi.GetId())
		}
	}
	return &w
}

func eligibleForBMIRecovery(w v1alpha1.WorkerStatus) bool {
	return w.Kind == workerKindBMI && w.BareMetalInstance.ID == "" && w.Phase != workerPhaseUnbinding && w.Phase != workerPhaseDeleting && (w.Phase != workerPhaseFailed || w.NextRetryTime == nil)
}

func eligibleForBMIAbsence(w v1alpha1.WorkerStatus) bool {
	return w.Kind == workerKindBMI && w.BareMetalInstance.ID != "" && w.Phase != workerPhaseFailed && w.Phase != workerPhaseUnbinding && w.Phase != workerPhaseDeleting
}

func (r *Reconciler) checkedDeleteBMI(ctx context.Context, co *v1alpha1.ClusterOrder, w v1alpha1.WorkerStatus) error {
	_, err := r.checkedDeleteBMIState(ctx, co, w)
	return err
}

// checkedDeleteBMIState combines a fresh ownership/existence read with deletion.
// Successful Delete is only a request; only this authoritative Get NotFound
// confirms absence. The initial reconcile observation never authorizes deletion.
func (r *Reconciler) checkedDeleteBMIState(ctx context.Context, co *v1alpha1.ClusterOrder, w v1alpha1.WorkerStatus) (bool, error) {
	tenant, err := r.authoritativeWorkerTenant(ctx, co)
	if err != nil {
		return false, err
	}
	bmi, err := r.fulfillment.GetBareMetalInstance(ctx, w.BareMetalInstance.ID)
	if status.Code(err) == codes.NotFound {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if err := checkRecordedWorkerBMI(co, tenant, w, bmi); err != nil {
		return false, r.rejectWorkerIdentity(co, err.Error())
	}
	return false, r.fulfillment.DeleteBareMetalInstance(ctx, w.BareMetalInstance.ID)
}
