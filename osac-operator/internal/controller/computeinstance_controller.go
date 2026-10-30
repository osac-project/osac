/*
Copyright 2025.

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
	"net/netip"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mc "sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	kubevirtv1 "kubevirt.io/api/core/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const (
	// defaultPreconditionRequeueInterval is the requeue delay when a precondition for
	// reconciliation is not yet met (e.g. parent resource not found, configuration
	// not populated, or dependent resource not in a ready state)
	defaultPreconditionRequeueInterval = 10 * time.Second
)

// errSubnetNotFound is returned when the Subnet CR referenced by the primary
// subnet (networkAttachments[0].subnetRef) does not exist.
// handleUpdate treats this as a transient error and requeues with a fixed delay
// instead of exponential backoff.
var errSubnetNotFound = errors.New("subnet CR not found")

// ComputeInstanceReconciler reconciles a ComputeInstance object
type ComputeInstanceReconciler struct {
	client.Client
	Scheme                   *runtime.Scheme
	Recorder                 events.EventRecorder
	mgr                      mcmanager.Manager
	ComputeInstanceNamespace string
	TenantNamespace          string
	NetworkingNamespace      string
	ProvisioningProvider     provisioning.ProvisioningProvider
	// StatusPollInterval defines how often to check provisioning job status
	StatusPollInterval time.Duration
	// MaxJobHistory defines how many jobs to keep per job array
	MaxJobHistory int
	targetCluster mc.ClusterName
	// TiersClient and BackendsClient query the fulfillment service Tier API for
	// JIT storage provisioning extra_vars. When nil, tier/backend context
	// injection is skipped — backward compatible with environments without a
	// fulfillment service connection. See resolveAndPartitionTierContext.
	TiersClient    StorageTiersLister
	BackendsClient StorageBackendsClient
	// SecretsClient resolves a storage backend's password from a referenced Secret
	// when the backend supplies password_secret instead of an inline password. When
	// nil, backends that rely on password_secret are skipped. See resolveAndPartitionTierContext.
	SecretsClient SecretsClient
}

func NewComputeInstanceReconciler(
	mgr mcmanager.Manager,
	computeInstanceNamespace string,
	tenantNamespace string,
	networkingNamespace string,
	provisioningProvider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration,
	maxJobHistory int,
	targetCluster mc.ClusterName,
) *ComputeInstanceReconciler {
	if mgr == nil {
		panic("mgr must not be nil")
	}

	if computeInstanceNamespace == "" {
		computeInstanceNamespace = defaultComputeInstanceNamespace
	}

	if statusPollInterval <= 0 {
		statusPollInterval = provisioning.DefaultStatusPollInterval
	}

	if maxJobHistory <= 0 {
		maxJobHistory = provisioning.DefaultMaxJobHistory
	}

	return &ComputeInstanceReconciler{
		Client:                   mgr.GetLocalManager().GetClient(),
		Scheme:                   mgr.GetLocalManager().GetScheme(),
		Recorder:                 mgr.GetLocalManager().GetEventRecorder(computeInstanceControllerName),
		mgr:                      mgr,
		ComputeInstanceNamespace: computeInstanceNamespace,
		TenantNamespace:          tenantNamespace,
		NetworkingNamespace:      networkingNamespace,
		ProvisioningProvider:     provisioningProvider,
		StatusPollInterval:       statusPollInterval,
		MaxJobHistory:            maxJobHistory,
		targetCluster:            targetCluster,
	}
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=computeinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=computeinstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=computeinstances/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachines;virtualmachineinstances,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ComputeInstanceReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	instance := &v1alpha1.ComputeInstance{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	val, exists := instance.Annotations[osacComputeInstanceManagementStateAnnotation]
	if instance.ObjectMeta.DeletionTimestamp.IsZero() && exists && val == ManagementStateUnmanaged {
		log.Info("ignoring ComputeInstance due to management-state annotation", "management-state", val)
		return ctrl.Result{}, nil
	}

	log.Info("start reconcile")

	oldstatus := instance.Status.DeepCopy()

	var res ctrl.Result
	if instance.ObjectMeta.DeletionTimestamp.IsZero() {
		res, err = r.handleUpdate(ctx, req.Request, instance)
	} else {
		res, err = r.handleDelete(ctx, req.Request, instance)
	}

	if !equality.Semantic.DeepEqual(instance.Status, *oldstatus) {
		log.Info("status requires update")
		if err := r.updateStatusWithRetry(ctx, req.NamespacedName, instance.Status); err != nil {
			return res, err
		}
	}

	log.Info("end reconcile")
	return res, err
}

// updateStatusWithRetry updates the instance status with retry on conflict.
// This prevents duplicate job triggers when status updates fail due to optimistic concurrency conflicts.
func (r *ComputeInstanceReconciler) updateStatusWithRetry(ctx context.Context, key client.ObjectKey, newStatus v1alpha1.ComputeInstanceStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Fetch latest version to get current resourceVersion
		latest := &v1alpha1.ComputeInstance{}
		if err := r.Get(ctx, key, latest); err != nil {
			return err
		}
		// Copy status updates to the latest version
		latest.Status = newStatus
		if !latest.DeletionTimestamp.IsZero() {
			// Provisioning can finish after the API has accepted deletion. Keep
			// the provider job history for cleanup, but don't publish stale
			// Running/Ready state over the deletion state.
			latest.Status.Phase = v1alpha1.ComputeInstancePhaseDeleting
			latest.SetStatusCondition(v1alpha1.ComputeInstanceConditionReady, metav1.ConditionFalse,
				"ComputeInstance is being deleted", v1alpha1.ReasonDeleting)
		}
		// Attempt to update with fresh resourceVersion
		return r.Status().Update(ctx, latest)
	})
}

func ComputeInstanceNamespacePredicate(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(
		func(obj client.Object) bool {
			return obj.GetNamespace() == namespace
		},
	)
}

// tenantAnnotationIndexField is the field path used for cache index and List MatchingFields.
var tenantAnnotationIndexField = fmt.Sprintf("metadata.annotations.%s", osacTenantKey)

// SetupWithManager sets up the controller with the Manager.
func (r *ComputeInstanceReconciler) SetupWithManager(mgr mcmanager.Manager) error {

	// Index tenant annotation in order to list compute instances by tenant
	ctx := context.Background()
	localMgr := mgr.GetLocalManager()
	if err := localMgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.ComputeInstance{}, tenantAnnotationIndexField,
		func(obj client.Object) []string {
			if v, ok := obj.GetAnnotations()[osacTenantKey]; ok {
				return []string{v}
			}
			return nil
		}); err != nil {
		return fmt.Errorf("failed to index ComputeInstance by tenant annotation: %w", err)
	}

	labelPredicate, err := predicate.LabelSelectorPredicate(metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      osacComputeInstanceNameLabel,
				Operator: metav1.LabelSelectorOpExists,
			},
		},
	})
	if err != nil {
		return err
	}

	return mcbuilder.ControllerManagedBy(mgr).
		For(&v1alpha1.ComputeInstance{},
			mcbuilder.WithPredicates(ComputeInstanceNamespacePredicate(r.ComputeInstanceNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Watches(
			&kubevirtv1.VirtualMachine{},
			mchandler.EnqueueRequestsFromMapFunc(r.mapObjectToComputeInstance),
			mcbuilder.WithPredicates(labelPredicate),
		).
		Watches(
			&kubevirtv1.VirtualMachineInstance{},
			mchandler.EnqueueRequestsFromMapFunc(r.mapObjectToComputeInstance),
			mcbuilder.WithPredicates(labelPredicate),
		).
		Watches(
			&v1alpha1.Tenant{},
			mchandler.EnqueueRequestsFromMapFunc(r.mapTenantToComputeInstances),
			mcbuilder.WithPredicates(ComputeInstanceNamespacePredicate(r.ComputeInstanceNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false),
		).
		Complete(r)
}

// mapObjectToComputeInstance maps an event for a watched object to the associated
// ComputeInstance resource.
func (r *ComputeInstanceReconciler) mapObjectToComputeInstance(ctx context.Context, obj client.Object) []reconcile.Request {
	log := ctrllog.FromContext(ctx)

	computeInstanceName, exists := obj.GetLabels()[osacComputeInstanceNameLabel]
	if !exists {
		return nil
	}

	// Verify that the referenced ComputeInstance exists in this controller's namespace
	// to filter out notifications for resources managed by other controller instances
	computeInstance := &v1alpha1.ComputeInstance{}
	key := client.ObjectKey{
		Name:      computeInstanceName,
		Namespace: r.ComputeInstanceNamespace,
	}
	if err := r.Get(ctx, key, computeInstance); err != nil {
		// ComputeInstance doesn't exist in our namespace, ignore this notification
		log.V(2).Info("ignoring notification for resource not managed by this controller instance",
			"kind", obj.GetObjectKind().GroupVersionKind().Kind,
			"namespace", obj.GetNamespace(),
			"name", obj.GetName(),
			"computeinstance", computeInstanceName,
			"controller_namespace", r.ComputeInstanceNamespace,
		)
		return nil
	}

	log.Info("mapped change notification",
		"kind", obj.GetObjectKind().GroupVersionKind().Kind,
		"namespace", obj.GetNamespace(),
		"name", obj.GetName(),
		"computeinstance", computeInstanceName,
	)

	return []reconcile.Request{
		{
			NamespacedName: key,
		},
	}
}

func (r *ComputeInstanceReconciler) mapTenantToComputeInstances(ctx context.Context, obj client.Object) []reconcile.Request {
	log := ctrllog.FromContext(ctx)

	// Get all compute instances matching the tenant reference
	computeInstances := &v1alpha1.ComputeInstanceList{}
	err := r.List(ctx,
		computeInstances,
		client.InNamespace(r.ComputeInstanceNamespace),
		client.MatchingFields{tenantAnnotationIndexField: obj.GetName()})
	if err != nil {
		log.Error(err, "failed to list compute instances", "annotationKey", tenantAnnotationIndexField, "tenant", obj.GetName())
		return nil
	}

	requests := []reconcile.Request{}
	for _, computeInstance := range computeInstances.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKey{
				Namespace: computeInstance.GetNamespace(),
				Name:      computeInstance.GetName(),
			},
		})
	}

	log.Info("mapped change notification",
		"kind", obj.GetObjectKind().GroupVersionKind().Kind,
		"namespace", obj.GetNamespace(),
		"name", obj.GetName(),
		"computeinstances", computeInstances.Items,
	)
	return requests
}

// handleProvisioning manages the provisioning job lifecycle for a ComputeInstance.
// Uses shared RunProvisioningLifecycle with statusFlush to prevent duplicate jobs
// from concurrent reconciliations.
func (r *ComputeInstanceReconciler) handleProvisioning(ctx context.Context, instance *v1alpha1.ComputeInstance) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// Check for ManagementStateManual annotation
	val, exists := instance.Annotations[osacComputeInstanceManagementStateAnnotation]
	if exists && val == ManagementStateManual {
		log.Info("skipping provisioning due to management-state annotation", "management-state", val)
		return ctrl.Result{}, nil
	}

	// If no provider configured, skip provisioning
	if r.ProvisioningProvider == nil {
		log.Info("no provisioning provider configured, skipping provisioning")
		return ctrl.Result{}, nil
	}

	return provisioning.RunProvisioningLifecycle(ctx, r.ProvisioningProvider, instance,
		r.provisionState(instance),
		r.MaxJobHistory, r.StatusPollInterval,
		&provisioning.PollCallbacks{
			OnFailed: func(message string) {
				// Only set Failed phase if no VM exists yet (first-time provisioning failure).
				// If the VM already exists (re-provisioning failure), the phase is driven by KubeVirt
				// PrintableStatus and the failed job is visible in status.provisioningJobs.
				if instance.Status.VirtualMachineReference == nil {
					setComputeInstancePhase(&instance.Status, v1alpha1.ComputeInstancePhaseFailed)
				}
				instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionProvisioned, metav1.ConditionFalse, message, v1alpha1.ReasonProvisioningFailed)
			},
		},
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(ctx, r.mgr.GetLocalManager().GetAPIReader(), client.ObjectKeyFromObject(instance), &v1alpha1.ComputeInstance{}, func(obj client.Object) []v1alpha1.JobStatus {
				return obj.(*v1alpha1.ComputeInstance).Status.ProvisioningJobs
			})
		},
		func() error {
			return r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(instance), instance.Status)
		},
	)
}

// handleDeprovisioning manages the deprovisioning job lifecycle for a ComputeInstance.
// Note: Finalizer management is handled by handleDelete(), not here.
func (r *ComputeInstanceReconciler) handleDeprovisioning(ctx context.Context, instance *v1alpha1.ComputeInstance) (ctrl.Result, error) {
	val, exists := instance.Annotations[osacComputeInstanceManagementStateAnnotation]
	if exists && val == ManagementStateManual {
		ctrllog.FromContext(ctx).Info("skipping deprovisioning due to management-state annotation", "management-state", val)
		return ctrl.Result{}, nil
	}

	result, done, err := provisioning.RunDeprovisioningLifecycle(ctx, r.ProvisioningProvider, instance,
		&instance.Status.ProvisioningJobs, r.MaxJobHistory, r.StatusPollInterval)
	if err != nil || !done {
		return result, err
	}
	return ctrl.Result{}, nil
}

// resolveSubnetTargetNamespace looks up the Subnet CR referenced by the primary subnet
// (networkAttachments[0].subnetRef) and returns the subnet target
// namespace (which equals the Subnet CR name).
// Returns empty string if no primary subnet is set.
// Returns error if Subnet CR lookup fails.
func (r *ComputeInstanceReconciler) resolveSubnetTargetNamespace(ctx context.Context, instance *v1alpha1.ComputeInstance) (string, error) {
	log := ctrllog.FromContext(ctx)

	primarySubnetRef := instance.Spec.PrimarySubnetRef()
	if primarySubnetRef == "" {
		// No subnet reference, no namespace to resolve
		return "", nil
	}

	// Look up Subnet CR in the same namespace as ComputeInstance
	subnet := &v1alpha1.Subnet{}
	subnetKey := types.NamespacedName{
		Name:      primarySubnetRef,
		Namespace: instance.Namespace,
	}

	err := r.Get(ctx, subnetKey, subnet)
	if err != nil {
		return "", fmt.Errorf("failed to get Subnet CR %s: %w", primarySubnetRef, err)
	}

	// Subnet namespace = Subnet CR name (established pattern from Phase 17)
	subnetTargetNamespace := subnet.Name

	log.Info("Resolved subnet target namespace from Subnet CR",
		"primarySubnetRef", primarySubnetRef,
		"subnetTargetNamespace", subnetTargetNamespace,
	)

	return subnetTargetNamespace, nil
}

// syncSubnetTargetNamespaceAnnotation ensures the subnet-target-namespace annotation is set
// when a primary subnet (networkAttachments[0].subnetRef) is configured.
// The primary subnet reference is immutable, so the annotation only needs to be resolved
// and written once; subsequent reconciles reuse the cached annotation value.
// Returns the resolved namespace, whether the annotation was written, and any error.
func (r *ComputeInstanceReconciler) syncSubnetTargetNamespaceAnnotation(ctx context.Context, instance *v1alpha1.ComputeInstance) (string, bool, error) {
	if instance.Spec.PrimarySubnetRef() == "" {
		return "", false, nil
	}

	// Primary subnet is immutable — if the annotation is already set, reuse it.
	if ns, ok := instance.Annotations[osacSubnetTargetNamespaceAnnotation]; ok {
		return ns, false, nil
	}

	subnetTargetNamespace, err := r.resolveSubnetTargetNamespace(ctx, instance)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", false, fmt.Errorf("%w: %w", errSubnetNotFound, err)
		}
		return "", false, err
	}
	if instance.Annotations == nil {
		instance.Annotations = make(map[string]string)
	}
	instance.Annotations[osacSubnetTargetNamespaceAnnotation] = subnetTargetNamespace
	return subnetTargetNamespace, true, nil
}

// syncMetadataPreflight ensures the finalizer is set and the subnet-target-namespace
// annotation is in sync with the current networkAttachments subnet.  It batches all metadata
// changes into a single r.Update() call to avoid multiple round-trips and the
// status-clobbering problem.  The resolved subnetTargetNamespace is returned so
// callers can reuse it without a second resolveSubnetNamespace call.
func (r *ComputeInstanceReconciler) syncMetadataPreflight(ctx context.Context, instance *v1alpha1.ComputeInstance) (string, error) {
	log := ctrllog.FromContext(ctx)

	metadataChanged := controllerutil.AddFinalizer(instance, osacComputeInstanceFinalizer)

	subnetTargetNamespace, changed, err := r.syncSubnetTargetNamespaceAnnotation(ctx, instance)
	if err != nil {
		log.Error(err, "Failed to resolve subnet target namespace")
		return "", err
	}
	if changed {
		metadataChanged = true
	}

	if metadataChanged {
		if err := r.Update(ctx, instance); err != nil {
			return "", err
		}
		// r.Update() returns the full server response via .Into(obj): the annotation
		// we just wrote, the latest ResourceVersion, and the current server-side status
		// are all present in instance after this call. No re-fetch is needed.
		// A cache-based r.Get() here would race against the async watch stream and
		// return a stale version that wipes the annotation from instance before it
		// reaches the AAP template payload in handleProvisioning.
	}

	return subnetTargetNamespace, nil
}

func (r *ComputeInstanceReconciler) handleUpdate(ctx context.Context, _ reconcile.Request, instance *v1alpha1.ComputeInstance) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	subnetTargetNamespace, err := r.syncMetadataPreflight(ctx, instance)
	if err != nil {
		if errors.Is(err, errSubnetNotFound) {
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}

	// Initialize status after the metadata update, because r.Update() overwrites
	// the in-memory status with the server response (status subresource is separate).
	r.initializeStatusConditions(instance)
	// Initialize phase to Starting for brand-new CIs (Phase is empty until first set).
	// Overridden by determinePhaseFromPrintableStatus() once a KubeVirt VM exists.
	if instance.Status.Phase == "" {
		setComputeInstancePhase(&instance.Status, v1alpha1.ComputeInstancePhaseStarting)
	}

	// Get the tenant (on local cluster)
	tenant, err := r.getTenant(ctx, instance)
	if err != nil {
		tenantName := instance.GetAnnotations()[osacTenantKey]
		log.Info("tenant does not exist or is being deleted, requeueing", "tenant", tenantName)
		return ctrl.Result{}, err
	}

	// If the tenant is not ready, requeue
	if tenant.Status.Phase != v1alpha1.TenantPhaseReady {
		msg := formatTenantNotReadyMessage(tenant)
		oldReason := conditionReason(instance, v1alpha1.ComputeInstanceConditionProvisioned)
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionProvisioned, metav1.ConditionFalse, msg, v1alpha1.ReasonTenantNotReady)
		if oldReason != v1alpha1.ReasonTenantNotReady {
			r.Recorder.Eventf(instance, nil, corev1.EventTypeNormal, eventReasonTenantNotReady, eventActionReconcile, "%s", msg)
		}
		log.Info("tenant is not ready, requeueing", "tenant", tenant.GetName())
		return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}

	targetClient, err := getTargetClient(ctx, r.mgr, r.targetCluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	targetNamespace := computeInstanceTargetNamespace(tenant, subnetTargetNamespace)

	kv, err := r.findKubeVirtVMs(ctx, targetClient, instance, targetNamespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Capture the persisted reason of the condition that records a storage
	// readiness gate before this reconcile overwrites it, so the TenantNotReady
	// event fires only when the gate is first hit.
	priorGateReason := conditionReason(instance, storageGateCondition(kv))
	if kv != nil {
		if err := r.handleKubeVirtVM(ctx, targetClient, instance, kv); err != nil {
			return ctrl.Result{}, err
		}
		setComputeInstancePhase(&instance.Status, determinePhaseFromPrintableStatus(ctx, kv, instance.Status.Phase))
	} else {
		// No KubeVirt VM exists yet: infrastructure is being provisioned.
		setComputeInstancePhase(&instance.Status, v1alpha1.ComputeInstancePhaseStarting)
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionProvisioned, metav1.ConditionFalse, "VirtualMachine not yet created, waiting for provisioning", v1alpha1.ReasonWaitingForVM)
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionReady, metav1.ConditionFalse, "", v1alpha1.ReasonAsExpected)
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionRestartRequired, metav1.ConditionFalse, "", v1alpha1.ReasonAsExpected)
	}

	if err := r.handleDesiredConfigVersion(ctx, instance); err != nil {
		return ctrl.Result{}, err
	}

	if provisioning.IsConfigApplied(&instance.Status.ProvisioningJobs, instance.Status.DesiredConfigVersion) {
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionConfigurationApplied, metav1.ConditionTrue, "", v1alpha1.ReasonAsExpected)

		// Update lastRestartedAt when a restart was requested and provisioning has reconciled it.
		if instance.Spec.RestartRequestedAt != nil {
			if instance.Status.LastRestartedAt == nil || instance.Spec.RestartRequestedAt.After(instance.Status.LastRestartedAt.Time) {
				log.Info("restart completed via provisioning", "restartRequestedAt", instance.Spec.RestartRequestedAt)
				instance.Status.LastRestartedAt = instance.Spec.RestartRequestedAt.DeepCopy()
				instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionRestartInProgress, metav1.ConditionFalse, "", v1alpha1.ReasonAsExpected)
			}
		}
	} else {
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionConfigurationApplied, metav1.ConditionFalse, "Applying configuration", v1alpha1.ReasonAsExpected)

		// Set RestartInProgress condition when a restart is pending provisioning.
		if instance.Spec.RestartRequestedAt != nil {
			if instance.Status.LastRestartedAt == nil || instance.Spec.RestartRequestedAt.After(instance.Status.LastRestartedAt.Time) {
				instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionRestartInProgress, metav1.ConditionTrue,
					fmt.Sprintf("Restart initiated at %s", instance.Spec.RestartRequestedAt.UTC().Format(time.RFC3339)),
					"RestartInProgress")
			}
		}
	}

	recordRunningConfig(instance)

	var gated bool
	ctx, gated, err = r.prepareStorageContext(ctx, targetClient, instance, tenant, kv, priorGateReason)
	if err != nil {
		return ctrl.Result{}, err
	}
	if gated {
		return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}

	// Always delegate to provisioning lifecycle — EvaluateAction decides
	// whether to skip, poll, or trigger. This avoids the A-B-A problem where
	// IsConfigApplied alone could match a stale historical job.
	return r.handleProvisioning(ctx, instance)
}

func formatTenantNotReadyMessage(tenant *v1alpha1.Tenant) string {
	msg := fmt.Sprintf("Tenant '%s' is not ready (phase: %s)", tenant.GetName(), tenant.Status.Phase)
	if srCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady); srCond != nil && srCond.Message != "" {
		msg = fmt.Sprintf("%s. %s: %s", msg, srCond.Type, srCond.Message)
	}
	if scCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady); scCond != nil && scCond.Message != "" {
		msg = fmt.Sprintf("%s. %s: %s", msg, scCond.Type, scCond.Message)
	}
	return msg
}

func (r *ComputeInstanceReconciler) prepareStorageContext(
	ctx context.Context,
	targetClient client.Client,
	instance *v1alpha1.ComputeInstance,
	tenant *v1alpha1.Tenant,
	kv *kubevirtv1.VirtualMachine,
	priorGateReason string,
) (context.Context, bool, error) {
	if !r.willTriggerProvisioning(instance) {
		return ctx, false, nil
	}
	log := ctrllog.FromContext(ctx)

	// Resolve and partition storage tier definitions and backend connections.
	// OSAC-managed tiers and connections are injected into ctx for JIT storage
	// provisioning (OSAC-1992), while CSP-managed tiers and skipped tiers are
	// inspected to gate VM provisioning on StorageReady and Tenant.status.storageClasses
	// (OSAC-6596).
	ctx, osacTiers, cspTiers, skippedTiers, tierErr := resolveAndPartitionTierContext(ctx, r.TiersClient, r.BackendsClient, r.SecretsClient, "computeInstance", instance.GetName())
	if tierErr != nil {
		log.Error(tierErr, "tier resolution failed", "computeInstance", instance.GetName())
	}

	reqTiers := requestedStorageTiers(instance)
	reqCSPTiers := requestedCSPTiers(tenant, reqTiers, osacTiers, cspTiers)
	unresolvedTiers := unresolvedRequestedTiers(reqTiers, osacTiers, cspTiers, skippedTiers)
	cspRequested := len(reqCSPTiers) > 0 || len(unresolvedTiers) > 0
	cspStoragePresent := cspRequested || len(cspTiers) > 0 || tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady) != nil

	// Every job this pass would trigger runs the AAP tier validation, so gate
	// day-2 jobs on an existing VM as well as initial provisioning.
	if ready, msg := checkStorageReadiness(tenant, reqTiers, reqCSPTiers, unresolvedTiers, tierErr); !ready {
		instance.SetStatusCondition(storageGateCondition(kv), metav1.ConditionFalse, msg, v1alpha1.ReasonTenantNotReady)
		if priorGateReason != v1alpha1.ReasonTenantNotReady {
			r.Recorder.Eventf(instance, nil, corev1.EventTypeNormal, eventReasonTenantNotReady, eventActionReconcile, "%s", msg)
		}
		log.Info("tenant storage is not ready for requested tiers, deferring provisioning", "tenant", tenant.GetName(), "reason", msg)
		return ctx, true, nil
	}

	// Inject tenant storage classes into context for AAP extra_vars.
	// Prefer Tenant.status.storageClasses (populated by the storage controller);
	// fall back to resolving labeled StorageClasses on the target cluster only
	// when the ClusterStorageReady and StorageReady conditions are absent and no
	// CSP tiers are present (storage controller disabled or hasn't reconciled
	// this tenant yet).
	if len(tenant.Status.StorageClasses) > 0 {
		scs := tenant.Status.StorageClasses
		if cspStoragePresent {
			scs = filterStorageClassesToRequestedTiers(scs, reqTiers)
		}
		if len(scs) > 0 {
			ctx = provisioning.WithTenantStorageClasses(ctx, scs)
		}
	} else if tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady) == nil &&
		!cspStoragePresent {
		tenantName := tenant.GetName()
		resolution, err := getTenantStorageClasses(ctx, targetClient, tenantName)
		if err != nil {
			return ctx, false, fmt.Errorf("failed to resolve tenant storage classes from target cluster for tenant %s: %w", tenantName, err)
		}

		for _, msg := range resolution.duplicateMessages {
			r.Recorder.Eventf(instance, nil, corev1.EventTypeWarning, eventReasonDuplicateStorageClass, eventActionDetectDuplicate, "%s", msg)
		}
		if len(resolution.resolved) > 0 {
			log.Info("resolved tenant storage classes from target cluster (status was empty)", "tenant", tenantName, "storageClasses", resolution.resolved)
			ctx = provisioning.WithTenantStorageClasses(ctx, resolution.resolved)
		} else if len(resolution.ambiguousTiers) > 0 {
			log.Info("no tenant storage classes resolved; all matched tiers are ambiguous (multiple StorageClasses per tier)", "tenant", tenantName, "ambiguousTiers", resolution.ambiguousTiers)
			r.Recorder.Eventf(instance, nil, corev1.EventTypeWarning, eventReasonDuplicateStorageClass, eventActionDetectDuplicate,
				"no tenant storage classes resolved for tenant %s; all matched tiers are ambiguous — check StorageClass labels", tenantName)
		} else {
			log.Info("no tenant storage classes resolved from target cluster", "tenant", tenantName)
		}
	}

	return ctx, false, nil
}

func (r *ComputeInstanceReconciler) handleDelete(ctx context.Context, _ reconcile.Request, instance *v1alpha1.ComputeInstance) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("deleting compute instance")

	// The outer Reconcile persists status after handleDelete returns. Keep the
	// base finalizer until a later reconcile observes that status in the API.
	oldStatus := instance.Status.DeepCopy()
	setComputeInstancePhase(&instance.Status, v1alpha1.ComputeInstancePhaseDeleting)
	instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionReady, metav1.ConditionFalse,
		"ComputeInstance is being deleted", v1alpha1.ReasonDeleting)

	// Base finalizer has already been removed, cleanup complete
	if !controllerutil.ContainsFinalizer(instance, osacComputeInstanceFinalizer) {
		return ctrl.Result{}, nil
	}

	done, result, err := reconcileAutoExternalIPCleanup(ctx, r.Client, r.NetworkingNamespace,
		autoExternalIPOwner{kind: computeInstanceOwner, id: instance.Labels[osacComputeInstanceIDLabel]}, r.StatusPollInterval)
	if err != nil || !done {
		return result, err
	}

	// Handle deprovisioning - provider decides internally if needed
	log.Info("handling deletion")
	result, err = r.handleDeprovisioning(ctx, instance)
	if err != nil {
		return result, err
	}

	// If we need to requeue (jobs still running or provider needs time), do so
	if result.RequeueAfter > 0 {
		return result, nil
	}

	if !equality.Semantic.DeepEqual(instance.Status, *oldStatus) {
		return ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
	}

	// Deprovisioning complete or skipped, remove base finalizer
	if controllerutil.RemoveFinalizer(instance, osacComputeInstanceFinalizer) {
		if err := r.mgr.GetLocalManager().GetClient().Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// initializeStatusConditions initializes the conditions that haven't already been initialized.
func (r *ComputeInstanceReconciler) initializeStatusConditions(instance *v1alpha1.ComputeInstance) {
	r.initializeStatusCondition(
		instance,
		v1alpha1.ComputeInstanceConditionConfigurationApplied,
		metav1.ConditionFalse,
		v1alpha1.ReasonInitialized,
	)
	r.initializeStatusCondition(
		instance,
		v1alpha1.ComputeInstanceConditionReady,
		metav1.ConditionFalse,
		v1alpha1.ReasonInitialized,
	)
	r.initializeStatusCondition(
		instance,
		v1alpha1.ComputeInstanceConditionProvisioned,
		metav1.ConditionFalse,
		v1alpha1.ReasonInitialized,
	)
	r.initializeStatusCondition(
		instance,
		v1alpha1.ComputeInstanceConditionRestartRequired,
		metav1.ConditionFalse,
		v1alpha1.ReasonInitialized,
	)
}

// initializeStatusCondition initializes a condition, but only if it is not already initialized.
func (r *ComputeInstanceReconciler) initializeStatusCondition(instance *v1alpha1.ComputeInstance,
	conditionType v1alpha1.ComputeInstanceConditionType, status metav1.ConditionStatus, reason string) {
	if instance.Status.Conditions == nil {
		instance.Status.Conditions = []metav1.Condition{}
	}
	condition := instance.GetStatusCondition(conditionType)
	if condition != nil {
		return
	}
	instance.SetStatusCondition(conditionType, status, "", reason)
}

func computeInstanceTargetNamespace(tenant *v1alpha1.Tenant, subnetTargetNamespace string) string {
	if subnetTargetNamespace != "" {
		return subnetTargetNamespace
	}
	if tenant.Status.Namespace != "" {
		return tenant.Status.Namespace
	}
	return tenant.GetName()
}

func (r *ComputeInstanceReconciler) findKubeVirtVMs(ctx context.Context, targetClient client.Client, instance *v1alpha1.ComputeInstance, nsName string) (*kubevirtv1.VirtualMachine, error) {
	log := ctrllog.FromContext(ctx)

	var kubeVirtVMList kubevirtv1.VirtualMachineList
	if err := targetClient.List(ctx, &kubeVirtVMList, client.InNamespace(nsName), labelSelectorFromComputeInstanceInstance(instance)); err != nil {
		log.Error(err, "failed to list KubeVirt VMs")
		return nil, err
	}

	if len(kubeVirtVMList.Items) > 1 {
		return nil, fmt.Errorf("found too many (%d) matching KubeVirt VMs for %s", len(kubeVirtVMList.Items), instance.GetName())
	}

	if len(kubeVirtVMList.Items) == 0 {
		return nil, nil
	}

	return &kubeVirtVMList.Items[0], nil
}

func (r *ComputeInstanceReconciler) handleKubeVirtVM(ctx context.Context, targetClient client.Client, instance *v1alpha1.ComputeInstance,
	kv *kubevirtv1.VirtualMachine) error {
	log := ctrllog.FromContext(ctx)

	name := kv.GetName()
	instance.SetVirtualMachineReferenceKubeVirtVirtualMachineName(name)
	instance.SetVirtualMachineReferenceNamespace(kv.GetNamespace())

	// Provisioned reflects whether compute AND storage resources are allocated.
	// While PrintableStatus="Provisioning", KubeVirt is still creating DataVolumes
	// (storage not yet ready). Known error states (DataVolumeError, ErrorPvcNotFound,
	// etc.) indicate provisioning failure. Known operational states (Running, Stopped,
	// etc.) indicate success. Unknown/new statuses default to error to avoid falsely
	// claiming success.
	//
	// Empty PrintableStatus and Unschedulable are transient during VM creation and
	// must NOT set ProvisioningFailed — see determinePhaseFromPrintableStatus for
	// the symmetric phase handling.
	oldProvisionedReason := conditionReason(instance, v1alpha1.ComputeInstanceConditionProvisioned)
	if kv.Status.PrintableStatus == "" {
		// PrintableStatus not yet set by KubeVirt — our watch fires before
		// KubeVirt's controller processes the new VM CR. Preserve the current
		// Provisioned condition to avoid a transient ProvisioningFailed.
		log.Info("KubeVirt PrintableStatus not yet set, skipping provisioned condition update")
	} else if kv.Status.PrintableStatus == kubevirtv1.VirtualMachineStatusProvisioning {
		msg := fmt.Sprintf("Creating DataVolumes for boot disk (%dGiB)", instance.Spec.BootDisk.SizeGiB)
		if len(instance.Spec.AdditionalDisks) > 0 {
			msg = fmt.Sprintf("%s and %d additional disk(s)", msg, len(instance.Spec.AdditionalDisks))
		}
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionProvisioned, metav1.ConditionFalse, msg, v1alpha1.ReasonProvisioningStorage)
		if oldProvisionedReason != v1alpha1.ReasonProvisioningStorage {
			r.Recorder.Eventf(instance, nil, corev1.EventTypeNormal, eventReasonProvisioningStorage, eventActionReconcile, "%s", msg)
		}
	} else if kv.Status.PrintableStatus == kubevirtv1.VirtualMachineStatusUnschedulable {
		// Unschedulable is transient during initial VM boot — the scheduler
		// may need time to find a suitable node. Treat as a non-fatal
		// scheduling wait rather than a provisioning failure.
		msg := schedulingWaitMessage(kv)
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionProvisioned, metav1.ConditionFalse, msg, v1alpha1.ReasonScheduling)
	} else if msg := provisioningErrorMessage(kv); msg != "" {
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionProvisioned, metav1.ConditionFalse, msg, v1alpha1.ReasonProvisioningFailed)
		if oldProvisionedReason != v1alpha1.ReasonProvisioningFailed {
			r.Recorder.Eventf(instance, nil, corev1.EventTypeWarning, eventReasonProvisioningFailed, eventActionReconcile, "%s", msg)
		}
	} else if isOperationalStatus(kv.Status.PrintableStatus) {
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionProvisioned, metav1.ConditionTrue, "All infrastructure resources provisioned successfully", v1alpha1.ReasonInfrastructureReady)
		if oldProvisionedReason != v1alpha1.ReasonInfrastructureReady {
			r.Recorder.Eventf(instance, nil, corev1.EventTypeNormal, eventReasonInfrastructureReady, eventActionReconcile, "All infrastructure resources provisioned successfully")
		}
	} else {
		msg := fmt.Sprintf("VM entered unexpected status: %s", kv.Status.PrintableStatus)
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionProvisioned, metav1.ConditionFalse, msg, v1alpha1.ReasonProvisioningFailed)
		if oldProvisionedReason != v1alpha1.ReasonProvisioningFailed {
			r.Recorder.Eventf(instance, nil, corev1.EventTypeWarning, eventReasonProvisioningFailed, eventActionReconcile, "%s", msg)
		}
	}

	// Ready mirrors VirtualMachine.Status.Ready, synced from the VirtualMachineInstance
	// Ready condition (set by the virt-launcher pod's readiness probe).
	oldReadyStatus := conditionStatus(instance, v1alpha1.ComputeInstanceConditionReady)
	instance.Status.ComputeNetworkAttachmentStatuses = nil
	if kvVMHasConditionWithStatus(kv, kubevirtv1.VirtualMachineReady, corev1.ConditionTrue) {
		ipAddress := r.getDefaultVMIIPAddress(ctx, targetClient, kv.GetNamespace(), name)

		if ipAddress != "" && len(instance.Spec.NetworkAttachments) == 1 {
			subnetRef := instance.Spec.NetworkAttachments[0].SubnetRef
			subnet := &v1alpha1.Subnet{}
			if err := r.Get(ctx, types.NamespacedName{Name: subnetRef, Namespace: instance.Namespace}, subnet); err != nil {
				return fmt.Errorf("failed to get Subnet CR %s for ComputeInstance network status: %w", subnetRef, err)
			}
			subnetID := subnet.Labels[osacSubnetIDLabel]
			if subnetID == "" {
				return fmt.Errorf("subnet CR %s is missing the %s label", subnetRef, osacSubnetIDLabel)
			}
			if !isCanonicalIPv4AddressInSubnet(ipAddress, subnet.Spec.IPv4CIDR) {
				log.Info("ignoring default VMI interface IP outside the attached Subnet IPv4 CIDR",
					"computeinstance", instance.GetName(), "ipAddress", ipAddress,
					"subnet", subnetRef, "subnetIPv4CIDR", subnet.Spec.IPv4CIDR)
				ipAddress = ""
			} else {
				instance.Status.ComputeNetworkAttachmentStatuses = []v1alpha1.ComputeNetworkAttachmentStatus{{
					SubnetRef: subnetID,
					IPAddress: ipAddress,
				}}
			}
		}

		log.Info("KubeVirt virtual machine (kubevirt resource) is ready", "computeinstance", instance.GetName(), "ipAddress", ipAddress)
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionReady, metav1.ConditionTrue, "", v1alpha1.ReasonAsExpected)
		instance.SetIPAddress(ipAddress)
		if oldReadyStatus != metav1.ConditionTrue {
			r.Recorder.Eventf(instance, nil, corev1.EventTypeNormal, eventReasonReady, eventActionReconcile, "VirtualMachine is ready, IP: %s", ipAddress)
		}
	} else {
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionReady, metav1.ConditionFalse, "", v1alpha1.ReasonAsExpected)
	}

	// RestartRequired mirrors KubeVirt's RestartRequired condition, which is set when
	// CPU/memory/device changes have been applied to the VM spec but require a reboot.
	if kvVMHasConditionWithStatus(kv, kubevirtv1.VirtualMachineRestartRequired, corev1.ConditionTrue) {
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionRestartRequired, metav1.ConditionTrue, "", v1alpha1.ReasonAsExpected)
	} else {
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionRestartRequired, metav1.ConditionFalse, "", v1alpha1.ReasonAsExpected)
	}

	return nil
}

// isCanonicalIPv4AddressInSubnet accepts only a canonical IPv4 address contained
// by the attached Subnet CIDR. VMI status is guest-reported, so it must not be
// published as a tenant attachment address or used as an ExternalIP DNAT target
// unless it belongs to that Subnet.
func isCanonicalIPv4AddressInSubnet(ipAddress, subnetCIDR string) bool {
	address, err := netip.ParseAddr(ipAddress)
	if err != nil || !address.Is4() || address.String() != ipAddress {
		return false
	}

	prefix, err := netip.ParsePrefix(subnetCIDR)
	if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() {
		return false
	}

	return prefix.Contains(address)
}

// getDefaultVMIIPAddress fetches the VirtualMachineInstance and returns the IP for its default network interface.
func (r *ComputeInstanceReconciler) getDefaultVMIIPAddress(ctx context.Context, targetClient client.Client, namespace, name string) string {
	log := ctrllog.FromContext(ctx)

	vmi := &kubevirtv1.VirtualMachineInstance{}
	if err := targetClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, vmi); err != nil {
		log.Error(err, "failed to get VirtualMachineInstance", "namespace", namespace, "name", name)
		return ""
	}
	for _, iface := range vmi.Status.Interfaces {
		if iface.Name == "default" {
			return iface.IP
		}
	}

	log.Info("no IP address found for default VirtualMachineInstance interface", "namespace", namespace, "name", name)
	return ""
}

func kvVMGetCondition(vm *kubevirtv1.VirtualMachine, cond kubevirtv1.VirtualMachineConditionType) *kubevirtv1.VirtualMachineCondition {
	if vm == nil {
		return nil
	}
	for _, c := range vm.Status.Conditions {
		if c.Type == cond {
			return &c
		}
	}
	return nil
}

func kvVMHasConditionWithStatus(vm *kubevirtv1.VirtualMachine, cond kubevirtv1.VirtualMachineConditionType, status corev1.ConditionStatus) bool {
	c := kvVMGetCondition(vm, cond)
	return c != nil && c.Status == status
}

// provisioningErrorMessage returns a non-empty error message when the VM's
// PrintableStatus indicates a provisioning failure (DataVolumeError,
// ErrorPvcNotFound, ErrorUnschedulable, etc.). It extracts detail from the VM's
// conditions when available. Returns "" for non-error states.
func provisioningErrorMessage(kv *kubevirtv1.VirtualMachine) string {
	var prefix string
	switch kv.Status.PrintableStatus {
	case kubevirtv1.VirtualMachineStatusDataVolumeError:
		prefix = "Boot disk provisioning failed"
	case kubevirtv1.VirtualMachineStatusPvcNotFound:
		prefix = "Boot disk provisioning failed: PVC not found"
	case kubevirtv1.VirtualMachineStatusCrashLoopBackOff:
		prefix = "VM is in CrashLoopBackOff"
	case kubevirtv1.VirtualMachineStatusErrImagePull,
		kubevirtv1.VirtualMachineStatusImagePullBackOff:
		prefix = "VM image pull failed"
	case kubevirtv1.VirtualMachineStatusTerminating:
		prefix = "VM is terminating unexpectedly"
	default:
		return ""
	}

	// Collect messages from False conditions for additional detail.
	var detail string
	for _, c := range kv.Status.Conditions {
		if c.Status == corev1.ConditionFalse && c.Message != "" {
			detail = c.Message
			break
		}
	}
	if detail != "" {
		return fmt.Sprintf("%s: %s", prefix, detail)
	}
	return prefix
}

// schedulingWaitMessage builds a human-readable message for the Unschedulable
// PrintableStatus, extracting detail from the VM's conditions when available.
func schedulingWaitMessage(kv *kubevirtv1.VirtualMachine) string {
	msg := "VM is waiting for a schedulable node"
	for _, c := range kv.Status.Conditions {
		if c.Status == corev1.ConditionFalse && c.Message != "" {
			return fmt.Sprintf("%s: %s", msg, c.Message)
		}
	}
	return msg
}

// isOperationalStatus returns true when the PrintableStatus represents a known
// operational (non-error) state where compute and storage resources are allocated.
// Unknown or new statuses return false so they don't silently claim success —
// mirroring the defensive posture of determinePhaseFromPrintableStatus.
func isOperationalStatus(status kubevirtv1.VirtualMachinePrintableStatus) bool {
	switch status {
	case kubevirtv1.VirtualMachineStatusRunning,
		kubevirtv1.VirtualMachineStatusStopped,
		kubevirtv1.VirtualMachineStatusPaused,
		kubevirtv1.VirtualMachineStatusStarting,
		kubevirtv1.VirtualMachineStatusWaitingForVolumeBinding,
		kubevirtv1.VirtualMachineStatusMigrating,
		kubevirtv1.VirtualMachineStatusWaitingForReceiver,
		kubevirtv1.VirtualMachineStatusStopping,
		kubevirtv1.VirtualMachineStatusUnknown:
		return true
	default:
		return false
	}
}

// determinePhaseFromPrintableStatus maps a KubeVirt VirtualMachine's PrintableStatus
// to a ComputeInstancePhaseType.
//
// Transient startup states (Provisioning, WaitingForVolumeBinding, Unschedulable) map to
// Starting because they are normal steps in the VM creation sequence, not error conditions.
//
// Paused is checked via both PrintableStatus (KubeVirt v1.6.0+) and the VirtualMachinePaused
// condition (older versions where PrintableStatus stayed "Running" when paused).
//
// Migrating and WaitingForReceiver map to Running because the source VM remains accessible
// throughout live migration. OSAC does not trigger live migration but it can be infra-initiated.
//
// Unknown preserves currentPhase: the hypervisor host is temporarily unreachable and the VM
// may still be healthy. The phase clears automatically when the host recovers.
//
// An empty PrintableStatus ("") occurs when our watch fires before KubeVirt's controller
// has processed the new VM CR. Like Unknown, it preserves the current phase to avoid a
// transient Failed.
//
// All remaining values (Terminating, CrashLoopBackOff, ErrorUnschedulable, ErrImagePull,
// ImagePullBackOff, ErrorPvcNotFound, DataVolumeError) map to Failed.
func determinePhaseFromPrintableStatus(ctx context.Context, kv *kubevirtv1.VirtualMachine, currentPhase v1alpha1.ComputeInstancePhaseType) v1alpha1.ComputeInstancePhaseType {
	log := ctrllog.FromContext(ctx)
	log.V(1).Info("mapping KubeVirt PrintableStatus to ComputeInstance phase",
		"printableStatus", kv.Status.PrintableStatus,
		"currentPhase", currentPhase)

	switch kv.Status.PrintableStatus {
	case kubevirtv1.VirtualMachineStatusProvisioning,
		kubevirtv1.VirtualMachineStatusWaitingForVolumeBinding,
		kubevirtv1.VirtualMachineStatusStarting,
		kubevirtv1.VirtualMachineStatusUnschedulable:
		return v1alpha1.ComputeInstancePhaseStarting
	case kubevirtv1.VirtualMachineStatusPaused:
		return v1alpha1.ComputeInstancePhasePaused
	case kubevirtv1.VirtualMachineStatusRunning:
		// Defensive fallback for older KubeVirt versions where PrintableStatus stayed
		// "Running" when the VM was paused.
		if kvVMHasConditionWithStatus(kv, kubevirtv1.VirtualMachinePaused, corev1.ConditionTrue) {
			return v1alpha1.ComputeInstancePhasePaused
		}
		return v1alpha1.ComputeInstancePhaseRunning
	case kubevirtv1.VirtualMachineStatusMigrating,
		kubevirtv1.VirtualMachineStatusWaitingForReceiver:
		return v1alpha1.ComputeInstancePhaseRunning
	case kubevirtv1.VirtualMachineStatusStopping:
		return v1alpha1.ComputeInstancePhaseStopping
	case kubevirtv1.VirtualMachineStatusStopped:
		return v1alpha1.ComputeInstancePhaseStopped
	case kubevirtv1.VirtualMachineStatusUnknown:
		// Host is temporarily unreachable. Preserve the last known phase rather than
		// asserting Failed. Clears automatically when the host recovers.
		log.Info("KubeVirt PrintableStatus is Unknown, preserving current phase",
			"currentPhase", currentPhase)
		return currentPhase
	case "":
		// PrintableStatus not yet set by KubeVirt — race condition at VM creation.
		// Our watch fires before KubeVirt's controller processes the new VM CR.
		// Preserve the current phase (always Starting at this point) to avoid a
		// transient Failed.
		log.Info("KubeVirt PrintableStatus not yet set, preserving current phase",
			"currentPhase", currentPhase)
		return currentPhase
	default:
		// Covers: Terminating, CrashLoopBackOff, ErrImagePull,
		// ImagePullBackOff, ErrorPvcNotFound, DataVolumeError.
		// If a new KubeVirt PrintableStatus is introduced and falls here, update this switch.
		log.Info("unhandled KubeVirt PrintableStatus, defaulting to Failed",
			"printableStatus", kv.Status.PrintableStatus)
		return v1alpha1.ComputeInstancePhaseFailed
	}
}

func (r *ComputeInstanceReconciler) provisionState(instance *v1alpha1.ComputeInstance) *provisioning.State {
	return &provisioning.State{
		Jobs:                 &instance.Status.ProvisioningJobs,
		DesiredConfigVersion: instance.Status.DesiredConfigVersion,
	}
}

// handleDesiredConfigVersion sets status.desiredConfigVersion to the hash of spec.
func (r *ComputeInstanceReconciler) handleDesiredConfigVersion(ctx context.Context, instance *v1alpha1.ComputeInstance) error {
	version, err := provisioning.ComputeDesiredConfigVersion(instance.Spec)
	if err != nil {
		return err
	}
	instance.Status.DesiredConfigVersion = version
	return nil
}

// recordRunningConfig records the desired config version that is actually running, so the
// running instance type can be billed from when it started running rather than from when
// the change was reported downstream. The stamp follows configRunningSince: the first
// record dates from the phase change, a change of the recorded version or type name from
// the observation. The version is recorded once per configuration: while it is unchanged
// the recorded time stays put. The instance type name is part of the recorded identity:
// two types with identical resources hash to the same config version, and a resize
// between them must still move the stamp.
//
// The record is the CR-side half of crConfirmsRunningType: recording establishes the name and
// time that the predicate later vouches with. A pending restart invalidates any earlier
// observation, so it is cleared; KubeVirt reports RestartRequired asynchronously, so an
// absent or unknown condition vouches for nothing yet, and the record waits for it to become
// explicitly false before it can confirm anything.
func recordRunningConfig(instance *v1alpha1.ComputeInstance) {
	restart, present := restartRequired(instance)
	switch {
	case present && restart == metav1.ConditionTrue:
		clearRecordedRunningConfig(instance)
		return
	case !present || restart != metav1.ConditionFalse:
		return
	}
	label := instance.Labels[osacComputeInstanceTypeNameLabel]
	if label == "" {
		return
	}
	// Steady state: the CR already confirms the labeled type running with the current
	// desired config, so the recorded observation stays put.
	if crConfirmsRunningType(instance, label) &&
		instance.Status.RunningConfigVersion == instance.Status.DesiredConfigVersion {
		return
	}
	if instance.Status.Phase != v1alpha1.ComputeInstancePhaseRunning {
		return
	}
	if !instance.IsStatusConditionTrue(v1alpha1.ComputeInstanceConditionConfigurationApplied) {
		return
	}
	// The stamp depends on what is being recorded: the first record of the
	// resource dates from the phase change, a change of the recorded version or
	// type name from this observation. See configRunningSince.
	since := configRunningSince(instance)
	instance.Status.RunningConfigVersion = instance.Status.DesiredConfigVersion
	instance.Status.RunningInstanceTypeName = label
	instance.Status.RunningConfigSince = &since
}

// configRunningSince returns when the recorded config started running.
//
// The first record of a resource, when no version was recorded before, stamps the
// RUNNING phase transition: the type is known from the spec at admission and is
// what the VM booted with, so the record must date from the moment the instance
// started running rather than from when the provisioning job completed and the
// ConfigurationApplied condition turned true, up to tens of seconds later. A VM
// stopped in that window would otherwise keep the gap permanently unbilled under
// the instance_type dimension. A missing phase time falls back to the observation
// time.
//
// A change of the recorded version or type name stamps the observation time. The
// condition time cannot be reused: a name-only resize between types with
// identical resources leaves the condition untouched, so its transition time is
// arbitrarily stale and would backdate the new type, moving the usage between the
// two pricing dimensions. The observation time cannot backdate and lags the
// change by at most one reconcile, a bound the condition-time rule also had since
// the condition was itself set at observation.
func configRunningSince(instance *v1alpha1.ComputeInstance) metav1.Time {
	if instance.Status.RunningConfigVersion == "" {
		if !instance.Status.PhaseTransitionTime.IsZero() {
			return *instance.Status.PhaseTransitionTime
		}
		return metav1.Now()
	}
	return metav1.Now()
}

// crConfirmsRunningType reports whether the CR confirms that the requested instance type is
// the one actually running. All three clauses must hold: the instance-type label exists and
// names the requested type, so the reconciler has applied it; the recorded running-config
// observation names the same type, so recordRunningConfig observed it running; and the
// RestartRequired condition exists and is explicitly false, so no restart is pending that the
// observation would predate. A label that names another type describes a resize the
// reconciler has not applied, a recorded name that lags the label describes the previous
// type, and a missing or unknown restart condition vouches for nothing.
func crConfirmsRunningType(instance *v1alpha1.ComputeInstance, requested string) bool {
	if requested == "" {
		return false
	}
	if instance.Labels[osacComputeInstanceTypeNameLabel] != requested {
		return false
	}
	if instance.Status.RunningInstanceTypeName != requested {
		return false
	}
	restart, present := restartRequired(instance)
	return present && restart == metav1.ConditionFalse
}

// restartRequired reads the RestartRequired condition. The second result is false when the
// condition is absent.
func restartRequired(instance *v1alpha1.ComputeInstance) (metav1.ConditionStatus, bool) {
	condition := meta.FindStatusCondition(instance.Status.Conditions,
		string(v1alpha1.ComputeInstanceConditionRestartRequired))
	if condition == nil {
		return "", false
	}
	return condition.Status, true
}

// clearRecordedRunningConfig drops the recorded running-config observation. Used when a
// pending restart invalidates it: the observation predates the restart and cannot vouch for
// the configuration that runs after it.
func clearRecordedRunningConfig(instance *v1alpha1.ComputeInstance) {
	instance.Status.RunningConfigVersion = ""
	instance.Status.RunningInstanceTypeName = ""
	instance.Status.RunningConfigSince = nil
}

// setComputeInstancePhase records the phase and, when it changes, the time of the change, so
// metering can bill the state transition from the moment it happened rather than from when
// feedback noticed it. The time is stamped once per phase change and kept while the phase is
// unchanged, the same recordRunningConfig pattern as RunningConfigSince.
func setComputeInstancePhase(status *v1alpha1.ComputeInstanceStatus, phase v1alpha1.ComputeInstancePhaseType) {
	if status.Phase == phase {
		return
	}
	status.Phase = phase
	now := metav1.Now()
	status.PhaseTransitionTime = &now
}

func conditionReason(instance *v1alpha1.ComputeInstance, condType v1alpha1.ComputeInstanceConditionType) string {
	cond := instance.GetStatusCondition(condType)
	if cond == nil {
		return ""
	}
	return cond.Reason
}

func conditionStatus(instance *v1alpha1.ComputeInstance, condType v1alpha1.ComputeInstanceConditionType) metav1.ConditionStatus {
	cond := instance.GetStatusCondition(condType)
	if cond == nil {
		return metav1.ConditionUnknown
	}
	return cond.Status
}

// requestedStorageTiers returns the deduplicated, lowercased storage tiers
// requested by a ComputeInstance across its bootDisk and additionalDisks,
// preserving first-seen order.
func requestedStorageTiers(instance *v1alpha1.ComputeInstance) []string {
	if instance == nil {
		return nil
	}
	rawTiers := make([]string, 0, 1+len(instance.Spec.AdditionalDisks))
	rawTiers = append(rawTiers, instance.Spec.BootDisk.StorageTier)
	for _, disk := range instance.Spec.AdditionalDisks {
		rawTiers = append(rawTiers, disk.StorageTier)
	}

	seen := make(map[string]bool, len(rawTiers))
	var out []string
	for _, raw := range rawTiers {
		tier := strings.ToLower(strings.TrimSpace(raw))
		if tier == "" || seen[tier] {
			continue
		}
		seen[tier] = true
		out = append(out, tier)
	}
	return out
}

// requestedCSPTiers returns the subset of requestedTiers that are CSP-managed:
// either explicitly present in cspTiers, or (when CSP storage is active in the
// catalog or on the Tenant via StorageReady) not present in osacTiers.
func requestedCSPTiers(
	tenant *v1alpha1.Tenant,
	requestedTiers []string,
	osacTiers, cspTiers []provisioning.TierDefinition,
) []string {
	if len(requestedTiers) == 0 {
		return nil
	}
	cspSet := tierNameSet(cspTiers)
	osacSet := tierNameSet(osacTiers)
	hasCSPContext := len(cspTiers) > 0 || (tenant != nil && tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady) != nil)

	var out []string
	for _, tier := range requestedTiers {
		_, inCSP := cspSet[tier]
		_, inOSAC := osacSet[tier]
		if inCSP || (hasCSPContext && !inOSAC) {
			out = append(out, tier)
		}
	}
	return out
}

// unresolvedRequestedTiers returns the subset of requestedTiers that are absent
// from both osacTiers and cspTiers when skippedTiers is true (indicating at
// least one catalog tier was skipped due to a missing backend or unready secret).
func unresolvedRequestedTiers(
	requestedTiers []string,
	osacTiers, cspTiers []provisioning.TierDefinition,
	skippedTiers bool,
) []string {
	if !skippedTiers || len(requestedTiers) == 0 {
		return nil
	}
	osacSet := tierNameSet(osacTiers)
	cspSet := tierNameSet(cspTiers)
	var out []string
	for _, tier := range requestedTiers {
		if _, inOSAC := osacSet[tier]; inOSAC {
			continue
		}
		if _, inCSP := cspSet[tier]; inCSP {
			continue
		}
		out = append(out, tier)
	}
	return out
}

// willTriggerProvisioning reports whether handleProvisioning will trigger a new
// provisioning job on this reconcile pass. When false (manual management state,
// nil provider, or EvaluateAction returning Skip, Poll, or Backoff), storage
// context resolution and readiness gating are skipped so steady-state VM/VMI
// updates and job polls make zero gRPC or StorageClass API calls.
func (r *ComputeInstanceReconciler) willTriggerProvisioning(
	instance *v1alpha1.ComputeInstance,
) bool {
	if r.ProvisioningProvider == nil {
		return false
	}
	if val, exists := instance.Annotations[osacComputeInstanceManagementStateAnnotation]; exists && val == ManagementStateManual {
		return false
	}
	action, _ := provisioning.EvaluateAction(r.provisionState(instance), func() bool { return false })
	return action == provisioning.Trigger
}

// storageGateCondition returns the ComputeInstance condition that records a
// storage readiness gate: Provisioned before the KubeVirt VM exists, and
// ConfigurationApplied for a day-2 job on an existing VM, so the Provisioned
// and Ready conditions keep reporting the running VM while the job waits.
func storageGateCondition(kv *kubevirtv1.VirtualMachine) v1alpha1.ComputeInstanceConditionType {
	if kv == nil {
		return v1alpha1.ComputeInstanceConditionProvisioned
	}
	return v1alpha1.ComputeInstanceConditionConfigurationApplied
}

// checkStorageReadiness reports whether a provisioning job can be triggered
// for the requested storage tiers. A requested tier that already maps to a
// single StorageClass in Tenant.status.storageClasses is usable as-is, so a
// storage tier catalog or backend credential lookup failure blocks only the
// requested tiers that still need catalog data: unmapped OSAC-managed tiers
// need JIT provisioning, and unclassified tiers may be CSP-managed. Requested
// CSP-managed tiers also require StorageReady=True and a single StorageClass
// per tier.
func checkStorageReadiness(
	tenant *v1alpha1.Tenant,
	reqTiers, reqCSPTiers, unresolvedTiers []string,
	tierErr error,
) (bool, string) {
	classCounts := countStorageClassNamesByTier(tenant)

	if tierErr != nil && tenantStorageClassesAuthoritative(tenant) {
		if unmapped := tiersWithoutStorageClass(reqTiers, classCounts); len(unmapped) > 0 {
			return false, fmt.Sprintf("Tenant '%s' storage tier catalog resolution failed and requested tier(s) have no StorageClass in Tenant status: %s",
				tenant.GetName(), strings.Join(unmapped, ", "))
		}
	}

	if unmapped := tiersWithoutStorageClass(unresolvedTiers, classCounts); len(unmapped) > 0 {
		return false, fmt.Sprintf("Tenant '%s' storage tier catalog has unresolved backend references for requested tier(s): %s",
			tenant.GetName(), strings.Join(unmapped, ", "))
	}

	if len(reqCSPTiers) == 0 {
		return true, ""
	}

	srCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady)
	if srCond == nil {
		return false, fmt.Sprintf("Tenant '%s' CSP storage onboarding is not complete: %s condition is not set",
			tenant.GetName(), v1alpha1.TenantConditionStorageReady)
	}
	if srCond.Status != metav1.ConditionTrue {
		return false, fmt.Sprintf("Tenant '%s' CSP storage onboarding is not complete (%s=%s, reason=%s): %s",
			tenant.GetName(), v1alpha1.TenantConditionStorageReady, srCond.Status, srCond.Reason, srCond.Message)
	}

	var missing []string
	var ambiguous []string
	for _, tier := range reqCSPTiers {
		switch classCounts[tier] {
		case 0:
			missing = append(missing, tier)
		case 1:
			// Unambiguously resolved.
		default:
			ambiguous = append(ambiguous, tier)
		}
	}

	if len(missing) == 0 && len(ambiguous) == 0 {
		return true, ""
	}

	var details []string
	if len(missing) > 0 {
		details = append(details, fmt.Sprintf("missing tier(s): %s", strings.Join(missing, ", ")))
	}
	if len(ambiguous) > 0 {
		details = append(details, fmt.Sprintf("ambiguous tier(s): %s", strings.Join(ambiguous, ", ")))
	}
	return false, fmt.Sprintf("Tenant '%s' storage classes are not ready for requested CSP-managed tier(s): %s",
		tenant.GetName(), strings.Join(details, "; "))
}

// countStorageClassNamesByTier returns, for each lowercased tier in
// Tenant.status.storageClasses, the number of distinct non-empty StorageClass
// names mapped to it. A count of 1 is a usable, unambiguous mapping.
func countStorageClassNamesByTier(tenant *v1alpha1.Tenant) map[string]int {
	names := make(map[string]map[string]bool)
	for _, sc := range tenant.Status.StorageClasses {
		name := strings.TrimSpace(sc.Name)
		tier := strings.ToLower(strings.TrimSpace(sc.Tier))
		if name == "" || tier == "" {
			continue
		}
		if names[tier] == nil {
			names[tier] = make(map[string]bool)
		}
		names[tier][name] = true
	}
	counts := make(map[string]int, len(names))
	for tier, tierNames := range names {
		counts[tier] = len(tierNames)
	}
	return counts
}

// tiersWithoutStorageClass returns the subset of tiers that do not map to
// exactly one StorageClass in classCounts.
func tiersWithoutStorageClass(tiers []string, classCounts map[string]int) []string {
	var out []string
	for _, tier := range tiers {
		if classCounts[tier] != 1 {
			out = append(out, tier)
		}
	}
	return out
}

// tenantStorageClassesAuthoritative reports whether the StorageReconciler
// manages this tenant's storage, making Tenant.status.storageClasses the only
// StorageClass source for provisioning. A requested tier missing from status
// can then only be served by JIT provisioning, which needs the tier catalog.
// Tenants without storage status use the target-cluster StorageClass lookup.
func tenantStorageClassesAuthoritative(tenant *v1alpha1.Tenant) bool {
	if len(tenant.Status.StorageClasses) > 0 ||
		tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady) != nil ||
		tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady) != nil {
		return true
	}
	sbCond := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageBackendReady)
	return sbCond != nil && sbCond.Reason == "TierResolutionFailed"
}

// filterStorageClassesToRequestedTiers returns the subset of scs with a
// non-empty Name whose lowercased Tier is in requestedTiers.
func filterStorageClassesToRequestedTiers(
	scs []v1alpha1.ResolvedStorageClass,
	requestedTiers []string,
) []v1alpha1.ResolvedStorageClass {
	if len(scs) == 0 || len(requestedTiers) == 0 {
		return nil
	}
	wanted := make(map[string]bool, len(requestedTiers))
	for _, tier := range requestedTiers {
		wanted[strings.ToLower(strings.TrimSpace(tier))] = true
	}
	var filtered []v1alpha1.ResolvedStorageClass
	for _, sc := range scs {
		if strings.TrimSpace(sc.Name) == "" {
			continue
		}
		if wanted[strings.ToLower(strings.TrimSpace(sc.Tier))] {
			filtered = append(filtered, sc)
		}
	}
	return filtered
}
