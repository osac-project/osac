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
	"strconv"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/test/netboxcontract/netboxtest"
)

var _ = Describe("TC-INT-016 exact AND semantics across distinct fields", func() {
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

var _ = Describe("TC-INT-017 empty-owner representations", func() {
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

var _ = Describe("TC-INT-019 scalar filtering and stored choice values", func() {
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

var _ = Describe("TC-INT-022 exact escaped string filtering", func() {
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
