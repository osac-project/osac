// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"reflect"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
	"github.com/osac-project/osac/osac-operator/pkg/pullsecret"
)

// createWorkerClusterOrderFixture supplies the same owned reference as fulfillment.
func createWorkerClusterOrderFixture(order *api.ClusterOrder) error {
	if err := k8sClient.Create(ctx, order); err != nil {
		return err
	}
	secretName := order.Name + "-pull-secret"
	if order.Spec.PullSecretRef != nil {
		secretName = order.Spec.PullSecretRef.Name
	}
	DeferCleanup(func() {
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: order.Namespace}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).To(Succeed())
	})
	fixturePayload := `{"auths":{}}`
	if err := pullsecret.Ensure(ctx, k8sClient, order, &fixturePayload); err != nil {
		return err
	}
	return k8sClient.Update(ctx, order)
}

// driveWorkerCheckpoints migrates legacy stage fixtures to explicit, finite
// public reconciles. Only durable one-second checkpoints are advanced: errors
// and dependency/backoff results are returned to the case unchanged. Every
// checkpoint must persist progress, and each call may create at most one BMI.
// R01 boundary specs call Reconcile directly to assert intermediate states.
func driveWorkerCheckpoints(r *baremetalworker.Reconciler, fc *workerFulfillmentStub, req reconcile.Request) (reconcile.Result, error) {
	GinkgoHelper()
	before := &api.ClusterOrder{}
	if err := k8sClient.Get(ctx, req.NamespacedName, before); err != nil {
		return r.Reconcile(ctx, req)
	}
	n := len(before.Status.Workers)
	for _, nr := range before.Spec.NodeRequests {
		n += nr.NumberOfNodes
	}
	for range 16 + 8*n {
		creates := len(fc.CreateCalls())
		res, err := r.Reconcile(ctx, req)
		Expect(len(fc.CreateCalls())-creates).To(BeNumerically("<=", 1), "one Create per public reconcile")
		if err != nil || res.RequeueAfter != time.Second {
			return res, err
		}
		after := &api.ClusterOrder{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(before), after)).To(Succeed())
		Expect(reflect.DeepEqual(before.Status.Workers, after.Status.Workers) && reflect.DeepEqual(before.Finalizers, after.Finalizers)).To(BeFalse(), "checkpoint must persist progress rather than hot-loop")
		before = after
	}
	Fail("worker fixture exceeded finite reconciliation bound")
	return reconcile.Result{}, nil
}
