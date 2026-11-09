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
	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
)

var _ = Describe("BareMetalInstance NetBox claim recovery", func() {
	var mock *mockNetBox

	BeforeEach(func() {
		ensureTestNamespace(netboxTestNS)
		mock = newMockNetBox()
	})

	AfterEach(func() {
		mock.stop()
	})

	Describe("claim recovery", func() {
		const bmiName = "netbox-claim-bmi"
		const hostname = "netbox-42"

		AfterEach(func() {
			cleanupNetBoxHost(bmiName, hostname)
		})

		It("TC-INT-003 keeps the claim and retries preparation after a failure", func() {
			mock.addDevice(42, "staged", netboxDeviceFields(16))

			// A foreign BMH with the generated name makes CreateBMH fail after the
			// claim PATCH has already succeeded.
			foreign := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{Name: hostname, Namespace: netboxTestNS},
				Spec: metal3api.BareMetalHostSpec{
					ConsumerRef: &corev1.ObjectReference{
						APIVersion: "osac.openshift.io/v1alpha1",
						Kind:       "BareMetalInstance",
						Name:       "another-instance",
					},
				},
			}
			Expect(k8sClient.Create(ctx, foreign)).To(Succeed())

			reconciler := newNetBoxReconciler(mock, newNetBoxCredentialResolver(42))
			createNetBoxBMI(bmiName, map[string]string{"cpu_cores": "16"}, nil)

			// Finalizer and selection.
			reconcileInNS(reconciler, netboxTestNS, bmiName, 2)
			bmi := getBMIInNS(netboxTestNS, bmiName)
			Expect(bmi.Spec.ExternalHostID).To(Equal(netboxTestNS + "/" + hostname))

			err := reconcileExpectErrInNS(reconciler, netboxTestNS, bmiName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("different consumerRef"))

			bmi = getBMIInNS(netboxTestNS, bmiName)
			Expect(bmi.Spec.ExternalHostID).To(Equal(netboxTestNS + "/" + hostname))
			Expect(bmi.Spec.HostClass).To(BeEmpty())

			device := mock.device(42)
			Expect(device.Status).To(Equal("active"))
			Expect(device.CustomFields["osac_instance_id"]).To(Equal(string(bmi.UID)))

			Expect(netboxBMH(hostname).Spec.ConsumerRef).NotTo(BeNil())
			Expect(netboxBMH(hostname).Spec.ConsumerRef.Name).To(Equal("another-instance"))

			// Removing the conflict lets the retry prepare the host without
			// reselecting or releasing the claim.
			Expect(k8sClient.Delete(ctx, foreign)).To(Succeed())
			reconcileInNS(reconciler, netboxTestNS, bmiName, 1)
			Expect(netboxBMH(hostname).Spec.ConsumerRef).NotTo(BeNil())
			Expect(netboxBMH(hostname).Spec.ConsumerRef.Name).To(Equal(string(bmi.UID)))
			Expect(mock.device(42).CustomFields["osac_instance_id"]).To(Equal(string(bmi.UID)))

			markNetBoxBMHReady(hostname)
			reconcileInNS(reconciler, netboxTestNS, bmiName, 1)
			Expect(getBMIInNS(netboxTestNS, bmiName).Spec.HostClass).To(Equal(netboxHostClass))

			// Deletion releases the retained claim.
			Expect(k8sClient.Delete(ctx, getBMIInNS(netboxTestNS, bmiName))).To(Succeed())
			reconcileInNS(reconciler, netboxTestNS, bmiName, 2)
			err = k8sClient.Get(ctx, types.NamespacedName{Name: bmiName, Namespace: netboxTestNS}, &v1alpha1.BareMetalInstance{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
			Expect(mock.device(42).Status).To(Equal("staged"))
			Expect(mock.device(42).CustomFields["osac_instance_id"]).To(BeNil())
			Expect(netboxBMHMissing(hostname)).To(BeTrue())
		})
	})
})
