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

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/test/netboxcontract/netboxtest"
)

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
