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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	kubevirtv1 "kubevirt.io/api/core/v1"
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
	// fulfillment service connection. See resolveAndInjectTierContext.
	TiersClient    StorageTiersLister
	BackendsClient StorageBackendsClient
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
					instance.Status.Phase = v1alpha1.ComputeInstancePhaseFailed
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
		instance.Status.Phase = v1alpha1.ComputeInstancePhaseStarting
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
		msg := fmt.Sprintf("Tenant '%s' is not ready (phase: %s)", tenant.GetName(), tenant.Status.Phase)
		if scCond := tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady); scCond != nil && scCond.Message != "" {
			msg = fmt.Sprintf("%s. %s: %s", msg, scCond.Type, scCond.Message)
		}
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

	// When a networkAttachment is set, the VM is created in the subnet target namespace
	// (by the AAP playbook), not in the tenant target namespace.  Reuse the
	// value resolved by syncMetadataPreflight to avoid a redundant API call.
	targetNamespace := tenant.Status.Namespace
	if subnetTargetNamespace != "" {
		targetNamespace = subnetTargetNamespace
	}

	kv, err := r.findKubeVirtVMs(ctx, targetClient, instance, targetNamespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	if kv != nil {
		if err := r.handleKubeVirtVM(ctx, targetClient, instance, kv); err != nil {
			return ctrl.Result{}, err
		}
		instance.Status.Phase = determinePhaseFromPrintableStatus(ctx, kv, instance.Status.Phase)
	} else {
		// No KubeVirt VM exists yet: infrastructure is being provisioned.
		instance.Status.Phase = v1alpha1.ComputeInstancePhaseStarting
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

	// Inject tenant storage classes into context for AAP extra_vars.
	// Prefer Tenant.status.storageClasses (populated by the storage controller);
	// fall back to resolving labeled StorageClasses on the target cluster only
	// when the ClusterStorageReady condition is absent (storage controller
	// disabled or hasn't reconciled this tenant yet).
	if len(tenant.Status.StorageClasses) > 0 {
		ctx = provisioning.WithTenantStorageClasses(ctx, tenant.Status.StorageClasses)
	} else if tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady) == nil {
		tenantName := tenant.GetName()
		resolution, err := getTenantStorageClasses(ctx, targetClient, tenantName)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to resolve tenant storage classes from target cluster for tenant %s: %w", tenantName, err)
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

	// Inject storage tier definitions and backend connections for JIT storage
	// provisioning — osac-aap's playbook_osac_create_compute_instance.yml reads
	// these to provision an on-demand StorageClass when the tenant doesn't
	// already have one for the requested tier (OSAC-1992).
	ctx, _ = resolveAndInjectTierContext(ctx, r.TiersClient, r.BackendsClient, "computeInstance", instance.GetName())

	// Always delegate to provisioning lifecycle — EvaluateAction decides
	// whether to skip, poll, or trigger. This avoids the A-B-A problem where
	// IsConfigApplied alone could match a stale historical job.
	return r.handleProvisioning(ctx, instance)
}

func (r *ComputeInstanceReconciler) handleDelete(ctx context.Context, _ reconcile.Request, instance *v1alpha1.ComputeInstance) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("deleting compute instance")

	instance.Status.Phase = v1alpha1.ComputeInstancePhaseDeleting

	// Base finalizer has already been removed, cleanup complete
	if !controllerutil.ContainsFinalizer(instance, osacComputeInstanceFinalizer) {
		return ctrl.Result{}, nil
	}

	// Handle deprovisioning - provider decides internally if needed
	log.Info("handling deletion")
	result, err := r.handleDeprovisioning(ctx, instance)
	if err != nil {
		return result, err
	}

	// If we need to requeue (jobs still running or provider needs time), do so
	if result.RequeueAfter > 0 {
		return result, nil
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
	if kvVMHasConditionWithStatus(kv, kubevirtv1.VirtualMachineReady, corev1.ConditionTrue) {
		ipAddress := r.getFirstVMIIPAddress(ctx, targetClient, kv.GetNamespace(), name)

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

// getFirstVMIIPAddress fetches the VirtualMachineInstance and returns the first non-empty
// IP from .status.interfaces[*].ipAddress, or "" if none or on error.
func (r *ComputeInstanceReconciler) getFirstVMIIPAddress(ctx context.Context, targetClient client.Client, namespace, name string) string {
	log := ctrllog.FromContext(ctx)

	vmi := &kubevirtv1.VirtualMachineInstance{}
	if err := targetClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, vmi); err != nil {
		log.Error(err, "failed to get VirtualMachineInstance", "namespace", namespace, "name", name)
		return ""
	}
	for _, iface := range vmi.Status.Interfaces {
		if iface.IP != "" {
			return iface.IP
		}
	}

	log.Info("no IP address found for VirtualMachineInstance", "namespace", namespace, "name", name)
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
