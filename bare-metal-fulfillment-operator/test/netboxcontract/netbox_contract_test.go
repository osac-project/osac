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

package netboxcontract

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/baremetalhost"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/credentials"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/netboxclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/test/netboxcontract/netboxtest"
)

const (
	// metal3Namespace is the namespace the adapter derives host IDs from.
	metal3Namespace = "metal3-system"
	// hostClass is the only host class the NetBox adapter accepts.
	hostClass = "metal3"
	// caseFieldName isolates contract cases from one another: every case gives
	// its devices a unique value so one search can only return that case's set.
	caseFieldName = "contract_case"
	// testToken is a v2-shaped token, so the adapter must send it as a Bearer
	// credential rather than a v1 Token header.
	testToken = "nbt_aaaaaaaaaaaa.bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

var (
	server *netboxtest.Server
	caFile string
)

func TestNetBoxContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NetBox API contract suite")
}

var _ = BeforeEach(func() {
	server = netboxtest.New()
	DeferCleanup(server.Stop)
	server.SetAuth("Bearer", testToken)
	for _, field := range contractFields() {
		server.AddField(field)
	}
	caFile = writeCAFile(server.Certificate())
})

// writeCAFile materializes the mock server certificate so the client exercises
// its real TLS and CA-file path instead of skipping verification.
func writeCAFile(certificate *x509.Certificate) string {
	path := filepath.Join(GinkgoT().TempDir(), "netbox-ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	Expect(os.WriteFile(path, caPEM, 0o600)).To(Succeed())
	return path
}

func writeTokenFile() string {
	path := filepath.Join(GinkgoT().TempDir(), "netbox-token")
	Expect(os.WriteFile(path, []byte(testToken+"\n"), 0o600)).To(Succeed())
	return path
}

// newAPIClient builds the production NetBox client against the mock server.
func newAPIClient() *netboxclient.Client {
	client, err := netboxclient.New(netboxclient.Config{
		Endpoint:  server.URL(),
		TokenFile: writeTokenFile(),
		CAFile:    caFile,
	})
	Expect(err).NotTo(HaveOccurred())
	return client
}

// newInventoryClient builds the inventory adapter under test. Credential
// resolution is omitted: selection and identity cases never reach the OSAC
// Secret API, and the stub fails the spec if they try.
func newInventoryClient() *inventory.NetBoxClient {
	return inventory.NewNetBoxClient(
		newAPIClient(), stubBMHManager{namespace: metal3Namespace}, noOpCredentialsWriter{}, hostClass, nil,
	)
}

type noOpCredentialsWriter struct{}

func (noOpCredentialsWriter) Create(context.Context, credentials.Reference, credentials.BMC) error {
	return nil
}

func (noOpCredentialsWriter) Delete(context.Context, credentials.Reference) error { return nil }

func contractContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// requestMark records how many requests the server has handled, so a case can
// assert exactly what the adapter sent.
func requestMark() int { return len(server.Requests()) }

// requestsSince returns the adapter requests recorded after mark.
func requestsSince(mark int) []netboxtest.Request {
	all := server.Requests()
	if mark > len(all) {
		return nil
	}
	return all[mark:]
}

// patchBodies returns the decoded body of every recorded PATCH.
func patchBodies(requests []netboxtest.Request) []map[string]any {
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

// stubBMHManager satisfies the BMH lifecycle dependency for cases that never
// reach Metal3. Every call fails the spec if it is unexpectedly made.
type stubBMHManager struct {
	namespace string
}

func (s stubBMHManager) Namespace() string { return s.namespace }

func (s stubBMHManager) CreateBMH(context.Context, baremetalhost.CreateParams) error {
	Fail("CreateBMH must not be called by this contract case")
	return nil
}

func (s stubBMHManager) DeleteBMH(context.Context, string, string) error {
	Fail("DeleteBMH must not be called by this contract case")
	return nil
}

func (s stubBMHManager) BMHExists(context.Context, string) (bool, error) {
	Fail("BMHExists must not be called by this contract case")
	return false, nil
}

func (s stubBMHManager) IsBMHAvailable(context.Context, string) (bool, error) {
	Fail("IsBMHAvailable must not be called by this contract case")
	return false, nil
}

func (s stubBMHManager) EnsureBMCSecret(context.Context, string, string, string, string) error {
	Fail("EnsureBMCSecret must not be called by this contract case")
	return nil
}

func (s stubBMHManager) DeleteBMCSecret(context.Context, string, string) error {
	Fail("DeleteBMCSecret must not be called by this contract case")
	return nil
}

func (s stubBMHManager) GetHardwareNICs(context.Context, string) ([]string, error) {
	Fail("GetHardwareNICs must not be called by this contract case")
	return nil, nil
}

var _ = Describe("Independent tags and preserved administrator metadata", func() {
	var (
		ctx     context.Context
		cancel  context.CancelFunc
		manager *recordingBMHManager
		client  *inventory.NetBoxClient
	)

	BeforeEach(func() {
		ctx, cancel = contractContext()
		manager = &recordingBMHManager{namespace: metal3Namespace}
		client = inventory.NewNetBoxClient(
			newAPIClient(), manager, noOpCredentialsWriter{}, hostClass, fakeCredentialsReader{},
		)
		addExactField("cpu_cores", "integer")
		addExactField("placement_zone", "text")
	})

	AfterEach(func() {
		cancel()
	})

	It("selects on capabilities only, then preserves every other field across claim and release", func() {
		tagged := claimableFields("tags-a")
		tagged["cpu_cores"] = 16
		tagged["placement_zone"] = "west"
		server.AddDevice(netboxtest.Device{
			ID: 42, Name: "worker-01", Status: "staged",
			CustomFields: tagged, Tags: []string{"rack-a", "managed-by-osac"},
		})
		addClaimableDevice(43, strPtr("worker-02"), "tags-b")
		// A legacy tag alone must not enroll a device.
		server.AddDevice(netboxtest.Device{
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

var _ = Describe("Conditional claims and release races", func() {
	var (
		ctx       context.Context
		cancel    context.CancelFunc
		managerA  *recordingBMHManager
		managerB  *recordingBMHManager
		clientA   *inventory.NetBoxClient
		clientB   *inventory.NetBoxClient
		hostID    = metal3Namespace + "/netbox-42"
		winnerUID = "bmi-uid-winner"
		loserUID  = "bmi-uid-loser"
	)

	BeforeEach(func() {
		ctx, cancel = contractContext()
		managerA = &recordingBMHManager{namespace: metal3Namespace}
		managerB = &recordingBMHManager{namespace: metal3Namespace}
		clientA = inventory.NewNetBoxClient(
			newAPIClient(), managerA, noOpCredentialsWriter{}, hostClass, fakeCredentialsReader{},
		)
		clientB = inventory.NewNetBoxClient(
			newAPIClient(), managerB, noOpCredentialsWriter{}, hostClass, fakeCredentialsReader{},
		)
		addClaimableDevice(42, strPtr("worker-01"), "race")
	})

	AfterEach(func() {
		cancel()
	})

	It("lets exactly one claim win, resumes ownership, and never releases a foreign claim", func() {
		// The loser reads a staged device, then a competing claim lands before its
		// conditional PATCH. The server must answer 412; the adapter must re-read
		// ownership and return no host instead of retrying the write.
		server.SetBeforePatchHook(func(id int) {
			server.SetBeforePatchHook(nil)
			server.UpdateDevice(id, func(device *netboxtest.Device) {
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
			server.UpdateDevice(id, func(device *netboxtest.Device) {
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

var _ = Describe("NetBox device ID identity and recovery", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		client *inventory.NetBoxClient
	)

	BeforeEach(func() {
		ctx, cancel = contractContext()
		client = newInventoryClient()
	})

	AfterEach(func() {
		cancel()
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
		addEligibleDevice(42, strPtr("worker-01"), "names-named")
		addEligibleDevice(43, nil, "names-unnamed")
		addEligibleDevice(44, strPtr("worker-01"), "names-duplicate")
		addEligibleDevice(45, strPtr("WORKER-01"), "names-case")

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
		addEligibleDevice(42, strPtr("worker-01"), "rename")
		expectIDDerivedHost("rename", 42)

		mark := requestMark()

		server.UpdateDevice(42, func(device *netboxtest.Device) { device.Name = "worker-renamed" })
		expectIDDerivedHost("rename", 42)

		server.UpdateDevice(42, func(device *netboxtest.Device) { device.Name = "" })
		expectIDDerivedHost("rename", 42)

		// Selection only reads; the adapter never writes the device name.
		for _, request := range requestsSince(mark) {
			Expect(request.Method).To(Equal("GET"), "selection must not write to NetBox")
			Expect(request.Path).To(HavePrefix("/api/dcim/devices"))
		}
	})

	It("never resolves a device by its raw name when a replacement reuses it", func() {
		addEligibleDevice(42, strPtr("worker-01"), "reuse-original")
		host, err := client.FindFreeHost(ctx, map[string]string{caseFieldName: "reuse-original"})
		Expect(err).NotTo(HaveOccurred())
		Expect(host.Name).To(Equal("netbox-42"))

		server.UpdateDevice(42, func(device *netboxtest.Device) { device.Name = "worker-renamed" })
		addEligibleDevice(99, strPtr("worker-01"), "reuse-replacement")

		expectIDDerivedHost("reuse-original", 42)
		expectIDDerivedHost("reuse-replacement", 99)
	})

	It("keeps the saved binding on 404 and never touches a replacement device", func() {
		addEligibleDevice(42, strPtr("worker-01"), "gone")
		host, err := client.FindFreeHost(ctx, map[string]string{caseFieldName: "gone"})
		Expect(err).NotTo(HaveOccurred())
		binding := host.InventoryHostID
		Expect(binding).To(Equal(fmt.Sprintf("%s/netbox-42", metal3Namespace)))

		server.DeleteDevice(42)
		addEligibleDevice(99, strPtr("worker-01"), "gone-replacement")

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

var _ = Describe("NetBox metadata discovery and pool default", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		client *inventory.NetBoxClient
	)

	BeforeEach(func() {
		ctx, cancel = contractContext()
		client = newInventoryClient()
	})

	AfterEach(func() {
		cancel()
	})

	It("excludes a device whose pool flag is omitted and selects the managed one", func() {
		// 42 models a device an administrator created without setting the pool
		// flag: the custom field is absent, which is not an explicit true.
		server.AddDevice(netboxtest.Device{
			ID:           42,
			Name:         "worker-01",
			Status:       "staged",
			CustomFields: map[string]any{caseFieldName: "pool-off"},
		})
		addEligibleDevice(43, strPtr("worker-02"), "pool-on")

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

var _ = Describe("Capability added after startup", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		client *inventory.NetBoxClient
	)

	BeforeEach(func() {
		ctx, cancel = contractContext()
		// Build the adapter before adding the capability.
		client = newInventoryClient()
	})

	AfterEach(func() {
		cancel()
	})

	It("matches a capability created after the adapter was built, without restart", func() {
		device := eligibleFields("late")
		device["placement_zone"] = "west"
		server.AddDevice(netboxtest.Device{ID: 42, Name: "worker-01", Status: "staged", CustomFields: device})

		addExactField("placement_zone", "text")
		host, err := client.FindFreeHost(ctx, map[string]string{caseFieldName: "late", "placement_zone": "west"})
		Expect(err).NotTo(HaveOccurred())
		Expect(host).NotTo(BeNil())
		Expect(host.Name).To(Equal("netbox-42"))
	})
})

var _ = Describe("Exact selection across distinct fields", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		client *inventory.NetBoxClient
	)

	BeforeEach(func() {
		ctx, cancel = contractContext()
		client = newInventoryClient()
		addExactField("cpu_cores", "integer")
		addExactField("gpu_model", "text")
		addExactField("memory_gb", "integer")
	})

	AfterEach(func() {
		cancel()
	})

	addDevice := func(id int, mutate func(map[string]any)) {
		fields := eligibleFields("and")
		fields["cpu_cores"] = 16
		fields["gpu_model"] = "a100"
		fields["memory_gb"] = 128
		mutate(fields)
		server.AddDevice(netboxtest.Device{ID: id, Name: "worker", Status: "staged", CustomFields: fields})
	}

	It("selects only the device matching every capability and fixed predicate", func() {
		addDevice(42, func(map[string]any) {})                                 // exact match
		addDevice(43, func(fields map[string]any) { fields["cpu_cores"] = 8 }) // differs
		addDevice(44, func(fields map[string]any) { fields["gpu_model"] = "h100" })
		addDevice(45, func(fields map[string]any) { fields["memory_gb"] = 64 })
		addDevice(46, func(fields map[string]any) { fields["osac_managed"] = false })
		addDevice(47, func(fields map[string]any) { fields["osac_instance_id"] = "other-owner" })
		// 48 differs only by native status, which is not a capability.
		server.AddDevice(netboxtest.Device{
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

var _ = Describe("Empty owner representations", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		client *inventory.NetBoxClient
	)

	BeforeEach(func() {
		ctx, cancel = contractContext()
		client = newInventoryClient()
	})

	AfterEach(func() {
		cancel()
	})

	addWithOwner := func(id int, caseValue string, owner any, includeOwner bool) {
		fields := eligibleFields(caseValue)
		if includeOwner {
			fields["osac_instance_id"] = owner
		}
		server.AddDevice(netboxtest.Device{ID: id, Name: "worker", Status: "staged", CustomFields: fields})
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

var _ = Describe("Scalar filtering and stored choice values", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		client *inventory.NetBoxClient
	)

	BeforeEach(func() {
		ctx, cancel = contractContext()
		client = newInventoryClient()
		addExactField("cpu_cores", "integer")
		addExactField("accelerated", "boolean")
		addExactField("placement_zone", "text")
		server.AddField(netboxtest.Field{
			Name: "accelerator", Label: "NVIDIA A100", Type: "select", FilterLogic: netboxtest.FilterExact,
		})
	})

	AfterEach(func() {
		cancel()
	})

	addWithCapability := func(id int, name string, value any) {
		fields := eligibleFields("scalars")
		fields[name] = value
		server.AddDevice(netboxtest.Device{ID: id, Name: "worker", Status: "staged", CustomFields: fields})
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
		server.AddField(netboxtest.Field{
			Name: "accelerator", Label: "NVIDIA A100 80GB", Type: "select", FilterLogic: netboxtest.FilterExact,
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

var _ = Describe("Exact escaped string filtering", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		client *inventory.NetBoxClient
	)

	BeforeEach(func() {
		ctx, cancel = contractContext()
		client = newInventoryClient()
		addExactField("placement_zone", "text")
	})

	AfterEach(func() {
		cancel()
	})

	It("keeps every stored string in one custom-field parameter", func() {
		zones := []string{"&status=active", "a+b", "x=y", "אזור-מערב", "Lab A", "active"}
		for index, zone := range zones {
			fields := eligibleFields("escaped")
			fields["placement_zone"] = zone
			server.AddDevice(netboxtest.Device{
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

// contractFields are the custom-field definitions the adapter's fixed filters
// require. Every one is exact-filtered, which is the schema the adapter assumes
// an administrator has provisioned.
func contractFields() []netboxtest.Field {
	return []netboxtest.Field{
		{Name: "osac_managed", Label: "OSAC managed", Type: "boolean", FilterLogic: netboxtest.FilterExact},
		{Name: "osac_instance_id", Label: "OSAC instance ID", Type: "text", FilterLogic: netboxtest.FilterExact},
		{Name: "osac_bmc_address", Label: "OSAC BMC address", Type: "text", FilterLogic: netboxtest.FilterExact},
		{Name: "osac_boot_mac", Label: "OSAC boot MAC", Type: "text", FilterLogic: netboxtest.FilterExact},
		{Name: "osac_interface_macs", Label: "OSAC interface MACs", Type: "json", FilterLogic: netboxtest.FilterExact},
		{Name: caseFieldName, Label: "Contract case", Type: "text", FilterLogic: netboxtest.FilterExact},
	}
}

// eligibleFields marks a device as an enrolled, unowned pool member for one
// case. osac_instance_id is deliberately absent, which is the representation an
// administrator-created device has.
func eligibleFields(caseValue string) map[string]any {
	return map[string]any{
		"osac_managed": true,
		caseFieldName:  caseValue,
	}
}

// addEligibleDevice registers a staged, managed, unowned device. A nil name
// models a device an administrator created without a name.
func addEligibleDevice(id int, name *string, caseValue string) {
	deviceName := ""
	if name != nil {
		deviceName = *name
	}
	server.AddDevice(netboxtest.Device{
		ID:           id,
		Name:         deviceName,
		Status:       "staged",
		CustomFields: eligibleFields(caseValue),
	})
}

func strPtr(value string) *string { return &value }

// addExactField registers an additional exact-filtered capability field.
func addExactField(name, fieldType string) {
	server.AddField(netboxtest.Field{
		Name:        name,
		Label:       "Contract " + name,
		Type:        fieldType,
		FilterLogic: netboxtest.FilterExact,
	})
}

// claimableFields returns the custom fields a device needs before the adapter
// can resolve Metal3 connection data and credentials for it.
func claimableFields(caseValue string) map[string]any {
	fields := eligibleFields(caseValue)
	fields["osac_bmc_address"] = "redfish-virtualmedia+https://192.0.2.42/redfish/v1/Systems/1"
	fields["osac_boot_mac"] = "aa:bb:cc:dd:ee:42"
	return fields
}

// addClaimableDevice registers a staged, managed, unowned device that carries
// valid BMC connection metadata.
func addClaimableDevice(id int, name *string, caseValue string) {
	deviceName := ""
	if name != nil {
		deviceName = *name
	}
	server.AddDevice(netboxtest.Device{
		ID:           id,
		Name:         deviceName,
		Status:       "staged",
		CustomFields: claimableFields(caseValue),
	})
}

// fakeCredentialsReader returns fixed BMC credentials without contacting the
// OSAC Secret API.
type fakeCredentialsReader struct{}

func (fakeCredentialsReader) GetCredentials(context.Context, string) (*credentials.BMC, error) {
	return &credentials.BMC{Username: []byte("bmc-user"), Password: []byte("bmc-pass")}, nil
}

func (fakeCredentialsReader) Close() error { return nil }

// recordingBMHManager records the Metal3 side effects the adapter requests.
type recordingBMHManager struct {
	namespace  string
	mutex      sync.Mutex
	secrets    []string
	created    []baremetalhost.CreateParams
	deletedBMH []string
}

func (m *recordingBMHManager) Namespace() string { return m.namespace }

func (m *recordingBMHManager) CreateBMH(_ context.Context, params baremetalhost.CreateParams) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.created = append(m.created, params)
	return nil
}

func (m *recordingBMHManager) DeleteBMH(_ context.Context, name, _ string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.deletedBMH = append(m.deletedBMH, name)
	return nil
}

func (m *recordingBMHManager) BMHExists(context.Context, string) (bool, error) { return false, nil }

func (m *recordingBMHManager) IsBMHAvailable(context.Context, string) (bool, error) { return true, nil }

func (m *recordingBMHManager) EnsureBMCSecret(_ context.Context, name, _, _, _ string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.secrets = append(m.secrets, name)
	return nil
}

func (m *recordingBMHManager) DeleteBMCSecret(context.Context, string, string) error { return nil }

func (m *recordingBMHManager) GetHardwareNICs(context.Context, string) ([]string, error) {
	return []string{"aa:bb:cc:dd:ee:42"}, nil
}
