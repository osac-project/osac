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
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/management"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// BareMetalInstanceReconciler reconciles a BareMetalInstance object
type BareMetalInstanceReconciler struct {
	client.Client
	// APIReader is a direct, uncached reader (mgr.GetAPIReader) used by the
	// duplicate-job guard so it does not read the same lagging informer cache it
	// is meant to bypass. Set in SetupWithManager.
	APIReader                         client.Reader
	Scheme                            *runtime.Scheme
	InventoryClient                   inventory.Client
	ManagementClient                  management.Client
	ProvisioningProvider              provisioning.ProvisioningProvider
	NetworkingProvider                provisioning.ProvisioningProvider
	IPDiscoveryProvider               provisioning.ProvisioningProvider
	AAPClient                         *aap.Client
	NoFreeHostsPollIntervalDuration   time.Duration
	TryLockFailPollIntervalDuration   time.Duration
	ManagementRecheckIntervalDuration time.Duration
	ProvisionPollIntervalDuration     time.Duration
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=subnets,verbs=get;list;watch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=networkclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=externalips,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=externalipattachments,verbs=get;list;watch;delete

func NewBareMetalInstanceReconciler(
	client client.Client,
	scheme *runtime.Scheme,
	inventoryClient inventory.Client,
	managementClient management.Client,
	provisioningProvider provisioning.ProvisioningProvider,
	networkingProvider provisioning.ProvisioningProvider,
	ipDiscoveryProvider provisioning.ProvisioningProvider,
	aapClient *aap.Client,
	noFreeHostsPollIntervalDuration time.Duration,
	tryLockFailPollIntervalDuration time.Duration,
	managementRecheckIntervalDuration time.Duration,
	provisionPollIntervalDuration time.Duration,
) *BareMetalInstanceReconciler {
	if noFreeHostsPollIntervalDuration <= 0 {
		noFreeHostsPollIntervalDuration = DefaultNoFreeHostsPollIntervalDuration
	}

	if tryLockFailPollIntervalDuration <= 0 {
		tryLockFailPollIntervalDuration = DefaultTryLockFailPollIntervalDuration
	}

	if managementRecheckIntervalDuration <= 0 {
		managementRecheckIntervalDuration = DefaultManagementRecheckIntervalDuration
	}

	if provisionPollIntervalDuration <= 0 {
		provisionPollIntervalDuration = DefaultProvisionPollIntervalDuration
	}

	return &BareMetalInstanceReconciler{
		Client:                            client,
		Scheme:                            scheme,
		InventoryClient:                   inventoryClient,
		NoFreeHostsPollIntervalDuration:   noFreeHostsPollIntervalDuration,
		TryLockFailPollIntervalDuration:   tryLockFailPollIntervalDuration,
		ManagementClient:                  managementClient,
		ProvisioningProvider:              provisioningProvider,
		NetworkingProvider:                networkingProvider,
		IPDiscoveryProvider:               ipDiscoveryProvider,
		AAPClient:                         aapClient,
		ManagementRecheckIntervalDuration: managementRecheckIntervalDuration,
		ProvisionPollIntervalDuration:     provisionPollIntervalDuration,
	}
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalinstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalinstances/finalizers,verbs=update
// +kubebuilder:rbac:groups=metal3.io,resources=baremetalhosts,verbs=create;delete;get;list;watch;update;patch
// NOTE: Secret access is intentionally NOT a cluster-wide rule. The operator
// only reads/creates BMC credential Secrets in the Metal3 (BMH) namespace, so
// that permission is granted via a namespace-scoped Role+RoleBinding in the
// Helm chart (templates/bmc-secret-role.yaml), not this cluster-scoped role.
// +kubebuilder:rbac:groups=osac.openshift.io,resources=subnets,verbs=get;list;watch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=networkclasses,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the pool closer to the desired state.
func (r *BareMetalInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("BareMetalInstance reconcile start")

	bareMetalInstance := &v1alpha1.BareMetalInstance{}
	err := r.Get(ctx, req.NamespacedName, bareMetalInstance)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	oldstatus := bareMetalInstance.Status.DeepCopy()

	var result ctrl.Result
	if !bareMetalInstance.DeletionTimestamp.IsZero() {
		result, err = r.handleDeletion(ctx, bareMetalInstance)
	} else {
		result, err = r.handleUpdate(ctx, bareMetalInstance)
	}

	if !equality.Semantic.DeepEqual(bareMetalInstance.Status, *oldstatus) {
		log.Info("Updating BareMetalInstance status")
		if statusErr := r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(bareMetalInstance), bareMetalInstance.Status); client.IgnoreNotFound(statusErr) != nil {
			return result, statusErr
		}
	}

	log.Info("BareMetalInstance reconcile end")
	return result, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *BareMetalInstanceReconciler) SetupWithManager(mgr ctrl.Manager, maxConcurrentReconciles int) error {
	r.APIReader = mgr.GetAPIReader()
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.BareMetalInstance{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
		}).
		Named("baremetalinstance").
		Complete(r)
}

// apiReaderOrClient returns the uncached API reader used by the duplicate-job
// guard, falling back to the cached client when no direct reader is configured
// (e.g. unit tests that construct the reconciler directly).
func (r *BareMetalInstanceReconciler) apiReaderOrClient() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// updateStatusWithRetry updates the BMI status with retry on conflict.
// The feedback controller may concurrently modify the resource (e.g. adding
// its finalizer), changing the resourceVersion. A plain Status().Update would
// fail with a conflict, causing the provisioning job to go unrecorded and a
// duplicate to be triggered on the next reconcile.
func (r *BareMetalInstanceReconciler) updateStatusWithRetry(ctx context.Context, key client.ObjectKey, newStatus v1alpha1.BareMetalInstanceStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.BareMetalInstance{}
		if err := r.Get(ctx, key, latest); err != nil {
			return err
		}
		latest.Status = newStatus
		return r.Status().Update(ctx, latest)
	})
}

// handleUpdate assigns an inventory node to the BareMetalInstance CR and marks it as acquired.
func (r *BareMetalInstanceReconciler) handleUpdate(ctx context.Context, bareMetalInstance *v1alpha1.BareMetalInstance) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Updating BareMetalInstance")

	if bareMetalInstance.Spec.TemplateID != "" && bareMetalInstance.Spec.TemplateID != shared.OsacNoopTemplate && r.ProvisioningProvider == nil {
		log.Error(nil, "Provisioning provider not configured", "templateID", bareMetalInstance.Spec.TemplateID)
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		if bareMetalInstance.IsStatusConditionTrue(v1alpha1.HostConditionAllocated) {
			bareMetalInstance.SetStatusCondition(
				v1alpha1.HostConditionProvisionTemplateComplete,
				metav1.ConditionFalse,
				v1alpha1.HostConditionReasonTemplateFailed,
				"Provisioning provider not configured",
			)
		} else {
			bareMetalInstance.SetStatusCondition(
				v1alpha1.HostConditionAllocated,
				metav1.ConditionFalse,
				v1alpha1.HostConditionReasonTemplateFailed,
				"Provisioning provider not configured",
			)
		}
		return ctrl.Result{}, nil
	}

	if bareMetalInstance.Spec.HostClass == "" {
		return r.reconcileInventory(ctx, bareMetalInstance)
	}

	return r.reconcileManagement(ctx, bareMetalInstance)
}

func (r *BareMetalInstanceReconciler) reconcileInventory(ctx context.Context, bareMetalInstance *v1alpha1.BareMetalInstance) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Reconciling inventory")

	bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseAllocating
	bareMetalInstance.SetStatusCondition(
		v1alpha1.HostConditionAllocated,
		metav1.ConditionFalse,
		v1alpha1.HostConditionReasonProgressing,
		"Allocating BareMetalInstance",
	)

	if controllerutil.AddFinalizer(bareMetalInstance, BareMetalInstanceInventoryFinalizer) {
		if err := r.Update(ctx, bareMetalInstance); err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		log.Info("Added finalizer")
		return ctrl.Result{}, nil
	}

	if bareMetalInstance.Spec.ExternalHostID == "" {
		matchExpressions := maps.Clone(bareMetalInstance.Spec.Selector.HostSelector)
		if matchExpressions == nil {
			matchExpressions = map[string]string{}
		}
		matchExpressions["hostType"] = bareMetalInstance.Spec.HostType
		if v, ok := matchExpressions["managedBy"]; !ok || v == "" {
			matchExpressions["managedBy"] = shared.OsacDefaultManagedByValue
		}
		if v, ok := matchExpressions["provisionState"]; !ok || v == "" {
			matchExpressions["provisionState"] = shared.OsacDefaultProvisionStateValue
		}

		inventoryHost, err := r.InventoryClient.FindFreeHost(ctx, matchExpressions)
		if err != nil {
			log.Error(err, "Failed to find a free host", "matchExpressions", matchExpressions)
			return ctrl.Result{}, err
		}
		if inventoryHost == nil {
			log.Info("No matching hosts available", "matchExpressions", matchExpressions)
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			bareMetalInstance.SetStatusCondition(
				v1alpha1.HostConditionAllocated,
				metav1.ConditionFalse,
				"Failed",
				"No matching hosts available",
			)
			return ctrl.Result{RequeueAfter: r.NoFreeHostsPollIntervalDuration}, nil
		}

		bareMetalInstance.Spec.ExternalHostID = inventoryHost.InventoryHostID
		if err := r.Update(ctx, bareMetalInstance); err != nil {
			log.Error(err, "Failed to update BareMetalInstance CR with ExternalHostID", "InventoryHostID", inventoryHost.InventoryHostID)
			return ctrl.Result{}, err
		}

		log.Info("Successfully updated BareMetalInstance with inventory host id")
		return ctrl.Result{}, nil
	}

	hostID := bareMetalInstance.Spec.ExternalHostID
	if !inventory.TryLock(hostID) {
		log.Info("Lock is currently held, retrying", "InventoryHostID", hostID)
		return ctrl.Result{RequeueAfter: r.TryLockFailPollIntervalDuration}, nil
	}
	defer inventory.Unlock(hostID)

	// Build combined labels from spec fields
	// Persistent labels override non-persistent labels with the same key
	combinedLabels := make(map[string]string)

	// First, copy inventory labels
	if bareMetalInstance.Spec.InventoryLabels != nil {
		maps.Copy(combinedLabels, bareMetalInstance.Spec.InventoryLabels)
	}

	// Then copy persistent labels (will override any duplicates)
	if bareMetalInstance.Spec.InventoryPersistentLabels != nil {
		maps.Copy(combinedLabels, bareMetalInstance.Spec.InventoryPersistentLabels)
	}

	if poolID, ok := bareMetalInstance.GetPoolID(); ok {
		combinedLabels[shared.OsacBareMetalPoolIDLabel] = poolID
	}

	inventoryHost, err := r.InventoryClient.AssignHost(
		ctx,
		bareMetalInstance.Spec.ExternalHostID,
		string(bareMetalInstance.UID),
		combinedLabels,
	)
	if err != nil {
		log.Error(err, "Failed to assign host", "InventoryHostID", bareMetalInstance.Spec.ExternalHostID)
		return ctrl.Result{}, err
	}
	if inventoryHost == nil {
		log.Info("Host is acquired by a different BareMetalInstance, unsetting ExternalHostID", "InventoryHostID", hostID)
		bareMetalInstance.Spec.ExternalHostID = ""
		if err = r.Update(ctx, bareMetalInstance); err != nil {
			log.Error(err, "Failed to update BareMetalInstance CR")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	bareMetalInstance.Spec.HostClass = inventoryHost.HostClass
	if err = r.Update(ctx, bareMetalInstance); err != nil {
		log.Error(err, "Failed to update BareMetalInstance CR with HostClass", "HostClass", inventoryHost.HostClass)
		return ctrl.Result{}, err
	}

	// Update status to indicate successful allocation
	bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
	bareMetalInstance.SetStatusCondition(
		v1alpha1.HostConditionAllocated,
		metav1.ConditionTrue,
		"Allocated",
		fmt.Sprintf("BareMetalInstance allocated a host (%s) from %s", bareMetalInstance.Spec.ExternalHostID, inventoryHost.HostClass),
	)

	log.Info("Successfully fulfilled BareMetalInstance", "InventoryHostID", bareMetalInstance.Spec.ExternalHostID)
	return ctrl.Result{}, nil
}

func (r *BareMetalInstanceReconciler) reconcileManagement(ctx context.Context, bareMetalInstance *v1alpha1.BareMetalInstance) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if controllerutil.AddFinalizer(bareMetalInstance, BareMetalInstanceManagementFinalizer) {
		if err := r.Update(ctx, bareMetalInstance); err != nil {
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			return ctrl.Result{}, err
		}
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return ctrl.Result{}, nil
	}

	// Add cleanup finalizer if auto-provisioned ExternalIP resources exist for this BMI
	if err := r.addCleanupFinalizerIfNeeded(ctx, bareMetalInstance); err != nil {
		return ctrl.Result{}, err
	}

	if result, err := r.reconcileNetworkProvisionAndDiscovery(ctx, bareMetalInstance); err != nil || !result.IsZero() {
		return result, err
	}
	if bareMetalInstance.Status.Phase == v1alpha1.BareMetalInstancePhaseFailed {
		return ctrl.Result{}, nil
	}

	// Capture whether power was synced before this reconciliation modifies conditions.
	// Used to detect stale power state reads during rapid stop/start cycles.
	// Nil (never set) is treated as synced — the guard only triggers when a prior
	// reconciliation explicitly set PowerSynced=False (indicating an active transition).
	prevPowerSynced := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
	powerWasPreviouslySynced := prevPowerSynced == nil || prevPowerSynced.Status == metav1.ConditionTrue

	// Handle restart trigger right after provisioning
	if result, err := r.reconcileRestartTrigger(ctx, bareMetalInstance); err != nil || !result.IsZero() {
		if err != nil {
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		} else {
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		}
		return result, err
	}

	powerStatus, err := r.ManagementClient.GetPowerState(ctx, bareMetalInstance.Spec.ExternalHostID)
	if err != nil {
		log.Error(err, "failed to get power state", "nodeID", bareMetalInstance.Spec.ExternalHostID)
		r.syncBareMetalInstanceStatus(bareMetalInstance, nil, err, log)
		return ctrl.Result{}, err
	}
	if powerStatus == nil {
		err := fmt.Errorf("management backend returned nil power status for host %s", bareMetalInstance.Spec.ExternalHostID)
		log.Error(err, "unexpected nil power status", "nodeID", bareMetalInstance.Spec.ExternalHostID)
		r.syncBareMetalInstanceStatus(bareMetalInstance, nil, err, log)
		return ctrl.Result{}, err
	}
	log.V(1).Info("Host power state", "nodeID", bareMetalInstance.Spec.ExternalHostID, "power_state", powerStatus.State)

	powerNeedsRequeue := false
	if bareMetalInstance.Spec.RunStrategy != v1alpha1.RunStrategyUnspecified {
		powerNeedsRequeue, err = r.reconcilePower(ctx, bareMetalInstance, powerStatus, log)
		if err != nil {
			r.syncBareMetalInstanceStatus(bareMetalInstance, nil, err, log)
			return ctrl.Result{}, err
		}

		powerStatus, err = r.ManagementClient.GetPowerState(ctx, bareMetalInstance.Spec.ExternalHostID)
		if err != nil {
			log.Error(err, "failed to refresh power state after reconciliation", "nodeID", bareMetalInstance.Spec.ExternalHostID)
			r.syncBareMetalInstanceStatus(bareMetalInstance, nil, err, log)
			return ctrl.Result{}, err
		}
		if powerStatus == nil {
			err := fmt.Errorf("management backend returned nil power status for host %s", bareMetalInstance.Spec.ExternalHostID)
			log.Error(err, "unexpected nil power status after reconciliation", "nodeID", bareMetalInstance.Spec.ExternalHostID)
			r.syncBareMetalInstanceStatus(bareMetalInstance, nil, err, log)
			return ctrl.Result{}, err
		}
	}

	r.syncBareMetalInstanceStatus(bareMetalInstance, powerStatus, nil, log)

	if bareMetalInstance.Spec.RunStrategy != v1alpha1.RunStrategyUnspecified {
		desiredOn := bareMetalInstance.Spec.RunStrategy == v1alpha1.RunStrategyAlways
		if powerNeedsRequeue || powerStatus.IsTransitioning || desiredOn != (powerStatus.State == management.PowerOn) {
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
			return ctrl.Result{RequeueAfter: r.ManagementRecheckIntervalDuration}, nil
		}

		// Guard against stale power state reads during rapid stop/start cycles.
		// If the previous reconciliation had PowerSynced=False (transition in progress),
		// requeue once more to verify the current "converged" read is accurate.
		if !powerWasPreviouslySynced {
			log.Info("Power appears converged but was recently transitioning, requeueing to verify",
				"runStrategy", bareMetalInstance.Spec.RunStrategy, "powerState", powerStatus.State)
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
			return ctrl.Result{RequeueAfter: r.ManagementRecheckIntervalDuration}, nil
		}
	}

	if err := r.reconcileNICMetadata(ctx, bareMetalInstance); err != nil {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return ctrl.Result{}, err
	}

	bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseReady
	log.Info("BareMetalInstance reconcile completed; status changes pending persistence", "bareMetalInstance", bareMetalInstance.Name)
	return ctrl.Result{}, nil
}

func (r *BareMetalInstanceReconciler) reconcileNICMetadata(ctx context.Context, bmi *v1alpha1.BareMetalInstance) error {
	if bmi.Status.Hardware != nil {
		return nil
	}
	nics, err := r.InventoryClient.GetHostNICs(ctx, bmi.Spec.ExternalHostID)
	if err != nil {
		return err
	}
	hw := &v1alpha1.BareMetalHardware{}
	for _, n := range nics {
		hw.NICs = append(hw.NICs, v1alpha1.BareMetalNICStatus{MAC: n.MAC})
	}
	bmi.Status.Hardware = hw
	return nil
}

// reconcilePower drives the host's power state toward the desired RunStrategy.
// It returns (needsRequeue, error) — needsRequeue is true when a power action was
// taken or the host is mid-transition, signaling the caller to re-check after a delay.
func (r *BareMetalInstanceReconciler) reconcilePower(ctx context.Context, bareMetalInstance *v1alpha1.BareMetalInstance, powerStatus *management.PowerStatus, log logr.Logger) (bool, error) {
	currentlyOn := powerStatus.State == management.PowerOn
	desiredOn := bareMetalInstance.Spec.RunStrategy == v1alpha1.RunStrategyAlways

	if powerStatus.IsTransitioning {
		log.V(1).Info("Node is transitioning, skipping power action",
			"nodeID", bareMetalInstance.Spec.ExternalHostID)
		return true, nil
	}

	needsPowerUpdate := desiredOn != currentlyOn
	if !needsPowerUpdate {
		log.Info("Power state already matches desired", "runStrategy", bareMetalInstance.Spec.RunStrategy, "power_state", powerStatus.State)
		return false, nil
	}

	targetState := management.PowerOff
	action := "off"
	if desiredOn {
		targetState = management.PowerOn
		action = "on"
	}

	log.Info("Powering "+action+" node", "nodeID", bareMetalInstance.Spec.ExternalHostID)
	if err := r.ManagementClient.SetPowerState(ctx, bareMetalInstance.Spec.ExternalHostID, targetState); err != nil {
		if errors.Is(err, management.ErrTransitioning) {
			log.Info("Node is transitioning (conflict), will retry",
				"nodeID", bareMetalInstance.Spec.ExternalHostID)
			return true, nil
		}
		log.Error(err, "failed to power "+action+" node", "nodeID", bareMetalInstance.Spec.ExternalHostID)
		return false, err
	}

	return true, nil
}

func (r *BareMetalInstanceReconciler) reconcileNetworkProvisionAndDiscovery(ctx context.Context, bareMetalInstance *v1alpha1.BareMetalInstance) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 1) Provisioning runs FIRST, while the server is still on the provisioning
	//    network segment (the initial provisioning-network attach done at bootstrap). Networking must
	//    NOT run before provisioning — the tenant must not reach a half-built host.
	//
	//    NOTE: The handoff (provision → move → reboot → discovery) protects INITIAL
	//    provisioning only. NetworkHandoffComplete is never reset, so an in-place
	//    re-provision after Ready (if the provisioning config-version changes) would
	//    run over the tenant network. This is a known short-term limitation; re-provision
	//    handoff-reset is deferred to the long-term design.
	if bareMetalInstance.Spec.TemplateID != "" && bareMetalInstance.Spec.TemplateID != shared.OsacNoopTemplate {
		result, provErr := r.reconcileProvisioning(ctx, bareMetalInstance)
		if provErr != nil {
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			return result, provErr
		}
		if !result.IsZero() {
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
			return result, nil
		}
		provisionCond := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionProvisionTemplateComplete)
		if provisionCond != nil && provisionCond.Status != metav1.ConditionTrue {
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			return ctrl.Result{}, nil
		}
	}

	if len(bareMetalInstance.Spec.NetworkAttachments) == 0 {
		return ctrl.Result{}, nil
	}

	// 2) Move the fabric port provisioning network -> tenant network (post-provision).
	result, netErr := r.reconcileNetworking(ctx, bareMetalInstance)
	if netErr != nil {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return result, netErr
	}
	if !result.IsZero() {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return result, nil
	}
	netCond := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionNetworkAttachmentsReady)
	if netCond == nil || netCond.Status != metav1.ConditionTrue {
		if netCond != nil && netCond.Reason == v1alpha1.HostConditionReasonTemplateFailed {
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			return ctrl.Result{}, nil
		}
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}

	// 3) Reboot so the OS re-DHCPs on the tenant network (single default route).
	result, rbErr := r.reconcileNetworkHandoffReboot(ctx, bareMetalInstance)
	if rbErr != nil {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return result, rbErr
	}
	if !result.IsZero() {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return result, nil
	}

	// 4) Discover the tenant-network DHCP lease (only after handoff reboot).
	result, ipErr := r.reconcileIPDiscovery(ctx, bareMetalInstance)
	if ipErr != nil {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		return result, ipErr
	}
	if !result.IsZero() {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return result, nil
	}
	ipCond := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionIPDiscoveryComplete)
	if ipCond == nil || ipCond.Status != metav1.ConditionTrue {
		if ipCond != nil && ipCond.Reason == v1alpha1.HostConditionReasonTemplateFailed {
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
			return ctrl.Result{}, nil
		}
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseProgressing
		return ctrl.Result{RequeueAfter: r.ProvisionPollIntervalDuration}, nil
	}
	log.Info("Network handoff and IP discovery complete", "bmi", bareMetalInstance.Name)
	return ctrl.Result{}, nil
}

func (r *BareMetalInstanceReconciler) reconcileProvisioning(ctx context.Context, bareMetalInstance *v1alpha1.BareMetalInstance) (ctrl.Result, error) {
	desiredVersion, err := provisioning.ComputeDesiredConfigVersion(struct {
		HostType                  string
		ExternalHostID            string
		ExternalHostName          string
		HostClass                 string
		Selector                  v1alpha1.HostSelectorSpec
		InventoryLabels           map[string]string
		InventoryPersistentLabels map[string]string
		TemplateID                string
		TemplateParameters        string
	}{
		bareMetalInstance.Spec.HostType,
		bareMetalInstance.Spec.ExternalHostID,
		bareMetalInstance.Spec.ExternalHostName,
		bareMetalInstance.Spec.HostClass,
		bareMetalInstance.Spec.Selector,
		bareMetalInstance.Spec.InventoryLabels,
		bareMetalInstance.Spec.InventoryPersistentLabels,
		bareMetalInstance.Spec.TemplateID,
		bareMetalInstance.Spec.TemplateParameters,
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to compute desired config version: %w", err)
	}
	bareMetalInstance.Status.DesiredConfigVersion = desiredVersion

	result, err := provisioning.RunProvisioningLifecycle(ctx, r.ProvisioningProvider, bareMetalInstance,
		&provisioning.State{Jobs: &bareMetalInstance.Status.ProvisioningJobs, DesiredConfigVersion: desiredVersion},
		provisioning.DefaultMaxJobHistory, r.ProvisionPollIntervalDuration,
		&provisioning.PollCallbacks{
			OnFailed: func(message string) {
				bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
				bareMetalInstance.SetStatusCondition(
					v1alpha1.HostConditionProvisionTemplateComplete,
					metav1.ConditionFalse,
					v1alpha1.HostConditionReasonTemplateFailed,
					message,
				)
			},
			OnSuccess: func(_ provisioning.ProvisionStatus) {
				bareMetalInstance.SetStatusCondition(
					v1alpha1.HostConditionProvisionTemplateComplete,
					metav1.ConditionTrue,
					"Succeeded",
					"Provision job completed successfully",
				)
			},
		},
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(
				ctx, r.apiReaderOrClient(), client.ObjectKeyFromObject(bareMetalInstance), &v1alpha1.BareMetalInstance{},
				func(obj client.Object) []opv1alpha1.JobStatus {
					return obj.(*v1alpha1.BareMetalInstance).Status.ProvisioningJobs
				},
			)
		},
		func() error {
			return r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(bareMetalInstance), bareMetalInstance.Status)
		},
	)
	if err != nil {
		return result, err
	}

	// Set progressing condition while provisioning is in-flight, but don't overwrite a failure.
	provisionCond := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionProvisionTemplateComplete)
	if result.RequeueAfter > 0 && (provisionCond == nil || provisionCond.Reason != v1alpha1.HostConditionReasonTemplateFailed) {
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionProvisionTemplateComplete,
			metav1.ConditionFalse,
			v1alpha1.HostConditionReasonProgressing,
			"Provisioning job in progress",
		)
	}

	return result, nil
}

// reconcileNetworkHandoffReboot reboots the host once after its fabric port has
// been moved to the tenant network, so the OS re-DHCPs on the tenant network. It is
// idempotent: it runs only until HostConditionNetworkHandoffComplete is True.
func (r *BareMetalInstanceReconciler) reconcileNetworkHandoffReboot(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if r.NetworkingProvider == nil {
		log.Info("Networking provider not configured, skipping network-handoff reboot")
		bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete,
			metav1.ConditionTrue, "Skipped", "Networking provider not configured")
		return ctrl.Result{}, nil
	}

	handoffCond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete)

	// Already complete — no-op
	if handoffCond != nil && handoffCond.Status == metav1.ConditionTrue {
		return ctrl.Result{}, nil
	}

	// Reboot already triggered (condition exists but is not True) — poll for completion
	if handoffCond != nil {
		completed, err := r.ManagementClient.IsRestartComplete(ctx, bmi.Spec.ExternalHostID)
		if err != nil {
			return ctrl.Result{}, err
		}
		if completed {
			// A reboot we previously triggered has finished.
			bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete,
				metav1.ConditionTrue, "Succeeded", "Host rebooted onto the tenant network")
			return ctrl.Result{}, nil
		}
		// Still rebooting, keep polling
		return ctrl.Result{RequeueAfter: r.ManagementRecheckIntervalDuration}, nil
	}

	// Condition doesn't exist yet — check power state before deciding to trigger.
	// A reboot only helps an already-running OS re-DHCP; an off host will boot fresh
	// on the tenant network when reconcilePower next powers it on, so no reboot is needed.
	powerStatus, err := r.ManagementClient.GetPowerState(ctx, bmi.Spec.ExternalHostID)
	if err != nil {
		return ctrl.Result{}, err
	}
	if powerStatus == nil {
		return ctrl.Result{}, fmt.Errorf("management backend returned nil power status for host %s", bmi.Spec.ExternalHostID)
	}

	if powerStatus.IsTransitioning {
		// Power state is transitioning, requeue without triggering
		return ctrl.Result{RequeueAfter: r.ManagementRecheckIntervalDuration}, nil
	}

	if powerStatus.State == management.PowerOff {
		// Host is powered off — skip reboot. It will boot fresh on the tenant network
		// when reconcilePower next powers it on (if RunStrategy=Always).
		bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete,
			metav1.ConditionTrue, "SkippedPoweredOff",
			"Host powered off; will DHCP on the tenant network at next power-on")
		log.Info("Skipped network-handoff reboot for powered-off host", "host", bmi.Spec.ExternalHostID)
		return ctrl.Result{}, nil
	}

	// Host is powered on — trigger the handoff reboot (exactly once)
	if err := r.ManagementClient.TriggerRestart(ctx, bmi.Spec.ExternalHostID); err != nil {
		if errors.Is(err, management.ErrTransitioning) {
			return ctrl.Result{RequeueAfter: r.ManagementRecheckIntervalDuration}, nil
		}
		return ctrl.Result{}, err
	}
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkHandoffComplete,
		metav1.ConditionFalse, v1alpha1.HostConditionReasonProgressing,
		"Rebooting host onto the tenant network")
	log.Info("Triggered network-handoff reboot", "host", bmi.Spec.ExternalHostID)
	return ctrl.Result{RequeueAfter: r.ManagementRecheckIntervalDuration}, nil
}

// reconcileNetworkOffboardShutdown powers off the host during deletion BEFORE
// the fabric port moves back to the provisioning network. This ensures tenant
// workloads never run on the provisioning network — the workload stops while
// the port is still on the tenant network, and by the time anything boots on
// provisioning network it is the bare-metal management backend's cleaning image, not the tenant OS.
func (r *BareMetalInstanceReconciler) reconcileNetworkOffboardShutdown(
	ctx context.Context, bmi *v1alpha1.BareMetalInstance,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	offboardCond := bmi.GetStatusCondition(v1alpha1.HostConditionNetworkOffboardComplete)

	if offboardCond != nil && offboardCond.Status == metav1.ConditionTrue {
		return ctrl.Result{}, nil
	}

	powerStatus, err := r.ManagementClient.GetPowerState(ctx, bmi.Spec.ExternalHostID)
	if err != nil {
		return ctrl.Result{}, err
	}
	if powerStatus == nil {
		return ctrl.Result{}, fmt.Errorf("management backend returned nil power status for host %s", bmi.Spec.ExternalHostID)
	}

	if powerStatus.State == management.PowerOff && !powerStatus.IsTransitioning {
		bmi.SetStatusCondition(v1alpha1.HostConditionNetworkOffboardComplete,
			metav1.ConditionTrue, "Succeeded",
			"Host powered off; safe to move port to provisioning network")
		log.Info("Offboard shutdown complete", "host", bmi.Spec.ExternalHostID)
		return ctrl.Result{}, nil
	}

	if powerStatus.IsTransitioning {
		log.Info("Host transitioning, waiting for power off", "host", bmi.Spec.ExternalHostID)
		return ctrl.Result{RequeueAfter: r.ManagementRecheckIntervalDuration}, nil
	}

	// Host is powered on — power it off
	if err := r.ManagementClient.SetPowerState(ctx, bmi.Spec.ExternalHostID, management.PowerOff); err != nil {
		if errors.Is(err, management.ErrTransitioning) {
			return ctrl.Result{RequeueAfter: r.ManagementRecheckIntervalDuration}, nil
		}
		return ctrl.Result{}, err
	}
	bmi.SetStatusCondition(v1alpha1.HostConditionNetworkOffboardComplete,
		metav1.ConditionFalse, v1alpha1.HostConditionReasonProgressing,
		"Powering off host before moving port to provisioning network")
	log.Info("Triggered offboard shutdown", "host", bmi.Spec.ExternalHostID)
	return ctrl.Result{RequeueAfter: r.ManagementRecheckIntervalDuration}, nil
}

func (r *BareMetalInstanceReconciler) syncBareMetalInstanceStatus(bareMetalInstance *v1alpha1.BareMetalInstance, powerStatus *management.PowerStatus, reconcileErr error, log logr.Logger) {
	if reconcileErr != nil {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionPowerSynced,
			metav1.ConditionFalse,
			v1alpha1.HostConditionReasonIronicAPIFailure,
			"failed to sync power status",
		)
		log.Error(reconcileErr, "Failed to sync BareMetalInstance power status", "phase", bareMetalInstance.Status.Phase, "condition", v1alpha1.HostConditionPowerSynced)
		return
	}

	if powerStatus == nil {
		return
	}

	poweredOn := powerStatus.State == management.PowerOn
	if poweredOn {
		bareMetalInstance.Status.RunStrategy = v1alpha1.RunStrategyAlways
	} else {
		bareMetalInstance.Status.RunStrategy = v1alpha1.RunStrategyHalted
	}

	if powerStatus.IsTransitioning {
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionPowerSynced,
			metav1.ConditionFalse,
			v1alpha1.HostConditionReasonProgressing,
			"node power state is transitioning",
		)
		return
	}

	if bareMetalInstance.Spec.RunStrategy != v1alpha1.RunStrategyUnspecified && bareMetalInstance.Spec.RunStrategy != bareMetalInstance.Status.RunStrategy {
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionPowerSynced,
			metav1.ConditionFalse,
			v1alpha1.HostConditionReasonProgressing,
			"waiting for node power state to converge",
		)
	} else if poweredOn {
		bareMetalInstance.SetStatusCondition(v1alpha1.HostConditionPowerSynced, metav1.ConditionTrue,
			v1alpha1.HostConditionReasonPowerOn, "")
		log.Info("BareMetalInstance power status synced", "runStrategy", bareMetalInstance.Status.RunStrategy, "condition", v1alpha1.HostConditionPowerSynced, "conditionStatus", metav1.ConditionTrue, "reason", v1alpha1.HostConditionReasonPowerOn)
	} else {
		bareMetalInstance.SetStatusCondition(v1alpha1.HostConditionPowerSynced, metav1.ConditionTrue,
			v1alpha1.HostConditionReasonPowerOff, "")
		log.Info("BareMetalInstance power status synced", "runStrategy", bareMetalInstance.Status.RunStrategy, "condition", v1alpha1.HostConditionPowerSynced, "conditionStatus", metav1.ConditionTrue, "reason", v1alpha1.HostConditionReasonPowerOff)
	}
}

func (r *BareMetalInstanceReconciler) reconcileRestartTrigger(ctx context.Context, bareMetalInstance *v1alpha1.BareMetalInstance) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Check if restart trigger has changed
	if bareMetalInstance.Spec.RestartTrigger == bareMetalInstance.Status.RestartTrigger {
		return ctrl.Result{}, nil
	}

	log.Info("Restart trigger changed",
		"spec", bareMetalInstance.Spec.RestartTrigger,
		"status", bareMetalInstance.Status.RestartTrigger,
		"hostID", bareMetalInstance.Spec.ExternalHostID)

	// If run strategy is halted, just update the status without triggering restart
	if bareMetalInstance.Spec.RunStrategy == v1alpha1.RunStrategyHalted {
		log.Info("RunStrategy is halted, updating restart trigger without restart",
			"trigger", bareMetalInstance.Spec.RestartTrigger)
		bareMetalInstance.Status.RestartTrigger = bareMetalInstance.Spec.RestartTrigger
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionPowerSynced,
			metav1.ConditionTrue,
			"Completed",
			"Restart trigger updated (instance halted)",
		)
		return ctrl.Result{}, nil
	}

	// Check if restart is already in progress
	restartCond := bareMetalInstance.GetStatusCondition(v1alpha1.HostConditionPowerSynced)
	if restartCond != nil && restartCond.Status == metav1.ConditionFalse && restartCond.Reason == v1alpha1.HostConditionReasonProgressing {
		// Check if restart has completed
		if completed, err := r.ManagementClient.IsRestartComplete(ctx, bareMetalInstance.Spec.ExternalHostID); err != nil {
			log.Error(err, "Failed to check restart completion", "hostID", bareMetalInstance.Spec.ExternalHostID)
			bareMetalInstance.SetStatusCondition(
				v1alpha1.HostConditionPowerSynced,
				metav1.ConditionFalse,
				v1alpha1.HostConditionReasonPowerSyncFailed,
				fmt.Sprintf("Failed to check restart completion: %v", err),
			)
			return ctrl.Result{}, err
		} else if completed {
			log.Info("Restart completed successfully", "hostID", bareMetalInstance.Spec.ExternalHostID)
			// Restart completed, update status and continue
		} else {
			// Still in progress, requeue
			log.Info("Restart still in progress", "hostID", bareMetalInstance.Spec.ExternalHostID)
			return ctrl.Result{RequeueAfter: r.ManagementRecheckIntervalDuration}, nil
		}
	} else {
		// No restart in progress, trigger new restart
		result, err := r.triggerRestart(ctx, bareMetalInstance)
		if err != nil {
			bareMetalInstance.SetStatusCondition(
				v1alpha1.HostConditionPowerSynced,
				metav1.ConditionFalse,
				v1alpha1.HostConditionReasonPowerSyncFailed,
				fmt.Sprintf("Failed to trigger restart: %v", err),
			)
			return result, err
		}
		if !result.IsZero() {
			return result, nil
		}
	}

	// Update status to match spec (indicating restart completion)
	bareMetalInstance.Status.RestartTrigger = bareMetalInstance.Spec.RestartTrigger
	bareMetalInstance.SetStatusCondition(
		v1alpha1.HostConditionPowerSynced,
		metav1.ConditionTrue,
		"Completed",
		"Restart trigger updated",
	)

	log.Info("Restart trigger reconciled",
		"trigger", bareMetalInstance.Spec.RestartTrigger,
		"hostID", bareMetalInstance.Spec.ExternalHostID)

	return ctrl.Result{}, nil
}

func (r *BareMetalInstanceReconciler) triggerRestart(ctx context.Context, bareMetalInstance *v1alpha1.BareMetalInstance) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	hostID := bareMetalInstance.Spec.ExternalHostID

	// Trigger restart through management backend
	if err := r.ManagementClient.TriggerRestart(ctx, hostID); err != nil {
		if errors.Is(err, management.ErrTransitioning) {
			log.Info("Host is already transitioning, will retry restart when host becomes idle", "hostID", hostID)
			bareMetalInstance.SetStatusCondition(
				v1alpha1.HostConditionPowerSynced,
				metav1.ConditionFalse,
				v1alpha1.HostConditionReasonPowerSyncFailed,
				"Host is transitioning, will retry restart when host becomes idle",
			)
			return ctrl.Result{RequeueAfter: r.ManagementRecheckIntervalDuration}, nil
		}
		log.Error(err, "Failed to trigger restart", "hostID", hostID)
		return ctrl.Result{}, fmt.Errorf("failed to trigger restart: %w", err)
	}

	log.Info("Successfully triggered restart", "hostID", hostID)
	bareMetalInstance.SetStatusCondition(
		v1alpha1.HostConditionPowerSynced,
		metav1.ConditionFalse,
		v1alpha1.HostConditionReasonProgressing,
		"Restart operation initiated",
	)

	// Requeue to check for restart completion
	return ctrl.Result{RequeueAfter: r.ManagementRecheckIntervalDuration}, nil
}

// handleDeletion frees the host in the inventory and removes the finalizer.
func (r *BareMetalInstanceReconciler) handleDeletion(ctx context.Context, bareMetalInstance *v1alpha1.BareMetalInstance) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Deleting BareMetalInstance")

	// Auto-cleanup: delete auto-provisioned ExternalIP resources first
	result, done, err := r.reconcileAutoCleanup(ctx, bareMetalInstance)
	if err != nil {
		return result, err
	}
	if !done {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseDeleting
		return result, nil
	}

	// Power off BEFORE moving the port back to the provisioning network — ensures tenant
	// workloads never run on the provisioning network. Only needed when networking
	// provisioning is enabled (port was actually moved to the tenant network).
	if len(bareMetalInstance.Spec.NetworkAttachments) > 0 && r.ManagementClient != nil && r.NetworkingProvider != nil {
		result, err = r.reconcileNetworkOffboardShutdown(ctx, bareMetalInstance)
		if err != nil {
			return result, err
		}
		if !result.IsZero() {
			bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseDeleting
			return result, nil
		}
	}

	// Network attachment cleanup (port tenant → provisioning); host is off at this point
	result, done, err = r.reconcileNetworkingDeletion(ctx, bareMetalInstance)
	if err != nil {
		return result, err
	}
	if !done {
		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseDeleting
		return result, nil
	}

	// Management cleanup
	if controllerutil.ContainsFinalizer(bareMetalInstance, BareMetalInstanceManagementFinalizer) {
		log.Info("Running management cleanup", "finalizer", BareMetalInstanceManagementFinalizer)

		bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseDeleting

		if bareMetalInstance.Spec.TemplateID != "" && bareMetalInstance.Spec.TemplateID != shared.OsacNoopTemplate {
			if r.ProvisioningProvider == nil {
				log.Error(nil, "Provisioning provider not configured", "templateID", bareMetalInstance.Spec.TemplateID)
				bareMetalInstance.Status.Phase = v1alpha1.BareMetalInstancePhaseFailed
				bareMetalInstance.SetStatusCondition(
					v1alpha1.HostConditionDeprovisionTemplateComplete,
					metav1.ConditionFalse,
					v1alpha1.HostConditionReasonTemplateFailed,
					"Provisioning provider not configured",
				)
				return ctrl.Result{}, nil
			}

			result, done, err := r.reconcileDeprovisioning(ctx, bareMetalInstance)
			if err != nil {
				return result, err
			}
			if !done {
				return result, nil
			}
		}

		controllerutil.RemoveFinalizer(bareMetalInstance, BareMetalInstanceManagementFinalizer)
		if err := r.Update(ctx, bareMetalInstance); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Management cleanup completed")
	}

	// Inventory cleanup
	if !controllerutil.ContainsFinalizer(bareMetalInstance, BareMetalInstanceInventoryFinalizer) {
		log.Info("No inventory finalizer present, deletion complete")
		return ctrl.Result{}, nil
	}

	hostID := bareMetalInstance.Spec.ExternalHostID
	if hostID != "" {
		log.Info("Unassigning host from inventory", "InventoryHostID", bareMetalInstance.Spec.ExternalHostID)

		if !inventory.TryLock(hostID) {
			log.Info("Could not acquire lock for host", "InventoryHostID", hostID)
			return ctrl.Result{RequeueAfter: r.TryLockFailPollIntervalDuration}, nil
		}
		defer inventory.Unlock(hostID)

		// Collect non-persistent inventory labels to remove
		var labelsToRemove []string
		if bareMetalInstance.Spec.InventoryLabels != nil {
			labelsToRemove = make([]string, 0, len(bareMetalInstance.Spec.InventoryLabels))
			for key := range bareMetalInstance.Spec.InventoryLabels {
				labelsToRemove = append(labelsToRemove, key)
			}
		}
		if _, ok := bareMetalInstance.GetPoolID(); ok {
			labelsToRemove = append(labelsToRemove, shared.OsacBareMetalPoolIDLabel)
		}

		err := r.InventoryClient.UnassignHost(ctx, hostID, labelsToRemove)
		if err != nil {
			log.Error(err, "Failed to unassign host in inventory")
			return ctrl.Result{}, err
		}
	}

	if controllerutil.RemoveFinalizer(bareMetalInstance, BareMetalInstanceInventoryFinalizer) {
		if err := r.Update(ctx, bareMetalInstance); err != nil {
			log.Error(err, "Failed to remove finalizer")
			return ctrl.Result{}, err
		}
	}

	log.Info("Successfully un-fulfilled BareMetalInstance")
	return ctrl.Result{}, nil
}

func (r *BareMetalInstanceReconciler) reconcileDeprovisioning(ctx context.Context, bareMetalInstance *v1alpha1.BareMetalInstance) (ctrl.Result, bool, error) {
	if bareMetalInstance.Status.ProvisioningJobs == nil {
		bareMetalInstance.Status.ProvisioningJobs = []opv1alpha1.JobStatus{}
	}

	result, done, err := provisioning.RunDeprovisioningLifecycle(
		ctx, r.ProvisioningProvider, bareMetalInstance,
		&bareMetalInstance.Status.ProvisioningJobs, provisioning.DefaultMaxJobHistory, r.ProvisionPollIntervalDuration,
	)
	// DeprovisionSkipped is represented as !done + zero result + nil error; treat as done.
	if !done && result.IsZero() && err == nil {
		done = true
	}
	if err != nil {
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionDeprovisionTemplateComplete,
			metav1.ConditionFalse,
			v1alpha1.HostConditionReasonTemplateFailed,
			"Deprovision job failed",
		)
		return result, false, err
	}
	if done {
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionDeprovisionTemplateComplete,
			metav1.ConditionTrue,
			"Succeeded",
			"Deprovision job completed successfully",
		)
	} else {
		bareMetalInstance.SetStatusCondition(
			v1alpha1.HostConditionDeprovisionTemplateComplete,
			metav1.ConditionFalse,
			v1alpha1.HostConditionReasonProgressing,
			"Deprovision job in progress",
		)
	}
	return result, done, nil
}
