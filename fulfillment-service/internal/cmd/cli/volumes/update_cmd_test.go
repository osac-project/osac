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
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("volumes update", func() {
	Context("command structure", func() {
		It("should create command without error", func() {
			cmd := updateCmd()
			Expect(cmd).NotTo(BeNil())
			Expect(cmd.Use).To(Equal("update [FLAG...] ID|NAME"))
		})

		It("should require exactly one argument", func() {
			cmd := updateCmd()
			Expect(cmd.Args).NotTo(BeNil())
		})
	})

	Context("flag registration", func() {
		It("should register --display-name flag", func() {
			cmd := updateCmd()
			flag := cmd.Flags().Lookup("display-name")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("NAME"))
		})

		It("should register --description flag", func() {
			cmd := updateCmd()
			flag := cmd.Flags().Lookup("description")
			Expect(flag).NotTo(BeNil())
			Expect(flag.Usage).To(ContainSubstring("TEXT"))
		})
	})

	Context("applyVolumeMetadataUpdate", func() {
		It("should initialize metadata and apply display name on a volume without metadata", func() {
			volume := publicv1.Volume_builder{
				Id: "vol-no-meta",
				Spec: publicv1.VolumeSpec_builder{
					StorageTier: "gold",
					SizeGib:     50,
				}.Build(),
			}.Build()

			Expect(volume.HasMetadata()).To(BeFalse())

			updated := applyVolumeMetadataUpdate(volume, true, "Test Display", false, "")

			Expect(updated.HasMetadata()).To(BeTrue())
			Expect(updated.GetMetadata().GetDisplayName()).To(Equal("Test Display"))
		})

		It("should initialize metadata and apply description on a volume without metadata", func() {
			volume := publicv1.Volume_builder{
				Id: "vol-no-meta",
			}.Build()

			updated := applyVolumeMetadataUpdate(volume, false, "", true, "A description")

			Expect(updated.HasMetadata()).To(BeTrue())
			Expect(updated.GetMetadata().GetDescription()).To(Equal("A description"))
		})

		It("should apply both display name and description simultaneously", func() {
			volume := publicv1.Volume_builder{
				Id: "vol-both",
			}.Build()

			updated := applyVolumeMetadataUpdate(volume, true, "Display", true, "Desc")

			Expect(updated.GetMetadata().GetDisplayName()).To(Equal("Display"))
			Expect(updated.GetMetadata().GetDescription()).To(Equal("Desc"))
		})

		It("should preserve existing metadata when present", func() {
			volume := publicv1.Volume_builder{
				Id: "vol-with-meta",
				Metadata: publicv1.Metadata_builder{
					Name: "existing-name",
				}.Build(),
			}.Build()

			Expect(volume.HasMetadata()).To(BeTrue())

			updated := applyVolumeMetadataUpdate(volume, true, "New Display", false, "")

			// The original name must be preserved:
			Expect(updated.GetMetadata().GetName()).To(Equal("existing-name"))
			Expect(updated.GetMetadata().GetDisplayName()).To(Equal("New Display"))
		})

		It("should not modify the original volume", func() {
			volume := publicv1.Volume_builder{
				Id: "vol-original",
				Metadata: publicv1.Metadata_builder{
					Name: "keep-me",
				}.Build(),
			}.Build()

			_ = applyVolumeMetadataUpdate(volume, true, "Changed", true, "Changed desc")

			// The original must be untouched:
			Expect(volume.GetMetadata().GetDisplayName()).To(Equal(""))
			Expect(volume.GetMetadata().GetDescription()).To(Equal(""))
		})
	})

	Context("help text", func() {
		It("should have proper short help", func() {
			cmd := updateCmd()
			Expect(cmd.Short).To(Equal("Update volume properties"))
		})

		It("should have proper long help with examples", func() {
			cmd := updateCmd()
			Expect(cmd.Long).To(ContainSubstring("Update mutable properties of a volume."))
			Expect(cmd.Long).To(ContainSubstring("--display-name"))
			Expect(cmd.Long).To(ContainSubstring("--description"))
		})
	})
})
