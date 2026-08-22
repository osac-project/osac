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

package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/bcmclient"
)

type mockBCMAPI struct{}

func (m *mockBCMAPI) CertWatcher() *certwatcher.CertWatcher                    { return nil }
func (m *mockBCMAPI) GetDevices(_ context.Context) ([]bcmclient.Device, error) { return nil, nil }

func TestBCMInventoryAdapter(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "BCM Inventory Adapter Suite")
}

var _ = Describe("BCM Inventory Adapter", func() {
	Describe("ParseBCMOptions", func() {
		It("should return error when bcm key is missing from options", func() {
			_, err := ParseBCMOptions(map[string]any{})
			Expect(err).To(MatchError(ContainSubstring("bcm options not found in config")))
		})

		It("should return error when url is missing", func() {
			_, err := ParseBCMOptions(map[string]any{
				"bcm": map[string]any{
					"credentialsSecret": "osac-bcm-certs",
				},
			})
			Expect(err).To(MatchError(ContainSubstring("bcm url is required in config")))
		})

		It("should return error when credentialsSecret is missing", func() {
			_, err := ParseBCMOptions(map[string]any{
				"bcm": map[string]any{
					"url": "https://bcm-head:8081",
				},
			})
			Expect(err).To(MatchError(ContainSubstring("bcm credentialsSecret is required in config")))
		})

		It("should return error when bcm options are invalid", func() {
			_, err := ParseBCMOptions(map[string]any{
				"bcm": "not-a-map",
			})
			Expect(err).To(MatchError(ContainSubstring("failed to unmarshal bcm options")))
		})

		It("should parse valid options", func() {
			cfg, err := ParseBCMOptions(map[string]any{
				"bcm": map[string]any{
					"url":                "https://bcm-head:8081",
					"credentialsSecret":  "osac-bcm-certs",
					"insecureSkipVerify": true,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.URL).To(Equal("https://bcm-head:8081"))
			Expect(cfg.CredentialsSecret).To(Equal("osac-bcm-certs"))
			Expect(cfg.InsecureSkipVerify).To(BeTrue())
		})

		It("should reject credentialsSecret that escapes cert directory", func() {
			_, err := ParseBCMOptions(map[string]any{
				"bcm": map[string]any{
					"url":               "https://bcm-head:8081",
					"credentialsSecret": "../../etc/shadow",
				},
			})
			Expect(err).To(MatchError(ContainSubstring("resolves outside cert directory")))
		})
	})

	Describe("NewBCMClient", func() {
		It("should create a BCMClient with the provided dependencies", func() {
			client := NewBCMClient(&mockBCMAPI{}, nil, "bcm")
			Expect(client).NotTo(BeNil())
			Expect(client.hostClass).To(Equal("bcm"))
			Expect(client.bmhManager).To(BeNil())
		})
	})

	Describe("Stub methods", func() {
		var client *BCMClient

		BeforeEach(func() {
			client = NewBCMClient(&mockBCMAPI{}, nil, "bcm")
		})

		It("should return not-implemented error from AssignHost", func() {
			host, err := client.AssignHost(context.Background(), "ns/host1", "bmi-123", nil)
			Expect(err).To(MatchError(ContainSubstring("not implemented")))
			Expect(host).To(BeNil())
		})

		It("should return not-implemented error from UnassignHost", func() {
			err := client.UnassignHost(context.Background(), "ns/host1", nil)
			Expect(err).To(MatchError(ContainSubstring("not implemented")))
		})
	})

	Describe("FindFreeHost", func() {
		const bmhNamespace = "osac-baremetal"

		var (
			ctx        context.Context
			bmhMgr     *baremetalhost.Manager
			bcmDevices func(w http.ResponseWriter, r *http.Request)
		)

		newTestClient := func() *BCMClient {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Service string          `json:"service"`
					Call    string          `json:"call"`
					Args    json.RawMessage `json:"args"`
				}
				Expect(json.NewDecoder(r.Body).Decode(&req)).To(Succeed())
				Expect(req.Service).To(Equal("cmdevice"))
				Expect(req.Call).To(Equal("getDevices"))
				bcmDevices(w, r)
			}))
			DeferCleanup(server.Close)

			bcm := bcmclient.NewClientForTest(server.Client(), server.URL)
			return NewBCMClient(bcm, bmhMgr, "bcm")
		}

		BeforeEach(func() {
			ctx = context.Background()
			bmhMgr = baremetalhost.NewManager(nil, bmhNamespace)
		})

		It("should return a matching free LiteNode", func() {
			bcmDevices = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `[
					{"baseType":"Device","childType":"LiteNode","uuid":"u1","hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100"}}
				]`)
				Expect(err).NotTo(HaveOccurred())
			}

			client := newTestClient()
			host, err := client.FindFreeHost(ctx, map[string]string{"hostType": "h100"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).NotTo(BeNil())
			Expect(host.InventoryHostID).To(Equal("osac-baremetal/node001"))
			Expect(host.Name).To(Equal("node001"))
			Expect(host.HostType).To(Equal("h100"))
			Expect(host.HostClass).To(Equal("bcm"))
			Expect(host.ManagedBy).To(Equal("baremetal"))
		})

		It("should skip PhysicalNode devices", func() {
			bcmDevices = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `[
					{"baseType":"Device","childType":"PhysicalNode","uuid":"u1","hostname":"head01","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100"}}
				]`)
				Expect(err).NotTo(HaveOccurred())
			}

			client := newTestClient()
			host, err := client.FindFreeHost(ctx, map[string]string{"hostType": "h100"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil())
		})

		It("should skip devices with nil extra_values", func() {
			bcmDevices = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `[
					{"baseType":"Device","childType":"LiteNode","uuid":"u1","hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":null}
				]`)
				Expect(err).NotTo(HaveOccurred())
			}

			client := newTestClient()
			host, err := client.FindFreeHost(ctx, map[string]string{"hostType": "h100"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil())
		})

		It("should skip devices without resource_class", func() {
			bcmDevices = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `[
					{"baseType":"Device","childType":"LiteNode","uuid":"u1","hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"some_key":"value"}}
				]`)
				Expect(err).NotTo(HaveOccurred())
			}

			client := newTestClient()
			host, err := client.FindFreeHost(ctx, map[string]string{"hostType": "h100"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil())
		})

		It("should skip already-assigned hosts", func() {
			bcmDevices = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `[
					{"baseType":"Device","childType":"LiteNode","uuid":"u1","hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100","osac_instance_id":"some-uid"}}
				]`)
				Expect(err).NotTo(HaveOccurred())
			}

			client := newTestClient()
			host, err := client.FindFreeHost(ctx, map[string]string{"hostType": "h100"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil())
		})

		It("should filter by hostType from matchExpressions", func() {
			bcmDevices = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `[
					{"baseType":"Device","childType":"LiteNode","uuid":"u1","hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"a100"}},
					{"baseType":"Device","childType":"LiteNode","uuid":"u2","hostname":"node002","mac":"aa:bb:cc:dd:ee:02","extra_values":{"resource_class":"h100"}}
				]`)
				Expect(err).NotTo(HaveOccurred())
			}

			client := newTestClient()
			host, err := client.FindFreeHost(ctx, map[string]string{"hostType": "h100"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).NotTo(BeNil())
			Expect(host.Name).To(Equal("node002"))
			Expect(host.HostType).To(Equal("h100"))
		})

		It("should return nil when no hosts match", func() {
			bcmDevices = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `[]`)
				Expect(err).NotTo(HaveOccurred())
			}

			client := newTestClient()
			host, err := client.FindFreeHost(ctx, map[string]string{"hostType": "h100"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil())
		})

		It("should return any matching host when hostType is empty", func() {
			bcmDevices = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `[
					{"baseType":"Device","childType":"LiteNode","uuid":"u1","hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100"}}
				]`)
				Expect(err).NotTo(HaveOccurred())
			}

			client := newTestClient()
			host, err := client.FindFreeHost(ctx, map[string]string{})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).NotTo(BeNil())
			Expect(host.Name).To(Equal("node001"))
		})

		It("should skip devices with missing MAC address", func() {
			bcmDevices = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `[
					{"baseType":"Device","childType":"LiteNode","uuid":"u1","hostname":"node001","mac":"","extra_values":{"resource_class":"h100"}}
				]`)
				Expect(err).NotTo(HaveOccurred())
			}

			client := newTestClient()
			host, err := client.FindFreeHost(ctx, map[string]string{"hostType": "h100"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil())
		})

		It("should skip devices with malformed MAC address", func() {
			bcmDevices = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `[
					{"baseType":"Device","childType":"LiteNode","uuid":"u1","hostname":"node001","mac":"not-a-mac","extra_values":{"resource_class":"h100"}}
				]`)
				Expect(err).NotTo(HaveOccurred())
			}

			client := newTestClient()
			host, err := client.FindFreeHost(ctx, map[string]string{"hostType": "h100"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil())
		})

		It("should skip devices with zero MAC address", func() {
			bcmDevices = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `[
					{"baseType":"Device","childType":"LiteNode","uuid":"u1","hostname":"node001","mac":"00:00:00:00:00:00","extra_values":{"resource_class":"h100"}}
				]`)
				Expect(err).NotTo(HaveOccurred())
			}

			client := newTestClient()
			host, err := client.FindFreeHost(ctx, map[string]string{"hostType": "h100"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil())
		})

		It("should skip devices with invalid Kubernetes hostname", func() {
			bcmDevices = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `[
					{"baseType":"Device","childType":"LiteNode","uuid":"u1","hostname":"Node_001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100"}}
				]`)
				Expect(err).NotTo(HaveOccurred())
			}

			client := newTestClient()
			host, err := client.FindFreeHost(ctx, map[string]string{"hostType": "h100"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil())
		})

		It("should return nil when managedBy does not match default", func() {
			bcmDevices = func(_ http.ResponseWriter, _ *http.Request) {
				Fail("GetDevices should not be called when managedBy filter excludes BCM")
			}

			client := newTestClient()
			host, err := client.FindFreeHost(ctx, map[string]string{"managedBy": "other-manager"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil())
		})

		It("should match when managedBy is the default value", func() {
			bcmDevices = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, err := fmt.Fprint(w, `[
					{"baseType":"Device","childType":"LiteNode","uuid":"u1","hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100"}}
				]`)
				Expect(err).NotTo(HaveOccurred())
			}

			client := newTestClient()
			host, err := client.FindFreeHost(ctx, map[string]string{"managedBy": "baremetal"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).NotTo(BeNil())
			Expect(host.ManagedBy).To(Equal("baremetal"))
		})
	})

	Describe("GetHostNICs", func() {
		const bmhNamespace = "osac-baremetal"

		var ctx context.Context

		newClientWithNICs := func(bmhName string, macs ...string) *BCMClient {
			scheme := newTestScheme()
			nics := make([]metal3api.NIC, 0, len(macs))
			for _, mac := range macs {
				nics = append(nics, metal3api.NIC{MAC: mac})
			}
			bmh := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{Name: bmhName, Namespace: bmhNamespace},
			}
			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(bmh).
				WithStatusSubresource(&metal3api.BareMetalHost{}).
				Build()
			// Set status via status subresource
			bmh.Status.HardwareDetails = &metal3api.HardwareDetails{NIC: nics}
			Expect(k8sClient.Status().Update(context.Background(), bmh)).To(Succeed())
			mgr := baremetalhost.NewManager(k8sClient, bmhNamespace)
			return NewBCMClient(&mockBCMAPI{}, mgr, "bcm")
		}

		newClientNoBMH := func() *BCMClient {
			scheme := newTestScheme()
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			mgr := baremetalhost.NewManager(k8sClient, bmhNamespace)
			return NewBCMClient(&mockBCMAPI{}, mgr, "bcm")
		}

		BeforeEach(func() {
			ctx = context.Background()
		})

		It("returns lowercased MACs from BMH status.hardware.nics", func() {
			client := newClientWithNICs("node001", "AA:BB:CC:DD:EE:01", "FF:00:11:22:33:44")
			nics, err := client.GetHostNICs(ctx, "osac-baremetal/node001")
			Expect(err).NotTo(HaveOccurred())
			Expect(nics).To(HaveLen(2))
			Expect(nics[0].MAC).To(Equal("aa:bb:cc:dd:ee:01"))
			Expect(nics[1].MAC).To(Equal("ff:00:11:22:33:44"))
		})

		It("returns error when BMH does not exist", func() {
			client := newClientNoBMH()
			_, err := client.GetHostNICs(ctx, "osac-baremetal/nonexistent")
			Expect(err).To(HaveOccurred())
		})

		It("returns nil,nil when BMH has no hardware details", func() {
			scheme := newTestScheme()
			bmh := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{Name: "node001", Namespace: bmhNamespace},
			}
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(bmh).Build()
			mgr := baremetalhost.NewManager(k8sClient, bmhNamespace)
			client := NewBCMClient(&mockBCMAPI{}, mgr, "bcm")
			nics, err := client.GetHostNICs(ctx, "osac-baremetal/node001")
			Expect(err).NotTo(HaveOccurred())
			Expect(nics).To(BeNil())
		})

		It("returns nil,nil when BMH hardware.nics is empty", func() {
			client := newClientWithNICs("node001") // no MACs
			nics, err := client.GetHostNICs(ctx, "osac-baremetal/node001")
			Expect(err).NotTo(HaveOccurred())
			Expect(nics).To(BeNil())
		})

		It("returns error for invalid inventoryHostID format", func() {
			client := newClientNoBMH()
			_, err := client.GetHostNICs(ctx, "no-slash")
			Expect(err).To(HaveOccurred())
		})
	})
})
