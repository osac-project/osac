/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package diskimage

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/timestamppb"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
)

func formatDiskImage(di *publicv1.DiskImage) string {
	var buf bytes.Buffer
	renderDiskImage(&buf, di)
	return buf.String()
}

var _ = Describe("Describe diskimage command", func() {
	It("should create command without error", func() {
		cmd := Cmd()
		Expect(cmd).NotTo(BeNil())
		Expect(cmd.Use).To(Equal("diskimage [FLAG...] ID|NAME"))
	})

	It("should have diskimages alias", func() {
		cmd := Cmd()
		Expect(cmd.Aliases).To(ContainElement("diskimages"))
	})
})

var _ = Describe("Disk image rendering", func() {
	It("renders all base fields", func() {
		di := publicv1.DiskImage_builder{
			Id: "fedora-41",
			Metadata: publicv1.Metadata_builder{
				Name: "fedora-41",
			}.Build(),
			Spec: publicv1.DiskImageSpec_builder{
				SourceType:    publicv1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:     "quay.io/containerdisks/fedora:41",
				GuestOsFamily: publicv1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
				Architecture: []publicv1.Architecture{
					publicv1.Architecture_ARCHITECTURE_AMD64,
				},
				Lifecycle: publicv1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE,
			}.Build(),
		}.Build()

		output := formatDiskImage(di)
		Expect(output).To(ContainSubstring("Name:"))
		Expect(output).To(ContainSubstring("fedora-41"))
		Expect(output).To(ContainSubstring("REGISTRY"))
		Expect(output).To(ContainSubstring("quay.io/containerdisks/fedora:41"))
		Expect(output).To(ContainSubstring("LINUX"))
		Expect(output).To(ContainSubstring("AMD64"))
		Expect(output).To(ContainSubstring("AVAILABLE"))
	})

	It("strips enum prefixes", func() {
		di := publicv1.DiskImage_builder{
			Id: "test",
			Metadata: publicv1.Metadata_builder{
				Name: "test",
			}.Build(),
			Spec: publicv1.DiskImageSpec_builder{
				SourceType:    publicv1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:     "quay.io/test:v1",
				GuestOsFamily: publicv1.GuestOSFamily_GUEST_OS_FAMILY_WINDOWS,
				Architecture: []publicv1.Architecture{
					publicv1.Architecture_ARCHITECTURE_ARM64,
				},
				Lifecycle: publicv1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED,
			}.Build(),
		}.Build()

		output := formatDiskImage(di)
		Expect(output).NotTo(ContainSubstring("SOURCE_TYPE_"))
		Expect(output).NotTo(ContainSubstring("GUEST_OS_FAMILY_"))
		Expect(output).NotTo(ContainSubstring("ARCHITECTURE_"))
		Expect(output).NotTo(ContainSubstring("DISK_IMAGE_LIFECYCLE_"))
		Expect(output).To(ContainSubstring("REGISTRY"))
		Expect(output).To(ContainSubstring("WINDOWS"))
		Expect(output).To(ContainSubstring("ARM64"))
		Expect(output).To(ContainSubstring("DEPRECATED"))
	})

	It("renders multiple architectures comma-separated", func() {
		di := publicv1.DiskImage_builder{
			Id: "multi-arch",
			Metadata: publicv1.Metadata_builder{
				Name: "multi-arch",
			}.Build(),
			Spec: publicv1.DiskImageSpec_builder{
				SourceType: publicv1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  "quay.io/test:v1",
				Architecture: []publicv1.Architecture{
					publicv1.Architecture_ARCHITECTURE_AMD64,
					publicv1.Architecture_ARCHITECTURE_ARM64,
					publicv1.Architecture_ARCHITECTURE_S390X,
				},
				Lifecycle: publicv1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE,
			}.Build(),
		}.Build()

		output := formatDiskImage(di)
		Expect(output).To(ContainSubstring("AMD64, ARM64, S390X"))
	})

	It("renders deprecation timestamps when present", func() {
		di := publicv1.DiskImage_builder{
			Id: "deprecated",
			Metadata: publicv1.Metadata_builder{
				Name: "deprecated",
			}.Build(),
			Spec: publicv1.DiskImageSpec_builder{
				SourceType: publicv1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  "quay.io/test:v1",
				Architecture: []publicv1.Architecture{
					publicv1.Architecture_ARCHITECTURE_AMD64,
				},
				Lifecycle: publicv1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE,
				Deprecation: publicv1.DiskImageDeprecation_builder{
					DeprecationTimestamp:  timestamppb.Now(),
					ObsolescenceTimestamp: timestamppb.Now(),
				}.Build(),
			}.Build(),
		}.Build()

		output := formatDiskImage(di)
		Expect(output).To(ContainSubstring("Deprecated At:"))
		Expect(output).To(ContainSubstring("Obsolete At:"))
	})

	It("hides deprecation when not present", func() {
		di := publicv1.DiskImage_builder{
			Id: "available",
			Metadata: publicv1.Metadata_builder{
				Name: "available",
			}.Build(),
			Spec: publicv1.DiskImageSpec_builder{
				SourceType: publicv1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  "quay.io/test:v1",
				Architecture: []publicv1.Architecture{
					publicv1.Architecture_ARCHITECTURE_AMD64,
				},
				Lifecycle: publicv1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE,
			}.Build(),
		}.Build()

		output := formatDiskImage(di)
		Expect(output).NotTo(ContainSubstring("Deprecated At:"))
		Expect(output).NotTo(ContainSubstring("Obsolete At:"))
	})

})
