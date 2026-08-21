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
	osacNATGatewayFinalizer = "osac.openshift.io/natgateway-finalizer"
)

// NATGatewayReconciler reconciles a NATGateway object
type NATGatewayReconciler struct {
	client.Client
	APIReader            client.Reader
	Scheme               *runtime.Scheme
	mgr                  mcmanager.Manager
	NetworkingNamespace  string
	ProvisioningProvider provisioning.ProvisioningProvider
	StatusPollInterval   time.Duration
	MaxJobHistory        int
	targetCluster        mc.ClusterName
}

// NewNATGatewayReconciler creates a new reconciler for NATGateway resources.
func NewNATGatewayReconciler(
	mgr mcmanager.Manager,
	networkingNamespace string,
	provisioningProvider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration,
	maxJobHistory int,
	targetCluster mc.ClusterName,
) *NATGatewayReconciler {
	if mgr == nil {
		panic("mgr must not be nil")
	}
	if statusPollInterval <= 0 {
		statusPollInterval = provisioning.DefaultStatusPollInterval
	}
	if maxJobHistory <= 0 {
		maxJobHistory = provisioning.DefaultMaxJobHistory
	}
	return &NATGatewayReconciler{
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

// +kubebuilder:rbac:groups=osac.openshift.io,resources=natgateways,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=natgateways/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=natgateways/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *NATGatewayReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	natgw := &v1alpha1.NATGateway{}
	err := r.Client.Get(ctx, req.NamespacedName, natgw)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	val, exists := natgw.Annotations[osacManagementStateAnnotation]
	if natgw.ObjectMeta.DeletionTimestamp.IsZero() && exists && val == ManagementStateUnmanaged {
		log.Info("ignoring NATGateway due to management-state annotation", "management-state", val)
		return ctrl.Result{}, nil
	}

	log.Info("start reconcile")

	oldstatus := natgw.Status.DeepCopy()

	var res ctrl.Result
	if natgw.ObjectMeta.DeletionTimestamp.IsZero() {
		res, err = r.handleUpdate(ctx, natgw)
	} else {
		res, err = r.handleDelete(ctx, natgw)
	}

	if !equality.Semantic.DeepEqual(natgw.Status, *oldstatus) {
		log.Info("status requires update")
		if err := r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(natgw), natgw.Status); err != nil {
			return res, err
		}
	}

	log.Info("end reconcile")
	return res, err
}

// updateStatusWithRetry updates the NATGateway status with retry on conflict.
func (r *NATGatewayReconciler) updateStatusWithRetry(ctx context.Context, key client.ObjectKey, newStatus v1alpha1.NATGatewayStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.NATGateway{}
		if err := r.Get(ctx, key, latest); err != nil {
			return err
		}
		latest.Status = newStatus
		return r.Status().Update(ctx, latest)
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *NATGatewayReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mgr).
		For(&v1alpha1.NATGateway{},
			mcbuilder.WithPredicates(NetworkingNamespacePredicate(r.NetworkingNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Complete(r)
}

func (r *NATGatewayReconciler) handleUpdate(ctx context.Context, natgw *v1alpha1.NATGateway) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// Add finalizer if not present
	if controllerutil.AddFinalizer(natgw, osacNATGatewayFinalizer) {
		if err := r.Update(ctx, natgw); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Set phase to Progressing only on first reconcile (empty phase).
	if natgw.Status.Phase == "" {
		natgw.Status.Phase = v1alpha1.NATGatewayPhaseProgressing
	}

	// Get parent VirtualNetwork by UUID label to read implementation strategy
	vnetList := &v1alpha1.VirtualNetworkList{}
	err := r.List(ctx, vnetList,
		client.InNamespace(natgw.Namespace),
		client.MatchingLabels{osacVirtualNetworkIDLabel: natgw.Spec.VirtualNetwork},
	)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(vnetList.Items) == 0 {
		log.Info("parent VirtualNetwork not found, requeueing", "uuid", natgw.Spec.VirtualNetwork)
		return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	vnet := &vnetList.Items[0]

	// Read implementation strategy from the annotation the parent VirtualNetwork's own
	// controller has already resolved and written onto it.
	implementationStrategy := vnet.Annotations[osacImplementationStrategyAnnotation]
	if implementationStrategy == "" {
		log.Info("implementation strategy not set on parent VirtualNetwork, requeueing", "virtualNetwork", vnet.Name)
		return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}

	// Add implementation-strategy annotation if not present or different
	if natgw.Annotations == nil {
		natgw.Annotations = make(map[string]string)
	}
	if natgw.Annotations[osacImplementationStrategyAnnotation] != implementationStrategy {
		natgw.Annotations[osacImplementationStrategyAnnotation] = implementationStrategy
		log.Info("setting implementation-strategy annotation", "strategy", implementationStrategy)
		if err := r.Update(ctx, natgw); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Compute desired config version from spec and inherited implementation strategy
	desiredVersion, err := provisioning.ComputeDesiredConfigVersion(struct {
		Spec                   v1alpha1.NATGatewaySpec
		ImplementationStrategy string
	}{natgw.Spec, implementationStrategy})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to compute desired config version: %w", err)
	}
	natgw.Status.DesiredConfigVersion = desiredVersion

	// Set phase to Progressing only on first provision (empty phase) or when spec changed
	// after a previous success. Don't override Failed during backoff.
	if natgw.Status.Phase == "" || (natgw.Status.Phase == v1alpha1.NATGatewayPhaseReady &&
		!provisioning.IsConfigApplied(&natgw.Status.ProvisioningJobs, natgw.Status.DesiredConfigVersion)) {
		natgw.Status.Phase = v1alpha1.NATGatewayPhaseProgressing
	}

	return r.handleProvisioning(ctx, natgw)
}

func (r *NATGatewayReconciler) handleDelete(ctx context.Context, natgw *v1alpha1.NATGateway) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("deleting NATGateway")

	natgw.Status.Phase = v1alpha1.NATGatewayPhaseDeleting

	if !controllerutil.ContainsFinalizer(natgw, osacNATGatewayFinalizer) {
		return ctrl.Result{}, nil
	}

	result, err := r.handleDeprovisioning(ctx, natgw)
	if err != nil {
		return result, err
	}

	if result.RequeueAfter > 0 {
		return result, nil
	}

	if controllerutil.RemoveFinalizer(natgw, osacNATGatewayFinalizer) {
		if err := r.Update(ctx, natgw); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *NATGatewayReconciler) handleProvisioning(ctx context.Context, natgw *v1alpha1.NATGateway) (ctrl.Result, error) {
	if r.ProvisioningProvider == nil {
		ctrllog.FromContext(ctx).Info("no provisioning provider configured, skipping provisioning")
		return ctrl.Result{}, nil
	}

	return provisioning.RunProvisioningLifecycle(ctx, r.ProvisioningProvider, natgw,
		&provisioning.State{Jobs: &natgw.Status.ProvisioningJobs, DesiredConfigVersion: natgw.Status.DesiredConfigVersion},
		r.MaxJobHistory, r.StatusPollInterval,
		&provisioning.PollCallbacks{
			OnFailed: func(message string) {
				natgw.Status.Phase = v1alpha1.NATGatewayPhaseFailed
				setReadyConditionFailed(&natgw.Status.Conditions, message)
			},
			OnSuccess: func(_ provisioning.ProvisionStatus) {
				natgw.Status.Phase = v1alpha1.NATGatewayPhaseReady
				setReadyConditionTrue(&natgw.Status.Conditions)
			},
		},
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(ctx, r.APIReader, client.ObjectKeyFromObject(natgw), &v1alpha1.NATGateway{}, func(obj client.Object) []v1alpha1.JobStatus {
				return obj.(*v1alpha1.NATGateway).Status.ProvisioningJobs
			})
		},
		func() error {
			return r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(natgw), natgw.Status)
		},
	)
}

func (r *NATGatewayReconciler) handleDeprovisioning(ctx context.Context, natgw *v1alpha1.NATGateway) (ctrl.Result, error) {
	if r.ProvisioningProvider == nil {
		ctrllog.FromContext(ctx).Info("no provisioning provider configured, skipping deprovisioning")
		return ctrl.Result{}, nil
	}
	result, done, err := provisioning.RunDeprovisioningLifecycle(ctx, r.ProvisioningProvider, natgw,
		&natgw.Status.ProvisioningJobs, r.MaxJobHistory, r.StatusPollInterval)
	if err != nil || !done {
		return result, err
	}
	return ctrl.Result{}, nil
}
