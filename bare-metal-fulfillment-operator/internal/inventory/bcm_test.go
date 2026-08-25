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
	"go.uber.org/mock/gomock"

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/bcmclient"
)

func TestBCMInventoryAdapter(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "BCM Inventory Adapter Suite")
}

func makeDevice(jsonStr string) *bcmclient.Device {
	var d bcmclient.Device
	ExpectWithOffset(1, json.Unmarshal([]byte(jsonStr), &d)).To(Succeed())
	d.Raw = json.RawMessage(jsonStr)
	return &d
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
			ctrl := gomock.NewController(GinkgoT())
			mockAPI := NewMockBCMAPI(ctrl)
			client := NewBCMClient(mockAPI, nil, "bcm")
			Expect(client).NotTo(BeNil())
			Expect(client.hostClass).To(Equal("bcm"))
			Expect(client.bmhManager).To(BeNil())
		})
	})

	Describe("Stub methods", func() {
		It("should return not-implemented error from UnassignHost", func() {
			ctrl := gomock.NewController(GinkgoT())
			mockAPI := NewMockBCMAPI(ctrl)
			client := NewBCMClient(mockAPI, nil, "bcm")
			err := client.UnassignHost(context.Background(), "ns/host1", nil)
			Expect(err).To(MatchError(ContainSubstring("not implemented")))
		})
	})

	Describe("AssignHost", func() {
		const bmhNamespace = "osac-baremetal"

		var (
			ctrl    *gomock.Controller
			mockAPI *MockBCMAPI
			bmhMgr  *MockBMHLifecycleManager
		)

		BeforeEach(func() {
			ctrl = gomock.NewController(GinkgoT())
			mockAPI = NewMockBCMAPI(ctrl)
			bmhMgr = NewMockBMHLifecycleManager(ctrl)
			bmhMgr.EXPECT().Namespace().Return(bmhNamespace).AnyTimes()
		})

		It("should write osac_instance_id and return host on happy path", func(ctx context.Context) {
			device := makeDevice(`{"hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100"}}`)
			verifiedDevice := makeDevice(`{"hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100","osac_instance_id":"bmi-123"}}`)

			gomock.InOrder(
				mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(device, nil),
				mockAPI.EXPECT().UpdateDevice(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, raw json.RawMessage) (*bcmclient.UpdateResponse, error) {
						ev := extraValues(raw)
						Expect(ev).To(HaveKeyWithValue("osac_instance_id", "bmi-123"))
						Expect(ev).To(HaveKeyWithValue("resource_class", "h100"))
						return &bcmclient.UpdateResponse{Success: true}, nil
					}),
				mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(verifiedDevice, nil),
			)

			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, bmhNamespace+"/node001", "bmi-123", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(host).NotTo(BeNil())
			Expect(host.InventoryHostID).To(Equal("osac-baremetal/node001"))
			Expect(host.Name).To(Equal("node001"))
			Expect(host.HostType).To(Equal("h100"))
			Expect(host.HostClass).To(Equal("bcm"))
			Expect(host.ManagedBy).To(Equal("baremetal"))
		})

		It("should skip write when osac_instance_id already matches", func(ctx context.Context) {
			device := makeDevice(`{"hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100","osac_instance_id":"bmi-123"}}`)
			mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(device, nil)

			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, bmhNamespace+"/node001", "bmi-123", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(host).NotTo(BeNil())
			Expect(host.Name).To(Equal("node001"))
			Expect(host.HostClass).To(Equal("bcm"))
		})

		It("should return nil when host is taken by another instance", func(ctx context.Context) {
			device := makeDevice(`{"hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100","osac_instance_id":"other-instance"}}`)
			mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(device, nil)

			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, bmhNamespace+"/node001", "bmi-123", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil())
		})

		It("should return nil when device not found and no BMH exists", func(ctx context.Context) {
			mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(nil, nil)
			bmhMgr.EXPECT().IsBMHReady(gomock.Any(), "node001").Return(false,
				apierrors.NewNotFound(schema.GroupResource{Group: "metal3.io", Resource: "baremetalhosts"}, "node001"))

			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, bmhNamespace+"/node001", "bmi-123", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil())
		})

		It("should return error when device not found but BMH exists", func(ctx context.Context) {
			mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(nil, nil)
			bmhMgr.EXPECT().IsBMHReady(gomock.Any(), "node001").Return(true, nil)

			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, bmhNamespace+"/node001", "bmi-123", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no longer exists in BCM inventory but BareMetalHost CR exists"))
			Expect(host).To(BeNil())
		})

		It("should return nil when verify-after-write detects concurrent writer", func(ctx context.Context) {
			device := makeDevice(`{"hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100"}}`)
			raceDevice := makeDevice(`{"hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100","osac_instance_id":"other-writer"}}`)

			gomock.InOrder(
				mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(device, nil),
				mockAPI.EXPECT().UpdateDevice(gomock.Any(), gomock.Any()).Return(&bcmclient.UpdateResponse{Success: true}, nil),
				mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(raceDevice, nil),
			)

			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, bmhNamespace+"/node001", "bmi-123", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil())
		})

		It("should return nil when device disappears during verify-after-write", func(ctx context.Context) {
			device := makeDevice(`{"hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100"}}`)

			gomock.InOrder(
				mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(device, nil),
				mockAPI.EXPECT().UpdateDevice(gomock.Any(), gomock.Any()).Return(&bcmclient.UpdateResponse{Success: true}, nil),
				mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(nil, nil),
			)

			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, bmhNamespace+"/node001", "bmi-123", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil())
		})

		It("should preserve all extra_values fields when writing osac_instance_id", func(ctx context.Context) {
			device := makeDevice(`{"hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100","osac_bmc_address":"10.0.0.1","osac_bmc_credentials_secret":"secret1","custom_field":"val"}}`)
			verifiedDevice := makeDevice(`{"hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100","osac_bmc_address":"10.0.0.1","osac_bmc_credentials_secret":"secret1","custom_field":"val","osac_instance_id":"bmi-123"}}`)

			gomock.InOrder(
				mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(device, nil),
				mockAPI.EXPECT().UpdateDevice(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, raw json.RawMessage) (*bcmclient.UpdateResponse, error) {
						ev := extraValues(raw)
						Expect(ev).To(HaveKeyWithValue("resource_class", "h100"))
						Expect(ev).To(HaveKeyWithValue("osac_bmc_address", "10.0.0.1"))
						Expect(ev).To(HaveKeyWithValue("osac_bmc_credentials_secret", "secret1"))
						Expect(ev).To(HaveKeyWithValue("custom_field", "val"))
						Expect(ev).To(HaveKeyWithValue("osac_instance_id", "bmi-123"))
						return &bcmclient.UpdateResponse{Success: true}, nil
					}),
				mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(verifiedDevice, nil),
			)

			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, bmhNamespace+"/node001", "bmi-123", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(host).NotTo(BeNil())
		})

		It("should return error when GetDevice fails", func(ctx context.Context) {
			mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(nil, fmt.Errorf("connection refused"))

			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, bmhNamespace+"/node001", "bmi-123", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("AssignHost"))
			Expect(host).To(BeNil())
		})

		It("should return error when UpdateDevice fails", func(ctx context.Context) {
			device := makeDevice(`{"hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100"}}`)

			gomock.InOrder(
				mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(device, nil),
				mockAPI.EXPECT().UpdateDevice(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("update failed")),
			)

			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, bmhNamespace+"/node001", "bmi-123", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("AssignHost"))
			Expect(host).To(BeNil())
		})

		It("should return error when UpdateDevice returns validation failure", func(ctx context.Context) {
			device := makeDevice(`{"hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100"}}`)

			gomock.InOrder(
				mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(device, nil),
				mockAPI.EXPECT().UpdateDevice(gomock.Any(), gomock.Any()).Return(nil,
					fmt.Errorf("validation failed: INVALID: extra_values: invalid field")),
			)

			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, bmhNamespace+"/node001", "bmi-123", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("AssignHost"))
			Expect(host).To(BeNil())
		})

		It("should write assignment when device has nil extra_values", func(ctx context.Context) {
			device := makeDevice(`{"hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":null}`)
			verifiedDevice := makeDevice(`{"hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"osac_instance_id":"bmi-123"}}`)

			gomock.InOrder(
				mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(device, nil),
				mockAPI.EXPECT().UpdateDevice(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, raw json.RawMessage) (*bcmclient.UpdateResponse, error) {
						ev := extraValues(raw)
						Expect(ev).To(HaveKeyWithValue("osac_instance_id", "bmi-123"))
						return &bcmclient.UpdateResponse{Success: true}, nil
					}),
				mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(verifiedDevice, nil),
			)

			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, bmhNamespace+"/node001", "bmi-123", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(host).NotTo(BeNil())
			Expect(host.Name).To(Equal("node001"))
		})

		It("should return error for invalid host ID format", func(ctx context.Context) {
			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, "invalid-no-slash", "bmi-123", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("AssignHost"))
			Expect(err.Error()).To(ContainSubstring("invalid host ID"))
			Expect(host).To(BeNil())
		})

		It("should return error for empty host ID", func(ctx context.Context) {
			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, "", "bmi-123", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("AssignHost"))
			Expect(host).To(BeNil())
		})

		It("should return error for empty bareMetalInstanceID", func(ctx context.Context) {
			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, bmhNamespace+"/node001", "", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("bareMetalInstanceID is empty"))
			Expect(host).To(BeNil())
		})

		It("should return error when verify-after-write GetDevice fails", func(ctx context.Context) {
			device := makeDevice(`{"hostname":"node001","mac":"aa:bb:cc:dd:ee:01","extra_values":{"resource_class":"h100"}}`)

			gomock.InOrder(
				mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(device, nil),
				mockAPI.EXPECT().UpdateDevice(gomock.Any(), gomock.Any()).Return(&bcmclient.UpdateResponse{Success: true}, nil),
				mockAPI.EXPECT().GetDevice(gomock.Any(), "node001").Return(nil, fmt.Errorf("verify failed")),
			)

			client := NewBCMClient(mockAPI, bmhMgr, "bcm")
			host, err := client.AssignHost(ctx, bmhNamespace+"/node001", "bmi-123", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("verify-after-write"))
			Expect(host).To(BeNil())
		})
	})

	Describe("FindFreeHost", func() {
		var (
			ctx        context.Context
			bmhMgr     *MockBMHLifecycleManager
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
			ctrl := gomock.NewController(GinkgoT())
			bmhMgr = NewMockBMHLifecycleManager(ctrl)
			bmhMgr.EXPECT().Namespace().Return("osac-baremetal").AnyTimes()
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

		var (
			ctx     context.Context
			mockAPI *MockBCMAPI
		)

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
			return NewBCMClient(mockAPI, mgr, "bcm")
		}

		newClientNoBMH := func() *BCMClient {
			scheme := newTestScheme()
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			mgr := baremetalhost.NewManager(k8sClient, bmhNamespace)
			return NewBCMClient(mockAPI, mgr, "bcm")
		}

		BeforeEach(func() {
			ctx = context.Background()
			ctrl := gomock.NewController(GinkgoT())
			mockAPI = NewMockBCMAPI(ctrl)
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
			client := NewBCMClient(mockAPI, mgr, "bcm")
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

func extraValues(raw json.RawMessage) map[string]any {
	var obj map[string]any
	ExpectWithOffset(1, json.Unmarshal(raw, &obj)).To(Succeed())
	ev, _ := obj["extra_values"].(map[string]any)
	return ev
}
