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

package controller

import (
	"context"
	"errors"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

// ExternalIPPoolReconciler runs the Fabric create_external_ip_pool and
// delete_external_ip_pool tasks (contract §4.10). A pool records only the
// Fabric Manager of the NetworkClass.
type ExternalIPPoolReconciler struct {
	Client              client.Client
	Engine              *networktask.Engine
	NetworkingNamespace string
}

// Reconcile advances the create or delete of an ExternalIPPool.
func (r *ExternalIPPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return reconcileNetworkResource(ctx, r.Client, r.Engine, req.NamespacedName, networkingHandlers[*v1alpha1.ExternalIPPool]{
		kind:      kindExternalIPPool,
		finalizer: osacExternalIPPoolFinalizer,
		newObject: func() *v1alpha1.ExternalIPPool { return &v1alpha1.ExternalIPPool{} },
		status:    func(o *v1alpha1.ExternalIPPool) any { return &o.Status },
		create:    r.create,
		remove:    r.remove,
	})
}

func (r *ExternalIPPoolReconciler) create(ctx context.Context, pool *v1alpha1.ExternalIPPool) (ctrl.Result, error) {
	if pool.Status.NetworkManagers == nil {
		nc, err := readyNetworkClass(ctx, r.Client, pool.Namespace, r.Engine.ProvisioningEnabled())
		if err != nil {
			return ctrl.Result{}, err
		}
		if nc == nil {
			networktask.SetProgressing(pool, msgWaitingForNetworkClass)
			return waitResult(), nil
		}
		copyManagers(pool, nc.Status.NetworkManagers, true)
	}
	input, store, err := inputOf(pool, func() (map[string]any, error) {
		resource, err := networktask.Snapshot(pool, kindExternalIPPool, pool.Spec, "cidrs")
		if err != nil {
			return nil, err
		}
		return networktask.NewInput(pool, kindExternalIPPool, resource), nil
	}, nil)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !r.Engine.ProvisioningEnabled() {
		networktask.SetProvisioningDisabled(pool, networktask.CreateExternalIPPool)
		return ctrl.Result{}, store()
	}
	req := taskRequest(pool, kindExternalIPPool, networktask.CreateExternalIPPool, v1alpha1.NetworkJobManagerFabric, input)
	req.BeforeLaunch = store
	out, err := r.Engine.Run(ctx, pool, req)
	if err != nil {
		return ctrl.Result{}, err
	}
	if out.Succeeded() {
		networktask.SetSucceeded(pool)
		return ctrl.Result{}, nil
	}
	reportOutcome(pool, out)
	return outcomeResult(out), nil
}

func (r *ExternalIPPoolReconciler) remove(ctx context.Context, pool *v1alpha1.ExternalIPPool) (bool, ctrl.Result, error) {
	if id := pool.Labels[osacExternalIPPoolIDLabel]; id != "" {
		used, err := anyItem(ctx, r.Client, &v1alpha1.ExternalIPList{}, pool.Namespace, func(o client.Object) bool {
			return o.(*v1alpha1.ExternalIP).Spec.Pool == id
		})
		if err != nil || used {
			return false, waitResult(), err
		}
	}
	input := cleanupInput(pool)
	return runCleanup(ctx, r.Engine, pool, kindExternalIPPool,
		cleanupTask{create: networktask.CreateExternalIPPool, manager: v1alpha1.NetworkJobManagerFabric,
			request: func() networktask.Request {
				return taskRequest(pool, kindExternalIPPool, networktask.DeleteExternalIPPool, v1alpha1.NetworkJobManagerFabric, input)
			}},
	)
}

// SetupWithManager registers the controller. NetworkClass events re-queue
// waiting pools; ExternalIP events re-queue their pool.
func (r *ExternalIPPoolReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	localMgr := mgr.GetLocalManager()
	if localMgr == nil {
		return errors.New("local manager is nil")
	}
	ns := r.NetworkingNamespace
	inNamespace := builder.WithPredicates(NetworkingNamespacePredicate(ns))
	poolList := func() client.ObjectList { return &v1alpha1.ExternalIPPoolList{} }
	return ctrl.NewControllerManagedBy(localMgr).
		Named("externalippool").
		For(&v1alpha1.ExternalIPPool{}, inNamespace).
		Watches(&v1alpha1.NetworkClass{}, enqueueWhere(r.Client, ns, poolList, func(obj, _ client.Object) bool {
			return obj.(*v1alpha1.ExternalIPPool).Status.NetworkManagers == nil
		}), inNamespace).
		Watches(&v1alpha1.ExternalIP{}, enqueueWhere(r.Client, ns, poolList, func(obj, trigger client.Object) bool {
			return triggerID(obj, kindExternalIPPool) == trigger.(*v1alpha1.ExternalIP).Spec.Pool
		}), inNamespace).
		Complete(r)
}
