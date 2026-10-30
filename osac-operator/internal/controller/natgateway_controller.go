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
	"errors"
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
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
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
	Resolver             *dispatcher.Resolver
	// NetworkProvisioningEnabled controls whether the controller dispatches AAP
	// provisioning jobs. When false, resources are set to Ready immediately.
	NetworkProvisioningEnabled bool
}

// NewNATGatewayReconciler creates a new reconciler for NATGateway resources.
func NewNATGatewayReconciler(
	mgr mcmanager.Manager,
	networkingNamespace string,
	provisioningProvider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration,
	maxJobHistory int,
	targetCluster mc.ClusterName,
	resolver *dispatcher.Resolver,
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
		Resolver:             resolver,
	}
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=natgateways,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=natgateways/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=natgateways/finalizers,verbs=update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=virtualnetworks,verbs=get;list;watch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=externalips,verbs=get;list;watch

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
	hadFinalizer := controllerutil.ContainsFinalizer(natgw, osacNATGatewayFinalizer)

	var res ctrl.Result
	if natgw.ObjectMeta.DeletionTimestamp.IsZero() {
		res, err = r.handleUpdate(ctx, natgw)
	} else {
		res, err = r.handleDelete(ctx, natgw)
	}

	statusPersistedBeforeFinalizerRemoval := !natgw.ObjectMeta.DeletionTimestamp.IsZero() &&
		hadFinalizer && !controllerutil.ContainsFinalizer(natgw, osacNATGatewayFinalizer)
	if !statusPersistedBeforeFinalizerRemoval && !equality.Semantic.DeepEqual(natgw.Status, *oldstatus) {
		log.Info("status requires update")
		if err := r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(natgw), string(natgw.UID), natgw.Generation, natgw.Status); err != nil {
			return res, err
		}
	}

	log.Info("end reconcile")
	return res, err
}

// updateStatusWithRetry updates the NATGateway status with retry on conflict.
func (r *NATGatewayReconciler) updateStatusWithRetry(ctx context.Context, key client.ObjectKey, expectedUID string, expectedGeneration int64, newStatus v1alpha1.NATGatewayStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.NATGateway{}
		if err := r.Get(ctx, key, latest); err != nil {
			return err
		}
		if string(latest.UID) != expectedUID || latest.Generation != expectedGeneration {
			return fmt.Errorf("NATGateway changed while status was being updated")
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

func (r *NATGatewayReconciler) resolveNATGatewayUpdateDependencies(
	ctx context.Context,
	natgw *v1alpha1.NATGateway,
) (*natGatewayUpdateDependencies, ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	vnetList := &v1alpha1.VirtualNetworkList{}
	if err := r.List(ctx, vnetList,
		client.InNamespace(natgw.Namespace),
		client.MatchingLabels{osacVirtualNetworkIDLabel: natgw.Spec.VirtualNetwork},
	); err != nil {
		return nil, ctrl.Result{}, err
	}
	if len(vnetList.Items) == 0 {
		log.Info("parent VirtualNetwork not found, requeueing", "uuid", natgw.Spec.VirtualNetwork)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	if len(vnetList.Items) != 1 {
		return nil, ctrl.Result{}, fmt.Errorf("expected exactly one parent VirtualNetwork with uuid %q but found %d", natgw.Spec.VirtualNetwork, len(vnetList.Items))
	}
	vnet := &vnetList.Items[0]
	if vnet.Status.Phase != v1alpha1.VirtualNetworkPhaseReady {
		log.Info("parent VirtualNetwork not Ready yet, requeueing",
			"virtualNetwork", vnet.Name, "phase", vnet.Status.Phase)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}

	externalIPList := &v1alpha1.ExternalIPList{}
	if err := r.List(ctx, externalIPList,
		client.InNamespace(natgw.Namespace),
		client.MatchingLabels{osacExternalIPIDLabel: natgw.Spec.ExternalIP},
	); err != nil {
		return nil, ctrl.Result{}, err
	}
	if len(externalIPList.Items) == 0 {
		log.Info("ExternalIP not found, requeueing", "uuid", natgw.Spec.ExternalIP)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}
	if len(externalIPList.Items) != 1 {
		return nil, ctrl.Result{}, fmt.Errorf("expected exactly one ExternalIP with uuid %q but found %d", natgw.Spec.ExternalIP, len(externalIPList.Items))
	}
	externalIP := &externalIPList.Items[0]
	if externalIP.Status.Address == "" {
		log.Info("ExternalIP address not allocated yet, requeueing", "externalIP", externalIP.Name)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}

	implementationStrategy, contractVersion, err := resolveNATGatewayManager(
		ctx,
		r.Resolver,
		vnet.Spec.NetworkClass,
		vnet.Annotations[osacImplementationStrategyAnnotation],
		natgw.Annotations[osacImplementationStrategyAnnotation],
		natgw.Annotations[osacNATGatewayContractVersionAnnotation],
	)
	if err != nil {
		if errors.Is(err, errNATGatewayContract) {
			setNATGatewayPhase(&natgw.Status, v1alpha1.NATGatewayPhaseFailed)
			setReadyConditionFailed(&natgw.Status.Conditions, err.Error())
			return nil, ctrl.Result{}, nil
		}
		return nil, ctrl.Result{}, err
	}
	if implementationStrategy == "" {
		log.Info("implementation strategy not set on parent VirtualNetwork, requeueing", "virtualNetwork", vnet.Name)
		return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
	}

	dependencies := &natGatewayUpdateDependencies{
		virtualNetwork:         vnet,
		externalIP:             externalIP,
		implementationStrategy: implementationStrategy,
		contractVersion:        contractVersion,
	}
	if contractVersion == networkmanager.NATGatewayContractVersionV1 {
		natGatewayContext, pending, err := buildNATGatewayContext(natgw, vnet, externalIP)
		if err != nil {
			setNATGatewayPhase(&natgw.Status, v1alpha1.NATGatewayPhaseFailed)
			setReadyConditionFailed(&natgw.Status.Conditions, err.Error())
			return nil, ctrl.Result{}, nil
		}
		if pending {
			return nil, ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
		}
		dependencies.context = natGatewayContext
	}
	return dependencies, ctrl.Result{}, nil
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
		setNATGatewayPhase(&natgw.Status, v1alpha1.NATGatewayPhaseProgressing)
	}

	// When networking provisioning is disabled, skip AAP job dispatch and set Ready
	// immediately.
	if !r.NetworkProvisioningEnabled {
		setNATGatewayPhase(&natgw.Status, v1alpha1.NATGatewayPhaseReady)
		setReadyConditionTrue(&natgw.Status.Conditions)
		return ctrl.Result{}, nil
	}

	dependencies, result, err := r.resolveNATGatewayUpdateDependencies(ctx, natgw)
	if err != nil || result.RequeueAfter > 0 {
		return result, err
	}
	if dependencies == nil {
		return ctrl.Result{}, nil
	}
	vnet := dependencies.virtualNetwork
	externalIP := dependencies.externalIP
	implementationStrategy := dependencies.implementationStrategy
	contractVersion := dependencies.contractVersion
	natGatewayContext := dependencies.context

	// Add implementation-strategy annotation if not present or different
	if natgw.Annotations == nil {
		natgw.Annotations = make(map[string]string)
	}
	annotationsChanged := false
	if natgw.Annotations[osacImplementationStrategyAnnotation] != implementationStrategy {
		natgw.Annotations[osacImplementationStrategyAnnotation] = implementationStrategy
		log.Info("setting implementation-strategy annotation", "strategy", implementationStrategy)
		annotationsChanged = true
	}
	if contractVersion == networkmanager.NATGatewayContractVersionV1 {
		if natgw.Annotations[osacNATGatewayContractVersionAnnotation] != contractVersion {
			natgw.Annotations[osacNATGatewayContractVersionAnnotation] = contractVersion
			annotationsChanged = true
		}
	} else {
		// The unversioned manager contract uses these legacy resource annotations.
		if natgw.Annotations[osacVirtualNetworkNameAnnotation] != vnet.Name {
			natgw.Annotations[osacVirtualNetworkNameAnnotation] = vnet.Name
			annotationsChanged = true
		}
		if natgw.Annotations[osacExternalIPNameAnnotation] != externalIP.Name {
			natgw.Annotations[osacExternalIPNameAnnotation] = externalIP.Name
			annotationsChanged = true
		}
		if natgw.Annotations[osacVNIPv4CIDRAnnotation] != vnet.Spec.IPv4CIDR {
			natgw.Annotations[osacVNIPv4CIDRAnnotation] = vnet.Spec.IPv4CIDR
			annotationsChanged = true
		}
	}
	if annotationsChanged {
		if err := r.Update(ctx, natgw); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	var desiredVersion string
	if contractVersion == networkmanager.NATGatewayContractVersionV1 {
		ctx = provisioning.WithNATGatewayContext(ctx, natGatewayContext)
		desiredVersion, err = provisioning.ComputeDesiredConfigVersion(struct {
			Spec                   v1alpha1.NATGatewaySpec
			ImplementationStrategy string
			ContractVersion        string
			Context                provisioning.NATGatewayContext
		}{natgw.Spec, implementationStrategy, contractVersion, natGatewayContext})
	} else {
		desiredVersion, err = provisioning.ComputeDesiredConfigVersion(struct {
			Spec                   v1alpha1.NATGatewaySpec
			ImplementationStrategy string
		}{natgw.Spec, implementationStrategy})
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to compute desired config version: %w", err)
	}
	natgw.Status.DesiredConfigVersion = desiredVersion

	// Set phase to Progressing only on first provision (empty phase) or when spec changed
	// after a previous success. Don't override Failed during backoff.
	if natgw.Status.Phase == "" || (natgw.Status.Phase == v1alpha1.NATGatewayPhaseReady &&
		!provisioning.IsConfigApplied(&natgw.Status.ProvisioningJobs, natgw.Status.DesiredConfigVersion)) {
		setNATGatewayPhase(&natgw.Status, v1alpha1.NATGatewayPhaseProgressing)
	}

	return r.handleProvisioning(ctx, natgw)
}

func (r *NATGatewayReconciler) handleDelete(ctx context.Context, natgw *v1alpha1.NATGateway) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("deleting NATGateway")

	statusChanged := setNATGatewayPhase(&natgw.Status, v1alpha1.NATGatewayPhaseDeleting)

	if !controllerutil.ContainsFinalizer(natgw, osacNATGatewayFinalizer) {
		return ctrl.Result{}, nil
	}
	if statusChanged {
		if err := r.Status().Update(ctx, natgw); err != nil {
			return ctrl.Result{}, err
		}
		latest := &v1alpha1.NATGateway{}
		if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(natgw), latest); err != nil {
			return ctrl.Result{}, err
		}
		*natgw = *latest
	}

	if natgw.Annotations[osacImplementationStrategyAnnotation] == "" {
		log.Info("skipping deprovisioning — resource was never provisioned")
	} else {
		result, err := r.handleDeprovisioning(ctx, natgw)
		if err != nil {
			return result, err
		}

		if result.RequeueAfter > 0 {
			return result, nil
		}
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

	callbacks := &provisioning.PollCallbacks{
		OnFailed: func(message string) {
			setNATGatewayPhase(&natgw.Status, v1alpha1.NATGatewayPhaseFailed)
			setReadyConditionFailed(&natgw.Status.Conditions, message)
		},
		OnSuccess: func(_ provisioning.ProvisionStatus) {
			setNATGatewayPhase(&natgw.Status, v1alpha1.NATGatewayPhaseReady)
			setReadyConditionTrue(&natgw.Status.Conditions)
		},
	}
	if natgw.Annotations[osacNATGatewayContractVersionAnnotation] == networkmanager.NATGatewayContractVersionV1 {
		expectedContext := provisioning.NATGatewayContextFromContext(ctx)
		callbacks.OnSuccessWithExtraVars = func(status provisioning.ProvisionStatusWithExtraVars) error {
			return r.validateNATGatewayProvisionResult(ctx, natgw, expectedContext, status)
		}
		callbacks.OnOutputError = func(message string) {
			failNATGatewayProvisionAttempt(natgw, message)
		}
	}

	return provisioning.RunProvisioningLifecycle(ctx, r.ProvisioningProvider, natgw,
		&provisioning.State{Jobs: &natgw.Status.ProvisioningJobs, DesiredConfigVersion: natgw.Status.DesiredConfigVersion},
		r.MaxJobHistory, r.StatusPollInterval,
		callbacks,
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(ctx, r.APIReader, client.ObjectKeyFromObject(natgw), &v1alpha1.NATGateway{}, func(obj client.Object) []v1alpha1.JobStatus {
				return obj.(*v1alpha1.NATGateway).Status.ProvisioningJobs
			})
		},
		func() error {
			return r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(natgw), string(natgw.UID), natgw.Generation, natgw.Status)
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
	if natgw.Annotations[osacNATGatewayContractVersionAnnotation] == networkmanager.NATGatewayContractVersionV1 {
		if err := r.validateNATGatewayDeprovisionResult(ctx, natgw); err != nil {
			failNATGatewayDeprovisionAttempt(natgw, err.Error())
			return ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
		}
	}
	return ctrl.Result{}, nil
}
