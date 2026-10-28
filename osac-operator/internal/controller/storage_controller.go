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
	"sync"
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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	crsource "sigs.k8s.io/controller-runtime/pkg/source"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mc "sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	storageFinalizer        = "osac.openshift.io/storage"
	clusterStorageFinalizer = "osac.openshift.io/cluster-storage"
	storageControllerName   = "storage-controller"
	cspManagedTenantValue   = "csp-managed"
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

// SecretsClient is a narrow subset of the generated privatev1.SecretsClient used to
// resolve a storage backend's password from a referenced Secret (Get) when the backend
// supplies password_secret instead of an inline password. The generated client satisfies
// this interface automatically; it is defined here to allow test mocking (mirrors
// StorageBackendsClient).
type SecretsClient interface {
	Get(ctx context.Context, in *privatev1.SecretsGetRequest, opts ...grpc.CallOption) (*privatev1.SecretsGetResponse, error)
}

// StorageReconciler reconciles storage lifecycle on Tenant CRs.
// It owns StorageBackendReady, ClusterStorageReady, and StorageReady conditions,
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
	// SecretsClient resolves a storage backend's password from a referenced Secret
	// when the backend supplies password_secret instead of an inline password. When
	// nil, backends that rely on password_secret are skipped (see resolveTierDefinitions);
	// backends with an inline password are unaffected.
	SecretsClient SecretsClient

	catalogMu     sync.RWMutex
	cachedCatalog *storageCatalogSnapshot
	catalogEvents chan event.TypedGenericEvent[*v1alpha1.Tenant]
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=tenants,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=tenants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=tenants/finalizers,verbs=update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=clusterorders,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedcontrolplanes,verbs=get;list;watch
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
		catalogEvents:          make(chan event.TypedGenericEvent[*v1alpha1.Tenant], 1),
	}
}

func (r *StorageReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	tenant := &v1alpha1.Tenant{}
	if err := r.Get(ctx, req.NamespacedName, tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if val, exists := tenant.Annotations[osacManagementStateAnnotation]; tenant.ObjectMeta.DeletionTimestamp.IsZero() && exists && val == ManagementStateUnmanaged {
		log.Info("skipping storage reconciliation — management state is Unmanaged")
		return ctrl.Result{}, nil
	}

	if tenant.Status.Phase != v1alpha1.TenantPhaseReady && tenant.ObjectMeta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	log.Info("start storage reconcile")

	oldstatus := tenant.Status.DeepCopy()

	var res ctrl.Result
	var err error
	if tenant.ObjectMeta.DeletionTimestamp.IsZero() {
		res, err = r.handleUpdate(ctx, tenant)
	} else {
		res, err = r.handleDelete(ctx, tenant)
	}

	if !equality.Semantic.DeepEqual(tenant.Status, *oldstatus) {
		log.Info("storage status requires update")
		if updateErr := r.patchTenantStorageStatus(ctx, client.ObjectKeyFromObject(tenant), tenant.Status); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
	}

	log.Info("end storage reconcile")
	return res, err
}

var storageOwnedConditionTypes = []v1alpha1.TenantConditionType{
	v1alpha1.TenantConditionStorageReady,
	v1alpha1.TenantConditionStorageBackendReady,
	v1alpha1.TenantConditionClusterStorageReady,
}

// patchTenantStorageStatus persists computed storage fields from a fresh read so
// concurrent status fields owned by other controllers are preserved.
func (r *StorageReconciler) patchTenantStorageStatus(ctx context.Context, key client.ObjectKey, computed v1alpha1.TenantStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.Tenant{}
		if err := r.APIReader.Get(ctx, key, latest); err != nil {
			return err
		}
		base := latest.DeepCopy()
		latest.Status.StorageClasses = computed.StorageClasses
		latest.Status.StorageBackends = computed.StorageBackends
		latest.Status.ClusterStorage = computed.ClusterStorage
		latest.Status.StorageBackendJobs = computed.StorageBackendJobs
		latest.Status.ClusterStorageJobs = computed.ClusterStorageJobs
		for _, condType := range storageOwnedConditionTypes {
			if c := apimeta.FindStatusCondition(computed.Conditions, string(condType)); c != nil {
				apimeta.SetStatusCondition(&latest.Status.Conditions, *c)
			} else {
				apimeta.RemoveStatusCondition(&latest.Status.Conditions, string(condType))
			}
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
//
// tierDefinitions carries the resolved tier catalog from the Tier API. When
// non-empty, the check requires hub Secrets for ALL dispatched providers (one
// per unique TierDefinition.Provider) before declaring StorageBackendReady.
// When nil or empty (no Tier API configured), it falls back to checking for
// any hub Secret regardless of provider — preserving backward compatibility.
func (r *StorageReconciler) handleBackendReadiness(ctx context.Context, tenant *v1alpha1.Tenant, tenantName string, tierDefinitions []provisioning.TierDefinition) (hubSecretReady bool, result ctrl.Result, stop bool, err error) {
	statuses, allReady, err := r.storageBackendStatuses(ctx, tenantName, tierDefinitions)
	if err != nil {
		return false, ctrl.Result{}, true, err
	}
	tenant.Status.StorageBackends = statuses
	hubSecretReady = allReady
	readinessMessage := formatStorageBackendReadinessMessage(statuses, hubSecretReady)

	if hubSecretReady {
		tenant.SetStatusCondition(v1alpha1.TenantConditionStorageBackendReady,
			metav1.ConditionTrue,
			v1alpha1.TenantReasonFound,
			readinessMessage)
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
		tenant.SetStatusCondition(v1alpha1.TenantConditionStorageBackendReady,
			metav1.ConditionFalse,
			v1alpha1.TenantReasonNotFound,
			fmt.Sprintf("%s; Hub Secret for tenant %q not found", readinessMessage, tenantName))
		provResult, provErr := r.handleBackendProvisioning(ctx, tenant)
		return false, provResult, true, provErr
	case backendRegistered:
		// Backend registered but no AAP: cannot provision hub Secret.
		tenant.SetStatusCondition(v1alpha1.TenantConditionStorageBackendReady,
			metav1.ConditionFalse,
			v1alpha1.TenantReasonBackendConfiguredNoProvider,
			fmt.Sprintf("%s; storage backend is registered but no AAP is configured to provision the hub Secret for tenant %q", readinessMessage, tenantName))
		return false, ctrl.Result{}, true, nil
	default:
		// Fall through to Stage 2 so the controller resolves StorageClasses from
		// manually labeled SCs. Distinguish the two sub-states so operators can tell
		// whether the fulfillment service is reachable.
		if r.BackendsClient == nil {
			// No gRPC connection configured: backend status is unknown.
			// Normal for prepare-tenant.sh environments without a fulfillment service.
			tenant.SetStatusCondition(v1alpha1.TenantConditionStorageBackendReady,
				metav1.ConditionFalse,
				v1alpha1.TenantReasonNoProvider,
				readinessMessage+"; no fulfillment service connection configured; storage backend status unknown")
		} else {
			// Connected to fulfillment service but no backend is registered yet.
			tenant.SetStatusCondition(v1alpha1.TenantConditionStorageBackendReady,
				metav1.ConditionFalse,
				v1alpha1.TenantReasonNoProvider,
				readinessMessage+"; no storage backend registered")
		}
		return false, ctrl.Result{}, false, nil
	}
}

// handleUpdate reconciles Tenant storage readiness and provisioning across the
// VMaaS target cluster and any CaaS ClusterOrders owned by the Tenant.
func (r *StorageReconciler) handleUpdate(ctx context.Context, tenant *v1alpha1.Tenant) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	tenantName := tenant.GetName()

	log.Info("handling storage update for Tenant", "name", tenantName)

	if !controllerutil.ContainsFinalizer(tenant, storageFinalizer) {
		controllerutil.AddFinalizer(tenant, storageFinalizer)
		if err := r.Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Resolved once here, before Stage 1 (which can return early via
	// handleBackendReadiness), so both Stage 2's tier-coverage validation and
	// every downstream AAP call in this reconcile see the same result without
	// re-fetching.
	ctx, osacTiers, cspTiers, skippedOSACTiers, tierErr := r.resolveTenantTierContext(ctx, tenantName)

	// Handle individual ClusterOrder deletions while the Tenant is still
	// alive. When a ClusterOrder is deleted, mapClusterOrderToTenant
	// triggers Tenant reconciliation, and we clean up cluster-side storage
	// for that specific cluster here.
	//
	// This runs BEFORE Stage 1 so that early returns from backend readiness
	// or StorageClass resolution cannot prevent CaaS cluster cleanup.
	// When the HostedControlPlane is already gone (kubeconfig==nil), there
	// is nothing to deprovision on the cluster side and the finalizer can
	// be removed immediately. When no ClusterStorageProvider is configured,
	// the finalizer is also removed without attempting AAP cleanup (OSAC-4340).
	caasDelResult, caasDelErr := r.handleCaaSDelete(ctx, tenant, false)
	if caasDelErr != nil || caasDelResult.RequeueAfter > 0 {
		return caasDelResult, caasDelErr
	}

	// Guard: when tier resolution failed, do not proceed — downstream AAP
	// calls would run without storage_tier_definitions and
	// storage_backend_connections, and pure-CSP reconciles would fall through
	// with empty tierDefinitions and wipe status.storageClasses.
	// CaaS cluster deletion (above) already ran and is unaffected.
	if tierErr != nil {
		log.Error(tierErr, "tier resolution failed, deferring storage provisioning until fulfillment service recovers",
			"tenant", tenantName)
		tenant.SetStatusCondition(v1alpha1.TenantConditionStorageBackendReady,
			metav1.ConditionFalse,
			"TierResolutionFailed",
			fmt.Sprintf("Storage tier definitions could not be resolved from the fulfillment service: %v", tierErr))
		return ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
	}

	cspResolved, pureCSP, err := r.handleCSPStorageOnboarding(ctx, tenant, tenantName, osacTiers, cspTiers, skippedOSACTiers)
	if err != nil || pureCSP {
		return ctrl.Result{}, err
	}

	// Stage 1: check hub Secret and route provisioning based on backend registration.
	// stop is always true when err is non-nil (handleBackendReadiness invariant).
	hubSecretReady, stageResult, stop, err := r.handleBackendReadiness(ctx, tenant, tenantName, osacTiers)
	if stop {
		return stageResult, err
	}
	// Stage 2: resolve StorageClasses on target cluster.
	targetClient, err := getTargetClient(ctx, r.mgr, r.targetCluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	clusterName := string(r.targetCluster)

	scResult, err := getTenantStorageClasses(ctx, targetClient, tenantName)
	if err != nil {
		return ctrl.Result{}, err
	}

	for _, msg := range scResult.duplicateMessages {
		r.Recorder.Eventf(tenant, nil, corev1.EventTypeWarning, eventReasonDuplicateStorageClass, eventActionDetectDuplicate, "%s", msg)
	}

	if len(scResult.resolved) == 0 {
		reason := v1alpha1.TenantReasonNotFound
		if len(scResult.duplicateMessages) > 0 {
			reason = v1alpha1.TenantReasonMultipleFound
		}
		condMsg := r.appendMissingTierWarnings(tenant, osacTiers, nil, scResult.ambiguousTiers, scResult.conditionMessage())
		tenant.SetStatusCondition(v1alpha1.TenantConditionClusterStorageReady,
			metav1.ConditionFalse,
			reason,
			condMsg)
		tenant.Status.StorageClasses = mergeResolvedStorageClasses(cspResolved, nil)
		r.updateTenantVMaaSStorage(tenant, clusterName, false, reason)
		if len(scResult.duplicateMessages) == 0 && r.ClusterStorageProvider != nil {
			return r.handleClusterStorageProvisioning(ctx, tenant, hubSecretReady)
		}
		return ctrl.Result{}, nil
	}

	condMsg := r.appendMissingTierWarnings(tenant, osacTiers, scResult.resolved, scResult.ambiguousTiers, scResult.conditionMessage())

	// Detect tiers that still lack a StorageClass. The hasMissingTiers
	// flag drives the Stage 4 retry (handleClusterStorageProvisioning).
	// When tiers are still missing, ClusterStorageReady stays False so
	// handleCaaSUpdate (Stage 3) does not attempt to provision
	// cluster-side storage with an incomplete tier set. This is safe
	// because handleCaaSDelete runs BEFORE this point (line ~339),
	// so finalizer removal on deleting ClusterOrders is never blocked.
	missing := missingTierNames(osacTiers, scResult.resolved, scResult.ambiguousTiers)
	hasMissingTiers := len(missing) > 0

	condStatus := metav1.ConditionTrue
	vmaasReason := v1alpha1.TenantReasonFound
	if hasMissingTiers {
		condStatus = metav1.ConditionFalse
		vmaasReason = v1alpha1.TenantReasonNotFound
	}
	tenant.SetStatusCondition(v1alpha1.TenantConditionClusterStorageReady, condStatus, vmaasReason, condMsg)
	tenant.Status.StorageClasses = mergeResolvedStorageClasses(cspResolved, scResult.resolved)
	r.updateTenantVMaaSStorage(tenant, clusterName, !hasMissingTiers, vmaasReason)

	if r.ClusterStorageProvider == nil {
		return ctrl.Result{}, nil
	}

	// Stage 3: provision cluster-side storage on CaaS clusters owned by this tenant.
	// Runs after VMaaS (Stage 2) because CaaS requires StorageBackendReady (Stage 1)
	// to have completed during tenant onboarding before cluster-side resources can
	// be installed.
	caasResult, caasErr := r.handleCaaSUpdate(ctx, tenant)
	if caasErr != nil || caasResult.RequeueAfter > 0 {
		return caasResult, caasErr
	}

	// Poll any non-terminal class provision job to update its status.
	// This runs after handleCaaSUpdate (Stage 3) so that CaaS cluster
	// lifecycle management is never blocked by VMaaS job polling.
	latestClassJob := provisioning.FindLatestJobByType(tenant.Status.ClusterStorageJobs, v1alpha1.JobTypeProvision)
	if latestClassJob != nil && !latestClassJob.State.IsTerminal() {
		return provisioning.PollJob(ctx, r.ClusterStorageProvider, tenant,
			&provisioning.State{Jobs: &tenant.Status.ClusterStorageJobs},
			latestClassJob, r.StatusPollInterval, nil)
	}

	// Stage 4: Retry provisioning for any storage tiers that failed to resolve.
	// This runs after handleCaaSUpdate to avoid blocking cluster lifecycle.
	if hasMissingTiers {
		return r.handleClusterStorageProvisioning(ctx, tenant, hubSecretReady)
	}

	return ctrl.Result{}, nil
}

// clearOSACManagedStorageStatus clears OSAC-managed storage backend status and
// storage conditions when a tenant uses exclusively CSP-managed storage tiers.
func clearOSACManagedStorageStatus(tenant *v1alpha1.Tenant) {
	tenant.Status.StorageBackends = nil
	apimeta.RemoveStatusCondition(&tenant.Status.Conditions, string(v1alpha1.TenantConditionStorageBackendReady))
	apimeta.RemoveStatusCondition(&tenant.Status.Conditions, string(v1alpha1.TenantConditionClusterStorageReady))
}

// canProvisionOSACStorage reports whether OSAC-managed AAP provisioning can proceed.
// When tier and backend gRPC clients are configured, at least one OSAC-managed
// tier must have resolved into ctx; otherwise AAP would be invoked with empty
// storage_tier_definitions and storage_backend_connections.
func (r *StorageReconciler) canProvisionOSACStorage(ctx context.Context) bool {
	if r.TiersClient == nil || r.BackendsClient == nil {
		return true
	}
	return len(provisioning.StorageTierDefinitionsFromContext(ctx)) > 0
}

// handleCaaSUpdate provisions cluster-side storage on CaaS clusters belonging
// to this tenant. For each Ready ClusterOrder without ClusterStorageReady=True,
// it retrieves the kubeconfig and triggers the AAP provisioning job.
func (r *StorageReconciler) handleCaaSUpdate(ctx context.Context, tenant *v1alpha1.Tenant) (ctrl.Result, error) {
	if !r.canProvisionOSACStorage(ctx) {
		return ctrl.Result{}, nil
	}
	log := ctrllog.FromContext(ctx)
	tenantName := tenant.GetName()

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

		r.updateTenantClusterStorage(tenant, co)
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

// updateTenantVMaaSStorage upserts the VMaaS target-cluster status while
// preserving status entries for other clusters.
func (r *StorageReconciler) updateTenantVMaaSStorage(tenant *v1alpha1.Tenant, clusterName string, ready bool, reason string) {
	r.upsertTenantClusterStorage(tenant, clusterName, ready, reason)
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

	r.upsertTenantClusterStorage(tenant, clusterName, ready, reason)
}

// upsertTenantClusterStorage updates the status entry for clusterName or appends
// one when that cluster is not yet represented.
func (r *StorageReconciler) upsertTenantClusterStorage(tenant *v1alpha1.Tenant, clusterName string, ready bool, reason string) {
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

// handleDelete tears down CaaS and VMaaS storage resources for a deleting Tenant
// and removes the storage finalizer once cleanup completes.
func (r *StorageReconciler) handleDelete(ctx context.Context, tenant *v1alpha1.Tenant) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("handling storage delete for Tenant", "name", tenant.Name)

	if !controllerutil.ContainsFinalizer(tenant, storageFinalizer) {
		return ctrl.Result{}, nil
	}

	// Resolved independently from handleUpdate's resolution (no caching between
	// create and delete paths), before the first AAP-triggering call in this
	// function, so every downstream call in this reconcile sees the same result.
	ctx, osacTiers, _, skippedOSACTiers, tierErr := resolveAndPartitionTierContext(ctx, r.TiersClient, r.BackendsClient, r.SecretsClient, "tenant", tenant.Name)
	if tierErr != nil {
		log.Error(tierErr, "tier resolution failed during deletion", "tenant", tenant.Name)
	}

	// CaaS cleanup: remove cluster-side storage (StorageClasses, CSI) from
	// all CaaS clusters and remove our finalizer from their ClusterOrders.
	// The ClusterOrders themselves are not being deleted (they have no
	// OwnerReference to the Tenant), but the storage backend is about to be
	// torn down so cluster-side resources must be removed first.
	//
	// This runs independently of ClusterStorageProvider: when the
	// HostedControlPlane is already gone the finalizer is removed without
	// triggering AAP deprovisioning (OSAC-4340).
	// handleCaaSDelete already guards against empty backend connections (OSAC-4855).
	caasResult, caasErr := r.handleCaaSDelete(ctx, tenant, true)
	if caasErr != nil || caasResult.RequeueAfter > 0 {
		return caasResult, caasErr
	}

	// Guard: when tier resolution failed, do not start or retry tenant
	// deprovisioning AAP jobs — they would run without backend connections
	// and fail. Retain the finalizer and requeue so the controller retries
	// when the fulfillment service recovers, rather than orphaning backend
	// resources. This differs from the handleCaaSDelete guard (OSAC-4855)
	// which removes the CaaS finalizer because the cluster being destroyed
	// makes resource cleanup moot; tenant-level resources (hub Secrets,
	// StorageClasses on the target cluster) outlive the Tenant CR and must
	// be cleaned up. When tier clients are nil (no fulfillment service
	// configured), tierErr is nil and deprovisioning proceeds normally.
	if tierErr != nil && (r.ClusterStorageProvider != nil || r.BackendProvider != nil) {
		log.Info("tier resolution failed during deletion, retaining finalizer and requeueing",
			"tenant", tenant.Name, "retryAfter", r.StatusPollInterval)
		r.Recorder.Eventf(tenant, nil, corev1.EventTypeWarning, "TierResolutionFailed", "Teardown",
			"Storage tier resolution failed during deletion, deferring deprovisioning: %v", tierErr)
		return ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
	}

	// Stage 1: class cleanup
	classDeprovJob := provisioning.FindLatestJobByType(tenant.Status.ClusterStorageJobs, v1alpha1.JobTypeDeprovision)
	classCleanupDone := classDeprovJob != nil && classDeprovJob.State.IsTerminal() && classDeprovJob.State.IsSuccessful()

	if !classCleanupDone {
		result, err := r.handleClusterStorageDeprovisioning(ctx, tenant, osacTiers, skippedOSACTiers)
		if err != nil {
			return result, err
		}
		if result.RequeueAfter > 0 {
			return result, nil
		}
		// Class cleanup just completed successfully — fall through to backend
	}

	// Stage 2: backend teardown
	result, err := r.handleBackendDeprovisioning(ctx, tenant)
	if err != nil {
		return result, err
	}
	if result.RequeueAfter > 0 {
		return result, nil
	}

	controllerutil.RemoveFinalizer(tenant, storageFinalizer)
	if err := r.Update(ctx, tenant); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("storage finalizer removed, deletion will proceed")
	return ctrl.Result{}, nil
}

// --- Stage 1: Backend provisioning ---

func (r *StorageReconciler) handleBackendProvisioning(ctx context.Context, tenant *v1alpha1.Tenant) (ctrl.Result, error) {
	if !r.canProvisionOSACStorage(ctx) {
		return ctrl.Result{}, nil
	}
	desiredVersion, _ := provisioning.ComputeDesiredConfigVersion(provisioning.StorageTierDefinitionsFromContext(ctx))
	latestJob := provisioning.FindLatestJobByType(tenant.Status.StorageBackendJobs, v1alpha1.JobTypeProvision)
	if latestJob != nil && latestJob.State == v1alpha1.JobStateFailed &&
		(latestJob.ConfigVersion == "" || latestJob.ConfigVersion == desiredVersion) {
		ctrllog.FromContext(ctx).Info("latest backend provision job failed, waiting for external trigger to retry",
			"message", latestJob.Message)
		return ctrl.Result{}, nil
	}

	return provisioning.RunProvisioningLifecycle(ctx, r.BackendProvider, tenant,
		&provisioning.State{
			Jobs:                 &tenant.Status.StorageBackendJobs,
			DesiredConfigVersion: desiredVersion,
		},
		r.MaxJobHistory, r.StatusPollInterval, nil,
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(ctx, r.Client,
				client.ObjectKeyFromObject(tenant), &v1alpha1.Tenant{},
				func(obj client.Object) []v1alpha1.JobStatus {
					return obj.(*v1alpha1.Tenant).Status.StorageBackendJobs
				})
		},
		func() error {
			return r.patchTenantStorageStatus(ctx, client.ObjectKeyFromObject(tenant), tenant.Status)
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
func (r *StorageReconciler) handleClusterStorageProvisioning(ctx context.Context, tenant *v1alpha1.Tenant, hubSecretReady bool) (ctrl.Result, error) {
	if !r.canProvisionOSACStorage(ctx) {
		return ctrl.Result{}, nil
	}
	desiredVersion, _ := provisioning.ComputeDesiredConfigVersion(provisioning.StorageTierDefinitionsFromContext(ctx))
	latestJob := provisioning.FindLatestJobByType(tenant.Status.ClusterStorageJobs, v1alpha1.JobTypeProvision)
	if latestJob != nil && latestJob.State == v1alpha1.JobStateFailed &&
		(latestJob.ConfigVersion == "" || latestJob.ConfigVersion == desiredVersion) {
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

	return provisioning.RunProvisioningLifecycle(ctx, r.ClusterStorageProvider, tenant,
		&provisioning.State{
			Jobs:                 &tenant.Status.ClusterStorageJobs,
			DesiredConfigVersion: desiredVersion,
		},
		r.MaxJobHistory, r.StatusPollInterval, nil,
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(ctx, r.Client,
				client.ObjectKeyFromObject(tenant), &v1alpha1.Tenant{},
				func(obj client.Object) []v1alpha1.JobStatus {
					return obj.(*v1alpha1.Tenant).Status.ClusterStorageJobs
				})
		},
		func() error {
			return r.patchTenantStorageStatus(ctx, client.ObjectKeyFromObject(tenant), tenant.Status)
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
func (r *StorageReconciler) handleCaaSDelete(ctx context.Context, tenant *v1alpha1.Tenant, tenantDeleting bool) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	tenantName := tenant.GetName()

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
		} else if r.ClusterStorageProvider == nil {
			// Cluster still exists but no storage provider is configured, so
			// we cannot run an AAP deprovisioning job. Remove the finalizer
			// without attempting cleanup (OSAC-4340).
			log.Info("no cluster storage provider configured, skipping CaaS cluster-side cleanup",
				"clusterOrder", co.Name, "tenant", tenantName)
		} else if len(provisioning.StorageBackendConnectionsFromContext(ctx)) == 0 {
			// Backend connections are unavailable (fulfillment service down,
			// tier resolution failed, or clients not configured). The AAP
			// deprovisioning job would receive no credentials and fail, and
			// BlockDeletionOnFailure would prevent finalizer removal,
			// leaving the ClusterOrder stuck in Deleting. Skip the job and
			// fall through to finalizer removal — the CaaS cluster is being
			// torn down anyway (OSAC-4855).
			log.Info("backend connections unavailable during CaaS teardown, skipping deprovisioning job",
				"clusterOrder", co.Name, "tenant", tenantName)
			r.Recorder.Eventf(co, nil, corev1.EventTypeWarning, "BackendConnectionsUnavailable", "Teardown",
				"Storage backend connections could not be resolved during teardown, skipping deprovisioning job")
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

		r.removeTenantClusterStorageEntry(tenant, co.Name)

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

func (r *StorageReconciler) handleClusterStorageDeprovisioning(
	ctx context.Context,
	tenant *v1alpha1.Tenant,
	osacTiers []provisioning.TierDefinition,
	skippedOSACTiers bool,
) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	if r.ClusterStorageProvider == nil {
		log.Info("no class provider configured, skipping cluster-side cleanup")
		return ctrl.Result{}, nil
	}

	skip, err := r.canSkipOSACDeprovisioning(ctx, tenant, osacTiers, skippedOSACTiers)
	if err != nil {
		log.Error(err, "failed to verify OSAC-managed storage state during deletion, retaining finalizer and requeueing",
			"tenant", tenant.Name, "retryAfter", r.StatusPollInterval)
		return ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
	}
	if skip {
		return ctrl.Result{}, nil
	}

	result, done, err := provisioning.RunDeprovisioningLifecycle(ctx, r.ClusterStorageProvider, tenant,
		&tenant.Status.ClusterStorageJobs, r.MaxJobHistory, r.StatusPollInterval)
	if err != nil || !done {
		return result, err
	}
	return ctrl.Result{}, nil
}

func (r *StorageReconciler) handleBackendDeprovisioning(ctx context.Context, tenant *v1alpha1.Tenant) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	if r.BackendProvider == nil {
		log.Info("no backend provider configured, skipping backend teardown")
		return ctrl.Result{}, nil
	}

	hubSecretReady, err := r.hubSecretExists(ctx, tenant.GetName(), "")
	if err != nil {
		log.Error(err, "failed to check hub Secret existence, requeueing")
		return ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
	}

	latestJob := provisioning.FindLatestJobByType(tenant.Status.StorageBackendJobs, v1alpha1.JobTypeDeprovision)
	deprovJobActive := latestJob != nil && latestJob.JobID != "" && !latestJob.State.IsTerminal()
	deprovJobBlocking := latestJob != nil &&
		latestJob.State.IsTerminal() &&
		!latestJob.State.IsSuccessful() &&
		latestJob.BlockDeletionOnFailure

	if !hubSecretReady && !deprovJobActive && !deprovJobBlocking {
		return ctrl.Result{}, nil
	}

	result, done, err := provisioning.RunDeprovisioningLifecycle(ctx, r.BackendProvider, tenant,
		&tenant.Status.StorageBackendJobs, r.MaxJobHistory, r.StatusPollInterval)
	if err != nil || !done {
		return result, err
	}
	return ctrl.Result{}, nil
}

// --- Helpers ---

// storageBackendStatuses reports readiness for each unique backend referenced by
// the resolved tier definitions. A TierDefinition represents the supported model
// of one backend association per tier; multi-backend tiers are intentionally not
// expanded here. When tier definitions are unavailable, the legacy any-Secret
// check is retained for environments without Tier API data.
func (r *StorageReconciler) storageBackendStatuses(ctx context.Context, tenantName string, tierDefinitions []provisioning.TierDefinition) ([]v1alpha1.StorageBackendStatus, bool, error) {
	backendProviders := make(map[string]string)
	for _, tier := range tierDefinitions {
		if tier.BackendID != "" && tier.Provider != "" {
			backendProviders[tier.BackendID] = tier.Provider
		}
	}

	providerSecrets, anySecret, err := r.hubSecretsByProvider(ctx, tenantName)
	if err != nil {
		return nil, false, err
	}
	if len(backendProviders) == 0 {
		return nil, anySecret, nil
	}

	backendIDs := make([]string, 0, len(backendProviders))
	for backendID := range backendProviders {
		backendIDs = append(backendIDs, backendID)
	}
	sort.Strings(backendIDs)

	statuses := make([]v1alpha1.StorageBackendStatus, 0, len(backendIDs))
	allReady := true
	for _, backendID := range backendIDs {
		provider := backendProviders[backendID]
		ready := providerSecrets[provider]

		message := fmt.Sprintf("Hub Secret for tenant %q exists", tenantName)
		if !ready {
			message = fmt.Sprintf("Hub Secret for tenant %q not found", tenantName)
			allReady = false
		}
		statuses = append(statuses, v1alpha1.StorageBackendStatus{
			Name:     backendID,
			Provider: provider,
			Ready:    ready,
			Message:  message,
		})
	}

	return statuses, allReady, nil
}

// formatStorageBackendReadinessMessage summarizes backend readiness, falling
// back to the legacy tenant-wide Secret result when backend details are absent.
func formatStorageBackendReadinessMessage(statuses []v1alpha1.StorageBackendStatus, legacyHubSecretReady bool) string {
	if len(statuses) == 0 {
		fallbackResult := "no Secret found"
		if legacyHubSecretReady {
			fallbackResult = "found a Secret"
		}
		return fmt.Sprintf("No storage backend entries are available to evaluate (ready: 0; not ready: 0); legacy tenant-wide Hub Secret check: %s", fallbackResult)
	}

	ready := make([]string, 0, len(statuses))
	notReady := make([]string, 0, len(statuses))
	for _, backend := range statuses {
		backendAndProvider := fmt.Sprintf("%s: %s", backend.Name, backend.Provider)
		if backend.Ready {
			ready = append(ready, backendAndProvider)
		} else {
			notReady = append(notReady, backendAndProvider)
		}
	}
	sort.Strings(ready)
	sort.Strings(notReady)

	readyNames := "none"
	if len(ready) > 0 {
		readyNames = strings.Join(ready, ", ")
	}
	notReadyNames := "none"
	if len(notReady) > 0 {
		notReadyNames = strings.Join(notReady, ", ")
	}

	message := fmt.Sprintf("Hub Secret readiness by backend: ready (%d): %s; not ready (%d): %s", len(ready), readyNames, len(notReady), notReadyNames)
	if len(ready) == 0 {
		message += "; no backend Hub Secrets are ready"
	}
	return message
}

// hubSecretsByProvider lists a tenant's Hub Secrets and reports readiness by
// provider together with whether any matching Secret exists.
func (r *StorageReconciler) hubSecretsByProvider(ctx context.Context, tenantName string) (map[string]bool, bool, error) {
	labels := map[string]string{osacTenantKey: tenantName}
	var secretList corev1.SecretList
	if err := r.List(ctx, &secretList,
		client.InNamespace(storageConfigNamespace()),
		client.MatchingLabels(labels),
	); err != nil {
		return nil, false, err
	}

	providers := make(map[string]bool)
	for _, secret := range secretList.Items {
		if provider := secret.Labels[osacStorageProviderLabel]; provider != "" {
			providers[provider] = true
		}
	}
	return providers, len(secretList.Items) > 0, nil
}

func (r *StorageReconciler) hubSecretExists(ctx context.Context, tenantName string, provider string) (bool, error) {
	labels := map[string]string{osacTenantKey: tenantName}
	if provider != "" {
		labels[osacStorageProviderLabel] = provider
	}
	var secretList corev1.SecretList
	if err := r.List(ctx, &secretList,
		client.InNamespace(storageConfigNamespace()),
		client.MatchingLabels(labels),
	); err != nil {
		return false, err
	}
	return len(secretList.Items) > 0, nil
}

// backendRegistered reports whether at least one non-cspManaged StorageBackend is
// registered in the fulfillment service (when Items are populated in the List
// response, backends with spec.csp_managed=true are excluded). When BackendsClient
// is nil (no gRPC connection configured), it returns (false, nil) — preserving the
// existing behavior for deployments that run without a fulfillment service
// connection. On transient gRPC failure the error is returned so the caller can
// requeue rather than silently falling back.
func (r *StorageReconciler) backendRegistered(ctx context.Context) (bool, error) {
	r.catalogMu.RLock()
	snap := r.cachedCatalog
	r.catalogMu.RUnlock()
	if snap != nil && snap.resolutionErr == nil {
		return snap.backendRegistered, nil
	}
	return r.fetchBackendRegistered(ctx)
}

func (r *StorageReconciler) fetchBackendRegistered(ctx context.Context) (bool, error) {
	if r.BackendsClient == nil {
		return false, nil
	}
	req := &privatev1.StorageBackendsListRequest{}
	req.SetLimit(tierListLimit)
	resp, err := r.BackendsClient.List(ctx, req)
	if err != nil {
		return false, fmt.Errorf("list storage backends: %w", err)
	}
	if items := resp.GetItems(); len(items) > 0 {
		for _, item := range items {
			if !item.GetSpec().GetCspManaged() {
				return true, nil
			}
		}
		return false, nil
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

// mapStorageClassToTenant maps StorageClass events to the affected Tenant(s),
// fanning out to all Tenants in r.tenantNamespace when the StorageClass
// carries the csp-managed tenant label.
func (r *StorageReconciler) mapStorageClassToTenant(ctx context.Context, obj client.Object) []reconcile.Request {
	log := ctrllog.FromContext(ctx)

	tenantName, exists := obj.GetLabels()[osacTenantKey]
	if !exists || tenantName == "" {
		return nil
	}

	if tenantName == cspManagedTenantValue {
		tenantList := &v1alpha1.TenantList{}
		if err := r.List(ctx, tenantList, client.InNamespace(r.tenantNamespace)); err != nil {
			log.Error(err, "unable to list Tenants for csp-managed StorageClass",
				"storageClass", obj.GetName(), "namespace", r.tenantNamespace)
			return nil
		}
		requests := make([]reconcile.Request, 0, len(tenantList.Items))
		for i := range tenantList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(&tenantList.Items[i]),
			})
		}
		return requests
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

// mapSecretToTenant maps storage hub Secret events to the owning Tenant.
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

// handleCSPStorageOnboarding evaluates CSP-managed StorageClass onboarding for
// the given tenant, updates TenantConditionStorageReady, and
// reports whether the tenant is pure-CSP (no active or retained OSAC storage).
func (r *StorageReconciler) handleCSPStorageOnboarding(
	ctx context.Context,
	tenant *v1alpha1.Tenant,
	tenantName string,
	osacTiers []provisioning.TierDefinition,
	cspTiers []provisioning.TierDefinition,
	skippedOSACTiers bool,
) ([]v1alpha1.ResolvedStorageClass, bool, error) {
	if len(cspTiers) == 0 {
		apimeta.RemoveStatusCondition(&tenant.Status.Conditions, string(v1alpha1.TenantConditionStorageReady))
		return nil, false, nil
	}
	targetClient, err := getTargetClient(ctx, r.mgr, r.targetCluster)
	if err != nil {
		return nil, false, err
	}
	cspResult, err := getCSPStorageClasses(ctx, targetClient, cspTiers)
	if err != nil {
		return nil, false, err
	}

	for _, msg := range cspResult.duplicateMessages {
		r.Recorder.Eventf(tenant, nil, corev1.EventTypeWarning, eventReasonDuplicateStorageClass, eventActionDetectDuplicate, "%s", msg)
	}

	missing := missingTierNames(cspTiers, cspResult.resolved, cspResult.ambiguousTiers)

	condMsg := cspResult.conditionMessage()
	if condMsg == "" {
		condMsg = fmt.Sprintf("no csp-managed StorageClass found for tenant %q", tenantName)
	}
	condMsg = r.appendMissingTierWarnings(tenant, cspTiers, cspResult.resolved, cspResult.ambiguousTiers, condMsg)

	if len(missing) == 0 && len(cspResult.duplicateMessages) == 0 {
		tenant.SetStatusCondition(v1alpha1.TenantConditionStorageReady,
			metav1.ConditionTrue,
			v1alpha1.TenantReasonFound,
			condMsg)
	} else if existing := tenant.GetStatusCondition(v1alpha1.TenantConditionStorageReady); existing == nil || existing.Status != metav1.ConditionTrue {
		reason := v1alpha1.TenantReasonNotFound
		if len(missing) == 0 {
			reason = v1alpha1.TenantReasonMultipleFound
		}
		tenant.SetStatusCondition(v1alpha1.TenantConditionStorageReady,
			metav1.ConditionFalse,
			reason,
			condMsg)
	}

	retainedOSAC := retainOSACStorageClasses(tenant.Status.StorageClasses, cspTiers, osacTiers, skippedOSACTiers)
	pureCSP, err := r.isPureCSPTenant(ctx, targetClient, tenant, osacTiers, retainedOSAC, skippedOSACTiers)
	if err != nil {
		return nil, false, err
	}
	if pureCSP {
		tenant.Status.StorageClasses = mergeResolvedStorageClasses(cspResult.resolved, nil)
		clearOSACManagedStorageStatus(tenant)
		r.removeTenantClusterStorageEntry(tenant, string(r.targetCluster))
		return cspResult.resolved, true, nil
	}
	tenant.Status.StorageClasses = mergeResolvedStorageClasses(cspResult.resolved, retainedOSAC)
	return cspResult.resolved, false, nil
}

// isPureCSPTenant reports whether a Tenant with configured CSP-managed tiers has
// no active or retained OSAC-managed storage state and should take the pure-CSP
// reconciliation path.
func (r *StorageReconciler) isPureCSPTenant(
	ctx context.Context,
	targetClient client.Client,
	tenant *v1alpha1.Tenant,
	osacTiers []provisioning.TierDefinition,
	retainedOSAC []v1alpha1.ResolvedStorageClass,
	skippedOSACTiers bool,
) (bool, error) {
	if len(osacTiers) > 0 {
		return false, nil
	}
	if !skippedOSACTiers {
		return true, nil
	}
	if len(retainedOSAC) > 0 ||
		len(tenant.Status.StorageBackendJobs) > 0 ||
		len(tenant.Status.ClusterStorageJobs) > 0 {
		return false, nil
	}
	osacBackendRegistered, err := r.backendRegistered(ctx)
	if err != nil {
		return false, err
	}
	if osacBackendRegistered {
		return false, nil
	}
	hubSecretReady, err := r.hubSecretExists(ctx, tenant.GetName(), "")
	if err != nil {
		return false, err
	}
	if hubSecretReady {
		return false, nil
	}
	scResult, err := getTenantStorageClasses(ctx, targetClient, tenant.GetName())
	if err != nil {
		return false, err
	}
	return len(scResult.resolved) == 0 && len(scResult.duplicateMessages) == 0, nil
}

// canSkipOSACDeprovisioning reports whether Tenant deletion can safely bypass
// OSAC-managed cluster storage and backend deprovisioning. It returns true only
// when tier/backend gRPC clients are configured, no OSAC-managed tiers resolved,
// no tiers were skipped during resolution (e.g., due to a missing backend
// password Secret), no non-CSP StorageBackend is registered in the fulfillment
// service, no tenant hub Secret exists, and no OSAC storage status or job
// history exists on the Tenant.
func (r *StorageReconciler) canSkipOSACDeprovisioning(
	ctx context.Context,
	tenant *v1alpha1.Tenant,
	osacTiers []provisioning.TierDefinition,
	skippedOSACTiers bool,
) (bool, error) {
	if r.TiersClient == nil || r.BackendsClient == nil || len(osacTiers) > 0 || skippedOSACTiers {
		return false, nil
	}
	if len(tenant.Status.StorageBackends) > 0 ||
		len(tenant.Status.ClusterStorage) > 0 ||
		len(tenant.Status.StorageBackendJobs) > 0 ||
		len(tenant.Status.ClusterStorageJobs) > 0 {
		return false, nil
	}
	osacBackendRegistered, err := r.fetchBackendRegistered(ctx)
	if err != nil {
		return false, err
	}
	if osacBackendRegistered {
		return false, nil
	}
	hubSecretReady, err := r.hubSecretExists(ctx, tenant.GetName(), "")
	if err != nil {
		return false, err
	}
	return !hubSecretReady, nil
}

// mergeResolvedStorageClasses merges CSP-managed and OSAC-managed resolved
// StorageClasses, deduplicating by Tier (with cspResolved taking precedence)
// and returning nil when empty or a slice sorted ascending by Tier.
func mergeResolvedStorageClasses(cspResolved, osacResolved []v1alpha1.ResolvedStorageClass) []v1alpha1.ResolvedStorageClass {
	if len(cspResolved) == 0 && len(osacResolved) == 0 {
		return nil
	}
	byTier := make(map[string]v1alpha1.ResolvedStorageClass, len(cspResolved)+len(osacResolved))
	for _, sc := range osacResolved {
		byTier[sc.Tier] = sc
	}
	for _, sc := range cspResolved {
		byTier[sc.Tier] = sc
	}
	merged := make([]v1alpha1.ResolvedStorageClass, 0, len(byTier))
	for _, sc := range byTier {
		merged = append(merged, sc)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Tier < merged[j].Tier
	})
	return merged
}

// retainOSACStorageClasses returns existing resolved StorageClasses whose Tier
// matches one of the configured OSAC-managed tiers (or any non-CSP tier when
// an OSAC tier was skipped during resolution) so Stage 1 early stops or
// transient errors before Stage 2 do not clobber previously resolved OSAC
// StorageClasses on a comingled Tenant.
func retainOSACStorageClasses(
	existing []v1alpha1.ResolvedStorageClass,
	cspTiers []provisioning.TierDefinition,
	osacTiers []provisioning.TierDefinition,
	skippedOSACTiers bool,
) []v1alpha1.ResolvedStorageClass {
	if len(existing) == 0 || (!skippedOSACTiers && len(osacTiers) == 0) {
		return nil
	}
	cspSet := tierNameSet(cspTiers)
	osacSet := tierNameSet(osacTiers)
	var retained []v1alpha1.ResolvedStorageClass
	for _, sc := range existing {
		tier := strings.ToLower(sc.Tier)
		_, isCSP := cspSet[tier]
		_, isOSAC := osacSet[tier]
		if (skippedOSACTiers && !isCSP) || (!skippedOSACTiers && isOSAC) {
			retained = append(retained, sc)
		}
	}
	return retained
}

// mapCatalogEventToTenants fans out a storage catalog change notification to
// all Tenants in r.tenantNamespace.
func (r *StorageReconciler) mapCatalogEventToTenants(ctx context.Context, _ *v1alpha1.Tenant) []mcreconcile.Request {
	tenantList := &v1alpha1.TenantList{}
	if err := r.List(ctx, tenantList, client.InNamespace(r.tenantNamespace)); err != nil {
		ctrllog.FromContext(ctx).Error(err, "unable to list Tenants for storage catalog sync",
			"namespace", r.tenantNamespace)
		return nil
	}
	requests := make([]mcreconcile.Request, 0, len(tenantList.Items))
	for i := range tenantList.Items {
		requests = append(requests, mcreconcile.Request{
			Request: reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(&tenantList.Items[i]),
			},
		})
	}
	return requests
}

// SetupWithManager registers StorageReconciler, its Tenant, ClusterOrder,
// StorageClass, Secret, and catalog-change watches, and the periodic storage
// catalog sync runnable with the multicluster manager.
func (r *StorageReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if r.catalogEvents == nil {
		r.catalogEvents = make(chan event.TypedGenericEvent[*v1alpha1.Tenant], 1)
	}
	if err := mcbuilder.ControllerManagedBy(mgr).
		For(&v1alpha1.Tenant{},
			mcbuilder.WithPredicates(tenantNamespacePredicate(r.tenantNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Named("storage").
		Watches(
			&v1alpha1.ClusterOrder{},
			mchandler.EnqueueRequestsFromMapFunc(r.mapClusterOrderToTenant),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false),
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
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false),
		).
		WatchesRawSource(
			crsource.TypedChannel(
				r.catalogEvents,
				handler.TypedEnqueueRequestsFromMapFunc(r.mapCatalogEventToTenants),
			),
		).
		Complete(r); err != nil {
		return err
	}

	if localMgr := mgr.GetLocalManager(); localMgr != nil {
		if err := localMgr.Add(NewStorageCatalogSyncRunnable(r, r.StatusPollInterval)); err != nil {
			return fmt.Errorf("storage catalog sync runnable: %w", err)
		}
	}
	return nil
}

// secretTenantPredicate filters Secret watch events to storage config Secrets
// in the configured storage config namespace that carry the osac tenant label.
func secretTenantPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		if obj.GetNamespace() != storageConfigNamespace() {
			return false
		}
		_, exists := obj.GetLabels()[osacTenantKey]
		return exists
	})
}
