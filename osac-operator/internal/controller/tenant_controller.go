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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const tenantFinalizer = "osac.openshift.io/tenant"

// TenantReconciler reconciles a Tenant object.
// Tracks tenant lifecycle (Phase, finalizer).
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

// NewTenantReconciler creates a TenantReconciler for the target cluster.
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

	instance.Status.Namespace = ""
	instance.Status.Phase = v1alpha1.TenantPhaseProgressing
	if err := r.ensureRemoteTenantNamespace(ctx, instance); err != nil {
		return ctrl.Result{}, err
	}

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

	if r.targetCluster != mcmanager.LocalCluster {
		for _, finalizer := range instance.Finalizers {
			if finalizer != tenantFinalizer {
				return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
			}
		}

		cluster, err := r.mgr.GetCluster(ctx, r.targetCluster)
		if err != nil {
			return ctrl.Result{}, err
		}
		namespace := &corev1.Namespace{}
		key := types.NamespacedName{Name: instance.GetName()}
		if err := cluster.GetAPIReader().Get(ctx, key, namespace); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("get remote tenant namespace %q: %w", key.Name, err)
			}
		} else {
			if !tenantNamespaceMatches(namespace, instance) {
				return ctrl.Result{}, fmt.Errorf("remote namespace %q does not match Tenant %s/%s", key.Name, instance.GetNamespace(), instance.GetName())
			}
			if namespace.DeletionTimestamp.IsZero() {
				if err := cluster.GetClient().Delete(ctx, namespace); err != nil && !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("delete remote tenant namespace %q: %w", key.Name, err)
				}
			}
			return ctrl.Result{RequeueAfter: defaultPreconditionRequeueInterval}, nil
		}
	}

	controllerutil.RemoveFinalizer(instance, tenantFinalizer)
	if err := r.Update(ctx, instance); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("tenant finalizer removed, deletion will proceed")
	return ctrl.Result{}, nil
}

// ensureRemoteTenantNamespace creates or validates the Tenant namespace on the
// remote cluster.
func (r *TenantReconciler) ensureRemoteTenantNamespace(ctx context.Context, instance *v1alpha1.Tenant) error {
	if r.targetCluster == mcmanager.LocalCluster {
		return nil
	}

	cluster, err := r.mgr.GetCluster(ctx, r.targetCluster)
	if err != nil {
		return err
	}
	key := types.NamespacedName{Name: instance.GetName()}
	namespace := &corev1.Namespace{}
	err = cluster.GetAPIReader().Get(ctx, key, namespace)
	if apierrors.IsNotFound(err) {
		namespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:   key.Name,
				Labels: tenantNamespaceLabels(instance),
			},
		}
		if err := cluster.GetClient().Create(ctx, namespace); err == nil {
			return nil
		} else if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create remote tenant namespace %q: %w", key.Name, err)
		}
		if err := cluster.GetAPIReader().Get(ctx, key, namespace); err != nil {
			return fmt.Errorf("get remote tenant namespace %q after AlreadyExists: %w", key.Name, err)
		}
	} else if err != nil {
		return fmt.Errorf("get remote tenant namespace %q: %w", key.Name, err)
	}

	if !tenantNamespaceMatches(namespace, instance) {
		return fmt.Errorf("remote namespace %q does not match Tenant %s/%s", key.Name, instance.GetNamespace(), instance.GetName())
	}
	if !namespace.DeletionTimestamp.IsZero() {
		return fmt.Errorf("remote tenant namespace %q is being deleted", key.Name)
	}
	if namespace.Labels[osacTenantKey] == "" ||
		(instance.Labels[osacTenantIDLabel] != "" && namespace.Labels[osacTenantIDLabel] == "") {
		base := namespace.DeepCopy()
		if namespace.Labels == nil {
			namespace.Labels = make(map[string]string)
		}
		namespace.Labels[osacTenantKey] = instance.GetName()
		if tenantID := instance.Labels[osacTenantIDLabel]; tenantID != "" {
			namespace.Labels[osacTenantIDLabel] = tenantID
		}
		if err := cluster.GetClient().Patch(ctx, namespace, client.MergeFrom(base)); err != nil {
			return fmt.Errorf("set tenant identity labels on remote namespace %q: %w", key.Name, err)
		}
	}
	return nil
}

// tenantNamespaceLabels returns the identity labels for a Tenant namespace.
func tenantNamespaceLabels(instance *v1alpha1.Tenant) map[string]string {
	labels := map[string]string{
		osacTenantKey:       instance.GetName(),
		osacTenantRefLabel:  instance.GetName(),
		osacProjectRefLabel: instance.GetNamespace(),
	}
	if tenantID := instance.Labels[osacTenantIDLabel]; tenantID != "" {
		labels[osacTenantIDLabel] = tenantID
	}
	return labels
}

// tenantNamespaceMatches reports whether a namespace's ownership labels are
// compatible with the Tenant.
func tenantNamespaceMatches(namespace *corev1.Namespace, instance *v1alpha1.Tenant) bool {
	if namespace.Labels[osacTenantRefLabel] != instance.GetName() ||
		namespace.Labels[osacProjectRefLabel] != instance.GetNamespace() ||
		(namespace.Labels[osacTenantKey] != "" && namespace.Labels[osacTenantKey] != instance.GetName()) {
		return false
	}
	tenantID := instance.Labels[osacTenantIDLabel]
	namespaceTenantID := namespace.Labels[osacTenantIDLabel]
	return namespaceTenantID == "" || namespaceTenantID == tenantID
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
		Complete(r)
}
