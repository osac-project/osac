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
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mc "sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const (
	osacVirtualNetworkFinalizer = "osac.openshift.io/virtualnetwork-finalizer"
)

// VirtualNetworkReconciler reconciles a VirtualNetwork object
type VirtualNetworkReconciler struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme
	// mgr and targetCluster are stored for future multi-cluster target client resolution
	mgr                  mcmanager.Manager
	NetworkingNamespace  string
	ProvisioningProvider provisioning.ProvisioningProvider
	StatusPollInterval   time.Duration
	MaxJobHistory        int
	targetCluster        mc.ClusterName
	// Resolver resolves a NetworkClass to its registered managers. Nil when the
	// two-manager model isn't configured (no gRPC connection / networking namespace),
	// in which case the controller cannot resolve an implementation strategy for any
	// VirtualNetwork and requeues until Resolver is configured.
	Resolver *dispatcher.Resolver
	// NetworkProvisioningEnabled controls whether the controller dispatches AAP
	// provisioning jobs. When false, resources are set to Ready immediately
	// without triggering any infrastructure provisioning.
	NetworkProvisioningEnabled bool
}

// NewVirtualNetworkReconciler creates a new reconciler for VirtualNetwork resources.
func NewVirtualNetworkReconciler(
	mgr mcmanager.Manager,
	networkingNamespace string,
	provisioningProvider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration,
	maxJobHistory int,
	targetCluster mc.ClusterName,
	resolver *dispatcher.Resolver,
) *VirtualNetworkReconciler {
	if mgr == nil {
		panic("mgr must not be nil")
	}
	if statusPollInterval <= 0 {
		statusPollInterval = provisioning.DefaultStatusPollInterval
	}
	if maxJobHistory <= 0 {
		maxJobHistory = provisioning.DefaultMaxJobHistory
	}
	return &VirtualNetworkReconciler{
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
	}
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=virtualnetworks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=virtualnetworks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=virtualnetworks/finalizers,verbs=update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=subnets,verbs=list
// +kubebuilder:rbac:groups=osac.openshift.io,resources=securitygroups,verbs=list
// +kubebuilder:rbac:groups=osac.openshift.io,resources=natgateways,verbs=list

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *VirtualNetworkReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	vnet := &v1alpha1.VirtualNetwork{}
	if err := r.Get(ctx, req.NamespacedName, vnet); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	val, exists := vnet.Annotations[osacManagementStateAnnotation]
	if vnet.ObjectMeta.DeletionTimestamp.IsZero() && exists && val == ManagementStateUnmanaged {
		log.Info("ignoring VirtualNetwork due to management-state annotation", "management-state", val)
		return ctrl.Result{}, nil
	}

	log.Info("start reconcile")

	oldstatus := vnet.Status.DeepCopy()

	var res ctrl.Result
	var err error
	if vnet.ObjectMeta.DeletionTimestamp.IsZero() {
		res, err = r.handleUpdate(ctx, vnet)
	} else {
		res, err = r.handleDelete(ctx, vnet)
	}

	if !equality.Semantic.DeepEqual(vnet.Status, *oldstatus) {
		log.Info("status requires update")
		if err := r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(vnet), vnet.Status); err != nil {
			return res, err
		}
	}

	log.Info("end reconcile")
	return res, err
}

// handleUpdate processes VirtualNetwork creation and updates
func (r *VirtualNetworkReconciler) handleUpdate(ctx context.Context, vnet *v1alpha1.VirtualNetwork) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// Add finalizer if not present
	if controllerutil.AddFinalizer(vnet, osacVirtualNetworkFinalizer) {
		if err := r.Update(ctx, vnet); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Set phase to Progressing only on first reconcile (empty phase).
	// Subsequent reconciles preserve the current phase — it gets updated
	// by OnSuccess/OnFailed callbacks in RunProvisioningLifecycle.
	if vnet.Status.Phase == "" {
		vnet.Status.Phase = v1alpha1.VirtualNetworkPhaseProgressing
	}

	if !r.NetworkProvisioningEnabled {
		vnet.Status.Phase = v1alpha1.VirtualNetworkPhaseReady
		setReadyConditionTrue(&vnet.Status.Conditions)
		return ctrl.Result{}, nil
	}

	// Determine implementation strategy from the dispatcher-resolved manager for this
	// VirtualNetwork's NetworkClass (fabric_manager, falling back to k8s_manager).
	implementationStrategy, err := resolveImplementationStrategy(
		ctx, r.Resolver, "VirtualNetwork", vnet.Spec.NetworkClass, "")
	if err != nil {
		return ctrl.Result{}, err
	}
	if implementationStrategy == "" {
		msg := fmt.Sprintf("NetworkClass '%s' has no fabric_manager or k8s_manager configured", vnet.Spec.NetworkClass)
		setReadyConditionBlocked(&vnet.Status.Conditions, v1alpha1.ReasonNoManagerConfigured, msg)
		log.Info("implementation strategy not set, requeueing", "virtualNetwork", vnet.Name)
		return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}

	// Add implementation-strategy annotation if not present or different
	// This allows AAP playbooks to select the appropriate role without doing lookups
	if vnet.Annotations == nil {
		vnet.Annotations = make(map[string]string)
	}
	if vnet.Annotations[osacImplementationStrategyAnnotation] != implementationStrategy {
		vnet.Annotations[osacImplementationStrategyAnnotation] = implementationStrategy
		log.Info("setting implementation-strategy annotation", "strategy", implementationStrategy)
		if err := r.Update(ctx, vnet); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Compute desired config version from spec and inherited implementation strategy
	desiredVersion, err := provisioning.ComputeDesiredConfigVersion(struct {
		Spec                   v1alpha1.VirtualNetworkSpec
		ImplementationStrategy string
	}{vnet.Spec, implementationStrategy})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to compute desired config version: %w", err)
	}
	vnet.Status.DesiredConfigVersion = desiredVersion

	// Set phase to Progressing only on first provision (empty phase) or when spec changed
	// after a previous success. Don't override Failed during backoff.
	if vnet.Status.Phase == "" || (vnet.Status.Phase == v1alpha1.VirtualNetworkPhaseReady &&
		!provisioning.IsConfigApplied(&vnet.Status.ProvisioningJobs, vnet.Status.DesiredConfigVersion)) {
		vnet.Status.Phase = v1alpha1.VirtualNetworkPhaseProgressing
	}

	// Handle provisioning
	return r.handleProvisioning(ctx, vnet)
}

// handleProvisioning manages the provisioning job lifecycle for a VirtualNetwork.
// Uses shared RunProvisioningLifecycle with config-version-based backoff on failure.
func (r *VirtualNetworkReconciler) handleProvisioning(ctx context.Context, vnet *v1alpha1.VirtualNetwork) (ctrl.Result, error) {
	if r.ProvisioningProvider == nil {
		ctrllog.FromContext(ctx).Info("no provisioning provider configured, skipping provisioning")
		return ctrl.Result{}, nil
	}

	return provisioning.RunProvisioningLifecycle(ctx, r.ProvisioningProvider, vnet,
		&provisioning.State{Jobs: &vnet.Status.ProvisioningJobs, DesiredConfigVersion: vnet.Status.DesiredConfigVersion},
		r.MaxJobHistory, r.StatusPollInterval,
		&provisioning.PollCallbacks{
			OnFailed: func(message string) {
				vnet.Status.Phase = v1alpha1.VirtualNetworkPhaseFailed
				setReadyConditionFailed(&vnet.Status.Conditions, message)
			},
			OnSuccess: func(_ provisioning.ProvisionStatus) {
				vnet.Status.Phase = v1alpha1.VirtualNetworkPhaseReady
				setReadyConditionTrue(&vnet.Status.Conditions)
			},
		},
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(ctx, r.APIReader, client.ObjectKeyFromObject(vnet), &v1alpha1.VirtualNetwork{}, func(obj client.Object) []v1alpha1.JobStatus {
				return obj.(*v1alpha1.VirtualNetwork).Status.ProvisioningJobs
			})
		},
		func() error {
			return r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(vnet), vnet.Status)
		},
	)
}

// handleDelete processes VirtualNetwork deletion
func (r *VirtualNetworkReconciler) handleDelete(ctx context.Context, vnet *v1alpha1.VirtualNetwork) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("deleting virtual network")

	vnet.Status.Phase = v1alpha1.VirtualNetworkPhaseDeleting

	// Base finalizer has already been removed, cleanup complete
	if !controllerutil.ContainsFinalizer(vnet, osacVirtualNetworkFinalizer) {
		return ctrl.Result{}, nil
	}

	// Gate: wait for all child resources referencing this VNet to be fully removed
	// before triggering the AAP deprovision job. Without this gate, the infrastructure
	// backend rejects the VNet deletion because children still exist, causing unnecessary
	// failed jobs and backoff delays.
	// Child resources reference the parent VN by its fulfillment-service UUID
	// (stored in the osac.openshift.io/virtualnetwork-uuid label), not by K8s name.
	vnetUUID := vnet.Labels[osacVirtualNetworkIDLabel]
	ns := vnet.Namespace

	subnetList := &v1alpha1.SubnetList{}
	if err := r.List(ctx, subnetList, client.InNamespace(ns)); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing subnets: %w", err)
	}
	for i := range subnetList.Items {
		if subnetList.Items[i].Spec.VirtualNetwork == vnetUUID {
			log.Info("waiting for child Subnet to be deleted before deprovisioning VirtualNetwork",
				"subnet", subnetList.Items[i].Name)
			return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
		}
	}

	sgList := &v1alpha1.SecurityGroupList{}
	if err := r.List(ctx, sgList, client.InNamespace(ns)); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing security groups: %w", err)
	}
	for i := range sgList.Items {
		if sgList.Items[i].Spec.VirtualNetwork == vnetUUID {
			log.Info("waiting for child SecurityGroup to be deleted before deprovisioning VirtualNetwork",
				"securityGroup", sgList.Items[i].Name)
			return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
		}
	}

	natgwList := &v1alpha1.NATGatewayList{}
	if err := r.List(ctx, natgwList, client.InNamespace(ns)); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing NAT gateways: %w", err)
	}
	for i := range natgwList.Items {
		if natgwList.Items[i].Spec.VirtualNetwork == vnetUUID {
			log.Info("waiting for child NATGateway to be deleted before deprovisioning VirtualNetwork",
				"natGateway", natgwList.Items[i].Name)
			return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
		}
	}

	// Handle deprovisioning
	if vnet.Annotations[osacImplementationStrategyAnnotation] == "" {
		log.Info("skipping deprovisioning — resource was never provisioned")
	} else {
		result, err := r.handleDeprovisioning(ctx, vnet)
		if err != nil {
			return result, err
		}

		// If we need to requeue (jobs still running), do so
		if result.RequeueAfter > 0 {
			return result, nil
		}
	}

	// Deprovisioning complete or skipped, remove base finalizer
	if controllerutil.RemoveFinalizer(vnet, osacVirtualNetworkFinalizer) {
		if err := r.Update(ctx, vnet); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// handleDeprovisioning manages the deprovisioning job lifecycle for a VirtualNetwork.
// It triggers deprovisioning if needed and polls job status until completion.
func (r *VirtualNetworkReconciler) handleDeprovisioning(ctx context.Context, vnet *v1alpha1.VirtualNetwork) (ctrl.Result, error) {
	if r.ProvisioningProvider == nil {
		ctrllog.FromContext(ctx).Info("no provisioning provider configured, skipping deprovisioning")
		return ctrl.Result{}, nil
	}

	// handleUpdate always stamps osacImplementationStrategyAnnotation before any
	// provisioning job is ever triggered, so by the time a job exists the annotation
	// is guaranteed present. A VirtualNetwork deleted before its first successful
	// handleUpdate reconcile (e.g. immediately after creation, before the dispatcher
	// resolved a manager) has neither: nothing was ever provisioned, and — unlike
	// Subnet's playbooks — the VirtualNetwork delete playbook indexes the annotation
	// directly with no spec-field fallback, so triggering a deprovision job here would
	// just fail with an undefined implementation_strategy. Skip deprovisioning
	// entirely in that case; there is nothing to tear down.
	if vnet.Annotations[osacImplementationStrategyAnnotation] == "" && len(vnet.Status.ProvisioningJobs) == 0 {
		ctrllog.FromContext(ctx).Info("no implementation-strategy annotation and no job history, skipping deprovisioning")
		return ctrl.Result{}, nil
	}

	result, done, err := provisioning.RunDeprovisioningLifecycle(ctx, r.ProvisioningProvider, vnet,
		&vnet.Status.ProvisioningJobs, r.MaxJobHistory, r.StatusPollInterval)
	if err != nil || !done {
		return result, err
	}
	return ctrl.Result{}, nil
}

// updateStatusWithRetry updates the virtual network status with retry on conflict.
func (r *VirtualNetworkReconciler) updateStatusWithRetry(ctx context.Context, key client.ObjectKey, newStatus v1alpha1.VirtualNetworkStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.VirtualNetwork{}
		if err := r.Get(ctx, key, latest); err != nil {
			return err
		}
		latest.Status = newStatus
		return r.Status().Update(ctx, latest)
	})
}

// NetworkingNamespacePredicate filters events by namespace for networking resources.
func NetworkingNamespacePredicate(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(
		func(obj client.Object) bool {
			return obj.GetNamespace() == namespace
		},
	)
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualNetworkReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mgr).
		For(&v1alpha1.VirtualNetwork{},
			mcbuilder.WithPredicates(NetworkingNamespacePredicate(r.NetworkingNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Complete(r)
}
