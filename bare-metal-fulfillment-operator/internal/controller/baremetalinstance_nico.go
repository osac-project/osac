package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const nicoBMaaSTemplateID = "osac.templates.nico_bm_host_provisioning"

// Create outcomes published by a failed NICo create job.
const (
	nicoCreateOutcomeNotCreated = "NotCreated"
	nicoCreateOutcomeUncertain  = "Uncertain"
	nicoCreateOutcomeCreated    = "Created"
)

// recordNicoCreateFailure saves what a failed create job reported, so delete
// knows whether a NICo Instance may exist. A job without a known outcome, for
// example a canceled one, is uncertain and keeps the conservative behavior.
func recordNicoCreateFailure(bmi *v1alpha1.BareMetalInstance, artifacts map[string]any) error {
	outcome, _ := artifacts["nicoCreateOutcome"].(string)
	instanceID, _ := artifacts["nicoInstanceID"].(string)
	machineID, _ := artifacts["nicoMachineID"].(string)
	if outcome == nicoCreateOutcomeNotCreated && instanceID == "" && machineID == "" {
		bmi.Status.NicoCreateOutcome = outcome
		return nil
	}
	// Any reported ID identifies a NICo resource to release, whatever the
	// reported outcome; never discard it.
	if instanceID != "" && bmi.Status.NicoInstanceID != "" && bmi.Status.NicoInstanceID != instanceID ||
		machineID != "" && bmi.Status.NicoMachineID != "" && bmi.Status.NicoMachineID != machineID {
		return fmt.Errorf("NICo AAP artifacts differ from saved provider IDs")
	}
	if instanceID != "" {
		bmi.Status.NicoInstanceID = instanceID
	}
	if machineID != "" {
		bmi.Status.NicoMachineID = machineID
	}
	if outcome == nicoCreateOutcomeCreated {
		bmi.Status.NicoCreateOutcome = outcome
	} else {
		bmi.Status.NicoCreateOutcome = nicoCreateOutcomeUncertain
	}
	return nil
}

// resolveNicoOperatingSystem resolves the approved NICo Operating System
// before any create intent is recorded. It returns resolved=false when the
// BMI must not be created now; the BMI status then explains why. An unsupported
// DiskImage fails without contacting NICo, never booting a different image
// while OSAC reports the requested one, and stays terminal.
func (r *BareMetalInstanceReconciler) resolveNicoOperatingSystem(
	bmi *v1alpha1.BareMetalInstance, bindings *nicoBindingsFile,
) (string, bool, error) {
	// The tenant already sees FAILED, so a later catalog change must not
	// provision this BMI on its own.
	if condition := bmi.GetStatusCondition(v1alpha1.HostConditionProvisionTemplateComplete); condition != nil &&
		condition.Reason == v1alpha1.HostConditionReasonUnsupportedDiskImage {
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return "", false, nil
	}
	operatingSystemID, err := nicoOperatingSystemFor(bindings, bmi)
	if errors.Is(err, errNicoDiskImageUnsupported) {
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		bmi.SetStatusCondition(v1alpha1.HostConditionProvisionTemplateComplete, metav1.ConditionFalse,
			v1alpha1.HostConditionReasonUnsupportedDiskImage, err.Error())
		return "", false, nil
	}
	if err != nil {
		// A configuration error, such as bindings without the catalog. No create
		// intent was recorded, so a later reconcile retries after the
		// administrator fixes the ConfigMap.
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		bmi.SetStatusCondition(v1alpha1.HostConditionProvisionTemplateComplete, metav1.ConditionFalse,
			v1alpha1.HostConditionReasonTemplateFailed, "NICo Operating System catalog is unavailable")
		return "", false, err
	}
	return operatingSystemID, true, nil
}

// failNicoCreate records a failed create job and its reported reason.
func failNicoCreate(bmi *v1alpha1.BareMetalInstance, artifacts map[string]any) error {
	if err := recordNicoCreateFailure(bmi, artifacts); err != nil {
		return err
	}
	message := "NICo create job failed; verify the Site before retrying"
	if reason, _ := artifacts["nicoFailureReason"].(string); reason != "" {
		message += ": " + reason
	}
	bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
	bmi.SetStatusCondition(v1alpha1.HostConditionProvisionTemplateComplete, metav1.ConditionFalse,
		v1alpha1.HostConditionReasonTemplateFailed, message)
	return nil
}

func isNicoBMaaS(bmi *v1alpha1.BareMetalInstance) bool {
	return bmi.Spec.TemplateID == nicoBMaaSTemplateID
}

// reconcileNicoBMaaS lets NICo atomically select, configure, and boot a Machine.
// It must not run the generic FindFreeHost/AssignHost, power, or fabric port move
// paths: those would race NICo's own Instance and networking ownership.
func (r *BareMetalInstanceReconciler) reconcileNicoBMaaS(ctx context.Context, bmi *v1alpha1.BareMetalInstance) (ctrl.Result, error) {
	if len(bmi.Spec.NetworkAttachments) != 1 {
		return ctrl.Result{}, fmt.Errorf("NICo BMI pilot requires exactly one OSAC Subnet attachment")
	}
	if r.ProvisioningProvider == nil {
		return ctrl.Result{}, fmt.Errorf("NICo BMI requires a provisioning provider")
	}
	if bmi.Spec.RunStrategy == v1alpha1.RunStrategyHalted || bmi.Spec.RestartTrigger != 0 {
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return ctrl.Result{}, fmt.Errorf("NICo BMI pilot does not support halt or restart requests")
	}
	if controllerutil.AddFinalizer(bmi, BareMetalInstanceManagementFinalizer) {
		if err := r.Update(ctx, bmi); err != nil {
			return ctrl.Result{}, err
		}
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return ctrl.Result{}, nil
	}
	boundCtx, bindings, err := r.withNicoCredentials(ctx, bmi)
	if err != nil {
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		bmi.SetStatusCondition(v1alpha1.HostConditionProvisionTemplateComplete, metav1.ConditionFalse,
			v1alpha1.HostConditionReasonTemplateFailed, "NICo tenant binding is unavailable")
		return ctrl.Result{}, err
	}

	outputs, ok := r.ProvisioningProvider.(provisioning.ProvisioningProviderWithProvisionOutputs)
	if !ok {
		return ctrl.Result{}, fmt.Errorf("NICo BMI requires AAP provision output artifacts")
	}
	return r.reconcileNicoBMaaSProvisioning(boundCtx, bmi, outputs, bindings)
}

func (r *BareMetalInstanceReconciler) reconcileNicoBMaaSProvisioning(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance, outputs provisioning.ProvisioningProviderWithProvisionOutputs,
	bindings *nicoBindingsFile,
) (ctrl.Result, error) {
	latest := provisioning.FindLatestJobByType(bmi.Status.ProvisioningJobs, opv1alpha1.JobTypeProvision)
	newAttempt := false
	operatingSystemID := ""
	if !bmi.Status.NicoCreateAttempted {
		var resolved bool
		var err error
		operatingSystemID, resolved, err = r.resolveNicoOperatingSystem(bmi, bindings)
		if !resolved {
			return ctrl.Result{}, err
		}
		// Persist the one-create-attempt marker BEFORE the AAP launch. A crash or
		// lost launch response must leave the BMI requiring Site verification,
		// never silently issue another NICo POST.
		bmi.Status.NicoCreateAttempted = true
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		if err := r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(bmi), bmi.Status); err != nil {
			return ctrl.Result{}, err
		}
		newAttempt = true
	}
	if latest == nil {
		if !newAttempt {
			bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			bmi.SetStatusCondition(v1alpha1.HostConditionProvisionTemplateComplete, metav1.ConditionFalse,
				v1alpha1.HostConditionReasonTemplateFailed,
				"NICo create outcome is uncertain; verify Site allocation before another create")
			return ctrl.Result{}, nil
		}
		trigger, ok := r.ProvisioningProvider.(provisioning.ProvisioningProviderWithExtraVars)
		if !ok {
			return ctrl.Result{}, fmt.Errorf("NICo BMI requires AAP extra vars for the one-time create authorization")
		}
		result, err := trigger.TriggerProvisionWithExtraVars(ctx, bmi, map[string]any{
			"nico_bmaas_create_allowed":      true,
			"nico_bmaas_operating_system_id": operatingSystemID,
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("NICo AAP launch outcome requires inspection before retry: %w", err)
		}
		bmi.Status.ProvisioningJobs = provisioning.AppendJob(bmi.Status.ProvisioningJobs, opv1alpha1.JobStatus{
			JobID: result.JobID, Type: opv1alpha1.JobTypeProvision, State: result.InitialState,
			Message: result.Message, Timestamp: metav1.NewTime(time.Now().UTC()),
		}, provisioning.DefaultMaxJobHistory)
		if err := r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(bmi), bmi.Status); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}
	if latest.State == opv1alpha1.JobStateFailed || latest.State == opv1alpha1.JobStateCanceled {
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		bmi.SetStatusCondition(v1alpha1.HostConditionProvisionTemplateComplete, metav1.ConditionFalse,
			v1alpha1.HostConditionReasonTemplateFailed,
			"NICo create job failed; verify the Site before retrying")
		return ctrl.Result{}, nil
	}
	status, err := outputs.GetProvisionStatusWithExtraVars(ctx, bmi, latest.JobID)
	if err != nil {
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, err
	}
	updated := *latest
	updated.State = status.State
	updated.Message = status.MessageWithDetails()
	provisioning.UpdateJob(bmi.Status.ProvisioningJobs, updated)
	if status.State == opv1alpha1.JobStateFailed || status.State == opv1alpha1.JobStateCanceled {
		return ctrl.Result{}, failNicoCreate(bmi, status.ExtraVars)
	}
	if status.State != opv1alpha1.JobStateSucceeded {
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}
	instanceID, instanceOK := status.ExtraVars["nicoInstanceID"].(string)
	machineID, machineOK := status.ExtraVars["nicoMachineID"].(string)
	ipAddress, ipOK := status.ExtraVars["nicoIPAddress"].(string)
	if !instanceOK || instanceID == "" || !machineOK || machineID == "" || !ipOK || ipAddress == "" {
		bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		bmi.SetStatusCondition(v1alpha1.HostConditionProvisionTemplateComplete, metav1.ConditionFalse,
			v1alpha1.HostConditionReasonTemplateFailed,
			"NICo job succeeded without provider IDs or assigned IP; retain BMI for recovery")
		return ctrl.Result{}, nil
	}
	if bmi.Status.NicoInstanceID != "" && bmi.Status.NicoInstanceID != instanceID ||
		bmi.Status.NicoMachineID != "" && bmi.Status.NicoMachineID != machineID {
		return ctrl.Result{}, fmt.Errorf("NICo AAP artifacts differ from saved provider IDs")
	}
	if bmi.Spec.ExternalHostID != machineID || bmi.Spec.HostClass != "nico" {
		bmi.Spec.ExternalHostID = machineID
		bmi.Spec.HostClass = "nico"
		if err := r.Update(ctx, bmi); err != nil {
			return ctrl.Result{}, err
		}
	}
	// A Kubernetes spec Update refreshes the object without status-subresource
	// edits. Assign the provider IDs afterward so the reconcile status flush
	// cannot lose them.
	bmi.Status.NicoInstanceID, bmi.Status.NicoMachineID = instanceID, machineID
	bmi.Status.NetworkAttachmentStatuses = []v1alpha1.BareMetalNetworkAttachmentStatus{{
		Interface: bmi.Spec.NetworkAttachments[0].Interface,
		SubnetRef: bmi.Spec.NetworkAttachments[0].SubnetRef,
		IPAddress: ipAddress,
		Primary:   true,
	}}
	bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseReady
	bmi.Status.RunStrategy = v1alpha1.RunStrategyAlways
	bmi.SetStatusCondition(v1alpha1.HostConditionAllocated, metav1.ConditionTrue, "Allocated", "NICo Machine allocated")
	bmi.SetStatusCondition(v1alpha1.HostConditionProvisionTemplateComplete, metav1.ConditionTrue,
		"Succeeded", "NICo Instance reached Ready")
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady, metav1.ConditionTrue,
		"Ready", "NICo Instance interface has an assigned IP")
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete, metav1.ConditionTrue,
		"Ready", "NICo owns the Machine network configuration")
	bmi.SetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete, metav1.ConditionTrue,
		"Ready", "NICo reported an assigned IP address")
	bmi.SetStatusCondition(v1alpha1.HostConditionPowerSynced, metav1.ConditionTrue,
		v1alpha1.HostConditionReasonPowerOn, "NICo Instance reached Ready")
	return ctrl.Result{}, nil
}

// resolveNicoIDsForDelete handles a delete that has no saved provider IDs yet.
// An in-flight create may finish after Kubernetes accepts deletion, so it waits
// for the create job's artifacts and never removes the finalizer on an
// uncertain result. A create job that reports NotCreated releases the BMI.
func (r *BareMetalInstanceReconciler) resolveNicoIDsForDelete(ctx context.Context, bmi *v1alpha1.BareMetalInstance) (ctrl.Result, error) {
	latest := provisioning.FindLatestJobByType(bmi.Status.ProvisioningJobs, opv1alpha1.JobTypeProvision)
	if latest == nil {
		return ctrl.Result{}, fmt.Errorf("NICo create attempt has no saved job or provider IDs; inspect Site allocation")
	}
	outputs, ok := r.ProvisioningProvider.(provisioning.ProvisioningProviderWithProvisionOutputs)
	if !ok {
		return ctrl.Result{}, fmt.Errorf("NICo BMI deletion requires AAP provision artifacts")
	}
	status, err := outputs.GetProvisionStatusWithExtraVars(ctx, bmi, latest.JobID)
	if err != nil {
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, err
	}
	if !status.State.IsTerminal() {
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}
	if status.State == opv1alpha1.JobStateSucceeded {
		instanceID, _ := status.ExtraVars["nicoInstanceID"].(string)
		machineID, _ := status.ExtraVars["nicoMachineID"].(string)
		if instanceID != "" && machineID != "" {
			bmi.Status.NicoInstanceID, bmi.Status.NicoMachineID = instanceID, machineID
		}
	} else {
		// The create job failed after deletion was requested.
		if err := recordNicoCreateFailure(bmi, status.ExtraVars); err != nil {
			return ctrl.Result{}, err
		}
		if bmi.Status.NicoCreateOutcome == nicoCreateOutcomeNotCreated {
			controllerutil.RemoveFinalizer(bmi, BareMetalInstanceManagementFinalizer)
			return ctrl.Result{}, r.Update(ctx, bmi)
		}
	}
	if bmi.Status.NicoInstanceID == "" {
		return ctrl.Result{}, fmt.Errorf("NICo create ended without a provider Instance ID; verify Site allocation before deletion")
	}
	return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
}

// deleteNicoBMaaS leaves the BMI finalizer in place until the AAP backend has
// observed Instance absence and the original Machine unassigned Ready.
func (r *BareMetalInstanceReconciler) deleteNicoBMaaS(ctx context.Context, bmi *v1alpha1.BareMetalInstance) (ctrl.Result, error) {
	bmi.Status.Phase = v1alpha1.BareMetalInstancePhaseDeleting
	if !controllerutil.ContainsFinalizer(bmi, BareMetalInstanceManagementFinalizer) {
		if bmi.Status.NicoCreateAttempted {
			return ctrl.Result{}, fmt.Errorf("NICo create was attempted without a BMI cleanup finalizer; inspect Site allocation")
		}
		return ctrl.Result{}, nil
	}
	if bmi.Status.NicoInstanceID == "" && bmi.Status.NicoMachineID == "" &&
		(!bmi.Status.NicoCreateAttempted || bmi.Status.NicoCreateOutcome == nicoCreateOutcomeNotCreated) {
		// The BMI was deleted before any NICo create attempt, or its create job
		// failed before the create request. There is no provider resource to release.
		controllerutil.RemoveFinalizer(bmi, BareMetalInstanceManagementFinalizer)
		return ctrl.Result{}, r.Update(ctx, bmi)
	}
	if r.ProvisioningProvider == nil {
		return ctrl.Result{}, fmt.Errorf("NICo BMI deletion requires a provisioning provider")
	}
	// A failed create can leave an Instance with no Machine assigned; the
	// Instance ID alone is enough to release it.
	if bmi.Status.NicoInstanceID == "" {
		return r.resolveNicoIDsForDelete(ctx, bmi)
	}
	boundCtx, _, err := r.withNicoCredentials(ctx, bmi)
	if err != nil {
		return ctrl.Result{}, err
	}
	result, done, err := r.reconcileDeprovisioning(boundCtx, bmi)
	if err != nil || !done {
		return result, err
	}
	latest := provisioning.FindLatestJobByType(bmi.Status.ProvisioningJobs, opv1alpha1.JobTypeDeprovision)
	if latest == nil || latest.State != opv1alpha1.JobStateSucceeded {
		return ctrl.Result{}, fmt.Errorf("NICo cleanup has no successful AAP job; retain BMI finalizer")
	}
	outputs, ok := r.ProvisioningProvider.(provisioning.ProvisioningProviderWithDeprovisionOutputs)
	if !ok {
		return ctrl.Result{}, fmt.Errorf("NICo cleanup requires AAP deprovision artifacts")
	}
	status, err := outputs.GetDeprovisionStatusWithExtraVars(ctx, bmi, latest.JobID)
	if err != nil {
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, err
	}
	if status.ExtraVars["nicoCleanupStatus"] != "Released" ||
		status.ExtraVars["nicoInstanceID"] != bmi.Status.NicoInstanceID ||
		status.ExtraVars["nicoMachineID"] != bmi.Status.NicoMachineID {
		return ctrl.Result{}, fmt.Errorf("NICo cleanup artifacts do not confirm original Machine release")
	}
	controllerutil.RemoveFinalizer(bmi, BareMetalInstanceManagementFinalizer)
	if err := r.Update(ctx, bmi); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}
