/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package schema_test

import (
	"reflect"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/schema"
)

var _ = Describe("Lifecycle", func() {
	Describe("LifecycleDataFields", func() {
		It("returns exactly the JSON field names of LifecycleData in struct field order", func() {
			typ := reflect.TypeOf(schema.LifecycleData{})
			var structFields []string
			for i := range typ.NumField() {
				tag := typ.Field(i).Tag.Get("json")
				name := strings.Split(tag, ",")[0]
				if name == "billing_dimensions" {
					continue
				}
				structFields = append(structFields, name)
			}

			fields := schema.LifecycleDataFields()

			Expect(fields).To(HaveLen(len(structFields)),
				"LifecycleDataFields() count should match LifecycleData JSON fields (excluding billing_dimensions)")

			for i, want := range structFields {
				Expect(fields[i]).To(Equal(want),
					"LifecycleDataFields()[%d] should match struct field order", i)
			}
		})

		It("returns a fresh copy that cannot mutate the canonical list", func() {
			a := schema.LifecycleDataFields()
			a[0] = "mutated"
			b := schema.LifecycleDataFields()
			Expect(b[0]).NotTo(Equal("mutated"),
				"LifecycleDataFields() must return a fresh copy on each call")
		})
	})

	Describe("SchemaVersion", func() {
		It("is not empty", func() {
			Expect(schema.SchemaVersion).NotTo(BeEmpty())
		})
	})

	It("defines the billable networking resource types", func() {
		Expect(schema.ResourceTypeExternalIP).To(Equal("external_ip"))
		Expect(schema.ResourceTypeNATGateway).To(Equal("nat_gateway"))
	})
})
