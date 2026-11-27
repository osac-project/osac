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
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const (
	csiInstallControllerName          = "clusterorder-csi-install"
	csiInstallKubeconfigReason        = "CSIInstallKubeconfigUnavailable"
	csiInstallCancelFailedReason      = "CSIInstallCancellationFailed"
	csiInstallCancelUnavailableReason = "CSIInstallCancellationUnavailable"
)

// CSIInstallReconciler launches the job that installs the OSAC CSI driver for
// each Ready ClusterOrder and reports its outcome in the CSIDriverReady
// condition. It does not own the main ClusterOrder provisioning lifecycle and
// adds no finalizer: a deletion is seen while the ClusterOrder controller's
// finalizer holds the object, and only cancels a running install job.
// +kubebuilder:rbac:groups=osac.openshift.io,resources=clusterorders,verbs=get;list;watch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=clusterorders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedcontrolplanes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
type CSIInstallReconciler struct {
	client.Client
	apiReader             client.Reader
	ClusterOrderNamespace string
	ProvisioningProvider  provisioning.ProvisioningProvider
	StatusPollInterval    time.Duration
	MaxJobHistory         int
	// Enabled turns job launches on. When false, the reconciler writes
	// nothing and only cancels install jobs of deleted ClusterOrders.
	Enabled            bool
	Recorder           events.EventRecorder
	getAdminKubeconfig func(context.Context, *v1alpha1.ClusterOrder) ([]byte, error)
}

// NewCSIInstallReconciler creates a reconciler for CSI install jobs.
func NewCSIInstallReconciler(
	k8sClient client.Client,
	apiReader client.Reader,
	namespace string,
	provider provisioning.ProvisioningProvider,
	pollInterval time.Duration,
	enabled bool,
) *CSIInstallReconciler {
	if namespace == "" {
		namespace = defaultClusterOrderNamespace
	}
	if pollInterval <= 0 {
		pollInterval = provisioning.DefaultStatusPollInterval
	}
	reconciler := &CSIInstallReconciler{
		Client:                k8sClient,
		apiReader:             apiReader,
		ClusterOrderNamespace: namespace,
		ProvisioningProvider:  provider,
		StatusPollInterval:    pollInterval,
		MaxJobHistory:         provisioning.DefaultMaxJobHistory,
		Enabled:               enabled,
	}
	reconciler.getAdminKubeconfig = func(ctx context.Context, clusterOrder *v1alpha1.ClusterOrder) ([]byte, error) {
		return hostedClusterAdminKubeconfig(ctx, reconciler.apiReader, clusterOrder)
	}
	return reconciler
}

// SetupWithManager registers the namespaced ClusterOrder controller.
func (r *CSIInstallReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}
	if r.Recorder == nil {
		r.Recorder = localMgr.GetEventRecorder(csiInstallControllerName)
	}

	return ctrl.NewControllerManagedBy(localMgr).
		Named(csiInstallControllerName).
		For(&v1alpha1.ClusterOrder{}, builder.WithPredicates(NamespacePredicate(r.ClusterOrderNamespace))).
		Complete(r)
}

// Reconcile launches and tracks the CSI install job of a ClusterOrder.
func (r *CSIInstallReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	instance := &v1alpha1.ClusterOrder{}
	if err := r.apiReader.Get(ctx, request.NamespacedName, instance); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !instance.DeletionTimestamp.IsZero() {
		r.cancelInstallJobs(ctx, instance)
		return ctrl.Result{}, nil
	}
	if !r.Enabled {
		return ctrl.Result{}, nil
	}
	managementState := instance.Annotations[osacManagementStateAnnotation]
	if managementState == ManagementStateUnmanaged || managementState == ManagementStateManual {
		return ctrl.Result{}, nil
	}
	if instance.Status.Phase != v1alpha1.ClusterOrderPhaseReady {
		return ctrl.Result{}, nil
	}
	return r.reconcileInstall(ctx, instance)
}

// cancelInstallJobs cancels every non-terminal install job. Failures are
// reported as events and never block the ClusterOrder deletion.
func (r *CSIInstallReconciler) cancelInstallJobs(ctx context.Context, instance *v1alpha1.ClusterOrder) {
	log := ctrllog.FromContext(ctx)
	canceler, canCancel := r.ProvisioningProvider.(provisioning.ProvisioningJobCanceler)
	canceled := make(map[string]struct{})
	for _, job := range instance.Status.CSIInstallJobs {
		if job.JobID == "" || job.State.IsTerminal() {
			continue
		}
		if _, seen := canceled[job.JobID]; seen {
			continue
		}
		canceled[job.JobID] = struct{}{}
		if !canCancel {
			log.Info("CSI install job cannot be cancelled during deletion", "jobID", job.JobID)
			r.recordWarningEvent(instance, csiInstallCancelUnavailableReason, "CancelCSIInstall",
				fmt.Sprintf("No cancellation mechanism is available for CSI install job %s", job.JobID))
			continue
		}
		if err := canceler.CancelJob(ctx, job.JobID); err != nil {
			log.Error(err, "failed to cancel CSI install job during deletion; continuing", "jobID", job.JobID)
			r.recordWarningEvent(instance, csiInstallCancelFailedReason, "CancelCSIInstall",
				fmt.Sprintf("Failed to cancel CSI install job %s: %v", job.JobID, err))
		}
	}
}

func (r *CSIInstallReconciler) reconcileInstall(ctx context.Context, instance *v1alpha1.ClusterOrder) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	key := client.ObjectKeyFromObject(instance)
	originalJobs := slices.Clone(instance.Status.CSIInstallJobs)
	originalCondition := findCSIDriverReadyCondition(instance)
	provState := &provisioning.State{Jobs: &instance.Status.CSIInstallJobs}

	jobCtx := ctx
	if csiInstallLaunchDue(instance.Status.CSIInstallJobs) {
		kubeconfig, err := r.getAdminKubeconfig(ctx, instance)
		if err != nil {
			log.Error(err, "failed to get the hosted cluster kubeconfig for the CSI install job")
			r.recordWarningEvent(instance, csiInstallKubeconfigReason, "ProvisionCSIInstall",
				fmt.Sprintf("CSI install is waiting for a usable hosted-cluster kubeconfig: %v", err))
			return ctrl.Result{RequeueAfter: r.StatusPollInterval}, nil
		}
		if len(kubeconfig) == 0 {
			if provisioning.FindLatestJobByType(instance.Status.CSIInstallJobs, v1alpha1.JobTypeProvision) == nil {
				setCSIDriverReadyCondition(instance, metav1.ConditionFalse, v1alpha1.ClusterOrderReasonCSIDriverInstalling,
					"Waiting for the hosted cluster kubeconfig")
			}
			return r.finishInstall(ctx, key, instance, originalJobs, originalCondition, ctrl.Result{RequeueAfter: r.StatusPollInterval})
		}
		jobCtx = provisioning.WithAdminKubeconfig(ctx, string(kubeconfig))
	}

	// After a failed launch, wait out its backoff before calling AAP again,
	// however often other controllers write the ClusterOrder.
	if isCSIInstallLaunchFailure(provisioning.FindLatestJobByType(instance.Status.CSIInstallJobs, v1alpha1.JobTypeProvision)) &&
		!csiInstallLaunchDue(instance.Status.CSIInstallJobs) {
		updateCSIDriverReadyCondition(instance)
		return r.finishInstall(ctx, key, instance, originalJobs, originalCondition,
			ctrl.Result{RequeueAfter: csiInstallRetryAfter(instance.Status.CSIInstallJobs)})
	}

	result, err := provisioning.RunProvisioningLifecycle(
		jobCtx, r.ProvisioningProvider, instance, provState,
		r.MaxJobHistory, r.StatusPollInterval, nil,
		func() bool {
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(ctx, r.apiReader, key, &v1alpha1.ClusterOrder{},
				func(obj client.Object) []v1alpha1.JobStatus {
					return obj.(*v1alpha1.ClusterOrder).Status.CSIInstallJobs
				})
		},
		func() error {
			updateCSIDriverReadyCondition(instance)
			return r.patchInstallStatus(ctx, key, instance)
		},
	)
	if err != nil {
		log.Error(err, "failed to launch the CSI install job")
		// Record the failed launch, with no job ID, so its backoff applies
		// to the next attempt as it would after a failed job.
		message := truncateAddOnOperatorMessage(fmt.Sprintf("Failed to launch the CSI install job: %v", err))
		instance.Status.CSIInstallJobs = provisioning.AppendJob(instance.Status.CSIInstallJobs, v1alpha1.JobStatus{
			Type:          v1alpha1.JobTypeProvision,
			Timestamp:     metav1.Now(),
			State:         v1alpha1.JobStateFailed,
			Message:       message,
			ConfigVersion: provState.DesiredConfigVersion,
		}, r.MaxJobHistory)
		setCSIDriverReadyCondition(instance, metav1.ConditionFalse, v1alpha1.ClusterOrderReasonCSIDriverInstallFailed, message)
		return r.finishInstall(ctx, key, instance, originalJobs, originalCondition,
			ctrl.Result{RequeueAfter: csiInstallRetryAfter(instance.Status.CSIInstallJobs)})
	}

	updateCSIDriverReadyCondition(instance)
	if result.IsZero() {
		result.RequeueAfter = csiInstallRetryAfter(instance.Status.CSIInstallJobs)
	}
	return r.finishInstall(ctx, key, instance, originalJobs, originalCondition, result)
}

// finishInstall persists the install status when this reconcile changed it.
// An unchanged snapshot is never written back, so a lagging read cannot
// overwrite newer job history.
func (r *CSIInstallReconciler) finishInstall(
	ctx context.Context,
	key client.ObjectKey,
	instance *v1alpha1.ClusterOrder,
	originalJobs []v1alpha1.JobStatus,
	originalCondition *metav1.Condition,
	result ctrl.Result,
) (ctrl.Result, error) {
	if equality.Semantic.DeepEqual(originalJobs, instance.Status.CSIInstallJobs) &&
		equality.Semantic.DeepEqual(originalCondition, findCSIDriverReadyCondition(instance)) {
		return result, nil
	}
	if err := r.patchInstallStatus(ctx, key, instance); err != nil {
		return ctrl.Result{}, err
	}
	return result, nil
}

// patchInstallStatus writes the install jobs and the CSIDriverReady condition
// with an optimistic-lock merge patch, leaving other status fields alone.
func (r *CSIInstallReconciler) patchInstallStatus(ctx context.Context, key client.ObjectKey, instance *v1alpha1.ClusterOrder) error {
	condition := findCSIDriverReadyCondition(instance)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.ClusterOrder{}
		if err := r.apiReader.Get(ctx, key, latest); err != nil {
			return err
		}
		base := latest.DeepCopy()
		latest.Status.CSIInstallJobs = slices.Clone(instance.Status.CSIInstallJobs)
		if condition != nil {
			apimeta.SetStatusCondition(&latest.Status.Conditions, *condition)
		}
		if equality.Semantic.DeepEqual(base.Status, latest.Status) {
			return nil
		}
		return r.Status().Patch(ctx, latest, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	})
}

func (r *CSIInstallReconciler) recordWarningEvent(instance *v1alpha1.ClusterOrder, reason, action, message string) {
	if r.Recorder != nil {
		r.Recorder.Eventf(instance, nil, corev1.EventTypeWarning, reason, action, "%s", message)
	}
}

// isCSIInstallLaunchFailure reports whether job records a launch that failed
// before AAP created a job: a Failed entry with no job ID.
func isCSIInstallLaunchFailure(job *v1alpha1.JobStatus) bool {
	return job != nil && job.JobID == "" && job.State == v1alpha1.JobStateFailed
}

// csiInstallFailed reports whether job is a failed launch or a failed job.
func csiInstallFailed(job *v1alpha1.JobStatus) bool {
	if isCSIInstallLaunchFailure(job) {
		return true
	}
	return provisioning.HasJobID(job) && job.State.IsTerminal() && !job.State.IsSuccessful()
}

// csiInstallLaunchDue reports whether the next lifecycle step may launch a
// job: there is none yet, or the latest launch or job failed and its backoff
// elapsed.
func csiInstallLaunchDue(jobs []v1alpha1.JobStatus) bool {
	latest := provisioning.FindLatestJobByType(jobs, v1alpha1.JobTypeProvision)
	if csiInstallFailed(latest) {
		return csiInstallBackoffRemaining(jobs, latest) <= 0
	}
	return !provisioning.HasJobID(latest)
}

// csiInstallRetryAfter returns when to retry after a failed launch or job, or
// zero when the latest did not fail.
func csiInstallRetryAfter(jobs []v1alpha1.JobStatus) time.Duration {
	latest := provisioning.FindLatestJobByType(jobs, v1alpha1.JobTypeProvision)
	if !csiInstallFailed(latest) {
		return 0
	}
	if remaining := csiInstallBackoffRemaining(jobs, latest); remaining > 0 {
		return remaining
	}
	return time.Second
}

func csiInstallBackoffRemaining(jobs []v1alpha1.JobStatus, latest *v1alpha1.JobStatus) time.Duration {
	backoff := provisioning.ComputeBackoffFromJobs(jobs, latest.ConfigVersion)
	return backoff - time.Since(latest.Timestamp.Time)
}

// updateCSIDriverReadyCondition derives CSIDriverReady from the latest install
// job. With no job it leaves the condition alone.
func updateCSIDriverReadyCondition(instance *v1alpha1.ClusterOrder) {
	latest := provisioning.FindLatestJobByType(instance.Status.CSIInstallJobs, v1alpha1.JobTypeProvision)
	if latest == nil {
		return
	}
	switch {
	case latest.State.IsSuccessful():
		setCSIDriverReadyCondition(instance, metav1.ConditionTrue, v1alpha1.ClusterOrderReasonCSIDriverInstalled,
			"The CSI install job succeeded")
	case latest.State.IsTerminal():
		message := latest.Message
		if message == "" {
			message = "The CSI install job failed"
		}
		setCSIDriverReadyCondition(instance, metav1.ConditionFalse, v1alpha1.ClusterOrderReasonCSIDriverInstallFailed,
			truncateAddOnOperatorMessage(message))
	default:
		setCSIDriverReadyCondition(instance, metav1.ConditionFalse, v1alpha1.ClusterOrderReasonCSIDriverInstalling,
			fmt.Sprintf("CSI install job %s is %s", latest.JobID, latest.State))
	}
}

func setCSIDriverReadyCondition(instance *v1alpha1.ClusterOrder, status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               string(v1alpha1.ClusterOrderConditionCSIDriverReady),
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: instance.Generation,
	})
}

func findCSIDriverReadyCondition(instance *v1alpha1.ClusterOrder) *metav1.Condition {
	condition := apimeta.FindStatusCondition(instance.Status.Conditions, string(v1alpha1.ClusterOrderConditionCSIDriverReady))
	if condition == nil {
		return nil
	}
	copied := *condition
	return &copied
}
