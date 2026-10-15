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

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mc "sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	osacExternalIPFinalizer = "osac.openshift.io/externalip-finalizer"
)

// ExternalIPReconciler reconciles ExternalIP CRs created by the fulfillment-service.
//
// Each ExternalIP belongs to a parent ExternalIPPool (referenced by UUID in spec.pool).
// The controller adds a finalizer, resolves the implementation strategy exclusively
// from the deployment NetworkClass singleton via the dispatcher, then delegates to the shared
// provisioning lifecycle to trigger AAP jobs for allocation and deallocation.
// When no manager is configured the controller blocks with ReasonNoManagerConfigured
// rather than falling back to a hardcoded default.
//
// Attach/detach is handled by the ExternalIPAttachment controller.
//
// Phase transitions: "" -> Progressing -> Ready/Failed; on delete: Deleting.
type ExternalIPReconciler struct {
	client.Client
	APIReader            client.Reader
	Scheme               *runtime.Scheme
	mgr                  mcmanager.Manager
	NetworkingNamespace  string
	ProvisioningProvider provisioning.ProvisioningProvider
	StatusPollInterval   time.Duration
	MaxJobHistory        int
	targetCluster        mc.ClusterName
	// Resolver resolves a NetworkClass to its registered managers. Nil when the
	// two-manager model isn't configured (no gRPC connection / networking namespace),
	// in which case the controller always uses the legacy implementation-strategy path.
	Resolver *dispatcher.Resolver
	// networkClassesClient lists NetworkClasses to find the deployment singleton used
	// as the dispatcher input. Nil when gRPC is not configured.
	networkClassesClient privatev1.NetworkClassesClient
	// NetworkProvisioningEnabled controls whether the controller dispatches AAP
	// provisioning jobs. When false, resources are set to Ready immediately
	// with a placeholder address.
	NetworkProvisioningEnabled bool
}

// NewExternalIPReconciler creates a new reconciler for ExternalIP resources.
func NewExternalIPReconciler(
	mgr mcmanager.Manager,
	networkingNamespace string,
	provisioningProvider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration,
	maxJobHistory int,
	targetCluster mc.ClusterName,
	resolver *dispatcher.Resolver,
	networkClassesClient privatev1.NetworkClassesClient,
) *ExternalIPReconciler {
	if mgr == nil {
		panic("mgr must not be nil")
	}
	if statusPollInterval <= 0 {
		statusPollInterval = provisioning.DefaultStatusPollInterval
	}
	if maxJobHistory <= 0 {
		maxJobHistory = provisioning.DefaultMaxJobHistory
	}
	return &ExternalIPReconciler{
		Client:               mgr.GetLocalManager().GetClient(),
		APIReader:            mgr.GetLocalManager().GetAPIReader(),
		Scheme:               mgr.GetLocalManager().GetScheme(),
		mgr:                  mgr,
		NetworkingNamespace:  networkingNamespace,
		ProvisioningProvider: provisioningProvider,
		StatusPollInterval:   statusPollInterval,
		MaxJobHistory:        maxJobHistory,
		targetCluster:        targetCluster,
		Resolver:             resolver,
		networkClassesClient: networkClassesClient,
	}
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=externalips,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=externalips/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=externalips/finalizers,verbs=update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=externalippools,verbs=get;list;watch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=externalipattachments,verbs=list
// +kubebuilder:rbac:groups=osac.openshift.io,resources=natgateways,verbs=list
// +kubebuilder:rbac:groups="",resources=services,verbs=get

// Reconcile handles create/update/delete for a ExternalIP CR.
// On create/update it ensures a finalizer, resolves the parent pool, and runs provisioning.
// On delete it triggers deprovisioning and removes the finalizer when complete.
func (r *ExternalIPReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	externalIP := &v1alpha1.ExternalIP{}
	err := r.Get(ctx, req.NamespacedName, externalIP)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Skip unmanaged resources, but still allow deletion to proceed
	val, exists := externalIP.Annotations[osacManagementStateAnnotation]
	if externalIP.ObjectMeta.DeletionTimestamp.IsZero() && exists && val == ManagementStateUnmanaged {
		log.Info("ignoring ExternalIP due to management-state annotation", "management-state", val)
		return ctrl.Result{}, nil
	}

	log.Info("start reconcile", "pool", externalIP.Spec.Pool, "phase", externalIP.Status.Phase)

	oldstatus := externalIP.Status.DeepCopy()
	hadFinalizer := controllerutil.ContainsFinalizer(externalIP, osacExternalIPFinalizer)

	var res ctrl.Result
	if externalIP.ObjectMeta.DeletionTimestamp.IsZero() {
		res, err = r.handleUpdate(ctx, externalIP)
	} else {
		res, err = r.handleDelete(ctx, externalIP)
	}

	statusPersistedBeforeFinalizerRemoval := !externalIP.ObjectMeta.DeletionTimestamp.IsZero() &&
		hadFinalizer && !controllerutil.ContainsFinalizer(externalIP, osacExternalIPFinalizer)
	if !statusPersistedBeforeFinalizerRemoval && !equality.Semantic.DeepEqual(externalIP.Status, *oldstatus) {
		log.Info("status requires update", "phase", externalIP.Status.Phase)
		if updateErr := r.updateStatusWithRetry(ctx, req.NamespacedName, externalIP.Status); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return res, updateErr
		}
	}

	log.Info("end reconcile", "phase", externalIP.Status.Phase)
	return res, err
}

func (r *ExternalIPReconciler) updateStatusWithRetry(ctx context.Context, key client.ObjectKey, computed v1alpha1.ExternalIPStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.ExternalIP{}
		if err := r.APIReader.Get(ctx, key, latest); err != nil {
			return err
		}
		latest.Status = computed
		return r.Status().Update(ctx, latest)
	})
}

// handleUpdate processes a non-deleted ExternalIP: adds finalizer, resolves the parent
// ExternalIPPool, inherits the implementation strategy, and runs provisioning.
func (r *ExternalIPReconciler) handleUpdate(ctx context.Context, externalIP *v1alpha1.ExternalIP) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// Add finalizer if not present
	if controllerutil.AddFinalizer(externalIP, osacExternalIPFinalizer) {
		log.Info("adding finalizer")
		if err := r.Update(ctx, externalIP); err != nil {
			return ctrl.Result{}, err
		}
		// Re-fetch to get the latest resourceVersion after the metadata update
		if err := r.Get(ctx, client.ObjectKeyFromObject(externalIP), externalIP); err != nil {
			return ctrl.Result{}, err
		}
	}

	if externalIP.Status.Phase == "" {
		externalIP.Status.Phase = v1alpha1.ExternalIPPhaseProgressing
		setExternalIPState(&externalIP.Status, v1alpha1.ExternalIPStatePending)
	}

	// When networking provisioning is disabled, skip AAP job dispatch and set Ready
	// immediately with a placeholder address. The placeholder ensures
	// ExternalIPAttachment's address check doesn't block.
	if !r.NetworkProvisioningEnabled {
		setExternalIPState(&externalIP.Status, v1alpha1.ExternalIPStateAllocated)
		externalIP.Status.Address = "0.0.0.0"
		externalIP.Status.Phase = v1alpha1.ExternalIPPhaseReady
		setReadyConditionTrue(&externalIP.Status.Conditions)
		return ctrl.Result{}, nil
	}

	// Resolve the parent ExternalIPPool by the fulfillment-service UUID stored in spec.pool.
	// The fulfillment-service creates pool CRs with a UUID label; spec.pool contains that
	// UUID, not the K8s object name.
	poolList := &v1alpha1.ExternalIPPoolList{}
	err := r.List(ctx, poolList,
		client.InNamespace(externalIP.Namespace),
		client.MatchingLabels{osacExternalIPPoolIDLabel: externalIP.Spec.Pool},
	)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(poolList.Items) == 0 {
		log.Info("parent ExternalIPPool not found, requeueing", "poolUUID", externalIP.Spec.Pool)
		return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	pool := &poolList.Items[0]
	log.Info("resolved parent ExternalIPPool", "poolName", pool.Name, "poolUUID", externalIP.Spec.Pool)

	// Resolve implementation strategy exclusively from the default NetworkClass
	// via the dispatcher. When no manager is configured the controller blocks
	// with ReasonNoManagerConfigured instead of falling back to the parent
	// pool's spec field.
	networkClassID, err := lookupDefaultNetworkClassID(ctx, r.networkClassesClient)
	if err != nil {
		return ctrl.Result{}, err
	}
	implementationStrategy, err := resolveImplementationStrategy(
		ctx, r.Resolver, "ExternalIP", networkClassID)
	if err != nil {
		return ctrl.Result{}, err
	}
	if implementationStrategy == "" {
		msg := fmt.Sprintf("no fabric_manager or k8s_manager configured for ExternalIP (default NetworkClass %q)", networkClassID)
		setReadyConditionBlocked(&externalIP.Status.Conditions, v1alpha1.ReasonNoManagerConfigured, msg)
		log.Info("implementation strategy not set", "externalIP", externalIP.Name)
		return ctrl.Result{}, fmt.Errorf("cannot reconcile ExternalIP %q: %s", externalIP.Name, msg)
	}

	if externalIP.Annotations == nil {
		externalIP.Annotations = make(map[string]string)
	}

	// Annotate the CR so AAP playbooks can select the appropriate role without
	// having to look up the parent pool themselves.
	needsUpdate := false
	if externalIP.Annotations[osacImplementationStrategyAnnotation] != implementationStrategy {
		externalIP.Annotations[osacImplementationStrategyAnnotation] = implementationStrategy
		log.Info("setting implementation-strategy annotation", "strategy", implementationStrategy)
		needsUpdate = true
	}
	if externalIP.Annotations[osacExternalIPPoolNameAnnotation] != pool.Name {
		externalIP.Annotations[osacExternalIPPoolNameAnnotation] = pool.Name
		log.Info("setting externalippool-name annotation", "poolName", pool.Name)
		needsUpdate = true
	}
	if needsUpdate {
		if err := r.Update(ctx, externalIP); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Compute desired config version from spec and inherited implementation strategy.
	// This hash drives the provisioning lifecycle: a new version triggers re-provisioning.
	desiredVersion, err := provisioning.ComputeDesiredConfigVersion(struct {
		Spec                   v1alpha1.ExternalIPSpec
		ImplementationStrategy string
	}{externalIP.Spec, implementationStrategy})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to compute desired config version: %w", err)
	}
	externalIP.Status.DesiredConfigVersion = desiredVersion

	v1alpha1.SetExternalIPStatusCondition(externalIP, metav1.Condition{
		Type:               string(v1alpha1.ExternalIPConditionConfigurationApplied),
		Status:             metav1.ConditionTrue,
		Reason:             conditionReasonConfigurationApplied,
		Message:            conditionMessageConfigurationApplied,
		LastTransitionTime: metav1.Now(),
	})

	// Transition to Progressing on first provision or when spec changed after a previous
	// success. Don't override Failed during backoff (the provisioning lifecycle handles retry).
	if externalIP.Status.Phase == "" || (externalIP.Status.Phase == v1alpha1.ExternalIPPhaseReady &&
		!provisioning.IsConfigApplied(&externalIP.Status.ProvisioningJobs, externalIP.Status.DesiredConfigVersion)) {
		externalIP.Status.Phase = v1alpha1.ExternalIPPhaseProgressing
		if externalIP.Status.State == "" {
			setExternalIPState(&externalIP.Status, v1alpha1.ExternalIPStatePending)
		}
	}

	result, err := r.handleProvisioning(ctx, externalIP)
	if err != nil || result.RequeueAfter > 0 {
		return result, err
	}

	if externalIP.Status.State == v1alpha1.ExternalIPStateAllocated && externalIP.Status.Address == "" {
		message := "ExternalIP is marked allocated but has no validated address; allocation must be retried"
		externalIP.Status.Phase = v1alpha1.ExternalIPPhaseFailed
		setExternalIPState(&externalIP.Status, v1alpha1.ExternalIPStateFailed)
		setReadyConditionFailed(&externalIP.Status.Conditions, message)
		if latestJob := provisioning.FindLatestJobByType(externalIP.Status.ProvisioningJobs, v1alpha1.JobTypeProvision); latestJob != nil && latestJob.State.IsSuccessful() {
			failedJob := *latestJob
			failedJob.State = v1alpha1.JobStateFailed
			failedJob.Message = message
			provisioning.UpdateJob(externalIP.Status.ProvisioningJobs, failedJob)
		}
		return ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
	}

	return result, nil
}

// handleDelete sets the Deleting phase, runs deprovisioning, and removes the finalizer
// once deprovisioning completes (or is skipped).
func (r *ExternalIPReconciler) handleDelete(ctx context.Context, externalIP *v1alpha1.ExternalIP) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("deleting external IP")

	statusChanged := setExternalIPDeleting(&externalIP.Status)

	if !controllerutil.ContainsFinalizer(externalIP, osacExternalIPFinalizer) {
		return ctrl.Result{}, nil
	}
	if statusChanged {
		if err := r.Status().Update(ctx, externalIP); err != nil {
			return ctrl.Result{}, err
		}
		latest := &v1alpha1.ExternalIP{}
		if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(externalIP), latest); err != nil {
			return ctrl.Result{}, err
		}
		*externalIP = *latest
	}

	// Gate: wait for all child resources referencing this ExternalIP to be fully removed.
	eipName := externalIP.Name
	ns := externalIP.Namespace

	attachmentList := &v1alpha1.ExternalIPAttachmentList{}
	if err := r.List(ctx, attachmentList, client.InNamespace(ns)); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing ExternalIPAttachments: %w", err)
	}
	for i := range attachmentList.Items {
		if attachmentList.Items[i].Spec.ExternalIP == eipName {
			log.Info("waiting for child ExternalIPAttachment to be deleted before deprovisioning ExternalIP",
				"attachment", attachmentList.Items[i].Name)
			return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
		}
	}

	natgwList := &v1alpha1.NATGatewayList{}
	if err := r.List(ctx, natgwList, client.InNamespace(ns)); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing NATGateways: %w", err)
	}
	for i := range natgwList.Items {
		if natgwList.Items[i].Spec.ExternalIP == eipName {
			log.Info("waiting for child NATGateway to be deleted before deprovisioning ExternalIP",
				"natGateway", natgwList.Items[i].Name)
			return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
		}
	}

	if externalIP.Annotations[osacImplementationStrategyAnnotation] == "" {
		log.Info("skipping deprovisioning — resource was never provisioned")
	} else {
		result, err := r.handleDeprovisioning(ctx, externalIP)
		if err != nil || result.RequeueAfter > 0 {
			return result, err
		}
	}

	// Deprovisioning complete, remove finalizer to allow K8s garbage collection
	log.Info("removing finalizer after successful deprovisioning")
	controllerutil.RemoveFinalizer(externalIP, osacExternalIPFinalizer)
	if err := r.Update(ctx, externalIP); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// handleProvisioning delegates to the shared provisioning lifecycle, which triggers
// an AAP job (e.g., osac-create-external-ip) and polls its status until completion.
func (r *ExternalIPReconciler) handleProvisioning(ctx context.Context, externalIP *v1alpha1.ExternalIP) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	if r.ProvisioningProvider == nil {
		log.Info("no provisioning provider configured, skipping provisioning")
		return ctrl.Result{}, nil
	}

	return provisioning.RunProvisioningLifecycle(ctx, r.ProvisioningProvider, externalIP,
		&provisioning.State{Jobs: &externalIP.Status.ProvisioningJobs, DesiredConfigVersion: externalIP.Status.DesiredConfigVersion},
		r.MaxJobHistory, r.StatusPollInterval,
		&provisioning.PollCallbacks{
			RetryOnOutputError: true,
			OnFailed: func(message string) {
				externalIP.Status.Phase = v1alpha1.ExternalIPPhaseFailed
				setExternalIPState(&externalIP.Status, v1alpha1.ExternalIPStateFailed)
				externalIP.Status.Address = ""
				setReadyConditionFailed(&externalIP.Status.Conditions, message)
			},
			OnOutputError: func(message string) {
				externalIP.Status.Phase = v1alpha1.ExternalIPPhaseFailed
				setExternalIPState(&externalIP.Status, v1alpha1.ExternalIPStateFailed)
				externalIP.Status.Address = ""
				setReadyConditionFailed(&externalIP.Status.Conditions, message)
			},
			OnSuccessWithExtraVars: func(status provisioning.ProvisionStatusWithExtraVars) error {
				return r.acceptAllocationResult(ctx, externalIP, status.ExtraVars)
			},
		},
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(
				ctx, r.APIReader, client.ObjectKeyFromObject(externalIP), &v1alpha1.ExternalIP{}, func(obj client.Object) []v1alpha1.JobStatus {
					return obj.(*v1alpha1.ExternalIP).Status.ProvisioningJobs
				})
		},
		func() error {
			return r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(externalIP), externalIP.Status)
		},
	)
}

// handleDeprovisioning triggers an AAP deprovisioning job (e.g., osac-delete-external-ip)
// and polls its status. On failure, it either blocks deletion (to prevent orphaned
// resources) or allows the process to continue, depending on provider policy.
func (r *ExternalIPReconciler) handleDeprovisioning(ctx context.Context, externalIP *v1alpha1.ExternalIP) (ctrl.Result, error) {
	if r.ProvisioningProvider == nil {
		ctrllog.FromContext(ctx).Info("no provisioning provider configured, skipping deprovisioning")
		return ctrl.Result{}, nil
	}
	result, done, err := provisioning.RunDeprovisioningLifecycleWithCallbacks(ctx, r.ProvisioningProvider, externalIP,
		&externalIP.Status.ProvisioningJobs, r.MaxJobHistory, r.StatusPollInterval,
		&provisioning.DeprovisionCallbacks{
			OnOutputError: func(message string) {
				ctrllog.FromContext(ctx).Error(fmt.Errorf("%s", message), "ExternalIP release result was rejected")
			},
			OnSuccessWithExtraVars: func(status provisioning.ProvisionStatusWithExtraVars) error {
				_, err := r.validateExternalIPResult(ctx, externalIP, status.ExtraVars, provisioning.ExternalIPOperationRelease)
				return err
			},
		})
	if err != nil || !done {
		return result, err
	}
	return ctrl.Result{}, nil
}

func (r *ExternalIPReconciler) acceptAllocationResult(ctx context.Context, externalIP *v1alpha1.ExternalIP, artifacts map[string]any) error {
	current, err := r.validateExternalIPResult(ctx, externalIP, artifacts, provisioning.ExternalIPOperationAllocate)
	if err != nil {
		return err
	}

	pools := &v1alpha1.ExternalIPPoolList{}
	if err := r.APIReader.List(ctx, pools,
		client.InNamespace(current.Namespace),
		client.MatchingLabels{osacExternalIPPoolIDLabel: current.Spec.Pool},
	); err != nil {
		return fmt.Errorf("listing ExternalIPPool for allocation validation: %w", err)
	}
	if len(pools.Items) != 1 {
		return fmt.Errorf("expected exactly one ExternalIPPool for UUID %q, found %d", current.Spec.Pool, len(pools.Items))
	}
	address := current.Annotations[osacExternalIPAllocatedAddressAnnotation]
	if err := validateAllocatedAddress(address, pools.Items[0].Spec.CIDRs); err != nil {
		return err
	}

	externalIP.Status.Address = address
	externalIP.Status.Phase = v1alpha1.ExternalIPPhaseReady
	setExternalIPState(&externalIP.Status, v1alpha1.ExternalIPStateAllocated)
	setReadyConditionTrue(&externalIP.Status.Conditions)
	return nil
}

func (r *ExternalIPReconciler) validateExternalIPResult(ctx context.Context, externalIP *v1alpha1.ExternalIP,
	artifacts map[string]any, expectedOperation provisioning.ExternalIPOperation) (*v1alpha1.ExternalIP, error) {
	outcome, err := provisioning.ParseExternalIPResult(artifacts)
	if err != nil {
		return nil, err
	}
	if outcome.Operation != expectedOperation {
		return nil, fmt.Errorf("ExternalIP result operation %q does not match expected operation %q", outcome.Operation, expectedOperation)
	}
	if outcome.ResourceUID != string(externalIP.UID) {
		return nil, fmt.Errorf("ExternalIP result UID %q does not match current UID %q", outcome.ResourceUID, externalIP.UID)
	}
	if outcome.Generation != externalIP.Generation {
		return nil, fmt.Errorf("ExternalIP result generation %d does not match dispatched generation %d", outcome.Generation, externalIP.Generation)
	}

	current := &v1alpha1.ExternalIP{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(externalIP), current); err != nil {
		return nil, fmt.Errorf("reading current ExternalIP before accepting manager result: %w", err)
	}
	if current.UID != externalIP.UID || string(current.UID) != outcome.ResourceUID {
		return nil, fmt.Errorf("ExternalIP identity changed before manager result was accepted")
	}
	if current.Generation != outcome.Generation {
		return nil, fmt.Errorf("ExternalIP generation changed before manager result was accepted")
	}
	return current, nil
}

// SetupWithManager registers this controller with the multicluster manager.
// It watches ExternalIP CRs in the networking namespace on the local cluster only.
func (r *ExternalIPReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mgr).
		For(&v1alpha1.ExternalIP{},
			mcbuilder.WithPredicates(NetworkingNamespacePredicate(r.NetworkingNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Complete(r)
}
