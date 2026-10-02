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
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	// managementStateAnnotation / managementStateUnmanaged mirror the values the other osac
	// controllers honor (controller.ManagementStateUnmanaged = "unmanaged"; the annotation const
	// there is unexported). Kept in sync by the management-state skip test.
	managementStateAnnotation = "osac.openshift.io/management-state"
	managementStateUnmanaged  = "unmanaged"
	bmWorkerFinalizer         = "osac.openshift.io/baremetalworker-finalizer"
)

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
	return r
}

// SetMACResolver overrides the function used to resolve a BMI's host NIC MACs for agent
// correlation. The default reads status.hardware.nics (OSAC-4203) from the
// reconcile-local BMI observation; tests can inject a stub resolver before use.
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
	if err := r.apiReader.Get(ctx, req.NamespacedName, co); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !co.DeletionTimestamp.IsZero() {
		return r.handleVerifiedClusterDeletion(ctx, co)
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
	tenant, err := r.authoritativeWorkerTenant(ctx, co)
	if err != nil {
		return ctrl.Result{}, err
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

	res, err := r.reconcileWorkers(ctx, co, tenant)
	if errors.Is(err, errWorkerObservationChanged) {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	return res, err
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
