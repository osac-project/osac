/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package fieldutil

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("parseField", func() {
	DescribeTable("valid inputs",
		func(input, expectedKey, expectedValue string) {
			key, value, err := parseField(input)
			Expect(err).ToNot(HaveOccurred())
			Expect(key).To(Equal(expectedKey))
			Expect(value).To(Equal(expectedValue))
		},
		Entry("simple key=value", "vlan=99", "vlan", "99"),
		Entry("value containing equals", "pull_secret=eyJhdXRocw==", "pull_secret", "eyJhdXRocw=="),
		Entry("multiple equals signs", "vlan=99=103", "vlan", "99=103"),
		Entry("empty value", "key=", "key", ""),
	)

	DescribeTable("invalid inputs",
		func(input string) {
			_, _, err := parseField(input)
			Expect(err).To(HaveOccurred())
		},
		Entry("no equals sign", "noequals"),
		Entry("empty string", ""),
		Entry("only equals sign with no key", "=value"),
	)
})

var _ = Describe("inferValue", func() {
	DescribeTable("type inference",
		func(input string, expected any) {
			Expect(inferValue(input)).To(Equal(expected))
		},
		Entry("true boolean", "true", true),
		Entry("false boolean", "false", false),
		Entry("integer", "99", int64(99)),
		Entry("large integer preserves precision", "9007199254740993", int64(9007199254740993)),
		Entry("float", "3.14", float64(3.14)),
		Entry("version string (not a number)", "4.21.0", "4.21.0"),
		Entry("plain string", "hello", "hello"),
		Entry("empty string", "", ""),
	)
})

var _ = Describe("ApplyFields", func() {
	It("sets a simple spec field", func() {
		spec := &publicv1.ClusterSpec{}
		err := ApplyFields(spec, []string{"pull_secret=my-secret"})
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetPullSecret()).To(Equal("my-secret"))
	})

	It("sets a nested spec field", func() {
		spec := &publicv1.ClusterSpec{}
		err := ApplyFields(spec, []string{"network.pod_cidr=10.0.0.0/14"})
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetNetwork().GetPodCidr()).To(Equal("10.0.0.0/14"))
	})

	It("sets a template parameter with string value", func() {
		spec := &publicv1.ClusterSpec{}
		err := ApplyFields(spec, []string{"template_parameters.vpc_id=vpc-123"})
		Expect(err).ToNot(HaveOccurred())
		tp := spec.GetTemplateParameters()
		Expect(tp).To(HaveKey("vpc_id"))
		Expect(tp["vpc_id"].GetTypeUrl()).To(Equal("type.googleapis.com/google.protobuf.StringValue"))
	})

	It("sets a template parameter with integer value", func() {
		spec := &publicv1.ClusterSpec{}
		err := ApplyFields(spec, []string{"template_parameters.vlan=99"})
		Expect(err).ToNot(HaveOccurred())
		tp := spec.GetTemplateParameters()
		Expect(tp).To(HaveKey("vlan"))
		Expect(tp["vlan"].GetTypeUrl()).To(Equal("type.googleapis.com/google.protobuf.Int64Value"))
	})

	It("sets a template parameter with boolean value", func() {
		spec := &publicv1.ClusterSpec{}
		err := ApplyFields(spec, []string{"template_parameters.debug=true"})
		Expect(err).ToNot(HaveOccurred())
		tp := spec.GetTemplateParameters()
		Expect(tp).To(HaveKey("debug"))
		Expect(tp["debug"].GetTypeUrl()).To(Equal("type.googleapis.com/google.protobuf.BoolValue"))
	})

	It("applies multiple fields", func() {
		spec := &publicv1.ClusterSpec{}
		err := ApplyFields(spec, []string{
			"pull_secret=my-secret",
			"ssh_public_key=ssh-ed25519 AAAA",
			"template_parameters.vpc_id=vpc-456",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetPullSecret()).To(Equal("my-secret"))
		Expect(spec.GetSshPublicKey()).To(Equal("ssh-ed25519 AAAA"))
		Expect(spec.GetTemplateParameters()).To(HaveKey("vpc_id"))
	})

	It("does nothing with empty fields", func() {
		spec := publicv1.ClusterSpec_builder{
			CatalogItem: "cat-123",
		}.Build()
		err := ApplyFields(spec, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetCatalogItem()).To(Equal("cat-123"))
	})

	It("returns error for invalid format", func() {
		spec := &publicv1.ClusterSpec{}
		err := ApplyFields(spec, []string{"badformat"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("KEY=VALUE"))
	})

	It("preserves values with equals signs", func() {
		spec := &publicv1.ClusterSpec{}
		err := ApplyFields(spec, []string{"pull_secret=eyJhdXRocw=="})
		Expect(err).ToNot(HaveOccurred())
		Expect(spec.GetPullSecret()).To(Equal("eyJhdXRocw=="))
	})
})
