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
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("ApplySpecDefaults", func() {
	It("Does nothing when defaults are nil", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
		}.Build()

		ApplySpecDefaults(spec, nil)

		Expect(spec.GetInstanceType()).To(BeNil())
	})

	It("Does nothing when spec is nil", func() {
		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
		}.Build()

		ApplySpecDefaults(nil, defaults)
	})

	It("Applies all defaults to empty spec", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
			DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: proto.Int32(10),
			}.Build(),
			RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetInstanceType().GetId()).To(Equal("standard-4-16"))
		Expect(spec.GetDiskImage().GetId()).To(Equal("test-disk-image"))
		Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(10)))
		Expect(spec.GetRunStrategy()).To(Equal(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS))
	})

	It("Does not override user-provided values", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "user-type"}.Build(),
			DiskImage:    &privatev1.DiskImageReference{Id: "user-disk-image"},
			RunStrategy:  privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED.Enum(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "default-type"}.Build(),
			DiskImage:    &privatev1.DiskImageReference{Id: "default-disk-image"},
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: proto.Int32(10),
			}.Build(),
			RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetInstanceType().GetId()).To(Equal("user-type"))
		Expect(spec.GetDiskImage().GetId()).To(Equal("user-disk-image"))
		Expect(spec.GetRunStrategy()).To(Equal(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED))
		Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(10)))
	})

	It("Applies partial defaults", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
			RunStrategy:  privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetInstanceType().GetId()).To(Equal("standard-4-16"))
		Expect(spec.GetRunStrategy()).To(Equal(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS))
		Expect(spec.HasDiskImage()).To(BeFalse())
		Expect(spec.HasBootDisk()).To(BeFalse())
	})

	It("Merges default boot_disk size_gib when user provides empty boot_disk", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			BootDisk: privatev1.ComputeInstanceDisk_builder{}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: proto.Int32(20),
			}.Build(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(20)))
	})

	It("Merges default boot_disk storage_tier when user provides boot_disk without storage_tier", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: proto.Int32(50),
			}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib:     proto.Int32(20),
				StorageTier: new("standard"),
			}.Build(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(50)))
		Expect(spec.GetBootDisk().GetStorageTier()).To(Equal("standard"))
	})

	It("Does not override user-provided boot_disk storage_tier with template default", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib:     proto.Int32(50),
				StorageTier: new("fast"),
			}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib:     proto.Int32(20),
				StorageTier: new("standard"),
			}.Build(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetBootDisk().GetStorageTier()).To(Equal("fast"))
	})

	It("Merges default boot_disk size when user provides only storage_tier", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				StorageTier: new("fast"),
			}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib:     proto.Int32(20),
				StorageTier: new("standard"),
			}.Build(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(20)))
		Expect(spec.GetBootDisk().GetStorageTier()).To(Equal("fast"))
	})

	It("Clones entire boot_disk default including storage_tier when user provides no boot_disk", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib:     proto.Int32(20),
				StorageTier: new("standard"),
			}.Build(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(20)))
		Expect(spec.GetBootDisk().GetStorageTier()).To(Equal("standard"))
	})

	It("Applies instance_type default when user provides no compute fields", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetInstanceType().GetId()).To(Equal("standard-4-16"))
	})

	It("Does not override user-provided instance_type with template default", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "user-chosen-type"}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetInstanceType().GetId()).To(Equal("user-chosen-type"))
	})

	It("Still applies non-compute defaults when instance_type is set", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
			DiskImage:   &privatev1.DiskImageReference{Id: "default-disk-image"},
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: proto.Int32(20),
			}.Build(),
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetRunStrategy()).To(Equal(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS))
		Expect(spec.GetDiskImage().GetId()).To(Equal("default-disk-image"))
		Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(20)))
	})

	It("Applies disk_image default when user does not provide value", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			DiskImage: &privatev1.DiskImageReference{Id: "template-disk-image"},
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.HasDiskImage()).To(BeTrue())
		Expect(spec.GetDiskImage().GetId()).To(Equal("template-disk-image"))
	})

	It("Does not override user-provided disk_image with template default", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:  privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			DiskImage: &privatev1.DiskImageReference{Id: "user-disk-image"},
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{
			DiskImage: &privatev1.DiskImageReference{Id: "template-disk-image"},
		}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.GetDiskImage().GetId()).To(Equal("user-disk-image"))
	})

	It("Does nothing when template has no disk_image default", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
		}.Build()

		defaults := privatev1.ComputeInstanceTemplateSpecDefaults_builder{}.Build()

		ApplySpecDefaults(spec, defaults)

		Expect(spec.HasDiskImage()).To(BeFalse())
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
			Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("boot_disk"))
		Expect(err.Error()).To(ContainSubstring("disk_image"))
		Expect(err.Error()).To(ContainSubstring("instance_type"))
		Expect(err.Error()).To(ContainSubstring("run_strategy"))
	})

	It("Returns error for partially missing fields", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
			RunStrategy:  privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("boot_disk"))
		Expect(err.Error()).To(ContainSubstring("disk_image"))
		Expect(err.Error()).ToNot(ContainSubstring("instance_type"))
		Expect(err.Error()).ToNot(ContainSubstring("run_strategy"))
	})

	It("Passes when all required fields are set", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
			DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib:     proto.Int32(20),
				StorageTier: new("standard"),
			}.Build(),
			RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Requires instance_type when not set", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:  privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			DiskImage: &privatev1.DiskImageReference{Id: "test-disk-image"},
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib:     proto.Int32(20),
				StorageTier: new("standard"),
			}.Build(),
			RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("instance_type"))
	})

	It("Still requires disk_image, boot_disk, run_strategy when instance_type is set", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("boot_disk"))
		Expect(err.Error()).To(ContainSubstring("disk_image"))
		Expect(err.Error()).To(ContainSubstring("run_strategy"))
		Expect(err.Error()).ToNot(ContainSubstring("instance_type"))
	})

	It("Rejects invalid run_strategy value", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
			DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib:     proto.Int32(20),
				StorageTier: new("standard"),
			}.Build(),
			RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_UNSPECIFIED.Enum(),
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("invalid run_strategy"))
		Expect(err.Error()).To(ContainSubstring("COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS"))
		Expect(err.Error()).To(ContainSubstring("COMPUTE_INSTANCE_RUN_STRATEGY_HALTED"))
	})

	It("Rejects boot_disk with zero size", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
			DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib:     proto.Int32(0),
				StorageTier: new("standard"),
			}.Build(),
			RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
		}.Build()

		err := ValidateRequiredSpecFields(spec)
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("boot_disk.size_gib"))
	})

	It("Accepts boot_disk with empty storage_tier", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
			DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib:     proto.Int32(20),
				StorageTier: new(""),
			}.Build(),
			RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
		}.Build()

		Expect(ValidateRequiredSpecFields(spec)).To(Succeed())
	})

	It("Accepts boot_disk without storage_tier", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
			DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib: proto.Int32(20),
			}.Build(),
			RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
		}.Build()

		Expect(ValidateRequiredSpecFields(spec)).To(Succeed())
	})

	It("Accepts additional_disk with empty storage_tier", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
			DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib:     proto.Int32(20),
				StorageTier: new("standard"),
			}.Build(),
			AdditionalDisks: []*privatev1.ComputeInstanceDisk{
				privatev1.ComputeInstanceDisk_builder{
					SizeGib:     proto.Int32(100),
					StorageTier: new("standard"),
				}.Build(),
				privatev1.ComputeInstanceDisk_builder{
					SizeGib:     proto.Int32(200),
					StorageTier: new(""),
				}.Build(),
			},
			RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
		}.Build()

		Expect(ValidateRequiredSpecFields(spec)).To(Succeed())
	})

	It("Accepts additional_disk without storage_tier", func() {
		spec := privatev1.ComputeInstanceSpec_builder{
			Template:     privatev1.ComputeInstanceTemplateReference_builder{Id: "test.template"}.Build(),
			InstanceType: privatev1.InstanceTypeReference_builder{Id: "standard-4-16"}.Build(),
			DiskImage:    &privatev1.DiskImageReference{Id: "test-disk-image"},
			BootDisk: privatev1.ComputeInstanceDisk_builder{
				SizeGib:     proto.Int32(20),
				StorageTier: new("standard"),
			}.Build(),
			AdditionalDisks: []*privatev1.ComputeInstanceDisk{
				privatev1.ComputeInstanceDisk_builder{
					SizeGib:     proto.Int32(100),
					StorageTier: new("standard"),
				}.Build(),
				privatev1.ComputeInstanceDisk_builder{
					SizeGib: proto.Int32(200),
				}.Build(),
			},
			RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
		}.Build()

		Expect(ValidateRequiredSpecFields(spec)).To(Succeed())
	})
})
