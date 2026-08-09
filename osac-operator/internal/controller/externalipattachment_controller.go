/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mc "sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"k8s.io/utils/ptr"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const (
	osacExternalIPAttachmentFinalizer = "osac.openshift.io/externalipattachment-finalizer"
)

// ExternalIPAttachmentReconciler reconciles ExternalIPAttachment CRs.
//
// Creating a ExternalIPAttachment triggers an attach operation (osac-attach-external-ip AAP
// template) that moves the MetalLB Service from the parking namespace to the VM namespace.
// Deleting the CR triggers detach (osac-detach-external-ip) which reverses that.
//
// The controller uses RunProvisioningLifecycle for provisioning, giving automatic
// exponential-backoff retry on failure. It also watches ComputeInstance resources to
// auto-delete the ExternalIPAttachment when the target CI is deleted.
type ExternalIPAttachmentReconciler struct {
	client.Client
	APIReader                  client.Reader
	Scheme                     *runtime.Scheme
	mgr                        mcmanager.Manager
	NetworkingNamespace        string
	ComputeInstanceNamespace   string
	ClusterOrderNamespace      string
	BaremetalInstanceNamespace string
	ProvisioningProvider       provisioning.ProvisioningProvider
	StatusPollInterval         time.Duration
	MaxJobHistory              int
	targetCluster              mc.ClusterName
}

// NewExternalIPAttachmentReconciler creates a new reconciler for ExternalIPAttachment resources.
func NewExternalIPAttachmentReconciler(
	mgr mcmanager.Manager,
	networkingNamespace string,
	computeInstanceNamespace string,
	clusterOrderNamespace string,
	baremetalInstanceNamespace string,
	provisioningProvider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration,
	maxJobHistory int,
	targetCluster mc.ClusterName,
) *ExternalIPAttachmentReconciler {
	if mgr == nil {
		panic("mgr must not be nil")
	}
	if statusPollInterval <= 0 {
		statusPollInterval = provisioning.DefaultStatusPollInterval
	}
	if maxJobHistory <= 0 {
		maxJobHistory = provisioning.DefaultMaxJobHistory
	}
	if computeInstanceNamespace == "" {
		computeInstanceNamespace = defaultComputeInstanceNamespace
	}
	if clusterOrderNamespace == "" {
		clusterOrderNamespace = defaultClusterOrderNamespace
	}
	if baremetalInstanceNamespace == "" {
		baremetalInstanceNamespace = DefaultBareMetalInstanceNamespace
	}
	return &ExternalIPAttachmentReconciler{
		Client:                     mgr.GetLocalManager().GetClient(),
		APIReader:                  mgr.GetLocalManager().GetAPIReader(),
		Scheme:                     mgr.GetLocalManager().GetScheme(),
		mgr:                        mgr,
		NetworkingNamespace:        networkingNamespace,
		ComputeInstanceNamespace:   computeInstanceNamespace,
		ClusterOrderNamespace:      clusterOrderNamespace,
		BaremetalInstanceNamespace: baremetalInstanceNamespace,
		ProvisioningProvider:       provisioningProvider,
		StatusPollInterval:         statusPollInterval,
		MaxJobHistory:              maxJobHistory,
		targetCluster:              targetCluster,
	}
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=externalipattachments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=externalipattachments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=externalipattachments/finalizers,verbs=update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=clusterorders,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=clusterorders/finalizers,verbs=update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalinstances,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalinstances/finalizers,verbs=update

// Reconcile handles create/update/delete for a ExternalIPAttachment CR.
func (r *ExternalIPAttachmentReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	attachment := &v1alpha1.ExternalIPAttachment{}
	if err := r.Get(ctx, req.NamespacedName, attachment); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	val, exists := attachment.Annotations[osacManagementStateAnnotation]
	if attachment.ObjectMeta.DeletionTimestamp.IsZero() && exists && val == ManagementStateUnmanaged {
		log.Info("ignoring ExternalIPAttachment due to management-state annotation", "management-state", val)
		return ctrl.Result{}, nil
	}

	log.Info("start reconcile", "externalIP", attachment.Spec.ExternalIP, "phase", attachment.Status.Phase)

	oldstatus := attachment.Status.DeepCopy()

	var res ctrl.Result
	var err error
	if attachment.ObjectMeta.DeletionTimestamp.IsZero() {
		res, err = r.handleUpdate(ctx, attachment)
	} else {
		res, err = r.handleDelete(ctx, attachment)
	}

	if !equality.Semantic.DeepEqual(attachment.Status, *oldstatus) {
		log.Info("status requires update", "phase", attachment.Status.Phase)
		if updateErr := r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(attachment), attachment.Status); updateErr != nil {
			return res, updateErr
		}
	}

	log.Info("end reconcile", "phase", attachment.Status.Phase)
	return res, err
}

func (r *ExternalIPAttachmentReconciler) handleUpdate(ctx context.Context, attachment *v1alpha1.ExternalIPAttachment) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	if controllerutil.AddFinalizer(attachment, osacExternalIPAttachmentFinalizer) {
		log.Info("adding finalizer")
		if err := r.Update(ctx, attachment); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, client.ObjectKeyFromObject(attachment), attachment); err != nil {
			return ctrl.Result{}, err
		}
	}

	if attachment.Status.Phase == "" {
		attachment.Status.Phase = v1alpha1.ExternalIPAttachmentPhaseProgressing
	}

	// Resolve parent ExternalIP by UUID label (spec.externalIP contains the fulfillment-service UUID)
	externalIPList := &v1alpha1.ExternalIPList{}
	if err := r.List(ctx, externalIPList,
		client.InNamespace(attachment.Namespace),
		client.MatchingLabels{osacExternalIPIDLabel: attachment.Spec.ExternalIP},
	); err != nil {
		return ctrl.Result{}, err
	}
	if len(externalIPList.Items) == 0 {
		log.Info("parent ExternalIP not found, requeueing", "externalIPUUID", attachment.Spec.ExternalIP)
		return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	externalIP := &externalIPList.Items[0]

	// Resolve parent ExternalIPPool by UUID label
	poolList := &v1alpha1.ExternalIPPoolList{}
	if err := r.List(ctx, poolList,
		client.InNamespace(attachment.Namespace),
		client.MatchingLabels{osacExternalIPPoolIDLabel: externalIP.Spec.Pool},
	); err != nil {
		return ctrl.Result{}, err
	}
	if len(poolList.Items) == 0 {
		log.Info("parent ExternalIPPool not found, requeueing", "poolUUID", externalIP.Spec.Pool)
		return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	pool := &poolList.Items[0]

	implementationStrategy := pool.Spec.ImplementationStrategy
	if implementationStrategy == "" {
		implementationStrategy = defaultExternalIPPoolImplementationStrategy
	}

	// Resolve target ComputeInstance
	ci, result, err := r.resolveComputeInstance(ctx, attachment)
	if err != nil || result.RequeueAfter > 0 {
		return result, err
	}

	// Resolve target ClusterOrder
	co, result, err := r.resolveClusterOrder(ctx, attachment)
	if err != nil || result.RequeueAfter > 0 {
		return result, err
	}

	// Resolve target BareMetalInstance
	_, result, err = r.resolveBaremetalInstance(ctx, attachment)
	if err != nil || result.RequeueAfter > 0 {
		return result, err
	}

	needsUpdate := r.syncAnnotations(attachment, pool, externalIP, implementationStrategy, ci, co)
	if needsUpdate {
		if err := r.Update(ctx, attachment); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// Compute desired config version
	desiredVersion, err := provisioning.ComputeDesiredConfigVersion(struct {
		Spec                   v1alpha1.ExternalIPAttachmentSpec
		ImplementationStrategy string
		TargetIP               string
	}{attachment.Spec, implementationStrategy, attachment.Annotations[osacExternalIPTargetIPAnnotation]})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to compute desired config version: %w", err)
	}
	attachment.Status.DesiredConfigVersion = desiredVersion

	v1alpha1.SetExternalIPAttachmentStatusCondition(attachment, metav1.Condition{
		Type:               string(v1alpha1.ExternalIPAttachmentConditionConfigurationApplied),
		Status:             metav1.ConditionTrue,
		Reason:             conditionReasonConfigurationApplied,
		Message:            conditionMessageConfigurationApplied,
		LastTransitionTime: metav1.Now(),
	})

	if attachment.Status.Phase == "" || (attachment.Status.Phase == v1alpha1.ExternalIPAttachmentPhaseReady &&
		!provisioning.IsConfigApplied(&attachment.Status.ProvisioningJobs, attachment.Status.DesiredConfigVersion)) {
		attachment.Status.Phase = v1alpha1.ExternalIPAttachmentPhaseProgressing
	}

	return r.handleProvisioning(ctx, attachment, externalIP, ci)
}

func (r *ExternalIPAttachmentReconciler) syncAnnotations(
	attachment *v1alpha1.ExternalIPAttachment,
	pool *v1alpha1.ExternalIPPool,
	externalIP *v1alpha1.ExternalIP,
	implementationStrategy string,
	ci *v1alpha1.ComputeInstance,
	co *v1alpha1.ClusterOrder,
) bool {
	if attachment.Annotations == nil {
		attachment.Annotations = make(map[string]string)
	}

	needsUpdate := false
	if attachment.Annotations[osacImplementationStrategyAnnotation] != implementationStrategy {
		attachment.Annotations[osacImplementationStrategyAnnotation] = implementationStrategy
		needsUpdate = true
	}
	if attachment.Annotations[osacExternalIPPoolNameAnnotation] != pool.Name {
		attachment.Annotations[osacExternalIPPoolNameAnnotation] = pool.Name
		needsUpdate = true
	}
	if attachment.Annotations[osacExternalIPNameAnnotation] != externalIP.Name {
		attachment.Annotations[osacExternalIPNameAnnotation] = externalIP.Name
		needsUpdate = true
	}
	if ci != nil && ci.Status.VirtualMachineReference != nil {
		targetNamespace := ci.Status.VirtualMachineReference.Namespace
		if attachment.Annotations[osacExternalIPTargetNamespaceAnnotation] != targetNamespace {
			attachment.Annotations[osacExternalIPTargetNamespaceAnnotation] = targetNamespace
			needsUpdate = true
		}
	}
	if co != nil {
		targetIP := r.resolveClusterEndpoint(co, attachment)
		if targetIP != "" && attachment.Annotations[osacExternalIPTargetIPAnnotation] != targetIP {
			attachment.Annotations[osacExternalIPTargetIPAnnotation] = targetIP
			needsUpdate = true
		}
	}
	return needsUpdate
}

// resolveComputeInstance looks up the target ComputeInstance by UUID label, handles
// auto-detach if the CI is being deleted, and adds the detach finalizer.
// Returns nil CI (with no requeue) when spec.computeInstance is not set.
func (r *ExternalIPAttachmentReconciler) resolveComputeInstance(
	ctx context.Context,
	attachment *v1alpha1.ExternalIPAttachment,
) (*v1alpha1.ComputeInstance, ctrl.Result, error) {
	if attachment.Spec.ComputeInstance == nil {
		return nil, ctrl.Result{}, nil
	}

	log := ctrllog.FromContext(ctx)

	ciList := &v1alpha1.ComputeInstanceList{}
	if err := r.List(ctx, ciList,
		client.InNamespace(r.ComputeInstanceNamespace),
		client.MatchingLabels{osacComputeInstanceIDLabel: *attachment.Spec.ComputeInstance},
	); err != nil {
		return nil, ctrl.Result{}, err
	}
	if len(ciList.Items) == 0 {
		log.Info("ComputeInstance not found, requeueing", "computeInstanceUUID", *attachment.Spec.ComputeInstance)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	ci := &ciList.Items[0]

	if !ci.DeletionTimestamp.IsZero() {
		log.Info("auto-detaching: ComputeInstance is being deleted", "computeInstance", ci.Name)
		if err := r.Delete(ctx, attachment); err != nil {
			return nil, ctrl.Result{}, client.IgnoreNotFound(err)
		}
		return nil, ctrl.Result{RequeueAfter: time.Second}, nil
	}

	if ci.Status.VirtualMachineReference == nil {
		log.Info("ComputeInstance has no VirtualMachineReference yet, requeueing", "computeInstance", ci.Name)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}

	if controllerutil.AddFinalizer(ci, osacExternalIPDetachFinalizer) {
		log.Info("adding externalip-detach finalizer to ComputeInstance", "computeInstance", ci.Name)
		if err := r.Update(ctx, ci); err != nil {
			return nil, ctrl.Result{}, err
		}
	}

	return ci, ctrl.Result{}, nil
}

// resolveClusterOrder looks up the target ClusterOrder by UUID label, handles
// auto-detach if the ClusterOrder is being deleted, checks endpoint readiness,
// and adds the detach finalizer.
// Returns nil ClusterOrder (with no requeue) when spec.cluster is not set.
func (r *ExternalIPAttachmentReconciler) resolveClusterOrder(
	ctx context.Context,
	attachment *v1alpha1.ExternalIPAttachment,
) (*v1alpha1.ClusterOrder, ctrl.Result, error) {
	if attachment.Spec.Cluster == nil {
		return nil, ctrl.Result{}, nil
	}

	log := ctrllog.FromContext(ctx)

	coList := &v1alpha1.ClusterOrderList{}
	if err := r.List(ctx, coList,
		client.InNamespace(r.ClusterOrderNamespace),
		client.MatchingLabels{osacClusterOrderIDLabel: *attachment.Spec.Cluster},
	); err != nil {
		return nil, ctrl.Result{}, err
	}
	if len(coList.Items) == 0 {
		log.Info("ClusterOrder not found, requeueing", "clusterOrderUUID", *attachment.Spec.Cluster)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	co := &coList.Items[0]

	if !co.DeletionTimestamp.IsZero() {
		log.Info("auto-detaching: ClusterOrder is being deleted", "clusterOrder", co.Name)
		if err := r.Delete(ctx, attachment); err != nil {
			return nil, ctrl.Result{}, client.IgnoreNotFound(err)
		}
		return nil, ctrl.Result{RequeueAfter: time.Second}, nil
	}

	endpoint := r.resolveClusterEndpoint(co, attachment)
	if endpoint == "" {
		log.Info("ClusterOrder endpoint not available yet, requeueing",
			"clusterOrder", co.Name,
			"targetEndpoint", ptr.Deref(attachment.Spec.TargetEndpoint, ""))
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}

	if controllerutil.AddFinalizer(co, osacExternalIPDetachFinalizer) {
		log.Info("adding externalip-detach finalizer to ClusterOrder", "clusterOrder", co.Name)
		if err := r.Update(ctx, co); err != nil {
			return nil, ctrl.Result{}, err
		}
	}

	return co, ctrl.Result{}, nil
}

func (r *ExternalIPAttachmentReconciler) resolveClusterEndpoint(
	co *v1alpha1.ClusterOrder,
	attachment *v1alpha1.ExternalIPAttachment,
) string {
	if attachment.Spec.TargetEndpoint == nil {
		return ""
	}
	switch *attachment.Spec.TargetEndpoint {
	case v1alpha1.ExternalIPAttachmentTargetEndpointAPI:
		return co.Status.ApiEndpoint
	case v1alpha1.ExternalIPAttachmentTargetEndpointIngress:
		return co.Status.IngressEndpoint
	default:
		return ""
	}
}

// resolveBaremetalInstance looks up the target BareMetalInstance by UUID label, handles
// auto-detach if the BMI is being deleted, and adds the detach finalizer.
// Returns nil BMI (with no requeue) when spec.baremetalInstance is not set.
func (r *ExternalIPAttachmentReconciler) resolveBaremetalInstance(
	ctx context.Context,
	attachment *v1alpha1.ExternalIPAttachment,
) (*bmfov1alpha1.BareMetalInstance, ctrl.Result, error) {
	if attachment.Spec.BaremetalInstance == nil {
		return nil, ctrl.Result{}, nil
	}

	log := ctrllog.FromContext(ctx)

	bmiList := &bmfov1alpha1.BareMetalInstanceList{}
	if err := r.List(ctx, bmiList,
		client.InNamespace(r.BaremetalInstanceNamespace),
		client.MatchingLabels{osacBareMetalInstanceIDLabel: *attachment.Spec.BaremetalInstance},
	); err != nil {
		return nil, ctrl.Result{}, err
	}
	if len(bmiList.Items) == 0 {
		log.Info("BareMetalInstance not found, requeueing", "baremetalInstanceUUID", *attachment.Spec.BaremetalInstance)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	bmi := &bmiList.Items[0]

	if !bmi.DeletionTimestamp.IsZero() {
		log.Info("auto-detaching: BareMetalInstance is being deleted", "baremetalInstance", bmi.Name)
		if err := r.Delete(ctx, attachment); err != nil {
			return nil, ctrl.Result{}, client.IgnoreNotFound(err)
		}
		return nil, ctrl.Result{RequeueAfter: time.Second}, nil
	}

	if controllerutil.AddFinalizer(bmi, osacExternalIPDetachFinalizer) {
		log.Info("adding externalip-detach finalizer to BareMetalInstance", "baremetalInstance", bmi.Name)
		if err := r.Update(ctx, bmi); err != nil {
			return nil, ctrl.Result{}, err
		}
	}

	return bmi, ctrl.Result{}, nil
}

func (r *ExternalIPAttachmentReconciler) handleProvisioning(
	ctx context.Context,
	attachment *v1alpha1.ExternalIPAttachment,
	externalIP *v1alpha1.ExternalIP,
	ci *v1alpha1.ComputeInstance,
) (ctrl.Result, error) {
	if r.ProvisioningProvider == nil {
		ctrllog.FromContext(ctx).Info("no provisioning provider configured, skipping provisioning")
		return ctrl.Result{}, nil
	}

	var provisionErr error

	result, err := provisioning.RunProvisioningLifecycle(ctx, r.ProvisioningProvider, attachment,
		&provisioning.State{Jobs: &attachment.Status.ProvisioningJobs, DesiredConfigVersion: attachment.Status.DesiredConfigVersion},
		r.MaxJobHistory, r.StatusPollInterval,
		&provisioning.PollCallbacks{
			OnFailed: func(message string) {
				attachment.Status.Phase = v1alpha1.ExternalIPAttachmentPhaseFailed
				setReadyConditionFailed(&attachment.Status.Conditions, message)
			},
			OnSuccess: func(_ provisioning.ProvisionStatus) {
				attachment.Status.Phase = v1alpha1.ExternalIPAttachmentPhaseReady
				// onProvisionSuccess error causes a requeue via provisionErr, but the
				// provisioning lifecycle won't re-invoke OnSuccess (job already succeeded).
				// The retry.RetryOnConflict inside onProvisionSuccess makes this window
				// very narrow — only persistent non-conflict API errors can reach here.
				provisionErr = r.onProvisionSuccess(ctx, externalIP, ci)
				setReadyConditionTrue(&attachment.Status.Conditions)
			},
		},
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(
				ctx, r.APIReader, client.ObjectKeyFromObject(attachment), &v1alpha1.ExternalIPAttachment{}, func(obj client.Object) []v1alpha1.JobStatus {
					return obj.(*v1alpha1.ExternalIPAttachment).Status.ProvisioningJobs
				})
		},
		func() error {
			return r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(attachment), attachment.Status)
		},
	)
	if err != nil {
		return result, err
	}
	if provisionErr != nil {
		return ctrl.Result{}, provisionErr
	}
	return result, nil
}

// onProvisionSuccess updates the parent ExternalIP and target ComputeInstance after
// a successful attach operation.
func (r *ExternalIPAttachmentReconciler) onProvisionSuccess(ctx context.Context, externalIP *v1alpha1.ExternalIP, ci *v1alpha1.ComputeInstance) error {
	// Set ExternalIP.status.attached = true
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fresh := &v1alpha1.ExternalIP{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(externalIP), fresh); err != nil {
			return err
		}
		if fresh.Status.Attached {
			return nil
		}
		fresh.Status.Attached = true
		return r.Status().Update(ctx, fresh)
	}); err != nil {
		return fmt.Errorf("failed to set ExternalIP status.attached=true: %w", err)
	}

	// Set ComputeInstance.status.externalIPAddress from the parent ExternalIP's address.
	// Re-fetch ExternalIP to get the latest address — the object captured by handleUpdate
	// may be stale if the ExternalIP controller populated the address after our initial read.
	if ci != nil {
		freshEIP := &v1alpha1.ExternalIP{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(externalIP), freshEIP); err != nil {
			return fmt.Errorf("failed to fetch ExternalIP for address lookup: %w", err)
		}
		if freshEIP.Status.Address != "" {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				fresh := &v1alpha1.ComputeInstance{}
				if err := r.Get(ctx, client.ObjectKeyFromObject(ci), fresh); err != nil {
					return err
				}
				if fresh.GetExternalIPAddress() == freshEIP.Status.Address {
					return nil
				}
				fresh.SetExternalIPAddress(freshEIP.Status.Address)
				return r.Status().Update(ctx, fresh)
			}); err != nil {
				return fmt.Errorf("failed to set ComputeInstance externalIPAddress: %w", err)
			}
		}
	}

	return nil
}

func (r *ExternalIPAttachmentReconciler) handleDelete(ctx context.Context, attachment *v1alpha1.ExternalIPAttachment) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("deleting ExternalIPAttachment")

	attachment.Status.Phase = v1alpha1.ExternalIPAttachmentPhaseDeleting

	if !controllerutil.ContainsFinalizer(attachment, osacExternalIPAttachmentFinalizer) {
		return ctrl.Result{}, nil
	}

	result, err := r.handleDeprovisioning(ctx, attachment)
	if err != nil || result.RequeueAfter > 0 {
		return result, err
	}

	// Deprovisioning complete: update parent resources and remove finalizers
	if err := r.onDeprovisionSuccess(ctx, attachment); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("removing finalizer after successful deprovisioning")
	controllerutil.RemoveFinalizer(attachment, osacExternalIPAttachmentFinalizer)
	if err := r.Update(ctx, attachment); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// onDeprovisionSuccess clears the attached state on the parent ExternalIP, clears
// externalIPAddress on the ComputeInstance, and removes the CI detach finalizer when
// no other ExternalIPAttachments reference the same CI.
func (r *ExternalIPAttachmentReconciler) onDeprovisionSuccess(ctx context.Context, attachment *v1alpha1.ExternalIPAttachment) error {
	// Clear ExternalIP.status.attached (look up by UUID label)
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		externalIPList := &v1alpha1.ExternalIPList{}
		if err := r.List(ctx, externalIPList,
			client.InNamespace(attachment.Namespace),
			client.MatchingLabels{osacExternalIPIDLabel: attachment.Spec.ExternalIP},
		); err != nil {
			return err
		}
		if len(externalIPList.Items) == 0 {
			return nil
		}
		externalIP := &externalIPList.Items[0]
		if !externalIP.Status.Attached {
			return nil
		}
		externalIP.Status.Attached = false
		return r.Status().Update(ctx, externalIP)
	}); err != nil {
		return fmt.Errorf("failed to clear ExternalIP status.attached: %w", err)
	}

	// Clear ComputeInstance.status.externalIPAddress and remove CI detach finalizer
	if attachment.Spec.ComputeInstance != nil {
		ciUUID := *attachment.Spec.ComputeInstance

		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			ciList := &v1alpha1.ComputeInstanceList{}
			if err := r.List(ctx, ciList,
				client.InNamespace(r.ComputeInstanceNamespace),
				client.MatchingLabels{osacComputeInstanceIDLabel: ciUUID},
			); err != nil {
				return err
			}
			if len(ciList.Items) == 0 {
				return nil
			}
			ci := &ciList.Items[0]
			if ci.GetExternalIPAddress() == "" {
				return nil
			}
			ci.SetExternalIPAddress("")
			return r.Status().Update(ctx, ci)
		}); err != nil {
			return fmt.Errorf("failed to clear ComputeInstance externalIPAddress: %w", err)
		}

		if err := r.removeCIDetachFinalizerIfUnreferenced(ctx, ciUUID, attachment.Name); err != nil {
			return fmt.Errorf("failed to remove CI detach finalizer: %w", err)
		}
	}

	// Remove ClusterOrder detach finalizer
	if attachment.Spec.Cluster != nil {
		coUUID := *attachment.Spec.Cluster
		if err := r.maybeRemoveCODetachFinalizer(ctx, coUUID, attachment.Name); err != nil {
			return fmt.Errorf("failed to remove ClusterOrder detach finalizer: %w", err)
		}
	}

	// Remove BareMetalInstance detach finalizer
	if attachment.Spec.BaremetalInstance != nil {
		bmiUUID := *attachment.Spec.BaremetalInstance
		if err := r.maybeRemoveBMIDetachFinalizer(ctx, bmiUUID, attachment.Name); err != nil {
			return fmt.Errorf("failed to remove BareMetalInstance detach finalizer: %w", err)
		}
	}

	return nil
}

// removeCIDetachFinalizerIfUnreferenced removes the externalip-detach finalizer from the
// ComputeInstance if no other ExternalIPAttachments still reference it.
// ciUUID is the fulfillment-service UUID used in spec.computeInstance and CI labels.
// Uses retry.RetryOnConflict to handle concurrent modifications to the ComputeInstance.
func (r *ExternalIPAttachmentReconciler) removeCIDetachFinalizerIfUnreferenced(ctx context.Context, ciUUID string, excludeAttachment string) error {
	log := ctrllog.FromContext(ctx)

	// Check if other ExternalIPAttachments still reference this CI (no retry needed)
	attachments := &v1alpha1.ExternalIPAttachmentList{}
	if err := r.List(ctx, attachments, client.InNamespace(r.NetworkingNamespace)); err != nil {
		return err
	}
	for i := range attachments.Items {
		if attachments.Items[i].Name == excludeAttachment {
			continue
		}
		if attachments.Items[i].Spec.ComputeInstance != nil && *attachments.Items[i].Spec.ComputeInstance == ciUUID {
			log.Info("other ExternalIPAttachments still reference CI, keeping finalizer",
				"computeInstanceUUID", ciUUID,
				"attachment", attachments.Items[i].Name)
			return nil
		}
	}

	log.Info("no more references, removing CI detach finalizer", "computeInstanceUUID", ciUUID)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		ciList := &v1alpha1.ComputeInstanceList{}
		if err := r.List(ctx, ciList,
			client.InNamespace(r.ComputeInstanceNamespace),
			client.MatchingLabels{osacComputeInstanceIDLabel: ciUUID},
		); err != nil {
			return err
		}
		if len(ciList.Items) == 0 {
			return nil
		}
		ci := &ciList.Items[0]

		// RemoveFinalizer returns false if the finalizer is absent, skipping the unnecessary Update.
		if controllerutil.RemoveFinalizer(ci, osacExternalIPDetachFinalizer) {
			return r.Update(ctx, ci)
		}
		return nil
	})
}

// maybeRemoveCODetachFinalizer removes the externalip-detach finalizer from the
// ClusterOrder if no other ExternalIPAttachments still reference it.
func (r *ExternalIPAttachmentReconciler) maybeRemoveCODetachFinalizer(ctx context.Context, coUUID string, excludeAttachment string) error {
	log := ctrllog.FromContext(ctx)

	// Check if other ExternalIPAttachments still reference this CO (no retry needed)
	attachments := &v1alpha1.ExternalIPAttachmentList{}
	if err := r.List(ctx, attachments, client.InNamespace(r.NetworkingNamespace)); err != nil {
		return err
	}
	for i := range attachments.Items {
		if attachments.Items[i].Name == excludeAttachment {
			continue
		}
		if attachments.Items[i].Spec.Cluster != nil && *attachments.Items[i].Spec.Cluster == coUUID {
			log.Info("other ExternalIPAttachments still reference ClusterOrder, keeping finalizer",
				"clusterOrderUUID", coUUID,
				"attachment", attachments.Items[i].Name)
			return nil
		}
	}

	log.Info("no more references, removing ClusterOrder detach finalizer", "clusterOrderUUID", coUUID)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		coList := &v1alpha1.ClusterOrderList{}
		if err := r.List(ctx, coList,
			client.InNamespace(r.ClusterOrderNamespace),
			client.MatchingLabels{osacClusterOrderIDLabel: coUUID},
		); err != nil {
			return err
		}
		if len(coList.Items) == 0 {
			return nil
		}
		co := &coList.Items[0]

		// RemoveFinalizer returns false if the finalizer is absent, skipping the unnecessary Update.
		if controllerutil.RemoveFinalizer(co, osacExternalIPDetachFinalizer) {
			return r.Update(ctx, co)
		}
		return nil
	})
}

// maybeRemoveBMIDetachFinalizer removes the externalip-detach finalizer from the
// BareMetalInstance if no other ExternalIPAttachments still reference it.
func (r *ExternalIPAttachmentReconciler) maybeRemoveBMIDetachFinalizer(ctx context.Context, bmiUUID string, excludeAttachment string) error {
	log := ctrllog.FromContext(ctx)

	attachments := &v1alpha1.ExternalIPAttachmentList{}
	if err := r.List(ctx, attachments, client.InNamespace(r.NetworkingNamespace)); err != nil {
		return err
	}
	for i := range attachments.Items {
		if attachments.Items[i].Name == excludeAttachment {
			continue
		}
		if attachments.Items[i].Spec.BaremetalInstance != nil && *attachments.Items[i].Spec.BaremetalInstance == bmiUUID {
			log.Info("other ExternalIPAttachments still reference BareMetalInstance, keeping finalizer",
				"baremetalInstanceUUID", bmiUUID,
				"attachment", attachments.Items[i].Name)
			return nil
		}
	}

	log.Info("no more references, removing BareMetalInstance detach finalizer", "baremetalInstanceUUID", bmiUUID)

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		bmiList := &bmfov1alpha1.BareMetalInstanceList{}
		if err := r.List(ctx, bmiList,
			client.InNamespace(r.BaremetalInstanceNamespace),
			client.MatchingLabels{osacBareMetalInstanceIDLabel: bmiUUID},
		); err != nil {
			return err
		}
		if len(bmiList.Items) == 0 {
			return nil
		}
		bmi := &bmiList.Items[0]

		if controllerutil.RemoveFinalizer(bmi, osacExternalIPDetachFinalizer) {
			return r.Update(ctx, bmi)
		}
		return nil
	})
}

// mapBaremetalInstanceToExternalIPAttachments maps a BareMetalInstance change to all
// ExternalIPAttachments that reference it, so the controller can react to
// BMI deletion.
func (r *ExternalIPAttachmentReconciler) mapBaremetalInstanceToExternalIPAttachments(ctx context.Context, obj client.Object) []reconcile.Request {
	log := ctrllog.FromContext(ctx)

	bmiUUID, exists := obj.GetLabels()[osacBareMetalInstanceIDLabel]
	if !exists {
		return nil
	}

	attachments := &v1alpha1.ExternalIPAttachmentList{}
	if err := r.List(ctx, attachments, client.InNamespace(r.NetworkingNamespace)); err != nil {
		log.Error(err, "failed to list ExternalIPAttachments for BareMetalInstance watch")
		return nil
	}

	var requests []reconcile.Request
	for i := range attachments.Items {
		if attachments.Items[i].Spec.BaremetalInstance != nil && *attachments.Items[i].Spec.BaremetalInstance == bmiUUID {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(&attachments.Items[i]),
			})
		}
	}

	if len(requests) > 0 {
		log.Info("mapped BareMetalInstance change to ExternalIPAttachments",
			"baremetalInstance", obj.GetName(),
			"baremetalInstanceUUID", bmiUUID,
			"attachmentCount", len(requests),
		)
	}

	return requests
}

// mapClusterOrderToExternalIPAttachments maps a ClusterOrder change to all
// ExternalIPAttachments that reference it, so the controller can react to
// ClusterOrder deletion or endpoint changes.
func (r *ExternalIPAttachmentReconciler) mapClusterOrderToExternalIPAttachments(ctx context.Context, obj client.Object) []reconcile.Request {
	log := ctrllog.FromContext(ctx)

	coUUID, exists := obj.GetLabels()[osacClusterOrderIDLabel]
	if !exists {
		return nil
	}

	attachments := &v1alpha1.ExternalIPAttachmentList{}
	if err := r.List(ctx, attachments, client.InNamespace(r.NetworkingNamespace)); err != nil {
		log.Error(err, "failed to list ExternalIPAttachments for ClusterOrder watch")
		return nil
	}

	var requests []reconcile.Request
	for i := range attachments.Items {
		if attachments.Items[i].Spec.Cluster != nil && *attachments.Items[i].Spec.Cluster == coUUID {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(&attachments.Items[i]),
			})
		}
	}

	if len(requests) > 0 {
		log.Info("mapped ClusterOrder change to ExternalIPAttachments",
			"clusterOrder", obj.GetName(),
			"clusterOrderUUID", coUUID,
			"attachmentCount", len(requests),
		)
	}

	return requests
}

func (r *ExternalIPAttachmentReconciler) handleDeprovisioning(ctx context.Context, attachment *v1alpha1.ExternalIPAttachment) (ctrl.Result, error) {
	if r.ProvisioningProvider == nil {
		ctrllog.FromContext(ctx).Info("no provisioning provider configured, skipping deprovisioning")
		return ctrl.Result{}, nil
	}
	result, done, err := provisioning.RunDeprovisioningLifecycle(ctx, r.ProvisioningProvider, attachment,
		&attachment.Status.ProvisioningJobs, r.MaxJobHistory, r.StatusPollInterval)
	if err != nil || !done {
		return result, err
	}
	return ctrl.Result{}, nil
}

func (r *ExternalIPAttachmentReconciler) updateStatusWithRetry(ctx context.Context, key client.ObjectKey, newStatus v1alpha1.ExternalIPAttachmentStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.ExternalIPAttachment{}
		if err := r.Get(ctx, key, latest); err != nil {
			return err
		}
		latest.Status = newStatus
		return r.Status().Update(ctx, latest)
	})
}

// mapComputeInstanceToExternalIPAttachments maps a ComputeInstance change to all
// ExternalIPAttachments that reference it, so the controller can react to CI deletion
// or VirtualMachineReference changes.
func (r *ExternalIPAttachmentReconciler) mapComputeInstanceToExternalIPAttachments(ctx context.Context, obj client.Object) []reconcile.Request {
	log := ctrllog.FromContext(ctx)

	ciUUID, exists := obj.GetLabels()[osacComputeInstanceIDLabel]
	if !exists {
		return nil
	}

	attachments := &v1alpha1.ExternalIPAttachmentList{}
	if err := r.List(ctx, attachments, client.InNamespace(r.NetworkingNamespace)); err != nil {
		log.Error(err, "failed to list ExternalIPAttachments for ComputeInstance watch")
		return nil
	}

	var requests []reconcile.Request
	for i := range attachments.Items {
		if attachments.Items[i].Spec.ComputeInstance != nil && *attachments.Items[i].Spec.ComputeInstance == ciUUID {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(&attachments.Items[i]),
			})
		}
	}

	if len(requests) > 0 {
		log.Info("mapped ComputeInstance change to ExternalIPAttachments",
			"computeInstance", obj.GetName(),
			"computeInstanceUUID", ciUUID,
			"attachmentCount", len(requests),
		)
	}

	return requests
}

// SetupWithManager registers this controller with the multicluster manager.
func (r *ExternalIPAttachmentReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mgr).
		For(&v1alpha1.ExternalIPAttachment{},
			mcbuilder.WithPredicates(NetworkingNamespacePredicate(r.NetworkingNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Watches(
			&v1alpha1.ComputeInstance{},
			mchandler.EnqueueRequestsFromMapFunc(r.mapComputeInstanceToExternalIPAttachments),
			mcbuilder.WithPredicates(ComputeInstanceNamespacePredicate(r.ComputeInstanceNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false),
		).
		Watches(
			&v1alpha1.ClusterOrder{},
			mchandler.EnqueueRequestsFromMapFunc(r.mapClusterOrderToExternalIPAttachments),
			mcbuilder.WithPredicates(NamespacePredicate(r.ClusterOrderNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false),
		).
		Watches(
			&bmfov1alpha1.BareMetalInstance{},
			mchandler.EnqueueRequestsFromMapFunc(r.mapBaremetalInstanceToExternalIPAttachments),
			mcbuilder.WithPredicates(BareMetalInstanceNamespacePredicate(r.BaremetalInstanceNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false),
		).
		Complete(r)
}
