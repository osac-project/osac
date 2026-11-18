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
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/inventory"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/netboxclient"
	"github.com/osac-project/osac/bare-metal-fulfillment-operator/test/netboxcontract/netboxtest"
)

var _ = Describe("TC-INT-029 Community ID identity and numeric-ID recovery", func() {
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
