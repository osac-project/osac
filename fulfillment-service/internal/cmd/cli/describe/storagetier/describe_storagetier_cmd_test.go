/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package storagetier

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
)

func formatStorageTier(st *publicv1.StorageTier) string {
	var buf bytes.Buffer
	renderStorageTier(&buf, st)
	return buf.String()
}

var _ = Describe("Rendering tests", func() {
	It("should display all fields when set", func() {
		msg := "All backends healthy"
		st := publicv1.StorageTier_builder{
			Id: "tier-001",
			Metadata: publicv1.Metadata_builder{
				Name: "gold",
			}.Build(),
			Spec: publicv1.StorageTierSpec_builder{
				Description:          "High-performance tier",
				Protocol:             publicv1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
				MaxReadBandwidthMbs:  1000,
				MaxWriteBandwidthMbs: 500,
				EncryptionEnabled:    true,
			}.Build(),
			Status: publicv1.StorageTierStatus_builder{
				State:   publicv1.StorageTierState_STORAGE_TIER_STATE_ACTIVE,
				Message: &msg,
			}.Build(),
		}.Build()

		output := formatStorageTier(st)
		Expect(output).To(ContainSubstring("tier-001"))
		Expect(output).To(ContainSubstring("gold"))
		Expect(output).To(ContainSubstring("High-performance tier"))
		Expect(output).To(ContainSubstring("BLOCK"))
		Expect(output).NotTo(ContainSubstring("STORAGE_PROTOCOL_"))
		Expect(output).To(MatchRegexp(`Max Read BW \(MB/s\):\s+1000`))
		Expect(output).To(MatchRegexp(`Max Write BW \(MB/s\):\s+500`))
		Expect(output).To(MatchRegexp(`Encryption Enabled:\s+true`))
		Expect(output).To(ContainSubstring("ACTIVE"))
		Expect(output).NotTo(ContainSubstring("STORAGE_TIER_STATE_"))
		Expect(output).To(ContainSubstring("All backends healthy"))
	})

	It("should show '-' for name, description and message when empty", func() {
		st := publicv1.StorageTier_builder{
			Id: "tier-002",
		}.Build()

		output := formatStorageTier(st)
		Expect(output).To(MatchRegexp(`Name:\s+-`))
		Expect(output).To(MatchRegexp(`Description:\s+-`))
		Expect(output).To(MatchRegexp(`Message:\s+-`))
	})

	It("should strip STORAGE_PROTOCOL_ prefix from protocol", func() {
		st := publicv1.StorageTier_builder{
			Id: "tier-003",
			Spec: publicv1.StorageTierSpec_builder{
				Protocol: publicv1.StorageProtocol_STORAGE_PROTOCOL_NFS,
			}.Build(),
		}.Build()

		output := formatStorageTier(st)
		Expect(output).To(ContainSubstring("NFS"))
		Expect(output).NotTo(ContainSubstring("STORAGE_PROTOCOL_"))
	})

	It("should strip STORAGE_TIER_STATE_ prefix from state", func() {
		st := publicv1.StorageTier_builder{
			Id: "tier-004",
			Status: publicv1.StorageTierStatus_builder{
				State: publicv1.StorageTierState_STORAGE_TIER_STATE_ACTIVE,
			}.Build(),
		}.Build()

		output := formatStorageTier(st)
		Expect(output).To(ContainSubstring("ACTIVE"))
		Expect(output).NotTo(ContainSubstring("STORAGE_TIER_STATE_"))
	})
})
