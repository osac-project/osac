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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/netboxclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/secretapi"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

const (
	netboxTestNS    = "netbox-test"
	netboxHostClass = "metal3"
	// netboxTestToken is a synthetic marker, not a credential.
	netboxTestToken = "test-netbox-token"
)

// mockNetBoxDevice is one dcim.device as the NetBox API would return it.
type mockNetBoxDevice struct {
	ID           int
	Name         string
	Status       string
	CustomFields map[string]any
	version      int
}

func (d *mockNetBoxDevice) etag() string {
	return fmt.Sprintf(`"v%d"`, d.version)
}

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
// It implements the subset of the device API the NetBox adapter uses:
// paginated list with exact custom-field filters, detail reads with ETags,
// and conditional PATCHes.
type mockNetBox struct {
	mu       sync.Mutex
	server   *httptest.Server
	devices  map[int]*mockNetBoxDevice
	requests []recordedRequest
}

func newMockNetBox() *mockNetBox {
	m := &mockNetBox{devices: map[int]*mockNetBoxDevice{}}
	m.server = httptest.NewTLSServer(http.HandlerFunc(m.handle))
	return m
}

func (m *mockNetBox) stop() {
	if m.server != nil {
		m.server.Close()
	}
}

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

func isEmptyCustomField(value any) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return text == ""
	}
	return false
}

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

func deviceIDFromPath(path string) (int, error) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(path, "/api/dcim/devices/"), "/")
	return strconv.Atoi(trimmed)
}

// fakeSecretAPI serves the system-scoped device Secrets the credential
// resolver lists and reads.
type fakeSecretAPI struct {
	secrets []secretapi.Secret
}

func (f *fakeSecretAPI) List(_ context.Context, _ secretapi.Filter, _ int32) (secretapi.ListResult, error) {
	return secretapi.ListResult{Items: append([]secretapi.Secret(nil), f.secrets...), Total: int32(len(f.secrets))}, nil
}

func (f *fakeSecretAPI) Get(_ context.Context, id string) (secretapi.Secret, error) {
	for _, secret := range f.secrets {
		if secret.ID == id {
			return secret, nil
		}
	}
	return secretapi.Secret{}, fmt.Errorf("secret %s not found", id)
}

func (f *fakeSecretAPI) Close() error { return nil }

func newNetBoxCredentialResolver(deviceIDs ...int) inventory.CredentialsReaderRepository {
	secrets := make([]secretapi.Secret, 0, len(deviceIDs))
	for _, id := range deviceIDs {
		secrets = append(secrets, secretapi.Secret{
			ID:     fmt.Sprintf("secret-%d", id),
			Tenant: "system",
			Labels: map[string]string{"osac.openshift.io/netbox-device-" + strconv.Itoa(id): ""},
			Data: map[string][]byte{
				"username": []byte("bmc-user"),
				"password": []byte("bmc-password"),
			},
		})
	}
	return inventory.NewNetBoxCredentialResolver(&fakeSecretAPI{secrets: secrets})
}

// netboxAPIFor builds the NetBox client against the mock server.
func netboxAPIFor(m *mockNetBox) inventory.NetBoxAPI {
	return netboxclient.NewClientForTest(m.server.Client(), m.server.URL, netboxTestToken)
}

// newNetBoxReconciler wires the real NetBox adapter and BMH manager to the
// shared envtest API server, mirroring newBCMReconciler.
func newNetBoxReconciler(m *mockNetBox, credentials inventory.CredentialsReaderRepository) *BareMetalInstanceReconciler {
	bmhManager := baremetalhost.NewManager(k8sClient, k8sClient, netboxTestNS)
	inventoryClient := inventory.NewNetBoxClient(netboxAPIFor(m), bmhManager, netboxHostClass, credentials)
	return newTestReconciler(inventoryClient, netboxTestNS)
}

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

func netboxBMH(name string) *metal3api.BareMetalHost {
	return getBMHInNS(netboxTestNS, name)
}

// cleanupNetBoxHost removes the BMI, its generated BMH and its BMC Secret.
func cleanupNetBoxHost(bmiName, hostname string) {
	cleanupBMIInNS(netboxTestNS, bmiName)
	cleanupBMHInNS(netboxTestNS, hostname)
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: hostname + "-bmc-secret", Namespace: netboxTestNS}}
	ExpectWithOffset(1, client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).NotTo(HaveOccurred())
}
