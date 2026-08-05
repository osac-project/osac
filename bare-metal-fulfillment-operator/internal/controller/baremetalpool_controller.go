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

// Package controller implements the controller logic
package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/osac-project/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/bare-metal-fulfillment-operator/internal/profile"
	"github.com/osac-project/bare-metal-fulfillment-operator/internal/shared"
	opv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac-operator/pkg/provisioning"
)

// BareMetalPoolReconciler reconciles a BareMetalPool object
type BareMetalPoolReconciler struct {
	client.Client
	Scheme                           *runtime.Scheme
	HostReadyPollIntervalDuration    time.Duration
	HostDeletionPollIntervalDuration time.Duration
	ProvisionJobPollIntervalDuration time.Duration
	MaxJobHistory                    int
	provider                         provisioning.ProvisioningProvider
}

// NewBareMetalPoolReconciler creates a new BareMetalPoolReconciler with the provided configuration.
// Zero or negative duration values are replaced with defaults.
func NewBareMetalPoolReconciler(
	client client.Client,
	scheme *runtime.Scheme,
	provider provisioning.ProvisioningProvider,
	hostReadyPollIntervalDuration time.Duration,
	hostDeletionPollIntervalDuration time.Duration,
	provisionJobPollIntervalDuration time.Duration,
	maxJobHistory int,
) *BareMetalPoolReconciler {

	if hostReadyPollIntervalDuration <= 0 {
		hostReadyPollIntervalDuration = DefaultHostReadyPollIntervalDuration
	}

	if hostDeletionPollIntervalDuration <= 0 {
		hostDeletionPollIntervalDuration = DefaultHostDeletionPollIntervalDuration
	}

	if provisionJobPollIntervalDuration <= 0 {
		provisionJobPollIntervalDuration = DefaultAAPStatusPollIntervalDuration
	}

	if maxJobHistory <= 0 {
		maxJobHistory = DefaultMaxJobHistory
	}

	return &BareMetalPoolReconciler{
		Client:                           client,
		Scheme:                           scheme,
		HostReadyPollIntervalDuration:    hostReadyPollIntervalDuration,
		HostDeletionPollIntervalDuration: hostDeletionPollIntervalDuration,
		ProvisionJobPollIntervalDuration: provisionJobPollIntervalDuration,
		MaxJobHistory:                    maxJobHistory,
		provider:                         provider,
	}
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalpools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalpools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalpools/finalizers,verbs=update
// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalinstances,verbs=get;list;watch;create;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the pool closer to the desired state.
func (r *BareMetalPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("BareMetalPool reconcile start")

	bareMetalPool := &v1alpha1.BareMetalPool{}
	err := r.Get(ctx, req.NamespacedName, bareMetalPool)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	oldstatus := bareMetalPool.Status.DeepCopy()

	var result ctrl.Result
	if !bareMetalPool.DeletionTimestamp.IsZero() {
		result, err = r.handleDeletion(ctx, bareMetalPool)
	} else {
		result, err = r.handleUpdate(ctx, bareMetalPool)
	}

	if !equality.Semantic.DeepEqual(bareMetalPool.Status, *oldstatus) {
		log.Info("Updating BareMetalPool status")
		if statusErr := r.Status().Update(ctx, bareMetalPool); client.IgnoreNotFound(statusErr) != nil {
			return result, statusErr
		}
	}

	log.Info("BareMetalPool reconcile end")
	return result, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *BareMetalPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.BareMetalPool{}).
		Owns(
			&v1alpha1.BareMetalInstance{},
			builder.WithPredicates(predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					return false
				},
				UpdateFunc: func(e event.UpdateEvent) bool {
					return false
				},
				DeleteFunc: func(e event.DeleteEvent) bool {
					return true
				},
			}),
		).
		Named("baremetalpool").
		Complete(r)
}

// handleUpdate reconciles the BareMetalPool to match its desired state.
func (r *BareMetalPoolReconciler) handleUpdate(ctx context.Context, bareMetalPool *v1alpha1.BareMetalPool) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Updating BareMetalPool")

	bareMetalPool.InitializeStatusConditions()
	if bareMetalPool.Status.Phase == "" {
		bareMetalPool.Status.Phase = v1alpha1.BareMetalPoolPhaseProgressing
	}

	if bareMetalPool.Spec.Profile != nil && r.provider == nil {
		log.Info("Provisioning provider not configured", "profile", bareMetalPool.Spec.Profile.Name)
		bareMetalPool.Status.Phase = v1alpha1.BareMetalPoolPhaseFailed
		bareMetalPool.SetStatusCondition(
			v1alpha1.BareMetalPoolConditionTypeReady,
			metav1.ConditionFalse,
			"Provisioning provider not configured",
			v1alpha1.BareMetalPoolReasonFailed,
		)
		return ctrl.Result{}, nil
	}

	desiredConfigVersion, err := provisioning.ComputeDesiredConfigVersion(bareMetalPool.Spec)
	if err != nil {
		log.Error(err, "Failed to compute desired config version")
		bareMetalPool.Status.Phase = v1alpha1.BareMetalPoolPhaseFailed
		bareMetalPool.SetStatusCondition(
			v1alpha1.BareMetalPoolConditionTypeReady,
			metav1.ConditionFalse,
			"Failed to compute desired config version",
			v1alpha1.BareMetalPoolReasonFailed,
		)
		return ctrl.Result{}, err
	}
	bareMetalPool.Status.DesiredConfigVersion = desiredConfigVersion

	currentProfile, ok := r.validateProfile(ctx, bareMetalPool)
	if !ok {
		return ctrl.Result{}, nil
	}

	if err := r.ensureFinalizer(ctx, bareMetalPool); err != nil {
		return ctrl.Result{}, err
	}

	if bareMetalPool.Status.HostSets == nil {
		bareMetalPool.Status.HostSets = []v1alpha1.BareMetalHostSet{}
	}

	currentBareMetalInstances, err := r.listAndGroupBareMetalInstances(ctx, bareMetalPool)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileBareMetalInstances(ctx, bareMetalPool, currentBareMetalInstances, currentProfile); err != nil {
		return ctrl.Result{}, err
	}

	if currentProfile != nil && currentProfile.BareMetalPoolTemplate != "" && currentProfile.BareMetalPoolTemplate != shared.OsacNoopTemplate && r.provider != nil {
		result, err := r.reconcileProvisioning(ctx, bareMetalPool, currentProfile.BareMetalPoolTemplate)
		if err != nil {
			log.Error(err, "Failed to run provisioning lifecycle")
			return result, err
		}
		if !result.IsZero() {
			return result, nil
		}
	}

	ready, err := r.checkBareMetalInstancesReady(ctx, bareMetalPool)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ready {
		return ctrl.Result{RequeueAfter: r.HostReadyPollIntervalDuration}, nil
	}

	bareMetalPool.Status.Phase = v1alpha1.BareMetalPoolPhaseReady
	bareMetalPool.SetStatusCondition(
		v1alpha1.BareMetalPoolConditionTypeReady,
		metav1.ConditionTrue,
		"Successfully reconciled host leases",
		v1alpha1.BareMetalPoolReasonReady,
	)

	log.Info("Successfully updated BareMetalPool")
	return ctrl.Result{}, nil
}

// validateProfile checks the profile exists and has valid parameters.
func (r *BareMetalPoolReconciler) validateProfile(ctx context.Context, bareMetalPool *v1alpha1.BareMetalPool) (*profile.Profile, bool) {
	log := logf.FromContext(ctx)

	if bareMetalPool.Spec.Profile == nil {
		return nil, true
	}

	profileName := bareMetalPool.Spec.Profile.Name
	currentProfile := profile.Get(profileName)
	if currentProfile == nil {
		log.Info("Profile does not exist", "profile name", profileName)
		bareMetalPool.Status.Phase = v1alpha1.BareMetalPoolPhaseFailed
		bareMetalPool.SetStatusCondition(
			v1alpha1.BareMetalPoolConditionTypeReady,
			metav1.ConditionFalse,
			"Profile does not exist",
			v1alpha1.BareMetalPoolReasonFailed,
		)
		return nil, false
	}

	if !currentProfile.ValidateParameters(bareMetalPool.Spec.Profile.TemplateParameters) {
		log.Info("TemplateParameters do not match the profile's expected parameters")
		bareMetalPool.Status.Phase = v1alpha1.BareMetalPoolPhaseFailed
		bareMetalPool.SetStatusCondition(
			v1alpha1.BareMetalPoolConditionTypeReady,
			metav1.ConditionFalse,
			"TemplateParameters do not match the profile's expected parameters",
			v1alpha1.BareMetalPoolReasonFailed,
		)
		return nil, false
	}

	return currentProfile, true
}

// ensureFinalizer adds the BareMetalPool finalizer if not already present.
func (r *BareMetalPoolReconciler) ensureFinalizer(ctx context.Context, bareMetalPool *v1alpha1.BareMetalPool) error {
	log := logf.FromContext(ctx)

	if !controllerutil.AddFinalizer(bareMetalPool, BareMetalPoolFinalizer) {
		return nil
	}

	if err := r.Update(ctx, bareMetalPool); err != nil {
		log.Error(err, "Failed to add finalizer")
		bareMetalPool.Status.Phase = v1alpha1.BareMetalPoolPhaseFailed
		bareMetalPool.SetStatusCondition(
			v1alpha1.BareMetalPoolConditionTypeReady,
			metav1.ConditionFalse,
			"Failed to add finalizer",
			v1alpha1.BareMetalPoolReasonFailed,
		)
		return err
	}

	log.Info("Added finalizer")
	return nil
}

// listAndGroupBareMetalInstances retrieves all BareMetalInstances owned by this pool and groups them by hostType.
func (r *BareMetalPoolReconciler) listAndGroupBareMetalInstances(ctx context.Context, bareMetalPool *v1alpha1.BareMetalPool) (map[string][]*v1alpha1.BareMetalInstance, error) {
	log := logf.FromContext(ctx)
	log.Info("Retrieving BareMetalInstances")

	bareMetalInstanceList := &v1alpha1.BareMetalInstanceList{}
	err := r.List(ctx, bareMetalInstanceList,
		client.InNamespace(bareMetalPool.Namespace),
		client.MatchingLabels{BareMetalPoolLabelKey: string(bareMetalPool.UID)},
	)
	if err != nil {
		log.Error(err, "Failed to list BareMetalInstance CRs")
		bareMetalPool.Status.Phase = v1alpha1.BareMetalPoolPhaseFailed
		bareMetalPool.SetStatusCondition(
			v1alpha1.BareMetalPoolConditionTypeReady,
			metav1.ConditionFalse,
			"Failed to list BareMetalInstance CRs",
			v1alpha1.BareMetalPoolReasonFailed,
		)
		return nil, err
	}

	log.Info("Extracting BareMetalInstances")
	currentBareMetalInstances := map[string][]*v1alpha1.BareMetalInstance{}
	for i := range bareMetalInstanceList.Items {
		if !bareMetalInstanceList.Items[i].DeletionTimestamp.IsZero() {
			continue
		}
		hostType := bareMetalInstanceList.Items[i].Spec.HostType
		currentBareMetalInstances[hostType] = append(currentBareMetalInstances[hostType], &bareMetalInstanceList.Items[i])
	}

	return currentBareMetalInstances, nil
}

// reconcileBareMetalInstances creates or deletes BareMetalInstances to match the desired replica count per hostType.
func (r *BareMetalPoolReconciler) reconcileBareMetalInstances(ctx context.Context, bareMetalPool *v1alpha1.BareMetalPool, currentBareMetalInstances map[string][]*v1alpha1.BareMetalInstance, currentProfile *profile.Profile) error {
	log := logf.FromContext(ctx)
	log.Info("Determining desired replica count per host type")

	desiredReplicas := map[string]int32{}
	for _, hostSet := range bareMetalPool.Spec.HostSets {
		desiredReplicas[hostSet.HostType] = hostSet.Replicas
	}
	for hostType := range currentBareMetalInstances {
		if _, ok := desiredReplicas[hostType]; !ok {
			desiredReplicas[hostType] = 0
		}
	}

	defer r.updateStatusHostSets(bareMetalPool, currentBareMetalInstances)

	for hostType, replicas := range desiredReplicas {
		delta := replicas - int32(len(currentBareMetalInstances[hostType]))
		if delta > 0 {
			if err := r.scaleUpBareMetalInstances(ctx, bareMetalPool, hostType, delta, currentBareMetalInstances, currentProfile); err != nil {
				return err
			}
		} else if delta < 0 {
			if err := r.scaleDownBareMetalInstances(ctx, bareMetalPool, hostType, replicas, currentBareMetalInstances); err != nil {
				return err
			}
		}
	}

	return nil
}

// scaleUpBareMetalInstances creates the specified number of new BareMetalInstances for the given hostType.
func (r *BareMetalPoolReconciler) scaleUpBareMetalInstances(ctx context.Context, bareMetalPool *v1alpha1.BareMetalPool, hostType string, delta int32, currentBareMetalInstances map[string][]*v1alpha1.BareMetalInstance, currentProfile *profile.Profile) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("Scaling up: %s (+%d)", hostType, delta))

	for range delta {
		log.Info("Creating BareMetalInstance", "hostType", hostType)
		if err := r.createBareMetalInstanceCR(ctx, bareMetalPool, hostType, currentProfile); err != nil {
			log.Error(err, "Failed to create BareMetalInstance CR")
			bareMetalPool.Status.Phase = v1alpha1.BareMetalPoolPhaseFailed
			bareMetalPool.SetStatusCondition(
				v1alpha1.BareMetalPoolConditionTypeReady,
				metav1.ConditionFalse,
				"Failed to create BareMetalInstance CR",
				v1alpha1.BareMetalPoolReasonFailed,
			)
			return err
		}
		currentBareMetalInstances[hostType] = append(currentBareMetalInstances[hostType], nil)
	}

	return nil
}

// scaleDownBareMetalInstances deletes BareMetalInstances to reach the desired replica count for the given hostType.
func (r *BareMetalPoolReconciler) scaleDownBareMetalInstances(ctx context.Context, bareMetalPool *v1alpha1.BareMetalPool, hostType string, replicas int32, currentBareMetalInstances map[string][]*v1alpha1.BareMetalInstance) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("Scaling down: %s (-%d)", hostType, int32(len(currentBareMetalInstances[hostType]))-replicas))

	for int32(len(currentBareMetalInstances[hostType])) > replicas {
		log.Info("Deleting BareMetalInstance", "hostType", hostType)
		lastIdx := len(currentBareMetalInstances[hostType]) - 1
		bareMetalInstance := currentBareMetalInstances[hostType][lastIdx]
		if err := r.Delete(ctx, bareMetalInstance); client.IgnoreNotFound(err) != nil {
			log.Error(err, "Failed to delete BareMetalInstance CR", "bareMetalInstance", bareMetalInstance.Name)
			bareMetalPool.Status.Phase = v1alpha1.BareMetalPoolPhaseFailed
			bareMetalPool.SetStatusCondition(
				v1alpha1.BareMetalPoolConditionTypeReady,
				metav1.ConditionFalse,
				"Failed to delete BareMetalInstance CR",
				v1alpha1.BareMetalPoolReasonFailed,
			)
			return err
		}
		currentBareMetalInstances[hostType] = currentBareMetalInstances[hostType][:lastIdx]
		if len(currentBareMetalInstances[hostType]) == 0 {
			delete(currentBareMetalInstances, hostType)
			break
		}
	}

	return nil
}

// checkBareMetalInstancesReady returns true if all BareMetalInstances have completed their provisioning templates.
func (r *BareMetalPoolReconciler) checkBareMetalInstancesReady(ctx context.Context, bareMetalPool *v1alpha1.BareMetalPool) (bool, error) {
	log := logf.FromContext(ctx)
	log.Info("Checking if BareMetalInstances have finished their provisioning templates")

	bareMetalInstanceList := &v1alpha1.BareMetalInstanceList{}
	err := r.List(ctx, bareMetalInstanceList,
		client.InNamespace(bareMetalPool.Namespace),
		client.MatchingLabels{BareMetalPoolLabelKey: string(bareMetalPool.UID)},
	)
	if err != nil {
		log.Error(err, "Failed to list BareMetalInstance CRs")
		bareMetalPool.Status.Phase = v1alpha1.BareMetalPoolPhaseFailed
		bareMetalPool.SetStatusCondition(
			v1alpha1.BareMetalPoolConditionTypeReady,
			metav1.ConditionFalse,
			"Failed to list BareMetalInstance CRs",
			v1alpha1.BareMetalPoolReasonFailed,
		)
		return false, err
	}

	for _, bareMetalInstance := range bareMetalInstanceList.Items {
		if !bareMetalInstance.DeletionTimestamp.IsZero() {
			continue
		}
		if bareMetalInstance.Status.Phase != v1alpha1.BareMetalInstancePhaseReady {
			log.Info("Not all BareMetalInstances finished their provisioning templates")
			bareMetalPool.Status.Phase = v1alpha1.BareMetalPoolPhaseProgressing
			bareMetalPool.SetStatusCondition(
				v1alpha1.BareMetalPoolConditionTypeReady,
				metav1.ConditionFalse,
				"Not all BareMetalInstances have finished their provisioning templates",
				v1alpha1.BareMetalPoolReasonProgressing,
			)
			return false, nil
		}
	}

	return true, nil
}

// handleDeletion runs deprovisioning workflow and deletes all BareMetalInstances before removing the finalizer.
func (r *BareMetalPoolReconciler) handleDeletion(ctx context.Context, bareMetalPool *v1alpha1.BareMetalPool) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Deleting BareMetalPool")

	bareMetalPool.Status.Phase = v1alpha1.BareMetalPoolPhaseDeleting
	bareMetalPool.SetStatusCondition(
		v1alpha1.BareMetalPoolConditionTypeReady,
		metav1.ConditionFalse,
		"BareMetalPool is being torn down",
		v1alpha1.BareMetalPoolReasonDeleting,
	)

	if bareMetalPool.Status.ProvisioningJobs == nil {
		bareMetalPool.Status.ProvisioningJobs = []opv1alpha1.JobStatus{}
	}

	bareMetalInstanceList := &v1alpha1.BareMetalInstanceList{}
	err := r.List(ctx, bareMetalInstanceList,
		client.InNamespace(bareMetalPool.Namespace),
		client.MatchingLabels{BareMetalPoolLabelKey: string(bareMetalPool.UID)},
	)
	if err != nil {
		log.Error(err, "Failed to list BareMetalInstance CRs during deletion")
		return ctrl.Result{}, err
	}

	// Delete any BareMetalInstances that don't have a deletion timestamp yet
	for i := range bareMetalInstanceList.Items {
		bareMetalInstance := &bareMetalInstanceList.Items[i]
		if bareMetalInstance.DeletionTimestamp.IsZero() {
			if err := r.Delete(ctx, bareMetalInstance); client.IgnoreNotFound(err) != nil {
				log.Error(err, "Failed to delete BareMetalInstance CR", "bareMetalInstance", bareMetalInstance.Name)
				return ctrl.Result{}, err
			}
			log.Info("Deleted BareMetalInstance CR", "bareMetalInstance", bareMetalInstance.Name)
		}
	}

	// Wait for all BareMetalInstances to be fully deleted before removing finalizer
	if len(bareMetalInstanceList.Items) > 0 {
		log.Info("Waiting for BareMetalInstances to be deleted", "count", len(bareMetalInstanceList.Items))
		return ctrl.Result{RequeueAfter: r.HostDeletionPollIntervalDuration}, nil
	}

	// Handle profile teardown workflow if configured
	if bareMetalPool.Spec.Profile != nil {
		profileName := bareMetalPool.Spec.Profile.Name
		currentProfile := profile.Get(profileName)
		if currentProfile != nil && currentProfile.BareMetalPoolTemplate != "" && currentProfile.BareMetalPoolTemplate != shared.OsacNoopTemplate {
			if r.provider == nil {
				log.Error(nil, "Provisioning provider not configured", "profile", profileName)
				bareMetalPool.Status.Phase = v1alpha1.BareMetalPoolPhaseFailed
				bareMetalPool.SetStatusCondition(
					v1alpha1.BareMetalPoolConditionTypeReady,
					metav1.ConditionFalse,
					"Provisioning provider not configured",
					v1alpha1.BareMetalPoolReasonFailed,
				)
				return ctrl.Result{}, nil
			}

			result, done, err := r.reconcileDeprovisioning(ctx, bareMetalPool, currentProfile.BareMetalPoolTemplate)
			if err != nil {
				return result, err
			}
			if !done {
				return result, nil
			}
		}
	}

	if controllerutil.RemoveFinalizer(bareMetalPool, BareMetalPoolFinalizer) {
		if err := r.Update(ctx, bareMetalPool); err != nil {
			log.Info("Failed to remove finalizer")
			return ctrl.Result{}, err
		}
	}

	log.Info("Successfully deleted BareMetalPool")
	return ctrl.Result{}, nil
}

// reconcileDeprovisioning triggers and monitors the deprovisioning job until it reaches a terminal state.
func (r *BareMetalPoolReconciler) reconcileDeprovisioning(ctx context.Context, bareMetalPool *v1alpha1.BareMetalPool, bareMetalPoolTemplate string) (ctrl.Result, bool, error) {
	if bareMetalPool.Annotations == nil {
		bareMetalPool.Annotations = make(map[string]string)
	}
	bareMetalPool.Annotations[BareMetalPoolTemplateIDAnnotationKey] = bareMetalPoolTemplate

	result, done, err := provisioning.RunDeprovisioningLifecycle(
		ctx, r.provider, bareMetalPool,
		&bareMetalPool.Status.ProvisioningJobs, r.MaxJobHistory, r.ProvisionJobPollIntervalDuration,
	)
	// DeprovisionSkipped is represented as !done + zero result + nil error; treat as done.
	if !done && result.IsZero() && err == nil {
		done = true
	}
	return result, done, err
}

// createBareMetalInstanceCR creates a new BareMetalInstance owned by the BareMetalPool with the given hostType and profile.
func (r *BareMetalPoolReconciler) createBareMetalInstanceCR(
	ctx context.Context,
	bareMetalPool *v1alpha1.BareMetalPool,
	hostType string,
	currentProfile *profile.Profile,
) error {
	log := logf.FromContext(ctx)

	bareMetalInstanceName := fmt.Sprintf("%s-host-%s", bareMetalPool.Name, rand.String(5))
	namespace := bareMetalPool.Namespace
	labels := map[string]string{
		BareMetalPoolLabelKey: string(bareMetalPool.UID),
		HostTypeLabelKey:      hostType, // TODO: should be validated by RFC1123 in the future
	}

	templateID := shared.OsacNoopTemplate
	templateParameters := ""
	selector := v1alpha1.HostSelectorSpec{
		HostSelector: map[string]string{
			"managedBy":      shared.OsacDefaultManagedByValue,
			"provisionState": shared.OsacDefaultProvisionStateValue,
		},
	}
	if currentProfile != nil {
		if currentProfile.HostTemplate != "" {
			templateID = currentProfile.HostTemplate
		}
		templateParameters = bareMetalPool.Spec.Profile.TemplateParameters
		selector.HostSelector = currentProfile.HostSelector
	}

	// Prepare inventory labels from profile
	var inventoryLabels map[string]string
	var inventoryPersistentLabels map[string]string

	if currentProfile != nil {
		if len(currentProfile.Labels) > 0 {
			inventoryLabels = currentProfile.Labels
		}
		if len(currentProfile.PersistentLabels) > 0 {
			inventoryPersistentLabels = currentProfile.PersistentLabels
		}
	}

	bareMetalInstanceCR := &v1alpha1.BareMetalInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bareMetalInstanceName,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: v1alpha1.BareMetalInstanceSpec{
			HostType:                  hostType,
			ExternalHostID:            "",
			ExternalHostName:          "",
			Selector:                  selector,
			InventoryLabels:           inventoryLabels,
			InventoryPersistentLabels: inventoryPersistentLabels,
			TemplateID:                templateID,
			TemplateParameters:        templateParameters,
		},
	}
	if err := controllerutil.SetControllerReference(bareMetalPool, bareMetalInstanceCR, r.Scheme); err != nil {
		log.Error(err, "Failed to set controller reference", "bareMetalInstance", bareMetalInstanceName)
		return err
	}
	if err := r.Create(ctx, bareMetalInstanceCR); client.IgnoreAlreadyExists(err) != nil {
		log.Error(err, "Failed to create BareMetalInstance CR", "bareMetalInstance", bareMetalInstanceName)
		return err
	}

	log.Info("Created BareMetalInstance CR", "bareMetalInstance", bareMetalInstanceName)
	return nil
}

// reconcileProvisioning initiates the provisioning lifecycle for a BareMetalPool using the specified template.
// It delegates to the provisioning provider and manages job status tracking with callbacks for success and failure.
func (r *BareMetalPoolReconciler) reconcileProvisioning(ctx context.Context, bareMetalPool *v1alpha1.BareMetalPool, bareMetalPoolTemplate string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if bareMetalPool.Status.ProvisioningJobs == nil {
		bareMetalPool.Status.ProvisioningJobs = []opv1alpha1.JobStatus{}
	}

	if bareMetalPool.Annotations == nil {
		bareMetalPool.Annotations = make(map[string]string)
	}
	bareMetalPool.Annotations[BareMetalPoolTemplateIDAnnotationKey] = bareMetalPoolTemplate

	return provisioning.RunProvisioningLifecycle(
		ctx,
		r.provider,
		bareMetalPool,
		&provisioning.State{
			Jobs:                 &bareMetalPool.Status.ProvisioningJobs,
			DesiredConfigVersion: bareMetalPool.Status.DesiredConfigVersion,
		},
		r.MaxJobHistory,
		r.ProvisionJobPollIntervalDuration,
		&provisioning.PollCallbacks{
			OnFailed: func(message string) {
				bareMetalPool.Status.Phase = v1alpha1.BareMetalPoolPhaseFailed
				bareMetalPool.SetStatusCondition(
					v1alpha1.BareMetalPoolConditionTypeReady,
					metav1.ConditionFalse,
					message,
					v1alpha1.BareMetalPoolReasonFailed,
				)
			},
			OnSuccess: func(_ provisioning.ProvisionStatus) {
				log.Info("Provision workflow completed successfully")
			},
		},
		func() bool {
			// Check API server for non-terminal provision job to prevent duplicates
			return provisioning.CheckAPIServerForNonTerminalProvisionJob(
				ctx,
				r.Client,
				client.ObjectKeyFromObject(bareMetalPool),
				&v1alpha1.BareMetalPool{},
				func(obj client.Object) []opv1alpha1.JobStatus {
					return obj.(*v1alpha1.BareMetalPool).Status.ProvisioningJobs
				},
			)
		},
		func() error {
			// Flush status after job trigger to prevent duplicate jobs
			return r.Status().Update(ctx, bareMetalPool)
		},
	)
}

// updateStatusHostSets syncs the BareMetalPool status.HostSets field with the current BareMetalInstances.
func (r *BareMetalPoolReconciler) updateStatusHostSets(bareMetalPool *v1alpha1.BareMetalPool, currentBareMetalInstances map[string][]*v1alpha1.BareMetalInstance) {
	updatedHostSets := []v1alpha1.BareMetalHostSet{}
	for hostType, bareMetalInstances := range currentBareMetalInstances {
		if len(bareMetalInstances) > 0 {
			updatedHostSets = append(updatedHostSets, v1alpha1.BareMetalHostSet{
				HostType: hostType,
				Replicas: int32(len(bareMetalInstances)),
			})
		}
	}
	sort.Slice(updatedHostSets, func(i, j int) bool {
		return updatedHostSets[i].HostType < updatedHostSets[j].HostType
	})

	bareMetalPool.Status.HostSets = updatedHostSets
}
