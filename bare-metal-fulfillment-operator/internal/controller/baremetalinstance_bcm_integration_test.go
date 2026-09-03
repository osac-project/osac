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
	"net/http/httptest"
	"sync"

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/bcmclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/management"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

const (
	bcmTestNS    = "bcm-test"
	bcmHostClass = "bcm"
)

// mockBCM is an in-memory stand-in for the BCM JSON API backed by an
// httptest.Server. It stores devices as decoded JSON objects so the
// GET-modify-PUT assignment round-trips (osac_instance_id) persist across
// reconciles, and supports fault injection for the error-path tests.
type mockBCM struct {
	mu         sync.Mutex
	server     *httptest.Server
	devices    map[string]map[string]any // hostname -> device object
	categories []map[string]any
	partitions []map[string]any

	down     bool   // simulate connection failure (hijack + close)
	failCall string // "service.call" to answer with an error response
}

func newMockBCM() *mockBCM {
	m := &mockBCM{devices: map[string]map[string]any{}}
	// TLS server for parity with the real mTLS BCM transport and with
	// bcmclient/client_test.go. EnableHTTP2 is left false because HTTP/2
	// doesn't support http.Hijacker (needed by setDown to simulate
	// connection drops for the unreachable-BCM error-path tests).
	m.server = httptest.NewTLSServer(http.HandlerFunc(m.handle))
	return m
}

func (m *mockBCM) stop() {
	if m.server != nil {
		m.server.Close()
	}
}

// addLiteNode registers a free LiteNode with device-level BMC credentials and a
// pre-configured (Priority-1) BMC address, so credential/address resolution
// needs no category/partition lookup or Redfish discoverer.
func (m *mockBCM) addLiteNode(hostname, resourceClass string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.devices[hostname] = map[string]any{
		"baseType":  "Device",
		"childType": "LiteNode",
		"uuid":      "uuid-" + hostname,
		"hostname":  hostname,
		"mac":       "aa:bb:cc:dd:ee:01",
		"bmcSettings": map[string]any{
			"baseType": "BMCSettings",
			"userName": "root",
			"password": "calvin",
			"userID":   2,
		},
		"extra_values": map[string]any{
			"resource_class":   resourceClass,
			"osac_bmc_address": "ipmi://10.0.0.1",
		},
	}
}

func (m *mockBCM) device(hostname string) map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.devices[hostname]
}

// removeDevice deletes a device so getDevice returns null (orphan/cleanup cases).
func (m *mockBCM) removeDevice(hostname string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.devices, hostname)
}

func (m *mockBCM) setDown(down bool)      { m.mu.Lock(); m.down = down; m.mu.Unlock() }
func (m *mockBCM) setFailCall(key string) { m.mu.Lock(); m.failCall = key; m.mu.Unlock() }

func (m *mockBCM) handle(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.down {
		// Simulate an unreachable BCM: drop the connection so the client sees a
		// transport error (classified as ErrConnectionFailed), not an HTTP status.
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	if r.URL.Path == "/rest/v1/version" {
		writeJSON(w, map[string]any{"cm_version": "11.0", "cmd_version": "3.1"})
		return
	}

	var req struct {
		Service string            `json:"service"`
		Call    string            `json:"call"`
		Args    []json.RawMessage `json:"args"`
	}
	// args may be {} or [] — tolerate both by ignoring decode errors on args.
	body := map[string]json.RawMessage{}
	_ = json.NewDecoder(r.Body).Decode(&body)
	_ = json.Unmarshal(body["service"], &req.Service)
	_ = json.Unmarshal(body["call"], &req.Call)
	_ = json.Unmarshal(body["args"], &req.Args)

	key := req.Service + "." + req.Call
	if m.failCall == key {
		writeJSON(w, map[string]any{"errormessage": "simulated BCM error for " + key})
		return
	}

	switch key {
	case "cmdevice.getDevices":
		out := make([]map[string]any, 0, len(m.devices))
		for _, d := range m.devices {
			out = append(out, d)
		}
		writeJSON(w, out)
	case "cmdevice.getDevice":
		var hostname string
		if len(req.Args) > 0 {
			_ = json.Unmarshal(req.Args[0], &hostname)
		}
		if d, ok := m.devices[hostname]; ok {
			writeJSON(w, d)
		} else {
			_, _ = w.Write([]byte("null"))
		}
	case "cmdevice.updateDevice":
		var dev map[string]any
		if len(req.Args) > 0 {
			_ = json.Unmarshal(req.Args[0], &dev)
		}
		if hn, ok := dev["hostname"].(string); ok {
			m.devices[hn] = dev
		}
		writeJSON(w, map[string]any{"success": true, "task_uuid": "0", "validation": []any{}})
	case "cmdevice.getCategories":
		writeJSON(w, m.categories)
	case "cmpart.getPartitions":
		writeJSON(w, m.partitions)
	default:
		writeJSON(w, map[string]any{"errormessage": "no such call: " + key})
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// newBCMReconciler wires a BareMetalInstance reconciler with the BCM inventory
// backend pointed at the mock server, a real (envtest-backed) BMH manager, and
// the Metal3 management client.
func newBCMReconciler(mock *mockBCM) *BareMetalInstanceReconciler {
	bcmAPI := bcmclient.NewClientForTest(mock.server.Client(), mock.server.URL)
	bmhMgr := baremetalhost.NewManager(k8sClient, k8sClient, bcmTestNS)
	invClient := inventory.NewBCMClient(bcmAPI, bmhMgr, bcmHostClass)
	mgmtClient := management.NewMetal3ClientForTest(k8sClient, bcmTestNS)
	return NewBareMetalInstanceReconciler(
		k8sClient,
		k8sClient.Scheme(),
		invClient,
		mgmtClient,
		nil, // provisioning provider
		nil, // networking provider
		nil, // IP discovery provider
		nil, // AAP client
		0, 0, 0, 0, 0,
	)
}

// These are thin, namespace-bound aliases over the shared integration helpers
// in baremetalinstance_integration_helpers_test.go.

func reconcileBCM(reconciler *BareMetalInstanceReconciler, name string, n int) ctrl.Result {
	return reconcileInNS(reconciler, bcmTestNS, name, n)
}

func reconcileBCMExpectErr(reconciler *BareMetalInstanceReconciler, name string) error {
	return reconcileExpectErrInNS(reconciler, bcmTestNS, name)
}

func getBCMBMI(name string) *v1alpha1.BareMetalInstance { return getBMIInNS(bcmTestNS, name) }

func cleanupBCMBMI(name string) { cleanupBMIInNS(bcmTestNS, name) }

func cleanupBCMBMH(name string) { cleanupBMHInNS(bcmTestNS, name) }

// markBMHReady flips a BMH's status to available+OK so IsBMHReady returns true.
// It stands in for the Metal3 baremetal-operator, which is not running in
// envtest (mirrors the Metal3 suite's "simulate BMO reconciliation" step).
func markBMHReady(name string) {
	bmh := getBMHInNS(bcmTestNS, name)
	bmh.Status.OperationalStatus = metal3api.OperationalStatusOK
	bmh.Status.Provisioning.State = metal3api.StateAvailable
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, bmh)).To(Succeed())
}

func bmhExistsInNS(name string) bool {
	err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: bcmTestNS}, &metal3api.BareMetalHost{})
	return err == nil
}

func createBCMBMI(name, resourceClass string) {
	bmi := &v1alpha1.BareMetalInstance{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: bcmTestNS},
		Spec: v1alpha1.BareMetalInstanceSpec{
			Selector: v1alpha1.HostSelectorSpec{
				// The BCM backend matches selector keys against a device's
				// extra_values, where resource_class lives.
				HostSelector: map[string]string{
					"resource_class": resourceClass,
				},
			},
			TemplateID: shared.OsacNoopTemplate,
		},
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, bmi)).To(Succeed())
}

// driveToAllocated runs the reconciles that take a fresh BMI through
// finalizer → FindFreeHost → AssignHost(create BMH, not ready) and then, after
// the BMH is marked ready, one more reconcile to reach HostClass/Progressing.
func driveToAllocated(reconciler *BareMetalInstanceReconciler, bmiName, hostname string) {
	// finalizer, FindFreeHost (sets ExternalHostID), AssignHost (creates BMH, Ready=false)
	reconcileBCM(reconciler, bmiName, 3)
	ExpectWithOffset(1, bmhExistsInNS(hostname)).To(BeTrue(), "AssignHost should have created the BMH")
	markBMHReady(hostname)
	// AssignHost again: IsBMHReady=true → HostClass set → Progressing
	reconcileBCM(reconciler, bmiName, 1)
}

var _ = Describe("BareMetalInstance BCM Integration", func() {
	var mock *mockBCM

	BeforeEach(func() {
		ensureTestNamespace(bcmTestNS)
		mock = newMockBCM()
	})

	AfterEach(func() {
		mock.stop()
	})

	Describe("Full allocation flow", func() {
		const bmiName = "bcm-alloc-bmi"
		const hostname = "node-alloc"

		AfterEach(func() {
			cleanupBCMBMI(bmiName)
			cleanupBCMBMH(hostname)
		})

		It("assigns a free BCM host, creates the BMH, and reaches Progressing once ready", func() {
			mock.addLiteNode(hostname, "gpu-node")
			createBCMBMI(bmiName, "gpu-node")
			reconciler := newBCMReconciler(mock)

			// Reconcile 1: inventory finalizer
			reconcileBCM(reconciler, bmiName, 1)
			Expect(getBCMBMI(bmiName).Finalizers).To(ContainElement(BareMetalInstanceInventoryFinalizer))

			// Reconcile 2: FindFreeHost → ExternalHostID
			reconcileBCM(reconciler, bmiName, 1)
			Expect(getBCMBMI(bmiName).Spec.ExternalHostID).To(Equal(bcmTestNS + "/" + hostname))

			// Reconcile 3: AssignHost → BCM write + BMH created, but not ready yet
			reconcileBCM(reconciler, bmiName, 1)
			Expect(bmhExistsInNS(hostname)).To(BeTrue())
			Expect(getBCMBMI(bmiName).Spec.HostClass).To(BeEmpty(), "HostClass not set until BMH ready")

			// BCM device now carries osac_instance_id
			device := mock.device(hostname)
			ev, ok := device["extra_values"].(map[string]any)
			Expect(ok).To(BeTrue(), "extra_values should be a map")
			Expect(ev).To(HaveKey("osac_instance_id"))

			// Operator-managed BMC secret was created with correct credentials
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: hostname + "-bmc-secret", Namespace: bcmTestNS}, secret)).To(Succeed())
			Expect(secret.Data).To(HaveKeyWithValue("username", []byte("root")))
			Expect(secret.Data).To(HaveKeyWithValue("password", []byte("calvin")))

			// Mark BMH ready → next reconcile sets HostClass and Progressing
			markBMHReady(hostname)
			reconcileBCM(reconciler, bmiName, 1)
			bmi := getBCMBMI(bmiName)
			Expect(bmi.Spec.HostClass).To(Equal(bcmHostClass))
			Expect(bmi.Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseProgressing))

			// BMH carries the consumerRef for this instance and BMC address from BCM
			bmh := &metal3api.BareMetalHost{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: hostname, Namespace: bcmTestNS}, bmh)).To(Succeed())
			Expect(bmh.Spec.ConsumerRef).NotTo(BeNil())
			Expect(bmh.Spec.ConsumerRef.Name).To(Equal(string(bmi.UID)))
			Expect(bmh.Spec.BMC.Address).To(Equal("ipmi://10.0.0.1"))
		})
	})

	Describe("BMH readiness delay", func() {
		const bmiName = "bcm-ready-bmi"
		const hostname = "node-ready"

		AfterEach(func() {
			cleanupBCMBMI(bmiName)
			cleanupBCMBMH(hostname)
		})

		It("keeps the instance in Allocating until the BMH becomes ready", func() {
			mock.addLiteNode(hostname, "gpu-node")
			createBCMBMI(bmiName, "gpu-node")
			reconciler := newBCMReconciler(mock)

			// finalizer + FindFreeHost + AssignHost(create BMH, not ready)
			reconcileBCM(reconciler, bmiName, 3)
			Expect(bmhExistsInNS(hostname)).To(BeTrue())

			// Poll while not ready: HostClass stays empty and reconcile requeues
			result := reconcileBCM(reconciler, bmiName, 1)
			Expect(getBCMBMI(bmiName).Spec.HostClass).To(BeEmpty())
			Expect(result.RequeueAfter).To(Equal(DefaultHostReadinessPollIntervalDuration))

			// Transition the BMH to ready → allocation completes
			markBMHReady(hostname)
			reconcileBCM(reconciler, bmiName, 1)
			Expect(getBCMBMI(bmiName).Spec.HostClass).To(Equal(bcmHostClass))
		})
	})

	Describe("Full deallocation flow", func() {
		const bmiName = "bcm-dealloc-bmi"
		const hostname = "node-dealloc"

		AfterEach(func() {
			cleanupBCMBMI(bmiName)
			cleanupBCMBMH(hostname)
		})

		It("clears BCM assignment, deletes the BMH and secret, and frees the host", func() {
			mock.addLiteNode(hostname, "gpu-node")
			createBCMBMI(bmiName, "gpu-node")
			reconciler := newBCMReconciler(mock)

			driveToAllocated(reconciler, bmiName, hostname)
			Expect(getBCMBMI(bmiName).Spec.HostClass).To(Equal(bcmHostClass))

			// Delete the BMI → inventory cleanup unassigns and deletes the BMH.
			Expect(k8sClient.Delete(ctx, getBCMBMI(bmiName))).To(Succeed())
			reconcileBCM(reconciler, bmiName, 3)

			// BCM assignment cleared
			device := mock.device(hostname)
			ev, ok := device["extra_values"].(map[string]any)
			Expect(ok).To(BeTrue(), "extra_values should be a map")
			Expect(ev).NotTo(HaveKey("osac_instance_id"))
			// BMH and operator secret deleted
			Expect(bmhExistsInNS(hostname)).To(BeFalse())
			err := k8sClient.Get(ctx, types.NamespacedName{Name: hostname + "-bmc-secret", Namespace: bcmTestNS}, &corev1.Secret{})
			Expect(client.IgnoreNotFound(err)).To(Succeed())
			Expect(err).To(HaveOccurred())

			// Host is free for re-assignment (no osac_instance_id)
			Expect(mock.device(hostname)).NotTo(BeNil())
		})
	})

	Describe("Missing BMC credentials in BCM", func() {
		const bmiName = "bcm-nocreds-bmi"
		const hostname = "node-nocreds"

		AfterEach(func() {
			cleanupBCMBMI(bmiName)
			cleanupBCMBMH(hostname)
		})

		It("surfaces an actionable error when no bmcSettings are configured", func() {
			// Free LiteNode with NO bmcSettings and no category/partition creds.
			mock.mu.Lock()
			mock.devices[hostname] = map[string]any{
				"baseType": "Device", "childType": "LiteNode", "uuid": "u", "hostname": hostname,
				"mac":          "aa:bb:cc:dd:ee:02",
				"extra_values": map[string]any{"resource_class": "gpu-node", "osac_bmc_address": "ipmi://10.0.0.2"},
			}
			mock.mu.Unlock()
			createBCMBMI(bmiName, "gpu-node")
			reconciler := newBCMReconciler(mock)

			// finalizer + FindFreeHost, then AssignHost errors on credential resolution
			reconcileBCM(reconciler, bmiName, 2)
			err := reconcileBCMExpectErr(reconciler, bmiName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no BMC credentials configured in BCM"))
		})
	})

	Describe("Assignment contention", func() {
		const bmiA = "bcm-contend-a"
		const bmiB = "bcm-contend-b"
		const hostname = "node-contend"

		AfterEach(func() {
			cleanupBCMBMI(bmiA)
			cleanupBCMBMI(bmiB)
			cleanupBCMBMH(hostname)
		})

		It("gives the only free host to one instance; the other finds none and retries", func() {
			mock.addLiteNode(hostname, "gpu-node") // exactly one free host
			createBCMBMI(bmiA, "gpu-node")
			createBCMBMI(bmiB, "gpu-node")
			reconciler := newBCMReconciler(mock)

			// A claims the host (finalizer, FindFreeHost, AssignHost).
			reconcileBCM(reconciler, bmiA, 3)
			Expect(getBCMBMI(bmiA).Spec.ExternalHostID).To(Equal(bcmTestNS + "/" + hostname))
			device := mock.device(hostname)
			ev, ok := device["extra_values"].(map[string]any)
			Expect(ok).To(BeTrue(), "extra_values should be a map")
			Expect(ev).To(HaveKey("osac_instance_id"))

			// B: finalizer, then FindFreeHost sees the host is taken → none free.
			reconcileBCM(reconciler, bmiB, 1)
			result := reconcileBCM(reconciler, bmiB, 1)
			Expect(getBCMBMI(bmiB).Spec.ExternalHostID).To(BeEmpty())
			Expect(getBCMBMI(bmiB).Status.Phase).To(Equal(v1alpha1.BareMetalInstancePhaseFailed))
			Expect(result.RequeueAfter).To(Equal(reconciler.NoFreeHostsPollIntervalDuration))
		})
	})

	Describe("BCM unreachable during allocation", func() {
		const bmiName = "bcm-down-alloc-bmi"
		const hostname = "node-down-alloc"

		AfterEach(func() {
			cleanupBCMBMI(bmiName)
			cleanupBCMBMH(hostname)
		})

		It("returns an error so the controller requeues, leaving the instance unallocated", func() {
			mock.addLiteNode(hostname, "gpu-node")
			createBCMBMI(bmiName, "gpu-node")
			reconciler := newBCMReconciler(mock)

			reconcileBCM(reconciler, bmiName, 1) // finalizer (no BCM call)
			mock.setDown(true)
			err := reconcileBCMExpectErr(reconciler, bmiName)
			Expect(err).To(HaveOccurred())
			Expect(getBCMBMI(bmiName).Spec.ExternalHostID).To(BeEmpty())
		})
	})

	Describe("BCM error response during allocation", func() {
		const bmiName = "bcm-err-alloc-bmi"
		const hostname = "node-err-alloc"

		AfterEach(func() {
			cleanupBCMBMI(bmiName)
			cleanupBCMBMH(hostname)
		})

		It("propagates the BCM error so the controller requeues", func() {
			mock.addLiteNode(hostname, "gpu-node")
			createBCMBMI(bmiName, "gpu-node")
			reconciler := newBCMReconciler(mock)

			reconcileBCM(reconciler, bmiName, 1) // finalizer
			mock.setFailCall("cmdevice.getDevices")
			err := reconcileBCMExpectErr(reconciler, bmiName)
			Expect(err).To(HaveOccurred())
			Expect(getBCMBMI(bmiName).Spec.ExternalHostID).To(BeEmpty())
		})
	})

	Describe("BCM unreachable during deallocation", func() {
		const bmiName = "bcm-down-dealloc-bmi"
		const hostname = "node-down-dealloc"

		AfterEach(func() {
			cleanupBCMBMI(bmiName)
			cleanupBCMBMH(hostname)
		})

		It("retries until BCM recovers, then completes cleanup", func() {
			mock.addLiteNode(hostname, "gpu-node")
			createBCMBMI(bmiName, "gpu-node")
			reconciler := newBCMReconciler(mock)

			driveToAllocated(reconciler, bmiName, hostname)
			Expect(k8sClient.Delete(ctx, getBCMBMI(bmiName))).To(Succeed())

			// BCM down → deallocation errors and retries; BMH is not deleted yet.
			mock.setDown(true)
			Expect(reconcileBCMExpectErr(reconciler, bmiName)).To(HaveOccurred())
			Expect(bmhExistsInNS(hostname)).To(BeTrue())

			// BCM recovers → cleanup completes.
			mock.setDown(false)
			reconcileBCM(reconciler, bmiName, 3)
			Expect(bmhExistsInNS(hostname)).To(BeFalse())
		})
	})

	Describe("BCM device removed during BMH readiness polling", func() {
		const bmiName = "bcm-orphan-bmi"
		const hostname = "node-orphan"

		AfterEach(func() {
			cleanupBCMBMI(bmiName)
			cleanupBCMBMH(hostname)
		})

		It("reports an orphan error when the BMH exists but the BCM device is gone", func() {
			mock.addLiteNode(hostname, "gpu-node")
			createBCMBMI(bmiName, "gpu-node")
			reconciler := newBCMReconciler(mock)

			// finalizer + FindFreeHost + AssignHost (BMH created, not yet ready)
			reconcileBCM(reconciler, bmiName, 3)
			Expect(bmhExistsInNS(hostname)).To(BeTrue())

			// The BCM device disappears while we're still polling for readiness.
			mock.removeDevice(hostname)
			err := reconcileBCMExpectErr(reconciler, bmiName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no longer exists in BCM inventory"))
		})
	})

	Describe("Deallocation with missing BCM device", func() {
		const bmiName = "bcm-dealloc-missing-bmi"
		const hostname = "node-dealloc-missing"

		AfterEach(func() {
			cleanupBCMBMI(bmiName)
			cleanupBCMBMH(hostname)
		})

		It("treats a missing BCM device as already cleaned up", func() {
			mock.addLiteNode(hostname, "gpu-node")
			createBCMBMI(bmiName, "gpu-node")
			reconciler := newBCMReconciler(mock)

			driveToAllocated(reconciler, bmiName, hostname)

			// Device removed from BCM before the instance is deleted.
			mock.removeDevice(hostname)
			Expect(k8sClient.Delete(ctx, getBCMBMI(bmiName))).To(Succeed())

			// Cleanup proceeds without error (missing device treated as done).
			reconcileBCM(reconciler, bmiName, 3)
			Expect(bmhExistsInNS(hostname)).To(BeFalse())
			err := k8sClient.Get(ctx, types.NamespacedName{Name: bmiName, Namespace: bcmTestNS}, &v1alpha1.BareMetalInstance{})
			Expect(client.IgnoreNotFound(err)).To(Succeed())
			Expect(err).To(HaveOccurred(), "BareMetalInstance should be garbage-collected after cleanup")
		})
	})
})
