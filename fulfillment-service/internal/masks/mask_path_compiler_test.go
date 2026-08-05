/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package masks

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	testsv1 "github.com/osac-project/fulfillment-service/internal/api/osac/tests/v1"
)

var _ = Describe("Path compiler", func() {
	Describe("Creation of a compiler", func() {
		It("Can be created with all the mandatory parameters", func() {
			compiler, err := NewPathCompiler[*testsv1.Object]().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(compiler).ToNot(BeNil())
		})
	})

	DescribeTable(
		"Successful compilation",
		func(path string, expected ...string) {
			compiler, err := NewPathCompiler[*testsv1.Object]().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(compiler).ToNot(BeNil())
			compiled, err := compiler.Compile(path)
			Expect(err).ToNot(HaveOccurred())
			steps := compiled.Steps()
			actual := make([]string, len(steps))
			for i, step := range steps {
				actual[i] = step.String()
			}
			Expect(actual).To(Equal(expected))
		},
		Entry(
			"Simple field",
			"my_string",
			"field(my_string)",
		),
		Entry(
			"Nested field",
			"my_msg.my_string",
			"field(my_msg)",
			"field(my_string)",
		),
		Entry(
			"Deeply nested field",
			"my_msg.my_msg.my_msg.my_bool",
			"field(my_msg)",
			"field(my_msg)",
			"field(my_msg)",
			"field(my_bool)",
		),
		Entry(
			"Map key",
			"my_map.my_key",
			"key(my_map, my_key)",
		),
		Entry(
			"Field of map entry",
			"my_map.my_key.my_bool",
			"key(my_map, my_key)",
			"field(my_bool)",
		),
		Entry(
			"List index",
			"my_repeated.0",
			"index(my_repeated, 0)",
		),
		Entry(
			"Field of list item",
			"my_repeated.0.my_string",
			"index(my_repeated, 0)",
			"field(my_string)",
		),
		Entry(
			"Field of spec",
			"spec.spec_string",
			"field(spec)",
			"field(spec_string)",
		),
		Entry(
			"Index of spec list",
			"spec.spec_list.0",
			"field(spec)",
			"index(spec_list, 0)",
		),
		Entry(
			"Entry of spec map",
			"spec.spec_map.mykey",
			"field(spec)",
			"key(spec_map, mykey)",
		),
		Entry(
			"Field of spec list item",
			"spec.spec_list.0.my_string",
			"field(spec)",
			"index(spec_list, 0)",
			"field(my_string)",
		),
		Entry(
			"Field of spec map entry",
			"spec.spec_map.mykey.my_string",
			"field(spec)",
			"key(spec_map, mykey)",
			"field(my_string)",
		),
	)
})
