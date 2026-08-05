/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package utils

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("ApplySpecDefaults", func() {
	It("Does nothing when defaults are nil", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: "test.template",
		}.Build()

		ApplySpecDefaults(spec, nil)

		Expect(spec.GetInstanceType()).To(BeEmpty())
	})

	It("Does nothing when spec is nil", func() {
		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			InstanceType: new("standard-4-16"),
		}.Build()

		ApplySpecDefaults(nil, defaults)
	})

	It("Applies all defaults to empty spec", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: "test.template",
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			InstanceType: new("standard-4-16"),
			Image: privatev1.ComputeInstanceImage_builder{
				SourceType: "registry",
				SourceRef:  "quay.io/containerdisks/fedora:latest",
			}.Build(),
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: 10,
			}.Build(),
			RunStrategy: new("Always"),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetInstanceType()).To(Equal("standard-4-16"))
		Expect(spec.GetImage().GetSourceType()).To(Equal("registry"))
		Expect(spec.GetImage().GetSourceRef()).To(Equal("quay.io/containerdisks/fedora:latest"))
		Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(10)))
		Expect(spec.GetRunStrategy()).To(Equal("Always"))
	})

	It("Does not override user-provided values", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     "test.template",
			InstanceType: new("user-type"),
			RunStrategy:  new("Halted"),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			InstanceType: new("default-type"),
			Image: privatev1.ComputeInstanceImage_builder{
				SourceType: "registry",
				SourceRef:  "quay.io/containerdisks/fedora:latest",
			}.Build(),
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: 10,
			}.Build(),
			RunStrategy: new("Always"),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		// User-provided values preserved:
		Expect(spec.GetInstanceType()).To(Equal("user-type"))
		Expect(spec.GetRunStrategy()).To(Equal("Halted"))
		// Defaults fill the rest:
		Expect(spec.GetImage().GetSourceRef()).To(Equal("quay.io/containerdisks/fedora:latest"))
		Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(10)))
	})

	It("Applies partial defaults", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: "test.template",
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			InstanceType: new("standard-4-16"),
			RunStrategy:  new("Always"),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetInstanceType()).To(Equal("standard-4-16"))
		Expect(spec.GetRunStrategy()).To(Equal("Always"))
		Expect(spec.HasImage()).To(BeFalse())
		Expect(spec.HasBootDisk()).To(BeFalse())
	})

	It("Merges default source_type into user-provided partial image", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: "test.template",
			Image: privatev1.ComputeInstanceImage_builder{
				SourceRef: "quay.io/my-image:latest",
			}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			Image: privatev1.ComputeInstanceImage_builder{
				SourceType: "registry",
				SourceRef:  "quay.io/containerdisks/fedora:latest",
			}.Build(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetImage().GetSourceType()).To(Equal("registry"))
		Expect(spec.GetImage().GetSourceRef()).To(Equal("quay.io/my-image:latest"))
	})

	It("Merges default source_ref into user-provided partial image", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: "test.template",
			Image: privatev1.ComputeInstanceImage_builder{
				SourceType: "registry",
			}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			Image: privatev1.ComputeInstanceImage_builder{
				SourceType: "registry",
				SourceRef:  "quay.io/containerdisks/fedora:latest",
			}.Build(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetImage().GetSourceType()).To(Equal("registry"))
		Expect(spec.GetImage().GetSourceRef()).To(Equal("quay.io/containerdisks/fedora:latest"))
	})

	It("Does not override user-provided image fields with defaults", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: "test.template",
			Image: privatev1.ComputeInstanceImage_builder{
				SourceType: "registry",
				SourceRef:  "quay.io/my-image:latest",
			}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			Image: privatev1.ComputeInstanceImage_builder{
				SourceType: "registry",
				SourceRef:  "quay.io/containerdisks/fedora:latest",
			}.Build(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetImage().GetSourceType()).To(Equal("registry"))
		Expect(spec.GetImage().GetSourceRef()).To(Equal("quay.io/my-image:latest"))
	})

	It("Merges default boot_disk size_gib when user provides empty boot_disk", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: "test.template",
			BootDisk: privatev1.ComputeInstanceDisk_builder{}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: 20,
			}.Build(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(20)))
	})

	It("Applies instance_type default when user provides no compute fields", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: "test.template",
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			InstanceType: new("standard-4-16"),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetInstanceType()).To(Equal("standard-4-16"))
	})

	It("Does not override user-provided instance_type with template default", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     "test.template",
			InstanceType: new("user-chosen-type"),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			InstanceType: new("standard-4-16"),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetInstanceType()).To(Equal("user-chosen-type"))
	})

	It("Still applies non-compute defaults when instance_type is set", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     "test.template",
			InstanceType: new("standard-4-16"),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			RunStrategy: new("Always"),
			Image: privatev1.ComputeInstanceImage_builder{
				SourceType: "registry",
				SourceRef:  "quay.io/containerdisks/fedora:latest",
			}.Build(),
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: 20,
			}.Build(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetRunStrategy()).To(Equal("Always"))
		Expect(spec.GetImage().GetSourceRef()).To(Equal("quay.io/containerdisks/fedora:latest"))
		Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(20)))
	})

	It("Clones message-type defaults to prevent shared state", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: "test.template",
		}.Build()

		defaultImage := privatev1.ComputeInstanceImage_builder{
			SourceType: "registry",
			SourceRef:  "quay.io/containerdisks/fedora:latest",
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			Image: defaultImage,
		}.Build()

		ApplySpecDefaults(spec, defaults)

		// Mutating the default should not affect the spec:
		defaultImage.SetSourceRef("changed")
		Expect(spec.GetImage().GetSourceRef()).To(Equal("quay.io/containerdisks/fedora:latest"))
	})

	It("Applies is_windows default when user does not provide value", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: "test.template",
		}.Build()

		trueVal := true
		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			IsWindows: &trueVal,
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.HasIsWindows()).To(BeTrue())
		Expect(spec.GetIsWindows()).To(BeTrue())
	})

	It("Does not override user-provided is_windows value with template default", func() {
		falseVal := false
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:  "test.template",
			IsWindows: &falseVal,
		}.Build()

		trueVal := true
		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			IsWindows: &trueVal,
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetIsWindows()).To(BeFalse())
	})

	It("Does nothing when template has no is_windows default", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: "test.template",
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.HasIsWindows()).To(BeFalse())
	})
})

var _ = Describe("ValidateRequiredSpecFields", func() {
	It("Returns error when spec is nil", func() {
		err := ValidateRequiredSpecFields(nil)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("Returns error listing all missing fields", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: "test.template",
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("boot_disk"))
		Expect(err.Error()).To(ContainSubstring("image"))
		Expect(err.Error()).To(ContainSubstring("instance_type"))
		Expect(err.Error()).To(ContainSubstring("run_strategy"))
	})

	It("Returns error for partially missing fields", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     "test.template",
			InstanceType: new("standard-4-16"),
			RunStrategy:  new("Always"),
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("boot_disk"))
		Expect(err.Error()).To(ContainSubstring("image"))
		Expect(err.Error()).ToNot(ContainSubstring("instance_type"))
		Expect(err.Error()).ToNot(ContainSubstring("run_strategy"))
	})

	It("Passes when all required fields are set", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     "test.template",
			InstanceType: new("standard-4-16"),
			Image: privatev1.ComputeInstanceImage_builder{
				SourceType: "registry",
				SourceRef:  "quay.io/containerdisks/fedora:latest",
			}.Build(),
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: 20,
			}.Build(),
			RunStrategy: new("Always"),
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Requires instance_type when not set", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: "test.template",
			Image: privatev1.ComputeInstanceImage_builder{
				SourceType: "registry",
				SourceRef:  "quay.io/containerdisks/fedora:latest",
			}.Build(),
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: 20,
			}.Build(),
			RunStrategy: new("Always"),
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("instance_type"))
	})

	It("Still requires image, boot_disk, run_strategy when instance_type is set", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     "test.template",
			InstanceType: new("standard-4-16"),
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("boot_disk"))
		Expect(err.Error()).To(ContainSubstring("image"))
		Expect(err.Error()).To(ContainSubstring("run_strategy"))
		Expect(err.Error()).ToNot(ContainSubstring("instance_type"))
	})

	It("Rejects invalid run_strategy value", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     "test.template",
			InstanceType: new("standard-4-16"),
			Image: privatev1.ComputeInstanceImage_builder{
				SourceType: "registry",
				SourceRef:  "quay.io/containerdisks/fedora:latest",
			}.Build(),
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: 20,
			}.Build(),
			RunStrategy: new("always"),
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("invalid run_strategy"))
		Expect(err.Error()).To(ContainSubstring("Always"))
		Expect(err.Error()).To(ContainSubstring("Halted"))
	})

	It("Rejects empty image fields", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     "test.template",
			InstanceType: new("standard-4-16"),
			Image:        privatev1.ComputeInstanceImage_builder{}.Build(),
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: 20,
			}.Build(),
			RunStrategy: new("Always"),
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("image.source_type"))
		Expect(err.Error()).To(ContainSubstring("image.source_ref"))
	})

	It("Rejects image with partial fields", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     "test.template",
			InstanceType: new("standard-4-16"),
			Image: privatev1.ComputeInstanceImage_builder{
				SourceType: "registry",
			}.Build(),
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: 20,
			}.Build(),
			RunStrategy: new("Always"),
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("image.source_ref"))
		Expect(err.Error()).ToNot(ContainSubstring("image.source_type"))
	})

	It("Rejects boot_disk with zero size", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     "test.template",
			InstanceType: new("standard-4-16"),
			Image: privatev1.ComputeInstanceImage_builder{
				SourceType: "registry",
				SourceRef:  "quay.io/containerdisks/fedora:latest",
			}.Build(),
			BootDisk:    privatev1.ComputeInstanceDisk_builder{}.Build(),
			RunStrategy: new("Always"),
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("boot_disk.size_gib"))
	})
})
