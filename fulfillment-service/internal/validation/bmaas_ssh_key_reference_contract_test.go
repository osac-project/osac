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
	"google.golang.org/protobuf/types/descriptorpb"

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

	It("deprecates the raw ssh_public_key in favor of the registered ssh_key", func() {
		rawKey := (&privatev1.BareMetalInstanceSpec{}).ProtoReflect().Descriptor().Fields().ByName("ssh_public_key")
		Expect(rawKey).ToNot(BeNil())
		rawOptions, ok := rawKey.Options().(*descriptorpb.FieldOptions)
		Expect(ok).To(BeTrue())
		Expect(rawOptions.GetDeprecated()).To(BeTrue(), "spec.ssh_public_key must be marked deprecated")

		registeredKey := (&privatev1.BareMetalInstanceSpec{}).ProtoReflect().Descriptor().Fields().ByName("ssh_key")
		Expect(registeredKey).ToNot(BeNil())
		registeredOptions, ok := registeredKey.Options().(*descriptorpb.FieldOptions)
		Expect(ok).To(BeTrue())
		Expect(registeredOptions.GetDeprecated()).To(BeFalse())
	})
})
