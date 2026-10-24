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

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/netboxclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/shared"
)

const testNetBoxHostClass = "metal3"

var _ = Describe("NetBox inventory client", func() {
	Describe("GetHostLogicalPortMACs", func() {
		It("returns a not-implemented error", func() {
			macs, err := (&NetBoxClient{}).GetHostLogicalPortMACs(context.Background(), "baremetal/netbox-42")

			Expect(errors.Is(err, errNetBoxClientNotImplemented)).To(BeTrue())
			Expect(macs).To(BeNil())
		})
	})

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
			client := NewNetBoxClient(api, manager, testNetBoxHostClass)

			Expect(client.api).To(BeIdenticalTo(api))
			Expect(client.bmhManager).To(BeIdenticalTo(manager))
			Expect(client.bmhManager.Namespace()).To(Equal("metal3-system"))
			Expect(client.hostClass).To(Equal(testNetBoxHostClass))
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

	Describe("stub methods", func() {
		It("return not-implemented errors", func() {
			client := &NetBoxClient{}
			ctx := context.Background()

			err := client.UnassignHost(ctx, "baremetal/netbox-42", []string{"192.0.2.42"})
			Expect(errors.Is(err, errNetBoxClientNotImplemented)).To(BeTrue())

			nics, err := client.GetHostNICs(ctx, "baremetal/netbox-42")
			Expect(nics).To(BeNil())
			Expect(errors.Is(err, errNetBoxClientNotImplemented)).To(BeTrue())
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
	patchCalls       int
	patchDeviceID    string
	patch            netboxclient.DevicePatch
	patchETag        string
	patchSnapshot    netboxclient.DeviceSnapshot
	patchErr         error
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

func (a *selectorQueryTestAPI) GetDevice(_ context.Context, id string) (netboxclient.DeviceSnapshot, error) {
	a.deviceReadCalls++
	a.deviceReadIDs = append(a.deviceReadIDs, id)
	if index := a.deviceReadCalls - 1; index < len(a.deviceSnapshots) {
		return a.deviceSnapshots[index], nil
	}
	return a.deviceSnapshot, nil
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

var _ = Describe("NetBox selector query", func() {
	Describe("FindFreeHost", func() {
		It("returns an error when the Metal3 namespace is missing without querying NetBox", func() {
			api := &selectorQueryTestAPI{}
			client := NewNetBoxClient(api, baremetalhost.NewManager(nil, nil, ""), testNetBoxHostClass)

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
			client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass)
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
			client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass)

			_, err := client.FindFreeHost(context.Background(), map[string]string{"osac_managed": "false"})

			Expect(err).NotTo(HaveOccurred())
			Expect(api.deviceQueries).To(Equal(1))
			Expect(api.deviceQuery["cf_osac_managed"]).To(Equal("true"))
		})

		It("queries the fixed free-host filters when the selector is empty", func() {
			api := &selectorQueryTestAPI{}
			client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass)

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
			client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass)

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
		client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass)

		host, err := client.AssignHost(context.Background(), "metal3-system/netbox-46", "bmi-123", nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(host).To(Equal(expectedTestNetBoxAssignedHost("bmi-123")))
		Expect(api.patchCalls).To(Equal(1))
	})

	It("does not claim a device that became active without an owner", func() {
		api := &selectorQueryTestAPI{
			deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, ""),
		}
		client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass)

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
		client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass)

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
	})

	It("returns an existing assignment without patching the device", func() {
		api := &selectorQueryTestAPI{
			deviceSnapshot: testNetBoxAssignmentSnapshot(netBoxStatusActive, "bmi-123"),
		}
		client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass)

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
		client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass)

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
		client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass)

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
		client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass)

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
			client := NewNetBoxClient(api, testNetBoxSelectorBMHManager(), testNetBoxHostClass)

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

func testNetBoxAssignmentSnapshot(status, instanceID string) netboxclient.DeviceSnapshot {
	return netboxclient.DeviceSnapshot{
		Device: netboxclient.Device{
			ID:     "46",
			Status: status,
			CustomFields: map[string]interface{}{
				"osac_managed":     true,
				"osac_instance_id": instanceID,
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
