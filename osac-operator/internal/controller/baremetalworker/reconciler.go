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
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"google.golang.org/grpc"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	// managementStateAnnotation / managementStateUnmanaged mirror the values the other osac
	// controllers honor (controller.ManagementStateUnmanaged = "unmanaged"; the annotation const
	// there is unexported). Kept in sync by the management-state skip test.
	managementStateAnnotation = "osac.openshift.io/management-state"
	managementStateUnmanaged  = "unmanaged"
	bmWorkerFinalizer         = "osac.openshift.io/baremetalworker-finalizer"
	caasAgentCleanupFinalizer = "osac.openshift.io/caas-agent-cleanup"
)

// Reconciler is the bare-metal worker reconciler (BareMetalWorkerReconciler). It watches
// ClusterOrder resources with bare-metal node sets, ensures a cluster-specific InfraEnv exists,
// creates BMIs, correlates registered Agents by MAC, and converges NodePool replicas.
type Reconciler struct {
	client.Client
	apiReader                  client.Reader
	scheme                     *runtime.Scheme
	fulfillment                FulfillmentClient
	ignition                   IgnitionFetcher
	recorder                   events.EventRecorder
	clusterOrderNamespace      string
	networkingNamespace        string
	macResolver                MACResolver
	secrets                    SecretsClient
	registryProbeGuestResolver registryProbeGuestResolver
	// NetworkingProvisioningEnabled reflects global.networking.provisioningEnabled.
	NetworkingProvisioningEnabled bool
	// WorkerNetworkConfigGate prevents Assisted from approving a CaaS Agent until
	// its MAC-driven NMStateConfig exists.
	WorkerNetworkConfigGate bool
}

// SecretsClient is the Secrets.Get subset used to materialize a registered
// SSH key when creating an InfraEnv.
type SecretsClient interface {
	Get(context.Context, *privatev1.SecretsGetRequest, ...grpc.CallOption) (*privatev1.SecretsGetResponse, error)
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
		Client:                        c,
		apiReader:                     apiReader,
		scheme:                        scheme,
		fulfillment:                   fulfillment,
		ignition:                      ignition,
		recorder:                      recorder,
		clusterOrderNamespace:         clusterOrderNamespace,
		networkingNamespace:           clusterOrderNamespace,
		NetworkingProvisioningEnabled: true,
	}
	return r
}

// SetNetworkingNamespace sets the namespace that holds BMF and NetworkAttachment hub CRs.
// It defaults to the ClusterOrder namespace for older deployments.
func (r *Reconciler) SetNetworkingNamespace(namespace string) {
	if namespace != "" {
		r.networkingNamespace = namespace
	}
}

// SetMACResolver overrides the function used to resolve a BMI's host NIC MACs for Agent
// correlation. CaaS uses the selected NetworkAttachment MAC when observed; the fallback
// reads status.hardware.nics from the reconcile-local BMI observation. Tests can inject a
// stub resolver before use.
func (r *Reconciler) SetMACResolver(resolver MACResolver) {
	r.macResolver = resolver
}

// SetSecretsClient sets the client used to resolve the Cluster's registered
// SSH-key Secret through the fulfillment private API. The resolved material
// feeds only the InfraEnv's authorized key; child BMIs receive the Secret
// reference, never the bytes. When nil, a referenced key holds InfraEnv
// creation with an SSHKeyResolutionPending condition.
func (r *Reconciler) SetSecretsClient(client SecretsClient) {
	r.secrets = client
}

// SetWorkerNetworkConfigGate enables the CaaS Agent approval gate that waits for
// the MAC-driven NMStateConfig created by the provisioning workflow.
func (r *Reconciler) SetWorkerNetworkConfigGate(enabled bool) {
	r.WorkerNetworkConfigGate = enabled
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=clusterorders,verbs=get;list;watch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=clusterorders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create
// +kubebuilder:rbac:groups=agent-install.openshift.io,resources=infraenvs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agent-install.openshift.io,resources=agents,verbs=get;list;watch;patch;delete
// +kubebuilder:rbac:groups=agent-install.openshift.io,resources=nmstateconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=capi-provider.agent-install.openshift.io,resources=agentmachines,verbs=get;list;watch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=baremetalinstances;networkattachments,verbs=get;list;watch
// +kubebuilder:rbac:groups=hypershift.openshift.io,resources=nodepools,verbs=get;list;watch;patch;update

// Reconcile ensures the InfraEnv for a bare-metal ClusterOrder exists, creates BMIs, correlates
// registered Agents by MAC, and converges NodePool replicas.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	co := &v1alpha1.ClusterOrder{}
	if err := r.apiReader.Get(ctx, req.NamespacedName, co); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !co.DeletionTimestamp.IsZero() {
		res, err := r.handleVerifiedClusterDeletion(ctx, co)
		if err != nil {
			return r.handleFulfillmentError(ctx, co, err)
		}
		return res, nil
	}
	if v, ok := co.Annotations[managementStateAnnotation]; ok && v == managementStateUnmanaged {
		return ctrl.Result{}, nil
	}
	if !co.HasBareMetalNodeSet() {
		return ctrl.Result{}, nil
	}
	if err := validateBareMetalNodeSets(co); err != nil {
		return ctrl.Result{}, err
	}
	cluster, err := r.authoritativeWorkerCluster(ctx, co)
	if err != nil {
		return r.handleFulfillmentError(ctx, co, err)
	}

	// Refresh the worker gauges from the full ClusterOrder set at the end of every
	// bare-metal reconcile, so desired/ready/failed converge as status changes.
	defer r.syncWorkerGauges(ctx)

	if controllerutil.AddFinalizer(co, bmWorkerFinalizer) {
		if err := r.Update(ctx, co); err != nil {
			return ctrl.Result{}, err
		}
		return workerBoundaryRequeue(), nil
	}

	res, err := r.reconcileWorkers(ctx, co, cluster)
	if errors.Is(err, errWorkerObservationChanged) {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	if err != nil {
		return r.handleFulfillmentError(ctx, co, err)
	}
	return res, nil
}

func namespacePredicate(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == namespace
	})
}

// workerClusterOrderChangedPredicate admits worker inputs and lifecycle changes.
// Worker condition and count writes complete in the current invocation or use
// explicit requeues to continue convergence.
func workerClusterOrderChangedPredicate() predicate.Predicate {
	return predicate.Or(predicate.GenerationChangedPredicate{}, predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			old := e.ObjectOld.(*v1alpha1.ClusterOrder)
			current := e.ObjectNew.(*v1alpha1.ClusterOrder)
			if !old.DeletionTimestamp.Equal(current.DeletionTimestamp) ||
				!reflect.DeepEqual(old.Finalizers, current.Finalizers) ||
				old.Labels[clusterOrderIDLabel] != current.Labels[clusterOrderIDLabel] ||
				!reflect.DeepEqual(old.Status.APIDNSRecordsPublishedTime, current.Status.APIDNSRecordsPublishedTime) ||
				!reflect.DeepEqual(old.Status.Jobs, current.Status.Jobs) {
				return true
			}
			for _, key := range []string{tenantAnnotationKey, managementStateAnnotation, infraEnvUIDAnnotation} {
				if old.Annotations[key] != current.Annotations[key] {
					return true
				}
			}
			for _, conditionType := range []string{
				string(v1alpha1.ClusterOrderConditionManagementPublicationReady),
				string(v1alpha1.ClusterOrderConditionClusterTeardownReady),
			} {
				if !reflect.DeepEqual(
					apimeta.FindStatusCondition(old.Status.Conditions, conditionType),
					apimeta.FindStatusCondition(current.Status.Conditions, conditionType),
				) {
					return true
				}
			}
			return !reflect.DeepEqual(old.Status.ClusterReference, current.Status.ClusterReference) ||
				!workerSlicesEqual(old.Status.Workers, current.Status.Workers)
		},
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
		For(&v1alpha1.ClusterOrder{}, builder.WithPredicates(
			namespacePredicate(r.clusterOrderNamespace), workerClusterOrderChangedPredicate(),
		))

	if crdExists(mgr, agentGVK) {
		agentObj := &unstructured.Unstructured{}
		agentObj.SetGroupVersionKind(agentGVK)
		bld = bld.Watches(agentObj, handler.EnqueueRequestsFromMapFunc(r.agentToClusterOrderMapper()))
		log.Info("watching Agent CRs for MAC correlation")
	} else {
		log.Info("Agent CRD not found, skipping Agent watch")
	}

	if crdExists(mgr, agentMachineGVK) {
		agentMachineObj := &unstructured.Unstructured{}
		agentMachineObj.SetGroupVersionKind(agentMachineGVK)
		bld = bld.Watches(agentMachineObj, handler.EnqueueRequestsFromMapFunc(
			r.labelToClusterOrderMapper("cluster.x-k8s.io/cluster-name")))
		log.Info("watching AgentMachine CRs for CAP-Agent readiness")
	} else {
		log.Info("AgentMachine CRD not found, skipping AgentMachine watch")
	}

	bmiGVK := bmfov1alpha1.GroupVersion.WithKind("BareMetalInstance")
	if crdExists(mgr, bmiGVK) {
		bld = bld.Watches(&bmfov1alpha1.BareMetalInstance{}, handler.EnqueueRequestsFromMapFunc(
			r.bareMetalInstanceToClusterOrderMapper()))
		log.Info("watching BareMetalInstance CRs for worker network readiness and cleanup")
	}

	networkAttachmentGVK := v1alpha1.GroupVersion.WithKind("NetworkAttachment")
	if crdExists(mgr, networkAttachmentGVK) {
		bld = bld.Watches(&v1alpha1.NetworkAttachment{}, handler.EnqueueRequestsFromMapFunc(
			r.networkAttachmentToClusterOrderMapper()))
		log.Info("watching NetworkAttachment CRs for worker network readiness")
	}

	npGVK := schema.GroupVersionKind{Group: "hypershift.openshift.io", Version: v1beta1APIVersion, Kind: "NodePool"}
	if crdExists(mgr, npGVK) {
		npObj := &unstructured.Unstructured{}
		npObj.SetGroupVersionKind(npGVK)
		bld = bld.Watches(npObj, handler.EnqueueRequestsFromMapFunc(r.labelToClusterOrderMapper(legacyClusterOrderLabel)))
		log.Info("watching NodePool CRs for replica convergence")
	} else {
		log.Info("NodePool CRD not found, skipping NodePool watch")
	}

	return bld.Complete(r)
}

func (r *Reconciler) bareMetalInstanceToClusterOrderMapper() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		id := obj.GetLabels()["osac.openshift.io/baremetalinstance-uuid"]
		return r.mapBMIIDToClusterOrder(ctx, id)
	}
}

func (r *Reconciler) networkAttachmentToClusterOrderMapper() handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		attachment, ok := obj.(*v1alpha1.NetworkAttachment)
		if !ok || attachment.Spec.Target.Kind != v1alpha1.NetworkAttachmentTargetBareMetalInstance {
			return nil
		}
		return r.mapBMIIDToClusterOrder(ctx, attachment.Spec.Target.ID)
	}
}

func (r *Reconciler) mapBMIIDToClusterOrder(ctx context.Context, bmiID string) []ctrl.Request {
	if bmiID == "" || r.fulfillment == nil {
		return nil
	}
	bmi, err := r.fulfillment.GetBareMetalInstance(ctx, bmiID)
	if err != nil {
		ctrllog.FromContext(ctx).Error(err, "failed to map BareMetalInstance event to ClusterOrder", "bareMetalInstanceID", bmiID)
		return nil
	}
	if bmi == nil {
		return nil
	}
	name := bmi.GetMetadata().GetLabels()[clusterOrderLabel]
	if name == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{Name: name, Namespace: r.clusterOrderNamespace}}}
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
