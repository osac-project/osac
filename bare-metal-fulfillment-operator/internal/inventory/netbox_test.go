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
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/credentials"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/credentials/bmh"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/directclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/netboxclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/test/netboxmock"
)

const testNetBoxHostClass = "metal3"

func testNetBoxCredentials() *credentials.BMC {
	return &credentials.BMC{
		Username: []byte("bmc-user"),
		Password: []byte("source-password"),
	}
}

func credentialResolutionFailure(err error) error {
	return fmt.Errorf("%w: %w", credentials.ErrResolutionFailed, err)
}

type noOpCredentialsWriter struct{}

func (noOpCredentialsWriter) Create(context.Context, credentials.Reference, credentials.BMC) error {
	return nil
}
func (noOpCredentialsWriter) Delete(context.Context, credentials.Reference) error { return nil }

func newNetBoxClient(api NetBoxAPI, manager baremetalhost.BMHLifecycleManager, hostClass string, reader credentials.Reader) *NetBoxClient {
	return NewNetBoxClient(api, manager, noOpCredentialsWriter{}, hostClass, reader)
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

			ctrl := gomock.NewController(GinkgoT())
			manager := newTestBMHManager("metal3-system")
			writer := NewMockWriter(ctrl)
			credentialsReader := &fakeCredentialsReaderRepository{}
			client := NewNetBoxClient(api, manager, writer, testNetBoxHostClass, credentialsReader)

			Expect(client.api).To(BeIdenticalTo(api))
			Expect(client.bmhManager).To(BeIdenticalTo(manager))
			Expect(client.credentialsWriter).To(BeIdenticalTo(writer))
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
			ctrl := gomock.NewController(GinkgoT())
			manager := baremetalhost.NewMockBMHLifecycleManager(ctrl)
			writer := NewMockWriter(ctrl)
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			bmhDelete := manager.EXPECT().DeleteBMH(gomock.Any(), "netbox-46", "bmi-123").Return(nil)
			bmhGone := manager.EXPECT().BMHExists(gomock.Any(), "netbox-46").Return(false, nil)
			secretDelete := writer.EXPECT().Delete(gomock.Any(), credentials.Reference{HostID: "netbox-46", BMIID: "bmi-123"}).Return(nil)
			gomock.InOrder(bmhDelete, bmhGone, secretDelete)
			client := newTestNetBoxClientWithWriter(api, nil, manager, writer)

			err := client.UnassignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(api.deviceReadIDs).To(Equal([]string{"46"}))
			Expect(api.patchCalls).To(Equal(1))
			Expect(api.patchETag).To(Equal(api.deviceSnapshot.ETag))
			Expect(*api.patch.Status).To(Equal(netBoxStatusStaged))
			Expect(api.patch.CustomFields).To(Equal(map[string]interface{}{"osac_instance_id": nil}))
		})

		It("cleans owned resources when the NetBox device is gone", func() {
			ctx := context.Background()
			bareMetalHost := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{Name: "netbox-5", Namespace: "metal3-system"},
				Spec:       metal3api.BareMetalHostSpec{ConsumerRef: &corev1.ObjectReference{Name: "bmi-old"}},
			}
			manager, k8sClient := newNetBoxResourceManager(bareMetalHost)
			writer := newTestSecretWriter(k8sClient, "metal3-system")
			Expect(writer.Create(ctx, credentials.Reference{HostID: "netbox-5", BMIID: "bmi-old"}, credentials.BMC{
				Username: []byte("admin"), Password: []byte("password"),
			})).To(Succeed())
			api := &selectorQueryTestAPI{deviceGetError: &netboxclient.APIError{Kind: netboxclient.ErrorKindNotFound, StatusCode: http.StatusNotFound}}
			netBoxClient := newTestNetBoxClientWithWriter(api, nil, manager, writer)

			Expect(netBoxClient.UnassignHost(ctx, "metal3-system/netbox-5", "bmi-old", nil)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bareMetalHost), &metal3api.BareMetalHost{})).NotTo(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "metal3-system", Name: "netbox-5-bmc-secret"}, &corev1.Secret{})).NotTo(Succeed())
			Expect(api.patchCalls).To(BeZero())
		})

		It("reports the device ID when a NetBox read fails", func() {
			api := &selectorQueryTestAPI{deviceGetError: &netboxclient.APIError{Kind: netboxclient.ErrorKindUnavailable}}
			manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			client := newNetBoxClient(api, manager, testNetBoxHostClass, nil)
			err := client.UnassignHost(context.Background(), "metal3-system/netbox-5", "bmi-old", nil)
			Expect(err).To(MatchError(ContainSubstring("NetBox device 5")))
		})

		It("leaves another BMI's claim and resources untouched on a stale retry", func() {
			api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-new")}
			manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			client := newNetBoxClient(api, manager, testNetBoxHostClass, nil)

			err := client.UnassignHost(context.Background(), "metal3-system/netbox-46", "bmi-old", nil)

			Expect(err).NotTo(HaveOccurred())
			Expect(api.patchCalls).To(BeZero())
		})

		It("keeps a foreign BMH and the NetBox claim", func() {
			ctx := context.Background()
			bareMetalHost := &metal3api.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{Name: "netbox-46", Namespace: "metal3-system"},
				Spec:       metal3api.BareMetalHostSpec{ConsumerRef: &corev1.ObjectReference{Name: "bmi-A"}},
			}
			manager, k8sClient := newNetBoxResourceManager(bareMetalHost)
			secretKey := client.ObjectKey{Namespace: "metal3-system", Name: "netbox-46-bmc-secret"}
			writer := newTestSecretWriter(k8sClient, "metal3-system")
			Expect(writer.Create(ctx, credentials.Reference{HostID: "netbox-46", BMIID: "bmi-B"}, credentials.BMC{
				Username: []byte("admin"), Password: []byte("password"),
			})).To(Succeed())
			api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-B")}
			netBoxClient := newTestNetBoxClientWithWriter(api, nil, manager, writer)

			Expect(netBoxClient.UnassignHost(ctx, "metal3-system/netbox-46", "bmi-B", nil)).To(MatchError(ContainSubstring("not owned by this BMI")))
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bareMetalHost), &metal3api.BareMetalHost{})).To(Succeed())
			Expect(k8sClient.Get(ctx, secretKey, &corev1.Secret{})).To(Succeed())
			Expect(api.patchCalls).To(BeZero())
		})

		It("keeps a foreign generated Secret and the NetBox claim", func() {
			ctx := context.Background()
			manager, k8sClient := newNetBoxResourceManager()
			secretKey := client.ObjectKey{Namespace: "metal3-system", Name: "netbox-46-bmc-secret"}
			writer := newTestSecretWriter(k8sClient, "metal3-system")
			Expect(writer.Create(ctx, credentials.Reference{HostID: "netbox-46", BMIID: "bmi-A"}, credentials.BMC{
				Username: []byte("admin"), Password: []byte("password"),
			})).To(Succeed())
			api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-B")}
			netBoxClient := newTestNetBoxClientWithWriter(api, nil, manager, writer)

			Expect(netBoxClient.UnassignHost(ctx, "metal3-system/netbox-46", "bmi-B", nil)).To(MatchError(ContainSubstring("not owned by this BMI")))
			Expect(k8sClient.Get(ctx, secretKey, &corev1.Secret{})).To(Succeed())
			Expect(api.patchCalls).To(BeZero())
		})

		DescribeTable("cleans resources without changing an unclaimed device", func(status string) {
			api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(status, "")}
			ctrl := gomock.NewController(GinkgoT())
			manager := baremetalhost.NewMockBMHLifecycleManager(ctrl)
			writer := NewMockWriter(ctrl)
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			manager.EXPECT().DeleteBMH(gomock.Any(), "netbox-46", "bmi-123").Return(nil)
			manager.EXPECT().BMHExists(gomock.Any(), "netbox-46").Return(false, nil)
			writer.EXPECT().Delete(gomock.Any(), credentials.Reference{HostID: "netbox-46", BMIID: "bmi-123"}).Return(nil)
			client := newTestNetBoxClientWithWriter(api, nil, manager, writer)

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
			client := newNetBoxClient(api, manager, testNetBoxHostClass, nil)

			err := client.UnassignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

			Expect(err).To(MatchError(ContainSubstring("inconsistent release state")))
			Expect(api.patchCalls).To(BeZero())
		})

		DescribeTable("leaves NetBox unchanged when cleanup fails", func(bmhErr, secretErr error) {
			api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123")}
			ctrl := gomock.NewController(GinkgoT())
			manager := baremetalhost.NewMockBMHLifecycleManager(ctrl)
			writer := NewMockWriter(ctrl)
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			bmhDelete := manager.EXPECT().DeleteBMH(gomock.Any(), "netbox-46", "bmi-123").Return(bmhErr)
			if bmhErr == nil {
				bmhGone := manager.EXPECT().BMHExists(gomock.Any(), "netbox-46").Return(false, nil).After(bmhDelete)
				writer.EXPECT().Delete(gomock.Any(), credentials.Reference{HostID: "netbox-46", BMIID: "bmi-123"}).Return(secretErr).After(bmhGone)
			}
			client := newTestNetBoxClientWithWriter(api, nil, manager, writer)

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
			bareMetalHost := &metal3api.BareMetalHost{ObjectMeta: metav1.ObjectMeta{
				Name: "netbox-46", Namespace: "metal3-system",
				Finalizers: []string{"test.osac.openshift.io/hold"},
			}, Spec: metal3api.BareMetalHostSpec{ConsumerRef: &corev1.ObjectReference{Name: "bmi-123"}}}
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(bareMetalHost).Build()
			manager := newTestBMHManagerFor(k8sClient, "metal3-system")
			secretKey := client.ObjectKey{Namespace: "metal3-system", Name: "netbox-46-bmc-secret"}
			writer := newTestSecretWriter(k8sClient, "metal3-system")
			Expect(writer.Create(ctx, credentials.Reference{HostID: "netbox-46", BMIID: "bmi-123"}, credentials.BMC{
				Username: []byte("admin"), Password: []byte("test-password"),
			})).To(Succeed())
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, secretKey, secret)).To(Succeed())
			secret.Finalizers = []string{"test.osac.openshift.io/hold"}
			Expect(k8sClient.Update(ctx, secret)).To(Succeed())
			api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123")}
			netBoxClient := newTestNetBoxClientWithWriter(api, nil, manager, writer)

			Expect(netBoxClient.UnassignHost(ctx, "metal3-system/netbox-46", "bmi-123", nil)).To(MatchError(errNetBoxBMHDeletionPending))
			Expect(api.patchCalls).To(BeZero())
			Expect(k8sClient.Get(ctx, secretKey, secret)).To(Succeed())
			Expect(secret.DeletionTimestamp).To(BeNil())

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bareMetalHost), bareMetalHost)).To(Succeed())
			bareMetalHost.Finalizers = nil
			Expect(k8sClient.Update(ctx, bareMetalHost)).To(Succeed())
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
			manager.EXPECT().DeleteBMH(gomock.Any(), "netbox-46", "bmi-123").Return(nil)
			manager.EXPECT().BMHExists(gomock.Any(), "netbox-46").Return(false, readErr)
			client := newNetBoxClient(api, manager, testNetBoxHostClass, nil)

			Expect(client.UnassignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)).To(MatchError(readErr))
			Expect(api.patchCalls).To(BeZero())
		})

		It("finishes when the device disappears after resource cleanup", func() {
			api := &selectorQueryTestAPI{
				deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123"),
				patchErr:       &netboxclient.APIError{Kind: netboxclient.ErrorKindNotFound, StatusCode: http.StatusNotFound},
			}
			ctrl := gomock.NewController(GinkgoT())
			manager := baremetalhost.NewMockBMHLifecycleManager(ctrl)
			writer := NewMockWriter(ctrl)
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			manager.EXPECT().DeleteBMH(gomock.Any(), "netbox-46", "bmi-123").Return(nil)
			manager.EXPECT().BMHExists(gomock.Any(), "netbox-46").Return(false, nil)
			writer.EXPECT().Delete(gomock.Any(), credentials.Reference{HostID: "netbox-46", BMIID: "bmi-123"}).Return(nil)
			client := newTestNetBoxClientWithWriter(api, nil, manager, writer)

			Expect(client.UnassignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)).To(Succeed())
			Expect(api.patchCalls).To(Equal(1))
		})

		It("returns a 412 for the next reconciliation", func() {
			patchErr := &netboxclient.APIError{Kind: netboxclient.ErrorKindConflict, StatusCode: http.StatusPreconditionFailed}
			api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123"), patchErr: patchErr}
			ctrl := gomock.NewController(GinkgoT())
			manager := baremetalhost.NewMockBMHLifecycleManager(ctrl)
			writer := NewMockWriter(ctrl)
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			manager.EXPECT().DeleteBMH(gomock.Any(), "netbox-46", "bmi-123").Return(nil)
			manager.EXPECT().BMHExists(gomock.Any(), "netbox-46").Return(false, nil)
			writer.EXPECT().Delete(gomock.Any(), credentials.Reference{HostID: "netbox-46", BMIID: "bmi-123"}).Return(nil)
			client := newTestNetBoxClientWithWriter(api, nil, manager, writer)

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
	credentials *credentials.BMC
	err         error
	calls       int
	hostID      string
	onResolve   func()
}

func (r *fakeCredentialsReaderRepository) GetCredentials(_ context.Context, hostID string) (*credentials.BMC, error) {
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

func newTestNetBoxClient(api NetBoxAPI, resolver credentials.Reader) *NetBoxClient {
	ctrl := gomock.NewController(GinkgoT())
	manager := baremetalhost.NewMockBMHLifecycleManager(ctrl)
	manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
	manager.EXPECT().CreateBMH(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	manager.EXPECT().IsBMHAvailable(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	return newTestNetBoxClientWithManager(api, resolver, manager)
}

func newTestNetBoxClientWithManager(api NetBoxAPI, resolver credentials.Reader, manager baremetalhost.BMHLifecycleManager) *NetBoxClient {
	return newNetBoxClient(api, manager, testNetBoxHostClass, resolver)
}

func newTestNetBoxClientWithWriter(
	api NetBoxAPI,
	resolver credentials.Reader,
	manager baremetalhost.BMHLifecycleManager,
	writer credentials.Writer,
) *NetBoxClient {
	return NewNetBoxClient(api, manager, writer, testNetBoxHostClass, resolver)
}

func newNetBoxResourceManager(objects ...client.Object) (*baremetalhost.Manager, client.Client) {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(metal3api.AddToScheme(scheme)).To(Succeed())
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return newTestBMHManagerFor(k8sClient, "metal3-system"), k8sClient
}

func newTestNetBoxClientWithCredentials(api NetBoxAPI) *NetBoxClient {
	return newTestNetBoxClient(api, &fakeCredentialsReaderRepository{
		credentials: &credentials.BMC{
			Username: []byte("user"),
			Password: []byte("pass"),
		},
	})
}

var _ = Describe("NetBox selector query", func() {
	Describe("FindFreeHost", func() {
		It("returns an error when the Metal3 namespace is missing without querying NetBox", func() {
			api := &selectorQueryTestAPI{}
			client := newNetBoxClient(api, newTestBMHManager(""), testNetBoxHostClass, nil)

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
			client := newNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass, nil)
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
				Available:       true,
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
			client := newNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass, nil)

			_, err := client.FindFreeHost(context.Background(), map[string]string{"osac_managed": "false"})

			Expect(err).NotTo(HaveOccurred())
			Expect(api.deviceQueries).To(Equal(1))
			Expect(api.deviceQuery["cf_osac_managed"]).To(Equal("true"))
		})

		It("queries the fixed free-host filters when the selector is empty", func() {
			api := &selectorQueryTestAPI{}
			client := newNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass, nil)

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
			client := newNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass, nil)

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
		ctrl := gomock.NewController(GinkgoT())
		manager := baremetalhost.NewMockBMHLifecycleManager(ctrl)
		writer := NewMockWriter(ctrl)
		manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
		secretEnsured := false
		bmhCreated := false
		ensureCall := writer.EXPECT().Create(gomock.Any(), credentials.Reference{HostID: "netbox-46", BMIID: "bmi-123"}, gomock.Eq(*testNetBoxCredentials())).DoAndReturn(
			func(_ context.Context, ref credentials.Reference, bmc credentials.BMC) error {
				Expect(api.patchCalls).To(Equal(1))
				Expect(ref.HostID).To(Equal("netbox-46"))
				Expect(bmc.Username).To(Equal([]byte("bmc-user")))
				Expect(bmc.Password).To(Equal([]byte("source-password")))
				Expect(ref.BMIID).To(Equal("bmi-123"))
				secretEnsured = true
				return nil
			})
		createCall := manager.EXPECT().CreateBMH(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params baremetalhost.CreateParams) error {
				Expect(secretEnsured).To(BeTrue())
				Expect(params.Name).To(Equal("netbox-46"))
				Expect(params.BMCAddress).To(Equal("redfish-virtualmedia+https://192.0.2.46/redfish/v1/Systems/1"))
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
		availabilityCall := manager.EXPECT().IsBMHAvailable(gomock.Any(), "netbox-46").DoAndReturn(
			func(context.Context, string) (bool, error) {
				Expect(bmhCreated).To(BeTrue())
				return true, nil
			})
		gomock.InOrder(ensureCall, createCall, availabilityCall)
		client := newTestNetBoxClientWithWriter(api, resolver, manager, writer)

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
		Expect(host.Available).To(BeTrue())
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
	It("returns an error when the reader returns no credentials", func() {
		api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123")}
		ctrl := gomock.NewController(GinkgoT())
		manager := baremetalhost.NewMockBMHLifecycleManager(ctrl)
		manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
		writer := NewMockWriter(ctrl)
		client := newTestNetBoxClientWithWriter(api, &fakeCredentialsReaderRepository{}, manager, writer)

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(host).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("BMC credentials are required")))
		Expect(api.patchCalls).To(BeZero())
	})

	It("returns not ready without an error when Metal3 readiness is false", func() {
		api := &selectorQueryTestAPI{deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123")}
		ctrl := gomock.NewController(GinkgoT())
		manager := baremetalhost.NewMockBMHLifecycleManager(ctrl)
		writer := NewMockWriter(ctrl)
		manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
		ensureCall := writer.EXPECT().Create(gomock.Any(), credentials.Reference{HostID: "netbox-46", BMIID: "bmi-123"}, gomock.Eq(*testNetBoxCredentials())).Return(nil)
		createCall := manager.EXPECT().CreateBMH(gomock.Any(), gomock.Any()).Return(nil)
		availabilityCall := manager.EXPECT().IsBMHAvailable(gomock.Any(), "netbox-46").Return(false, nil)
		gomock.InOrder(ensureCall, createCall, availabilityCall)
		client := newTestNetBoxClientWithWriter(api, &fakeCredentialsReaderRepository{credentials: testNetBoxCredentials()}, manager, writer)

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(host).NotTo(BeNil())
		Expect(host.Available).To(BeFalse())
		Expect(api.patchCalls).To(BeZero())
	})

	DescribeTable("returns preparation failures without compensating the confirmed NetBox claim",
		func(failureStage, wantError string) {
			api := &selectorQueryTestAPI{
				deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusStaged, ""),
				patchSnapshot:  testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123"),
			}
			ctrl := gomock.NewController(GinkgoT())
			manager := baremetalhost.NewMockBMHLifecycleManager(ctrl)
			writer := NewMockWriter(ctrl)
			manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
			var orderedCalls []*gomock.Call
			secretCall := writer.EXPECT().Create(gomock.Any(), credentials.Reference{HostID: "netbox-46", BMIID: "bmi-123"}, gomock.Eq(*testNetBoxCredentials()))
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
					availabilityCall := manager.EXPECT().IsBMHAvailable(gomock.Any(), "netbox-46").Return(false, errors.New("availability check failed"))
					orderedCalls = append(orderedCalls, availabilityCall)
				}
			}
			orderedCallArgs := make([]any, len(orderedCalls))
			for i, call := range orderedCalls {
				orderedCallArgs[i] = call
			}
			gomock.InOrder(orderedCallArgs...)
			resolver := &fakeCredentialsReaderRepository{credentials: testNetBoxCredentials()}
			client := newTestNetBoxClientWithWriter(api, resolver, manager, writer)

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
		Entry("availability check failure", "readiness", "failed to check BareMetalHost availability for NetBox host"),
	)

	It("retries preparation for the persisted same-owner allocation without another claim PATCH", func() {
		api := &selectorQueryTestAPI{
			deviceSnapshots: []netboxclient.DeviceSnapshot{
				testNetBoxAssignmentSnapshot(netBoxStatusStaged, ""),
				testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123"),
			},
			patchSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123"),
		}
		ctrl := gomock.NewController(GinkgoT())
		manager := baremetalhost.NewMockBMHLifecycleManager(ctrl)
		writer := NewMockWriter(ctrl)
		manager.EXPECT().Namespace().Return("metal3-system").AnyTimes()
		var ensureCalls int
		ensureCall := writer.EXPECT().Create(gomock.Any(), credentials.Reference{HostID: "netbox-46", BMIID: "bmi-123"}, gomock.Eq(*testNetBoxCredentials())).DoAndReturn(
			func(context.Context, credentials.Reference, credentials.BMC) error {
				ensureCalls++
				if ensureCalls == 1 {
					return errors.New("temporary Secret operation failure")
				}
				return nil
			}).Times(2)
		createCall := manager.EXPECT().CreateBMH(gomock.Any(), gomock.Any()).Return(nil)
		availabilityCall := manager.EXPECT().IsBMHAvailable(gomock.Any(), "netbox-46").Return(true, nil)
		gomock.InOrder(ensureCall, createCall, availabilityCall)
		resolver := &fakeCredentialsReaderRepository{credentials: testNetBoxCredentials()}
		client := newTestNetBoxClientWithWriter(api, resolver, manager, writer)

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
		Available:           true,
		BareMetalInstanceID: instanceID,
	}
}

func testNetBoxSelectorBMHManager() *baremetalhost.Manager {
	return newTestBMHManager("metal3-system")
}

// newTestDirectClient wraps an already-uncached client as a direct client.
func newTestDirectClient(c client.Client) directclient.Client {
	direct, err := directclient.WrapForTest(c)
	Expect(err).NotTo(HaveOccurred())
	return direct
}

// newTestBMHManagerFor builds a BMH manager over the given client.
func newTestBMHManagerFor(c client.Client, namespace string) *baremetalhost.Manager {
	manager, err := baremetalhost.NewManager(c, newTestDirectClient(c), namespace)
	Expect(err).NotTo(HaveOccurred())
	return manager
}

// newTestSecretWriter builds a BMC Secret writer over the given client.
func newTestSecretWriter(c client.Client, namespace string) *bmh.SecretWriter {
	writer, err := bmh.NewSecretWriter(newTestDirectClient(c), namespace)
	Expect(err).NotTo(HaveOccurred())
	return writer
}

// newTestBMHManager returns a BMH manager backed by an empty fake client. It
// is for tests that exercise wiring rather than BMH or Secret behavior.
func newTestBMHManager(namespace string) *baremetalhost.Manager {
	return newTestBMHManagerFor(fake.NewClientBuilder().WithScheme(newTestScheme()).Build(), namespace)
}

var _ = Describe("NetBox GetHostNICs", func() {
	It("reads inspected NICs from the BMH manager without querying NetBox", func() {
		ctx := context.Background()
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("baremetal").Times(2)
		manager.EXPECT().GetHardwareNICs(ctx, "netbox-42").Return([]string{"AA:BB:CC:DD:EE:01"}, nil)
		api := &selectorQueryTestAPI{}
		got, err := newNetBoxClient(api, manager, testNetBoxHostClass, nil).GetHostNICs(ctx, "baremetal/netbox-42")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]HostNIC{{MAC: "aa:bb:cc:dd:ee:01"}}))
		Expect(api.deviceReadCalls).To(BeZero())
	})

	It("reads NICs from a provisioned BMH without requiring availability", func() {
		ctx := context.Background()
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("baremetal").Times(2)
		manager.EXPECT().GetHardwareNICs(ctx, "netbox-42").Return([]string{"AA:BB:CC:DD:EE:02"}, nil)
		got, err := newNetBoxClient(&selectorQueryTestAPI{}, manager, testNetBoxHostClass, nil).
			GetHostNICs(ctx, "baremetal/netbox-42")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]HostNIC{{MAC: "aa:bb:cc:dd:ee:02"}}))
	})

	It("errors when the BMH has no NIC inventory, matching metal3", func() {
		ctx := context.Background()
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("baremetal").Times(2)
		manager.EXPECT().GetHardwareNICs(ctx, "netbox-42").Return(nil, nil)

		got, err := newNetBoxClient(&selectorQueryTestAPI{}, manager, testNetBoxHostClass, nil).
			GetHostNICs(ctx, "baremetal/netbox-42")

		Expect(err).To(MatchError(ContainSubstring("netbox-42 has no NIC inventory")))
		Expect(got).To(BeNil())
	})

	It("propagates BMH errors without querying NetBox", func() {
		lookupErr := errors.New("BMH lookup failed")
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return("baremetal").Times(2)
		manager.EXPECT().GetHardwareNICs(gomock.Any(), "netbox-42").Return(nil, lookupErr)
		api := &selectorQueryTestAPI{}
		got, err := newNetBoxClient(api, manager, testNetBoxHostClass, nil).
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
		got, err := newNetBoxClient(api, manager, testNetBoxHostClass, nil).
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
		got, err := newNetBoxClient(api, manager, testNetBoxHostClass, nil).
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
		got, err := newNetBoxClient(api, manager, testNetBoxHostClass, nil).
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
		got, err := newNetBoxClient(api, manager, testNetBoxHostClass, nil).
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

		got, err := newNetBoxClient(api, manager, testNetBoxHostClass, nil).
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

		got, err := newNetBoxClient(api, manager, testNetBoxHostClass, nil).
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
		Entry("missing Secret", credentials.ErrCredentialsNotFound, "no BMC Secret"),
		Entry("ambiguous Secret", credentials.ErrAmbiguousCredentials, "multiple BMC Secrets"),
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
		client := newTestNetBoxClient(api, &fakeCredentialsReaderRepository{err: credentialResolutionFailure(credentials.ErrCredentialsNotFound)})

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
		client := newTestNetBoxClient(api, &fakeCredentialsReaderRepository{err: credentialResolutionFailure(credentials.ErrCredentialsNotFound)})

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
		Expect(host.Available).To(BeTrue())
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

var _ = Describe("NetBox HTTP contract", Label("netbox-contract"), func() {
	const (
		metal3Namespace = "metal3-system"
		caseFieldName   = "contract_case"
		// A synthetic v2 token exercises the production Bearer authentication.
		testToken = "nbt_aaaaaaaaaaaa.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	var (
		ctx       context.Context
		server    *netboxmock.Server
		caFile    string
		tokenFile string
	)

	// requestMark records how many requests the server has handled, so a case can
	// assert exactly what the adapter sent.
	requestMark := func() int { return len(server.Requests()) }

	// requestsSince returns the adapter requests recorded after mark.
	requestsSince := func(mark int) []netboxmock.Request {
		all := server.Requests()
		if mark > len(all) {
			return nil
		}
		return all[mark:]
	}

	// patchBodies returns the decoded body of every recorded PATCH.
	patchBodies := func(requests []netboxmock.Request) []map[string]any {
		bodies := make([]map[string]any, 0, len(requests))
		for _, request := range requests {
			if request.Method != "PATCH" {
				continue
			}
			var body map[string]any
			if err := json.Unmarshal(request.Body, &body); err != nil {
				continue
			}
			bodies = append(bodies, body)
		}
		return bodies
	}

	// contractFields are the custom-field definitions the adapter's fixed filters
	// require. Every one is exact-filtered, which is the schema the adapter assumes
	// an administrator has provisioned.
	contractFields := func() []netboxmock.Field {
		return []netboxmock.Field{
			{Name: "osac_managed", Label: "OSAC managed", Type: "boolean", FilterLogic: netboxmock.FilterExact},
			{Name: "osac_instance_id", Label: "OSAC instance ID", Type: "text", FilterLogic: netboxmock.FilterExact},
			{Name: "osac_bmc_address", Label: "OSAC BMC address", Type: "text", FilterLogic: netboxmock.FilterExact},
			{Name: "osac_boot_mac", Label: "OSAC boot MAC", Type: "text", FilterLogic: netboxmock.FilterExact},
			{Name: "osac_interface_macs", Label: "OSAC interface MACs", Type: "json", FilterLogic: netboxmock.FilterExact},
			{Name: caseFieldName, Label: "Contract case", Type: "text", FilterLogic: netboxmock.FilterExact},
		}
	}

	// eligibleFields marks a device as an enrolled, unowned pool member for one
	// case. osac_instance_id is deliberately absent, which is the representation an
	// administrator-created device has.
	eligibleFields := func(caseValue string) map[string]any {
		return map[string]any{
			"osac_managed": true,
			caseFieldName:  caseValue,
		}
	}

	// addEligibleDevice registers a staged, managed, unowned device. A nil name
	// models a device an administrator created without a name.
	addEligibleDevice := func(id int, name *string, caseValue string) {
		deviceName := ""
		if name != nil {
			deviceName = *name
		}
		server.AddDevice(netboxmock.Device{
			ID:           id,
			Name:         deviceName,
			Status:       "staged",
			CustomFields: eligibleFields(caseValue),
		})
	}

	// addExactField registers an additional exact-filtered capability field.
	addExactField := func(name, fieldType string) {
		server.AddField(netboxmock.Field{
			Name:        name,
			Label:       "Contract " + name,
			Type:        fieldType,
			FilterLogic: netboxmock.FilterExact,
		})
	}

	// claimableFields returns the custom fields a device needs before the adapter
	// can resolve Metal3 connection data and credentials for it.
	claimableFields := func(caseValue string) map[string]any {
		fields := eligibleFields(caseValue)
		fields["osac_bmc_address"] = "redfish-virtualmedia+https://192.0.2.42/redfish/v1/Systems/1"
		fields["osac_boot_mac"] = "aa:bb:cc:dd:ee:42"
		return fields
	}

	// addClaimableDevice registers a staged, managed, unowned device that carries
	// valid BMC connection metadata.
	addClaimableDevice := func(id int, name *string, caseValue string) {
		deviceName := ""
		if name != nil {
			deviceName = *name
		}
		server.AddDevice(netboxmock.Device{
			ID:           id,
			Name:         deviceName,
			Status:       "staged",
			CustomFields: claimableFields(caseValue),
		})
	}

	BeforeEach(func() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		DeferCleanup(cancel)

		server = netboxmock.New()
		DeferCleanup(server.Stop)
		server.SetAuth("Bearer", testToken)
		for _, field := range contractFields() {
			server.AddField(field)
		}

		fixtureDir := GinkgoT().TempDir()
		caFile = filepath.Join(fixtureDir, "netbox-ca.pem")
		cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
		Expect(os.WriteFile(caFile, cert, 0o600)).To(Succeed())
		tokenFile = filepath.Join(fixtureDir, "netbox-token")
		Expect(os.WriteFile(tokenFile, []byte(testToken+"\n"), 0o600)).To(Succeed())
	})

	newAPIClient := func() *netboxclient.Client {
		client, err := netboxclient.New(netboxclient.Config{
			Endpoint: server.URL(), CAFile: caFile, TokenFile: tokenFile,
		})
		Expect(err).NotTo(HaveOccurred())
		return client
	}

	newInventoryClient := func() *NetBoxClient {
		manager := baremetalhost.NewMockBMHLifecycleManager(gomock.NewController(GinkgoT()))
		manager.EXPECT().Namespace().Return(metal3Namespace).AnyTimes()
		return newTestNetBoxClientWithManager(newAPIClient(), nil, manager)
	}

	newContractClientWithManager := func(manager baremetalhost.BMHLifecycleManager) *NetBoxClient {
		reader := &fakeCredentialsReaderRepository{credentials: testNetBoxCredentials()}
		return newTestNetBoxClientWithManager(newAPIClient(), reader, manager)
	}

	Describe("Independent tags and preserved administrator metadata", func() {
		var (
			manager *netBoxContractBMHManager
			client  *NetBoxClient
		)

		BeforeEach(func() {
			manager = &netBoxContractBMHManager{namespace: metal3Namespace}
			client = newContractClientWithManager(manager)
			addExactField("cpu_cores", "integer")
			addExactField("placement_zone", "text")
		})

		It("selects on capabilities only, then preserves every other field across claim and release", func() {
			tagged := claimableFields("tags-a")
			tagged["cpu_cores"] = 16
			tagged["placement_zone"] = "west"
			server.AddDevice(netboxmock.Device{
				ID: 42, Name: "worker-01", Status: "staged",
				CustomFields: tagged, Tags: []string{"rack-a", "managed-by-osac"},
			})
			addClaimableDevice(43, new("worker-02"), "tags-b")
			// A legacy tag alone must not enroll a device.
			server.AddDevice(netboxmock.Device{
				ID: 44, Name: "worker-03", Status: "staged",
				CustomFields: map[string]any{caseFieldName: "tags-c"},
				Tags:         []string{"managed-by-osac"},
			})

			host, err := client.FindFreeHost(ctx, map[string]string{caseFieldName: "tags-a"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host.Name).To(Equal("netbox-42"))
			host, err = client.FindFreeHost(ctx, map[string]string{caseFieldName: "tags-b"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host.Name).To(Equal("netbox-43"))
			host, err = client.FindFreeHost(ctx, map[string]string{caseFieldName: "tags-c"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil(), "a legacy tag is not an enrollment signal")

			hostID := metal3Namespace + "/netbox-42"
			mark := requestMark()
			host, err = client.AssignHost(ctx, hostID, "bmi-uid-A", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(host).NotTo(BeNil())

			// The write is limited to the native status and the owner custom field.
			bodies := patchBodies(requestsSince(mark))
			Expect(bodies).To(HaveLen(1))
			Expect(bodies[0]).To(Equal(map[string]any{
				"status":        "active",
				"custom_fields": map[string]any{"osac_instance_id": "bmi-uid-A"},
			}))

			device, ok := server.Device(42)
			Expect(ok).To(BeTrue())
			Expect(device.Status).To(Equal("active"))
			Expect(device.CustomFields["osac_instance_id"]).To(Equal("bmi-uid-A"))
			Expect(device.CustomFields["osac_managed"]).To(BeTrue())
			Expect(device.CustomFields["cpu_cores"]).To(Equal(16))
			Expect(device.CustomFields["placement_zone"]).To(Equal("west"))
			Expect(device.CustomFields["osac_bmc_address"]).To(Equal(
				"redfish-virtualmedia+https://192.0.2.42/redfish/v1/Systems/1",
			))
			Expect(device.Tags).To(Equal([]string{"rack-a", "managed-by-osac"}))
			Expect(manager.created).To(HaveLen(1), "Metal3 resources are prepared after the claim")

			// A same-owner retry must not claim again.
			mark = requestMark()
			host, err = client.AssignHost(ctx, hostID, "bmi-uid-A", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(host).NotTo(BeNil())
			Expect(patchBodies(requestsSince(mark))).To(BeEmpty())

			mark = requestMark()
			Expect(client.UnassignHost(ctx, hostID, "bmi-uid-A", nil)).To(Succeed())

			release := patchBodies(requestsSince(mark))
			Expect(release).To(HaveLen(1))
			Expect(release[0]).To(Equal(map[string]any{
				"status":        "staged",
				"custom_fields": map[string]any{"osac_instance_id": nil},
			}))

			device, ok = server.Device(42)
			Expect(ok).To(BeTrue())
			Expect(device.Status).To(Equal("staged"))
			Expect(device.CustomFields["osac_instance_id"]).To(BeNil())
			Expect(device.CustomFields["cpu_cores"]).To(Equal(16), "release must not clear capabilities")
			Expect(device.Tags).To(Equal([]string{"rack-a", "managed-by-osac"}))
		})
	})

	Describe("Conditional claims and release races", func() {
		var (
			managerA  *netBoxContractBMHManager
			managerB  *netBoxContractBMHManager
			clientA   *NetBoxClient
			clientB   *NetBoxClient
			hostID    = metal3Namespace + "/netbox-42"
			winnerUID = "bmi-uid-winner"
			loserUID  = "bmi-uid-loser"
		)

		BeforeEach(func() {
			managerA = &netBoxContractBMHManager{namespace: metal3Namespace}
			managerB = &netBoxContractBMHManager{namespace: metal3Namespace}
			clientA = newContractClientWithManager(managerA)
			clientB = newContractClientWithManager(managerB)
			addClaimableDevice(42, new("worker-01"), "race")
		})

		It("lets exactly one claim win, resumes ownership, and never releases a foreign claim", func() {
			// The loser reads a staged device, then a competing claim lands before its
			// conditional PATCH. The server must answer 412; the adapter must re-read
			// ownership and return no host instead of retrying the write.
			server.SetBeforePatchHook(func(id int) {
				server.SetBeforePatchHook(nil)
				server.UpdateDevice(id, func(device *netboxmock.Device) {
					device.Status = "active"
					device.CustomFields["osac_instance_id"] = winnerUID
				})
			})

			mark := requestMark()
			host, err := clientB.AssignHost(ctx, hostID, loserUID, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil(), "the losing instance must not report ownership")

			requests := requestsSince(mark)
			Expect(requests).To(HaveLen(3), "read, conditional write, ownership re-read")
			Expect(requests[0].Method).To(Equal("GET"))
			Expect(requests[0].Path).To(Equal("/api/dcim/devices/42/"))
			Expect(requests[1].Method).To(Equal("PATCH"))
			Expect(requests[1].IfMatch).NotTo(BeEmpty(), "every claim must be conditional")
			Expect(requests[1].StatusCode).To(Equal(412), "a stale claim must be rejected")
			Expect(requests[2].Method).To(Equal("GET"))

			device, ok := server.Device(42)
			Expect(ok).To(BeTrue())
			Expect(device.CustomFields["osac_instance_id"]).To(Equal(winnerUID))
			Expect(managerB.created).To(BeEmpty(), "the loser must not create Metal3 resources")

			// The winner resumes from the persisted binding without claiming again.
			mark = requestMark()
			host, err = clientA.AssignHost(ctx, hostID, winnerUID, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(host).NotTo(BeNil())
			Expect(patchBodies(requestsSince(mark))).To(BeEmpty())

			// Releasing with the losing UID must leave the winner's claim intact.
			mark = requestMark()
			Expect(clientB.UnassignHost(ctx, hostID, loserUID, nil)).To(Succeed())
			Expect(patchBodies(requestsSince(mark))).To(BeEmpty())
			Expect(managerB.deletedBMH).To(BeEmpty(), "a foreign claim must not be torn down")
			device, _ = server.Device(42)
			Expect(device.CustomFields["osac_instance_id"]).To(Equal(winnerUID))
		})

		It("fails a stale release with 412 and preserves the administrator edit", func() {
			// The winner claims, then an administrator edits an unrelated field between
			// the release read and its conditional PATCH.
			host, err := clientA.AssignHost(ctx, hostID, winnerUID, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(host).NotTo(BeNil())

			server.SetBeforePatchHook(func(id int) {
				server.SetBeforePatchHook(nil)
				server.UpdateDevice(id, func(device *netboxmock.Device) {
					device.CustomFields["placement_zone"] = "west"
				})
			})

			mark := requestMark()
			err = clientA.UnassignHost(ctx, hostID, winnerUID, nil)
			Expect(err).To(HaveOccurred(), "a stale release must fail rather than overwrite the edit")

			requests := requestsSince(mark)
			patch := requests[len(requests)-1]
			Expect(patch.Method).To(Equal("PATCH"))
			Expect(patch.StatusCode).To(Equal(412))

			// The failed release must not have cleared the claim, and the external
			// edit must still be there for the next reconciliation.
			device, ok := server.Device(42)
			Expect(ok).To(BeTrue())
			Expect(device.Status).To(Equal("active"))
			Expect(device.CustomFields["osac_instance_id"]).To(Equal(winnerUID))
			Expect(device.CustomFields["placement_zone"]).To(Equal("west"))

			// Retrying with a fresh ETag completes the release and keeps the edit.
			Expect(clientA.UnassignHost(ctx, hostID, winnerUID, nil)).To(Succeed())
			device, _ = server.Device(42)
			Expect(device.Status).To(Equal("staged"))
			Expect(device.CustomFields["osac_instance_id"]).To(BeNil())
			Expect(device.CustomFields["placement_zone"]).To(Equal("west"))
		})
	})

	Describe("NetBox device ID identity and recovery", func() {
		var client *NetBoxClient

		BeforeEach(func() {
			client = newInventoryClient()
		})

		// expectIDDerivedHost asserts the adapter derives host identity from the
		// numeric device ID and never from the mutable device name.
		expectIDDerivedHost := func(caseValue string, wantID int) {
			host, err := client.FindFreeHost(ctx, map[string]string{caseFieldName: caseValue})
			Expect(err).NotTo(HaveOccurred(), "case %q", caseValue)
			Expect(host).NotTo(BeNil(), "case %q", caseValue)
			Expect(host.Name).To(Equal(fmt.Sprintf("netbox-%d", wantID)))
			Expect(host.InventoryHostID).To(Equal(fmt.Sprintf("%s/netbox-%d", metal3Namespace, wantID)))
		}

		It("derives netbox-<id> for named, unnamed, duplicate, and case-equivalent device names", func() {
			addEligibleDevice(42, new("worker-01"), "names-named")
			addEligibleDevice(43, nil, "names-unnamed")
			addEligibleDevice(44, new("worker-01"), "names-duplicate")
			addEligibleDevice(45, new("WORKER-01"), "names-case")

			for _, expected := range []struct {
				caseValue string
				id        int
			}{
				{"names-named", 42},
				{"names-unnamed", 43},
				{"names-duplicate", 44},
				{"names-case", 45},
			} {
				expectIDDerivedHost(expected.caseValue, expected.id)
			}
		})

		It("keeps the ID-derived identity when the device name changes or is cleared", func() {
			addEligibleDevice(42, new("worker-01"), "rename")
			expectIDDerivedHost("rename", 42)

			mark := requestMark()

			server.UpdateDevice(42, func(device *netboxmock.Device) { device.Name = "worker-renamed" })
			expectIDDerivedHost("rename", 42)

			server.UpdateDevice(42, func(device *netboxmock.Device) { device.Name = "" })
			expectIDDerivedHost("rename", 42)

			// Selection only reads; the adapter never writes the device name.
			for _, request := range requestsSince(mark) {
				Expect(request.Method).To(Equal("GET"), "selection must not write to NetBox")
				Expect(request.Path).To(HavePrefix("/api/dcim/devices"))
			}
		})

		It("never resolves a device by its raw name when a replacement reuses it", func() {
			addEligibleDevice(42, new("worker-01"), "reuse-original")
			host, err := client.FindFreeHost(ctx, map[string]string{caseFieldName: "reuse-original"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host.Name).To(Equal("netbox-42"))

			server.UpdateDevice(42, func(device *netboxmock.Device) { device.Name = "worker-renamed" })
			addEligibleDevice(99, new("worker-01"), "reuse-replacement")

			expectIDDerivedHost("reuse-original", 42)
			expectIDDerivedHost("reuse-replacement", 99)
		})

		It("keeps the saved binding on 404 and never touches a replacement device", func() {
			addEligibleDevice(42, new("worker-01"), "gone")
			host, err := client.FindFreeHost(ctx, map[string]string{caseFieldName: "gone"})
			Expect(err).NotTo(HaveOccurred())
			binding := host.InventoryHostID
			Expect(binding).To(Equal(fmt.Sprintf("%s/netbox-42", metal3Namespace)))

			server.DeleteDevice(42)
			addEligibleDevice(99, new("worker-01"), "gone-replacement")

			mark := requestMark()
			_, err = client.AssignHost(ctx, binding, "bmi-uid-contract", nil)

			Expect(err).To(HaveOccurred())
			var apiError *netboxclient.APIError
			Expect(errors.As(err, &apiError)).To(BeTrue(), "expected a classified NetBox error, got %v", err)
			Expect(apiError.Kind).To(Equal(netboxclient.ErrorKindNotFound))

			requests := requestsSince(mark)
			Expect(requests).To(HaveLen(1), "recovery must re-read only the saved device ID")
			Expect(requests[0].Method).To(Equal("GET"))
			Expect(requests[0].Path).To(Equal("/api/dcim/devices/42/"),
				"a 404 must never fall back to a replacement device")
		})
	})

	Describe("NetBox metadata discovery and pool default", func() {
		var client *NetBoxClient

		BeforeEach(func() {
			client = newInventoryClient()
		})

		It("excludes a device whose pool flag is omitted and selects the managed one", func() {
			// 42 models a device an administrator created without setting the pool
			// flag: the custom field is absent, which is not an explicit true.
			server.AddDevice(netboxmock.Device{
				ID:           42,
				Name:         "worker-01",
				Status:       "staged",
				CustomFields: map[string]any{caseFieldName: "pool-off"},
			})
			addEligibleDevice(43, new("worker-02"), "pool-on")

			host, err := client.FindFreeHost(ctx, map[string]string{caseFieldName: "pool-off"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil(), "an omitted pool flag must not enroll a device")

			host, err = client.FindFreeHost(ctx, map[string]string{caseFieldName: "pool-on"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).NotTo(BeNil())
			Expect(host.Name).To(Equal("netbox-43"))

			Expect(server.Requests()).NotTo(BeEmpty())
			last := server.Requests()[len(server.Requests())-1]
			Expect(last.Query.Get("cf_osac_managed")).To(Equal("true"),
				"the adapter must always require an explicit pool flag")
		})

	})

	Describe("Capability added after startup", func() {
		var client *NetBoxClient

		BeforeEach(func() {
			// Build the adapter before adding the capability.
			client = newInventoryClient()
		})

		It("matches a capability created after the adapter was built, without restart", func() {
			device := eligibleFields("late")
			device["placement_zone"] = "west"
			server.AddDevice(netboxmock.Device{ID: 42, Name: "worker-01", Status: "staged", CustomFields: device})

			addExactField("placement_zone", "text")
			host, err := client.FindFreeHost(ctx, map[string]string{caseFieldName: "late", "placement_zone": "west"})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).NotTo(BeNil())
			Expect(host.Name).To(Equal("netbox-42"))
		})
	})

	Describe("Exact selection across distinct fields", func() {
		var client *NetBoxClient

		BeforeEach(func() {
			client = newInventoryClient()
			addExactField("cpu_cores", "integer")
			addExactField("gpu_model", "text")
			addExactField("memory_gb", "integer")
		})

		addDevice := func(id int, mutate func(map[string]any)) {
			fields := eligibleFields("and")
			fields["cpu_cores"] = 16
			fields["gpu_model"] = "a100"
			fields["memory_gb"] = 128
			mutate(fields)
			server.AddDevice(netboxmock.Device{ID: id, Name: "worker", Status: "staged", CustomFields: fields})
		}

		It("selects only the device matching every capability and fixed predicate", func() {
			addDevice(42, func(map[string]any) {})                                 // exact match
			addDevice(43, func(fields map[string]any) { fields["cpu_cores"] = 8 }) // differs
			addDevice(44, func(fields map[string]any) { fields["gpu_model"] = "h100" })
			addDevice(45, func(fields map[string]any) { fields["memory_gb"] = 64 })
			addDevice(46, func(fields map[string]any) { fields["osac_managed"] = false })
			addDevice(47, func(fields map[string]any) { fields["osac_instance_id"] = "other-owner" })
			// 48 differs only by native status, which is not a capability.
			server.AddDevice(netboxmock.Device{
				ID: 48, Name: "worker", Status: "active",
				CustomFields: map[string]any{
					"osac_managed": true, caseFieldName: "and",
					"cpu_cores": 16, "gpu_model": "a100", "memory_gb": 128,
				},
			})

			mark := requestMark()
			host, err := client.FindFreeHost(ctx, map[string]string{
				"cpu_cores": "16", "gpu_model": "a100", "memory_gb": "128",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).NotTo(BeNil())
			Expect(host.Name).To(Equal("netbox-42"))

			// The adapter sends every capability plus the fixed predicates as
			// separate exact filters and then takes the server's first result.
			requests := requestsSince(mark)
			Expect(requests).To(HaveLen(1))
			query := requests[0].Query
			Expect(query.Get("status")).To(Equal("staged"))
			Expect(query.Get("cf_osac_managed")).To(Equal("true"))
			Expect(query.Get("cf_osac_instance_id__empty")).To(Equal("true"))
			Expect(query.Get("cf_cpu_cores")).To(Equal("16"))
			Expect(query.Get("cf_gpu_model")).To(Equal("a100"))
			Expect(query.Get("cf_memory_gb")).To(Equal("128"))
		})

		It("claims no partial match when the matching device is gone", func() {
			addDevice(43, func(fields map[string]any) { fields["cpu_cores"] = 8 })
			addDevice(44, func(fields map[string]any) { fields["gpu_model"] = "h100" })

			host, err := client.FindFreeHost(ctx, map[string]string{
				"cpu_cores": "16", "gpu_model": "a100", "memory_gb": "128",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(host).To(BeNil(), "no partial-match device may be claimed")
		})

		It("cannot widen the fixed pool predicate from the selector", func() {
			addDevice(42, func(map[string]any) {})
			mark := requestMark()

			host, err := client.FindFreeHost(ctx, map[string]string{"osac_managed": "false"})
			Expect(err).NotTo(HaveOccurred())
			// The device is still eligible: only the fixed predicate is sent, so the
			// selector cannot exclude managed devices either.
			Expect(host).NotTo(BeNil())
			Expect(host.Name).To(Equal("netbox-42"))
			Expect(requestsSince(mark)[0].Query.Get("cf_osac_managed")).To(Equal("true"),
				"the fixed managed predicate must win over the selector")
		})
	})

	Describe("Empty owner representations", func() {
		var client *NetBoxClient

		BeforeEach(func() {
			client = newInventoryClient()
		})

		addWithOwner := func(id int, caseValue string, owner any, includeOwner bool) {
			fields := eligibleFields(caseValue)
			if includeOwner {
				fields["osac_instance_id"] = owner
			}
			server.AddDevice(netboxmock.Device{ID: id, Name: "worker", Status: "staged", CustomFields: fields})
		}

		It("treats an absent owner and an empty-string owner as unassigned", func() {
			addWithOwner(42, "absent", nil, false)
			addWithOwner(43, "empty", "", true)

			for _, expected := range []struct {
				caseValue string
				id        int
			}{{"absent", 42}, {"empty", 43}} {
				host, err := client.FindFreeHost(ctx, map[string]string{caseFieldName: expected.caseValue})
				Expect(err).NotTo(HaveOccurred())
				Expect(host).NotTo(BeNil())
				Expect(host.Name).To(Equal("netbox-" + strconv.Itoa(expected.id)))
			}
		})

		It("excludes populated and whitespace-only owners and never claims them", func() {
			addWithOwner(44, "owned", "another-instance", true)
			addWithOwner(45, "whitespace", "   ", true)

			for _, caseValue := range []string{"owned", "whitespace"} {
				host, err := client.FindFreeHost(ctx, map[string]string{caseFieldName: caseValue})
				Expect(err).NotTo(HaveOccurred())
				Expect(host).To(BeNil(), "case %q must not be selectable", caseValue)
			}

			// If a malformed owner is ever reached directly, the adapter rejects it
			// instead of adopting the device under a bogus owner.
			mark := requestMark()
			_, err := client.AssignHost(ctx, metal3Namespace+"/netbox-45", "bmi-uid-contract", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("malformed"))
			for _, request := range requestsSince(mark) {
				Expect(request.Method).To(Equal("GET"), "a malformed owner must not be overwritten")
			}
		})
	})

	Describe("Scalar filtering and stored choice values", func() {
		var client *NetBoxClient

		BeforeEach(func() {
			client = newInventoryClient()
			addExactField("cpu_cores", "integer")
			addExactField("accelerated", "boolean")
			addExactField("placement_zone", "text")
			server.AddField(netboxmock.Field{
				Name: "accelerator", Label: "NVIDIA A100", Type: "select", FilterLogic: netboxmock.FilterExact,
			})
		})

		addWithCapability := func(id int, name string, value any) {
			fields := eligibleFields("scalars")
			fields[name] = value
			server.AddDevice(netboxmock.Device{ID: id, Name: "worker", Status: "staged", CustomFields: fields})
		}

		expectSelection := func(selector map[string]string, wantName string) {
			host, err := client.FindFreeHost(ctx, selector)
			Expect(err).NotTo(HaveOccurred(), "selector %v", selector)
			if wantName == "" {
				Expect(host).To(BeNil(), "selector %v must match nothing", selector)
				return
			}
			Expect(host).NotTo(BeNil(), "selector %v", selector)
			Expect(host.Name).To(Equal(wantName), "selector %v", selector)
		}

		It("matches exact typed values, including zero, negatives, and booleans", func() {
			addWithCapability(42, "cpu_cores", 0)
			addWithCapability(43, "cpu_cores", -8)
			addWithCapability(44, "cpu_cores", int64(9007199254740993))
			addWithCapability(45, "accelerated", true)
			addWithCapability(46, "accelerated", false)
			addWithCapability(47, "placement_zone", "Lab A")
			addWithCapability(48, "placement_zone", "lab a")
			addWithCapability(49, "accelerator", "a100")

			expectSelection(map[string]string{"cpu_cores": "0"}, "netbox-42")
			expectSelection(map[string]string{"cpu_cores": "-8"}, "netbox-43")
			expectSelection(map[string]string{"cpu_cores": "9007199254740993"}, "netbox-44")
			expectSelection(map[string]string{"accelerated": "true"}, "netbox-45")
			expectSelection(map[string]string{"accelerated": "false"}, "netbox-46")
			expectSelection(map[string]string{"placement_zone": "Lab A"}, "netbox-47")
			// No trimming and no case folding: the near-match is not selected.
			expectSelection(map[string]string{"placement_zone": "lab a"}, "netbox-48")
			expectSelection(map[string]string{"placement_zone": " Lab A"}, "")
			expectSelection(map[string]string{"placement_zone": "lab A"}, "")
		})

		It("filters select fields by stored value and never by display label", func() {
			addWithCapability(49, "accelerator", "a100")
			mark := requestMark()

			expectSelection(map[string]string{"accelerator": "a100"}, "netbox-49")
			Expect(requestsSince(mark)[0].Query["cf_accelerator"]).To(Equal([]string{"a100"}),
				"the stored value must be sent, never the label")

			expectSelection(map[string]string{"accelerator": "NVIDIA A100"}, "")

			// Changing only the choice label must not change matching.
			server.AddField(netboxmock.Field{
				Name: "accelerator", Label: "NVIDIA A100 80GB", Type: "select", FilterLogic: netboxmock.FilterExact,
			})
			mark = requestMark()
			expectSelection(map[string]string{"accelerator": "a100"}, "netbox-49")
			Expect(requestsSince(mark)[0].Query["cf_accelerator"]).To(Equal([]string{"a100"}))
		})

		It("does not match a device whose capability is missing or null", func() {
			addWithCapability(50, "accelerated", nil)
			expectSelection(map[string]string{"accelerated": "false"}, "")
		})
	})

	Describe("Exact escaped string filtering", func() {
		var client *NetBoxClient

		BeforeEach(func() {
			client = newInventoryClient()
			addExactField("placement_zone", "text")
		})

		It("keeps every stored string in one custom-field parameter", func() {
			zones := []string{"&status=active", "a+b", "x=y", "אזור-מערב", "Lab A", "active"}
			for index, zone := range zones {
				fields := eligibleFields("escaped")
				fields["placement_zone"] = zone
				server.AddDevice(netboxmock.Device{
					ID: 42 + index, Name: "worker", Status: "staged", CustomFields: fields,
				})
			}

			for index, zone := range zones {
				mark := requestMark()
				host, err := client.FindFreeHost(ctx, map[string]string{"placement_zone": zone})
				Expect(err).NotTo(HaveOccurred(), "zone %q", zone)
				Expect(host).NotTo(BeNil(), "zone %q", zone)
				Expect(host.Name).To(Equal("netbox-"+strconv.Itoa(42+index)), "zone %q", zone)

				request := requestsSince(mark)[0]
				Expect(request.Query["cf_placement_zone"]).To(Equal([]string{zone}),
					"the value must survive encoding unchanged")
				Expect(request.Query["status"]).To(Equal([]string{"staged"}),
					"an embedded status= fragment must not become a second predicate")
			}
		})
	})
})

// netBoxContractBMHManager records the Metal3 side effects the adapter requests.
type netBoxContractBMHManager struct {
	namespace  string
	mutex      sync.Mutex
	created    []baremetalhost.CreateParams
	deletedBMH []string
}

func (m *netBoxContractBMHManager) Namespace() string { return m.namespace }

func (m *netBoxContractBMHManager) CreateBMH(_ context.Context, params baremetalhost.CreateParams) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.created = append(m.created, params)
	return nil
}

func (m *netBoxContractBMHManager) DeleteBMH(_ context.Context, name, _ string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.deletedBMH = append(m.deletedBMH, name)
	return nil
}

func (m *netBoxContractBMHManager) BMHExists(context.Context, string) (bool, error) {
	return false, nil
}

func (m *netBoxContractBMHManager) IsBMHAvailable(context.Context, string) (bool, error) {
	return true, nil
}

func (m *netBoxContractBMHManager) EnsureBMCSecret(context.Context, string, string, string, string) error {
	return nil
}

func (m *netBoxContractBMHManager) DeleteBMCSecret(context.Context, string, string) error { return nil }

func (m *netBoxContractBMHManager) GetHardwareNICs(context.Context, string) ([]string, error) {
	return []string{"aa:bb:cc:dd:ee:42"}, nil
}
