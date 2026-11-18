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

var _ = Describe("TC-INT-020 tags are independent and administrator metadata survives", func() {
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

var _ = Describe("TC-INT-021 conditional claims and release races", func() {
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
