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
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mc "sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const (
	// osacSubnetFinalizer is the finalizer for Subnet resources
	osacSubnetFinalizer = "osac.openshift.io/subnet-finalizer"
)

// SubnetReconciler reconciles a Subnet object
type SubnetReconciler struct {
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
}

// NewSubnetReconciler creates a new reconciler for Subnet resources.
func NewSubnetReconciler(
	mgr mcmanager.Manager,
	networkingNamespace string,
	provisioningProvider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration,
	maxJobHistory int,
	targetCluster mc.ClusterName,
) *SubnetReconciler {
	if mgr == nil {
		panic("mgr must not be nil")
	}
	if statusPollInterval <= 0 {
		statusPollInterval = provisioning.DefaultStatusPollInterval
	}
	if maxJobHistory <= 0 {
		maxJobHistory = provisioning.DefaultMaxJobHistory
	}
	return &SubnetReconciler{
		Client:               mgr.GetLocalManager().GetClient(),
		APIReader:            mgr.GetLocalManager().GetAPIReader(),
		Scheme:               mgr.GetLocalManager().GetScheme(),
		mgr:                  mgr,
		NetworkingNamespace:  networkingNamespace,
		ProvisioningProvider: provisioningProvider,
		StatusPollInterval:   statusPollInterval,
		MaxJobHistory:        maxJobHistory,
		targetCluster:        targetCluster,
	}
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=subnets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=subnets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=subnets/finalizers,verbs=update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=virtualnetworks,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *SubnetReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	subnet := &v1alpha1.Subnet{}
	err := r.Client.Get(ctx, req.NamespacedName, subnet)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	val, exists := subnet.Annotations[osacManagementStateAnnotation]
	if subnet.ObjectMeta.DeletionTimestamp.IsZero() && exists && val == ManagementStateUnmanaged {
		log.Info("ignoring Subnet due to management-state annotation", "management-state", val)
		return ctrl.Result{}, nil
	}

	log.Info("start reconcile")

	oldstatus := subnet.Status.DeepCopy()

	var res ctrl.Result
	if subnet.ObjectMeta.DeletionTimestamp.IsZero() {
		res, err = r.handleUpdate(ctx, subnet)
	} else {
		res, err = r.handleDelete(ctx, subnet)
	}

	if !equality.Semantic.DeepEqual(subnet.Status, *oldstatus) {
		log.Info("status requires update")
		if err := r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(subnet), subnet.Status); err != nil {
			return res, err
		}
	}

	log.Info("end reconcile")
	return res, err
}

// updateStatusWithRetry updates the subnet status with retry on conflict.
func (r *SubnetReconciler) updateStatusWithRetry(ctx context.Context, key client.ObjectKey, newStatus v1alpha1.SubnetStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.Subnet{}
		if err := r.Get(ctx, key, latest); err != nil {
			return err
		}
		latest.Status = newStatus
		return r.Status().Update(ctx, latest)
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *SubnetReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mgr).
		For(&v1alpha1.Subnet{},
			mcbuilder.WithPredicates(NetworkingNamespacePredicate(r.NetworkingNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Complete(r)
}

func (r *SubnetReconciler) handleUpdate(ctx context.Context, subnet *v1alpha1.Subnet) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// Add finalizer if not present
	if controllerutil.AddFinalizer(subnet, osacSubnetFinalizer) {
		if err := r.Update(ctx, subnet); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Set phase to Progressing only on first reconcile (empty phase).
	// Subsequent reconciles preserve the current phase — it gets updated
	// by OnSuccess/OnFailed callbacks in RunProvisioningLifecycle.
	if subnet.Status.Phase == "" {
		subnet.Status.Phase = v1alpha1.SubnetPhaseProgressing
	}

	// Get parent VirtualNetwork by UUID label to read implementation strategy
	vnetList := &v1alpha1.VirtualNetworkList{}
	err := r.List(ctx, vnetList,
		client.InNamespace(subnet.Namespace),
		client.MatchingLabels{osacVirtualNetworkIDLabel: subnet.Spec.VirtualNetwork},
	)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(vnetList.Items) == 0 {
		log.Info("parent VirtualNetwork not found, requeueing", "uuid", subnet.Spec.VirtualNetwork)
		return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	vnet := &vnetList.Items[0]

	// Read implementation strategy from parent VirtualNetwork spec
	implementationStrategy := vnet.Spec.ImplementationStrategy
	if implementationStrategy == "" {
		log.Info("implementation strategy not set on parent VirtualNetwork, requeueing", "virtualNetwork", vnet.Name)
		return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}

	// Add implementation-strategy annotation if not present or different
	// This allows AAP playbooks to select the appropriate role without doing lookups
	if subnet.Annotations == nil {
		subnet.Annotations = make(map[string]string)
	}
	if subnet.Annotations[osacImplementationStrategyAnnotation] != implementationStrategy {
		subnet.Annotations[osacImplementationStrategyAnnotation] = implementationStrategy
		log.Info("setting implementation-strategy annotation", "strategy", implementationStrategy)
		if err := r.Update(ctx, subnet); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Compute desired config version from spec and inherited implementation strategy
	desiredVersion, err := provisioning.ComputeDesiredConfigVersion(struct {
		Spec                   v1alpha1.SubnetSpec
		ImplementationStrategy string
	}{subnet.Spec, implementationStrategy})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to compute desired config version: %w", err)
	}
	subnet.Status.DesiredConfigVersion = desiredVersion

	// Set phase to Progressing only on first provision (empty phase) or when spec changed
	// after a previous success. Don't override Failed during backoff.
	if subnet.Status.Phase == "" || (subnet.Status.Phase == v1alpha1.SubnetPhaseReady &&
		!provisioning.IsConfigApplied(&subnet.Status.ProvisioningJobs, subnet.Status.DesiredConfigVersion)) {
		subnet.Status.Phase = v1alpha1.SubnetPhaseProgressing
	}

	// Handle provisioning
	return r.handleProvisioning(ctx, subnet)
}

func (r *SubnetReconciler) handleDelete(ctx context.Context, subnet *v1alpha1.Subnet) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("deleting subnet")

	subnet.Status.Phase = v1alpha1.SubnetPhaseDeleting

	// Base finalizer has already been removed, cleanup complete
	if !controllerutil.ContainsFinalizer(subnet, osacSubnetFinalizer) {
		return ctrl.Result{}, nil
	}

	// Handle deprovisioning
	result, err := r.handleDeprovisioning(ctx, subnet)
	if err != nil {
		return result, err
	}

	// If we need to requeue (jobs still running), do so
	if result.RequeueAfter > 0 {
		return result, nil
	}

	// Deprovisioning complete or skipped, remove base finalizer
	if controllerutil.RemoveFinalizer(subnet, osacSubnetFinalizer) {
		if err := r.Update(ctx, subnet); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// handleProvisioning manages the provisioning job lifecycle for a Subnet.
// Uses shared RunProvisioningLifecycle with config-version-based backoff on failure.
func (r *SubnetReconciler) handleProvisioning(ctx context.Context, subnet *v1alpha1.Subnet) (ctrl.Result, error) {
	if r.ProvisioningProvider == nil {
		ctrllog.FromContext(ctx).Info("no provisioning provider configured, skipping provisioning")
		return ctrl.Result{}, nil
	}

	return provisioning.RunProvisioningLifecycle(ctx, r.ProvisioningProvider, subnet,
		&provisioning.State{Jobs: &subnet.Status.ProvisioningJobs, DesiredConfigVersion: subnet.Status.DesiredConfigVersion},
		r.MaxJobHistory, r.StatusPollInterval,
		&provisioning.PollCallbacks{
			OnFailed: func(message string) {
				subnet.Status.Phase = v1alpha1.SubnetPhaseFailed
				setReadyConditionFailed(&subnet.Status.Conditions, message)
			},
			OnSuccess: func(_ provisioning.ProvisionStatus) {
				subnet.Status.Phase = v1alpha1.SubnetPhaseReady
				setReadyConditionTrue(&subnet.Status.Conditions)
			},
		},
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(ctx, r.APIReader, client.ObjectKeyFromObject(subnet), &v1alpha1.Subnet{}, func(obj client.Object) []v1alpha1.JobStatus { return obj.(*v1alpha1.Subnet).Status.ProvisioningJobs })
		},
		func() error {
			return r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(subnet), subnet.Status)
		},
	)
}

// handleDeprovisioning manages the deprovisioning job lifecycle for a Subnet.
// It triggers deprovisioning if needed and polls job status until completion.
func (r *SubnetReconciler) handleDeprovisioning(ctx context.Context, subnet *v1alpha1.Subnet) (ctrl.Result, error) {
	if r.ProvisioningProvider == nil {
		ctrllog.FromContext(ctx).Info("no provisioning provider configured, skipping deprovisioning")
		return ctrl.Result{}, nil
	}
	result, done, err := provisioning.RunDeprovisioningLifecycle(ctx, r.ProvisioningProvider, subnet,
		&subnet.Status.ProvisioningJobs, r.MaxJobHistory, r.StatusPollInterval)
	if err != nil || !done {
		return result, err
	}
	return ctrl.Result{}, nil
}
