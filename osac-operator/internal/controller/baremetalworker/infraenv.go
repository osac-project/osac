// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package baremetalworker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	infraEnvNameSuffix   = "-infraenv"
	pullSecretNameSuffix = "-pull-secret"
	// ignitionSizeWarningThreshold is 75% of the 64KB user_data limit; above it the controller
	// warns that discovery ignition is approaching the BMI user_data ceiling.
	ignitionSizeWarningThreshold     = 48 * 1024
	infraEnvRequeueInterval          = 30 * time.Second
	eventReasonIgnitionSizeWarning   = "DiscoveryIgnitionSizeWarning"
	reasonInfraEnvReady              = "InfraEnvReady"
	reasonIgnitionPending            = "IgnitionPending"
	infraEnvUIDAnnotation            = "osac.openshift.io/infraenv-uid"
	eventReasonStaleIgnition         = "StaleIgnition"
	reasonInfraEnvRecreated          = "InfraEnvRecreated"
	reasonStaleIgnitionWorkersMarked = "StaleIgnitionWorkersMarked"
)

var infraEnvGVK = schema.GroupVersionKind{Group: "agent-install.openshift.io", Version: agentInstallAPIVersion, Kind: "InfraEnv"}

// prepareWorkerProvisioning preserves the prerequisite and UID interruption
// boundaries. Stale failures must be durable before recording the new UID,
// even when disk-image resolution subsequently requeues.
func (r *Reconciler) prepareWorkerProvisioning(ctx context.Context, co *v1alpha1.ClusterOrder) (*privatev1.DiskImageReference, []byte, ctrl.Result, error) {
	if err := r.ensurePullSecret(ctx, co); err != nil {
		return nil, nil, ctrl.Result{}, fmt.Errorf("ensuring pull secret: %w", err)
	}
	ignition, uid, res, err := r.ensureInfraEnv(ctx, co)
	if err != nil || !res.IsZero() {
		return nil, nil, res, err
	}
	changed, res, err := r.detectStaleIgnitionWorkers(ctx, co, uid)
	if err != nil || !res.IsZero() {
		return nil, nil, res, err
	}
	if changed {
		return nil, nil, workerBoundaryRequeue(), nil
	}
	if err := r.trackInfraEnvUID(ctx, co, uid); err != nil {
		return nil, nil, ctrl.Result{}, err
	}
	image, res, err := r.resolveDiskImage(ctx, co)
	return image, ignition, res, err
}

// ensureInfraEnv creates one InfraEnv per ClusterOrder (late binding, owned by the ClusterOrder),
// then polls for and fetches its discovery ignition, setting the InfraEnvReady condition.
// Returns the fetched ignition bytes and the InfraEnv's UID once ready.
func (r *Reconciler) ensureInfraEnv(ctx context.Context, co *v1alpha1.ClusterOrder) ([]byte, string, ctrl.Result, error) {
	key := client.ObjectKey{Name: co.Name + infraEnvNameSuffix, Namespace: co.Namespace}
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(infraEnvGVK)

	if apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionInfraEnvReady) {
		if err := r.Get(ctx, key, existing); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, "", ctrl.Result{}, fmt.Errorf("getting infraenv %s: %w", key, err)
			}
			ctrllog.FromContext(ctx).Info("InfraEnv deleted while InfraEnvReady=True, resetting condition", "infraenv", key)
			if condErr := r.setInfraEnvReady(ctx, co, metav1.ConditionFalse, reasonInfraEnvRecreated,
				"InfraEnv was deleted; recreating"); condErr != nil {
				return nil, "", ctrl.Result{}, condErr
			}
			res, createErr := r.createInfraEnv(ctx, co, key)
			return nil, "", res, createErr
		}
		ign, res, err := r.fetchDiscoveryIgnition(ctx, co, key, existing)
		return ign, string(existing.GetUID()), res, err
	}

	err := r.Get(ctx, key, existing)
	switch {
	case apierrors.IsNotFound(err):
		res, createErr := r.createInfraEnv(ctx, co, key)
		return nil, "", res, createErr
	case err != nil:
		return nil, "", ctrl.Result{}, fmt.Errorf("getting infraenv %s: %w", key, err)
	}
	ign, res, fetchErr := r.fetchDiscoveryIgnition(ctx, co, key, existing)
	return ign, string(existing.GetUID()), res, fetchErr
}

// createInfraEnv creates the InfraEnv and marks InfraEnvReady=False (ignition pending), requeuing.
func (r *Reconciler) createInfraEnv(ctx context.Context, co *v1alpha1.ClusterOrder, key client.ObjectKey) (ctrl.Result, error) {
	infraEnv, err := r.buildInfraEnv(co, key.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, infraEnv); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, fmt.Errorf("creating infraenv %s: %w", key, err)
	}
	ctrllog.FromContext(ctx).Info("created InfraEnv", "infraenv", key.String())
	if err := r.setInfraEnvReady(ctx, co, metav1.ConditionFalse, reasonIgnitionPending,
		"InfraEnv created; waiting for discovery ignition"); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: infraEnvRequeueInterval}, nil
}

// fetchDiscoveryIgnition polls the InfraEnv's discovery ignition URL and, once available, fetches
// the ignition (warning if oversized) and marks InfraEnvReady=True. Returns the fetched bytes.
func (r *Reconciler) fetchDiscoveryIgnition(
	ctx context.Context, co *v1alpha1.ClusterOrder, key client.ObjectKey, infraEnv *unstructured.Unstructured,
) ([]byte, ctrl.Result, error) {
	ignitionURL, _, err := unstructured.NestedString(infraEnv.Object, "status", "bootArtifacts", "discoveryIgnitionURL")
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("reading infraenv %s ignition URL: %w", key, err)
	}
	if ignitionURL == "" {
		if condErr := r.setInfraEnvReady(ctx, co, metav1.ConditionFalse, reasonIgnitionPending,
			"waiting for discovery ignition URL"); condErr != nil {
			return nil, ctrl.Result{}, condErr
		}
		return nil, ctrl.Result{RequeueAfter: infraEnvRequeueInterval}, nil
	}

	ignition, err := r.ignition.FetchIgnition(ctx, ignitionURL)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("fetching discovery ignition for %s: %w", key, err)
	}
	if !json.Valid(ignition) {
		return nil, ctrl.Result{}, fmt.Errorf("discovery ignition for %s is not valid JSON", key)
	}
	if len(ignition) > ignitionSizeWarningThreshold {
		r.recorder.Eventf(co, nil, corev1.EventTypeWarning, eventReasonIgnitionSizeWarning, "FetchIgnition",
			"discovery ignition is %d bytes, exceeding the %d byte warning threshold", len(ignition), ignitionSizeWarningThreshold)
	}
	if !apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionInfraEnvReady) {
		if err := r.setInfraEnvReady(ctx, co, metav1.ConditionTrue, reasonInfraEnvReady,
			"discovery ignition fetched"); err != nil {
			return nil, ctrl.Result{}, err
		}
	}
	return ignition, ctrl.Result{}, nil
}

// ensurePullSecret ensures the pull secret K8s Secret referenced by the InfraEnv exists.
// It checks co.Spec.PullSecret first, then falls back to extracting the pull_secret key
// from co.Spec.TemplateParameters (a JSON-encoded map).
func (r *Reconciler) ensurePullSecret(ctx context.Context, co *v1alpha1.ClusterOrder) error {
	secretName := co.Name + pullSecretNameSuffix
	key := client.ObjectKey{Name: secretName, Namespace: co.Namespace}

	existing := &corev1.Secret{}
	if err := r.Get(ctx, key, existing); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("checking pull secret %s: %w", key, err)
	}

	pullSecret := co.Spec.PullSecret
	if pullSecret == "" && co.Spec.TemplateParameters != "" {
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(co.Spec.TemplateParameters), &params); err == nil {
			if ps, ok := params["pull_secret"].(string); ok {
				pullSecret = ps
			}
		}
	}
	if pullSecret == "" {
		return fmt.Errorf("no pull secret available on ClusterOrder %s (neither spec.pullSecret nor templateParameters.pull_secret)", co.Name)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: co.Namespace,
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(pullSecret),
		},
	}
	if err := controllerutil.SetControllerReference(co, secret, r.scheme); err != nil {
		return fmt.Errorf("setting pull secret owner reference: %w", err)
	}
	if err := r.Create(ctx, secret); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("creating pull secret %s: %w", key, err)
	}
	ctrllog.FromContext(ctx).Info("created pull secret for InfraEnv", "secret", key.String())
	return nil
}

// resolveSSHPublicKey extracts the SSH public key from co.Spec.SSHPublicKey,
// falling back to co.Spec.TemplateParameters["ssh_public_key"] if unset.
func (r *Reconciler) resolveSSHPublicKey(co *v1alpha1.ClusterOrder) string {
	if co.Spec.SSHPublicKey != "" {
		return co.Spec.SSHPublicKey
	}
	if co.Spec.TemplateParameters != "" {
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(co.Spec.TemplateParameters), &params); err == nil {
			if key, ok := params["ssh_public_key"].(string); ok && key != "" {
				return key
			}
		}
	}
	return ""
}

// buildInfraEnv constructs the (unstructured) InfraEnv object: late binding (no clusterRef),
// owned by the ClusterOrder, referencing the cluster pull secret and SSH key.
func (r *Reconciler) buildInfraEnv(co *v1alpha1.ClusterOrder, name string) (*unstructured.Unstructured, error) {
	infraEnv := &unstructured.Unstructured{}
	infraEnv.SetGroupVersionKind(infraEnvGVK)
	infraEnv.SetName(name)
	infraEnv.SetNamespace(co.Namespace)

	// The pull-secret Secret is ensured by ensurePullSecret before InfraEnv creation.
	// No clusterRef is set (late binding): agents
	// register unbound and are bound explicitly after MAC correlation (OSAC-4160).
	spec := map[string]interface{}{
		"pullSecretRef": map[string]interface{}{"name": co.Name + pullSecretNameSuffix},
	}
	if sshKey := r.resolveSSHPublicKey(co); sshKey != "" {
		spec["sshAuthorizedKey"] = sshKey
	}
	if err := unstructured.SetNestedMap(infraEnv.Object, spec, "spec"); err != nil {
		return nil, fmt.Errorf("building infraenv spec: %w", err)
	}
	if err := controllerutil.SetControllerReference(co, infraEnv, r.scheme); err != nil {
		return nil, fmt.Errorf("setting infraenv owner reference: %w", err)
	}
	return infraEnv, nil
}

func (r *Reconciler) setInfraEnvReady(
	ctx context.Context, co *v1alpha1.ClusterOrder, status metav1.ConditionStatus, reason, message string,
) error {
	return r.patchStatusWithRetry(ctx, co, func(latest *v1alpha1.ClusterOrder) {
		latest.SetStatusCondition(v1alpha1.ConditionInfraEnvReady, status, message, reason)
	})
}

// trackInfraEnvUID stores the InfraEnv's UID as an annotation on the ClusterOrder so
// stale ignition can be detected after InfraEnv deletion+recreation.
func (r *Reconciler) trackInfraEnvUID(
	ctx context.Context, co *v1alpha1.ClusterOrder, uid string,
) error {
	if uid == "" || co.Annotations[infraEnvUIDAnnotation] == uid {
		return nil
	}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.ClusterOrder{}
		if err := r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
			return err
		}
		if !sameWorkerOrder(co, latest) || latest.Annotations[infraEnvUIDAnnotation] != co.Annotations[infraEnvUIDAnnotation] {
			return fmt.Errorf("ClusterOrder changed before InfraEnv UID recording")
		}
		base := latest.DeepCopy()
		if latest.Annotations == nil {
			latest.Annotations = make(map[string]string)
		}
		latest.Annotations[infraEnvUIDAnnotation] = uid
		return r.Patch(ctx, latest, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	})
	if err != nil {
		return fmt.Errorf("recording InfraEnv UID: %w", err)
	}
	return r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co)
}

// detectStaleIgnitionWorkers checks whether the InfraEnv was recreated since the last
// reconcile. If so, workers in WaitingForAgent phase have stale ignition and are marked
// Failed with AgentRegistrationTimeout so they enter the retry pipeline.
func (r *Reconciler) detectStaleIgnitionWorkers(
	ctx context.Context, co *v1alpha1.ClusterOrder, infraEnvUID string,
) (bool, ctrl.Result, error) {
	changes := classifyStaleIgnition(co, infraEnvUID)
	res, err := r.persistWorkerChangesAndRefresh(ctx, co, changes)
	if err != nil || !res.IsZero() {
		return false, res, err
	}
	for _, change := range changes {
		w := *change.replacement
		observeProvisioningFailure(tenantOf(co), w)
		r.recorder.Eventf(co, nil, corev1.EventTypeWarning, eventReasonStaleIgnition, "DetectStaleIgnition",
			"worker %s: marked failed due to stale ignition after InfraEnv recreation", w.Name)
	}
	return len(changes) > 0, ctrl.Result{}, nil
}

func classifyStaleIgnition(co *v1alpha1.ClusterOrder, infraEnvUID string) []workerChange {
	storedUID := co.Annotations[infraEnvUIDAnnotation]
	if storedUID == "" || storedUID == infraEnvUID {
		return nil
	}
	var changes []workerChange
	now := metav1.Now()
	for _, w := range co.Status.Workers {
		if w.Phase != workerPhaseWaitingForAgent {
			continue
		}
		next := w
		next.Phase = workerPhaseFailed
		next.LastFailureReason = eventReasonAgentRegistrationTimeout
		next.LastFailureMessage = "stale ignition: InfraEnv was recreated"
		next.LastFailureTime = &now
		changes = appendWorkerDifference(changes, w, next)
	}
	return changes
}
