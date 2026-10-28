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
	"google.golang.org/protobuf/reflect/protoreflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("BMaaS SSH-key reference contracts", func() {
	It("uses the shared full SecretReference policy for BMaaS and ComputeInstance", func() {
		bmaasSSHKey := (&privatev1.BareMetalInstanceSpec{}).ProtoReflect().Descriptor().Fields().ByName("ssh_key")
		Expect(bmaasSSHKey).ToNot(BeNil())
		Expect(bmaasSSHKey.Message().FullName()).To(Equal(protoreflect.FullName("osac.private.v1.SecretReference")))

		catalogSSHKey := (&privatev1.BareMetalInstanceCatalogItemFields{}).ProtoReflect().Descriptor().Fields().ByName("ssh_key")
		Expect(catalogSSHKey).ToNot(BeNil())
		Expect(catalogSSHKey.Message().FullName()).To(Equal(protoreflect.FullName("osac.private.v1.FullSecretReferenceFieldPolicy")))

		computeSSHKey := (&privatev1.ComputeInstanceSpec{}).ProtoReflect().Descriptor().Fields().ByName("ssh_key")
		Expect(computeSSHKey).ToNot(BeNil())
		Expect(computeSSHKey.Message().FullName()).To(Equal(protoreflect.FullName("osac.private.v1.SecretReference")))

		fullPolicy := (&privatev1.FullSecretReferenceFieldPolicy{}).ProtoReflect().Descriptor()
		Expect(fullPolicy.Fields().ByName("locked").Message().FullName()).To(Equal(protoreflect.FullName("osac.private.v1.SecretReference")))
	})

	It("uses registered Secret references for Cluster SSH keys", func() {
		clusterSSHKey := (&privatev1.ClusterSpec{}).ProtoReflect().Descriptor().Fields().ByName("ssh_key")
		Expect(clusterSSHKey).ToNot(BeNil())
		Expect(clusterSSHKey.Number()).To(Equal(protoreflect.FieldNumber(15)))
		Expect(clusterSSHKey.Message().FullName()).To(Equal(protoreflect.FullName("osac.private.v1.SecretReference")))

		templateSSHKey := (&privatev1.ClusterTemplateSpecDefaults{}).ProtoReflect().Descriptor().Fields().ByName("ssh_key")
		Expect(templateSSHKey).ToNot(BeNil())
		Expect(templateSSHKey.Number()).To(Equal(protoreflect.FieldNumber(7)))
		Expect(templateSSHKey.Message().FullName()).To(Equal(protoreflect.FullName("osac.private.v1.SecretReference")))

		catalogSSHKey := (&privatev1.ClusterCatalogItemFields{}).ProtoReflect().Descriptor().Fields().ByName("ssh_key")
		Expect(catalogSSHKey).ToNot(BeNil())
		Expect(catalogSSHKey.Number()).To(Equal(protoreflect.FieldNumber(8)))
		Expect(catalogSSHKey.Message().FullName()).To(Equal(protoreflect.FullName("osac.private.v1.FullSecretReferenceFieldPolicy")))
	})

	It("removes and reserves every deprecated inline ssh_public_key field", func() {
		expectRemovedAndReserved := func(message protoreflect.MessageDescriptor, fieldName string, number protoreflect.FieldNumber) {
			GinkgoHelper()
			fields := message.Fields()
			Expect(fields.ByName(protoreflect.Name(fieldName))).To(BeNil(),
				"%s.%s must be removed", message.FullName(), fieldName)
			Expect(fields.ByNumber(number)).To(BeNil(),
				"%s field number %d must not be reused", message.FullName(), number)
			names := message.ReservedNames()
			foundName := false
			for i := 0; i < names.Len(); i++ {
				if names.Get(i) == protoreflect.Name(fieldName) {
					foundName = true
				}
			}
			Expect(foundName).To(BeTrue(),
				"%s must reserve the name %s", message.FullName(), fieldName)
			found := false
			ranges := message.ReservedRanges()
			for i := 0; i < ranges.Len(); i++ {
				rn := ranges.Get(i)
				if rn[0] <= number && number < rn[1] {
					found = true
				}
			}
			Expect(found).To(BeTrue(),
				"%s must reserve field number %d", message.FullName(), number)
		}

		expectRemovedAndReserved(
			(&privatev1.BareMetalInstanceSpec{}).ProtoReflect().Descriptor(), "ssh_public_key", 2)
		expectRemovedAndReserved(
			(&privatev1.BareMetalInstanceCatalogItemFields{}).ProtoReflect().Descriptor(), "ssh_public_key", 1)
		expectRemovedAndReserved(
			(&privatev1.ClusterSpec{}).ProtoReflect().Descriptor(), "ssh_public_key", 5)
		expectRemovedAndReserved(
			(&privatev1.ClusterTemplateSpecDefaults{}).ProtoReflect().Descriptor(), "ssh_public_key", 2)
		expectRemovedAndReserved(
			(&privatev1.ClusterCatalogItemFields{}).ProtoReflect().Descriptor(), "ssh_public_key", 2)
	})

	It("keeps every registered ssh_key replacement field", func() {
		clusterSSHKey := (&privatev1.ClusterSpec{}).ProtoReflect().Descriptor().Fields().ByName("ssh_key")
		Expect(clusterSSHKey).ToNot(BeNil())
		Expect(clusterSSHKey.Number()).To(Equal(protoreflect.FieldNumber(15)))

		templateSSHKey := (&privatev1.ClusterTemplateSpecDefaults{}).ProtoReflect().Descriptor().Fields().ByName("ssh_key")
		Expect(templateSSHKey).ToNot(BeNil())
		Expect(templateSSHKey.Number()).To(Equal(protoreflect.FieldNumber(7)))

		catalogSSHKey := (&privatev1.ClusterCatalogItemFields{}).ProtoReflect().Descriptor().Fields().ByName("ssh_key")
		Expect(catalogSSHKey).ToNot(BeNil())
		Expect(catalogSSHKey.Number()).To(Equal(protoreflect.FieldNumber(8)))

		bmiSSHKey := (&privatev1.BareMetalInstanceSpec{}).ProtoReflect().Descriptor().Fields().ByName("ssh_key")
		Expect(bmiSSHKey).ToNot(BeNil())
		Expect(bmiSSHKey.Number()).To(Equal(protoreflect.FieldNumber(15)))
	})
})
