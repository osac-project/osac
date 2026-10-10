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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
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
	reasonIgnitionPendingMessage     = "waiting for discovery ignition URL"
	infraEnvUIDAnnotation            = "osac.openshift.io/infraenv-uid"
	eventReasonStaleIgnition         = "StaleIgnition"
	reasonInfraEnvRecreated          = "InfraEnvRecreated"
	reasonStaleIgnitionWorkersMarked = "StaleIgnitionWorkersMarked"
)

var infraEnvGVK = schema.GroupVersionKind{Group: "agent-install.openshift.io", Version: agentInstallAPIVersion, Kind: "InfraEnv"}

// infraEnvEvidence is the invocation-local result of the single InfraEnv
// observation. object is nil only when absence was authoritative (an uncached
// read confirmed NotFound). err is an unknown result or a missing creation
// prerequisite and is never treated as absence: it can hold the invocation's
// error for the caller to merge after prerequisite-free work has converged.
type infraEnvEvidence struct {
	object *unstructured.Unstructured
	err    error
}

// observeInfraEnvEvidence observes the cluster InfraEnv once per invocation and
// records its UID so stale waiting workers are classified before a replacement
// UID is acknowledged. It never resolves creation inputs: discovery ignition and
// the disk image belong to the stage that needs them. A durable write made here
// (creating the object, replacing stale Ready evidence, or persisting a
// stale-worker classification) reports mutated=true and ends the invocation; a
// missing creation prerequisite, an unknown lookup or a foreign object is
// returned as evidence.err for the caller to merge, never as a global gate on
// existing-worker work.
func (r *Reconciler) observeInfraEnvEvidence(
	ctx context.Context, co *v1alpha1.ClusterOrder,
) (infraEnvEvidence, ctrl.Result, bool, error) {
	infraEnv, err := r.observeInfraEnvObject(ctx, co)
	if err != nil {
		// Unknown existence or ownership is not absence and never authorizes a
		// Create; prerequisite-free existing-worker work still converges.
		return infraEnvEvidence{err: err}, ctrl.Result{}, false, nil
	}
	if infraEnv == nil {
		return r.createMissingInfraEnv(ctx, co)
	}
	// Current artifact evidence replaces a stale Ready claim. A published URL is
	// not itself a claim: only the create stage's validated fetch reports Ready. A
	// condition-only write is not a boundary: the object is refreshed
	// authoritatively and the invocation continues.
	if err := r.deriveInfraEnvPending(ctx, co, infraEnv); err != nil {
		return infraEnvEvidence{object: infraEnv, err: err}, ctrl.Result{}, false, nil
	}
	uid := string(infraEnv.GetUID())
	if uid == "" {
		return infraEnvEvidence{object: infraEnv}, ctrl.Result{}, false, nil
	}
	changed, _, err := r.detectStaleIgnitionWorkers(ctx, co, uid)
	if err != nil {
		return infraEnvEvidence{object: infraEnv, err: err}, ctrl.Result{}, false, nil
	}
	if changed {
		return infraEnvEvidence{object: infraEnv}, workerBoundaryRequeue(), true, nil
	}
	if err := r.trackInfraEnvUID(ctx, co, uid); err != nil {
		return infraEnvEvidence{object: infraEnv, err: err}, ctrl.Result{}, false, nil
	}
	return infraEnvEvidence{object: infraEnv}, ctrl.Result{}, false, nil
}

// observeInfraEnvObject performs the one InfraEnv Get of an invocation. A cached
// omission is confirmed with an uncached read before it is treated as absence, and
// a present object must be controlled by this ClusterOrder before its UID or boot
// artifacts are consumed as evidence.
func (r *Reconciler) observeInfraEnvObject(ctx context.Context, co *v1alpha1.ClusterOrder) (*unstructured.Unstructured, error) {
	key := client.ObjectKey{Name: co.Name + infraEnvNameSuffix, Namespace: co.Namespace}
	cached := &unstructured.Unstructured{}
	cached.SetGroupVersionKind(infraEnvGVK)
	err := r.Get(ctx, key, cached)
	if err == nil {
		return cached, validateInfraEnvOwner(co, cached)
	}
	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("getting infraenv %s: %w", key, err)
	}
	// Cache omission is not absence: an existing or recreated object must be
	// confirmed gone before a Create is authorized.
	reader := r.apiReader
	if reader == nil {
		reader = r.Client
	}
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(infraEnvGVK)
	switch err := reader.Get(ctx, key, current); {
	case err == nil:
		return current, validateInfraEnvOwner(co, current)
	case !apierrors.IsNotFound(err):
		return nil, fmt.Errorf("getting infraenv %s: %w", key, err)
	}
	return nil, nil
}

// validateInfraEnvOwner rejects a same-name InfraEnv this ClusterOrder does not
// control. A foreign object (including one left by an earlier ClusterOrder
// incarnation) is an error, not permission to recreate or to consume its UID and
// boot artifacts.
func validateInfraEnvOwner(co *v1alpha1.ClusterOrder, infraEnv *unstructured.Unstructured) error {
	if infraEnv.GetNamespace() != co.Namespace {
		return fmt.Errorf("infraenv %s/%s is not in ClusterOrder namespace %s",
			infraEnv.GetNamespace(), infraEnv.GetName(), co.Namespace)
	}
	owner := metav1.GetControllerOf(infraEnv)
	if owner == nil || owner.Kind != "ClusterOrder" || owner.Name != co.Name {
		return fmt.Errorf("infraenv %s/%s is not controlled by ClusterOrder %s",
			infraEnv.GetNamespace(), infraEnv.GetName(), co.Name)
	}
	if co.UID != "" && owner.UID != co.UID {
		return fmt.Errorf("infraenv %s/%s is not controlled by ClusterOrder %s incarnation %s",
			infraEnv.GetNamespace(), infraEnv.GetName(), co.Name, co.UID)
	}
	return nil
}

// deriveInfraEnvPending replaces a stale InfraEnvReady claim with current pending
// evidence when the observed InfraEnv publishes no discovery ignition URL. A
// present URL is not a claim: only the create stage's validated fetch reports
// Ready, so a stable order never fetches discovery ignition for status alone. The
// condition write is a one-shot worker-owned patch, not a boundary: the object is
// refreshed authoritatively so later optimistic patches do not build on a stale
// snapshot, and repeating the same evidence writes nothing.
func (r *Reconciler) deriveInfraEnvPending(
	ctx context.Context, co *v1alpha1.ClusterOrder, infraEnv *unstructured.Unstructured,
) error {
	key := client.ObjectKeyFromObject(infraEnv)
	ignitionURL, _, err := unstructured.NestedString(infraEnv.Object, "status", "bootArtifacts", "discoveryIgnitionURL")
	if err != nil {
		return fmt.Errorf("reading infraenv %s ignition URL: %w", key, err)
	}
	if ignitionURL != "" {
		return nil
	}
	cond := apimeta.FindStatusCondition(co.Status.Conditions, v1alpha1.ConditionInfraEnvReady)
	if cond != nil && cond.Status == metav1.ConditionFalse && cond.Reason == reasonIgnitionPending &&
		cond.Message == reasonIgnitionPendingMessage {
		return nil
	}
	if err := r.setInfraEnvReady(ctx, co, metav1.ConditionFalse, reasonIgnitionPending,
		reasonIgnitionPendingMessage); err != nil {
		return err
	}
	return r.readAuthoritativeOrder(ctx, co)
}

// createMissingInfraEnv reacts to authoritative absence. A recorded Ready claim is
// cleared first, so a replacement object never inherits a stale success and the
// reset survives a missing pull secret. The pull secret and the owned,
// deterministically named InfraEnv are then created; that Create is the durable
// boundary. A missing prerequisite is returned as held evidence instead.
func (r *Reconciler) createMissingInfraEnv(
	ctx context.Context, co *v1alpha1.ClusterOrder,
) (infraEnvEvidence, ctrl.Result, bool, error) {
	key := client.ObjectKey{Name: co.Name + infraEnvNameSuffix, Namespace: co.Namespace}
	if apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionInfraEnvReady) {
		ctrllog.FromContext(ctx).Info("InfraEnv deleted while InfraEnvReady=True, resetting condition", "infraenv", key)
		if err := r.setInfraEnvReady(ctx, co, metav1.ConditionFalse, reasonInfraEnvRecreated,
			"InfraEnv was deleted; recreating"); err != nil {
			return infraEnvEvidence{}, ctrl.Result{}, false, err
		}
		// The reset advanced the object's resource version; refresh before the
		// remaining work so later optimistic patches are not based on a stale
		// snapshot.
		if err := r.readAuthoritativeOrder(ctx, co); err != nil {
			return infraEnvEvidence{}, ctrl.Result{}, false, err
		}
	}
	if err := r.ensurePullSecret(ctx, co); err != nil {
		return infraEnvEvidence{err: fmt.Errorf("ensuring pull secret: %w", err)}, ctrl.Result{}, false, nil
	}
	infraEnv, err := r.buildInfraEnv(co, key.Name)
	if err != nil {
		return infraEnvEvidence{err: err}, ctrl.Result{}, false, nil
	}
	if err := r.Create(ctx, infraEnv); err != nil && !apierrors.IsAlreadyExists(err) {
		return infraEnvEvidence{err: fmt.Errorf("creating infraenv %s: %w", key, err)}, ctrl.Result{}, false, nil
	}
	ctrllog.FromContext(ctx).Info("created InfraEnv", "infraenv", key.String())
	if err := r.setInfraEnvReady(ctx, co, metav1.ConditionFalse, reasonIgnitionPending,
		reasonIgnitionPendingMessage); err != nil {
		return infraEnvEvidence{}, ctrl.Result{}, true, err
	}
	return infraEnvEvidence{}, ctrl.Result{RequeueAfter: infraEnvRequeueInterval}, true, nil
}

// fetchDiscoveryIgnition is the create stage's input reader: it fetches and
// validates the observed InfraEnv's discovery ignition and reports Ready only for
// bytes it actually verified. A missing URL reports pending evidence and waits;
// a failed fetch or invalid JSON is an error and never a successful input.
func (r *Reconciler) fetchDiscoveryIgnition(
	ctx context.Context, co *v1alpha1.ClusterOrder, infraEnv *unstructured.Unstructured,
) ([]byte, ctrl.Result, error) {
	key := client.ObjectKeyFromObject(infraEnv)
	ignitionURL, _, err := unstructured.NestedString(infraEnv.Object, "status", "bootArtifacts", "discoveryIgnitionURL")
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("reading infraenv %s ignition URL: %w", key, err)
	}
	if ignitionURL == "" {
		if condErr := r.setInfraEnvReady(ctx, co, metav1.ConditionFalse, reasonIgnitionPending,
			reasonIgnitionPendingMessage); condErr != nil {
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
	return r.patchStatus(ctx, co, func(latest *v1alpha1.ClusterOrder) {
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
	base := co.DeepCopy()
	reader := r.apiReader
	if reader == nil {
		reader = r.Client
	}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(co), base); err != nil {
		return fmt.Errorf("reading ClusterOrder for InfraEnv UID recording: %w", err)
	}
	if !sameWorkerOrder(co, base) || base.Annotations[infraEnvUIDAnnotation] != co.Annotations[infraEnvUIDAnnotation] {
		return fmt.Errorf("ClusterOrder changed before InfraEnv UID recording")
	}
	next := base.DeepCopy()
	if next.Annotations == nil {
		next.Annotations = make(map[string]string)
	}
	next.Annotations[infraEnvUIDAnnotation] = uid
	if err := r.Patch(ctx, next, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("recording InfraEnv UID: %w", err)
	}
	return nil
}

// detectStaleIgnitionWorkers checks whether the InfraEnv was recreated since the last
// reconcile. If so, workers in WaitingForAgent phase have stale ignition and are marked
// Failed with AgentRegistrationTimeout so they enter the retry pipeline.
func (r *Reconciler) detectStaleIgnitionWorkers(
	ctx context.Context, co *v1alpha1.ClusterOrder, infraEnvUID string,
) (bool, ctrl.Result, error) {
	workers := classifyStaleIgnition(co, infraEnvUID)
	if workerSlicesEqual(co.Status.Workers, workers) {
		return false, ctrl.Result{}, nil
	}
	changed := make(map[string]v1alpha1.WorkerStatus)
	for i, worker := range co.Status.Workers {
		if workers[i].Phase == workerPhaseFailed && worker.Phase == workerPhaseWaitingForAgent {
			changed[workers[i].Name] = workers[i]
		}
	}
	if err := r.updateWorkerStatus(ctx, co, workers); err != nil {
		return false, ctrl.Result{}, err
	}
	for name := range changed {
		w := workerByName(workers, name)
		if w == nil {
			continue
		}
		observeProvisioningFailure(tenantOf(co), *w)
		r.recorder.Eventf(co, nil, corev1.EventTypeWarning, eventReasonStaleIgnition, "DetectStaleIgnition",
			"worker %s: marked failed due to stale ignition after InfraEnv recreation", w.Name)
	}
	return true, ctrl.Result{}, nil
}

func classifyStaleIgnition(co *v1alpha1.ClusterOrder, infraEnvUID string) []v1alpha1.WorkerStatus {
	workers := append([]v1alpha1.WorkerStatus(nil), co.Status.Workers...)
	storedUID := co.Annotations[infraEnvUIDAnnotation]
	if storedUID == "" || storedUID == infraEnvUID {
		return workers
	}
	now := metav1.Now()
	for i := range workers {
		if workers[i].Phase != workerPhaseWaitingForAgent {
			continue
		}
		workers[i].Phase = workerPhaseFailed
		workers[i].LastFailureReason = eventReasonAgentRegistrationTimeout
		workers[i].LastFailureMessage = "stale ignition: InfraEnv was recreated"
		workers[i].LastFailureTime = &now
	}
	return workers
}
