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
	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

func formatTierTable(tiers []*publicv1.StorageTier) string {
	buffer := &bytes.Buffer{}
	console, err := terminal.NewConsole().
		SetLogger(logger).
		SetStdout(buffer).
		Build()
	Expect(err).ToNot(HaveOccurred())
	renderTierTable(console, tiers)
	return buffer.String()
}

var _ = Describe("renderTierTable", func() {
	It("should render all columns for a fully populated tier", func() {
		tiers := []*publicv1.StorageTier{
			publicv1.StorageTier_builder{
				Id: "tier-001",
				Metadata: publicv1.Metadata_builder{
					Name: "gold",
				}.Build(),
				Spec: publicv1.StorageTierSpec_builder{
					Description: "High-performance tier",
					Protocol:    publicv1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
				}.Build(),
				Status: publicv1.StorageTierStatus_builder{
					State: publicv1.StorageTierState_STORAGE_TIER_STATE_ACTIVE,
				}.Build(),
			}.Build(),
		}

		output := formatTierTable(tiers)
		Expect(output).To(ContainSubstring("ID"))
		Expect(output).To(ContainSubstring("NAME"))
		Expect(output).To(ContainSubstring("DESCRIPTION"))
		Expect(output).To(ContainSubstring("PROTOCOL"))
		Expect(output).To(ContainSubstring("STATE"))
		Expect(output).To(MatchRegexp(`tier-001\s+gold\s+High-performance tier\s+BLOCK\s+ACTIVE`))
		Expect(output).NotTo(ContainSubstring("STORAGE_PROTOCOL_"))
		Expect(output).NotTo(ContainSubstring("STORAGE_TIER_STATE_"))
	})

	It("should show '-' for name and description when empty", func() {
		tiers := []*publicv1.StorageTier{
			publicv1.StorageTier_builder{
				Id: "tier-002",
			}.Build(),
		}

		output := formatTierTable(tiers)
		Expect(output).To(MatchRegexp(`tier-002\s+-\s+-\s+UNSPECIFIED\s+UNSPECIFIED`))
	})

	It("should render one row per tier", func() {
		tiers := []*publicv1.StorageTier{
			publicv1.StorageTier_builder{
				Id: "tier-001",
				Metadata: publicv1.Metadata_builder{
					Name: "gold",
				}.Build(),
			}.Build(),
			publicv1.StorageTier_builder{
				Id: "tier-002",
				Metadata: publicv1.Metadata_builder{
					Name: "silver",
				}.Build(),
			}.Build(),
		}

		output := formatTierTable(tiers)
		Expect(output).To(ContainSubstring("tier-001"))
		Expect(output).To(ContainSubstring("gold"))
		Expect(output).To(ContainSubstring("tier-002"))
		Expect(output).To(ContainSubstring("silver"))
	})
})
