/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	infraEnvNameSuffix   = "-infraenv"
	pullSecretNameSuffix = "-pull-secret"

	// ignitionSizeWarningThreshold is 75% of the 64KB user_data limit; above it the controller
	// warns that discovery ignition is approaching the BMI user_data ceiling.
	ignitionSizeWarningThreshold = 48 * 1024
	infraEnvRequeueInterval      = 30 * time.Second

	// managementStateAnnotation / managementStateUnmanaged mirror the values the other osac
	// controllers honor (controller.ManagementStateUnmanaged = "unmanaged"; the annotation const
	// there is unexported). Kept in sync by the management-state skip test.
	managementStateAnnotation = "osac.openshift.io/management-state"
	managementStateUnmanaged  = "unmanaged"

	// clusterOrderIDLabel is the label set by the fulfillment-service provisioning flow, carrying
	// the fulfillment-service Cluster UUID. Same key as controller.osacClusterOrderIDLabel (unexported).
	clusterOrderIDLabel = "osac.openshift.io/clusterorder-uuid"

	eventReasonIgnitionSizeWarning = "DiscoveryIgnitionSizeWarning"
	reasonInfraEnvReady            = "InfraEnvReady"
	reasonIgnitionPending          = "IgnitionPending"
	reasonDiskImageNotFound        = "DiskImageNotFound"
	reasonDiskImageResolved        = "DiskImageResolved"

	systemTenant                 = "system"
	systemCatalogItemName        = "system-bmi-passthrough"
	systemBMITemplateID          = "osac.templates.bm_host_provisioning"
	clusterOrderLabel            = "osac.openshift.io/cluster-order"
	infraEnvAgentLabel           = "infraenvs.agent-install.openshift.io"
	ownerReferenceAnnotation     = "osac.openshift.io/owner-reference"
	reasonFulfillmentUnavailable = "FulfillmentServiceUnavailable"
	reasonFulfillmentAvailable   = "FulfillmentServiceAvailable"
	unavailableBackoff           = 5 * time.Minute

	workerKindBMI           = "BareMetalInstance"
	workerPhaseProvisioning = "Provisioning"

	eventReasonWorkerRetry     = "WorkerRetry"
	eventReasonWorkerFailed    = "WorkerFailed"
	reasonWorkersFailed        = "WorkersRetrying"
	reasonWorkersFailedCleared = "AllWorkersHealthy"

	infraEnvUIDAnnotation            = "osac.openshift.io/infraenv-uid"
	eventReasonStaleIgnition         = "StaleIgnition"
	reasonInfraEnvRecreated          = "InfraEnvRecreated"
	reasonStaleIgnitionWorkersMarked = "StaleIgnitionWorkersMarked"
	nodePoolResourceClassLabel       = "osac.openshift.io/resource_class"

	bmWorkerFinalizer = "osac.openshift.io/baremetalworker-finalizer"

	workerPhaseUnbinding = "Unbinding"
	workerPhaseDeleting  = "Deleting"

	agentUnbindingState              = "unbinding-pending-user-action"
	agentUnbindingTimeout            = 30 * time.Minute
	eventReasonWorkerDeleted         = "WorkerDeleted"
	eventReasonWorkerCreated         = "WorkerCreated"
	eventReasonAgentUnbindingTimeout = "AgentUnbindingTimeout"
	teardownRequeueInterval          = 30 * time.Second
)

var infraEnvGVK = schema.GroupVersionKind{Group: "agent-install.openshift.io", Version: agentInstallAPIVersion, Kind: "InfraEnv"}

// Reconciler is the bare-metal worker reconciler (BareMetalWorkerReconciler). It watches
// ClusterOrder resources with bare-metal node sets, ensures a cluster-specific InfraEnv exists,
// creates BMIs, correlates registered Agents by MAC, and converges NodePool replicas.
type Reconciler struct {
	client.Client
	apiReader             client.Reader
	scheme                *runtime.Scheme
	fulfillment           FulfillmentClient
	ignition              IgnitionFetcher
	recorder              events.EventRecorder
	clusterOrderNamespace string
	macResolver           MACResolver
}

// NewReconciler builds the bare-metal worker reconciler.
func NewReconciler(
	c client.Client,
	apiReader client.Reader,
	scheme *runtime.Scheme,
	fulfillment FulfillmentClient,
	ignition IgnitionFetcher,
	recorder events.EventRecorder,
	clusterOrderNamespace string,
) *Reconciler {
	r := &Reconciler{
		Client:                c,
		apiReader:             apiReader,
		scheme:                scheme,
		fulfillment:           fulfillment,
		ignition:              ignition,
		recorder:              recorder,
		clusterOrderNamespace: clusterOrderNamespace,
	}
	r.macResolver = r.resolveHostMACs
	return r
}

// SetMACResolver overrides the function used to resolve a BMI's host NIC MACs for agent
// correlation. The default is Reconciler.resolveHostMACs, which reads them from the BMI's
// status.hardware.nics (OSAC-4203); tests use it to inject a stub resolver.
func (r *Reconciler) SetMACResolver(resolver MACResolver) {
	r.macResolver = resolver
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=clusterorders,verbs=get;list;watch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=clusterorders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create
// +kubebuilder:rbac:groups=agent-install.openshift.io,resources=infraenvs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agent-install.openshift.io,resources=agents,verbs=get;list;watch;patch;delete
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=nodepools,verbs=get;list;watch;patch;update

// Reconcile ensures the InfraEnv for a bare-metal ClusterOrder exists, creates BMIs, correlates
// registered Agents by MAC, and converges NodePool replicas.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	co := &v1alpha1.ClusterOrder{}
	if err := r.Get(ctx, req.NamespacedName, co); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !co.DeletionTimestamp.IsZero() {
		return r.handleClusterDeletion(ctx, co)
	}
	if v, ok := co.Annotations[managementStateAnnotation]; ok && v == managementStateUnmanaged {
		return ctrl.Result{}, nil
	}
	if !co.HasBareMetalNodeSet() {
		return ctrl.Result{}, nil
	}

	// Refresh the worker gauges from the full ClusterOrder set at the end of every
	// bare-metal reconcile, so desired/ready/failed converge as status changes.
	defer r.syncWorkerGauges(ctx)

	if controllerutil.AddFinalizer(co, bmWorkerFinalizer) {
		if err := r.Update(ctx, co); err != nil {
			return ctrl.Result{}, err
		}
	}

	if changed, err := r.rebuildWorkerState(ctx, co); err != nil {
		return ctrl.Result{}, fmt.Errorf("rebuilding worker state: %w", err)
	} else if changed {
		if err := r.apiReader.Get(ctx, req.NamespacedName, co); err != nil {
			return ctrl.Result{}, fmt.Errorf("re-reading ClusterOrder after rebuild: %w", err)
		}
	}

	if err := r.ensurePullSecret(ctx, co); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring pull secret: %w", err)
	}

	ignition, infraEnvUID, res, err := r.ensureInfraEnv(ctx, co)
	if err != nil || !res.IsZero() {
		return res, err
	}

	if r.detectStaleIgnitionWorkers(ctx, co, infraEnvUID) {
		if err := r.apiReader.Get(ctx, req.NamespacedName, co); err != nil {
			return ctrl.Result{}, fmt.Errorf("re-reading ClusterOrder after stale detection: %w", err)
		}
	}
	r.trackInfraEnvUID(ctx, co, infraEnvUID)

	image, res, err := r.resolveDiskImage(ctx, co)
	if err != nil || !res.IsZero() {
		return res, err
	}

	res, err = r.reconcileWorkers(ctx, co, image, ignition)
	if err != nil || !res.IsZero() {
		return res, err
	}

	// Re-read the ClusterOrder to pick up the workers just written by reconcileWorkers.
	if err := r.apiReader.Get(ctx, req.NamespacedName, co); err != nil {
		return ctrl.Result{}, fmt.Errorf("re-reading ClusterOrder: %w", err)
	}

	if err := r.processTeardownWorkers(ctx, co, req.NamespacedName); err != nil {
		return ctrl.Result{}, err
	}

	workers, res, err := r.correlateAgents(ctx, co, co.Status.Workers)
	if err != nil {
		return ctrl.Result{}, err
	}

	npRes, npErr := r.reconcileNodePoolReplicas(ctx, co)
	if npErr != nil {
		return ctrl.Result{}, npErr
	}

	if err := r.updateWorkerStatusWithCorrelation(ctx, co, workers); err != nil {
		return ctrl.Result{}, err
	}

	if !res.IsZero() {
		return res, nil
	}
	if hasTeardownWorkers(workers) {
		return ctrl.Result{RequeueAfter: teardownRequeueInterval}, nil
	}
	return npRes, nil
}

func (r *Reconciler) processTeardownWorkers(ctx context.Context, co *v1alpha1.ClusterOrder, key client.ObjectKey) error {
	teardownWorkers := co.Status.Workers
	teardownWorkers = r.handleUnbindingWorkers(ctx, co, teardownWorkers)
	teardownWorkers = r.handleDeletingWorkers(ctx, co, teardownWorkers)
	if !workerSlicesEqual(co.Status.Workers, teardownWorkers) {
		if err := r.updateWorkerStatus(ctx, co, teardownWorkers); err != nil {
			return fmt.Errorf("updating worker status after teardown: %w", err)
		}
		if err := r.apiReader.Get(ctx, key, co); err != nil {
			return fmt.Errorf("re-reading ClusterOrder after teardown: %w", err)
		}
	}
	return nil
}

func (r *Reconciler) handleClusterDeletion(ctx context.Context, co *v1alpha1.ClusterOrder) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(co, bmWorkerFinalizer) {
		return ctrl.Result{}, nil
	}

	log := ctrllog.FromContext(ctx)
	log.Info("handling cluster deletion", "clusterOrder", co.Name)

	workers := co.Status.Workers
	changed := false
	for i := range workers {
		if workers[i].Kind != workerKindBMI {
			continue
		}
		if workers[i].Phase != workerPhaseDeleting {
			workers[i].Phase = workerPhaseDeleting
			changed = true
		}
	}

	workers = r.handleDeletingWorkers(ctx, co, workers)

	if changed || !workerSlicesEqual(co.Status.Workers, workers) {
		if err := r.updateWorkerStatus(ctx, co, workers); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating worker status during cluster deletion: %w", err)
		}
	}

	if len(workers) > 0 {
		return ctrl.Result{RequeueAfter: teardownRequeueInterval}, nil
	}

	latest := &v1alpha1.ClusterOrder{}
	if err := r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
		return ctrl.Result{}, fmt.Errorf("re-reading ClusterOrder for finalizer removal: %w", err)
	}
	if controllerutil.RemoveFinalizer(latest, bmWorkerFinalizer) {
		if err := r.Update(ctx, latest); err != nil {
			return ctrl.Result{}, fmt.Errorf("removing baremetalworker finalizer: %w", err)
		}
	}
	log.Info("cluster deletion complete, finalizer removed", "clusterOrder", co.Name)
	return ctrl.Result{}, nil
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
) {
	if uid == "" {
		return
	}
	if co.Annotations != nil && co.Annotations[infraEnvUIDAnnotation] == uid {
		return
	}
	base := co.DeepCopy()
	if co.Annotations == nil {
		co.Annotations = make(map[string]string)
	}
	co.Annotations[infraEnvUIDAnnotation] = uid
	if err := r.Patch(ctx, co, client.MergeFrom(base)); err != nil {
		ctrllog.FromContext(ctx).Error(err, "patching infraenv-uid annotation")
	}
}

// detectStaleIgnitionWorkers checks whether the InfraEnv was recreated since the last
// reconcile. If so, workers in WaitingForAgent phase have stale ignition and are marked
// Failed with AgentRegistrationTimeout so they enter the retry pipeline.
func (r *Reconciler) detectStaleIgnitionWorkers(
	ctx context.Context, co *v1alpha1.ClusterOrder, infraEnvUID string,
) bool {
	storedUID := ""
	if co.Annotations != nil {
		storedUID = co.Annotations[infraEnvUIDAnnotation]
	}
	if storedUID == "" || storedUID == infraEnvUID {
		return false
	}

	log := ctrllog.FromContext(ctx)
	marked := false
	now := metav1.Now()
	for i := range co.Status.Workers {
		w := &co.Status.Workers[i]
		if w.Phase != workerPhaseWaitingForAgent {
			continue
		}
		log.Info("marking worker as stale-ignition failure", "worker", w.Name)
		w.Phase = workerPhaseFailed
		w.LastFailureReason = eventReasonAgentRegistrationTimeout
		w.LastFailureMessage = "stale ignition: InfraEnv was recreated"
		w.LastFailureTime = &now
		observeProvisioningFailure(tenantOf(co), *w)
		marked = true
		r.recorder.Eventf(co, nil, corev1.EventTypeWarning, eventReasonStaleIgnition, "DetectStaleIgnition",
			"worker %s: marked failed due to stale ignition after InfraEnv recreation", w.Name)
	}

	if marked {
		if err := r.updateWorkerStatus(ctx, co, co.Status.Workers); err != nil {
			log.Error(err, "updating worker status after stale ignition detection")
		}
	}
	return marked
}

// ensureSystemCatalogItem creates the system-owned BareMetalInstanceCatalogItem
// ("system-bmi-passthrough") if it doesn't already exist. Empty field_definitions means all
// fields are unlocked — hardware profile is determined by BareMetalInstanceType, not by this
// catalog item. AlreadyExists is handled gracefully for concurrent reconcile races.
func (r *Reconciler) ensureSystemCatalogItem(ctx context.Context, co *v1alpha1.ClusterOrder) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	filter := fmt.Sprintf(`this.metadata.name == "%s"`, systemCatalogItemName)
	items, err := r.fulfillment.ListBareMetalInstanceCatalogItems(ctx, filter)
	if res, handled := r.handleUnavailable(ctx, co, err); handled {
		return res, nil
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("listing catalog items: %w", err)
	}
	if len(items) > 0 {
		return ctrl.Result{}, nil
	}

	ci := privatev1.BareMetalInstanceCatalogItem_builder{
		Metadata: privatev1.Metadata_builder{
			Tenant: systemTenant,
			Name:   systemCatalogItemName,
		}.Build(),
		Template: privatev1.BareMetalInstanceTemplateReference_builder{
			Id: systemBMITemplateID,
		}.Build(),
	}.Build()
	ci.SetTitle("System BMI Pass-through")
	ci.SetPublished(true)

	_, err = r.fulfillment.CreateBareMetalInstanceCatalogItem(ctx, ci)
	if err != nil {
		if res, handled := r.handleUnavailable(ctx, co, err); handled {
			return res, nil
		}
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			log.Info("system catalog item created concurrently, continuing")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("creating system catalog item: %w", err)
	}

	log.Info("created system catalog item", "name", systemCatalogItemName)
	return ctrl.Result{}, nil
}

// resolveDiskImage reads the Cluster's ClusterVersion reference via the fulfillment-service
// private API and validates the referenced DiskImage. It returns the canonical DiskImage
// reference; fulfillment-service resolves its source URL when it materializes the provider CR.
// It re-resolves on every reconcile so a ClusterVersion upgrade takes effect without controller
// restart. Returns a nil reference (and no error) when the ClusterVersion carries no disk_image
// reference; reconciliation is requeued so workers are not created until a usable image is
// available.
func (r *Reconciler) resolveDiskImage(
	ctx context.Context, co *v1alpha1.ClusterOrder,
) (*privatev1.DiskImageReference, ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	clusterID := co.Labels[clusterOrderIDLabel]
	if clusterID == "" {
		log.Info("ClusterOrder missing clusterorder-uuid label, requeuing")
		return nil, ctrl.Result{RequeueAfter: infraEnvRequeueInterval}, nil
	}

	cluster, err := r.fulfillment.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("getting cluster %s: %w", clusterID, err)
	}

	versionID := cluster.GetSpec().GetVersion().GetId()
	if versionID == "" {
		return nil, ctrl.Result{}, fmt.Errorf("cluster %s has no version reference", clusterID)
	}

	cv, err := r.fulfillment.GetClusterVersion(ctx, versionID)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("getting cluster version %s: %w", versionID, err)
	}

	diskImage := cv.GetSpec().GetDiskImage()
	diskImageKey := refKeyStr(diskImage)
	if diskImageKey == "" {
		log.Info("ClusterVersion has no disk_image, setting RHCOSImageNotFound", "clusterVersion", versionID)
		if condErr := r.setRHCOSImageNotFound(ctx, co, metav1.ConditionTrue, reasonDiskImageNotFound,
			fmt.Sprintf("ClusterVersion %s has no disk_image reference", versionID)); condErr != nil {
			return nil, ctrl.Result{}, condErr
		}
		return nil, ctrl.Result{RequeueAfter: infraEnvRequeueInterval}, nil
	}

	di, err := r.fulfillment.GetDiskImage(ctx, diskImageKey)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("getting disk image %s: %w", diskImageKey, err)
	}
	image := privatev1.DiskImageReference_builder{
		Id:   di.GetId(),
		Name: di.GetMetadata().GetName(),
	}.Build()

	if apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionRHCOSImageNotFound) {
		if condErr := r.setRHCOSImageNotFound(ctx, co, metav1.ConditionFalse, reasonDiskImageResolved,
			"disk_image reference resolved"); condErr != nil {
			return nil, ctrl.Result{}, condErr
		}
	}

	return image, ctrl.Result{}, nil
}

// refKeyStr derives the lookup key for a DiskImage reference, mirroring the ComputeInstance
// reconciler's RefKeyStr (OSAC-3724): prefer the id, fall back to the name. The fulfillment-service
// ClusterVersion validation backfills the id on write, so the id branch is the usual path, but a
// name-only reference must still resolve identically to keep the two paths consistent. Kept as a
// local helper because the canonical RefKeyStr lives in the separate fulfillment-service Go module
// and cannot be imported here.
func refKeyStr(ref *privatev1.DiskImageReference) string {
	if ref.GetId() != "" {
		return ref.GetId()
	}
	return ref.GetName()
}

func (r *Reconciler) setRHCOSImageNotFound(
	ctx context.Context, co *v1alpha1.ClusterOrder, status metav1.ConditionStatus, reason, message string,
) error {
	return r.patchStatusWithRetry(ctx, co, func(latest *v1alpha1.ClusterOrder) {
		latest.SetStatusCondition(v1alpha1.ConditionRHCOSImageNotFound, status, message, reason)
	})
}

// reconcileWorkers creates one BareMetalInstance per requested bare-metal worker, idempotently
// (list-before-create + AlreadyExists handling), and updates status.workers. Preserves existing
// worker phases (Binding, Ready, etc.) for workers that have already been correlated.
// Failed workers are retried with escalating backoff: the failed BMI is deleted, and a
// replacement is created once NextRetryTime has passed.
func (r *Reconciler) reconcileWorkers(
	ctx context.Context, co *v1alpha1.ClusterOrder, image *privatev1.DiskImageReference, ignition []byte,
) (ctrl.Result, error) {
	filter := fmt.Sprintf(`this.metadata.labels["%s"] == "%s"`, clusterOrderLabel, co.Name)
	existing, err := r.fulfillment.ListBareMetalInstances(ctx, filter)
	if res, handled := r.handleUnavailable(ctx, co, err); handled {
		return res, nil
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("listing BMIs for %s: %w", co.Name, err)
	}

	existingByName := bmisByName(existing)
	ignitionRaw := string(ignition)

	r.handleFailedWorkers(ctx, co, existingByName)

	workers, res, err := r.reconcileNodeSets(ctx, co, existingByName, image, ignitionRaw, filter)
	if err != nil || !res.IsZero() {
		return res, err
	}

	if apimeta.IsStatusConditionTrue(co.Status.Conditions, v1alpha1.ConditionFulfillmentServiceUnavailable) {
		if condErr := r.setFulfillmentServiceUnavailable(ctx, co, metav1.ConditionFalse,
			reasonFulfillmentAvailable, "fulfillment service recovered"); condErr != nil {
			return ctrl.Result{}, condErr
		}
	}

	retryResult := r.earliestRetryRequeue(workers)

	if err := r.updateWorkerStatus(ctx, co, workers); err != nil {
		return ctrl.Result{}, err
	}

	return retryResult, nil
}

// reconcileNodeSets iterates bare-metal node requests and ensures a BMI exists for each worker
// slot. Reuses existing WorkerStatus entries (preserving phase/failure fields) for workers that
// already exist; only creates new entries for genuinely new workers.
func (r *Reconciler) reconcileNodeSets(
	ctx context.Context, co *v1alpha1.ClusterOrder,
	existingByName map[string]*privatev1.BareMetalInstance,
	image *privatev1.DiskImageReference, ignitionRaw, filter string,
) ([]v1alpha1.WorkerStatus, ctrl.Result, error) {
	existingWorkers := workersByName(co.Status.Workers)
	var workers []v1alpha1.WorkerStatus
	globalIndex := 0

	for i := range co.Spec.NodeRequests {
		nr := &co.Spec.NodeRequests[i]
		if !nr.IsBareMetal() {
			continue
		}

		instanceType, res, err := r.resolveNodeSetInstanceType(ctx, co, nr.BareMetal.InstanceType)
		if err != nil {
			return nil, ctrl.Result{}, err
		}
		if !res.IsZero() {
			return nil, res, nil
		}

		var fabricInterface string
		if co.Spec.NetworkAttachment != nil && co.Spec.NetworkAttachment.SubnetRef != "" {
			fabricInterface, err = resolveFabricInterface(instanceType)
			if err != nil {
				return nil, ctrl.Result{}, err
			}
		}

		if res, err := r.ensureSystemCatalogItem(ctx, co); err != nil || !res.IsZero() {
			return nil, res, err
		}

		for j := 0; j < nr.NumberOfNodes; j++ {
			workerName := fmt.Sprintf("%s-worker-%d", co.Name, globalIndex)
			globalIndex++

			if prev, ok := existingWorkers[workerName]; ok {
				res, err := r.retryFailedWorker(ctx, co, nr, &prev, image, ignitionRaw, filter, fabricInterface)
				if err != nil {
					return nil, ctrl.Result{}, err
				}
				if !res.IsZero() {
					return nil, res, nil
				}
				workers = append(workers, prev)
				continue
			}

			ws, res, err := r.ensureWorkerBMI(ctx, co, nr, workerName, existingByName, image, ignitionRaw, filter, fabricInterface)
			if err != nil {
				return nil, ctrl.Result{}, err
			}
			if !res.IsZero() {
				return nil, res, nil
			}
			workers = append(workers, ws)
		}
	}

	excess := r.identifyExcessWorkers(co, workers)
	if len(excess) > 0 {
		workers = r.handleScaleDown(ctx, co, workers, excess)
	}

	return workers, ctrl.Result{}, nil
}

// retryFailedWorker creates a replacement BMI for a failed worker whose retry is due.
// Returns a non-zero result if the fulfillment service is unavailable. If the worker is not
// eligible for retry, this is a no-op.
func (r *Reconciler) retryFailedWorker(
	ctx context.Context, co *v1alpha1.ClusterOrder,
	nr *v1alpha1.NodeRequest, prev *v1alpha1.WorkerStatus,
	image *privatev1.DiskImageReference, ignitionRaw, filter, fabricInterface string,
) (ctrl.Result, error) {
	if prev.Phase != workerPhaseFailed || prev.ResourceID != "" || !isRetryDue(*prev) {
		return ctrl.Result{}, nil
	}
	log := ctrllog.FromContext(ctx)
	bmi, res, err := r.ensureBMI(ctx, co, *nr, prev.Name, image, ignitionRaw, filter, fabricInterface)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !res.IsZero() {
		return res, nil
	}
	log.Info("created replacement BMI for failed worker", "name", prev.Name, "attempt", prev.AttemptCount, "id", bmi.GetId())
	prev.ResourceID = bmi.GetId()
	prev.Phase = workerPhaseWaitingForAgent
	prev.NextRetryTime = nil
	r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonWorkerRetry, "RetryWorker",
		"worker %s: retry attempt %d", prev.Name, prev.AttemptCount)
	return ctrl.Result{}, nil
}

// ensureWorkerBMI creates a BMI for a new worker slot, skipping creation if a BMI with the
// same name already exists (list-before-create idempotency after controller restart).
func (r *Reconciler) ensureWorkerBMI(
	ctx context.Context, co *v1alpha1.ClusterOrder,
	nr *v1alpha1.NodeRequest, workerName string,
	existingByName map[string]*privatev1.BareMetalInstance,
	image *privatev1.DiskImageReference, ignitionRaw, filter, fabricInterface string,
) (v1alpha1.WorkerStatus, ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	if bmi, ok := existingByName[workerName]; ok {
		log.Info("worker BMI already exists, skipping create", "name", workerName)
		return newWorkerStatus(nr.ResourceClass, nr.BareMetal.InstanceType, workerName, bmi.GetId()), ctrl.Result{}, nil
	}

	bmi, res, err := r.ensureBMI(ctx, co, *nr, workerName, image, ignitionRaw, filter, fabricInterface)
	if err != nil {
		return v1alpha1.WorkerStatus{}, ctrl.Result{}, err
	}
	if !res.IsZero() {
		return v1alpha1.WorkerStatus{}, res, nil
	}
	log.Info("created BMI", "name", workerName, "id", bmi.GetId())
	r.recorder.Eventf(co, nil, corev1.EventTypeNormal, eventReasonWorkerCreated, "CreateWorker",
		"worker %s: BMI %s created", workerName, bmi.GetId())
	return newWorkerStatus(nr.ResourceClass, nr.BareMetal.InstanceType, workerName, bmi.GetId()), ctrl.Result{}, nil
}

// handleFailedWorkers processes workers in Failed phase: deletes their BMIs via the
// fulfillment API, increments attemptCount, computes failure-appropriate backoff, and
// sets NextRetryTime. The failed BMI's ResourceID is cleared so reconcileNodeSets
// creates a replacement when the retry is due.
func (r *Reconciler) handleFailedWorkers(
	ctx context.Context, co *v1alpha1.ClusterOrder,
	existingByName map[string]*privatev1.BareMetalInstance,
) {
	log := ctrllog.FromContext(ctx)
	for i := range co.Status.Workers {
		w := &co.Status.Workers[i]
		if w.Phase != workerPhaseFailed || w.ResourceID == "" {
			continue
		}
		if err := r.fulfillment.DeleteBareMetalInstance(ctx, w.ResourceID); err != nil {
			log.Error(err, "deleting failed BMI", "worker", w.Name, "bmiID", w.ResourceID)
			continue
		}
		log.Info("deleted failed BMI", "worker", w.Name, "bmiID", w.ResourceID)
		delete(existingByName, w.Name)

		w.AttemptCount++
		category := ClassifyFailure(w.LastFailureReason)
		backoff := ComputeBackoff(category, w.AttemptCount)
		now := metav1.Now()
		retryTime := metav1.NewTime(now.Add(backoff))
		w.NextRetryTime = &retryTime
		w.ResourceID = ""
		w.ReadySince = nil
		r.recorder.Eventf(co, nil, corev1.EventTypeWarning, eventReasonWorkerFailed, "HandleFailedWorker",
			"worker %s: failed (attempt %d, reason %s), next retry in %s",
			w.Name, w.AttemptCount, w.LastFailureReason, backoff)
	}
}

// isRetryDue returns true if a Failed worker's NextRetryTime has passed (or is nil).
func isRetryDue(w v1alpha1.WorkerStatus) bool {
	if w.NextRetryTime == nil {
		return true
	}
	return !time.Now().Before(w.NextRetryTime.Time)
}

// earliestRetryRequeue returns a RequeueAfter result for the earliest pending retry
// among Failed workers, or a zero result if no retries are pending.
func (r *Reconciler) earliestRetryRequeue(workers []v1alpha1.WorkerStatus) ctrl.Result {
	var earliest time.Time
	for i := range workers {
		w := &workers[i]
		if w.Phase != workerPhaseFailed || w.NextRetryTime == nil {
			continue
		}
		if earliest.IsZero() || w.NextRetryTime.Time.Before(earliest) {
			earliest = w.NextRetryTime.Time
		}
	}
	if earliest.IsZero() {
		return ctrl.Result{}
	}
	delay := time.Until(earliest)
	if delay < time.Second {
		delay = time.Second
	}
	return ctrl.Result{RequeueAfter: delay}
}

func workersByName(workers []v1alpha1.WorkerStatus) map[string]v1alpha1.WorkerStatus {
	m := make(map[string]v1alpha1.WorkerStatus, len(workers))
	for _, w := range workers {
		m[w.Name] = w
	}
	return m
}

func (r *Reconciler) resolveNodeSetInstanceType(
	ctx context.Context, co *v1alpha1.ClusterOrder, instanceTypeName string,
) (*privatev1.BareMetalInstanceType, ctrl.Result, error) {
	it, err := r.fulfillment.GetBareMetalInstanceType(ctx, instanceTypeName)
	if res, handled := r.handleUnavailable(ctx, co, err); handled {
		return nil, res, nil
	}
	if err != nil {
		if status.Code(err) == codes.NotFound {
			ctrllog.FromContext(ctx).Info("BareMetalInstanceType not found, requeuing", "instanceType", instanceTypeName)
			return nil, ctrl.Result{RequeueAfter: infraEnvRequeueInterval}, nil
		}
		return nil, ctrl.Result{}, fmt.Errorf("getting BareMetalInstanceType %s: %w", instanceTypeName, err)
	}
	if it == nil {
		ctrllog.FromContext(ctx).Info("BareMetalInstanceType is empty, requeuing", "instanceType", instanceTypeName)
		return nil, ctrl.Result{RequeueAfter: infraEnvRequeueInterval}, nil
	}
	return it, ctrl.Result{}, nil
}

// ensureBMI creates a single BareMetalInstance, handling the AlreadyExists race by re-listing.
// Returns the BMI, a non-zero result on unavailability backoff, or an error.
func (r *Reconciler) ensureBMI(
	ctx context.Context, co *v1alpha1.ClusterOrder, nodeSet v1alpha1.NodeRequest,
	workerName string, image *privatev1.DiskImageReference, ignitionRaw, filter, fabricInterface string,
) (*privatev1.BareMetalInstance, ctrl.Result, error) {
	req := r.buildBMICreateRequest(co, nodeSet, workerName, image, ignitionRaw, fabricInterface)
	created, err := r.fulfillment.CreateBareMetalInstance(ctx, req)
	if err == nil {
		return created, ctrl.Result{}, nil
	}

	if res, handled := r.handleUnavailable(ctx, co, err); handled {
		return nil, res, nil
	}

	if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
		bmi, listErr := r.findBMIByName(ctx, filter, workerName)
		if listErr != nil {
			return nil, ctrl.Result{}, listErr
		}
		return bmi, ctrl.Result{}, nil
	}

	return nil, ctrl.Result{}, fmt.Errorf("creating BMI %s: %w", workerName, err)
}

// handleUnavailable checks whether err is ErrFulfillmentServiceUnavailable and, if so, sets the
// condition and returns a backoff result. Returns (result, true) when handled, (zero, false) otherwise.
func (r *Reconciler) handleUnavailable(ctx context.Context, co *v1alpha1.ClusterOrder, err error) (ctrl.Result, bool) {
	if !errors.Is(err, ErrFulfillmentServiceUnavailable) {
		return ctrl.Result{}, false
	}
	ctrllog.FromContext(ctx).Info("fulfillment service unavailable, backing off")
	if condErr := r.setFulfillmentServiceUnavailable(ctx, co, metav1.ConditionTrue,
		reasonFulfillmentUnavailable, err.Error()); condErr != nil {
		return ctrl.Result{}, false
	}
	return ctrl.Result{RequeueAfter: unavailableBackoff}, true
}

// findBMIByName re-lists BMIs and returns the one matching the given name.
func (r *Reconciler) findBMIByName(ctx context.Context, filter, name string) (*privatev1.BareMetalInstance, error) {
	ctrllog.FromContext(ctx).Info("BMI create returned AlreadyExists, re-listing", "name", name)
	refreshed, err := r.fulfillment.ListBareMetalInstances(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("re-listing BMIs after AlreadyExists: %w", err)
	}
	for _, bmi := range refreshed {
		if bmi.GetMetadata().GetName() == name {
			return bmi, nil
		}
	}
	return nil, fmt.Errorf("BMI %s returned AlreadyExists but not found in re-list", name)
}

func newWorkerStatus(nodeSet, instanceType, name, resourceID string) v1alpha1.WorkerStatus {
	return v1alpha1.WorkerStatus{
		NodeSet:           nodeSet,
		InstanceType:      instanceType,
		Name:              name,
		Kind:              workerKindBMI,
		ResourceID:        resourceID,
		Phase:             workerPhaseWaitingForAgent,
		CreationTimestamp: metav1.Now(),
	}
}

// resolveFabricInterface returns the name of the first network port with role "fabric" from the
// BareMetalInstanceType. Returns an error if no fabric port is found.
func resolveFabricInterface(it *privatev1.BareMetalInstanceType) (string, error) {
	for _, port := range it.GetSpec().GetHardware().GetNetworkPorts() {
		if port.GetRole() == "fabric" {
			return port.GetName(), nil
		}
	}
	return "", fmt.Errorf("BareMetalInstanceType %s has no fabric-role network port", it.GetMetadata().GetName())
}

func bmisByName(bmis []*privatev1.BareMetalInstance) map[string]*privatev1.BareMetalInstance {
	m := make(map[string]*privatev1.BareMetalInstance, len(bmis))
	for _, bmi := range bmis {
		m[bmi.GetMetadata().GetName()] = bmi
	}
	return m
}

func (r *Reconciler) buildBMICreateRequest(
	co *v1alpha1.ClusterOrder, nodeSet v1alpha1.NodeRequest, workerName string,
	image *privatev1.DiskImageReference, ignitionRaw, fabricInterface string,
) *privatev1.BareMetalInstance {
	labels := map[string]string{clusterOrderLabel: co.Name}
	annotations := map[string]string{ownerReferenceAnnotation: fmt.Sprintf("ClusterOrder/%s", co.Name)}

	var netAttachments []*privatev1.BareMetalNetworkAttachment
	if na := co.Spec.NetworkAttachment; na != nil && na.SubnetRef != "" {
		sgRefs := make([]*privatev1.SecurityGroupLocalReference, 0, len(na.SecurityGroupRefs))
		for _, sg := range na.SecurityGroupRefs {
			sgRefs = append(sgRefs, privatev1.SecurityGroupLocalReference_builder{Name: sg}.Build())
		}
		primary := true
		netAttachments = []*privatev1.BareMetalNetworkAttachment{
			privatev1.BareMetalNetworkAttachment_builder{
				Subnet:         privatev1.SubnetLocalReference_builder{Name: na.SubnetRef}.Build(),
				SecurityGroups: sgRefs,
				Interface:      &fabricInterface,
				Primary:        &primary,
			}.Build(),
		}
	}

	var instanceType *privatev1.BareMetalInstanceTypeLocalReference
	if nodeSet.BareMetal.InstanceType != "" {
		instanceType = privatev1.BareMetalInstanceTypeLocalReference_builder{
			Name: nodeSet.BareMetal.InstanceType,
		}.Build()
	}

	specBuilder := privatev1.BareMetalInstanceSpec_builder{
		CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{
			Name: systemCatalogItemName,
		}.Build(),
		DiskImage:          image,
		UserData:           &ignitionRaw,
		InstanceType:       instanceType,
		NetworkAttachments: netAttachments,
	}
	if sshKey := r.resolveSSHPublicKey(co); sshKey != "" {
		specBuilder.SshPublicKey = &sshKey
	}

	return privatev1.BareMetalInstance_builder{
		Metadata: privatev1.Metadata_builder{
			Tenant:      systemTenant,
			Name:        workerName,
			Labels:      labels,
			Annotations: annotations,
		}.Build(),
		Spec: specBuilder.Build(),
	}.Build()
}

func (r *Reconciler) setFulfillmentServiceUnavailable(
	ctx context.Context, co *v1alpha1.ClusterOrder, condStatus metav1.ConditionStatus, reason, message string,
) error {
	return r.patchStatusWithRetry(ctx, co, func(latest *v1alpha1.ClusterOrder) {
		latest.SetStatusCondition(v1alpha1.ConditionFulfillmentServiceUnavailable, condStatus, message, reason)
	})
}

func (r *Reconciler) updateWorkerStatus(
	ctx context.Context, co *v1alpha1.ClusterOrder, workers []v1alpha1.WorkerStatus,
) error {
	return r.patchStatusWithRetry(ctx, co, func(latest *v1alpha1.ClusterOrder) {
		latest.Status.Workers = workers
	})
}

// patchStatusWithRetry re-reads the ClusterOrder and applies the mutate function to its status,
// retrying on conflict with optimistic locking.
func (r *Reconciler) patchStatusWithRetry(
	ctx context.Context, co *v1alpha1.ClusterOrder, mutate func(*v1alpha1.ClusterOrder),
) error {
	key := client.ObjectKeyFromObject(co)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.ClusterOrder{}
		if err := r.apiReader.Get(ctx, key, latest); err != nil {
			return err
		}
		base := latest.DeepCopy()
		mutate(latest)
		return r.Status().Patch(ctx, latest, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	})
}

func namespacePredicate(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == namespace
	})
}

// SetupWithManager registers the reconciler, watching ClusterOrder in the configured namespace.
// Agent and NodePool watches are gated on CRD presence to avoid hard startup dependencies.
func (r *Reconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return fmt.Errorf("local manager is nil")
	}

	log := ctrl.Log.WithName("baremetalworker")
	bld := ctrl.NewControllerManagedBy(localMgr).
		Named("baremetalworker").
		For(&v1alpha1.ClusterOrder{}, builder.WithPredicates(namespacePredicate(r.clusterOrderNamespace)))

	if crdExists(mgr, agentGVK) {
		agentObj := &unstructured.Unstructured{}
		agentObj.SetGroupVersionKind(agentGVK)
		bld = bld.Watches(agentObj, handler.EnqueueRequestsFromMapFunc(r.agentToClusterOrderMapper()))
		log.Info("watching Agent CRs for MAC correlation")
	} else {
		log.Info("Agent CRD not found, skipping Agent watch")
	}

	npGVK := schema.GroupVersionKind{Group: "hypershift.openshift.io", Version: "v1beta1", Kind: "NodePool"}
	if crdExists(mgr, npGVK) {
		npObj := &unstructured.Unstructured{}
		npObj.SetGroupVersionKind(npGVK)
		bld = bld.Watches(npObj, handler.EnqueueRequestsFromMapFunc(r.labelToClusterOrderMapper("osac.openshift.io/clusterorder")))
		log.Info("watching NodePool CRs for replica convergence")
	} else {
		log.Info("NodePool CRD not found, skipping NodePool watch")
	}

	return bld.Complete(r)
}

// syncWorkerGauges recomputes the desired/ready/failed worker gauges from all ClusterOrders in
// the configured namespace, following the aggregate-gauge convention (see updateWorkerGauges).
// Called at the end of each reconcile so the gauges converge as worker status changes and drop
// series for deleted clusters. Errors are logged and swallowed — a failed metrics refresh must
// not fail reconciliation.
func (r *Reconciler) syncWorkerGauges(ctx context.Context) {
	list := &v1alpha1.ClusterOrderList{}
	if err := r.List(ctx, list, client.InNamespace(r.clusterOrderNamespace)); err != nil {
		ctrllog.FromContext(ctx).Error(err, "listing ClusterOrders for worker metrics")
		return
	}
	updateWorkerGauges(list.Items)
}

// crdExists checks whether a CRD for the given GVK is registered in the API server.
func crdExists(mgr mcmanager.Manager, gvk schema.GroupVersionKind) bool {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return false
	}
	mapper := localMgr.GetRESTMapper()
	_, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	return err == nil
}

// agentToClusterOrderMapper maps Agent events to ClusterOrder reconcile requests.
// Unbound agents carry infraenvs.agent-install.openshift.io: <order>-infraenv,
// while bound agents carry clusterOrderLabel.
func (r *Reconciler) agentToClusterOrderMapper() handler.MapFunc {
	return func(_ context.Context, obj client.Object) []ctrl.Request {
		labels := obj.GetLabels()
		if coName := labels[clusterOrderLabel]; coName != "" {
			return []ctrl.Request{{
				NamespacedName: client.ObjectKey{Name: coName, Namespace: r.clusterOrderNamespace},
			}}
		}
		if infraEnv := labels[infraEnvAgentLabel]; infraEnv != "" {
			coName := strings.TrimSuffix(infraEnv, infraEnvNameSuffix)
			return []ctrl.Request{{
				NamespacedName: client.ObjectKey{Name: coName, Namespace: r.clusterOrderNamespace},
			}}
		}
		return nil
	}
}

// labelToClusterOrderMapper returns a MapFunc that maps events to ClusterOrder reconcile
// requests by reading a label value as the ClusterOrder name.
func (r *Reconciler) labelToClusterOrderMapper(labelKey string) handler.MapFunc {
	return func(_ context.Context, obj client.Object) []ctrl.Request {
		coName := obj.GetLabels()[labelKey]
		if coName == "" {
			return nil
		}
		return []ctrl.Request{{
			NamespacedName: client.ObjectKey{Name: coName, Namespace: r.clusterOrderNamespace},
		}}
	}
}
