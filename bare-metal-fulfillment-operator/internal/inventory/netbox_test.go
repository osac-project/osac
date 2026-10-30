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
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/netboxclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/secretapi"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

const testNetBoxHostClass = "metal3"

func testNetBoxCredentials() *BMCCredentials {
	return &BMCCredentials{
		Username: []byte("bmc-user"),
		Password: []byte("source-password"),
	}
}

var _ = Describe("NetBox inventory client", func() {
	Describe("ParseNetBoxOptions", func() {
		DescribeTable("rejects invalid options",
			func(options map[string]any, wantErr string) {
				_, err := ParseNetBoxOptions(options)
				Expect(err).To(MatchError(ContainSubstring(wantErr)))
			},
			Entry("missing options", map[string]any{}, "netbox options not found in config"),
			Entry("wrong value type", map[string]any{"netbox": "not-a-map"}, "failed to parse netbox options"),
			Entry("missing URL", map[string]any{"netbox": map[string]any{"tokenFile": "/var/run/secrets/netbox/token"}}, "netbox url is required in config"),
			Entry("missing token file", map[string]any{"netbox": map[string]any{"url": "https://netbox.example.test"}}, "netbox tokenFile is required in config"),
		)

		It("parses valid options", func() {
			options := map[string]any{"netbox": map[string]any{
				"url":               "https://netbox.example.test/",
				"tokenFile":         "/var/run/secrets/netbox/token",
				"caFile":            "/var/run/secrets/netbox/ca.crt",
				"allowInsecureHTTP": true,
			}}

			got, err := ParseNetBoxOptions(options)

			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(&NetBoxClientConfig{
				URL:               "https://netbox.example.test/",
				TokenFile:         "/var/run/secrets/netbox/token",
				CAFile:            "/var/run/secrets/netbox/ca.crt",
				AllowInsecureHTTP: true,
			}))
		})

		It("rejects values that cannot be marshaled", func() {
			_, err := ParseNetBoxOptions(map[string]any{"netbox": make(chan int)})

			Expect(err).To(MatchError(ContainSubstring("failed to parse netbox options")))
		})
	})

	Describe("NewNetBoxClient", func() {
		It("retains the injected API, manager, and host class", func() {
			tokenFile := filepath.Join(GinkgoT().TempDir(), "token")
			Expect(os.WriteFile(tokenFile, []byte("test-token\n"), 0o600)).To(Succeed())

			api, err := NewNetBoxAPI(&Config{Options: map[string]any{"netbox": map[string]any{
				"url":       "https://netbox.example.test/",
				"tokenFile": tokenFile,
			}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(api).NotTo(BeNil())

			manager := baremetalhost.NewManager(nil, nil, "metal3-system")
			credentialsReader := &fakeCredentialsReaderRepository{}
			client := NewNetBoxClient(api, manager, testNetBoxHostClass, credentialsReader)

			Expect(client.api).To(BeIdenticalTo(api))
			Expect(client.bmhManager).To(BeIdenticalTo(manager))
			Expect(client.bmhManager.Namespace()).To(Equal("metal3-system"))
			Expect(client.hostClass).To(Equal(testNetBoxHostClass))
			Expect(client.credentialsReader).To(BeIdenticalTo(credentialsReader))
		})
	})

	Describe("NewNetBoxAPI", func() {
		DescribeTable("rejects invalid configuration",
			func(config *Config, wantErr string) {
				_, err := NewNetBoxAPI(config)
				Expect(err).To(MatchError(ContainSubstring(wantErr)))
			},
			Entry("nil config", (*Config)(nil), "netbox inventory config is required"),
			Entry("missing NetBox options", &Config{}, "netbox options not found in config"),
			Entry("HTTP URL without opt-in", &Config{Options: map[string]any{"netbox": map[string]any{
				"url":       "http://netbox.example.test",
				"tokenFile": "/unused/token",
			}}}, "NetBox HTTP endpoints require AllowInsecureHTTP"),
		)

		It("rejects an unreadable token file", func() {
			tokenFile := filepath.Join(GinkgoT().TempDir(), "missing-token")
			config := &Config{Options: map[string]any{"netbox": map[string]any{
				"url":       "https://netbox.example.test",
				"tokenFile": tokenFile,
			}}}

			_, err := NewNetBoxAPI(config)

			Expect(err).To(MatchError(ContainSubstring("token file is missing or invalid")))
		})
	})

	Describe("UnassignHost", func() {
		It("deletes runtime resources before releasing the device with its ETag", func() {
			api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123")}
			manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			bmhDelete := manager.EXPECT().DeleteBMH(gomock.Any(), "netbox-46").Return(nil)
			bmhGone := manager.EXPECT().BMHExists(gomock.Any(), "netbox-46").Return(false, nil)
			secretDelete := manager.EXPECT().DeleteBMCSecret(gomock.Any(), "netbox-46-bmc-secret").Return(nil)
			gomock.InOrder(bmhDelete, bmhGone, secretDelete)
			client := NewNetBoxClient(api, manager, testNetBoxHostClass, nil)

			err := client.UnassignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(api.deviceReadIDs).To(Equal([]string{"46"}))
			Expect(api.patchCalls).To(Equal(1))
			Expect(api.patchETag).To(Equal(api.deviceSnapshot.ETag))
			Expect(*api.patch.Status).To(Equal(netBoxStatusStaged))
			Expect(api.patch.CustomFields).To(Equal(map[string]interface{}{"osac_instance_id": nil}))
		})

		It("leaves another BMI's claim and resources untouched on a stale retry", func() {
			api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-new")}
			manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			client := NewNetBoxClient(api, manager, testNetBoxHostClass, nil)

			err := client.UnassignHost(context.Background(), "metal3-system/netbox-46", "bmi-old", nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(api.patchCalls).To(BeZero())
		})

		DescribeTable("cleans resources without changing an unclaimed device", func(status string) {
			api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(status, "")}
			manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			manager.EXPECT().DeleteBMH(gomock.Any(), "netbox-46").Return(nil)
			manager.EXPECT().BMHExists(gomock.Any(), "netbox-46").Return(false, nil)
			manager.EXPECT().DeleteBMCSecret(gomock.Any(), "netbox-46-bmc-secret").Return(nil)
			client := NewNetBoxClient(api, manager, testNetBoxHostClass, nil)

			err := client.UnassignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(api.patchCalls).To(BeZero())
		},
			Entry("staged", netBoxStatusStaged),
			Entry("failed", "failed"),
		)

		It("does not clean resources when device status and owner disagree", func() {
			api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "")}
			manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			client := NewNetBoxClient(api, manager, testNetBoxHostClass, nil)

			err := client.UnassignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

			Expect(err).To(MatchError(ContainSubstring("inconsistent release state")))
			Expect(api.patchCalls).To(BeZero())
		})

		DescribeTable("leaves NetBox unchanged when cleanup fails", func(bmhErr, secretErr error) {
			api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123")}
			manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			bmhDelete := manager.EXPECT().DeleteBMH(gomock.Any(), "netbox-46").Return(bmhErr)
			if bmhErr == nil {
				bmhGone := manager.EXPECT().BMHExists(gomock.Any(), "netbox-46").Return(false, nil).After(bmhDelete)
				manager.EXPECT().DeleteBMCSecret(gomock.Any(), "netbox-46-bmc-secret").Return(secretErr).After(bmhGone)
			}
			client := NewNetBoxClient(api, manager, testNetBoxHostClass, nil)

			err := client.UnassignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

			Expect(err).To(HaveOccurred())
			Expect(api.patchCalls).To(BeZero())
		},
			Entry("BMH deletion", errors.New("BMH delete failed"), nil),
			Entry("generated Secret deletion", nil, errors.New("Secret delete failed")),
		)

		It("keeps NetBox claimed until the BMH and generated Secret finish deleting", func() {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			Expect(metal3api.AddToScheme(scheme)).To(Succeed())
			bmh := &metal3api.BareMetalHost{ObjectMeta: metav1.ObjectMeta{
				Name: "netbox-46", Namespace: "metal3-system",
				Finalizers: []string{"test.osac.openshift.io/hold"},
			}}
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(bmh).Build()
			manager := baremetalhost.NewManager(k8sClient, k8sClient, "metal3-system")
			secretKey := client.ObjectKey{Namespace: "metal3-system", Name: "netbox-46-bmc-secret"}
			Expect(manager.EnsureBMCSecret(ctx, secretKey.Name, "admin", "test-password")).To(Succeed())
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, secretKey, secret)).To(Succeed())
			secret.Finalizers = []string{"test.osac.openshift.io/hold"}
			Expect(k8sClient.Update(ctx, secret)).To(Succeed())
			api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123")}
			netBoxClient := NewNetBoxClient(api, manager, testNetBoxHostClass, nil)

			Expect(netBoxClient.UnassignHost(ctx, "metal3-system/netbox-46", "bmi-123", nil)).To(MatchError(errNetBoxBMHDeletionPending))
			Expect(api.patchCalls).To(BeZero())
			Expect(k8sClient.Get(ctx, secretKey, secret)).To(Succeed())
			Expect(secret.DeletionTimestamp).To(BeNil())

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bmh), bmh)).To(Succeed())
			bmh.Finalizers = nil
			Expect(k8sClient.Update(ctx, bmh)).To(Succeed())
			Expect(netBoxClient.UnassignHost(ctx, "metal3-system/netbox-46", "bmi-123", nil)).
				To(MatchError(ContainSubstring("Secret metal3-system/netbox-46-bmc-secret deletion is pending")))
			Expect(api.patchCalls).To(BeZero())
			Expect(k8sClient.Get(ctx, secretKey, secret)).To(Succeed())
			Expect(secret.DeletionTimestamp).NotTo(BeNil())

			secret.Finalizers = nil
			Expect(k8sClient.Update(ctx, secret)).To(Succeed())
			Expect(netBoxClient.UnassignHost(ctx, "metal3-system/netbox-46", "bmi-123", nil)).To(Succeed())
			Expect(api.patchCalls).To(Equal(1))
			Expect(*api.patch.Status).To(Equal(netBoxStatusStaged))
			Expect(api.patch.CustomFields).To(Equal(map[string]interface{}{"osac_instance_id": nil}))
		})

		It("keeps NetBox claimed when BMH deletion cannot be verified", func() {
			readErr := errors.New("BMH read failed")
			api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123")}
			manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			manager.EXPECT().DeleteBMH(gomock.Any(), "netbox-46").Return(nil)
			manager.EXPECT().BMHExists(gomock.Any(), "netbox-46").Return(false, readErr)
			client := NewNetBoxClient(api, manager, testNetBoxHostClass, nil)

			Expect(client.UnassignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)).To(MatchError(readErr))
			Expect(api.patchCalls).To(BeZero())
		})

		It("returns a 412 for the next reconciliation", func() {
			patchErr := &netboxclient.APIError{Kind: netboxclient.ErrorKindConflict, StatusCode: http.StatusPreconditionFailed}
			api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123"), patchErr: patchErr}
			manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			manager.EXPECT().DeleteBMH(gomock.Any(), "netbox-46").Return(nil)
			manager.EXPECT().BMHExists(gomock.Any(), "netbox-46").Return(false, nil)
			manager.EXPECT().DeleteBMCSecret(gomock.Any(), "netbox-46-bmc-secret").Return(nil)
			client := NewNetBoxClient(api, manager, testNetBoxHostClass, nil)

			err := client.UnassignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

			Expect(errors.Is(err, patchErr)).To(BeTrue())
			Expect(api.deviceReadCalls).To(Equal(1))
			Expect(api.patchCalls).To(Equal(1))
		})
	})
})

type selectorQueryTestAPI struct {
	deviceQueries    int
	deviceQuery      netboxclient.DeviceQuery
	devices          []netboxclient.Device
	deviceErr        error
	deviceVisits     int
	customFieldReads int
	deviceSnapshot   netboxclient.DeviceSnapshot
	deviceSnapshots  []netboxclient.DeviceSnapshot
	deviceReadCalls  int
	deviceReadIDs    []string
	deviceGetError   error
	patchCalls       int
	patchDeviceID    string
	patch            netboxclient.DevicePatch
	patchETag        string
	patchSnapshot    netboxclient.DeviceSnapshot
	patchErr         error
	interfaces       []netboxclient.DeviceInterface
	interfaceErr     error
	interfaceDevice  string
	deviceReadErr    error
}

func (a *selectorQueryTestAPI) ForEachDevice(
	_ context.Context,
	query netboxclient.DeviceQuery,
	visit func(netboxclient.Device) (bool, error),
) error {
	a.deviceQueries++
	a.deviceQuery = maps.Clone(query)
	if a.deviceErr != nil {
		return a.deviceErr
	}
	for _, device := range a.devices {
		a.deviceVisits++
		keepGoing, err := visit(device)
		if err != nil {
			return err
		}
		if !keepGoing {
			return nil
		}
	}
	return nil
}

func (a *selectorQueryTestAPI) ForEachDeviceCustomField(
	context.Context,
	func(netboxclient.CustomField) (bool, error),
) error {
	a.customFieldReads++
	return errors.New("unexpected custom-field metadata lookup")
}

func (a *selectorQueryTestAPI) ListDeviceInterfaces(_ context.Context, deviceID string) ([]netboxclient.DeviceInterface, error) {
	a.interfaceDevice = deviceID
	return a.interfaces, a.interfaceErr
}

func (a *selectorQueryTestAPI) GetDevice(_ context.Context, id string) (netboxclient.DeviceSnapshot, error) {
	a.deviceReadCalls++
	a.deviceReadIDs = append(a.deviceReadIDs, id)
	if a.deviceGetError != nil {
		return netboxclient.DeviceSnapshot{}, a.deviceGetError
	}
	if index := a.deviceReadCalls - 1; index < len(a.deviceSnapshots) {
		return a.deviceSnapshots[index], nil
	}
	return a.deviceSnapshot, a.deviceReadErr
}

func (a *selectorQueryTestAPI) PatchDevice(
	_ context.Context,
	id string,
	patch netboxclient.DevicePatch,
	etag string,
) (netboxclient.DeviceSnapshot, error) {
	a.patchCalls++
	a.patchDeviceID = id
	a.patch = patch
	a.patchETag = etag
	return a.patchSnapshot, a.patchErr
}

type fakeCredentialsReaderRepository struct {
	credentials *BMCCredentials
	err         error
	calls       int
	hostID      string
	onResolve   func()
}

func (r *fakeCredentialsReaderRepository) GetCredentials(_ context.Context, hostID string) (*BMCCredentials, error) {
	r.calls++
	r.hostID = hostID
	if r.onResolve != nil {
		r.onResolve()
	}
	return r.credentials, r.err
}

func (*fakeCredentialsReaderRepository) Close() error {
	return nil
}

func newTestNetBoxClient(api NetBoxAPI, resolver CredentialsReaderRepository) *NetBoxClient {
	ctrl := gomock.NewController(GinkgoT())
	manager := baremetalhost.NewMockBMHLifecycleManager(ctrl)
	manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
	manager.EXPECT().EnsureBMCSecret(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	manager.EXPECT().CreateBMH(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	manager.EXPECT().IsBMHReady(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	return newTestNetBoxClientWithManager(api, resolver, manager)
}

func newTestNetBoxClientWithManager(api NetBoxAPI, resolver CredentialsReaderRepository, manager baremetalhost.BMHLifecycleManager) *NetBoxClient {
	return NewNetBoxClient(api, manager, testNetBoxHostClass, resolver)
}

func newTestNetBoxClientWithCredentials(api NetBoxAPI) *NetBoxClient {
	return newTestNetBoxClient(api, &fakeCredentialsReaderRepository{
		credentials: &BMCCredentials{
			Username: []byte("user"),
			Password: []byte("pass"),
		},
	})
}

var _ = Describe("NetBox selector query", func() {
	Describe("FindFreeHost", func() {
		It("returns an error when the Metal3 namespace is missing without querying NetBox", func() {
			api := &selectorQueryTestAPI{}
			client := NewNetBoxClient(api, baremetalhost.NewManager(nil, nil, ""), testNetBoxHostClass, nil)

			host, err := client.FindFreeHost(context.Background(), nil)

			Expect(host).To(BeNil())
			Expect(err).To(MatchError("NetBox inventory requires a Metal3 namespace"))
			Expect(api.deviceQueries).To(BeZero())
		})

		It("queries with required free-host filters and selector fields", func() {
			api := &selectorQueryTestAPI{
				devices: []netboxclient.Device{
					{ID: "46", Name: "netbox-device-46"},
					{ID: "47", Name: "netbox-device-47"},
				},
			}
			client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass, nil)
			selector := map[string]string{
				"cpu_cores":      "9007199254740993",
				"accelerated":    "true",
				"placement_zone": "Lab A",
				"accelerator":    "a100",
			}

			host, err := client.FindFreeHost(context.Background(), selector)

			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(Equal(&Host{
				InventoryHostID: "metal3-system/netbox-46",
				Name:            "netbox-46",
				HostClass:       testNetBoxHostClass,
				ProvisionState:  shared.OsacDefaultProvisionStateValue,
				ManagedBy:       shared.OsacDefaultManagedByValue,
				Ready:           true,
			}))
			Expect(api.customFieldReads).To(BeZero())
			Expect(api.deviceQueries).To(Equal(1))
			Expect(api.deviceVisits).To(Equal(1))
			Expect(api.deviceQuery).To(Equal(netboxclient.DeviceQuery{
				"status":                     "staged",
				"cf_osac_managed":            "true",
				"cf_osac_instance_id__empty": "true",
				"cf_cpu_cores":               "9007199254740993",
				"cf_accelerated":             "true",
				"cf_placement_zone":          "Lab A",
				"cf_accelerator":             "a100",
			}))
		})

		It("keeps the required managed filter when the selector requests another value", func() {
			api := &selectorQueryTestAPI{}
			client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass, nil)

			_, err := client.FindFreeHost(context.Background(), map[string]string{"osac_managed": "false"})

			Expect(err).NotTo(HaveOccurred())
			Expect(api.deviceQueries).To(Equal(1))
			Expect(api.deviceQuery["cf_osac_managed"]).To(Equal("true"))
		})

		It("queries the fixed free-host filters when the selector is empty", func() {
			api := &selectorQueryTestAPI{}
			client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass, nil)

			host, err := client.FindFreeHost(context.Background(), nil)

			Expect(host).To(BeNil())
			Expect(err).NotTo(HaveOccurred())
			Expect(api.customFieldReads).To(BeZero())
			Expect(api.deviceQueries).To(Equal(1))
			Expect(api.deviceQuery).To(Equal(netboxclient.DeviceQuery{
				"status":                     "staged",
				"cf_osac_managed":            "true",
				"cf_osac_instance_id__empty": "true",
			}))
		})

		It("returns device-list errors", func() {
			apiError := errors.New("device endpoint unavailable")
			api := &selectorQueryTestAPI{deviceErr: apiError}
			client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass, nil)

			host, err := client.FindFreeHost(context.Background(), map[string]string{"placement_zone": "west"})

			Expect(host).To(BeNil())
			Expect(err).To(MatchError(ContainSubstring("failed to search NetBox for a free host")))
			Expect(errors.Is(err, apiError)).To(BeTrue())
			Expect(api.customFieldReads).To(BeZero())
			Expect(api.deviceQueries).To(Equal(1))
		})
	})

	Describe("Capability selector filters", func() {
		DescribeTable("prefixes selector fields as NetBox custom-field filters",
			func(key string) {
				query := buildFreeHostQuery(map[string]string{key: "a100"})

				Expect(query["cf_"+key]).To(Equal("a100"))
			},
			Entry("hyphenated name", "gpu-model"),
			Entry("name longer than 50 characters", strings.Repeat("x", 51)),
			Entry("name beginning with the query prefix", "cf_gpu_cores"),
		)

		DescribeTable("passes selector values unchanged",
			func(value string) {
				query := buildFreeHostQuery(map[string]string{"capability": value})

				Expect(query["cf_capability"]).To(Equal(value))
			},
			Entry("integer value", "16"),
			Entry("integer formatting", "016"),
			Entry("Boolean value", "false"),
			Entry("Boolean casing", "TRUE"),
			Entry("empty text", ""),
			Entry("text null string", "null"),
			Entry("text whitespace", " a100 "),
			Entry("select stored value", "a100"),
		)

	})
})

var _ = Describe("NetBox assignment eligibility", func() {
	It("trusts the managed filter used when the device was selected", func() {
		freeDevice := testNetBoxAssignmentSnapshot(netBoxStatusStaged, "")
		freeDevice.Device.CustomFields["osac_managed"] = false
		claimedDevice := testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123")
		claimedDevice.Device.CustomFields["osac_managed"] = false
		api := &selectorQueryTestAPI{deviceSnapshot: freeDevice, patchSnapshot: claimedDevice}
		client := newTestNetBoxClient(api, &fakeCredentialsReaderRepository{credentials: testNetBoxCredentials()})

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(host).To(Equal(expectedTestNetBoxAssignedHost("bmi-123")))
		Expect(api.patchCalls).To(Equal(1))
	})

	It("does not claim a device that became active without an owner", func() {
		api := &selectorQueryTestAPI{
			deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, ""),
		}
		client := newTestNetBoxClientWithCredentials(api)

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(host).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("active without an assigned instance")))
		Expect(api.patchCalls).To(BeZero())
	})

	It("claims a free managed device with its ETag and confirms the assignment", func() {
		freeDevice := testNetBoxAssignmentSnapshot(netBoxStatusStaged, "")
		api := &selectorQueryTestAPI{
			deviceSnapshot: freeDevice,
			patchSnapshot:  testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123"),
		}
		resolver := &fakeCredentialsReaderRepository{credentials: testNetBoxCredentials()}
		resolver.onResolve = func() { Expect(api.patchCalls).To(BeZero()) }
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
		secretEnsured := false
		bmhCreated := false
		ensureCall := manager.EXPECT().EnsureBMCSecret(gomock.Any(), "netbox-46-bmc-secret", "bmc-user", "source-password").DoAndReturn(
			func(_ context.Context, name, username, password string) error {
				Expect(api.patchCalls).To(Equal(1))
				Expect(name).To(Equal("netbox-46-bmc-secret"))
				Expect(username).To(Equal("bmc-user"))
				Expect(password).To(Equal("source-password"))
				secretEnsured = true
				return nil
			})
		createCall := manager.EXPECT().CreateBMH(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params baremetalhost.CreateParams) error {
				Expect(secretEnsured).To(BeTrue())
				Expect(params.Name).To(Equal("netbox-46"))
				Expect(params.BMCAddress).To(Equal("redfish-virtualmedia+https://192.0.2.46/redfish/v1/Systems/1"))
				Expect(params.CredentialsSecret).To(Equal("netbox-46-bmc-secret"))
				Expect(params.BootMACAddress).To(Equal("aa:bb:cc:dd:ee:46"))
				Expect(params.ConsumerRef).NotTo(BeNil())
				Expect(params.ConsumerRef.APIVersion).To(Equal("osac.openshift.io/v1alpha1"))
				Expect(params.ConsumerRef.Kind).To(Equal("BareMetalInstance"))
				Expect(params.ConsumerRef.Name).To(Equal("bmi-123"))
				Expect(params.ConsumerRef.Namespace).To(BeEmpty())
				Expect(params.Labels).To(Equal(map[string]string{Metal3ManagedByLabel: shared.OsacDefaultManagedByValue}))
				bmhCreated = true
				return nil
			})
		readinessCall := manager.EXPECT().IsBMHReady(gomock.Any(), "netbox-46").DoAndReturn(
			func(context.Context, string) (bool, error) {
				Expect(bmhCreated).To(BeTrue())
				return true, nil
			})
		gomock.InOrder(ensureCall, createCall, readinessCall)
		client := newTestNetBoxClientWithManager(api, resolver, manager)

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(host).To(Equal(expectedTestNetBoxAssignedHost("bmi-123")))
		Expect(api.deviceReadIDs).To(Equal([]string{"46"}))
		Expect(api.patchCalls).To(Equal(1))
		Expect(api.patchDeviceID).To(Equal("46"))
		Expect(api.patchETag).To(Equal(freeDevice.ETag))
		Expect(api.patch.Status).NotTo(BeNil())
		Expect(*api.patch.Status).To(Equal(netBoxStatusActive))
		Expect(api.patch.CustomFields).To(Equal(map[string]interface{}{"osac_instance_id": "bmi-123"}))
		Expect(api.patch.CustomFields).NotTo(HaveKey("username"))
		Expect(api.patch.CustomFields).NotTo(HaveKey("password"))
		Expect(resolver.calls).To(Equal(1))
		Expect(host.Ready).To(BeTrue())
	})

	It("returns an existing assignment without patching the device", func() {
		api := &selectorQueryTestAPI{
			deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123"),
		}
		client := newTestNetBoxClientWithCredentials(api)

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(host).To(Equal(expectedTestNetBoxAssignedHost("bmi-123")))
		Expect(api.patchCalls).To(BeZero())
	})

	It("propagates the client error for a free device without an ETag", func() {
		freeDevice := testNetBoxAssignmentSnapshot(netBoxStatusStaged, "")
		freeDevice.ETag = ""
		api := &selectorQueryTestAPI{
			deviceSnapshot: freeDevice,
			patchErr:       &netboxclient.APIError{Kind: netboxclient.ErrorKindInvalidETag},
		}
		client := newTestNetBoxClientWithCredentials(api)

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(host).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("NetBox device ETag is missing or invalid")))
		Expect(api.patchCalls).To(Equal(1))
		Expect(api.patchETag).To(BeEmpty())
	})

	It("returns non-conflict PATCH errors", func() {
		patchErr := errors.New("NetBox unavailable")
		api := &selectorQueryTestAPI{
			deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusStaged, ""),
			patchErr:       patchErr,
		}
		client := newTestNetBoxClientWithCredentials(api)

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(host).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("failed to claim NetBox device")))
		Expect(errors.Is(err, patchErr)).To(BeTrue())
		Expect(api.patchCalls).To(Equal(1))
	})

	It("returns an error when a successful PATCH does not confirm the assignment", func() {
		freeDevice := testNetBoxAssignmentSnapshot(netBoxStatusStaged, "")
		api := &selectorQueryTestAPI{
			deviceSnapshot: freeDevice,
			patchSnapshot:  freeDevice,
		}
		client := newTestNetBoxClientWithCredentials(api)

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(host).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("did not confirm the host assignment")))
		Expect(api.patchCalls).To(Equal(1))
	})

	DescribeTable("reconciles a claim after a 412 response",
		func(latest netboxclient.DeviceSnapshot, wantHost bool, wantErr string) {
			api := &selectorQueryTestAPI{
				deviceSnapshots: []netboxclient.DeviceSnapshot{
					testNetBoxAssignmentSnapshot(netBoxStatusStaged, ""),
					latest,
				},
				patchErr: &netboxclient.APIError{
					Kind:       netboxclient.ErrorKindConflict,
					StatusCode: http.StatusPreconditionFailed,
				},
			}
			client := newTestNetBoxClientWithCredentials(api)

			host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

			if wantHost {
				Expect(host).To(Equal(expectedTestNetBoxAssignedHost("bmi-123")))
			} else {
				Expect(host).To(BeNil())
			}
			if wantErr != "" {
				Expect(err).To(MatchError(ContainSubstring(wantErr)))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(api.patchCalls).To(Equal(1))
			Expect(api.deviceReadIDs).To(Equal([]string{"46", "46"}))
		},
		Entry("now owned by this instance", testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123"), true, ""),
		Entry("now owned by another instance", testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-456"), false, ""),
		Entry("still free", testNetBoxAssignmentSnapshot(netBoxStatusStaged, ""), false, "changed during assignment"),
	)
})

var _ = Describe("NetBox Metal3 preparation", func() {
	It("returns not ready without an error when Metal3 readiness is false", func() {
		api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123")}
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
		ensureCall := manager.EXPECT().EnsureBMCSecret(gomock.Any(), "netbox-46-bmc-secret", "bmc-user", "source-password").Return(nil)
		createCall := manager.EXPECT().CreateBMH(gomock.Any(), gomock.Any()).Return(nil)
		readinessCall := manager.EXPECT().IsBMHReady(gomock.Any(), "netbox-46").Return(false, nil)
		gomock.InOrder(ensureCall, createCall, readinessCall)
		client := newTestNetBoxClientWithManager(api, &fakeCredentialsReaderRepository{credentials: testNetBoxCredentials()}, manager)

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(host).NotTo(BeNil())
		Expect(host.Ready).To(BeFalse())
		Expect(api.patchCalls).To(BeZero())
	})

	DescribeTable("returns preparation failures without compensating the confirmed NetBox claim",
		func(failureStage, wantError string) {
			api := &selectorQueryTestAPI{
				deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusStaged, ""),
				patchSnapshot:  testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123"),
			}
			manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			var orderedCalls []*gomock.Call
			secretCall := manager.EXPECT().EnsureBMCSecret(gomock.Any(), "netbox-46-bmc-secret", "bmc-user", "source-password")
			if failureStage == "secret" {
				secretCall.Return(errors.New("secret operation failed"))
				orderedCalls = append(orderedCalls, secretCall)
			} else {
				secretCall.Return(nil)
				orderedCalls = append(orderedCalls, secretCall)
				createCall := manager.EXPECT().CreateBMH(gomock.Any(), gomock.Any())
				if failureStage == "bmh" {
					createCall.Return(errors.New("BMH operation failed"))
					orderedCalls = append(orderedCalls, createCall)
				} else {
					createCall.Return(nil)
					orderedCalls = append(orderedCalls, createCall)
					readinessCall := manager.EXPECT().IsBMHReady(gomock.Any(), "netbox-46").Return(false, errors.New("readiness operation failed"))
					orderedCalls = append(orderedCalls, readinessCall)
				}
			}
			orderedCallArgs := make([]any, len(orderedCalls))
			for i, call := range orderedCalls {
				orderedCallArgs[i] = call
			}
			gomock.InOrder(orderedCallArgs...)
			resolver := &fakeCredentialsReaderRepository{credentials: testNetBoxCredentials()}
			client := newTestNetBoxClientWithManager(api, resolver, manager)

			host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

			Expect(host).To(BeNil())
			Expect(err).To(MatchError(ContainSubstring(wantError)))
			Expect(err.Error()).NotTo(ContainSubstring("bmc-user"))
			Expect(err.Error()).NotTo(ContainSubstring("source-password"))
			Expect(resolver.calls).To(Equal(1))
			Expect(api.patchCalls).To(Equal(1))
			Expect(api.patch.CustomFields).To(Equal(map[string]interface{}{"osac_instance_id": "bmi-123"}))
		},
		Entry("Secret failure", "secret", "failed to ensure BMC credentials Secret for NetBox host"),
		Entry("BMH failure", "bmh", "failed to create BareMetalHost for NetBox host"),
		Entry("readiness failure", "readiness", "failed to check BareMetalHost readiness for NetBox host"),
	)

	It("retries preparation for the persisted same-owner allocation without another claim PATCH", func() {
		api := &selectorQueryTestAPI{
			deviceSnapshots: []netboxclient.DeviceSnapshot{
				testNetBoxAssignmentSnapshot(netBoxStatusStaged, ""),
				testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123"),
			},
			patchSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123"),
		}
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
		var ensureCalls int
		ensureCall := manager.EXPECT().EnsureBMCSecret(gomock.Any(), "netbox-46-bmc-secret", "bmc-user", "source-password").DoAndReturn(
			func(context.Context, string, string, string) error {
				ensureCalls++
				if ensureCalls == 1 {
					return errors.New("temporary Secret operation failure")
				}
				return nil
			}).Times(2)
		createCall := manager.EXPECT().CreateBMH(gomock.Any(), gomock.Any()).Return(nil)
		readinessCall := manager.EXPECT().IsBMHReady(gomock.Any(), "netbox-46").Return(true, nil)
		gomock.InOrder(ensureCall, createCall, readinessCall)
		resolver := &fakeCredentialsReaderRepository{credentials: testNetBoxCredentials()}
		client := newTestNetBoxClientWithManager(api, resolver, manager)

		firstHost, firstErr := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)
		secondHost, secondErr := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(firstHost).To(BeNil())
		Expect(firstErr).To(MatchError(ContainSubstring("failed to ensure BMC credentials Secret for NetBox host")))
		Expect(secondErr).NotTo(HaveOccurred())
		Expect(secondHost).To(Equal(expectedTestNetBoxAssignedHost("bmi-123")))
		Expect(resolver.calls).To(Equal(2))
		Expect(api.deviceReadIDs).To(Equal([]string{"46", "46"}))
		Expect(api.patchCalls).To(Equal(1))
	})
})

func testNetBoxAssignmentSnapshot(status, instanceID string) netboxclient.DeviceSnapshot {
	return netboxclient.DeviceSnapshot{
		Device: netboxclient.Device{
			ID:     "46",
			Status: status,
			CustomFields: map[string]interface{}{
				"osac_managed":        true,
				"osac_instance_id":    instanceID,
				netBoxBMCAddressField: "redfish-virtualmedia+https://192.0.2.46/redfish/v1/Systems/1",
				netBoxBootMACField:    "aa:bb:cc:dd:ee:46",
			},
		},
		ETag: `W/"device-revision-1"`,
	}
}

func expectedTestNetBoxAssignedHost(instanceID string) *Host {
	return &Host{
		InventoryHostID:     "metal3-system/netbox-46",
		Name:                "netbox-46",
		HostClass:           testNetBoxHostClass,
		ProvisionState:      shared.OsacDefaultProvisionStateValue,
		ManagedBy:           shared.OsacDefaultManagedByValue,
		Ready:               true,
		BareMetalInstanceID: instanceID,
	}
}

func testNetBoxSelectorBMHManager() *baremetalhost.Manager {
	return baremetalhost.NewManager(nil, nil, "metal3-system")
}

var _ = Describe("NetBox GetHostNICs", func() {
	It("reads inspected NICs from the BMH manager without querying NetBox", func() {
		ctx := context.Background()
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("baremetal").Times(2)
		manager.EXPECT().IsBMHReady(ctx, "netbox-42").Return(true, nil)
		manager.EXPECT().GetHardwareNICs(ctx, "netbox-42").Return([]string{"AA:BB:CC:DD:EE:01"}, nil)
		api := &selectorQueryTestAPI{}
		got, err := NewNetBoxClient(api, manager, testNetBoxHostClass, nil).GetHostNICs(ctx, "baremetal/netbox-42")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]HostNIC{{MAC: "aa:bb:cc:dd:ee:01"}}))
		Expect(api.deviceReadCalls).To(BeZero())
	})

	It("does not read NICs before the BMH is ready", func() {
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("baremetal").Times(2)
		manager.EXPECT().IsBMHReady(gomock.Any(), "netbox-42").Return(false, nil)
		got, err := NewNetBoxClient(&selectorQueryTestAPI{}, manager, testNetBoxHostClass, nil).
			GetHostNICs(context.Background(), "baremetal/netbox-42")
		Expect(err).To(MatchError(ContainSubstring("not ready for NIC metadata")))
		Expect(got).To(BeNil())
	})

	It("returns an empty NIC list after a ready BMH confirms no NICs", func() {
		ctx := context.Background()
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("baremetal").Times(2)
		manager.EXPECT().IsBMHReady(ctx, "netbox-42").Return(true, nil)
		manager.EXPECT().GetHardwareNICs(ctx, "netbox-42").Return(nil, nil)

		got, err := NewNetBoxClient(&selectorQueryTestAPI{}, manager, testNetBoxHostClass, nil).
			GetHostNICs(ctx, "baremetal/netbox-42")

		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeNil())
		Expect(got).To(BeEmpty())
	})

	It("propagates BMH errors without querying NetBox", func() {
		lookupErr := errors.New("BMH lookup failed")
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("baremetal").Times(2)
		manager.EXPECT().IsBMHReady(gomock.Any(), "netbox-42").Return(true, nil)
		manager.EXPECT().GetHardwareNICs(gomock.Any(), "netbox-42").Return(nil, lookupErr)
		api := &selectorQueryTestAPI{}
		got, err := NewNetBoxClient(api, manager, testNetBoxHostClass, nil).
			GetHostNICs(context.Background(), "baremetal/netbox-42")
		Expect(errors.Is(err, lookupErr)).To(BeTrue())
		Expect(got).To(BeNil())
		Expect(api.deviceReadCalls).To(BeZero())
	})
})

var _ = Describe("NetBox GetHostLogicalPortMACs", func() {
	It("prefers custom values for matching labels and retains aliases", func() {
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("baremetal").Times(2)
		api := &selectorQueryTestAPI{
			interfaces: []netboxclient.DeviceInterface{
				{Name: "eno1", PrimaryMAC: "AA:BB:CC:DD:EE:01"},
				{Name: "data-1", PrimaryMAC: "52:54:00:16:04:84"},
				{Name: "eno2", PrimaryMAC: "52:54:00:16:04:85"},
			},
			deviceSnapshot: netboxclient.DeviceSnapshot{Device: netboxclient.Device{
				ID: "42",
				CustomFields: map[string]interface{}{
					"osac_interface_macs": map[string]interface{}{
						"data-0": "aa:bb:cc:dd:ee:01",
						"data-1": "52:54:00:16:04:86",
						"data-2": "52:54:00:16:04:87",
					},
				},
			}},
		}
		got, err := NewNetBoxClient(api, manager, testNetBoxHostClass, nil).
			GetHostLogicalPortMACs(context.Background(), "baremetal/netbox-42")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(map[string]string{
			"data-0": "aa:bb:cc:dd:ee:01",
			"data-1": "52:54:00:16:04:86",
			"data-2": "52:54:00:16:04:87",
			"eno1":   "AA:BB:CC:DD:EE:01",
			"eno2":   "52:54:00:16:04:85",
		}))
		Expect(api.interfaceDevice).To(Equal("42"))
		Expect(api.deviceReadIDs).To(Equal([]string{"42"}))
	})

	It("returns custom-field lookup errors", func() {
		lookupErr := errors.New("custom-field lookup failed")
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("baremetal").Times(2)
		api := &selectorQueryTestAPI{
			interfaces:    []netboxclient.DeviceInterface{{Name: "data-0", PrimaryMAC: "52:54:00:16:04:83"}},
			deviceReadErr: lookupErr,
		}
		got, err := NewNetBoxClient(api, manager, testNetBoxHostClass, nil).
			GetHostLogicalPortMACs(context.Background(), "baremetal/netbox-42")
		Expect(errors.Is(err, lookupErr)).To(BeTrue())
		Expect(got).To(BeNil())
	})

	It("uses the custom field when native interfaces have no primary MAC", func() {
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("baremetal").Times(2)
		api := &selectorQueryTestAPI{
			interfaces: []netboxclient.DeviceInterface{{Name: "data-0"}},
			deviceSnapshot: netboxclient.DeviceSnapshot{
				Device: netboxclient.Device{
					ID: "42",
					CustomFields: map[string]interface{}{
						"osac_interface_macs": map[string]interface{}{"data-0": "52:54:00:16:04:83"},
					},
				},
			},
		}
		got, err := NewNetBoxClient(api, manager, testNetBoxHostClass, nil).
			GetHostLogicalPortMACs(context.Background(), "baremetal/netbox-42")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(map[string]string{"data-0": "52:54:00:16:04:83"}))
		Expect(api.deviceReadIDs).To(Equal([]string{"42"}))
	})

	It("returns native interface lookup errors without reading the custom field", func() {
		lookupErr := errors.New("interface lookup failed")
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("baremetal").Times(2)
		api := &selectorQueryTestAPI{
			interfaces:   []netboxclient.DeviceInterface{{Name: "native", PrimaryMAC: "52:54:00:00:00:01"}},
			interfaceErr: lookupErr,
			deviceSnapshot: netboxclient.DeviceSnapshot{Device: netboxclient.Device{
				ID: "42",
				CustomFields: map[string]interface{}{
					"osac_interface_macs": map[string]interface{}{"data-0": "52:54:00:16:04:83"},
				},
			}},
		}
		got, err := NewNetBoxClient(api, manager, testNetBoxHostClass, nil).
			GetHostLogicalPortMACs(context.Background(), "baremetal/netbox-42")
		Expect(errors.Is(err, lookupErr)).To(BeTrue())
		Expect(got).To(BeNil())
		Expect(api.deviceReadCalls).To(BeZero())
	})

	It("returns an empty map when neither source provides a mapping", func() {
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("baremetal").Times(2)
		api := &selectorQueryTestAPI{
			deviceSnapshot: netboxclient.DeviceSnapshot{Device: netboxclient.Device{ID: "42"}},
		}

		got, err := NewNetBoxClient(api, manager, testNetBoxHostClass, nil).
			GetHostLogicalPortMACs(context.Background(), "baremetal/netbox-42")

		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeNil())
		Expect(got).To(BeEmpty())
	})

	It("returns malformed custom-field errors", func() {
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("baremetal").Times(2)
		api := &selectorQueryTestAPI{
			deviceSnapshot: netboxclient.DeviceSnapshot{Device: netboxclient.Device{
				ID: "42", CustomFields: map[string]interface{}{"osac_interface_macs": []string{"invalid"}},
			}},
		}

		got, err := NewNetBoxClient(api, manager, testNetBoxHostClass, nil).
			GetHostLogicalPortMACs(context.Background(), "baremetal/netbox-42")

		Expect(err).To(HaveOccurred())
		Expect(got).To(BeNil())
	})
})

var _ = Describe("NetBox assignment credential checks", func() {
	It("resolves credentials before conditionally claiming a staged device", func() {
		api := &selectorQueryTestAPI{
			deviceSnapshot: stagedManagedDeviceSnapshot(`W/"device-revision-1"`),
			patchSnapshot: netboxclient.DeviceSnapshot{Device: netboxclient.Device{
				ID:     "46",
				Status: netBoxStatusActive,
				CustomFields: map[string]interface{}{
					"osac_managed":     true,
					"osac_instance_id": "bmi-123",
				},
			}},
		}
		resolver := &fakeCredentialsReaderRepository{
			credentials: testNetBoxCredentials(),
		}
		resolver.onResolve = func() { Expect(api.patchCalls).To(BeZero()) }
		client := newTestNetBoxClient(api, resolver)

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(host).NotTo(BeNil())
		Expect(host.BareMetalInstanceID).To(Equal("bmi-123"))
		Expect(resolver.calls).To(Equal(1))
		Expect(resolver.hostID).To(Equal("46"))
		Expect(api.deviceReadCalls).To(Equal(1))
		Expect(api.patchCalls).To(Equal(1))
		Expect(api.patchDeviceID).To(Equal("46"))
		Expect(api.patchETag).To(Equal(`W/"device-revision-1"`))
		Expect(api.patch.Status).NotTo(BeNil())
		Expect(*api.patch.Status).To(Equal(netBoxStatusActive))
		Expect(api.patch.CustomFields).To(Equal(map[string]interface{}{"osac_instance_id": "bmi-123"}))
		Expect(api.patch.CustomFields).NotTo(HaveKey("username"))
		Expect(api.patch.CustomFields).NotTo(HaveKey("password"))
	})

	It("validates NetBox connection data before resolving credentials", func() {
		device := stagedManagedDeviceSnapshot(`W/"device-revision-1"`)
		delete(device.Device.CustomFields, netBoxBMCAddressField)
		api := &selectorQueryTestAPI{
			deviceSnapshot: device,
			patchSnapshot:  testNetBoxAssignmentSnapshot("failed", ""),
		}
		resolver := &fakeCredentialsReaderRepository{credentials: testNetBoxCredentials()}
		client := newTestNetBoxClient(api, resolver)

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(host).To(BeNil())
		Expect(resolver.calls).To(BeZero())
		Expect(api.patchCalls).To(Equal(1))
		Expect(api.patch.Status).NotTo(BeNil())
		Expect(*api.patch.Status).To(Equal("failed"))
	})

	DescribeTable("quarantines deterministic credential errors with a fixed repair message",
		func(cause error, wantMessage string) {
			api := &selectorQueryTestAPI{
				deviceSnapshot: stagedManagedDeviceSnapshot(`W/"device-revision-1"`),
				patchSnapshot:  testNetBoxAssignmentSnapshot("failed", ""),
			}
			resolver := &fakeCredentialsReaderRepository{err: credentialResolutionFailure(cause)}
			client := newTestNetBoxClient(api, resolver)

			host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

			Expect(host).To(BeNil())
			Expect(err).NotTo(HaveOccurred())
			Expect(resolver.calls).To(Equal(1))
			Expect(api.patchCalls).To(Equal(1))
			Expect(api.patchDeviceID).To(Equal("46"))
			Expect(api.patchETag).To(Equal(`W/"device-revision-1"`))
			Expect(api.patch.Status).NotTo(BeNil())
			Expect(*api.patch.Status).To(Equal("failed"))
			Expect(api.patch.CustomFields).To(BeEmpty())
			Expect(api.patch.ChangelogMessage).To(ContainSubstring(wantMessage))
			Expect(api.patch.ChangelogMessage).To(ContainSubstring("staged"))
			Expect(api.patch.ChangelogMessage).NotTo(ContainSubstring("source-password"))
		},
		Entry("missing Secret", errMissingNetBoxSecret, "no BMC Secret"),
		Entry("ambiguous Secret", errAmbiguousNetBoxSecret, "multiple BMC Secrets"),
		Entry("invalid Secret data", errors.New("source-password"), "BMC credentials"),
	)

	It("returns retryable resolver errors without changing device status", func() {
		api := &selectorQueryTestAPI{deviceSnapshot: stagedManagedDeviceSnapshot(`W/"device-revision-1"`)}
		resolver := &fakeCredentialsReaderRepository{err: errors.New("Secret API unavailable")}
		client := newTestNetBoxClient(api, resolver)

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(host).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("failed to resolve NetBox BMC credentials")))
		Expect(err.Error()).NotTo(ContainSubstring("source-password"))
		Expect(resolver.calls).To(Equal(1))
		Expect(api.patchCalls).To(BeZero())
	})

	DescribeTable("reconciles an uncertain quarantine write",
		func(patchErr error, latest netboxclient.DeviceSnapshot, wantErr string, wantPatchCause bool) {
			api := &selectorQueryTestAPI{
				deviceSnapshots: []netboxclient.DeviceSnapshot{
					stagedManagedDeviceSnapshot(`W/"device-revision-1"`), latest,
				},
				patchErr: patchErr,
			}
			resolver := &fakeCredentialsReaderRepository{err: credentialResolutionFailure(errors.New("invalid credentials"))}
			client := newTestNetBoxClient(api, resolver)

			host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

			Expect(host).To(BeNil())
			if wantErr == "" {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(MatchError(ContainSubstring(wantErr)))
				Expect(errors.Is(err, patchErr)).To(Equal(wantPatchCause))
			}
			Expect(api.patchCalls).To(Equal(1))
			Expect(api.deviceReadIDs).To(Equal([]string{"46", "46"}))
		},
		Entry("412 and already failed", &netboxclient.APIError{Kind: netboxclient.ErrorKindConflict}, testNetBoxAssignmentSnapshot("failed", ""), "", false),
		Entry("lost response and already failed", &netboxclient.APIError{Kind: netboxclient.ErrorKindTransport}, testNetBoxAssignmentSnapshot("failed", ""), "", false),
		Entry("412 and claimed elsewhere", &netboxclient.APIError{Kind: netboxclient.ErrorKindConflict}, testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-other"), "", false),
		Entry("412 and still staged", &netboxclient.APIError{Kind: netboxclient.ErrorKindConflict}, testNetBoxAssignmentSnapshot(netBoxStatusStaged, ""), "retry", true),
		Entry("transport failure and still staged", &netboxclient.APIError{Kind: netboxclient.ErrorKindTransport}, testNetBoxAssignmentSnapshot(netBoxStatusStaged, ""), "retry", true),
		Entry("reread identifies another device", &netboxclient.APIError{Kind: netboxclient.ErrorKindTransport}, netboxclient.DeviceSnapshot{Device: netboxclient.Device{ID: "47"}}, "different device", false),
	)

	It("returns an error when the quarantine response does not confirm failure", func() {
		api := &selectorQueryTestAPI{
			deviceSnapshot: stagedManagedDeviceSnapshot(`W/"device-revision-1"`),
			patchSnapshot:  testNetBoxAssignmentSnapshot(netBoxStatusStaged, ""),
		}
		client := newTestNetBoxClient(api, &fakeCredentialsReaderRepository{err: credentialResolutionFailure(errors.New("invalid credentials"))})

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(host).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("did not confirm")))
		Expect(api.patchCalls).To(Equal(1))
	})

	It("does not release the saved host ID when the response identifies a different device", func() {
		api := &selectorQueryTestAPI{
			deviceSnapshot: stagedManagedDeviceSnapshot(`W/"device-revision-1"`),
			patchSnapshot:  netboxclient.DeviceSnapshot{Device: netboxclient.Device{ID: "47", Status: "failed"}},
		}
		client := newTestNetBoxClient(api, &fakeCredentialsReaderRepository{err: credentialResolutionFailure(errMissingNetBoxSecret)})

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(host).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("different device")))
		Expect(api.patchCalls).To(Equal(1))
	})

	It("does not release the saved host ID when the failed device has a malformed owner", func() {
		api := &selectorQueryTestAPI{
			deviceSnapshot: stagedManagedDeviceSnapshot(`W/"device-revision-1"`),
			patchSnapshot: netboxclient.DeviceSnapshot{Device: netboxclient.Device{
				ID: "46", Status: "failed", CustomFields: map[string]interface{}{"osac_instance_id": 123},
			}},
		}
		client := newTestNetBoxClient(api, &fakeCredentialsReaderRepository{err: credentialResolutionFailure(errMissingNetBoxSecret)})

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(host).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("malformed osac_instance_id")))
		Expect(api.patchCalls).To(Equal(1))
	})

	It("resolves credentials and prepares an existing same-owner assignment without patching", func() {
		api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123")}
		resolver := &fakeCredentialsReaderRepository{credentials: testNetBoxCredentials()}
		client := newTestNetBoxClient(api, resolver)

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(host).NotTo(BeNil())
		Expect(host.BareMetalInstanceID).To(Equal("bmi-123"))
		Expect(resolver.calls).To(Equal(1))
		Expect(api.patchCalls).To(BeZero())
		Expect(host.Ready).To(BeTrue())
	})

	It("does not resolve credentials for a device assigned to another instance", func() {
		api := &selectorQueryTestAPI{deviceSnapshot: netboxclient.DeviceSnapshot{Device: netboxclient.Device{
			ID:     "46",
			Status: netBoxStatusActive,
			CustomFields: map[string]interface{}{
				"osac_managed":     true,
				"osac_instance_id": "bmi-other",
			},
		}}}
		resolver := &fakeCredentialsReaderRepository{}
		client := newTestNetBoxClient(api, resolver)

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(host).To(BeNil())
		Expect(resolver.calls).To(BeZero())
		Expect(api.patchCalls).To(BeZero())
	})

})

func stagedManagedDeviceSnapshot(etag string) netboxclient.DeviceSnapshot {
	return netboxclient.DeviceSnapshot{
		Device: netboxclient.Device{
			ID:     "46",
			Status: netBoxStatusStaged,
			CustomFields: map[string]interface{}{
				"osac_managed":        true,
				netBoxBMCAddressField: "redfish-virtualmedia+https://192.0.2.46/redfish/v1/Systems/1",
				netBoxBootMACField:    "aa:bb:cc:dd:ee:46",
			},
		},
		ETag: etag,
	}
}

type fakeSecretReader struct {
	listFilter secretapi.Filter
	listLimit  int32
	listResult secretapi.ListResult
	listError  error
	getID      string
	getSecret  secretapi.Secret
	getError   error
	listCalls  int
	getCalls   int
}

func (f *fakeSecretReader) List(_ context.Context, filter secretapi.Filter, limit int32) (secretapi.ListResult, error) {
	f.listCalls++
	f.listFilter = filter
	f.listLimit = limit
	return f.listResult, f.listError
}

func (f *fakeSecretReader) Get(_ context.Context, id string) (secretapi.Secret, error) {
	f.getCalls++
	f.getID = id
	return f.getSecret, f.getError
}

func (*fakeSecretReader) Close() error {
	return nil
}

var _ = Describe("NetBox connection data", func() {
	It("validates and normalizes the BMC address and boot MAC", func() {
		got, err := netBoxConnection(map[string]interface{}{
			"osac_bmc_address": "redfish-virtualmedia+https://192.0.2.42/redfish/v1/Systems/1",
			"osac_boot_mac":    "AA:BB:CC:DD:EE:42",
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(&netBoxConnectionData{
			BMCAddress:     "redfish-virtualmedia+https://192.0.2.42/redfish/v1/Systems/1",
			BootMACAddress: "aa:bb:cc:dd:ee:42",
		}))
	})

	DescribeTable("rejects invalid fields independently from credentials",
		func(customFields map[string]interface{}, wantErr string) {
			_, err := netBoxConnection(customFields)
			Expect(err).To(MatchError(ContainSubstring(wantErr)))
		},
		Entry("missing BMC address", map[string]interface{}{"osac_boot_mac": "aa:bb:cc:dd:ee:42"}, "invalid BMC address"),
		Entry("unsupported BMC address scheme", map[string]interface{}{"osac_bmc_address": "http://192.0.2.42", "osac_boot_mac": "aa:bb:cc:dd:ee:42"}, "invalid BMC address"),
		Entry("invalid boot MAC", map[string]interface{}{"osac_bmc_address": "ipmi://192.0.2.42", "osac_boot_mac": "not-a-mac"}, "invalid boot MAC"),
	)
})

var _ = Describe("NetBox credential resolver", func() {
	It("resolves typed BMC credentials from one system Secret", func() {
		secret := secretapi.Secret{
			ID:     "secret-1",
			Tenant: "system",
			Labels: map[string]string{"osac.openshift.io/netbox-device-42": ""},
			Data: map[string][]byte{
				"username": []byte("bmc-user"),
				"password": []byte("bmc-password"),
			},
		}
		reader := &fakeSecretReader{
			listResult: secretapi.ListResult{
				Total: 1,
				Items: []secretapi.Secret{{ID: "secret-1", Tenant: "system", Labels: secret.Labels}},
			},
			getSecret: secret,
		}
		resolver := NewNetBoxCredentialResolver(reader)

		got, err := resolver.GetCredentials(context.Background(), "42")

		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(&BMCCredentials{
			Username: []byte("bmc-user"),
			Password: []byte("bmc-password"),
		}))
		Expect(reader.listFilter).To(Equal(secretapi.Filter(`this.metadata.tenant == "system" && "osac.openshift.io/netbox-device-42" in this.metadata.labels`)))
		Expect(reader.listLimit).To(Equal(int32(1)))
		Expect(reader.getID).To(Equal("secret-1"))
		Expect(reader.listCalls).To(Equal(1))
		Expect(reader.getCalls).To(Equal(1))
	})

	DescribeTable("rejects missing or ambiguous Secret matches",
		func(result secretapi.ListResult, wantErr string, deterministic bool) {
			reader := &fakeSecretReader{listResult: result}
			resolver := NewNetBoxCredentialResolver(reader)

			_, err := resolver.GetCredentials(context.Background(), "42")

			Expect(err).To(MatchError(ContainSubstring(wantErr)))
			Expect(errors.Is(err, ErrCredentialResolutionFailed)).To(Equal(deterministic))
			Expect(reader.getCalls).To(BeZero())
		},
		Entry("missing", secretapi.ListResult{}, "no system-scoped OSAC Secret is associated", true),
		Entry("ambiguous", secretapi.ListResult{Total: 2, Items: []secretapi.Secret{{ID: "one"}, {ID: "two"}}}, "multiple system-scoped OSAC Secrets are associated", true),
		Entry("inconsistent total", secretapi.ListResult{Total: 1}, "inconsistent OSAC Secret list response", false),
	)

	DescribeTable("rejects missing, malformed, or changed credentials without exposing values",
		func(secret secretapi.Secret, wantErr string, deterministic bool) {
			reader := &fakeSecretReader{
				listResult: secretapi.ListResult{Total: 1, Items: []secretapi.Secret{{ID: "secret-1", Tenant: "system", Labels: map[string]string{"osac.openshift.io/netbox-device-42": "true"}}}},
				getSecret:  secret,
			}
			resolver := NewNetBoxCredentialResolver(reader)

			_, err := resolver.GetCredentials(context.Background(), "42")

			Expect(err).To(MatchError(ContainSubstring(wantErr)))
			Expect(errors.Is(err, ErrCredentialResolutionFailed)).To(Equal(deterministic))
			Expect(err.Error()).NotTo(ContainSubstring("source-user"))
			Expect(err.Error()).NotTo(ContainSubstring("source-password"))
		},
		Entry("missing username", secretapi.Secret{ID: "secret-1", Tenant: "system", Labels: map[string]string{"osac.openshift.io/netbox-device-42": "true"}, Data: map[string][]byte{"password": []byte("source-password")}}, "non-empty username and password", true),
		Entry("empty password", secretapi.Secret{ID: "secret-1", Tenant: "system", Labels: map[string]string{"osac.openshift.io/netbox-device-42": "true"}, Data: map[string][]byte{"username": []byte("source-user"), "password": {}}}, "non-empty username and password", true),
		Entry("changed tenant", secretapi.Secret{ID: "secret-1", Tenant: "tenant-a", Labels: map[string]string{"osac.openshift.io/netbox-device-42": "true"}, Data: map[string][]byte{"username": []byte("source-user"), "password": []byte("source-password")}}, "not a system-scoped Secret associated", false),
		Entry("changed label", secretapi.Secret{ID: "secret-1", Tenant: "system", Labels: map[string]string{"other": "true"}, Data: map[string][]byte{"username": []byte("source-user"), "password": []byte("source-password")}}, "not a system-scoped Secret associated", false),
	)

	It("returns retryable API errors without credentials in the error", func() {
		reader := &fakeSecretReader{listError: errors.New("permission denied")}
		resolver := NewNetBoxCredentialResolver(reader)

		_, err := resolver.GetCredentials(context.Background(), "42")

		Expect(err).To(MatchError(ContainSubstring("failed to list OSAC Secrets")))
		Expect(errors.Is(err, ErrCredentialResolutionFailed)).To(BeFalse())
		Expect(err.Error()).NotTo(ContainSubstring("source-password"))
		Expect(reader.getCalls).To(BeZero())
	})

	It("leaves a failed Secret Get retryable", func() {
		reader := &fakeSecretReader{
			listResult: secretapi.ListResult{Total: 1, Items: []secretapi.Secret{{
				ID: "secret-1", Tenant: "system", Labels: map[string]string{"osac.openshift.io/netbox-device-42": "true"},
			}}},
			getError: errors.New("permission denied"),
		}

		_, err := NewNetBoxCredentialResolver(reader).GetCredentials(context.Background(), "42")

		Expect(err).To(MatchError(ContainSubstring("failed to get OSAC Secret")))
		Expect(errors.Is(err, ErrCredentialResolutionFailed)).To(BeFalse())
		Expect(reader.getCalls).To(Equal(1))
	})
})
