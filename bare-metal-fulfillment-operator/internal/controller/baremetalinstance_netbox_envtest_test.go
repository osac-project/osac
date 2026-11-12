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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"

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
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/netboxclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/secretapi"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
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

// mockNetBoxDevice is one dcim.device as the NetBox API would return it.
type mockNetBoxDevice struct {
	ID           int
	Name         string
	Status       string
	CustomFields map[string]any
	version      int
}

// etag returns the mock's current ETag for the device.
func (d *mockNetBoxDevice) etag() string {
	return fmt.Sprintf(`"v%d"`, d.version)
}

// response renders the device as the NetBox API response body.
func (d *mockNetBoxDevice) response() map[string]any {
	return netboxclient.DevicePayloadForTest(d.ID, d.Name, d.Status, d.CustomFields)
}

// recordedRequest keeps the parts of an API call the specs assert on.
type recordedRequest struct {
	Method        string
	Path          string
	Query         map[string][]string
	IfMatch       string
	Authorization string
}

// mockNetBox is a stateful NetBox HTTPS endpoint for the envtest reconciler.
// It implements the subset of the device API the NetBox adapter uses: exact
// custom-field filters, detail reads with ETags, and conditional PATCHes.
type mockNetBox struct {
	mu       sync.Mutex
	server   *httptest.Server
	devices  map[int]*mockNetBoxDevice
	requests []recordedRequest
}

// newMockNetBox starts a mock NetBox HTTPS server.
func newMockNetBox() *mockNetBox {
	m := &mockNetBox{devices: map[int]*mockNetBoxDevice{}}
	m.server = httptest.NewTLSServer(http.HandlerFunc(m.handle))
	return m
}

// stop shuts down the mock server.
func (m *mockNetBox) stop() {
	m.server.Close()
}

// addDevice registers a fixture device.
func (m *mockNetBox) addDevice(id int, status string, fields map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	custom := make(map[string]any, len(fields))
	for key, value := range fields {
		custom[key] = value
	}
	m.devices[id] = &mockNetBoxDevice{
		ID:           id,
		Name:         fmt.Sprintf("device-%d", id),
		Status:       status,
		CustomFields: custom,
	}
}

// device returns a copy of the fixture device state.
func (m *mockNetBox) device(id int) mockNetBoxDevice {
	m.mu.Lock()
	defer m.mu.Unlock()
	device, ok := m.devices[id]
	if !ok {
		return mockNetBoxDevice{}
	}
	snapshot := *device
	snapshot.CustomFields = make(map[string]any, len(device.CustomFields))
	for key, value := range device.CustomFields {
		snapshot.CustomFields[key] = value
	}
	return snapshot
}

// requestsFor returns recorded calls matching method and path.
func (m *mockNetBox) requestsFor(method, path string) []recordedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]recordedRequest, 0, len(m.requests))
	for _, request := range m.requests {
		if request.Method == method && request.Path == path {
			result = append(result, request)
		}
	}
	return result
}

// handle routes the device API calls used by the adapter.
func (m *mockNetBox) handle(w http.ResponseWriter, r *http.Request) {
	defer GinkgoRecover()

	m.mu.Lock()
	m.requests = append(m.requests, recordedRequest{
		Method:        r.Method,
		Path:          r.URL.Path,
		Query:         r.URL.Query(),
		IfMatch:       r.Header.Get("If-Match"),
		Authorization: r.Header.Get("Authorization"),
	})
	m.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && strings.TrimSuffix(r.URL.Path, "/") == "/api/dcim/devices":
		m.listDevices(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/dcim/devices/"):
		m.getDevice(w, r)
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/dcim/devices/"):
		m.patchDevice(w, r)
	default:
		http.NotFound(w, r)
	}
}

// listDevices applies the adapter filters to the device fixtures.
func (m *mockNetBox) listDevices(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	m.mu.Lock()
	matches := make([]*mockNetBoxDevice, 0, len(m.devices))
	for _, device := range m.devices {
		if deviceMatchesQuery(device, query) {
			matches = append(matches, device)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	results := make([]map[string]any, 0, len(matches))
	for _, device := range matches {
		results = append(results, device.response())
	}
	m.mu.Unlock()

	writeJSON(w, map[string]any{
		"count":    len(results),
		"next":     nil,
		"previous": nil,
		"results":  results,
	})
}

// deviceMatchesQuery reports whether a device matches the request query.
func deviceMatchesQuery(device *mockNetBoxDevice, query map[string][]string) bool {
	for key, values := range query {
		if len(values) == 0 {
			continue
		}
		value := values[0]
		if key == "status" {
			if device.Status != value {
				return false
			}
			continue
		}
		if !strings.HasPrefix(key, "cf_") {
			continue
		}
		name := strings.TrimPrefix(key, "cf_")
		if strings.HasSuffix(name, "__empty") {
			field := strings.TrimSuffix(name, "__empty")
			empty := isEmptyCustomField(device.CustomFields[field])
			if (value == "true") != empty {
				return false
			}
			continue
		}
		if fmt.Sprint(device.CustomFields[name]) != value {
			return false
		}
	}
	return true
}

// isEmptyCustomField reports whether a custom field is absent or empty.
func isEmptyCustomField(value any) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return text == ""
	}
	return false
}

// getDevice returns one device with its current ETag.
func (m *mockNetBox) getDevice(w http.ResponseWriter, r *http.Request) {
	id, err := deviceIDFromPath(r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	m.mu.Lock()
	device, ok := m.devices[id]
	m.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("ETag", device.etag())
	writeJSON(w, device.response())
}

// patchDevice applies a conditional device PATCH.
func (m *mockNetBox) patchDevice(w http.ResponseWriter, r *http.Request) {
	id, err := deviceIDFromPath(r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var body struct {
		Status       *string        `json:"status"`
		CustomFields map[string]any `json:"custom_fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	device, ok := m.devices[id]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("If-Match") != device.etag() {
		http.Error(w, "precondition failed", http.StatusPreconditionFailed)
		return
	}

	if body.Status != nil {
		device.Status = *body.Status
	}
	if body.CustomFields != nil {
		for key, value := range body.CustomFields {
			device.CustomFields[key] = value
		}
	}
	device.version++

	w.Header().Set("ETag", device.etag())
	writeJSON(w, device.response())
}

// deviceIDFromPath parses the numeric device ID from a request path.
func deviceIDFromPath(path string) (int, error) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(path, "/api/dcim/devices/"), "/")
	return strconv.Atoi(trimmed)
}

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
func newNetBoxReconciler(m *mockNetBox, reader credentials.Reader) *BareMetalInstanceReconciler {
	bmhManager := baremetalhost.NewManager(k8sClient, k8sClient, netboxTestNS)
	secretWriter := bmh.NewSecretWriter(k8sClient, netboxTestNS)
	api := netboxclient.NewClientForTest(m.server.Client(), m.server.URL, testNetBoxToken)
	inventoryClient := inventory.NewNetBoxClient(api, bmhManager, secretWriter, metal3HostClass, reader)
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
			reconciler := newNetBoxReconciler(mock, newNetBoxCredentialsReader(42))

			createNetBoxBMI(bmiName, map[string]string{"cpu_cores": "16"})

			// Finalizer, then selection: the host binding is persisted before any claim.
			reconcileInNS(reconciler, netboxTestNS, bmiName, 2)
			bmi := getBMIInNS(netboxTestNS, bmiName)
			Expect(bmi.Spec.ExternalHostID).To(Equal(netboxTestNS + "/" + hostname))
			Expect(bmi.Spec.ExternalHostName).To(Equal(hostname))

			listRequests := mock.requestsFor(http.MethodGet, "/api/dcim/devices/")
			Expect(listRequests).NotTo(BeEmpty())
			query := listRequests[0].Query
			Expect(query["status"]).To(ConsistOf("staged"))
			Expect(query["cf_osac_managed"]).To(ConsistOf("true"))
			Expect(query["cf_osac_instance_id__empty"]).To(ConsistOf("true"))
			Expect(query["cf_cpu_cores"]).To(ConsistOf("16"))
			Expect(strings.HasPrefix(listRequests[0].Authorization, "Token ")).To(BeTrue())
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

		It("TC-INT-002 releases the NetBox claim on deletion", func() {
			mock.addDevice(42, "staged", netboxDeviceFields(16))
			reconciler := newNetBoxReconciler(mock, newNetBoxCredentialsReader(42))

			createNetBoxBMI(bmiName, map[string]string{"cpu_cores": "16"})
			driveToAllocatedIn(reconciler, netboxTestNS, bmiName, hostname)

			bmi := getBMIInNS(netboxTestNS, bmiName)
			Expect(bmi.Spec.HostClass).To(Equal(metal3HostClass))

			Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
			reconcileInNS(reconciler, netboxTestNS, bmiName, 2)

			err := k8sClient.Get(ctx, types.NamespacedName{Name: bmiName, Namespace: netboxTestNS}, &v1alpha1.BareMetalInstance{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the inventory finalizer should be released")

			device := mock.device(42)
			Expect(device.Status).To(Equal("staged"))
			Expect(device.CustomFields["osac_instance_id"]).To(BeNil())
			Expect(bmhExistsInNS(netboxTestNS, hostname)).To(BeFalse())

			secret := &corev1.Secret{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: baremetalhost.CredentialsSecretName(hostname), Namespace: netboxTestNS}, secret)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			patches := mock.requestsFor(http.MethodPatch, "/api/dcim/devices/42/")
			Expect(patches).To(HaveLen(2), "one claim and one release PATCH")
			Expect(patches[1].IfMatch).NotTo(BeEmpty())
		})
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

			reconciler := newNetBoxReconciler(mock, newNetBoxCredentialsReader(42))
			createNetBoxBMI(bmiName, map[string]string{"cpu_cores": "16"})

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

			foreignBMH := netboxBMH(hostname)
			Expect(foreignBMH.Spec.ConsumerRef).NotTo(BeNil())
			Expect(foreignBMH.Spec.ConsumerRef.Name).To(Equal("another-instance"))

			// Removing the conflict lets the retry prepare the host without
			// reselecting or releasing the claim.
			Expect(k8sClient.Delete(ctx, foreign)).To(Succeed())
			reconcileInNS(reconciler, netboxTestNS, bmiName, 1)
			Expect(mock.requestsFor(http.MethodGet, "/api/dcim/devices/")).To(HaveLen(1))
			Expect(mock.requestsFor(http.MethodPatch, "/api/dcim/devices/42/")).To(HaveLen(1))
			claimedBMH := netboxBMH(hostname)
			Expect(claimedBMH.Spec.ConsumerRef).NotTo(BeNil())
			Expect(claimedBMH.Spec.ConsumerRef.Name).To(Equal(string(bmi.UID)))
			Expect(mock.device(42).CustomFields["osac_instance_id"]).To(Equal(string(bmi.UID)))

			markBMHReadyInNS(netboxTestNS, hostname)
			reconcileInNS(reconciler, netboxTestNS, bmiName, 1)
			Expect(getBMIInNS(netboxTestNS, bmiName).Spec.HostClass).To(Equal(metal3HostClass))

			// Deletion releases the retained claim.
			Expect(k8sClient.Delete(ctx, getBMIInNS(netboxTestNS, bmiName))).To(Succeed())
			reconcileInNS(reconciler, netboxTestNS, bmiName, 2)
			err = k8sClient.Get(ctx, types.NamespacedName{Name: bmiName, Namespace: netboxTestNS}, &v1alpha1.BareMetalInstance{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
			Expect(mock.device(42).Status).To(Equal("staged"))
			Expect(mock.device(42).CustomFields["osac_instance_id"]).To(BeNil())
			Expect(bmhExistsInNS(netboxTestNS, hostname)).To(BeFalse())
		})
	})
})
