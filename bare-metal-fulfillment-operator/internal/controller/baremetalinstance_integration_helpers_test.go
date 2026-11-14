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
	"encoding/json"
	"net/http"

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/management"
)

// Shared helpers for the controller integration suites (Metal3, BCM and NetBox).
// They run in package controller against the same envtest API server (see
// suite_test.go); these namespace-parameterized helpers hold the logic once so
// each suite keeps only a thin, namespace-bound wrapper for readability.

// ensureTestNamespace creates the given namespace, tolerating AlreadyExists so
// it is safe to call from each suite's BeforeEach.
func ensureTestNamespace(name string) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := k8sClient.Create(ctx, ns); err != nil && client.IgnoreAlreadyExists(err) != nil {
		Fail("failed to create test namespace " + name + ": " + err.Error())
	}
}

// reconcileInNS reconciles name n times, asserting no error each time, and
// returns the last result.
func reconcileInNS(reconciler *BareMetalInstanceReconciler, namespace, name string, n int) ctrl.Result {
	var result ctrl.Result
	for range n {
		var err error
		result, err = reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: namespace},
		})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
	}
	return result
}

// reconcileExpectErrInNS reconciles name once and returns the (expected) error.
func reconcileExpectErrInNS(reconciler *BareMetalInstanceReconciler, namespace, name string) error {
	_, err := reconciler.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: namespace},
	})
	return err
}

func getBMIInNS(namespace, name string) *v1alpha1.BareMetalInstance {
	bmi := &v1alpha1.BareMetalInstance{}
	ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, bmi)).To(Succeed())
	return bmi
}

func getBMHInNS(namespace, name string) *metal3api.BareMetalHost {
	bmh := &metal3api.BareMetalHost{}
	ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, bmh)).To(Succeed())
	return bmh
}

// cleanupBMIInNS deletes a BareMetalInstance, stripping finalizers first since
// no controller runs in envtest to process them.
func cleanupBMIInNS(namespace, name string) {
	bmi := &v1alpha1.BareMetalInstance{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, bmi); err != nil {
		ExpectWithOffset(1, client.IgnoreNotFound(err)).NotTo(HaveOccurred())
		return
	}
	if len(bmi.Finalizers) > 0 {
		bmi.Finalizers = nil
		ExpectWithOffset(1, k8sClient.Update(ctx, bmi)).To(Succeed())
	}
	ExpectWithOffset(1, client.IgnoreNotFound(k8sClient.Delete(ctx, bmi))).NotTo(HaveOccurred())
}

func cleanupBMHInNS(namespace, name string) {
	bmh := &metal3api.BareMetalHost{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	ExpectWithOffset(1, client.IgnoreNotFound(k8sClient.Delete(ctx, bmh))).NotTo(HaveOccurred())
}

// newTestReconciler wires a BareMetalInstance reconciler with the given
// inventory client and the Metal3 management client for namespace.
func newTestReconciler(invClient inventory.Client, namespace string) *BareMetalInstanceReconciler {
	return NewBareMetalInstanceReconciler(
		k8sClient,
		k8sClient.Scheme(),
		invClient,
		management.NewMetal3ClientForTest(k8sClient, namespace),
		nil,   // provisioning provider
		false, // networking enabled
		0, 0, 0, 0, 0,
	)
}

// markBMHReadyInNS flips a BMH to available+OK so IsBMHAvailable returns true.
// It stands in for the Metal3 baremetal-operator, which is not running in
// envtest.
func markBMHReadyInNS(namespace, name string) {
	bmh := getBMHInNS(namespace, name)
	bmh.Status.OperationalStatus = metal3api.OperationalStatusOK
	bmh.Status.Provisioning.State = metal3api.StateAvailable
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, bmh)).To(Succeed())
}

// bmhExistsInNS reports whether the BMH exists, failing on read errors.
func bmhExistsInNS(namespace, name string) bool {
	err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &metal3api.BareMetalHost{})
	ExpectWithOffset(1, client.IgnoreNotFound(err)).To(Succeed())
	return err == nil
}

// driveToAllocatedIn runs finalizer, selection and claim, then marks the BMH
// ready and reconciles once more so hostClass is persisted.
func driveToAllocatedIn(reconciler *BareMetalInstanceReconciler, namespace, bmiName, hostname string) {
	reconcileInNS(reconciler, namespace, bmiName, 3)
	ExpectWithOffset(1, bmhExistsInNS(namespace, hostname)).To(BeTrue(), "AssignHost should have created the BMH")
	markBMHReadyInNS(namespace, hostname)
	reconcileInNS(reconciler, namespace, bmiName, 1)
}

// writeJSON writes a JSON response, reporting encoding failures as 500.
func writeJSON(w http.ResponseWriter, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(data); err != nil {
		GinkgoWriter.Printf("writeJSON: writing response: %v\n", err)
	}
}
