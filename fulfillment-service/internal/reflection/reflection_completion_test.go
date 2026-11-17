/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package reflection

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/packages"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

var _ = Describe("ObjectTypeNames", func() {
	Describe("P2 human lock: mutation completion stays collection-only", func() {
		It("excludes get-only singleton names from public and private mutation completion", func() {
			for _, pkg := range []string{packages.PublicV1, packages.PrivateV1} {
				mutationNames := ObjectTypeNames(pkg)
				updateNames := UpdatableObjectTypeNames(pkg)
				for _, name := range []string{"billingproviderstatus", "billingproviderstatuses"} {
					Expect(mutationNames).NotTo(ContainElement(name))
					Expect(updateNames).NotTo(ContainElement(name))
				}
			}
		})

		DescribeTable("validates registered collection response shapes before advertising names",
			func(shape string) {
				names := map[string]bool{}
				descriptor := p2RegisteredService(protoreflect.FullName("osac.public.v1.Clusters"), func(file *descriptorpb.FileDescriptorProto) {
					if shape != "valid" {
						p2ObjectShape(file, shape)
					}
				})
				Expect(func() { scanServiceForNames(descriptor, names, false, false) }).NotTo(Panic())
				if shape == "valid" {
					Expect(names).To(Equal(map[string]bool{"cluster": true, "clusters": true}))
				} else {
					Expect(names).To(BeEmpty())
				}
			},
			Entry("registered valid counterpart", "valid"),
			Entry("missing object", "missing"), Entry("repeated object", "repeated"),
		)
	})

	It("returns names for public packages", func() {
		names := ObjectTypeNames(packages.Public...)
		Expect(names).ToNot(BeEmpty())
		Expect(names).To(ContainElement("cluster"))
		Expect(names).To(ContainElement("clusters"))
	})

	It("returns names for private packages", func() {
		names := ObjectTypeNames(packages.Private...)
		Expect(names).ToNot(BeEmpty())
		Expect(names).To(ContainElement("cluster"))
		Expect(names).To(ContainElement("clusters"))
		Expect(names).To(ContainElement("baremetalinstancetype"))
		Expect(names).To(ContainElement("baremetalinstancetypes"))
	})

	It("includes both singular and plural forms", func() {
		names := ObjectTypeNames(packages.Public...)
		Expect(names).To(ContainElement("virtualnetwork"))
		Expect(names).To(ContainElement("virtualnetworks"))
		Expect(names).To(ContainElements(
			"subnet", "subnets",
			"securitygroup", "securitygroups",
			"externalip", "externalips",
			"externalipattachment", "externalipattachments",
			"natgateway", "natgateways",
		))
		Expect(names).To(ContainElement("computeinstance"))
		Expect(names).To(ContainElement("computeinstances"))
	})

	It("only returns update-capable types for update completion", func() {
		names := UpdatableObjectTypeNames(packages.Public...)
		Expect(names).To(ContainElements("cluster", "clusters", "computeinstance", "computeinstances"))
		Expect(names).ToNot(ContainElements(
			"virtualnetwork", "virtualnetworks",
			"subnet", "subnets",
			"securitygroup", "securitygroups",
			"externalip", "externalips",
			"externalipattachment", "externalipattachments",
			"natgateway", "natgateways",
		))
	})

	It("returns sorted results", func() {
		names := ObjectTypeNames(packages.Public...)
		Expect(names).To(Equal(sortedCopy(names)))
	})

	It("returns empty for unknown package", func() {
		names := ObjectTypeNames("nonexistent.package.v1")
		Expect(names).To(BeEmpty())
	})
})

func sortedCopy(s []string) []string {
	c := make([]string, len(s))
	copy(c, s)
	for i := range c {
		for j := i + 1; j < len(c); j++ {
			if c[i] > c[j] {
				c[i], c[j] = c[j], c[i]
			}
		}
	}
	return c
}
