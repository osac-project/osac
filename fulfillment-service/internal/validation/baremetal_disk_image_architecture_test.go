/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package validation

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Bare metal disk image architecture", func() {
	DescribeTable("matches every declared architecture without changing the resources",
		func(cpu string, images []privatev1.Architecture, compatible bool) {
			instanceType := privatev1.BareMetalInstanceType_builder{
				Id:       "type-id",
				Metadata: privatev1.Metadata_builder{Name: "selected-type"}.Build(),
				Spec: privatev1.BareMetalInstanceTypeSpec_builder{
					Hardware: privatev1.BareMetalHardwareSpec_builder{
						Cpu: privatev1.BareMetalCPUSpec_builder{Architecture: cpu}.Build(),
					}.Build(),
					HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
						MatchLabels: map[string]string{"private-selector": "hidden-host"},
					}.Build(),
				}.Build(),
			}.Build()
			image := privatev1.DiskImage_builder{
				Id:       "image-id",
				Metadata: privatev1.Metadata_builder{Name: "selected-image"}.Build(),
				Spec: privatev1.DiskImageSpec_builder{
					Architecture: images,
					SourceRef:    "oci://private.example/secret-image",
				}.Build(),
			}.Build()
			originalType, originalImage := proto.Clone(instanceType), proto.Clone(image)
			err := ValidateBareMetalDiskImageArchitecture(instanceType, image)
			if compatible {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(MatchError(And(
					HavePrefix("INCOMPATIBLE_DISK_IMAGE_ARCHITECTURE: "),
					ContainSubstring("DiskImage 'selected-image' supports ["),
					ContainSubstring(fmt.Sprintf("BareMetalInstanceType 'selected-type' requires %s.", cpu)),
					ContainSubstring("Select a compatible DiskImage or BareMetalInstanceType"),
					Not(ContainSubstring("hidden-host")),
					Not(ContainSubstring("private-selector")),
					Not(ContainSubstring("oci://private.example/secret-image")),
				)))
			}
			Expect(proto.Equal(instanceType, originalType)).To(BeTrue())
			Expect(proto.Equal(image, originalImage)).To(BeTrue())
		},
		Entry("amd64 with amd64", "amd64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64}, true),
		Entry("amd64 with arm64", "amd64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_ARM64}, false),
		Entry("amd64 with s390x", "amd64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_S390X}, false),
		Entry("arm64 with amd64", "arm64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64}, false),
		Entry("arm64 with arm64", "arm64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_ARM64}, true),
		Entry("arm64 with s390x", "arm64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_S390X}, false),
		Entry("s390x with amd64", "s390x", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64}, false),
		Entry("s390x with arm64", "s390x", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_ARM64}, false),
		Entry("s390x with s390x", "s390x", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_S390X}, true),
		Entry("first multi-image member", "amd64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64, privatev1.Architecture_ARCHITECTURE_ARM64}, true),
		Entry("second multi-image member", "arm64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64, privatev1.Architecture_ARCHITECTURE_ARM64}, true),
		Entry("reversed first member", "arm64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_ARM64, privatev1.Architecture_ARCHITECTURE_AMD64}, true),
		Entry("reversed second member", "amd64", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_ARM64, privatev1.Architecture_ARCHITECTURE_AMD64}, true),
		Entry("multi-image nonmember", "s390x", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64, privatev1.Architecture_ARCHITECTURE_ARM64}, false),
		Entry("all architectures", "s390x", []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64, privatev1.Architecture_ARCHITECTURE_ARM64, privatev1.Architecture_ARCHITECTURE_S390X}, true),
	)

	It("uses IDs for unnamed resources and lists all supported architectures", func() {
		instanceType := privatev1.BareMetalInstanceType_builder{
			Id: "type-id",
			Spec: privatev1.BareMetalInstanceTypeSpec_builder{
				Hardware: privatev1.BareMetalHardwareSpec_builder{
					Cpu: privatev1.BareMetalCPUSpec_builder{Architecture: "s390x"}.Build(),
				}.Build(),
			}.Build(),
		}.Build()
		image := privatev1.DiskImage_builder{
			Id: "image-id",
			Spec: privatev1.DiskImageSpec_builder{Architecture: []privatev1.Architecture{
				privatev1.Architecture_ARCHITECTURE_ARM64, privatev1.Architecture_ARCHITECTURE_AMD64,
			}}.Build(),
		}.Build()
		Expect(ValidateBareMetalDiskImageArchitecture(instanceType, image)).To(MatchError(
			"INCOMPATIBLE_DISK_IMAGE_ARCHITECTURE: DiskImage 'image-id' supports [arm64, amd64]; BareMetalInstanceType 'type-id' requires s390x. Select a compatible DiskImage or BareMetalInstanceType",
		))
	})
})
