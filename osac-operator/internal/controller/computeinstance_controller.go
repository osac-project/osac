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
	"net/netip"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
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
	"github.com/osac-project/osac/osac-operator/internal/networktask"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const (
	// defaultPreconditionRequeueInterval is the requeue delay when a precondition for
	// reconciliation is not yet met (e.g. parent resource not found, configuration
	// not populated, or dependent resource not in a ready state)
	defaultPreconditionRequeueInterval = 10 * time.Second

	// computeInstanceProvisionTask and computeInstanceDeprovisionTask are the
	// status.jobs tasks of the ocp_virt_vm jobs (VMaaS design; manager none).
	computeInstanceProvisionTask   = "provision"
	computeInstanceDeprovisionTask = "deprovision"

	// computeInstanceIDVMLabel is the label ocp_virt_vm sets on the VM template,
	// DataVolumes and Secrets (VMaaS design, VM construction). The Kubernetes
	// Manager selects the VM pod by it.
	computeInstanceIDVMLabel = "osac.openshift.io/compute-instance-id"

	// vmNetworkTenant is the VM's pod network and interface name with provider
	// networking enabled; vmNetworkDefault the fallback with it disabled.
	vmNetworkTenant  = "tenant"
	vmNetworkDefault = "default"

	// ipAddressNotFoundDelay is how long a Running VMI may report no address
	// before the IPAddressNotFound Warning event.
	ipAddressNotFoundDelay = 10 * time.Minute

	eventReasonNetworkAttached         = "NetworkAttached"
	eventReasonNetworkAttachmentFailed = "NetworkAttachmentFailed"
	eventReasonIPAddressNotFound       = "IPAddressNotFound"
	eventReasonIPAddressChanged        = "IPAddressChanged"
)

// dataVolumeListGVK is the CDI DataVolume list kind, read as unstructured so
// the operator needs no CDI API dependency.
var dataVolumeListGVK = schema.GroupVersionKind{Group: "cdi.kubevirt.io", Version: "v1beta1", Kind: "DataVolumeList"}

// ComputeInstanceReconciler reconciles a ComputeInstance object (VMaaS
// networking design): it creates the VM's NetworkAttachment, runs the
// ocp_virt_vm provision and deprovision jobs through the network task engine
// (status.jobs, manager none), discovers the VM address and reports NetworkReady.
type ComputeInstanceReconciler struct {
	client.Client
	// APIReader reads from the API server where a stale cache must not be used.
	APIReader                client.Reader
	Scheme                   *runtime.Scheme
	Recorder                 events.EventRecorder
	mgr                      mcmanager.Manager
	ComputeInstanceNamespace string
	TenantNamespace          string
	NetworkingNamespace      string
	// Engine launches and tracks the provision and deprovision jobs. Its
	// ProvisioningEnabled is global.networking.provisioningEnabled.
	Engine *networktask.Engine
	// ProvisionTemplate and DeprovisionTemplate are the ocp_virt_vm job templates.
	ProvisionTemplate   string
	DeprovisionTemplate string
	// StatusPollInterval is how often waiting steps are checked again.
	StatusPollInterval time.Duration
	targetCluster      mc.ClusterName
	// TiersClient and BackendsClient query the fulfillment service Tier API for
	// JIT storage provisioning extra_vars. When nil, tier/backend context
	// injection is skipped — backward compatible with environments without a
	// fulfillment service connection. See resolveAndInjectTierContext.
	TiersClient    StorageTiersLister
	BackendsClient StorageBackendsClient
	// SecretsClient resolves a storage backend's password from a referenced Secret
	// when the backend supplies password_secret instead of an inline password. When
	// nil, backends that rely on password_secret are skipped. See resolveAndInjectTierContext.
	SecretsClient SecretsClient

	now func() time.Time

	// ipNotFoundEvents records the VMIs (by UID) that already got the
	// IPAddressNotFound event, so it is emitted once per VMI.
	ipNotFoundMu     sync.Mutex
	ipNotFoundEvents map[types.UID]bool
}

func NewComputeInstanceReconciler(
	mgr mcmanager.Manager,
	computeInstanceNamespace string,
	tenantNamespace string,
	networkingNamespace string,
	engine *networktask.Engine,
	provisionTemplate string,
	deprovisionTemplate string,
	statusPollInterval time.Duration,
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

	return &ComputeInstanceReconciler{
		Client:                   mgr.GetLocalManager().GetClient(),
		APIReader:                mgr.GetLocalManager().GetAPIReader(),
		Scheme:                   mgr.GetLocalManager().GetScheme(),
		Recorder:                 mgr.GetLocalManager().GetEventRecorder(computeInstanceControllerName),
		mgr:                      mgr,
		ComputeInstanceNamespace: computeInstanceNamespace,
		TenantNamespace:          tenantNamespace,
		NetworkingNamespace:      networkingNamespace,
		Engine:                   engine,
		ProvisionTemplate:        provisionTemplate,
		DeprovisionTemplate:      deprovisionTemplate,
		StatusPollInterval:       statusPollInterval,
		targetCluster:            targetCluster,
	}
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=computeinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=computeinstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=computeinstances/finalizers,verbs=update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=tenants,verbs=get;list;watch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=networkattachments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=externalipattachments;externalips;subnets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets;pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachines;virtualmachineinstances,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=cdi.kubevirt.io,resources=datavolumes,verbs=get;list;watch
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
	if err != nil {
		// A failed step (including a status write conflict of the task
		// engine) leaves the in-memory object stale: write nothing.
		return res, err
	}

	if !equality.Semantic.DeepEqual(instance.Status, *oldstatus) {
		log.Info("status requires update")
		// The update is conditional on the resourceVersion read (or written)
		// in this reconcile, so a stale object never overwrites status.jobs.
		if err := r.Status().Update(ctx, instance); err != nil {
			return res, err
		}
	}

	log.Info("end reconcile")
	return res, nil
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
		Watches(
			&v1alpha1.NetworkAttachment{},
			mchandler.EnqueueRequestsFromMapFunc(r.mapNetworkAttachmentToComputeInstance),
			mcbuilder.WithPredicates(ComputeInstanceNamespacePredicate(r.NetworkingNamespace)),
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

// mapNetworkAttachmentToComputeInstance maps a NetworkAttachment event to the
// ComputeInstance it targets (by the API ID label).
func (r *ComputeInstanceReconciler) mapNetworkAttachmentToComputeInstance(ctx context.Context, obj client.Object) []reconcile.Request {
	na, ok := obj.(*v1alpha1.NetworkAttachment)
	if !ok || na.Spec.Target.Kind != v1alpha1.NetworkAttachmentTargetComputeInstance || na.Spec.Target.ID == "" {
		return nil
	}
	list := &v1alpha1.ComputeInstanceList{}
	if err := r.List(ctx, list, client.InNamespace(r.ComputeInstanceNamespace),
		client.MatchingLabels{osacComputeInstanceIDLabel: na.Spec.Target.ID}); err != nil {
		ctrllog.FromContext(ctx).Error(err, "failed to list compute instances for a NetworkAttachment", "networkattachment", na.Name)
		return nil
	}
	requests := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
	}
	return requests
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

// provisioningEnabled reports global.networking.provisioningEnabled.
func (r *ComputeInstanceReconciler) provisioningEnabled() bool {
	return r.Engine.ProvisioningEnabled()
}

func (r *ComputeInstanceReconciler) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// vmNetwork is the ocp_virt_vm networking input (VMaaS design, VM
// construction): the namespace, the pod network and its interface, and the
// API ID label. With provider networking disabled the VM uses the cluster
// default pod network with masquerade.
func vmNetwork(namespace, id string, enabled bool) map[string]any {
	name := vmNetworkTenant
	iface := map[string]any{fieldName: name, "binding": map[string]any{fieldName: "l2bridge"}}
	if !enabled {
		name = vmNetworkDefault
		iface = map[string]any{fieldName: name, "masquerade": map[string]any{}}
	}
	return map[string]any{
		"namespace":  namespace,
		"networks":   []any{map[string]any{fieldName: name, "pod": map[string]any{}}},
		"interfaces": []any{iface},
		"labels":     map[string]any{computeInstanceIDVMLabel: id},
	}
}

func (r *ComputeInstanceReconciler) handleUpdate(ctx context.Context, _ reconcile.Request, instance *v1alpha1.ComputeInstance) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	if controllerutil.AddFinalizer(instance, osacComputeInstanceFinalizer) {
		// r.Update returns the server object: the in-memory status is the stored
		// one again, so status changes are made only after this call.
		if err := r.Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
	}

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

	// The VM's namespace: the accepted vm_attachment of its NetworkAttachment,
	// or the Tenant namespace with provider networking disabled.
	var na *v1alpha1.NetworkAttachment
	vmNamespace := ""
	if r.provisioningEnabled() {
		var wait bool
		na, wait, err = r.reconcileNetworkAttachment(ctx, instance)
		if err != nil || wait {
			return ctrl.Result{RequeueAfter: r.StatusPollInterval}, err
		}
		vmNamespace = na.Status.VMAttachment.Namespace
	} else {
		vmNamespace = tenant.Status.Namespace
		if vmNamespace == "" {
			vmNamespace = tenant.GetName()
		}
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionNetworkReady, metav1.ConditionTrue,
			networktask.MessageNetworkingDisabled, v1alpha1.ReasonProvisioningDisabled)
	}

	if err := r.handleDesiredConfigVersion(ctx, instance); err != nil {
		return ctrl.Result{}, err
	}

	ctx, err = r.injectStorageContext(ctx, instance, tenant, targetClient)
	if err != nil {
		return ctrl.Result{}, err
	}

	provision, err := r.handleProvisioning(ctx, instance, vmNamespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	result := ctrl.Result{RequeueAfter: provision.RequeueAfter}

	var kv *kubevirtv1.VirtualMachine
	if instance.Status.VirtualMachineReference != nil {
		kv, err = r.findKubeVirtVMs(ctx, targetClient, instance, instance.Status.VirtualMachineReference.Namespace)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	if kv != nil {
		r.handleKubeVirtVM(ctx, instance, kv)
		setComputeInstancePhase(&instance.Status, determinePhaseFromPrintableStatus(ctx, kv, instance.Status.Phase))
	} else {
		// No KubeVirt VM exists yet: infrastructure is being provisioned.
		setComputeInstancePhase(&instance.Status, v1alpha1.ComputeInstancePhaseStarting)
		if !provision.Failed() {
			instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionProvisioned, metav1.ConditionFalse, "VirtualMachine not yet created, waiting for provisioning", v1alpha1.ReasonWaitingForVM)
		}
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionRestartRequired, metav1.ConditionFalse, "", v1alpha1.ReasonAsExpected)
	}
	if provision.Failed() {
		// A failed provision job is final until the spec changes: a new spec runs the job again
		// as the next attempt (see handleProvisioning); the same spec stays failed (VMaaS design,
		// Create step 5).
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionProvisioned, metav1.ConditionFalse,
			provision.Entry.Message, v1alpha1.ReasonProvisioningFailed)
		if kv == nil {
			setComputeInstancePhase(&instance.Status, v1alpha1.ComputeInstancePhaseFailed)
		}
	}

	requeue, err := r.reconcileNetworkStatus(ctx, instance, na, kv)
	if err != nil {
		return ctrl.Result{}, err
	}
	result = mergeReconcileResult(result, requeue)
	r.reconcileReadyCondition(instance, kv)

	r.reconcileConfigAndRestart(ctx, instance)

	if na != nil && networkAttachmentFailed(na) {
		setComputeInstancePhase(&instance.Status, v1alpha1.ComputeInstancePhaseFailed)
	}

	recordRunningConfig(instance)
	return result, nil
}

func (r *ComputeInstanceReconciler) injectStorageContext(
	ctx context.Context,
	instance *v1alpha1.ComputeInstance,
	tenant *v1alpha1.Tenant,
	targetClient client.Client,
) (context.Context, error) {
	log := ctrllog.FromContext(ctx)
	if len(tenant.Status.StorageClasses) > 0 {
		ctx = provisioning.WithTenantStorageClasses(ctx, tenant.Status.StorageClasses)
	} else if tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady) == nil {
		tenantName := tenant.GetName()
		resolution, err := getTenantStorageClasses(ctx, targetClient, tenantName)
		if err != nil {
			return ctx, fmt.Errorf("failed to resolve tenant storage classes from target cluster for tenant %s: %w", tenantName, err)
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

	ctx, _, tierErr := resolveAndInjectTierContext(ctx, r.TiersClient, r.BackendsClient, r.SecretsClient, "computeInstance", instance.GetName())
	if tierErr != nil {
		log.Error(tierErr, "tier resolution failed, proceeding with VM provisioning — tier data is supplementary for JIT storage",
			"computeInstance", instance.GetName())
	}
	return ctx, nil
}

func (r *ComputeInstanceReconciler) reconcileConfigAndRestart(ctx context.Context, instance *v1alpha1.ComputeInstance) {
	log := ctrllog.FromContext(ctx)
	if computeInstanceConfigApplied(instance) {
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionConfigurationApplied, metav1.ConditionTrue, "", v1alpha1.ReasonAsExpected)
		if instance.Spec.RestartRequestedAt != nil {
			if instance.Status.LastRestartedAt == nil || instance.Spec.RestartRequestedAt.After(instance.Status.LastRestartedAt.Time) {
				log.Info("restart completed via provisioning", "restartRequestedAt", instance.Spec.RestartRequestedAt)
				instance.Status.LastRestartedAt = instance.Spec.RestartRequestedAt.DeepCopy()
				instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionRestartInProgress, metav1.ConditionFalse, "", v1alpha1.ReasonAsExpected)
			}
		}
	} else {
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionConfigurationApplied, metav1.ConditionFalse, "Applying configuration", v1alpha1.ReasonAsExpected)
		if instance.Spec.RestartRequestedAt != nil {
			if instance.Status.LastRestartedAt == nil || instance.Spec.RestartRequestedAt.After(instance.Status.LastRestartedAt.Time) {
				instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionRestartInProgress, metav1.ConditionTrue,
					fmt.Sprintf("Restart initiated at %s", instance.Spec.RestartRequestedAt.UTC().Format(time.RFC3339)),
					"RestartInProgress")
			}
		}
	}
}

// networkAttachmentFailed reports whether a NetworkAttachment is Ready=False, reason Failed.
func networkAttachmentFailed(na *v1alpha1.NetworkAttachment) bool {
	ready := meta.FindStatusCondition(na.Status.Conditions, v1alpha1.NetworkConditionReady)
	return ready != nil && ready.Status == metav1.ConditionFalse && ready.Reason == v1alpha1.ReasonFailed
}

// networkAttachmentReadyMessage returns the message of the Ready condition of a NetworkAttachment.
func networkAttachmentReadyMessage(na *v1alpha1.NetworkAttachment) string {
	if ready := meta.FindStatusCondition(na.Status.Conditions, v1alpha1.NetworkConditionReady); ready != nil {
		return ready.Message
	}
	return ""
}

// reconcileNetworkAttachment creates the VM's NetworkAttachment when missing
// and sets NetworkReady while it is not usable yet (VMaaS design, Create
// steps 3 and 4). It returns the NetworkAttachment and whether the VM must
// still wait (no accepted vm_attachment, or a failed attachment).
func (r *ComputeInstanceReconciler) setNetworkReady(instance *v1alpha1.ComputeInstance, status metav1.ConditionStatus, reason, message string) {
	instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionNetworkReady, status, message, reason)
}

func (r *ComputeInstanceReconciler) reconcileNetworkAttachment(ctx context.Context, instance *v1alpha1.ComputeInstance) (*v1alpha1.NetworkAttachment, bool, error) {
	id := instance.Labels[osacComputeInstanceIDLabel]
	if id == "" {
		return nil, true, fmt.Errorf("compute instance %s has no %s label", instance.Name, osacComputeInstanceIDLabel)
	}
	if len(instance.Spec.NetworkAttachments) == 0 || len(instance.Spec.NetworkAttachments[0].SecurityGroupRefs) == 0 {
		r.markNetworkAttachmentFailed(instance, "the ComputeInstance has no network attachment with a Subnet and SecurityGroups")
		setComputeInstancePhase(&instance.Status, v1alpha1.ComputeInstancePhaseFailed)
		return nil, true, nil
	}

	name := v1alpha1.NetworkAttachmentName(v1alpha1.NetworkAttachmentTargetComputeInstance, id)
	na := &v1alpha1.NetworkAttachment{}
	err := r.Get(ctx, client.ObjectKey{Namespace: r.NetworkingNamespace, Name: name}, na)
	if apierrors.IsNotFound(err) {
		attachment := instance.Spec.NetworkAttachments[0]
		na = &v1alpha1.NetworkAttachment{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   r.NetworkingNamespace,
				Finalizers:  []string{osacComputeInstanceFinalizer},
				Annotations: map[string]string{osacTenantKey: instance.Annotations[osacTenantKey]},
			},
			Spec: v1alpha1.NetworkAttachmentSpec{
				Target:         v1alpha1.NetworkAttachmentTarget{Kind: v1alpha1.NetworkAttachmentTargetComputeInstance, ID: id},
				Subnet:         attachment.SubnetRef,
				Interface:      v1alpha1.NetworkAttachmentInterface{Type: v1alpha1.NetworkInterfaceOverlay},
				SecurityGroups: attachment.SecurityGroupRefs,
			},
		}
		// Create only if missing; AlreadyExists counts as created (VMaaS edge
		// case "Restart after the NetworkAttachment create").
		if err := r.Create(ctx, na); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, true, fmt.Errorf("create NetworkAttachment %s: %w", name, err)
		}
		r.setNetworkReady(instance, metav1.ConditionFalse, v1alpha1.ComputeInstanceReasonNetworkAttachmentProgressing,
			"waiting for the network attachment")
		return nil, true, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("get NetworkAttachment %s: %w", name, err)
	}

	if networkAttachmentFailed(na) {
		r.markNetworkAttachmentFailed(instance, networkAttachmentReadyMessage(na))
		// After a Kubernetes apply failure no VM is created; after a Fabric
		// apply failure the VM stays and its status is still reported.
		return na, na.Status.VMAttachment == nil, nil
	}
	if na.Status.VMAttachment == nil || na.Status.VMAttachment.Namespace == "" {
		message := networkAttachmentReadyMessage(na)
		if message == "" {
			message = "waiting for the network attachment"
		}
		r.setNetworkReady(instance, metav1.ConditionFalse, v1alpha1.ComputeInstanceReasonNetworkAttachmentProgressing, message)
		return na, true, nil
	}
	return na, false, nil
}

// markNetworkAttachmentFailed sets NetworkReady=False NetworkAttachmentFailed,
// phase Failed, and emits the event once.
func (r *ComputeInstanceReconciler) markNetworkAttachmentFailed(instance *v1alpha1.ComputeInstance, message string) {
	if conditionReason(instance, v1alpha1.ComputeInstanceConditionNetworkReady) != v1alpha1.ComputeInstanceReasonNetworkAttachmentFailed {
		r.Recorder.Eventf(instance, nil, corev1.EventTypeWarning, eventReasonNetworkAttachmentFailed, eventActionReconcile, "%s", message)
	}
	r.setNetworkReady(instance, metav1.ConditionFalse, v1alpha1.ComputeInstanceReasonNetworkAttachmentFailed, message)
	setComputeInstancePhase(&instance.Status, v1alpha1.ComputeInstancePhaseFailed)
}

// handleProvisioning runs the ocp_virt_vm provision job (VMaaS design, Create
// step 5): one status.jobs entry, task provision, manager none. The first
// launch records status.virtualMachineReference in the same status write. A
// spec change after a provision that ended, Succeeded or Failed (a restart, a
// run strategy or an instance type change), runs the job again as a new
// attempt (decision 23). BeforeLaunch records the version the attempt runs
// for, so a rerun that fails again stops until the spec changes once more; the
// same spec after a failure stays final (design decision D4).
func (r *ComputeInstanceReconciler) handleProvisioning(ctx context.Context, instance *v1alpha1.ComputeInstance, vmNamespace string) (networktask.Outcome, error) {
	log := ctrllog.FromContext(ctx)

	// Check for ManagementStateManual annotation
	if val, exists := instance.Annotations[osacComputeInstanceManagementStateAnnotation]; exists && val == ManagementStateManual {
		log.Info("skipping provisioning due to management-state annotation", "management-state", val)
		return networktask.Outcome{}, nil
	}

	entry := networktask.FindJob(instance.Status.Jobs, computeInstanceProvisionTask, v1alpha1.NetworkJobManagerNone)
	if entry != nil && (entry.State == v1alpha1.NetworkJobStateSucceeded || entry.State == v1alpha1.NetworkJobStateFailed) &&
		instance.Status.ProvisionConfigVersion != instance.Status.DesiredConfigVersion {
		log.Info("configuration changed after the provision job ended; running it again",
			"state", entry.State,
			"provisionConfigVersion", instance.Status.ProvisionConfigVersion,
			"desiredConfigVersion", instance.Status.DesiredConfigVersion)
		r.Engine.MarkForRerun(instance, computeInstanceProvisionTask, v1alpha1.NetworkJobManagerNone)
	}

	vars, err := provisioning.JobVars(ctx, instance)
	if err != nil {
		return networktask.Outcome{}, fmt.Errorf("build the provision job input: %w", err)
	}
	vars["vm_network"] = vmNetwork(vmNamespace, instance.Labels[osacComputeInstanceIDLabel], r.provisioningEnabled())
	desired := instance.Status.DesiredConfigVersion
	return r.Engine.Run(ctx, instance, networktask.Request{
		Task:     computeInstanceProvisionTask,
		Manager:  v1alpha1.NetworkJobManagerNone,
		Kind:     kindComputeInstance,
		Template: r.ProvisionTemplate,
		Vars:     vars,
		BeforeLaunch: func() error {
			instance.Status.VirtualMachineReference = &v1alpha1.VirtualMachineReferenceType{
				Namespace:                  vmNamespace,
				KubeVirtVirtualMachineName: instance.Name,
			}
			instance.Status.ProvisionConfigVersion = desired
			return nil
		},
	})
}

// computeInstanceConfigApplied reports whether the provision job succeeded
// with the current desiredConfigVersion.
func computeInstanceConfigApplied(instance *v1alpha1.ComputeInstance) bool {
	entry := networktask.FindJob(instance.Status.Jobs, computeInstanceProvisionTask, v1alpha1.NetworkJobManagerNone)
	return entry != nil && entry.State == v1alpha1.NetworkJobStateSucceeded &&
		instance.Status.ProvisionConfigVersion == instance.Status.DesiredConfigVersion
}

// reconcileNetworkStatus discovers the VM address and sets NetworkReady
// (VMaaS design, Network status and address discovery).
func (r *ComputeInstanceReconciler) reconcileNetworkStatus(ctx context.Context, instance *v1alpha1.ComputeInstance,
	na *v1alpha1.NetworkAttachment, kv *kubevirtv1.VirtualMachine) (ctrl.Result, error) {
	enabled := r.provisioningEnabled()
	failed := na != nil && networkAttachmentFailed(na)
	oldAddress := instance.Status.IPAddress

	vmi, err := r.runningVMI(ctx, instance, kv)
	if err != nil {
		return ctrl.Result{}, err
	}
	if vmi == nil {
		instance.Status.ComputeNetworkAttachmentStatuses = nil
		instance.SetIPAddress("")
		if enabled && !failed {
			r.setNetworkReady(instance, metav1.ConditionFalse, v1alpha1.ComputeInstanceReasonVMNotRunning, "the VM is not running")
		}
		return ctrl.Result{}, nil
	}

	if !enabled {
		instance.Status.ComputeNetworkAttachmentStatuses = nil
		instance.SetIPAddress(firstIPv4OfInterface(vmi, vmNetworkDefault, nil))
		return ctrl.Result{}, nil
	}

	// The guest reports its address, so it is trusted only inside the Subnet's
	// overlay range. With no range known (no Subnet ID, a missing or
	// ambiguous Subnet, or no status.ipam yet) no address is published: the
	// check fails closed, and the controller polls until the range arrives.
	subnetID := instance.Spec.PrimarySubnetRef()
	overlay, err := r.subnetOverlayCIDRs(ctx, subnetID)
	if err != nil {
		return ctrl.Result{}, err
	}
	address := ""
	if len(overlay) > 0 {
		address = firstIPv4OfInterface(vmi, vmNetworkTenant, overlay)
	}
	if address == "" {
		if len(overlay) == 0 && keepsValidatedAddress(instance, vmi, subnetID) {
			// The range went away under a VM whose address was validated against it and is
			// still reported: keep the address and the condition. Only new addresses wait.
			return ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
		}
		instance.Status.ComputeNetworkAttachmentStatuses = nil
		instance.SetIPAddress("")
		if len(overlay) == 0 {
			if !failed {
				r.setNetworkReady(instance, metav1.ConditionFalse, v1alpha1.ComputeInstanceReasonWaitingForIPAddress,
					fmt.Sprintf("waiting for the address range of the Subnet %q before accepting a VM address", subnetID))
			}
			return mergeReconcileResult(r.ipAddressNotFound(instance, vmi), ctrl.Result{RequeueAfter: r.StatusPollInterval}), nil
		}
		if !failed {
			r.setNetworkReady(instance, metav1.ConditionFalse, v1alpha1.ComputeInstanceReasonWaitingForIPAddress,
				fmt.Sprintf("waiting for an IPv4 address in %s on the VM interface %s", strings.Join(overlay, ", "), vmNetworkTenant))
		}
		return r.ipAddressNotFound(instance, vmi), nil
	}

	instance.Status.ComputeNetworkAttachmentStatuses = []v1alpha1.ComputeNetworkAttachmentStatus{{
		SubnetRef: subnetID,
		IPAddress: address,
	}}
	instance.SetIPAddress(address)
	if bound := boundIPv4Address(na); address != oldAddress && bound != "" && address != bound {
		r.Recorder.Eventf(instance, nil, corev1.EventTypeWarning, eventReasonIPAddressChanged, eventActionReconcile,
			"the VM address changed from %s to %s; the network attachment keeps %s", bound, address, bound)
	}
	if failed {
		return ctrl.Result{}, nil
	}
	if na != nil && meta.IsStatusConditionTrue(na.Status.Conditions, v1alpha1.NetworkConditionReady) {
		if conditionStatus(instance, v1alpha1.ComputeInstanceConditionNetworkReady) != metav1.ConditionTrue {
			r.Recorder.Eventf(instance, nil, corev1.EventTypeNormal, eventReasonNetworkAttached, eventActionReconcile,
				"network attachment ready, IP: %s", address)
		}
		r.setNetworkReady(instance, metav1.ConditionTrue, v1alpha1.ComputeInstanceReasonIPDiscovered, "")
		return ctrl.Result{}, nil
	}
	message := ""
	if na != nil {
		message = networkAttachmentReadyMessage(na)
	}
	if message == "" {
		message = "waiting for the fabric binding of the VM address"
	}
	r.setNetworkReady(instance, metav1.ConditionFalse, v1alpha1.ComputeInstanceReasonWaitingForFabricBinding, message)
	return ctrl.Result{}, nil
}

// reconcileReadyCondition sets Ready: a ready VM and NetworkReady=True.
func (r *ComputeInstanceReconciler) reconcileReadyCondition(instance *v1alpha1.ComputeInstance, kv *kubevirtv1.VirtualMachine) {
	oldReadyStatus := conditionStatus(instance, v1alpha1.ComputeInstanceConditionReady)
	vmReady := kv != nil && kvVMHasConditionWithStatus(kv, kubevirtv1.VirtualMachineReady, corev1.ConditionTrue)
	networkReady := instance.IsStatusConditionTrue(v1alpha1.ComputeInstanceConditionNetworkReady)
	if vmReady && networkReady {
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionReady, metav1.ConditionTrue, "", v1alpha1.ReasonAsExpected)
		if oldReadyStatus != metav1.ConditionTrue {
			r.Recorder.Eventf(instance, nil, corev1.EventTypeNormal, eventReasonReady, eventActionReconcile,
				"VirtualMachine is ready, IP: %s", instance.Status.IPAddress)
		}
		return
	}
	message := ""
	if vmReady && !networkReady {
		message = "waiting for the VM network"
	}
	instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionReady, metav1.ConditionFalse, message, v1alpha1.ReasonAsExpected)
}

// runningVMI returns the VM's VirtualMachineInstance when it is Running, or nil.
func (r *ComputeInstanceReconciler) runningVMI(ctx context.Context, instance *v1alpha1.ComputeInstance, kv *kubevirtv1.VirtualMachine) (*kubevirtv1.VirtualMachineInstance, error) {
	if kv == nil {
		return nil, nil
	}
	targetClient, err := getTargetClient(ctx, r.mgr, r.targetCluster)
	if err != nil {
		return nil, err
	}
	vmi := &kubevirtv1.VirtualMachineInstance{}
	if err := targetClient.Get(ctx, types.NamespacedName{Namespace: kv.Namespace, Name: kv.Name}, vmi); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get VirtualMachineInstance %s/%s: %w", kv.Namespace, kv.Name, err)
	}
	if vmi.Status.Phase != kubevirtv1.Running {
		return nil, nil
	}
	return vmi, nil
}

// keepsValidatedAddress reports whether the published address was validated against the Subnet
// with this ID and the VM still reports it.
func keepsValidatedAddress(instance *v1alpha1.ComputeInstance, vmi *kubevirtv1.VirtualMachineInstance, subnetID string) bool {
	statuses := instance.Status.ComputeNetworkAttachmentStatuses
	if len(statuses) != 1 || statuses[0].SubnetRef != subnetID || statuses[0].IPAddress == "" {
		return false
	}
	previous := statuses[0].IPAddress
	return firstIPv4OfInterface(vmi, vmNetworkTenant, []string{previous + "/32"}) == previous
}

// subnetOverlayCIDRs returns the overlay range of the Subnet with the given
// API ID (Subnet status.ipam, contract §4.8). It returns no range when the ID
// is empty, the Subnet is missing or ambiguous, or status.ipam is not set yet;
// the caller then accepts no address.
func (r *ComputeInstanceReconciler) subnetOverlayCIDRs(ctx context.Context, subnetID string) ([]string, error) {
	if subnetID == "" {
		return nil, nil
	}
	list := &v1alpha1.SubnetList{}
	if err := r.List(ctx, list, client.InNamespace(r.NetworkingNamespace), client.MatchingLabels{osacSubnetIDLabel: subnetID}); err != nil {
		return nil, fmt.Errorf("list Subnet %s: %w", subnetID, err)
	}
	if len(list.Items) != 1 || list.Items[0].Status.IPAM == nil {
		return nil, nil
	}
	return list.Items[0].Status.IPAM.OverlayIPv4CIDRs, nil
}

// firstIPv4OfInterface returns the first canonical IPv4 address of the VMI
// interface with the given name; with cidrs, only an address inside one of
// them. Other interfaces (including guest-only ones) are ignored.
func firstIPv4OfInterface(vmi *kubevirtv1.VirtualMachineInstance, name string, cidrs []string) string {
	for _, iface := range vmi.Status.Interfaces {
		if iface.Name != name {
			continue
		}
		candidates := append([]string{}, iface.IPs...)
		if iface.IP != "" {
			candidates = append(candidates, iface.IP)
		}
		for _, candidate := range candidates {
			address, err := netip.ParseAddr(candidate)
			if err != nil || !address.Is4() || address.String() != candidate {
				continue
			}
			if cidrs == nil || addressInCIDRs(address, cidrs) {
				return candidate
			}
		}
	}
	return ""
}

func addressInCIDRs(address netip.Addr, cidrs []string) bool {
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(cidr)
		if err == nil && prefix.Contains(address) {
			return true
		}
	}
	return false
}

// boundIPv4Address returns the VM address in the NetworkAttachment's stored
// task input (network_attachment.interface.ipv4_address), or "".
func boundIPv4Address(na *v1alpha1.NetworkAttachment) string {
	if na == nil {
		return ""
	}
	input, err := networktask.LoadInput(na)
	if err != nil || input == nil {
		return ""
	}
	attachment, _ := input["network_attachment"].(map[string]any)
	iface, _ := attachment["interface"].(map[string]any)
	address, _ := iface["ipv4_address"].(string)
	return address
}

// ipAddressNotFound emits IPAddressNotFound once per VMI, 10 minutes after
// its transition to Running, and returns when to check again.
func (r *ComputeInstanceReconciler) ipAddressNotFound(instance *v1alpha1.ComputeInstance, vmi *kubevirtv1.VirtualMachineInstance) ctrl.Result {
	runningSince := time.Time{}
	for _, transition := range vmi.Status.PhaseTransitionTimestamps {
		if transition.Phase == kubevirtv1.Running && transition.PhaseTransitionTimestamp.Time.After(runningSince) {
			runningSince = transition.PhaseTransitionTimestamp.Time
		}
	}
	if runningSince.IsZero() {
		return ctrl.Result{RequeueAfter: r.StatusPollInterval}
	}
	if wait := runningSince.Add(ipAddressNotFoundDelay).Sub(r.clock()); wait > 0 {
		return ctrl.Result{RequeueAfter: wait}
	}
	r.ipNotFoundMu.Lock()
	defer r.ipNotFoundMu.Unlock()
	if r.ipNotFoundEvents == nil {
		r.ipNotFoundEvents = map[types.UID]bool{}
	}
	if !r.ipNotFoundEvents[vmi.UID] {
		r.ipNotFoundEvents[vmi.UID] = true
		r.Recorder.Eventf(instance, nil, corev1.EventTypeWarning, eventReasonIPAddressNotFound, eventActionReconcile,
			"the VM has no usable IPv4 address on interface %s after %s (none reported, or none in the Subnet range)", vmNetworkTenant, ipAddressNotFoundDelay)
	}
	return ctrl.Result{}
}

func (r *ComputeInstanceReconciler) handleDelete(ctx context.Context, _ reconcile.Request, instance *v1alpha1.ComputeInstance) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("deleting compute instance")

	setComputeInstancePhase(&instance.Status, v1alpha1.ComputeInstancePhaseDeleting)
	instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionReady, metav1.ConditionFalse,
		"ComputeInstance is being deleted", v1alpha1.ReasonDeleting)

	// Base finalizer has already been removed, cleanup complete
	if !controllerutil.ContainsFinalizer(instance, osacComputeInstanceFinalizer) {
		return ctrl.Result{}, nil
	}

	// Hold until no automatic ExternalIPAttachment or ExternalIP CR remains;
	// the fulfillment service deletes them (VMaaS design, Delete step 2).
	remains, err := automaticExternalAccessRemains(ctx, r.Client, r.NetworkingNamespace,
		autoExternalIPOwner{kind: computeInstanceOwner, id: instance.Labels[osacComputeInstanceIDLabel]})
	if err != nil {
		return ctrl.Result{}, err
	}
	if remains {
		return ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
	}

	vmGone, result, err := r.removeVM(ctx, instance)
	if err != nil || !vmGone {
		return result, err
	}

	if r.provisioningEnabled() {
		naGone, err := r.deleteNetworkAttachment(ctx, instance)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !naGone {
			return ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
		}
	}

	// Write the deletion status first; remove the finalizer on a later reconcile.
	if !equality.Semantic.DeepEqual(instance.Status, *r.persistedStatus(ctx, instance)) {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	if controllerutil.RemoveFinalizer(instance, osacComputeInstanceFinalizer) {
		if err := r.Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
	}
	r.Engine.Forget(instance.UID)
	return ctrl.Result{}, nil
}

// persistedStatus returns the status last read from the cache.
func (r *ComputeInstanceReconciler) persistedStatus(ctx context.Context, instance *v1alpha1.ComputeInstance) *v1alpha1.ComputeInstanceStatus {
	current := &v1alpha1.ComputeInstance{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(instance), current); err != nil {
		return &v1alpha1.ComputeInstanceStatus{}
	}
	return &current.Status
}

// removeVM removes the VM (VMaaS design, Delete step 4): it cancels a running
// provision job and waits for its end, runs the deprovision job when a
// provision job ever started, then waits until the VM, the VMI, the
// virt-launcher pod, the DataVolumes and the Secrets are gone.
func (r *ComputeInstanceReconciler) removeVM(ctx context.Context, instance *v1alpha1.ComputeInstance) (bool, ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	if val, exists := instance.Annotations[osacComputeInstanceManagementStateAnnotation]; exists && val == ManagementStateManual {
		log.Info("skipping deprovisioning due to management-state annotation", "management-state", val)
		return true, ctrl.Result{}, nil
	}

	done, wait, err := r.Engine.CancelForDelete(ctx, instance, kindComputeInstance)
	if err != nil {
		return false, ctrl.Result{}, err
	}
	if !done {
		return false, ctrl.Result{RequeueAfter: wait}, nil
	}
	if !networktask.Started(instance, computeInstanceProvisionTask, v1alpha1.NetworkJobManagerNone) ||
		instance.Status.VirtualMachineReference == nil {
		// No provision job ever started: no VM exists.
		return true, ctrl.Result{}, nil
	}

	namespace := instance.Status.VirtualMachineReference.Namespace
	vars, err := provisioning.JobVars(ctx, instance)
	if err != nil {
		return false, ctrl.Result{}, fmt.Errorf("build the deprovision job input: %w", err)
	}
	vars["vm_network"] = vmNetwork(namespace, instance.Labels[osacComputeInstanceIDLabel], r.provisioningEnabled())
	outcome, err := r.Engine.Run(ctx, instance, networktask.Request{
		Task:     computeInstanceDeprovisionTask,
		Manager:  v1alpha1.NetworkJobManagerNone,
		Kind:     kindComputeInstance,
		Template: r.DeprovisionTemplate,
		Vars:     vars,
	})
	if err != nil {
		return false, ctrl.Result{}, err
	}
	if !outcome.Succeeded() {
		return false, ctrl.Result{RequeueAfter: outcome.RequeueAfter}, nil
	}

	gone, err := r.vmResourcesGone(ctx, instance, namespace)
	if err != nil {
		return false, ctrl.Result{}, err
	}
	if !gone {
		return false, ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
	}
	return true, ctrl.Result{}, nil
}

// vmResourcesGone reports whether the VM and VMI (by name) and the
// virt-launcher pod, DataVolumes and Secrets (by the API ID label) are gone
// from the VM namespace. It reads the API server, not a cache.
func (r *ComputeInstanceReconciler) vmResourcesGone(ctx context.Context, instance *v1alpha1.ComputeInstance, namespace string) (bool, error) {
	cluster, err := r.mgr.GetCluster(ctx, r.targetCluster)
	if err != nil {
		return false, err
	}
	reader := cluster.GetAPIReader()
	key := types.NamespacedName{Namespace: namespace, Name: instance.Name}
	for _, obj := range []client.Object{&kubevirtv1.VirtualMachine{}, &kubevirtv1.VirtualMachineInstance{}} {
		if err := reader.Get(ctx, key, obj); err == nil {
			return false, nil
		} else if !apierrors.IsNotFound(err) {
			return false, err
		}
	}
	selector := client.MatchingLabels{computeInstanceIDVMLabel: instance.Labels[osacComputeInstanceIDLabel]}
	if instance.Labels[osacComputeInstanceIDLabel] == "" {
		return true, nil
	}
	dataVolumes := &unstructured.UnstructuredList{}
	dataVolumes.SetGroupVersionKind(dataVolumeListGVK)
	for _, list := range []client.ObjectList{&corev1.PodList{}, &corev1.SecretList{}, dataVolumes} {
		if err := reader.List(ctx, list, client.InNamespace(namespace), selector); err != nil {
			if meta.IsNoMatchError(err) {
				// CDI is not installed: no DataVolume can exist.
				continue
			}
			return false, err
		}
		if meta.LenList(list) > 0 {
			return false, nil
		}
	}
	return true, nil
}

// deleteNetworkAttachment deletes the VM's NetworkAttachment and removes the
// ComputeInstance finalizer from it once only that finalizer remains (VMaaS
// design, Delete steps 5 and 6). It reads the API server, not the cache. It
// returns true when the NetworkAttachment is gone.
func (r *ComputeInstanceReconciler) deleteNetworkAttachment(ctx context.Context, instance *v1alpha1.ComputeInstance) (bool, error) {
	id := instance.Labels[osacComputeInstanceIDLabel]
	if id == "" {
		return true, nil
	}
	na := &v1alpha1.NetworkAttachment{}
	key := client.ObjectKey{Namespace: r.NetworkingNamespace, Name: v1alpha1.NetworkAttachmentName(v1alpha1.NetworkAttachmentTargetComputeInstance, id)}
	if err := r.APIReader.Get(ctx, key, na); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}
	if na.DeletionTimestamp.IsZero() {
		if err := r.Delete(ctx, na); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete NetworkAttachment %s: %w", key.Name, err)
		}
		return false, nil
	}
	if len(na.Finalizers) == 1 && na.Finalizers[0] == osacComputeInstanceFinalizer {
		base := na.DeepCopy()
		controllerutil.RemoveFinalizer(na, osacComputeInstanceFinalizer)
		if err := r.Patch(ctx, na, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
	}
	return false, nil
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

// handleKubeVirtVM records the VM reference and sets the Provisioned and
// RestartRequired conditions from the KubeVirt VM. Ready and the address are
// set by reconcileNetworkStatus and reconcileReadyCondition.
func (r *ComputeInstanceReconciler) handleKubeVirtVM(ctx context.Context, instance *v1alpha1.ComputeInstance,
	kv *kubevirtv1.VirtualMachine) {
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

	// RestartRequired mirrors KubeVirt's RestartRequired condition, which is set when
	// CPU/memory/device changes have been applied to the VM spec but require a reboot.
	if kvVMHasConditionWithStatus(kv, kubevirtv1.VirtualMachineRestartRequired, corev1.ConditionTrue) {
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionRestartRequired, metav1.ConditionTrue, "", v1alpha1.ReasonAsExpected)
	} else {
		instance.SetStatusCondition(v1alpha1.ComputeInstanceConditionRestartRequired, metav1.ConditionFalse, "", v1alpha1.ReasonAsExpected)
	}
}

// handleDesiredConfigVersion sets status.desiredConfigVersion to the hash of spec.
func (r *ComputeInstanceReconciler) handleDesiredConfigVersion(_ context.Context, instance *v1alpha1.ComputeInstance) error {
	version, err := provisioning.ComputeDesiredConfigVersion(instance.Spec)
	if err != nil {
		return err
	}
	instance.Status.DesiredConfigVersion = version
	return nil
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
