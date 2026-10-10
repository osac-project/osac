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

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// formatVolume renders a volume detail view and returns the output as a string.
func formatVolume(v *publicv1.Volume) string {
	var buf bytes.Buffer
	err := renderVolume(&buf, v)
	Expect(err).ToNot(HaveOccurred())
	return buf.String()
}

var _ = Describe("volumes get", func() {
	Context("command structure", func() {
		It("should create command without error", func() {
			cmd := getCmd()
			Expect(cmd).NotTo(BeNil())
			Expect(cmd.Use).To(Equal("get [FLAG...] ID|NAME"))
		})

		It("should require exactly one argument", func() {
			cmd := getCmd()
			Expect(cmd.Args).NotTo(BeNil())
		})

		It("should have proper short help", func() {
			cmd := getCmd()
			Expect(cmd.Short).To(Equal("Get volume details"))
		})
	})

	Context("renderVolume", func() {
		It("should display all fields when set", func() {
			msg := "Provisioning complete"
			v := publicv1.Volume_builder{
				Id: "vol-001",
				Metadata: publicv1.Metadata_builder{
					Name:    "my-volume",
					Project: "team-a.frontend",
				}.Build(),
				Spec: publicv1.VolumeSpec_builder{
					StorageTier: "gold",
					SizeGib:     100,
					AccessMode:  publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
				}.Build(),
				Status: publicv1.VolumeStatus_builder{
					State:   publicv1.VolumeState_VOLUME_STATE_AVAILABLE,
					Message: &msg,
				}.Build(),
			}.Build()

			output := formatVolume(v)
			Expect(output).To(ContainSubstring("vol-001"))
			Expect(output).To(ContainSubstring("my-volume"))
			Expect(output).To(ContainSubstring("team-a.frontend"))
			Expect(output).To(ContainSubstring("gold"))
			Expect(output).To(ContainSubstring("100"))
			Expect(output).To(ContainSubstring("READ_WRITE_ONCE"))
			Expect(output).NotTo(ContainSubstring("VOLUME_ACCESS_MODE_"))
			Expect(output).To(ContainSubstring("AVAILABLE"))
			Expect(output).NotTo(ContainSubstring("VOLUME_STATE_"))
			Expect(output).To(ContainSubstring("Provisioning complete"))
		})

		It("should show '-' for name, project, and message when empty", func() {
			v := publicv1.Volume_builder{
				Id: "vol-002",
			}.Build()

			output := formatVolume(v)
			Expect(output).To(MatchRegexp(`Name:\s+-`))
			Expect(output).To(MatchRegexp(`Project:\s+-`))
			Expect(output).To(MatchRegexp(`Message:\s+-`))
		})

		It("should strip VOLUME_ACCESS_MODE_ prefix from access mode", func() {
			v := publicv1.Volume_builder{
				Id: "vol-003",
				Spec: publicv1.VolumeSpec_builder{
					AccessMode: publicv1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_ONLY_MANY,
				}.Build(),
			}.Build()

			output := formatVolume(v)
			Expect(output).To(ContainSubstring("READ_ONLY_MANY"))
			Expect(output).NotTo(ContainSubstring("VOLUME_ACCESS_MODE_"))
		})

		It("should strip VOLUME_STATE_ prefix from state", func() {
			v := publicv1.Volume_builder{
				Id: "vol-004",
				Status: publicv1.VolumeStatus_builder{
					State: publicv1.VolumeState_VOLUME_STATE_CREATING,
				}.Build(),
			}.Build()

			output := formatVolume(v)
			Expect(output).To(ContainSubstring("CREATING"))
			Expect(output).NotTo(ContainSubstring("VOLUME_STATE_"))
		})

		It("should show '-' for storage tier when empty", func() {
			v := publicv1.Volume_builder{
				Id: "vol-005",
			}.Build()

			output := formatVolume(v)
			Expect(output).To(MatchRegexp(`Storage Tier:\s+-`))
		})
	})
})
