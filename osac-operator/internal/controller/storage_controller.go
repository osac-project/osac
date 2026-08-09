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
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mc "sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const (
	storageFinalizer        = "osac.openshift.io/storage"
	clusterStorageFinalizer = "osac.openshift.io/cluster-storage"
	storageControllerName   = "storage-controller"
)

// StorageBackendsClient is a narrow subset of the generated privatev1.StorageBackendsClient
// used to check whether any storage backend is registered (List) and to resolve a single
// backend's provider and connection details by ID (Get). The generated client satisfies
// this interface automatically; it is defined here to allow test mocking.
type StorageBackendsClient interface {
	List(ctx context.Context, in *privatev1.StorageBackendsListRequest, opts ...grpc.CallOption) (*privatev1.StorageBackendsListResponse, error)
	Get(ctx context.Context, in *privatev1.StorageBackendsGetRequest, opts ...grpc.CallOption) (*privatev1.StorageBackendsGetResponse, error)
}

// StorageTiersLister is a narrow subset of the generated privatev1.StorageTiersClient
// used to list tier definitions. The generated client satisfies this interface
// automatically; it is defined here to allow test mocking (mirrors StorageBackendsClient).
type StorageTiersLister interface {
	List(ctx context.Context, in *privatev1.StorageTiersListRequest, opts ...grpc.CallOption) (*privatev1.StorageTiersListResponse, error)
}

// StorageReconciler reconciles storage lifecycle on Tenant CRs.
// It owns StorageBackendReady, ClusterStorageReady conditions,
// status.storageClasses, status.storageBackendJobs, and status.clusterStorageJobs on the Tenant CR.
type StorageReconciler struct {
	client.Client
	APIReader              client.Reader
	Scheme                 *runtime.Scheme
	Recorder               events.EventRecorder
	tenantNamespace        string
	mgr                    mcmanager.Manager
	targetCluster          mc.ClusterName
	BackendProvider        provisioning.ProvisioningProvider
	ClusterStorageProvider provisioning.ProvisioningProvider
	StatusPollInterval     time.Duration
	MaxJobHistory          int
	// BackendsClient queries the fulfillment service Backend API to determine whether
	// a storage backend is registered (List, via backendRegistered()) and to resolve
	// a backend's connection details by ID (Get, via resolveTierDefinitions). When
	// nil (no gRPC connection configured), both backward compatible with
	// environments that run without a fulfillment service connection (e.g.
	// prepare-tenant.sh).
	BackendsClient StorageBackendsClient
	// TiersClient queries the fulfillment service Tier API. When nil, tier
	// validation and extra_vars injection are both skipped — backward compatible
	// with environments without a fulfillment service connection.
	TiersClient StorageTiersLister
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=tenants,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=tenants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=tenants/finalizers,verbs=update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=clusterorders,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedcontrolplanes,verbs=get
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

func NewStorageReconciler(
	mgr mcmanager.Manager,
	tenantNamespace string,
	targetCluster mc.ClusterName,
	backendProvider provisioning.ProvisioningProvider,
	clusterStorageProvider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration,
	maxJobHistory int,
) *StorageReconciler {
	if mgr == nil {
		panic("mgr must not be nil")
	}

	if statusPollInterval == 0 {
		statusPollInterval = 30 * time.Second
	}

	if maxJobHistory <= 0 {
		maxJobHistory = provisioning.DefaultMaxJobHistory
	}

	return &StorageReconciler{
		Client:                 mgr.GetLocalManager().GetClient(),
		APIReader:              mgr.GetLocalManager().GetAPIReader(),
		Scheme:                 mgr.GetLocalManager().GetScheme(),
		Recorder:               mgr.GetLocalManager().GetEventRecorder(storageControllerName),
		tenantNamespace:        tenantNamespace,
		mgr:                    mgr,
		targetCluster:          targetCluster,
		BackendProvider:        backendProvider,
		ClusterStorageProvider: clusterStorageProvider,
		StatusPollInterval:     statusPollInterval,
		MaxJobHistory:          maxJobHistory,
	}
}

func (r *StorageReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	instance := &v1alpha1.Tenant{}
	if err := r.Get(ctx, req.NamespacedName, instance); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if val, exists := instance.Annotations[osacManagementStateAnnotation]; instance.ObjectMeta.DeletionTimestamp.IsZero() && exists && val == ManagementStateUnmanaged {
		log.Info("skipping storage reconciliation — management state is Unmanaged")
		return ctrl.Result{}, nil
	}

	if instance.Status.Phase != v1alpha1.TenantPhaseReady && instance.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	log.Info("start storage reconcile")

	oldstatus := instance.Status.DeepCopy()

	var res ctrl.Result
	var err error
	if instance.ObjectMeta.DeletionTimestamp.IsZero() {
		res, err = r.handleUpdate(ctx, instance)
	} else {
		res, err = r.handleDelete(ctx, instance)
	}

	if !equality.Semantic.DeepEqual(instance.Status, *oldstatus) {
		log.Info("storage status requires update")
		if updateErr := r.patchTenantStorageStatus(ctx, client.ObjectKeyFromObject(instance), instance.Status); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
	}

	log.Info("end storage reconcile")
	return res, err
}

func (r *StorageReconciler) patchTenantStorageStatus(ctx context.Context, key client.ObjectKey, computed v1alpha1.TenantStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.Tenant{}
		if err := r.APIReader.Get(ctx, key, latest); err != nil {
			return err
		}
		base := latest.DeepCopy()
		latest.Status.StorageClasses = computed.StorageClasses
		latest.Status.ClusterStorage = computed.ClusterStorage
		latest.Status.StorageBackendJobs = computed.StorageBackendJobs
		latest.Status.ClusterStorageJobs = computed.ClusterStorageJobs
		for _, c := range computed.Conditions {
			apimeta.SetStatusCondition(&latest.Status.Conditions, c)
		}
		return r.Status().Patch(ctx, latest, client.MergeFrom(base))
	})
}

func (r *StorageReconciler) patchClusterOrderStorageStatus(ctx context.Context, key client.ObjectKey, computed v1alpha1.ClusterOrderStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.ClusterOrder{}
		if err := r.APIReader.Get(ctx, key, latest); err != nil {
			return err
		}
		base := latest.DeepCopy()
		latest.Status.ClusterStorageJobs = computed.ClusterStorageJobs
		for _, c := range computed.Conditions {
			apimeta.SetStatusCondition(&latest.Status.Conditions, c)
		}
		return r.Status().Patch(ctx, latest, client.MergeFrom(base))
	})
}

// handleBackendReadiness evaluates Stage 1 backend readiness and updates the
// StorageBackendReady condition. It returns (hubSecretReady, result, stop, error):
//   - hubSecretReady: whether the hub Secret exists (needed for Stage 2 retry logic)
//   - result: ctrl.Result to return when stop is true
//   - stop: when true, the caller should return (result, err) immediately
//   - error: any unexpected failure
func (r *StorageReconciler) handleBackendReadiness(ctx context.Context, instance *v1alpha1.Tenant, tenantName string) (hubSecretReady bool, result ctrl.Result, stop bool, err error) {
	hubSecretReady, err = r.hubSecretExists(ctx, tenantName)
	if err != nil {
		return false, ctrl.Result{}, true, err
	}

	if hubSecretReady {
		instance.SetStatusCondition(v1alpha1.TenantConditionStorageBackendReady,
			metav1.ConditionTrue,
			v1alpha1.TenantReasonFound,
			fmt.Sprintf("Hub Secret for tenant %q exists", tenantName))
		return true, ctrl.Result{}, false, nil
	}

	// Hub Secret absent: query Backend API to decide the provisioning path.
	// This avoids triggering AAP when AAP is configured for compute provisioning
	// but no storage backend is registered.
	backendRegistered, err := r.backendRegistered(ctx)
	if err != nil {
		return false, ctrl.Result{}, true, fmt.Errorf("check backend registration: %w", err)
	}
	switch {
	case backendRegistered && r.BackendProvider != nil:
		// Backend registered and AAP available: provision the hub Secret.
		instance.SetStatusCondition(v1alpha1.TenantConditionStorageBackendReady,
			metav1.ConditionFalse,
			v1alpha1.TenantReasonNotFound,
			fmt.Sprintf("Hub Secret for tenant %q not found", tenantName))
		provResult, provErr := r.handleBackendProvisioning(ctx, instance)
		return false, provResult, true, provErr
	case backendRegistered:
		// Backend registered but no AAP: cannot provision hub Secret.
		instance.SetStatusCondition(v1alpha1.TenantConditionStorageBackendReady,
			metav1.ConditionFalse,
			v1alpha1.TenantReasonBackendConfiguredNoProvider,
			fmt.Sprintf("Storage backend is registered but no AAP is configured to provision the hub Secret for tenant %q", tenantName))
		return false, ctrl.Result{}, true, nil
	default:
		// Fall through to Stage 2 so the controller resolves StorageClasses from
		// manually labeled SCs. Distinguish the two sub-states so operators can tell
		// whether the fulfillment service is reachable.
		if r.BackendsClient == nil {
			// No gRPC connection configured: backend status is unknown.
			// Normal for prepare-tenant.sh environments without a fulfillment service.
			instance.SetStatusCondition(v1alpha1.TenantConditionStorageBackendReady,
				metav1.ConditionFalse,
				v1alpha1.TenantReasonNoProvider,
				"No fulfillment service connection configured; storage backend status unknown")
		} else {
			// Connected to fulfillment service but no backend is registered yet.
			instance.SetStatusCondition(v1alpha1.TenantConditionStorageBackendReady,
				metav1.ConditionFalse,
				v1alpha1.TenantReasonNoProvider,
				"No storage backend registered")
		}
		return false, ctrl.Result{}, false, nil
	}
}

func (r *StorageReconciler) handleUpdate(ctx context.Context, instance *v1alpha1.Tenant) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	tenantName := instance.GetName()

	log.Info("handling storage update for Tenant", "name", tenantName)

	if !controllerutil.ContainsFinalizer(instance, storageFinalizer) {
		controllerutil.AddFinalizer(instance, storageFinalizer)
		if err := r.Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Resolved once here, before Stage 1 (which can return early via
	// handleBackendReadiness), so both Stage 2's tier-coverage validation and
	// every downstream AAP call in this reconcile see the same result without
	// re-fetching.
	ctx, tierDefinitions := resolveAndInjectTierContext(ctx, r.TiersClient, r.BackendsClient, "tenant", tenantName)

	// Stage 1: check hub Secret and route provisioning based on backend registration.
	// stop is always true when err is non-nil (handleBackendReadiness invariant).
	hubSecretReady, stageResult, stop, err := r.handleBackendReadiness(ctx, instance, tenantName)
	if stop {
		return stageResult, err
	}
	// TODO(OSAC-1111): populate StorageBackendStatus once StorageBackend API provides name/provider

	// Stage 2: resolve StorageClasses on target cluster.
	targetClient, err := getTargetClient(ctx, r.mgr, r.targetCluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	clusterName := string(r.targetCluster)

	if r.ClusterStorageProvider != nil {
		scResult, err := r.resolveTenantSpecificStorageClasses(ctx, targetClient, tenantName)
		if err != nil {
			return ctrl.Result{}, err
		}

		for _, msg := range scResult.duplicateMessages {
			r.Recorder.Eventf(instance, nil, corev1.EventTypeWarning, eventReasonDuplicateStorageClass, eventActionDetectDuplicate, "%s", msg)
		}

		if len(scResult.resolved) == 0 {
			if len(scResult.duplicateMessages) > 0 {
				condMsg := r.appendMissingTierWarnings(instance, tierDefinitions, nil, scResult.ambiguousTiers, strings.Join(scResult.duplicateMessages, "; "))
				instance.SetStatusCondition(v1alpha1.TenantConditionClusterStorageReady,
					metav1.ConditionFalse,
					v1alpha1.TenantReasonMultipleFound,
					condMsg)
				instance.Status.StorageClasses = nil
				instance.Status.ClusterStorage = []v1alpha1.ClusterStorageStatus{
					{ClusterName: clusterName, Ready: false, Reason: v1alpha1.TenantReasonMultipleFound},
				}
				return ctrl.Result{}, nil
			}

			condMsg := r.appendMissingTierWarnings(instance, tierDefinitions, nil, scResult.ambiguousTiers,
				fmt.Sprintf("no StorageClass found for tenant %q", tenantName))
			instance.SetStatusCondition(v1alpha1.TenantConditionClusterStorageReady,
				metav1.ConditionFalse,
				v1alpha1.TenantReasonNotFound,
				condMsg)
			instance.Status.StorageClasses = nil
			instance.Status.ClusterStorage = []v1alpha1.ClusterStorageStatus{
				{ClusterName: clusterName, Ready: false, Reason: v1alpha1.TenantReasonNotFound},
			}

			return r.handleClusterStorageProvisioning(ctx, instance, hubSecretReady)
		}

		condMsg := formatResolvedStorageClasses(scResult.resolved)
		if len(scResult.duplicateMessages) > 0 {
			condMsg = condMsg + "; " + strings.Join(scResult.duplicateMessages, "; ")
		}
		condMsg = r.appendMissingTierWarnings(instance, tierDefinitions, scResult.resolved, scResult.ambiguousTiers, condMsg)
		instance.SetStatusCondition(v1alpha1.TenantConditionClusterStorageReady,
			metav1.ConditionTrue,
			v1alpha1.TenantReasonFound,
			condMsg)
		instance.Status.StorageClasses = scResult.resolved
		instance.Status.ClusterStorage = []v1alpha1.ClusterStorageStatus{
			{ClusterName: clusterName, Ready: true, Reason: v1alpha1.TenantReasonFound},
		}
	} else {
		// When no provisioning provider is configured, resolve StorageClasses
		// labeled osac.openshift.io/tenant=<tenantName>. This serves environments
		// running OSAC without AAP where an admin has pre-provisioned
		// tenant-specific StorageClasses manually.
		result, err := getTenantStorageClasses(ctx, targetClient, tenantName)
		if err != nil {
			return ctrl.Result{}, err
		}

		for _, msg := range result.duplicateMessages {
			r.Recorder.Eventf(instance, nil, corev1.EventTypeWarning, eventReasonDuplicateStorageClass, eventActionDetectDuplicate, "%s", msg)
		}

		if len(result.resolved) == 0 {
			reason := v1alpha1.TenantReasonNotFound
			if len(result.duplicateMessages) > 0 {
				reason = v1alpha1.TenantReasonMultipleFound
			}
			condMsg := r.appendMissingTierWarnings(instance, tierDefinitions, nil, result.ambiguousTiers, result.conditionMessage())
			instance.SetStatusCondition(v1alpha1.TenantConditionClusterStorageReady,
				metav1.ConditionFalse,
				reason,
				condMsg)
			instance.Status.StorageClasses = nil
			instance.Status.ClusterStorage = []v1alpha1.ClusterStorageStatus{
				{ClusterName: clusterName, Ready: false, Reason: reason},
			}
			return ctrl.Result{}, nil
		}

		condMsg := r.appendMissingTierWarnings(instance, tierDefinitions, result.resolved, result.ambiguousTiers, result.conditionMessage())
		instance.SetStatusCondition(v1alpha1.TenantConditionClusterStorageReady,
			metav1.ConditionTrue,
			v1alpha1.TenantReasonFound,
			condMsg)
		instance.Status.StorageClasses = result.resolved
		instance.Status.ClusterStorage = []v1alpha1.ClusterStorageStatus{
			{ClusterName: clusterName, Ready: true, Reason: v1alpha1.TenantReasonFound},
		}
	}

	// Poll any non-terminal class provision job to update its status
	latestClassJob := provisioning.FindLatestJobByType(instance.Status.ClusterStorageJobs, v1alpha1.JobTypeProvision)
	if latestClassJob != nil && !latestClassJob.State.IsTerminal() && r.ClusterStorageProvider != nil {
		return provisioning.PollJob(ctx, r.ClusterStorageProvider, instance,
			&provisioning.State{Jobs: &instance.Status.ClusterStorageJobs},
			latestClassJob, r.StatusPollInterval, nil)
	}

	// Stage 3: provision cluster-side storage on CaaS clusters owned by this tenant.
	// Runs after VMaaS (Stage 2) because CaaS requires StorageBackendReady (Stage 1)
	// to have completed during tenant onboarding before cluster-side resources can
	// be installed.
	if r.ClusterStorageProvider != nil {
		caasResult, caasErr := r.handleCaaSUpdate(ctx, instance)
		if caasErr != nil || caasResult.RequeueAfter > 0 {
			return caasResult, caasErr
		}

		// Handle individual ClusterOrder deletions while the Tenant is still
		// alive. When a ClusterOrder is deleted, mapClusterOrderToTenant
		// triggers Tenant reconciliation, and we clean up cluster-side storage
		// for that specific cluster here.
		caasDelResult, caasDelErr := r.handleCaaSDelete(ctx, instance, false)
		if caasDelErr != nil || caasDelResult.RequeueAfter > 0 {
			return caasDelResult, caasDelErr
		}
	}

	return ctrl.Result{}, nil
}

// handleCaaSUpdate provisions cluster-side storage on CaaS clusters belonging
// to this tenant. For each Ready ClusterOrder without ClusterStorageReady=True,
// it retrieves the kubeconfig and triggers the AAP provisioning job.
func (r *StorageReconciler) handleCaaSUpdate(ctx context.Context, instance *v1alpha1.Tenant) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	tenantName := instance.GetName()

	clusterOrders := &v1alpha1.ClusterOrderList{}
	if err := r.List(ctx, clusterOrders); err != nil {
		return ctrl.Result{}, fmt.Errorf("list ClusterOrders: %w", err)
	}

	for i := range clusterOrders.Items {
		co := &clusterOrders.Items[i]

		annotation, exists := co.GetAnnotations()[osacTenantKey]
		if !exists || annotation != tenantName {
			continue
		}

		if co.Status.Phase != v1alpha1.ClusterOrderPhaseReady {
			continue
		}

		if co.IsStatusConditionTrue(string(v1alpha1.ClusterOrderConditionClusterStorageReady)) {
			continue
		}

		// ClusterOrders being deleted are handled by handleCaaSDelete
		if !co.DeletionTimestamp.IsZero() {
			continue
		}

		log.Info("processing CaaS cluster storage", "clusterOrder", co.Name, "tenant", tenantName)

		if controllerutil.AddFinalizer(co, clusterStorageFinalizer) {
			if err := r.Update(ctx, co); err != nil {
				return ctrl.Result{}, fmt.Errorf("add storage finalizer to ClusterOrder %s: %w", co.Name, err)
			}
		}

		kubeconfig, err := r.getClusterKubeconfig(ctx, co)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("get kubeconfig for ClusterOrder %s: %w", co.Name, err)
		}

		// Continue to the next ClusterOrder rather than returning so that
		// other clusters for this tenant can still be provisioned. The
		// HostedControlPlane may not be ready yet for this cluster.
		if kubeconfig == nil {
			co.SetStatusCondition(
				string(v1alpha1.ClusterOrderConditionClusterStorageReady),
				metav1.ConditionFalse,
				"Kubeconfig not yet available for CaaS cluster",
				"KubeConfigNotAvailable")
			if err := r.patchClusterOrderStorageStatus(ctx, client.ObjectKeyFromObject(co), co.Status); err != nil {
				return ctrl.Result{}, fmt.Errorf("update ClusterOrder %s status: %w", co.Name, err)
			}
			continue
		}

		provCtx := provisioning.WithAdminKubeconfig(ctx, string(kubeconfig))

		provResult, provErr := provisioning.RunProvisioningLifecycle(provCtx, r.ClusterStorageProvider, co,
			&provisioning.State{Jobs: &co.Status.ClusterStorageJobs},
			r.MaxJobHistory, r.StatusPollInterval, nil,
			func() bool {
				return provisioning.CheckAPIServerForNonTerminalProvisionJob(ctx, r.Client,
					client.ObjectKeyFromObject(co), &v1alpha1.ClusterOrder{},
					func(obj client.Object) []v1alpha1.JobStatus {
						return obj.(*v1alpha1.ClusterOrder).Status.ClusterStorageJobs
					})
			},
			func() error {
				return r.patchClusterOrderStorageStatus(ctx, client.ObjectKeyFromObject(co), co.Status)
			},
		)
		if provErr != nil {
			co.SetStatusCondition(
				string(v1alpha1.ClusterOrderConditionClusterStorageReady),
				metav1.ConditionFalse,
				fmt.Sprintf("Provisioning failed: %v", provErr),
				"ProvisionFailed")
			if updateErr := r.patchClusterOrderStorageStatus(ctx, client.ObjectKeyFromObject(co), co.Status); updateErr != nil {
				log.Error(updateErr, "failed to update ClusterOrder status after provision error", "clusterOrder", co.Name)
			}
			return provResult, provErr
		}

		if provResult.RequeueAfter > 0 {
			if err := r.patchClusterOrderStorageStatus(ctx, client.ObjectKeyFromObject(co), co.Status); err != nil {
				return ctrl.Result{}, fmt.Errorf("update ClusterOrder %s status: %w", co.Name, err)
			}
			return provResult, nil
		}

		// Provisioning complete: discover StorageClasses on the CaaS cluster
		caasClient, err := r.buildClientFromKubeconfig(kubeconfig)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("build CaaS client for ClusterOrder %s: %w", co.Name, err)
		}

		scResult, err := getTenantStorageClasses(ctx, caasClient, tenantName)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("get StorageClasses on CaaS cluster %s: %w", co.Name, err)
		}

		for _, msg := range scResult.duplicateMessages {
			r.Recorder.Eventf(co, nil, corev1.EventTypeWarning, eventReasonDuplicateStorageClass, eventActionDetectDuplicate, "%s", msg)
		}

		if len(scResult.resolved) > 0 {
			co.SetStatusCondition(
				string(v1alpha1.ClusterOrderConditionClusterStorageReady),
				metav1.ConditionTrue,
				scResult.conditionMessage(),
				v1alpha1.TenantReasonFound)
			r.Recorder.Eventf(co, nil, corev1.EventTypeNormal, "ClusterStorageProvisioned", "Provision",
				"Storage provisioned on CaaS cluster %s", co.Name)
		} else {
			reason := v1alpha1.TenantReasonNotFound
			if len(scResult.duplicateMessages) > 0 {
				reason = v1alpha1.TenantReasonMultipleFound
			}
			co.SetStatusCondition(
				string(v1alpha1.ClusterOrderConditionClusterStorageReady),
				metav1.ConditionFalse,
				scResult.conditionMessage(),
				reason)
		}

		if err := r.patchClusterOrderStorageStatus(ctx, client.ObjectKeyFromObject(co), co.Status); err != nil {
			return ctrl.Result{}, fmt.Errorf("update ClusterOrder %s status: %w", co.Name, err)
		}

		r.updateTenantClusterStorage(instance, co)
	}

	return ctrl.Result{}, nil
}

func (r *StorageReconciler) buildClientFromKubeconfig(kubeconfig []byte) (client.Client, error) {
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	c, err := client.New(restConfig, client.Options{Scheme: r.Scheme})
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}
	return c, nil
}

// updateTenantClusterStorage adds or updates the CaaS cluster entry in
// Tenant.status.clusterStorage without overwriting existing entries.
func (r *StorageReconciler) updateTenantClusterStorage(tenant *v1alpha1.Tenant, co *v1alpha1.ClusterOrder) {
	clusterName := co.Name
	ready := co.IsStatusConditionTrue(string(v1alpha1.ClusterOrderConditionClusterStorageReady))
	reason := v1alpha1.TenantReasonNotFound
	if ready {
		reason = v1alpha1.TenantReasonFound
	} else if cond := apimeta.FindStatusCondition(co.Status.Conditions, string(v1alpha1.ClusterOrderConditionClusterStorageReady)); cond != nil {
		reason = cond.Reason
	}

	for i, cs := range tenant.Status.ClusterStorage {
		if cs.ClusterName == clusterName {
			tenant.Status.ClusterStorage[i].Ready = ready
			tenant.Status.ClusterStorage[i].Reason = reason
			return
		}
	}
	tenant.Status.ClusterStorage = append(tenant.Status.ClusterStorage, v1alpha1.ClusterStorageStatus{
		ClusterName: clusterName,
		Ready:       ready,
		Reason:      reason,
	})
}

func (r *StorageReconciler) handleDelete(ctx context.Context, instance *v1alpha1.Tenant) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("handling storage delete for Tenant", "name", instance.Name)

	if !controllerutil.ContainsFinalizer(instance, storageFinalizer) {
		return ctrl.Result{}, nil
	}

	// Resolved independently from handleUpdate's resolution (no caching between
	// create and delete paths), before the first AAP-triggering call in this
	// function, so every downstream call in this reconcile sees the same result.
	ctx, _ = resolveAndInjectTierContext(ctx, r.TiersClient, r.BackendsClient, "tenant", instance.Name)

	// CaaS cleanup: remove cluster-side storage (StorageClasses, CSI) from
	// all CaaS clusters and remove our finalizer from their ClusterOrders.
	// The ClusterOrders themselves are not being deleted (they have no
	// OwnerReference to the Tenant), but the storage backend is about to be
	// torn down so cluster-side resources must be removed first.
	if r.ClusterStorageProvider != nil {
		caasResult, caasErr := r.handleCaaSDelete(ctx, instance, true)
		if caasErr != nil || caasResult.RequeueAfter > 0 {
			return caasResult, caasErr
		}
	}

	// Stage 1: class cleanup
	classDeprovJob := provisioning.FindLatestJobByType(instance.Status.ClusterStorageJobs, v1alpha1.JobTypeDeprovision)
	classCleanupDone := classDeprovJob != nil && classDeprovJob.State.IsTerminal() && classDeprovJob.State.IsSuccessful()

	if !classCleanupDone {
		result, err := r.handleClusterStorageDeprovisioning(ctx, instance)
		if err != nil {
			return result, err
		}
		if result.RequeueAfter > 0 {
			return result, nil
		}
		// Class cleanup just completed successfully — fall through to backend
	}

	// Stage 2: backend teardown
	result, err := r.handleBackendDeprovisioning(ctx, instance)
	if err != nil {
		return result, err
	}
	if result.RequeueAfter > 0 {
		return result, nil
	}

	controllerutil.RemoveFinalizer(instance, storageFinalizer)
	if err := r.Update(ctx, instance); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("storage finalizer removed, deletion will proceed")
	return ctrl.Result{}, nil
}

// --- Stage 1: Backend provisioning ---

func (r *StorageReconciler) handleBackendProvisioning(ctx context.Context, instance *v1alpha1.Tenant) (ctrl.Result, error) {
	latestJob := provisioning.FindLatestJobByType(instance.Status.StorageBackendJobs, v1alpha1.JobTypeProvision)
	if latestJob != nil && latestJob.State == v1alpha1.JobStateFailed {
		ctrllog.FromContext(ctx).Info("latest backend provision job failed, waiting for external trigger to retry",
			"message", latestJob.Message)
		return ctrl.Result{}, nil
	}

	return provisioning.RunProvisioningLifecycle(ctx, r.BackendProvider, instance,
		&provisioning.State{Jobs: &instance.Status.StorageBackendJobs},
		r.MaxJobHistory, r.StatusPollInterval, nil,
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(ctx, r.Client,
				client.ObjectKeyFromObject(instance), &v1alpha1.Tenant{},
				func(obj client.Object) []v1alpha1.JobStatus {
					return obj.(*v1alpha1.Tenant).Status.StorageBackendJobs
				})
		},
		func() error {
			return r.patchTenantStorageStatus(ctx, client.ObjectKeyFromObject(instance), instance.Status)
		},
	)
}

// --- Stage 2: Cluster storage provisioning ---

// handleClusterStorageProvisioning triggers or polls the AAP job that creates
// StorageClasses on the target cluster. hubSecretReady indicates whether the
// storage backend hub Secret already exists for this tenant; when true, a failed
// job is retried with backoff because the backend is provisioned and the failure
// is likely transient. When false (no backend registered), the controller waits
// for an external trigger before retrying.
func (r *StorageReconciler) handleClusterStorageProvisioning(ctx context.Context, instance *v1alpha1.Tenant, hubSecretReady bool) (ctrl.Result, error) {
	latestJob := provisioning.FindLatestJobByType(instance.Status.ClusterStorageJobs, v1alpha1.JobTypeProvision)
	if latestJob != nil && latestJob.State == v1alpha1.JobStateFailed {
		if hubSecretReady {
			// Hub Secret exists: the storage backend is provisioned. Requeue
			// periodically so the controller picks up when the failed job is
			// externally cleared or the AAP template becomes available.
			ctrllog.FromContext(ctx).Info("latest cluster storage provision job failed, requeueing",
				"message", latestJob.Message)
			return ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
		}
		ctrllog.FromContext(ctx).Info("latest cluster storage provision job failed, waiting for external trigger to retry",
			"message", latestJob.Message)
		return ctrl.Result{}, nil
	}

	return provisioning.RunProvisioningLifecycle(ctx, r.ClusterStorageProvider, instance,
		&provisioning.State{Jobs: &instance.Status.ClusterStorageJobs},
		r.MaxJobHistory, r.StatusPollInterval, nil,
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(ctx, r.Client,
				client.ObjectKeyFromObject(instance), &v1alpha1.Tenant{},
				func(obj client.Object) []v1alpha1.JobStatus {
					return obj.(*v1alpha1.Tenant).Status.ClusterStorageJobs
				})
		},
		func() error {
			return r.patchTenantStorageStatus(ctx, client.ObjectKeyFromObject(instance), instance.Status)
		},
	)
}

// --- CaaS Deprovisioning ---

// handleCaaSDelete removes cluster-side storage from CaaS clusters that have
// the storage finalizer.
//
// When tenantDeleting=true (called from handleDelete): processes ALL ClusterOrders
// with our finalizer because the backend is about to be torn down.
//
// When tenantDeleting=false (called from handleUpdate): processes only ClusterOrders
// with a DeletionTimestamp, handling the case where a single cluster is being
// destroyed while the Tenant is still alive.
//
// If the HostedControlPlane is already gone (cluster destroyed outside OSAC),
// cleanup is skipped because there are no cluster-side resources left to remove.
func (r *StorageReconciler) handleCaaSDelete(ctx context.Context, instance *v1alpha1.Tenant, tenantDeleting bool) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	tenantName := instance.GetName()

	clusterOrders := &v1alpha1.ClusterOrderList{}
	if err := r.List(ctx, clusterOrders); err != nil {
		return ctrl.Result{}, fmt.Errorf("list ClusterOrders for CaaS teardown: %w", err)
	}

	for i := range clusterOrders.Items {
		co := &clusterOrders.Items[i]

		if !controllerutil.ContainsFinalizer(co, clusterStorageFinalizer) {
			continue
		}

		annotation, exists := co.GetAnnotations()[osacTenantKey]
		if !exists || annotation != tenantName {
			continue
		}

		// When called from handleUpdate, only process ClusterOrders that are
		// actively being deleted. When the Tenant itself is being deleted, we
		// process all ClusterOrders to clean up before backend teardown.
		if !tenantDeleting && co.DeletionTimestamp.IsZero() {
			continue
		}

		log.Info("tearing down CaaS cluster storage", "clusterOrder", co.Name, "tenant", tenantName)

		kubeconfig, err := r.getClusterKubeconfig(ctx, co)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("get kubeconfig for teardown of ClusterOrder %s: %w", co.Name, err)
		}

		if kubeconfig == nil {
			// HostedControlPlane is gone, so the cluster no longer exists.
			// Nothing to clean up on the cluster side.
			log.Info("HostedControlPlane not found during teardown, skipping cluster-side cleanup",
				"clusterOrder", co.Name, "tenant", tenantName)
			r.Recorder.Eventf(co, nil, corev1.EventTypeWarning, "KubeConfigNotAvailable", "Teardown",
				"HostedControlPlane not found during storage teardown, skipping cleanup")
		} else {
			provCtx := provisioning.WithAdminKubeconfig(ctx, string(kubeconfig))

			result, done, err := provisioning.RunDeprovisioningLifecycle(provCtx, r.ClusterStorageProvider, co,
				&co.Status.ClusterStorageJobs, r.MaxJobHistory, r.StatusPollInterval)
			if err != nil {
				if updateErr := r.patchClusterOrderStorageStatus(ctx, client.ObjectKeyFromObject(co), co.Status); updateErr != nil {
					log.Error(updateErr, "failed to update ClusterOrder status after teardown error", "clusterOrder", co.Name)
				}
				return result, err
			}
			if !done {
				if updateErr := r.patchClusterOrderStorageStatus(ctx, client.ObjectKeyFromObject(co), co.Status); updateErr != nil {
					log.Error(updateErr, "failed to update ClusterOrder status during teardown", "clusterOrder", co.Name)
				}
				return result, nil
			}
		}

		r.removeTenantClusterStorageEntry(instance, co.Name)

		controllerutil.RemoveFinalizer(co, clusterStorageFinalizer)
		if err := r.Update(ctx, co); err != nil {
			return ctrl.Result{}, fmt.Errorf("remove storage finalizer from ClusterOrder %s: %w", co.Name, err)
		}

		log.Info("CaaS cluster storage teardown complete", "clusterOrder", co.Name, "tenant", tenantName)
	}

	return ctrl.Result{}, nil
}

func (r *StorageReconciler) removeTenantClusterStorageEntry(tenant *v1alpha1.Tenant, clusterName string) {
	filtered := make([]v1alpha1.ClusterStorageStatus, 0, len(tenant.Status.ClusterStorage))
	for _, cs := range tenant.Status.ClusterStorage {
		if cs.ClusterName != clusterName {
			filtered = append(filtered, cs)
		}
	}
	tenant.Status.ClusterStorage = filtered
}

// --- VMaaS Deprovisioning ---

func (r *StorageReconciler) handleClusterStorageDeprovisioning(ctx context.Context, instance *v1alpha1.Tenant) (ctrl.Result, error) {
	if r.ClusterStorageProvider == nil {
		ctrllog.FromContext(ctx).Info("no class provider configured, skipping cluster-side cleanup")
		return ctrl.Result{}, nil
	}

	result, done, err := provisioning.RunDeprovisioningLifecycle(ctx, r.ClusterStorageProvider, instance,
		&instance.Status.ClusterStorageJobs, r.MaxJobHistory, r.StatusPollInterval)
	if err != nil || !done {
		return result, err
	}
	return ctrl.Result{}, nil
}

func (r *StorageReconciler) handleBackendDeprovisioning(ctx context.Context, instance *v1alpha1.Tenant) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	if r.BackendProvider == nil {
		log.Info("no backend provider configured, skipping backend teardown")
		return ctrl.Result{}, nil
	}

	hubSecretReady, err := r.hubSecretExists(ctx, instance.GetName())
	if err != nil {
		log.Error(err, "failed to check hub Secret existence, requeueing")
		return ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
	}

	latestJob := provisioning.FindLatestJobByType(instance.Status.StorageBackendJobs, v1alpha1.JobTypeDeprovision)
	deprovJobActive := latestJob != nil && latestJob.JobID != "" && !latestJob.State.IsTerminal()
	deprovJobBlocking := latestJob != nil &&
		latestJob.State.IsTerminal() &&
		!latestJob.State.IsSuccessful() &&
		latestJob.BlockDeletionOnFailure

	if !hubSecretReady && !deprovJobActive && !deprovJobBlocking {
		return ctrl.Result{}, nil
	}

	result, done, err := provisioning.RunDeprovisioningLifecycle(ctx, r.BackendProvider, instance,
		&instance.Status.StorageBackendJobs, r.MaxJobHistory, r.StatusPollInterval)
	if err != nil || !done {
		return result, err
	}
	return ctrl.Result{}, nil
}

// --- Helpers ---

func (r *StorageReconciler) hubSecretExists(ctx context.Context, tenantName string) (bool, error) {
	var secretList corev1.SecretList
	if err := r.List(ctx, &secretList,
		client.InNamespace(storageConfigNamespace()),
		client.MatchingLabels{osacTenantKey: tenantName},
	); err != nil {
		return false, err
	}
	return len(secretList.Items) > 0, nil
}

// backendRegistered reports whether at least one StorageBackend is registered in the
// fulfillment service. When BackendsClient is nil (no gRPC connection configured),
// it returns (false, nil) — preserving the existing behavior for deployments that
// run without a fulfillment service connection. On transient gRPC failure the error
// is returned so the caller can requeue rather than silently falling back.
func (r *StorageReconciler) backendRegistered(ctx context.Context) (bool, error) {
	if r.BackendsClient == nil {
		return false, nil
	}
	req := &privatev1.StorageBackendsListRequest{}
	req.SetLimit(1)
	resp, err := r.BackendsClient.List(ctx, req)
	if err != nil {
		return false, fmt.Errorf("list storage backends: %w", err)
	}
	return resp.GetTotal() > 0, nil
}

// storageConfigNamespace returns the namespace where storage config Secrets are stored.
// In production, OSAC_STORAGE_CONFIG_NAMESPACE is set by the Helm chart or kustomize
// overlay to match the deployment namespace. The fallback is for local development only.
func storageConfigNamespace() string {
	if ns := os.Getenv("OSAC_STORAGE_CONFIG_NAMESPACE"); ns != "" {
		return ns
	}
	return "osac-system"
}

// --- Watch mapping ---

func (r *StorageReconciler) mapStorageClassToTenant(ctx context.Context, obj client.Object) []reconcile.Request {
	log := ctrllog.FromContext(ctx)

	tenantName, exists := obj.GetLabels()[osacTenantKey]
	if !exists || tenantName == "" {
		return nil
	}

	tenant := &v1alpha1.Tenant{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.tenantNamespace, Name: tenantName}, tenant); err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "unable to get Tenant for StorageClass",
				"storageClass", obj.GetName(), "tenant", tenantName)
		}
		return nil
	}

	return []reconcile.Request{{NamespacedName: client.ObjectKeyFromObject(tenant)}}
}

func (r *StorageReconciler) mapSecretToTenant(ctx context.Context, obj client.Object) []reconcile.Request {
	tenantName, exists := obj.GetLabels()[osacTenantKey]
	if !exists || tenantName == "" {
		return nil
	}

	tenant := &v1alpha1.Tenant{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.tenantNamespace, Name: tenantName}, tenant); err != nil {
		return nil
	}

	return []reconcile.Request{{NamespacedName: client.ObjectKeyFromObject(tenant)}}
}

// tenantSpecificStorageClasses is the result of resolveTenantSpecificStorageClasses:
// resolved StorageClasses per tier, plus the duplicate-SC messages and ambiguous
// tier names for tiers excluded from resolved because multiple StorageClasses
// matched (distinct from a tier having no StorageClass at all).
type tenantSpecificStorageClasses struct {
	resolved          []v1alpha1.ResolvedStorageClass
	duplicateMessages []string
	ambiguousTiers    []string
}

// resolveTenantSpecificStorageClasses lists StorageClasses labeled with the
// given tenant name. Used when AAP is configured.
func (r *StorageReconciler) resolveTenantSpecificStorageClasses(
	ctx context.Context, targetClient client.Client, tenantName string,
) (tenantSpecificStorageClasses, error) {
	log := ctrllog.FromContext(ctx)
	scList := &storagev1.StorageClassList{}
	if err := targetClient.List(ctx, scList, client.MatchingLabels{osacTenantKey: tenantName}); err != nil {
		return tenantSpecificStorageClasses{}, err
	}
	byTier := groupByTier(scList.Items)
	sortedTiers := make([]string, 0, len(byTier))
	for tier := range byTier {
		sortedTiers = append(sortedTiers, tier)
	}
	sort.Strings(sortedTiers)

	var result tenantSpecificStorageClasses
	for _, tier := range sortedTiers {
		scs := byTier[tier]
		switch len(scs) {
		case 1:
			result.resolved = append(result.resolved, v1alpha1.ResolvedStorageClass{
				Name: scs[0].GetName(),
				Tier: tier,
			})
		default:
			joined, names := joinStorageClassNames(scs)
			msg := fmt.Sprintf("tier %q: multiple tenant StorageClasses [%s]", tier, joined)
			log.Info(msg, "tenant", tenantName, "tier", tier, "storageClasses", names)
			result.duplicateMessages = append(result.duplicateMessages, msg)
			result.ambiguousTiers = append(result.ambiguousTiers, tier)
		}
	}
	return result, nil
}

// formatResolvedStorageClasses builds a human-readable condition message
// listing each resolved tier and its StorageClass name.
func formatResolvedStorageClasses(classes []v1alpha1.ResolvedStorageClass) string {
	parts := make([]string, len(classes))
	for i, sc := range classes {
		parts[i] = fmt.Sprintf("tier %q: StorageClass %q (tenant-specific)", sc.Tier, sc.Name)
	}
	return strings.Join(parts, "; ")
}

// mapClusterOrderToTenant maps ClusterOrder events to the owning Tenant so the
// storage controller can evaluate CaaS cluster storage provisioning/teardown.
func (r *StorageReconciler) mapClusterOrderToTenant(ctx context.Context, obj client.Object) []reconcile.Request {
	log := ctrllog.FromContext(ctx)

	tenantName, exists := obj.GetAnnotations()[osacTenantKey]
	if !exists || tenantName == "" {
		return nil
	}

	tenant := &v1alpha1.Tenant{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.tenantNamespace, Name: tenantName}, tenant); err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "unable to get Tenant for ClusterOrder",
				"clusterOrder", obj.GetName(), "tenant", tenantName)
		}
		return nil
	}

	return []reconcile.Request{{NamespacedName: client.ObjectKeyFromObject(tenant)}}
}

// getClusterKubeconfig retrieves the admin kubeconfig for a CaaS cluster by
// reading the HostedControlPlane's kubeConfig Secret reference. Returns nil
// without error if the HostedControlPlane or Secret is not yet available.
func (r *StorageReconciler) getClusterKubeconfig(ctx context.Context, clusterOrder *v1alpha1.ClusterOrder) ([]byte, error) {
	log := ctrllog.FromContext(ctx)
	ref := clusterOrder.Status.ClusterReference
	if ref == nil || ref.Namespace == "" || ref.HostedClusterName == "" {
		log.V(1).Info("no cluster reference on ClusterOrder", "clusterOrder", clusterOrder.Name)
		return nil, nil
	}

	// HyperShift places the HostedControlPlane in {HostedCluster-namespace}-{HostedCluster-name}.
	hcpNamespace := ref.Namespace + "-" + ref.HostedClusterName

	hcp := &hypershiftv1beta1.HostedControlPlane{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: hcpNamespace,
		Name:      ref.HostedClusterName,
	}, hcp); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, fmt.Errorf("get HostedControlPlane %s/%s: %w", hcpNamespace, ref.HostedClusterName, err)
		}
		log.Info("HostedControlPlane not found", "clusterOrder", clusterOrder.Name,
			"namespace", hcpNamespace, "hostedClusterName", ref.HostedClusterName)
		return nil, nil
	}

	if hcp.Status.KubeConfig == nil || hcp.Status.KubeConfig.Name == "" {
		log.Info("HostedControlPlane kubeConfig reference not yet populated",
			"clusterOrder", clusterOrder.Name, "hostedControlPlane", hcp.Name)
		return nil, nil
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: hcpNamespace,
		Name:      hcp.Status.KubeConfig.Name,
	}, secret); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, fmt.Errorf("get kubeconfig Secret %s/%s: %w", hcpNamespace, hcp.Status.KubeConfig.Name, err)
		}
		log.Info("kubeconfig Secret not found", "clusterOrder", clusterOrder.Name,
			"secret", hcp.Status.KubeConfig.Name, "namespace", hcpNamespace)
		return nil, nil
	}

	kubeconfig, exists := secret.Data[hcp.Status.KubeConfig.Key]
	if !exists || len(kubeconfig) == 0 {
		log.Info("kubeconfig key missing or empty in Secret", "clusterOrder", clusterOrder.Name,
			"secret", secret.Name, "key", hcp.Status.KubeConfig.Key)
		return nil, nil
	}

	return kubeconfig, nil
}

func (r *StorageReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mgr).
		For(&v1alpha1.Tenant{},
			mcbuilder.WithPredicates(tenantNamespacePredicate(r.tenantNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Named("storage").
		Watches(
			&v1alpha1.ClusterOrder{},
			mchandler.EnqueueRequestsFromMapFunc(r.mapClusterOrderToTenant),
		).
		Watches(
			&storagev1.StorageClass{},
			mchandler.EnqueueRequestsFromMapFunc(r.mapStorageClassToTenant),
			mcbuilder.WithPredicates(storageClassTenantPredicate()),
		).
		Watches(
			&corev1.Secret{},
			mchandler.EnqueueRequestsFromMapFunc(r.mapSecretToTenant),
			mcbuilder.WithPredicates(secretTenantPredicate()),
		).
		Complete(r)
}

func secretTenantPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		if obj.GetNamespace() != storageConfigNamespace() {
			return false
		}
		_, exists := obj.GetLabels()[osacTenantKey]
		return exists
	})
}
