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
	DescribeTable("validates generic Ethernet east-west profile references", func(manager, profile string, valid bool) {
		bindings := privatev1.BareMetalFabricBindings_builder{
			EthernetEw: map[string]string{manager: profile},
		}.Build()
		err := protovalidate.Validate(bindings)
		if valid {
			Expect(err).ToNot(HaveOccurred())
		} else {
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ethernet_ew"))
		}
	},
		Entry("Netris template ID", "netris", "1", true),
		Entry("opaque profile reference", "other-manager", "profile/hgx-v1", true),
		Entry("empty manager name", "", "profile", false),
		Entry("empty profile reference", "netris", "", false),
	)

	It("allows no configured Ethernet manager profiles", func() {
		Expect(protovalidate.Validate(&privatev1.BareMetalFabricBindings{})).To(Succeed())
	})

	It("keeps bindings and their messages out of the public schema", func() {
		spec := (&publicv1.BareMetalInstanceTypeSpec{}).ProtoReflect().Descriptor()
		Expect(spec.Fields().ByName("fabric_bindings")).To(BeNil())
		for _, name := range []protoreflect.Name{"BareMetalFabricBindings"} {
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
