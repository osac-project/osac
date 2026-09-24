/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"google.golang.org/protobuf/proto"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Bare-metal worker contract fields (OSAC-4148)", func() {
	roundTrip := func(msg proto.Message, empty proto.Message) proto.Message {
		data, err := proto.Marshal(msg)
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Unmarshal(data, empty)).To(Succeed())
		return empty
	}

	It("round-trips ClusterNodeSet.baremetal_instance_type on the private API", func() {
		original := privatev1.ClusterNodeSet_builder{
			BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
				Id:      "bmit-id",
				Name:    "bm-standard",
				Project: "proj",
				Shared:  true,
			}.Build(),
			Size: proto.Int32(3),
		}.Build()

		decoded := roundTrip(original, &privatev1.ClusterNodeSet{}).(*privatev1.ClusterNodeSet)

		Expect(proto.Equal(original, decoded)).To(BeTrue())
		Expect(decoded.GetBaremetalInstanceType().GetName()).To(Equal("bm-standard"))
		Expect(decoded.GetBaremetalInstanceType().GetShared()).To(BeTrue())
	})

	It("round-trips ClusterNodeSet.baremetal_instance_type on the public API", func() {
		original := publicv1.ClusterNodeSet_builder{
			BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{
				Name: "bm-standard",
			}.Build(),
			Size: proto.Int32(2),
		}.Build()

		decoded := roundTrip(original, &publicv1.ClusterNodeSet{}).(*publicv1.ClusterNodeSet)

		Expect(proto.Equal(original, decoded)).To(BeTrue())
		Expect(decoded.GetBaremetalInstanceType().GetName()).To(Equal("bm-standard"))
	})

	It("round-trips BareMetalInstanceSpec.instance_type on the private API", func() {
		original := privatev1.BareMetalInstanceSpec_builder{
			InstanceType: privatev1.BareMetalInstanceTypeLocalReference_builder{
				Name: "bm-standard",
			}.Build(),
		}.Build()

		decoded := roundTrip(original, &privatev1.BareMetalInstanceSpec{}).(*privatev1.BareMetalInstanceSpec)

		Expect(proto.Equal(original, decoded)).To(BeTrue())
		Expect(decoded.GetInstanceType().GetName()).To(Equal("bm-standard"))
	})

	It("round-trips ClusterVersionSpec.disk_image on the private API", func() {
		original := privatev1.ClusterVersionSpec_builder{
			Image:   "quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64",
			Version: "4.18.0",
			DiskImage: privatev1.DiskImageReference_builder{
				Id:   "di-id",
				Name: "rhcos-4.18",
			}.Build(),
		}.Build()

		decoded := roundTrip(original, &privatev1.ClusterVersionSpec{}).(*privatev1.ClusterVersionSpec)

		Expect(proto.Equal(original, decoded)).To(BeTrue())
		Expect(decoded.GetDiskImage().GetName()).To(Equal("rhcos-4.18"))
	})

	It("round-trips BareMetalInstanceTypeReference on both APIs", func() {
		privateRef := privatev1.BareMetalInstanceTypeReference_builder{
			Id: "id", Name: "name", Project: "proj", Shared: true,
		}.Build()
		decodedPrivate := roundTrip(privateRef, &privatev1.BareMetalInstanceTypeReference{}).(*privatev1.BareMetalInstanceTypeReference)
		Expect(proto.Equal(privateRef, decodedPrivate)).To(BeTrue())

		publicRef := publicv1.BareMetalInstanceTypeReference_builder{
			Id: "id", Name: "name", Project: "proj", Shared: true,
		}.Build()
		decodedPublic := roundTrip(publicRef, &publicv1.BareMetalInstanceTypeReference{}).(*publicv1.BareMetalInstanceTypeReference)
		Expect(proto.Equal(publicRef, decodedPublic)).To(BeTrue())
	})
})
