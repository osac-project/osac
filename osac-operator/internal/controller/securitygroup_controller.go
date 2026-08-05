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
	osacSecurityGroupFinalizer = "osac.openshift.io/securitygroup-finalizer"
)

// SecurityGroupReconciler reconciles a SecurityGroup object
type SecurityGroupReconciler struct {
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

// NewSecurityGroupReconciler creates a new reconciler for SecurityGroup resources.
func NewSecurityGroupReconciler(
	mgr mcmanager.Manager,
	networkingNamespace string,
	provisioningProvider provisioning.ProvisioningProvider,
	statusPollInterval time.Duration,
	maxJobHistory int,
	targetCluster mc.ClusterName,
) *SecurityGroupReconciler {
	if mgr == nil {
		panic("mgr must not be nil")
	}
	if statusPollInterval <= 0 {
		statusPollInterval = provisioning.DefaultStatusPollInterval
	}
	if maxJobHistory <= 0 {
		maxJobHistory = provisioning.DefaultMaxJobHistory
	}
	return &SecurityGroupReconciler{
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

// +kubebuilder:rbac:groups=osac.openshift.io,resources=securitygroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=securitygroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=securitygroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=virtualnetworks,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *SecurityGroupReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	sg := &v1alpha1.SecurityGroup{}
	err := r.Get(ctx, req.NamespacedName, sg)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	val, exists := sg.Annotations[osacManagementStateAnnotation]
	if sg.ObjectMeta.DeletionTimestamp.IsZero() && exists && val == ManagementStateUnmanaged {
		log.Info("ignoring SecurityGroup due to management-state annotation", "management-state", val)
		return ctrl.Result{}, nil
	}

	log.Info("start reconcile")

	oldstatus := sg.Status.DeepCopy()

	var res ctrl.Result
	if sg.ObjectMeta.DeletionTimestamp.IsZero() {
		res, err = r.handleUpdate(ctx, sg)
	} else {
		res, err = r.handleDelete(ctx, sg)
	}

	if !equality.Semantic.DeepEqual(sg.Status, *oldstatus) {
		log.Info("status requires update")
		if err := r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(sg), sg.Status); err != nil {
			return res, err
		}
	}

	log.Info("end reconcile")
	return res, err
}

func (r *SecurityGroupReconciler) handleUpdate(ctx context.Context, sg *v1alpha1.SecurityGroup) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// Add finalizer if not present
	if controllerutil.AddFinalizer(sg, osacSecurityGroupFinalizer) {
		if err := r.Update(ctx, sg); err != nil {
			return ctrl.Result{}, err
		}
		// Re-fetch so we have the latest resourceVersion and status
		if err := r.Get(ctx, client.ObjectKeyFromObject(sg), sg); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Set initial phase to Progressing
	if sg.Status.Phase == "" {
		sg.Status.Phase = v1alpha1.SecurityGroupPhaseProgressing
	}

	// Read implementation strategy from spec (set by fulfillment-service), fall back to default
	implementationStrategy := sg.Spec.ImplementationStrategy
	if implementationStrategy == "" {
		implementationStrategy = defaultSecurityGroupImplementationStrategy
	}

	// Add implementation-strategy annotation if not present or different
	// This allows AAP playbooks to select the appropriate role without doing lookups
	if sg.Annotations == nil {
		sg.Annotations = make(map[string]string)
	}
	if sg.Annotations[osacImplementationStrategyAnnotation] != implementationStrategy {
		sg.Annotations[osacImplementationStrategyAnnotation] = implementationStrategy
		log.Info("setting implementation-strategy annotation", "strategy", implementationStrategy)
		if err := r.Update(ctx, sg); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Compute desired config version from spec and inherited implementation strategy
	desiredVersion, err := provisioning.ComputeDesiredConfigVersion(struct {
		Spec                   v1alpha1.SecurityGroupSpec
		ImplementationStrategy string
	}{sg.Spec, implementationStrategy})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to compute desired config version: %w", err)
	}
	sg.Status.DesiredConfigVersion = desiredVersion

	// Set phase to Progressing only on first provision (empty phase) or when spec changed
	// after a previous success. Don't override Failed during backoff.
	if sg.Status.Phase == "" || (sg.Status.Phase == v1alpha1.SecurityGroupPhaseReady &&
		!provisioning.IsConfigApplied(&sg.Status.ProvisioningJobs, sg.Status.DesiredConfigVersion)) {
		sg.Status.Phase = v1alpha1.SecurityGroupPhaseProgressing
	}

	// Handle provisioning
	return r.handleProvisioning(ctx, sg)
}

func (r *SecurityGroupReconciler) handleDelete(ctx context.Context, sg *v1alpha1.SecurityGroup) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("deleting security group")

	sg.Status.Phase = v1alpha1.SecurityGroupPhaseDeleting

	// Finalizer already removed, cleanup complete
	if !controllerutil.ContainsFinalizer(sg, osacSecurityGroupFinalizer) {
		return ctrl.Result{}, nil
	}

	// Handle deprovisioning
	result, err := r.handleDeprovisioning(ctx, sg)
	if err != nil || result.RequeueAfter > 0 {
		return result, err
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(sg, osacSecurityGroupFinalizer)
	if err := r.Update(ctx, sg); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// handleProvisioning manages the provisioning job lifecycle for a SecurityGroup.
// Uses shared RunProvisioningLifecycle with config-version-based backoff on failure.
func (r *SecurityGroupReconciler) handleProvisioning(ctx context.Context, sg *v1alpha1.SecurityGroup) (ctrl.Result, error) {
	if r.ProvisioningProvider == nil {
		ctrllog.FromContext(ctx).Info("no provisioning provider configured, skipping provisioning")
		return ctrl.Result{}, nil
	}

	return provisioning.RunProvisioningLifecycle(ctx, r.ProvisioningProvider, sg,
		&provisioning.State{Jobs: &sg.Status.ProvisioningJobs, DesiredConfigVersion: sg.Status.DesiredConfigVersion},
		r.MaxJobHistory, r.StatusPollInterval,
		&provisioning.PollCallbacks{
			OnFailed: func(message string) {
				sg.Status.Phase = v1alpha1.SecurityGroupPhaseFailed
				setReadyConditionFailed(&sg.Status.Conditions, message)
			},
			OnSuccess: func(_ provisioning.ProvisionStatus) {
				sg.Status.Phase = v1alpha1.SecurityGroupPhaseReady
				setReadyConditionTrue(&sg.Status.Conditions)
			},
		},
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(ctx, r.APIReader, client.ObjectKeyFromObject(sg), &v1alpha1.SecurityGroup{}, func(obj client.Object) []v1alpha1.JobStatus {
				return obj.(*v1alpha1.SecurityGroup).Status.ProvisioningJobs
			})
		},
		func() error {
			return r.updateStatusWithRetry(ctx, client.ObjectKeyFromObject(sg), sg.Status)
		},
	)
}

// handleDeprovisioning manages the deprovisioning job lifecycle for a SecurityGroup.
// It triggers deprovisioning if needed and polls job status until completion.
func (r *SecurityGroupReconciler) handleDeprovisioning(ctx context.Context, sg *v1alpha1.SecurityGroup) (ctrl.Result, error) {
	if r.ProvisioningProvider == nil {
		ctrllog.FromContext(ctx).Info("no provisioning provider configured, skipping deprovisioning")
		return ctrl.Result{}, nil
	}
	result, done, err := provisioning.RunDeprovisioningLifecycle(ctx, r.ProvisioningProvider, sg,
		&sg.Status.ProvisioningJobs, r.MaxJobHistory, r.StatusPollInterval)
	if err != nil || !done {
		return result, err
	}
	return ctrl.Result{}, nil
}

// updateStatusWithRetry updates the security group status with retry on conflict.
func (r *SecurityGroupReconciler) updateStatusWithRetry(ctx context.Context, key client.ObjectKey, newStatus v1alpha1.SecurityGroupStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.SecurityGroup{}
		if err := r.Get(ctx, key, latest); err != nil {
			return err
		}
		latest.Status = newStatus
		return r.Status().Update(ctx, latest)
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityGroupReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mgr).
		For(&v1alpha1.SecurityGroup{},
			mcbuilder.WithPredicates(NetworkingNamespacePredicate(r.NetworkingNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Complete(r)
}
