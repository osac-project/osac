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
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/trustadmission"
)

const (
	// DefaultFulfillmentTrustSourceName is the management CA ConfigMap name used when none is configured.
	DefaultFulfillmentTrustSourceName = "ca-bundle"
	trustRecordLifetime               = 8 * time.Minute
	trustRetryInterval                = 30 * time.Second
)

var errTrustClientUnavailable = errors.New("supported CSI trust client is unavailable")

type FulfillmentTrustTarget interface {
	Observe(context.Context, trustadmission.ExpectedBundle) (bool, bool, error)
	Publish(context.Context, trustadmission.ExpectedBundle) error
	Apply(context.Context, trustadmission.ExpectedBundle) error
	Revoke(context.Context, trustadmission.RecordKey) error
}

type FulfillmentTrustTargetResolver interface {
	Resolve(context.Context, *v1alpha1.ClusterOrder) (FulfillmentTrustTarget, error)
}

// FulfillmentTrustReconciler synchronizes the management CA to tenant-scoped
// ClusterOrders. It uses short-lived target service-account tokens and never
// stores a tenant kubeconfig or token in the management cluster.
type FulfillmentTrustReconciler struct {
	client.Client
	APIReader             client.Reader
	Enabled               bool
	ClusterOrderNamespace string
	SourceNamespace       string
	TenantNamespace       string
	SourceName            string
	Targets               FulfillmentTrustTargetResolver
	PollInterval          time.Duration
	Now                   func() time.Time
}

func (r *FulfillmentTrustReconciler) pollInterval() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return trustRetryInterval
}

func (r *FulfillmentTrustReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if !r.Enabled {
		return ctrl.Result{}, nil
	}
	order := &v1alpha1.ClusterOrder{}
	if err := r.Get(ctx, req.NamespacedName, order); err != nil {
		if client.IgnoreNotFound(err) == nil {
			r.forgetTargetByOrder(req.NamespacedName)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if order.Namespace != r.ClusterOrderNamespace || order.Annotations[trustadmission.TenantAnnotation] == "" ||
		order.Annotations[osacManagementStateAnnotation] == ManagementStateUnmanaged {
		r.forgetTarget(order)
		return ctrl.Result{}, nil
	}
	if r.Targets == nil || r.APIReader == nil || r.TenantNamespace == "" || r.SourceNamespace == "" {
		return ctrl.Result{}, errors.New("fulfillment trust reconciler is not configured")
	}
	if !order.DeletionTimestamp.IsZero() {
		r.fenceDeletion(ctx, order)
		r.forgetTarget(order)
		return ctrl.Result{}, nil
	}
	if order.UID == "" || order.Status.ClusterReference == nil || order.Status.ClusterReference.HostedClusterName == "" {
		r.forgetTarget(order)
		return r.failed(ctx, order, "KubeconfigNotAvailable", "Tenant cluster is not available")
	}
	return r.reconcileTrust(ctx, order)
}

func (r *FulfillmentTrustReconciler) reconcileTrust(ctx context.Context, order *v1alpha1.ClusterOrder) (ctrl.Result, error) {
	bundle, hash, err := r.sourceBundle(ctx)
	if err != nil {
		r.revokeCurrentRecord(ctx, order)
		return r.failed(ctx, order, "TrustBundleUnavailable", "Management trust bundle is unavailable")
	}
	target, err := r.Targets.Resolve(ctx, order)
	if err != nil || target == nil {
		return r.failed(ctx, order, "KubeconfigNotAvailable", "Tenant trust access is not ready")
	}
	now := r.now()
	record := trustadmission.ExpectedBundle{
		Key: trustadmission.RecordKey{
			ClusterOrderUID: string(order.UID), TenantNamespace: r.TenantNamespace,
			ConfigMapName: trustadmission.ConfigMapName, BundleSHA256: hash,
		},
		Tenant: order.Annotations[trustadmission.TenantAnnotation], OwnerReference: string(order.UID),
		BundlePEM: bundle, ExpiresAt: now.Add(trustRecordLifetime),
	}
	current, unsupported, err := target.Observe(ctx, record)
	if err != nil {
		return r.failed(ctx, order, "KubeconfigNotAvailable", "Tenant trust target is unavailable")
	}
	if unsupported {
		return r.failed(ctx, order, "CSIClientUpgradeRequired", "CSI controller does not support fulfillment trust")
	}
	if !current {
		if err := target.Publish(ctx, record); err != nil {
			return r.failed(ctx, order, "TrustBundleApplyFailed", "Expected trust bundle publication failed")
		}
		if err := target.Apply(ctx, record); err != nil {
			if errors.Is(err, errTrustClientUnavailable) {
				return r.failed(ctx, order, "CSIClientUnavailable", "CSI trust-enabled controller is not available")
			}
			return r.failed(ctx, order, "TrustBundleApplyFailed", "Tenant trust update failed")
		}
		current, unsupported, err = target.Observe(ctx, record)
		if err != nil {
			return r.failed(ctx, order, "KubeconfigNotAvailable", "Tenant trust target is unavailable")
		}
		if unsupported {
			return r.failed(ctx, order, "CSIClientUpgradeRequired", "CSI controller does not support fulfillment trust")
		}
	}
	if !current {
		order.SetStatusCondition(string(v1alpha1.ClusterOrderConditionFulfillmentTrustReady), metav1.ConditionFalse,
			"Waiting for the trust bundle and CSI rollout to converge", "TrustBundleSyncing")
		return ctrl.Result{RequeueAfter: r.pollInterval()}, r.patchStatus(ctx, order)
	}
	if previous := order.Status.FulfillmentTrustBundleHash; previous != "" && previous != hash {
		oldKey := record.Key
		oldKey.BundleSHA256 = previous
		if err := target.Revoke(ctx, oldKey); err != nil {
			return r.failed(ctx, order, "TrustBundleApplyFailed", "Previous trust authorization could not be revoked")
		}
	}
	order.Status.FulfillmentTrustBundleHash = hash
	order.SetStatusCondition(string(v1alpha1.ClusterOrderConditionFulfillmentTrustReady), metav1.ConditionTrue,
		"Trust bundle and CSI rollout verified", "TrustBundleSynchronized")
	return ctrl.Result{RequeueAfter: r.pollInterval()}, r.patchStatus(ctx, order)
}

func (r *FulfillmentTrustReconciler) sourceBundle(ctx context.Context) ([]byte, string, error) {
	name := r.SourceName
	if name == "" {
		name = DefaultFulfillmentTrustSourceName
	}
	configMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: r.SourceNamespace, Name: name}, configMap); err != nil {
		return nil, "", err
	}
	bundle := []byte(configMap.Data[trustadmission.BundleDataKey])
	if len(bundle) == 0 || !x509.NewCertPool().AppendCertsFromPEM(bundle) {
		return nil, "", errors.New("invalid management trust bundle")
	}
	digest := sha256.Sum256(bundle)
	return bundle, hex.EncodeToString(digest[:]), nil
}

func (r *FulfillmentTrustReconciler) revokeCurrentRecord(ctx context.Context, order *v1alpha1.ClusterOrder) {
	if order.Status.FulfillmentTrustBundleHash == "" || order.UID == "" {
		return
	}
	target, err := r.Targets.Resolve(ctx, order)
	if err != nil || target == nil {
		if err != nil {
			ctrllog.FromContext(ctx).Error(err, "tenant trust record revocation deferred", "clusterOrder", order.Name)
		}
		return
	}
	key := trustadmission.RecordKey{ClusterOrderUID: string(order.UID), TenantNamespace: r.TenantNamespace,
		ConfigMapName: trustadmission.ConfigMapName, BundleSHA256: order.Status.FulfillmentTrustBundleHash}
	if err := target.Revoke(ctx, key); err != nil {
		ctrllog.FromContext(ctx).Error(err, "tenant trust record revocation failed", "clusterOrder", order.Name)
	}
}

func (r *FulfillmentTrustReconciler) fenceDeletion(ctx context.Context, order *v1alpha1.ClusterOrder) {
	r.revokeCurrentRecord(ctx, order)
}

func (r *FulfillmentTrustReconciler) forgetTarget(order *v1alpha1.ClusterOrder) {
	if order != nil {
		r.forgetTargetByOrder(client.ObjectKeyFromObject(order))
	}
}

func (r *FulfillmentTrustReconciler) forgetTargetByOrder(key client.ObjectKey) {
	if forgetter, ok := r.Targets.(interface{ ForgetByOrder(client.ObjectKey) }); ok {
		forgetter.ForgetByOrder(key)
	}
}

func (r *FulfillmentTrustReconciler) failed(ctx context.Context, order *v1alpha1.ClusterOrder, reason, message string) (ctrl.Result, error) {
	order.SetStatusCondition(string(v1alpha1.ClusterOrderConditionFulfillmentTrustReady), metav1.ConditionFalse, message, reason)
	return ctrl.Result{RequeueAfter: r.pollInterval()}, r.patchStatus(ctx, order)
}

func (r *FulfillmentTrustReconciler) patchStatus(ctx context.Context, order *v1alpha1.ClusterOrder) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.ClusterOrder{}
		if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(order), latest); err != nil {
			return err
		}
		base := latest.DeepCopy()
		latest.Status.FulfillmentTrustBundleHash = order.Status.FulfillmentTrustBundleHash
		if condition := apimeta.FindStatusCondition(order.Status.Conditions, string(v1alpha1.ClusterOrderConditionFulfillmentTrustReady)); condition != nil {
			apimeta.SetStatusCondition(&latest.Status.Conditions, *condition)
		}
		return r.Status().Patch(ctx, latest, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	})
}

func (r *FulfillmentTrustReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *FulfillmentTrustReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if !r.Enabled {
		return nil
	}
	local := mgr.GetLocalManager()
	if local == nil {
		return fmt.Errorf("local manager is nil")
	}
	sourceName := r.SourceName
	if sourceName == "" {
		sourceName = DefaultFulfillmentTrustSourceName
	}
	return ctrl.NewControllerManagedBy(local).
		Named("fulfillment-trust").
		WithOptions(crcontroller.Options{MaxConcurrentReconciles: 4}).
		For(&v1alpha1.ClusterOrder{}, builder.WithPredicates(NamespacePredicate(r.ClusterOrderNamespace))).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.mapSourceToOrders),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
				return object.GetNamespace() == r.SourceNamespace && object.GetName() == sourceName
			}))).
		Complete(r)
}

func (r *FulfillmentTrustReconciler) mapSourceToOrders(ctx context.Context, _ client.Object) []reconcile.Request {
	orders := &v1alpha1.ClusterOrderList{}
	if err := r.List(ctx, orders, client.InNamespace(r.ClusterOrderNamespace)); err != nil {
		ctrllog.FromContext(ctx).Error(err, "list tenant-scoped ClusterOrders")
		return nil
	}
	requests := make([]reconcile.Request, 0, len(orders.Items))
	for i := range orders.Items {
		order := &orders.Items[i]
		if order.Annotations[trustadmission.TenantAnnotation] != "" && order.Status.ClusterReference != nil {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(order)})
		}
	}
	return requests
}
