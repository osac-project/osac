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
	"net/http"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

const (
	netboxBMCSecretName = "netbox-42-bmc-secret"
	netboxDeviceBMC     = "redfish-virtualmedia+https://192.168.1.42/redfish/v1/Systems/1"
	netboxDeviceMAC     = "aa:bb:cc:dd:ee:42"
)

func netboxDeviceFields(cpuCores int) map[string]any {
	return map[string]any{
		"osac_managed":     true,
		"cpu_cores":        cpuCores,
		"osac_bmc_address": netboxDeviceBMC,
		"osac_boot_mac":    netboxDeviceMAC,
	}
}

var _ = Describe("BareMetalInstance NetBox allocation and release", func() {
	var mock *mockNetBox

	BeforeEach(func() {
		ensureTestNamespace(netboxTestNS)
		mock = newMockNetBox()
	})

	AfterEach(func() {
		mock.stop()
	})

	Describe("allocation", func() {
		const bmiName = "netbox-alloc-bmi"
		const hostname = "netbox-42"

		AfterEach(func() {
			cleanupNetBoxHost(bmiName, hostname)
		})

		It("TC-INT-001 allocates a matching staged NetBox device", func() {
			mock.addDevice(42, "staged", netboxDeviceFields(16))
			mock.addDevice(43, "staged", netboxDeviceFields(8))
			reconciler := newNetBoxReconciler(mock, newNetBoxCredentialResolver(42))

			createNetBoxBMI(bmiName, map[string]string{"cpu_cores": "16"}, nil)

			// Finalizer, then selection: the host binding is persisted before any claim.
			reconcileInNS(reconciler, netboxTestNS, bmiName, 2)
			bmi := getBMIInNS(netboxTestNS, bmiName)
			Expect(bmi.Spec.ExternalHostID).To(Equal(netboxTestNS + "/" + hostname))
			Expect(bmi.Spec.ExternalHostName).To(BeEmpty())
			Expect(bmi.Annotations).To(BeEmpty())

			listRequests := mock.requestsFor(http.MethodGet, "/api/dcim/devices/")
			Expect(listRequests).NotTo(BeEmpty())
			query := listRequests[0].Query
			Expect(query["status"]).To(ConsistOf("staged"))
			Expect(query["cf_osac_managed"]).To(ConsistOf("true"))
			Expect(query["cf_osac_instance_id__empty"]).To(ConsistOf("true"))
			Expect(query["cf_cpu_cores"]).To(ConsistOf("16"))
			Expect(mock.requestsFor(http.MethodPatch, "/api/dcim/devices/42/")).To(BeEmpty())

			// Claim: the device becomes active and owned by the BareMetalInstance UID.
			reconcileInNS(reconciler, netboxTestNS, bmiName, 1)
			bmi = getBMIInNS(netboxTestNS, bmiName)
			device := mock.device(42)
			Expect(device.Status).To(Equal("active"))
			Expect(device.CustomFields["osac_instance_id"]).To(Equal(string(bmi.UID)))
			Expect(mock.device(43).Status).To(Equal("staged"))
			patches := mock.requestsFor(http.MethodPatch, "/api/dcim/devices/42/")
			Expect(patches).To(HaveLen(1))
			Expect(patches[0].IfMatch).NotTo(BeEmpty())

			bmh := netboxBMH(hostname)
			Expect(bmh.Spec.BMC.Address).To(Equal(netboxDeviceBMC))
			Expect(bmh.Spec.BMC.CredentialsName).To(Equal(netboxBMCSecretName))
			Expect(bmh.Spec.BootMACAddress).To(Equal(netboxDeviceMAC))
			Expect(bmh.Spec.ConsumerRef).NotTo(BeNil())
			Expect(bmh.Spec.ConsumerRef.Kind).To(Equal("BareMetalInstance"))
			Expect(bmh.Spec.ConsumerRef.Name).To(Equal(string(bmi.UID)))

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: netboxBMCSecretName, Namespace: netboxTestNS}, secret)).To(Succeed())
			Expect(secret.Data["username"]).To(Equal([]byte("bmc-user")))
			Expect(secret.Data["password"]).To(Equal([]byte("bmc-password")))
			Expect(secret.Labels["osac.openshift.io/managed-by"]).To(Equal(shared.OsacDefaultManagedByValue))
			Expect(secret.Labels["osac.openshift.io/bare-metal-instance-uid"]).To(Equal(string(bmi.UID)))

			// Host is reserved but not available yet.
			Expect(bmi.Spec.HostClass).To(BeEmpty())

			markNetBoxBMHReady(hostname)
			reconcileInNS(reconciler, netboxTestNS, bmiName, 1)

			bmi = getBMIInNS(netboxTestNS, bmiName)
			Expect(bmi.Spec.HostClass).To(Equal(netboxHostClass))
			Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseProgressing))
			Expect(bmi.IsStatusConditionTrue(v1alpha1.HostConditionAllocated)).To(BeTrue())
		})

		It("TC-INT-002 releases the NetBox claim on deletion", func() {
			mock.addDevice(42, "staged", netboxDeviceFields(16))
			reconciler := newNetBoxReconciler(mock, newNetBoxCredentialResolver(42))

			createNetBoxBMI(bmiName, map[string]string{"cpu_cores": "16"}, nil)
			driveNetBoxToAllocated(reconciler, bmiName, hostname)

			bmi := getBMIInNS(netboxTestNS, bmiName)
			Expect(bmi.Spec.HostClass).To(Equal(netboxHostClass))

			Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
			reconcileInNS(reconciler, netboxTestNS, bmiName, 2)

			err := k8sClient.Get(ctx, types.NamespacedName{Name: bmiName, Namespace: netboxTestNS}, &v1alpha1.BareMetalInstance{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the inventory finalizer should be released")

			device := mock.device(42)
			Expect(device.Status).To(Equal("staged"))
			Expect(device.CustomFields["osac_instance_id"]).To(BeNil())
			Expect(netboxBMHMissing(hostname)).To(BeTrue())

			secret := &corev1.Secret{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: netboxBMCSecretName, Namespace: netboxTestNS}, secret)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			patches := mock.requestsFor(http.MethodPatch, "/api/dcim/devices/42/")
			Expect(patches).To(HaveLen(2), "one claim and one release PATCH")
			Expect(patches[1].IfMatch).NotTo(BeEmpty())
		})
	})
})
