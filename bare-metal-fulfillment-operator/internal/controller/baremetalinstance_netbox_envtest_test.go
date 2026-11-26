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
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/credentials"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/credentials/bmh"
	netboxcredentials "github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/credentials/netbox"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/directclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/netboxclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/secretapi"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/test/netboxmock"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/test/wiremock"
)

const (
	netboxTestNS = "netbox-test"
	// testNetBoxToken is a synthetic fixture marker, not a credential.
	testNetBoxToken = "fixture-netbox-token"
	testBMCUsername = "fixture-bmc-user"
	testBMCPassword = "fixture-bmc-password"
	netboxDeviceBMC = "redfish-virtualmedia+https://192.168.1.42/redfish/v1/Systems/1"
	netboxDeviceMAC = "aa:bb:cc:dd:ee:42"
)

// fakeSecretAPI serves the system-scoped device Secrets the credential
// resolver lists and reads.
type fakeSecretAPI struct {
	secrets []secretapi.Secret
}

// List returns the seeded system Secrets.
func (f *fakeSecretAPI) List(_ context.Context, _ secretapi.Filter, _ int32) (secretapi.ListResult, error) {
	return secretapi.ListResult{Items: append([]secretapi.Secret(nil), f.secrets...), Total: int32(len(f.secrets))}, nil
}

// Get returns the seeded Secret with the given ID.
func (f *fakeSecretAPI) Get(_ context.Context, id string) (secretapi.Secret, error) {
	for _, secret := range f.secrets {
		if secret.ID == id {
			return secret, nil
		}
	}
	return secretapi.Secret{}, fmt.Errorf("secret %s not found", id)
}

// Close satisfies the Secret API client contract.
func (f *fakeSecretAPI) Close() error { return nil }

// newNetBoxCredentialsReader builds a reader backed by a fixture Secret.
func newNetBoxCredentialsReader(deviceID int) credentials.Reader {
	secret := secretapi.Secret{
		ID:     fmt.Sprintf("secret-%d", deviceID),
		Tenant: "system",
		Labels: map[string]string{"osac.openshift.io/netbox-device-" + strconv.Itoa(deviceID): ""},
		Data: map[string][]byte{
			"username": []byte(testBMCUsername),
			"password": []byte(testBMCPassword),
		},
	}
	return netboxcredentials.NewReader(&fakeSecretAPI{secrets: []secretapi.Secret{secret}})
}

// newNetBoxReconciler wires the real NetBox adapter and BMH manager to the
// shared envtest API server, mirroring newBCMReconciler.
func newNetBoxReconciler(server *httptest.Server, reader credentials.Reader) *BareMetalInstanceReconciler {
	direct, err := directclient.WrapForTest(k8sClient)
	Expect(err).NotTo(HaveOccurred())
	bmhManager, err := baremetalhost.NewManager(k8sClient, direct, netboxTestNS)
	Expect(err).NotTo(HaveOccurred())
	secretWriter, err := bmh.NewSecretWriter(direct, netboxTestNS)
	Expect(err).NotTo(HaveOccurred())
	api := netboxclient.NewClientForTest(server.Client(), server.URL, testNetBoxToken)
	inventoryClient := inventory.NewNetBoxClient(api, bmhManager, secretWriter, metal3HostClass, reader, true)
	return newTestReconciler(inventoryClient, netboxTestNS)
}

// createNetBoxBMI creates a BareMetalInstance with the given selector.
func createNetBoxBMI(name string, selector map[string]string) {
	bmi := &v1alpha1.BareMetalInstance{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: netboxTestNS},
		Spec: v1alpha1.BareMetalInstanceSpec{
			Selector:   v1alpha1.HostSelectorSpec{HostSelector: selector},
			TemplateID: shared.OsacNoopTemplate,
		},
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, bmi)).To(Succeed())
}

// netboxBMH returns the generated BareMetalHost.
func netboxBMH(name string) *metal3api.BareMetalHost {
	return getBMHInNS(netboxTestNS, name)
}

// cleanupNetBoxHost removes the BMI, its generated BMH and its BMC Secret.
func cleanupNetBoxHost(bmiName, hostname string) {
	cleanupBMIInNS(netboxTestNS, bmiName)
	cleanupBMHInNS(netboxTestNS, hostname)
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: baremetalhost.CredentialsSecretName(hostname), Namespace: netboxTestNS}}
	ExpectWithOffset(1, client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).NotTo(HaveOccurred())
}

// netboxDeviceFields returns managed-device fields for a fixture.
func netboxDeviceFields(cpuCores int) map[string]any {
	return map[string]any{
		"osac_managed":     true,
		"cpu_cores":        cpuCores,
		"osac_bmc_address": netboxDeviceBMC,
		"osac_boot_mac":    netboxDeviceMAC,
	}
}

var _ = Describe("BareMetalInstance NetBox Integration", func() {
	var mock *netboxmock.Fixture
	var admin wiremock.Admin
	var server *httptest.Server

	selector := map[string]string{"cpu_cores": "16"}

	BeforeEach(func(specCtx SpecContext) {
		ensureTestNamespace(netboxTestNS)
		baseURL, stop, err := wireMockLauncher.Start(specCtx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(stop)
		admin = wiremock.HTTPAdmin(baseURL)
		target, err := url.Parse(baseURL)
		Expect(err).NotTo(HaveOccurred())
		// Keep the NetBox client's TLS and CA trust path in the controller test.
		server = httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(target))
		DeferCleanup(server.Close)
	})

	configure := func(devices ...netboxmock.Device) {
		GinkgoHelper()
		var err error
		mock, err = netboxmock.Configure(ctx, admin, []string{testNetBoxToken}, selector, devices...)
		Expect(err).NotTo(HaveOccurred())
	}
	device := func(id int) netboxmock.Device {
		GinkgoHelper()
		snapshot, err := mock.Device(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		return snapshot
	}
	requestsFor := func(method, path string) []netboxmock.Request {
		GinkgoHelper()
		requests, err := mock.RequestsFor(ctx, method, path)
		Expect(err).NotTo(HaveOccurred())
		return requests
	}

	Describe("allocation", func() {
		const bmiName = "netbox-alloc-bmi"
		const hostname = "netbox-42"

		AfterEach(func() {
			cleanupNetBoxHost(bmiName, hostname)
		})

		It("allocates a host through a BareMetalInstance (TC-INT-001)", func() {
			configure(
				netboxmock.Device{ID: 42, Status: "staged", CustomFields: netboxDeviceFields(16)},
				netboxmock.Device{ID: 43, Status: "staged", CustomFields: netboxDeviceFields(8)},
			)
			reconciler := newNetBoxReconciler(server, newNetBoxCredentialsReader(42))

			createNetBoxBMI(bmiName, selector)

			// Finalizer, then selection: the host binding is persisted before any claim.
			reconcileInNS(reconciler, netboxTestNS, bmiName, 2)
			bmi := getBMIInNS(netboxTestNS, bmiName)
			Expect(bmi.Spec.ExternalHostID).To(Equal(netboxTestNS + "/" + hostname))
			Expect(bmi.Spec.ExternalHostName).To(Equal(hostname))

			listRequests := requestsFor(http.MethodGet, "/api/dcim/devices/")
			listCount := len(listRequests)
			Expect(listCount).To(BeNumerically(">", 0))
			query := listRequests[0].Query
			Expect(query["status"]).To(ConsistOf("staged"))
			Expect(query["cf_osac_managed"]).To(ConsistOf("true"))
			Expect(query["cf_osac_instance_id__empty"]).To(ConsistOf("true"))
			Expect(query["cf_cpu_cores"]).To(ConsistOf("16"))
			Expect(listRequests[0].TokenIndex).To(BeZero())
			claimPatches := len(requestsFor(http.MethodPatch, "/api/dcim/devices/42/"))
			Expect(claimPatches).To(Equal(0))

			// Claim: the device becomes active and owned by the BareMetalInstance UID.
			reconcileInNS(reconciler, netboxTestNS, bmiName, 1)
			bmi = getBMIInNS(netboxTestNS, bmiName)
			snapshot := device(42)
			Expect(snapshot.Status).To(Equal("active"))
			Expect(snapshot.CustomFields["osac_instance_id"]).To(Equal(string(bmi.UID)))
			Expect(device(43).Status).To(Equal("staged"))
			patches := requestsFor(http.MethodPatch, "/api/dcim/devices/42/")
			patchCount := len(patches)
			Expect(patchCount).To(Equal(1))
			Expect(patches[0].IfMatch).NotTo(BeEmpty())

			bmh := netboxBMH(hostname)
			Expect(bmh.Spec.BMC.Address).To(Equal(netboxDeviceBMC))
			Expect(bmh.Spec.BMC.CredentialsName).To(Equal(baremetalhost.CredentialsSecretName(hostname)))
			Expect(bmh.Spec.BootMACAddress).To(Equal(netboxDeviceMAC))
			Expect(bmh.Spec.ConsumerRef).NotTo(BeNil())
			Expect(bmh.Spec.ConsumerRef.Kind).To(Equal("BareMetalInstance"))
			Expect(bmh.Spec.ConsumerRef.Name).To(Equal(string(bmi.UID)))

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: baremetalhost.CredentialsSecretName(hostname), Namespace: netboxTestNS}, secret)).To(Succeed())
			usernameMatches := string(secret.Data["username"]) == testBMCUsername
			Expect(usernameMatches).To(BeTrue())
			passwordMatches := subtle.ConstantTimeCompare(secret.Data["password"], []byte(testBMCPassword)) == 1
			Expect(passwordMatches).To(BeTrue())
			Expect(secret.Labels["osac.openshift.io/managed-by"]).To(Equal(shared.OsacDefaultManagedByValue))
			Expect(secret.Labels["osac.openshift.io/bare-metal-instance-uid"]).To(Equal(string(bmi.UID)))

			// Host is reserved but not available yet.
			Expect(bmi.Spec.HostClass).To(BeEmpty())

			markBMHReadyInNS(netboxTestNS, hostname)
			reconcileInNS(reconciler, netboxTestNS, bmiName, 1)

			bmi = getBMIInNS(netboxTestNS, bmiName)
			Expect(bmi.Spec.HostClass).To(Equal(metal3HostClass))
			Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseProgressing))
			Expect(bmi.IsStatusConditionTrue(v1alpha1.HostConditionAllocated)).To(BeTrue())
		})

		It("releases the NetBox claim when the BareMetalInstance is deleted (TC-INT-002)", func() {
			configure(netboxmock.Device{ID: 42, Status: "staged", CustomFields: netboxDeviceFields(16)})
			reconciler := newNetBoxReconciler(server, newNetBoxCredentialsReader(42))

			createNetBoxBMI(bmiName, selector)
			driveToAllocatedIn(reconciler, netboxTestNS, bmiName, hostname)

			bmi := getBMIInNS(netboxTestNS, bmiName)
			Expect(bmi.Spec.HostClass).To(Equal(metal3HostClass))

			Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
			reconcileInNS(reconciler, netboxTestNS, bmiName, 2)

			err := k8sClient.Get(ctx, types.NamespacedName{Name: bmiName, Namespace: netboxTestNS}, &v1alpha1.BareMetalInstance{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the inventory finalizer should be released")

			snapshot := device(42)
			Expect(snapshot.Status).To(Equal("staged"))
			Expect(snapshot.CustomFields["osac_instance_id"]).To(BeNil())
			Expect(snapshot.CustomFields["cpu_cores"]).To(BeNumerically("==", 16))
			Expect(bmhExistsInNS(netboxTestNS, hostname)).To(BeFalse())

			secret := &corev1.Secret{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: baremetalhost.CredentialsSecretName(hostname), Namespace: netboxTestNS}, secret)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			patches := requestsFor(http.MethodPatch, "/api/dcim/devices/42/")
			patchCount := len(patches)
			Expect(patchCount).To(Equal(2))
			Expect(patches[1].IfMatch).NotTo(BeEmpty())
		})
	})

	Describe("claim recovery", func() {
		const bmiName = "netbox-claim-bmi"
		const hostname = "netbox-42"

		AfterEach(func() {
			cleanupNetBoxHost(bmiName, hostname)
		})

		It("retries host preparation without losing the NetBox claim (TC-INT-003)", func() {
			configure(netboxmock.Device{ID: 42, Status: "staged", CustomFields: netboxDeviceFields(16)})

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

			reconciler := newNetBoxReconciler(server, newNetBoxCredentialsReader(42))
			createNetBoxBMI(bmiName, selector)

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

			snapshot := device(42)
			Expect(snapshot.Status).To(Equal("active"))
			Expect(snapshot.CustomFields["osac_instance_id"]).To(Equal(string(bmi.UID)))

			foreignBMH := netboxBMH(hostname)
			Expect(foreignBMH.Spec.ConsumerRef).NotTo(BeNil())
			Expect(foreignBMH.Spec.ConsumerRef.Name).To(Equal("another-instance"))

			// Removing the conflict lets the retry prepare the host without
			// reselecting or releasing the claim.
			Expect(k8sClient.Delete(ctx, foreign)).To(Succeed())
			reconcileInNS(reconciler, netboxTestNS, bmiName, 1)
			searchCount := len(requestsFor(http.MethodGet, "/api/dcim/devices/"))
			Expect(searchCount).To(Equal(1))
			retryPatchCount := len(requestsFor(http.MethodPatch, "/api/dcim/devices/42/"))
			Expect(retryPatchCount).To(Equal(1))
			claimedBMH := netboxBMH(hostname)
			Expect(claimedBMH.Spec.ConsumerRef).NotTo(BeNil())
			Expect(claimedBMH.Spec.ConsumerRef.Name).To(Equal(string(bmi.UID)))
			Expect(device(42).CustomFields["osac_instance_id"]).To(Equal(string(bmi.UID)))

			markBMHReadyInNS(netboxTestNS, hostname)
			reconcileInNS(reconciler, netboxTestNS, bmiName, 1)
			Expect(getBMIInNS(netboxTestNS, bmiName).Spec.HostClass).To(Equal(metal3HostClass))

			// Deletion releases the retained claim.
			Expect(k8sClient.Delete(ctx, getBMIInNS(netboxTestNS, bmiName))).To(Succeed())
			reconcileInNS(reconciler, netboxTestNS, bmiName, 2)
			err = k8sClient.Get(ctx, types.NamespacedName{Name: bmiName, Namespace: netboxTestNS}, &v1alpha1.BareMetalInstance{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
			Expect(device(42).Status).To(Equal("staged"))
			Expect(device(42).CustomFields["osac_instance_id"]).To(BeNil())
			Expect(bmhExistsInNS(netboxTestNS, hostname)).To(BeFalse())
		})
	})
})
