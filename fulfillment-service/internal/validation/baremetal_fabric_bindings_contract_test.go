/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package validation

import (
	"buf.build/go/protovalidate"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Bare metal fabric bindings contract", func() {
	DescribeTable("validates canonical positive uint64 template IDs", func(id string, valid bool) {
		binding := privatev1.BareMetalNetrisFabricBinding_builder{
			NetworkClass: "network-class-id", TemplateId: id,
		}.Build()
		err := protovalidate.Validate(binding)
		if valid {
			Expect(err).ToNot(HaveOccurred())
		} else {
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("template_id"))
		}
	},
		Entry("minimum", "1", true),
		Entry("typical", "12345", true),
		Entry("above signed range", "9223372036854775808", true),
		Entry("uint64 maximum", "18446744073709551615", true),
		Entry("missing", "", false),
		Entry("zero", "0", false),
		Entry("leading zero", "01", false),
		Entry("negative", "-1", false),
		Entry("plus sign", "+1", false),
		Entry("space", " 1", false),
		Entry("trailing space", "1 ", false),
		Entry("decimal", "1.0", false),
		Entry("hexadecimal", "0x1", false),
		Entry("exponent", "1e3", false),
		Entry("name", "gpu-template", false),
		Entry("unicode digits", "１２", false),
		Entry("uint64 overflow", "18446744073709551616", false),
		Entry("too long", "100000000000000000000", false),
	)

	It("requires a NetworkClass ID when a Netris binding is present", func() {
		err := protovalidate.Validate(privatev1.BareMetalNetrisFabricBinding_builder{TemplateId: "1"}.Build())
		Expect(err).To(MatchError(ContainSubstring("network_class")))
	})

	It("allows cleared optional binding messages", func() {
		Expect(protovalidate.Validate(&privatev1.BareMetalFabricBindings{})).To(Succeed())
		Expect(protovalidate.Validate(privatev1.BareMetalFabricBindings_builder{
			EthernetEw: &privatev1.BareMetalEthernetFabricBinding{},
		}.Build())).To(Succeed())
	})

	It("keeps bindings and their messages out of the public schema", func() {
		spec := (&publicv1.BareMetalInstanceTypeSpec{}).ProtoReflect().Descriptor()
		Expect(spec.Fields().ByName("fabric_bindings")).To(BeNil())
		for _, name := range []protoreflect.Name{"BareMetalFabricBindings", "BareMetalEthernetFabricBinding", "BareMetalNetrisFabricBinding"} {
			Expect(spec.ParentFile().Messages().ByName(name)).To(BeNil())
		}
		privateSpec := (&privatev1.BareMetalInstanceTypeSpec{}).ProtoReflect().Descriptor()
		Expect(privateSpec.Fields().ByName("fabric_bindings").Number()).To(Equal(protoreflect.FieldNumber(4)))
	})

	It("reserves the removed NetworkClass template field", func() {
		config := (&privatev1.EthernetEastWestConfig{}).ProtoReflect().Descriptor()
		Expect(config.Fields().Len()).To(BeZero())
		Expect(config.ReservedNames().Has("template_id")).To(BeTrue())
		Expect(config.ReservedRanges().Has(1)).To(BeTrue())
	})

	It("does not add an instance type to FabricDomain", func() {
		for _, spec := range []protoreflect.MessageDescriptor{
			(&privatev1.FabricDomainSpec{}).ProtoReflect().Descriptor(),
			(&publicv1.FabricDomainSpec{}).ProtoReflect().Descriptor(),
		} {
			Expect(spec.Fields().ByName("instance_type")).To(BeNil())
		}
	})
	It("keeps FabricDomain hub placement private", func() {
		privateStatus := (&privatev1.FabricDomainStatus{}).ProtoReflect().Descriptor()
		Expect(privateStatus.Fields().ByName("hub").Number()).To(Equal(protoreflect.FieldNumber(5)))
		publicStatus := (&publicv1.FabricDomainStatus{}).ProtoReflect().Descriptor()
		Expect(publicStatus.Fields().ByName("hub")).To(BeNil())
	})

})
