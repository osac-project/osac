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
	"errors"
	"fmt"
	"strings"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const (
	maxAddOnOperatorJobMessageLength     = 4096
	addOnOperatorsReadyReason            = "AddOnOperatorsReady"
	addOnOperatorsFailedReason           = "AddOnOperatorsFailed"
	addOnOperatorPurgedJobMessage        = "AAP job was purged before completion"
	addOnOperatorKubeconfigReason        = "AddOnOperatorKubeconfigUnavailable"
	addOnOperatorCancelFailedReason      = "AddOnOperatorCancellationFailed"
	addOnOperatorCancelUnavailableReason = "AddOnOperatorCancellationUnavailable"
)

// AddOnOperatorReconciler reconciles per-operator installation jobs for ready
// ClusterOrders without owning the main ClusterOrder provisioning lifecycle.
// +kubebuilder:rbac:groups=osac.openshift.io,resources=clusterorders,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=clusterorders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=clusterorders/finalizers,verbs=update
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedcontrolplanes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
type AddOnOperatorReconciler struct {
	client.Client
	apiReader             client.Reader
	ClusterOrderNamespace string
	ProvisioningProvider  provisioning.ProvisioningProvider
	StatusPollInterval    time.Duration
	MaxJobHistory         int
	Recorder              events.EventRecorder
	getAdminKubeconfig    func(context.Context, *v1alpha1.ClusterOrder) ([]byte, error)
}

// NewAddOnOperatorReconciler creates a reconciler for add-on operator jobs.
func NewAddOnOperatorReconciler(
	k8sClient client.Client,
	apiReader client.Reader,
	namespace string,
	provider provisioning.ProvisioningProvider,
	pollInterval time.Duration,
) *AddOnOperatorReconciler {
	if namespace == "" {
		namespace = defaultClusterOrderNamespace
	}
	if pollInterval <= 0 {
		pollInterval = provisioning.DefaultStatusPollInterval
	}
	reconciler := &AddOnOperatorReconciler{
		Client:                k8sClient,
		apiReader:             apiReader,
		ClusterOrderNamespace: namespace,
		ProvisioningProvider:  provider,
		StatusPollInterval:    pollInterval,
		MaxJobHistory:         provisioning.DefaultMaxJobHistory,
	}
	reconciler.getAdminKubeconfig = reconciler.getClusterKubeconfig
	return reconciler
}

// SetupWithManager registers the namespaced ClusterOrder controller.
func (r *AddOnOperatorReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}
	if r.Recorder == nil {
		r.Recorder = localMgr.GetEventRecorder("clusterorder-addon-operators")
	}

	return ctrl.NewControllerManagedBy(localMgr).
		Named("clusterorder-addon-operators").
		For(&v1alpha1.ClusterOrder{}, builder.WithPredicates(NamespacePredicate(r.ClusterOrderNamespace))).
		Complete(r)
}

// Reconcile processes independently tracked operator installation jobs after
// the main ClusterOrder reaches Ready.
func (r *AddOnOperatorReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	instance := &v1alpha1.ClusterOrder{}
	if err := r.apiReader.Get(ctx, request.NamespacedName, instance); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	managementState := instance.Annotations[osacManagementStateAnnotation]
	if !instance.DeletionTimestamp.IsZero() {
		return r.reconcileDeletion(ctx, instance)
	}
	if managementState == ManagementStateUnmanaged || managementState == ManagementStateManual {
		return ctrl.Result{}, nil
	}
	if len(instance.Spec.AddOnOperators) == 0 {
		return ctrl.Result{}, nil
	}
	if !controllerutil.ContainsFinalizer(instance, osacAddOnOperatorFinalizer) {
		if err := r.patchAddOnOperatorFinalizerWithRetry(ctx, client.ObjectKeyFromObject(instance), true); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.apiReader.Get(ctx, request.NamespacedName, instance); err != nil {
			return ctrl.Result{}, err
		}
		if !instance.DeletionTimestamp.IsZero() {
			return r.reconcileDeletion(ctx, instance)
		}
	}
	if instance.Status.Phase != v1alpha1.ClusterOrderPhaseReady {
		return ctrl.Result{}, nil
	}

	return r.reconcileOperators(ctx, instance)
}

func (r *AddOnOperatorReconciler) reconcileDeletion(ctx context.Context, instance *v1alpha1.ClusterOrder) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	canceled := make(map[string]struct{})
	canceler, canCancel := r.ProvisioningProvider.(provisioning.ProvisioningJobCanceler)
	for _, job := range instance.Status.AddOnOperatorJobs {
		if job.JobID == "" || job.State.IsTerminal() {
			continue
		}
		if _, seen := canceled[job.JobID]; seen {
			continue
		}
		canceled[job.JobID] = struct{}{}
		if !canCancel {
			log.Info("add-on operator job cannot be cancelled during deletion", "jobID", job.JobID, "operator", job.Name)
			r.recordWarningEvent(instance, addOnOperatorCancelUnavailableReason, "CancelAddOnOperator", fmt.Sprintf("No cancellation mechanism is available for add-on operator job %s", job.JobID))
			continue
		}
		if err := canceler.CancelJob(ctx, job.JobID); err != nil {
			log.Error(err, "failed to cancel add-on operator job during deletion; continuing", "jobID", job.JobID, "operator", job.Name)
			r.recordWarningEvent(instance, addOnOperatorCancelFailedReason, "CancelAddOnOperator", fmt.Sprintf("Failed to cancel add-on operator job %s: %v", job.JobID, err))
		}
	}

	if !controllerutil.ContainsFinalizer(instance, osacAddOnOperatorFinalizer) {
		return ctrl.Result{}, nil
	}
	if err := r.patchAddOnOperatorFinalizerWithRetry(ctx, client.ObjectKeyFromObject(instance), false); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *AddOnOperatorReconciler) recordWarningEvent(instance *v1alpha1.ClusterOrder, reason, action, message string) {
	if r.Recorder != nil {
		r.Recorder.Eventf(instance, nil, corev1.EventTypeWarning, reason, action, "%s", message)
	}
}

func (r *AddOnOperatorReconciler) patchAddOnOperatorFinalizerWithRetry(ctx context.Context, key client.ObjectKey, add bool) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.ClusterOrder{}
		if err := r.apiReader.Get(ctx, key, latest); err != nil {
			return err
		}
		if add && !latest.DeletionTimestamp.IsZero() {
			return nil
		}

		base := latest.DeepCopy()
		var changed bool
		if add {
			changed = controllerutil.AddFinalizer(latest, osacAddOnOperatorFinalizer)
		} else {
			changed = controllerutil.RemoveFinalizer(latest, osacAddOnOperatorFinalizer)
		}
		if !changed {
			return nil
		}
		return r.Patch(ctx, latest, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	})
}

func (r *AddOnOperatorReconciler) persistAddOnOperatorJobStatusWithRetry(
	ctx context.Context,
	key client.ObjectKey,
	operatorName, jobID string,
	status provisioning.ProvisionStatus,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.ClusterOrder{}
		if err := r.apiReader.Get(ctx, key, latest); err != nil {
			return err
		}
		job := latestAddOnOperatorJob(latest.Status.AddOnOperatorJobs, operatorName)
		if job == nil || job.JobID != jobID {
			return nil
		}

		message := addOnOperatorStatusMessage(status)
		if job.State == status.State && job.Message == message {
			return nil
		}
		base := latest.DeepCopy()
		job.State = status.State
		job.Message = message
		return r.Status().Patch(ctx, latest, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	})
}

func (r *AddOnOperatorReconciler) reconcileOperators(ctx context.Context, instance *v1alpha1.ClusterOrder) (ctrl.Result, error) {
	var result ctrl.Result
	trimmedJobs := trimAddOnOperatorJobs(instance.Status.AddOnOperatorJobs, r.MaxJobHistory)
	statusChanged := !equality.Semantic.DeepEqual(trimmedJobs, instance.Status.AddOnOperatorJobs)
	instance.Status.AddOnOperatorJobs = trimmedJobs
	originalJobs := append([]v1alpha1.AddOnOperatorJobStatus(nil), instance.Status.AddOnOperatorJobs...)
	var provisioningContext context.Context
	var provisioningContextLoaded bool
	var provisioningContextAvailable bool
	var provisioningContextErr error
	loadProvisioningContext := func() (context.Context, bool, error) {
		if !provisioningContextLoaded {
			provisioningContextLoaded = true
			provisioningContext, provisioningContextAvailable, provisioningContextErr = r.addOnProvisioningContext(ctx, instance)
			if provisioningContextErr != nil {
				r.recordWarningEvent(instance, addOnOperatorKubeconfigReason, "ProvisionAddOnOperator", fmt.Sprintf("Add-on operators are waiting for a usable hosted-cluster kubeconfig: %v", provisioningContextErr))
			} else if !provisioningContextAvailable {
				r.recordWarningEvent(instance, addOnOperatorKubeconfigReason, "ProvisionAddOnOperator", "Add-on operators are waiting for a hosted-cluster kubeconfig")
			}
		}
		return provisioningContext, provisioningContextAvailable, provisioningContextErr
	}

	for _, operatorName := range instance.Spec.AddOnOperators {
		operatorResult, changed, err := r.reconcileOperator(ctx, instance, operatorName, loadProvisioningContext)
		if err != nil {
			if statusChanged {
				updateAddOnOperatorsReadyCondition(instance)
				if patchErr := r.patchAddOnStatusWithRetry(ctx, client.ObjectKeyFromObject(instance), originalJobs, instance.Status); patchErr != nil {
					return ctrl.Result{}, errors.Join(err, patchErr)
				}
			}
			return ctrl.Result{}, err
		}
		statusChanged = statusChanged || changed
		result = earlierRequeue(result, operatorResult)
		if changed {
			updateAddOnOperatorsReadyCondition(instance)
			if err := r.patchAddOnStatusWithRetry(ctx, client.ObjectKeyFromObject(instance), originalJobs, instance.Status); err != nil {
				return ctrl.Result{}, err
			}
			originalJobs = append([]v1alpha1.AddOnOperatorJobStatus(nil), instance.Status.AddOnOperatorJobs...)
			statusChanged = false
		}
	}

	statusChanged = updateAddOnOperatorsReadyCondition(instance) || statusChanged
	if !statusChanged {
		return result, nil
	}

	if err := r.patchAddOnStatusWithRetry(ctx, client.ObjectKeyFromObject(instance), originalJobs, instance.Status); err != nil {
		return ctrl.Result{}, err
	}
	return result, nil
}

func (r *AddOnOperatorReconciler) reconcileOperator(
	ctx context.Context,
	instance *v1alpha1.ClusterOrder,
	operatorName string,
	loadProvisioningContext func() (context.Context, bool, error),
) (ctrl.Result, bool, error) {
	latest := latestAddOnOperatorJob(instance.Status.AddOnOperatorJobs, operatorName)
	if latest == nil {
		hasJob, err := r.hasNonTerminalJob(ctx, client.ObjectKeyFromObject(instance), operatorName)
		if err != nil {
			return ctrl.Result{}, false, err
		}
		if hasJob {
			return ctrl.Result{RequeueAfter: r.StatusPollInterval}, false, nil
		}

		provisioningContext, available, err := loadProvisioningContext()
		if err != nil {
			ctrllog.FromContext(ctx).Error(err, "failed to load add-on operator provisioning context", "operator", operatorName)
			return ctrl.Result{RequeueAfter: r.StatusPollInterval}, false, nil
		}
		if !available {
			return ctrl.Result{RequeueAfter: r.StatusPollInterval}, false, nil
		}
		return r.triggerAddOnOperator(ctx, instance, operatorName, provisioningContext)
	}

	if latest.State.IsSuccessful() {
		return ctrl.Result{}, false, nil
	}

	if !latest.State.IsTerminal() {
		return r.pollOperator(ctx, instance, latest)
	}

	remaining := addOnOperatorBackoffRemaining(instance, operatorName, latest)
	if remaining > 0 {
		return ctrl.Result{RequeueAfter: remaining}, false, nil
	}

	provisioningContext, available, err := loadProvisioningContext()
	if err != nil {
		ctrllog.FromContext(ctx).Error(err, "failed to load add-on operator provisioning context", "operator", operatorName)
		return ctrl.Result{RequeueAfter: r.StatusPollInterval}, false, nil
	}
	if !available {
		return ctrl.Result{RequeueAfter: r.StatusPollInterval}, false, nil
	}
	return r.triggerAddOnOperator(ctx, instance, operatorName, provisioningContext)
}

func (r *AddOnOperatorReconciler) triggerAddOnOperator(ctx context.Context, instance *v1alpha1.ClusterOrder, operatorName string, provisioningContext context.Context) (ctrl.Result, bool, error) {
	operatorContext := provisioning.WithAddOnOperatorName(provisioningContext, operatorName)
	// Generic launch recovery and idempotency are intentionally deferred to the
	// shared provisioning design; this controller records the provider result.
	result, err := r.ProvisioningProvider.TriggerProvision(operatorContext, instance)
	if err != nil {
		return r.recordAddOnOperatorFailure(instance, operatorName, fmt.Sprintf("failed to trigger add-on operator %q: %v", operatorName, err)), true, nil
	}
	if result == nil || result.JobID == "" {
		return r.recordAddOnOperatorFailure(instance, operatorName, "add-on operator provider returned no job ID"), true, nil
	}

	instance.Status.AddOnOperatorJobs = trimAddOnOperatorJobs(append(instance.Status.AddOnOperatorJobs, v1alpha1.AddOnOperatorJobStatus{
		Name: operatorName,
		JobStatus: v1alpha1.JobStatus{
			JobID:         result.JobID,
			Type:          v1alpha1.JobTypeProvision,
			State:         result.InitialState,
			Message:       truncateAddOnOperatorMessage(result.Message),
			Timestamp:     addOnOperatorTimestamp(),
			ConfigVersion: instance.Status.DesiredConfigVersion,
		},
	}), r.MaxJobHistory)
	return ctrl.Result{RequeueAfter: r.StatusPollInterval}, true, nil
}

func (r *AddOnOperatorReconciler) recordAddOnOperatorFailure(instance *v1alpha1.ClusterOrder, operatorName, message string) ctrl.Result {
	instance.Status.AddOnOperatorJobs = trimAddOnOperatorJobs(append(instance.Status.AddOnOperatorJobs, v1alpha1.AddOnOperatorJobStatus{
		Name: operatorName,
		JobStatus: v1alpha1.JobStatus{
			Type:          v1alpha1.JobTypeProvision,
			State:         v1alpha1.JobStateFailed,
			Message:       truncateAddOnOperatorMessage(message),
			Timestamp:     addOnOperatorTimestamp(),
			ConfigVersion: instance.Status.DesiredConfigVersion,
		},
	}), r.MaxJobHistory)
	latest := latestAddOnOperatorJob(instance.Status.AddOnOperatorJobs, operatorName)
	return ctrl.Result{RequeueAfter: addOnOperatorBackoffRemaining(instance, operatorName, latest)}
}

func (r *AddOnOperatorReconciler) pollOperator(ctx context.Context, instance *v1alpha1.ClusterOrder, latest *v1alpha1.AddOnOperatorJobStatus) (ctrl.Result, bool, error) {
	status, err := r.ProvisioningProvider.GetProvisionStatus(ctx, instance, latest.JobID)
	if err != nil {
		var notFoundErr *aap.NotFoundError
		if errors.As(err, &notFoundErr) {
			changed := latest.State != v1alpha1.JobStateFailed || latest.Message != addOnOperatorPurgedJobMessage
			latest.State = v1alpha1.JobStateFailed
			latest.Message = addOnOperatorPurgedJobMessage
			return ctrl.Result{RequeueAfter: addOnOperatorBackoffRemaining(instance, latest.Name, latest)}, changed, nil
		}
		ctrllog.FromContext(ctx).Error(err, "failed to get add-on operator job status", "jobID", latest.JobID, "operator", latest.Name)
		message := "Failed to retrieve AAP job status"
		changed := latest.Message != message
		latest.Message = message
		return ctrl.Result{RequeueAfter: r.StatusPollInterval}, changed, nil
	}

	message := addOnOperatorStatusMessage(status)
	changed := latest.State != status.State || latest.Message != message
	latest.State = status.State
	latest.Message = message
	if !status.State.IsTerminal() {
		return ctrl.Result{RequeueAfter: r.StatusPollInterval}, changed, nil
	}
	if !status.State.IsSuccessful() {
		return ctrl.Result{RequeueAfter: addOnOperatorBackoffRemaining(instance, latest.Name, latest)}, changed, nil
	}
	return ctrl.Result{}, changed, nil
}

func addOnOperatorBackoffRemaining(instance *v1alpha1.ClusterOrder, operatorName string, latest *v1alpha1.AddOnOperatorJobStatus) time.Duration {
	backoff := provisioning.ComputeBackoffFromJobs(addOnOperatorJobs(instance.Status.AddOnOperatorJobs, operatorName), instance.Status.DesiredConfigVersion)
	remaining := backoff - time.Since(latest.Timestamp.Time)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (r *AddOnOperatorReconciler) hasNonTerminalJob(ctx context.Context, key client.ObjectKey, operatorName string) (bool, error) {
	latest := &v1alpha1.ClusterOrder{}
	if err := r.apiReader.Get(ctx, key, latest); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	job := latestAddOnOperatorJob(latest.Status.AddOnOperatorJobs, operatorName)
	return job != nil && job.JobID != "" && !job.State.IsTerminal(), nil
}

func (r *AddOnOperatorReconciler) addOnProvisioningContext(ctx context.Context, instance *v1alpha1.ClusterOrder) (context.Context, bool, error) {
	kubeconfig, err := r.getAdminKubeconfig(ctx, instance)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get admin kubeconfig for ClusterOrder %s: %w", instance.Name, err)
	}
	if len(kubeconfig) == 0 {
		return ctx, false, nil
	}
	return provisioning.WithAdminKubeconfig(ctx, string(kubeconfig)), true, nil
}

func (r *AddOnOperatorReconciler) getClusterKubeconfig(ctx context.Context, clusterOrder *v1alpha1.ClusterOrder) ([]byte, error) {
	ref := clusterOrder.Status.ClusterReference
	if ref == nil || ref.Namespace == "" || ref.HostedClusterName == "" {
		return nil, nil
	}
	hcpNamespace := ref.Namespace + "-" + ref.HostedClusterName
	hcp := &hypershiftv1beta1.HostedControlPlane{}
	if err := r.apiReader.Get(ctx, types.NamespacedName{Namespace: hcpNamespace, Name: ref.HostedClusterName}, hcp); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get HostedControlPlane %s/%s: %w", hcpNamespace, ref.HostedClusterName, err)
	}
	if hcp.Status.KubeConfig == nil || hcp.Status.KubeConfig.Name == "" {
		return nil, nil
	}
	secret := &corev1.Secret{}
	if err := r.apiReader.Get(ctx, types.NamespacedName{Namespace: hcpNamespace, Name: hcp.Status.KubeConfig.Name}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get kubeconfig Secret %s/%s: %w", hcpNamespace, hcp.Status.KubeConfig.Name, err)
	}
	kubeconfig, ok := secret.Data[hcp.Status.KubeConfig.Key]
	if !ok || len(kubeconfig) == 0 {
		return nil, nil
	}
	return kubeconfig, nil
}

func (r *AddOnOperatorReconciler) patchAddOnStatusWithRetry(ctx context.Context, key client.ObjectKey, originalJobs []v1alpha1.AddOnOperatorJobStatus, computed v1alpha1.ClusterOrderStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.ClusterOrder{}
		if err := r.apiReader.Get(ctx, key, latest); err != nil {
			return err
		}
		base := latest.DeepCopy()
		latest.Status.AddOnOperatorJobs = trimAddOnOperatorJobs(
			mergeAddOnOperatorJobs(originalJobs, computed.AddOnOperatorJobs, latest.Status.AddOnOperatorJobs),
			r.MaxJobHistory,
		)
		if condition := apimeta.FindStatusCondition(computed.Conditions, string(v1alpha1.ClusterOrderConditionAddOnOperatorsReady)); condition != nil {
			apimeta.SetStatusCondition(&latest.Status.Conditions, *condition)
		} else {
			latest.RemoveStatusCondition(string(v1alpha1.ClusterOrderConditionAddOnOperatorsReady))
		}
		return r.Status().Patch(ctx, latest, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	})
}

func updateAddOnOperatorsReadyCondition(instance *v1alpha1.ClusterOrder) bool {
	latestIndices := latestAddOnOperatorJobIndices(instance.Status.AddOnOperatorJobs)

	failed := make([]string, 0)
	allInstalled := true
	for _, operatorName := range instance.Spec.AddOnOperators {
		jobIndex, ok := latestIndices[operatorName]
		var job *v1alpha1.AddOnOperatorJobStatus
		if ok {
			job = &instance.Status.AddOnOperatorJobs[jobIndex]
		}
		if job == nil || !job.State.IsSuccessful() {
			allInstalled = false
		}
		if job != nil && !job.State.IsSuccessful() && (job.State.IsTerminal() || hasFailedAddOnOperatorAttempt(instance.Status.AddOnOperatorJobs, operatorName)) {
			failed = append(failed, operatorName)
		}
	}

	if len(failed) > 0 {
		return setAddOnOperatorsCondition(instance, metav1.ConditionFalse, addOnOperatorsFailedReason,
			fmt.Sprintf("Add-on operator installation failed: %s", strings.Join(failed, ", ")))
	}
	if allInstalled && len(instance.Spec.AddOnOperators) > 0 {
		return setAddOnOperatorsCondition(instance, metav1.ConditionTrue, addOnOperatorsReadyReason,
			"All add-on operators installed")
	}
	return instance.RemoveStatusCondition(string(v1alpha1.ClusterOrderConditionAddOnOperatorsReady))
}

func setAddOnOperatorsCondition(instance *v1alpha1.ClusterOrder, status metav1.ConditionStatus, reason, message string) bool {
	before := append([]metav1.Condition(nil), instance.Status.Conditions...)
	apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               string(v1alpha1.ClusterOrderConditionAddOnOperatorsReady),
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: instance.Generation,
	})
	return !equality.Semantic.DeepEqual(before, instance.Status.Conditions)
}

func latestAddOnOperatorJob(jobs []v1alpha1.AddOnOperatorJobStatus, operatorName string) *v1alpha1.AddOnOperatorJobStatus {
	index, ok := latestAddOnOperatorJobIndices(jobs)[operatorName]
	if !ok {
		return nil
	}
	return &jobs[index]
}

func latestAddOnOperatorJobIndices(jobs []v1alpha1.AddOnOperatorJobStatus) map[string]int {
	latest := make(map[string]int)
	for index, job := range jobs {
		latestIndex, ok := latest[job.Name]
		if !ok || !job.Timestamp.Time.Before(jobs[latestIndex].Timestamp.Time) {
			latest[job.Name] = index
		}
	}
	return latest
}

func addOnOperatorJobs(jobs []v1alpha1.AddOnOperatorJobStatus, operatorName string) []v1alpha1.JobStatus {
	filtered := make([]v1alpha1.JobStatus, 0)
	for _, job := range jobs {
		if job.Name == operatorName {
			filtered = append(filtered, job.JobStatus)
		}
	}
	return filtered
}

func hasFailedAddOnOperatorAttempt(jobs []v1alpha1.AddOnOperatorJobStatus, operatorName string) bool {
	for _, job := range jobs {
		if job.Name == operatorName && job.State.IsTerminal() && !job.State.IsSuccessful() {
			return true
		}
	}
	return false
}

func mergeAddOnOperatorJobs(original, computed, latest []v1alpha1.AddOnOperatorJobStatus) []v1alpha1.AddOnOperatorJobStatus {
	merged := append([]v1alpha1.AddOnOperatorJobStatus(nil), latest...)
	for _, computedJob := range computed {
		originalIndex := findAddOnOperatorJobIndex(original, computedJob)
		latestIndex := findAddOnOperatorJobIndex(merged, computedJob)
		if originalIndex < 0 {
			if latestIndex < 0 {
				merged = append(merged, computedJob)
			}
			continue
		}
		if latestIndex < 0 {
			merged = append(merged, computedJob)
			continue
		}
		if equality.Semantic.DeepEqual(merged[latestIndex], original[originalIndex]) {
			merged[latestIndex] = computedJob
		}
	}
	return merged
}

func trimAddOnOperatorJobs(jobs []v1alpha1.AddOnOperatorJobStatus, maxHistory int) []v1alpha1.AddOnOperatorJobStatus {
	// maxHistory limits additional terminal history. Active attempts and the two
	// newest failures per operator are retained because they drive cancellation,
	// failure conditions, and exponential retry backoff.
	if maxHistory <= 0 || len(jobs) <= maxHistory {
		return append([]v1alpha1.AddOnOperatorJobStatus(nil), jobs...)
	}

	latestByOperator := latestAddOnOperatorJobIndices(jobs)
	failedByOperator := make(map[string]int)
	keep := make(map[int]struct{})
	for index, job := range jobs {
		if !job.State.IsTerminal() && job.JobID != "" {
			keep[index] = struct{}{}
		}
	}

	keepCount := maxHistory
	for _, index := range latestByOperator {
		keep[index] = struct{}{}
	}
	for index := len(jobs) - 1; index >= 0; index-- {
		job := jobs[index]
		if job.State != v1alpha1.JobStateFailed || failedByOperator[job.Name] >= 2 {
			continue
		}
		failedByOperator[job.Name]++
		keep[index] = struct{}{}
	}
	if keepCount < len(keep) {
		keepCount = len(keep)
	}

	for len(keep) < keepCount {
		newestIndex := -1
		for index, job := range jobs {
			if _, exists := keep[index]; exists {
				continue
			}
			if newestIndex == -1 || job.Timestamp.Time.After(jobs[newestIndex].Timestamp.Time) {
				newestIndex = index
			}
		}
		if newestIndex == -1 {
			break
		}
		keep[newestIndex] = struct{}{}
	}

	trimmed := make([]v1alpha1.AddOnOperatorJobStatus, 0, len(keep))
	for index, job := range jobs {
		if _, exists := keep[index]; exists {
			trimmed = append(trimmed, job)
		}
	}
	return trimmed
}

func findAddOnOperatorJobIndex(jobs []v1alpha1.AddOnOperatorJobStatus, target v1alpha1.AddOnOperatorJobStatus) int {
	for index, job := range jobs {
		if job.Name != target.Name {
			continue
		}
		if target.JobID != "" && job.JobID == target.JobID {
			return index
		}
		if target.JobID == "" && job.JobID == "" && job.Timestamp.Time.Equal(target.Timestamp.Time) {
			return index
		}
	}
	return -1
}

func addOnOperatorStatusMessage(status provisioning.ProvisionStatus) string {
	if status.State.IsSuccessful() {
		return ""
	}
	if status.Message != "" {
		return truncateAddOnOperatorMessage(status.Message)
	}
	if status.State == v1alpha1.JobStateFailed {
		return "AAP job failed"
	}
	return ""
}

func truncateAddOnOperatorMessage(message string) string {
	if len(message) <= maxAddOnOperatorJobMessageLength {
		return message
	}
	return message[:maxAddOnOperatorJobMessageLength]
}

func addOnOperatorTimestamp() metav1.Time {
	return metav1.NewTime(time.Now().UTC().Truncate(time.Second))
}

func earlierRequeue(current, candidate ctrl.Result) ctrl.Result {
	if current.RequeueAfter == 0 || (candidate.RequeueAfter > 0 && candidate.RequeueAfter < current.RequeueAfter) {
		return candidate
	}
	return current
}
