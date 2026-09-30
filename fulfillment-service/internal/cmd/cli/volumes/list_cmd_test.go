/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package volumes

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func formatVolumeTable(volumes []*publicv1.Volume) string {
	buffer := &bytes.Buffer{}
	console, err := terminal.NewConsole().
		SetLogger(logger).
		SetStdout(buffer).
		Build()
	Expect(err).ToNot(HaveOccurred())
	renderVolumeTable(console, volumes)
	return buffer.String()
}

var _ = Describe("volumes list", func() {
	Context("command structure", func() {
		It("should create command without error", func() {
			cmd := listCmd()
			Expect(cmd).NotTo(BeNil())
			Expect(cmd.Use).To(Equal("list"))
		})

		It("should register --project flag", func() {
			cmd := listCmd()
			flag := cmd.Flags().Lookup("project")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("PROJECT"))
		})

		It("should have proper short help", func() {
			cmd := listCmd()
			Expect(cmd.Short).To(Equal("List volumes"))
		})
	})

	Context("renderVolumeTable", func() {
		It("should render all columns for a fully populated volume", func() {
			volumes := []*publicv1.Volume{
				publicv1.Volume_builder{
					Id: "vol-001",
					Metadata: publicv1.Metadata_builder{
						Name: "my-volume",
					}.Build(),
					Spec: publicv1.VolumeSpec_builder{
						StorageTier: "gold",
						SizeGib:     100,
						AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
					Status: publicv1.VolumeStatus_builder{
						State: publicv1.VolumeState_VOLUME_STATE_AVAILABLE,
					}.Build(),
				}.Build(),
			}

			output := formatVolumeTable(volumes)
			Expect(output).To(ContainSubstring("ID"))
			Expect(output).To(ContainSubstring("NAME"))
			Expect(output).To(ContainSubstring("STORAGE TIER"))
			Expect(output).To(ContainSubstring("SIZE (GiB)"))
			Expect(output).To(ContainSubstring("ACCESS MODE"))
			Expect(output).To(ContainSubstring("STATE"))
			Expect(output).To(MatchRegexp(`vol-001\s+my-volume\s+gold\s+100\s+READ_WRITE_ONCE\s+AVAILABLE`))
			Expect(output).NotTo(ContainSubstring("VOLUME_ACCESS_MODE_"))
			Expect(output).NotTo(ContainSubstring("VOLUME_STATE_"))
		})

		It("should show '-' for name and storage tier when empty", func() {
			volumes := []*publicv1.Volume{
				publicv1.Volume_builder{
					Id: "vol-002",
				}.Build(),
			}

			output := formatVolumeTable(volumes)
			Expect(output).To(MatchRegexp(`vol-002\s+-\s+-`))
		})

		It("should render one row per volume", func() {
			volumes := []*publicv1.Volume{
				publicv1.Volume_builder{
					Id: "vol-001",
					Metadata: publicv1.Metadata_builder{
						Name: "alpha",
					}.Build(),
				}.Build(),
				publicv1.Volume_builder{
					Id: "vol-002",
					Metadata: publicv1.Metadata_builder{
						Name: "beta",
					}.Build(),
				}.Build(),
			}

			output := formatVolumeTable(volumes)
			Expect(output).To(ContainSubstring("vol-001"))
			Expect(output).To(ContainSubstring("alpha"))
			Expect(output).To(ContainSubstring("vol-002"))
			Expect(output).To(ContainSubstring("beta"))
		})
	})
})
