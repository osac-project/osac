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

	ovnv1 "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/userdefinednetwork/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mc "sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/osac-project/osac-operator/api/v1alpha1"
)

const tenantFinalizer = "osac.openshift.io/tenant"

// TenantReconciler reconciles a Tenant object.
// Tracks namespace readiness and tenant lifecycle (Phase, finalizer).
// Storage provisioning is handled by the OSAC Storage Controller.
type TenantReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	Recorder        events.EventRecorder
	tenantNamespace string
	mgr             mcmanager.Manager
	targetCluster   mc.ClusterName
}

// +kubebuilder:rbac:groups=osac.openshift.io,resources=tenants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=osac.openshift.io,resources=tenants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=osac.openshift.io,resources=tenants/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=k8s.ovn.org,resources=userdefinednetworks,verbs=get;list;watch;create;update;patch;delete

func NewTenantReconciler(
	mgr mcmanager.Manager,
	tenantNamespace string,
	targetCluster mc.ClusterName,
) *TenantReconciler {
	if mgr == nil {
		panic("mgr must not be nil")
	}

	return &TenantReconciler{
		Client:          mgr.GetLocalManager().GetClient(),
		Scheme:          mgr.GetLocalManager().GetScheme(),
		Recorder:        mgr.GetLocalManager().GetEventRecorder(tenantControllerName),
		tenantNamespace: tenantNamespace,
		mgr:             mgr,
		targetCluster:   targetCluster,
	}
}

func (r *TenantReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	instance := &v1alpha1.Tenant{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("start reconcile")

	oldstatus := instance.Status.DeepCopy()

	var res ctrl.Result
	if instance.ObjectMeta.DeletionTimestamp.IsZero() {
		res, err = r.handleUpdate(ctx, req.Request, instance)
	} else {
		res, err = r.handleDelete(ctx, instance)
	}

	if !equality.Semantic.DeepEqual(instance.Status, *oldstatus) {
		log.Info("status requires update", "old", *oldstatus, "new", instance.Status)
		if err := r.Status().Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
	}

	log.Info("end reconcile")
	return res, err
}

func (r *TenantReconciler) handleUpdate(ctx context.Context, req reconcile.Request, instance *v1alpha1.Tenant) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	log.Info("handling update for Tenant", "name", instance.GetName())

	if !controllerutil.ContainsFinalizer(instance, tenantFinalizer) {
		controllerutil.AddFinalizer(instance, tenantFinalizer)
		if err := r.Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
	}

	instance.Status.Phase = v1alpha1.TenantPhaseProgressing
	instance.Status.Namespace = ""

	targetClient, err := getTargetClient(ctx, r.mgr, r.targetCluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	var namespace corev1.Namespace
	if err = targetClient.Get(ctx, client.ObjectKey{Name: instance.GetName()}, &namespace); err != nil {
		instance.SetStatusCondition(v1alpha1.TenantConditionNamespaceReady,
			metav1.ConditionFalse,
			v1alpha1.TenantReasonNotFound,
			fmt.Sprintf("Namespace %q not found on target cluster", instance.GetName()))
		return ctrl.Result{}, err
	}

	instance.SetStatusCondition(v1alpha1.TenantConditionNamespaceReady,
		metav1.ConditionTrue,
		v1alpha1.TenantReasonFound,
		fmt.Sprintf("Namespace %q found on target cluster", instance.GetName()))

	instance.Status.Namespace = namespace.GetName()
	instance.Status.Phase = v1alpha1.TenantPhaseReady
	return ctrl.Result{}, nil
}

func (r *TenantReconciler) handleDelete(ctx context.Context, instance *v1alpha1.Tenant) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)
	log.Info("handling delete for Tenant", "name", instance.Name)

	if !controllerutil.ContainsFinalizer(instance, tenantFinalizer) {
		return ctrl.Result{}, nil
	}

	instance.Status.Phase = v1alpha1.TenantPhaseDeleting

	controllerutil.RemoveFinalizer(instance, tenantFinalizer)
	if err := r.Update(ctx, instance); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("tenant finalizer removed, deletion will proceed")
	return ctrl.Result{}, nil
}

func (r *TenantReconciler) mapObjectToTenant(ctx context.Context, obj client.Object) []reconcile.Request {
	log := ctrllog.FromContext(ctx)

	tenantName, exists := obj.GetLabels()[osacTenantRefLabel]
	if !exists {
		return nil
	}

	tenant := &v1alpha1.Tenant{}
	err := r.Get(ctx, client.ObjectKey{Namespace: r.tenantNamespace, Name: tenantName}, tenant)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "unable to get Tenant from object", "kind", obj.GetObjectKind(), "name", obj.GetName(), "namespace", obj.GetNamespace(), "tenant", tenantName)
		}
		return nil
	}

	log.Info("mapping object to Tenant", "kind", obj.GetObjectKind(), "name", obj.GetName(), "namespace", obj.GetNamespace(), "tenant", tenantName)
	return []reconcile.Request{
		{
			NamespacedName: client.ObjectKeyFromObject(tenant),
		},
	}
}

func (r *TenantReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	tenantLabelPredicate, err := predicate.LabelSelectorPredicate(tenantLabelSelector(r.tenantNamespace))
	if err != nil {
		return err
	}

	return mcbuilder.ControllerManagedBy(mgr).
		For(&v1alpha1.Tenant{},
			mcbuilder.WithPredicates(tenantNamespacePredicate(r.tenantNamespace)),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false)).
		Named("tenant").
		Watches(
			&corev1.Namespace{},
			mchandler.EnqueueRequestsFromMapFunc(r.mapObjectToTenant),
			mcbuilder.WithPredicates(tenantLabelPredicate),
		).
		Watches(
			&ovnv1.UserDefinedNetwork{},
			mchandler.EnqueueRequestsFromMapFunc(r.mapObjectToTenant),
			mcbuilder.WithPredicates(tenantLabelPredicate),
		).
		Complete(r)
}
