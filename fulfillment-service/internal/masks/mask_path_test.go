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
	"encoding/json"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	testsv1 "github.com/osac-project/fulfillment-service/internal/api/osac/tests/v1"
)

var _ = Describe("Path", func() {
	var compiler *PathCompiler[*testsv1.Object]

	BeforeEach(func() {
		var err error
		compiler, err = NewPathCompiler[*testsv1.Object]().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	DescribeTable(
		"Get field",
		func(object *testsv1.Object, path string, expected any) {
			// Convert the expected value to a protoreflect value, so that the tests cases below will be
			// simpler:
			var expectedValue protoreflect.Value
			switch expected := expected.(type) {
			case proto.Message:
				expectedValue = protoreflect.ValueOfMessage(expected.ProtoReflect())
			default:
				expectedValue = protoreflect.ValueOf(expected)
			}

			// Compile the path:
			compiledPath, err := compiler.Compile(path)
			Expect(err).ToNot(HaveOccurred())

			// Get the value:
			actualValue, ok := compiledPath.Get(object)
			Expect(ok).To(BeTrue())

			// Convert the expected and actual values to JSON, so that we can compare them as JSON documents
			// as that generates better error messages when there are differeces.
			convert := func(value protoreflect.Value) []byte {
				var (
					data []byte
					err  error
				)
				switch value := value.Interface().(type) {
				case proto.Message:
					data, err = protojson.Marshal(value)
				default:
					data, err = json.Marshal(value)
				}
				Expect(err).ToNot(HaveOccurred())
				return data
			}
			expectedJson := convert(expectedValue)
			actualJson := convert(actualValue)

			// Compare actual and expected:
			Expect(actualJson).To(MatchJSON(expectedJson))
		},
		Entry(
			"String",
			testsv1.Object_builder{
				MyString: "my value",
			}.Build(),
			"my_string",
			"my value",
		),
		Entry(
			"Boolean",
			testsv1.Object_builder{
				MyBool: true,
			}.Build(),
			"my_bool",
			true,
		),
		Entry(
			"32 bits integer",
			testsv1.Object_builder{
				MyInt32: int32(123),
			}.Build(),
			"my_int32",
			int32(123),
		),
		Entry(
			"64 bits integer",
			testsv1.Object_builder{
				MyInt64: int64(123),
			}.Build(),
			"my_int64",
			int64(123),
		),
		Entry(
			"Message",
			testsv1.Object_builder{
				MyMsg: testsv1.Object_builder{}.Build(),
			}.Build(),
			"my_msg",
			testsv1.Object_builder{}.Build(),
		),
		Entry(
			"Nested string",
			testsv1.Object_builder{
				MyMsg: testsv1.Object_builder{
					MyString: "my_value",
				}.Build(),
			}.Build(),
			"my_msg.my_string",
			"my_value",
		),
		Entry(
			"Nested boolean",
			testsv1.Object_builder{
				MyMsg: testsv1.Object_builder{
					MyBool: true,
				}.Build(),
			}.Build(),
			"my_msg.my_bool",
			true,
		),
		Entry(
			"Nested 32 bits integer",
			testsv1.Object_builder{
				MyMsg: testsv1.Object_builder{
					MyInt32: int32(123),
				}.Build(),
			}.Build(),
			"my_msg.my_int32",
			int32(123),
		),
		Entry(
			"Nested 64 bits integer",
			testsv1.Object_builder{
				MyMsg: testsv1.Object_builder{
					MyInt64: int64(123),
				}.Build(),
			}.Build(),
			"my_msg.my_int64",
			int64(123),
		),
		Entry(
			"List index, first value",
			testsv1.Object_builder{
				MyStringList: []string{
					"first value",
					"second value",
				},
			}.Build(),
			"my_string_list.0",
			"first value",
		),
		Entry(
			"List index, second value",
			testsv1.Object_builder{
				MyStringList: []string{
					"first value",
					"second value",
				},
			}.Build(),
			"my_string_list.1",
			"second value",
		),
		Entry(
			"Field of list index, first value",
			testsv1.Object_builder{
				MyRepeated: []*testsv1.Object{
					testsv1.Object_builder{
						MyString: "first value",
					}.Build(),
					testsv1.Object_builder{
						MyString: "second value",
					}.Build(),
				},
			}.Build(),
			"my_repeated.0.my_string",
			"first value",
		),
		Entry(
			"Field of list index, second value",
			testsv1.Object_builder{
				MyRepeated: []*testsv1.Object{
					testsv1.Object_builder{
						MyString: "first value",
					}.Build(),
					testsv1.Object_builder{
						MyString: "second value",
					}.Build(),
				},
			}.Build(),
			"my_repeated.1.my_string",
			"second value",
		),
		Entry(
			"Field of map entry, first value",
			testsv1.Object_builder{
				MyMap: map[string]*testsv1.Object{
					"first": testsv1.Object_builder{
						MyString: "first value",
					}.Build(),
					"second": testsv1.Object_builder{
						MyString: "second value",
					}.Build(),
				},
			}.Build(),
			"my_map.first.my_string",
			"first value",
		),
		Entry(
			"Field of map entry, second value",
			testsv1.Object_builder{
				MyMap: map[string]*testsv1.Object{
					"first": testsv1.Object_builder{
						MyString: "first value",
					}.Build(),
					"second": testsv1.Object_builder{
						MyString: "second value",
					}.Build(),
				},
			}.Build(),
			"my_map.second.my_string",
			"second value",
		),
		Entry(
			"Map key, first value",
			testsv1.Object_builder{
				MyStringMap: map[string]string{
					"first_key":  "first value",
					"second_key": "second value",
				},
			}.Build(),
			"my_string_map.first_key",
			"first value",
		),
		Entry(
			"Map key, second value",
			testsv1.Object_builder{
				MyStringMap: map[string]string{
					"first_key":  "first value",
					"second_key": "second value",
				},
			}.Build(),
			"my_string_map.second_key",
			"second value",
		),
	)

	DescribeTable(
		"Set field",
		func(path string, value any) {
			// Convert the value to a protoreflect value, so that the tests cases below will be simpler:
			var inputValue protoreflect.Value
			switch value := value.(type) {
			case proto.Message:
				inputValue = protoreflect.ValueOfMessage(value.ProtoReflect())
			default:
				inputValue = protoreflect.ValueOf(value)
			}

			// Compile the path:
			compiledPath, err := compiler.Compile(path)
			Expect(err).ToNot(HaveOccurred())

			// Set the value, and verify that it is set:
			object := testsv1.Object_builder{}.Build()
			compiledPath.Set(object, inputValue)
			outputValue, ok := compiledPath.Get(object)
			Expect(ok).To(BeTrue())

			// Convert the input and output values to JSON, so that we can compare them as JSON documents
			// as that generates better error messages when there are differeces.
			convert := func(value protoreflect.Value) []byte {
				var (
					data []byte
					err  error
				)
				switch value := value.Interface().(type) {
				case proto.Message:
					data, err = protojson.Marshal(value)
				default:
					data, err = json.Marshal(value)
				}
				Expect(err).ToNot(HaveOccurred())
				return data
			}
			inputJson := convert(inputValue)
			outputJson := convert(outputValue)

			// Compare actual and expected:
			Expect(inputJson).To(MatchJSON(outputJson))
		},
		Entry(
			"String",
			"my_string",
			"my value",
		),
		Entry(
			"Boolean",
			"my_bool",
			true,
		),
		Entry(
			"32 bits integer",
			"my_int32",
			int32(123),
		),
		Entry(
			"64 bits integer",
			"my_int64",
			int64(123),
		),
		Entry(
			"List index, first value",
			"my_string_list.0",
			"first value",
		),
		Entry(
			"List index, second value",
			"my_string_list.0",
			"second value",
		),
		Entry(
			"Nested string",
			"my_msg.my_string",
			"my value",
		),
		Entry(
			"Nested boolean",
			"my_msg.my_bool",
			true,
		),
		Entry(
			"Field of spec map entry",
			"spec.spec_map.mykey.my_string",
			"my value",
		),
	)

	DescribeTable(
		"Clear field",
		func(objectBuilder testsv1.Object_builder, path string, shouldExist bool) {
			// Build the initial object:
			object := objectBuilder.Build()

			// Compile the path:
			compiledPath, err := compiler.Compile(path)
			Expect(err).ToNot(HaveOccurred())

			// Verify that the field exists initially (if expected):
			if shouldExist {
				_, ok := compiledPath.Get(object)
				Expect(ok).To(BeTrue(), "Field should exist before clearing")
			}

			// Clear the field:
			compiledPath.Clear(object)

			// Verify that the field is cleared:
			value, ok := compiledPath.Get(object)
			if ok {
				// If the field still exists, it should be the zero value
				switch value.Interface().(type) {
				case string:
					Expect(value.String()).To(Equal(""))
				case bool:
					Expect(value.Bool()).To(BeFalse())
				case int32:
					Expect(value.Int()).To(Equal(int64(0)))
				case int64:
					Expect(value.Int()).To(Equal(int64(0)))
				case float32:
					Expect(value.Float()).To(Equal(float64(0)))
				case float64:
					Expect(value.Float()).To(Equal(float64(0)))
				default:
					// For messages and other complex types, we expect them to be cleared completely
					Fail("Unexpected type for cleared field")
				}
			}
		},
		Entry(
			"String field",
			testsv1.Object_builder{
				MyString: "my value",
			},
			"my_string",
			true,
		),
		Entry(
			"Boolean field",
			testsv1.Object_builder{
				MyBool: true,
			},
			"my_bool",
			true,
		),
		Entry(
			"32 bits integer field",
			testsv1.Object_builder{
				MyInt32: int32(123),
			},
			"my_int32",
			true,
		),
		Entry(
			"64 bits integer field",
			testsv1.Object_builder{
				MyInt64: int64(123),
			},
			"my_int64",
			true,
		),
		Entry(
			"Float field",
			testsv1.Object_builder{
				MyFloat: float32(123.45),
			},
			"my_float",
			true,
		),
		Entry(
			"Double field",
			testsv1.Object_builder{
				MyDouble: float64(123.45),
			},
			"my_double",
			true,
		),
		Entry(
			"Message field",
			testsv1.Object_builder{
				MyMsg: testsv1.Object_builder{
					MyString: "nested value",
				}.Build(),
			},
			"my_msg",
			true,
		),
		Entry(
			"Nested string field",
			testsv1.Object_builder{
				MyMsg: testsv1.Object_builder{
					MyString: "nested value",
				}.Build(),
			},
			"my_msg.my_string",
			true,
		),
		Entry(
			"Nested boolean field",
			testsv1.Object_builder{
				MyMsg: testsv1.Object_builder{
					MyBool: true,
				}.Build(),
			},
			"my_msg.my_bool",
			true,
		),
		Entry(
			"List element",
			testsv1.Object_builder{
				MyStringList: []string{
					"first value",
					"second value",
					"third value",
				},
			},
			"my_string_list.1",
			true,
		),
		Entry(
			"Field of list element",
			testsv1.Object_builder{
				MyRepeated: []*testsv1.Object{
					testsv1.Object_builder{
						MyString: "first value",
					}.Build(),
					testsv1.Object_builder{
						MyString: "second value",
					}.Build(),
				},
			},
			"my_repeated.0.my_string",
			true,
		),
		Entry(
			"Map entry",
			testsv1.Object_builder{
				MyStringMap: map[string]string{
					"first_key":  "first value",
					"second_key": "second value",
				},
			},
			"my_string_map.first_key",
			true,
		),
		Entry(
			"Field of map entry",
			testsv1.Object_builder{
				MyMap: map[string]*testsv1.Object{
					"first": testsv1.Object_builder{
						MyString: "first value",
					}.Build(),
					"second": testsv1.Object_builder{
						MyString: "second value",
					}.Build(),
				},
			},
			"my_map.first.my_string",
			true,
		),
		Entry(
			"Non-existent field",
			testsv1.Object_builder{},
			"my_string",
			false,
		),
		Entry(
			"Non-existent nested field",
			testsv1.Object_builder{},
			"my_msg.my_string",
			false,
		),
		Entry(
			"Non-existent list element",
			testsv1.Object_builder{
				MyStringList: []string{"first value"},
			},
			"my_string_list.5",
			false,
		),
		Entry(
			"Non-existent map key",
			testsv1.Object_builder{
				MyStringMap: map[string]string{
					"existing_key": "existing value",
				},
			},
			"my_string_map.non_existent_key",
			false,
		),
	)

	Describe("Clear field behavior verification", func() {
		It("Should remove map keys completely", func() {
			// Create object with map entries:
			object := testsv1.Object_builder{
				MyStringMap: map[string]string{
					"key1": "value1",
					"key2": "value2",
					"key3": "value3",
				},
			}.Build()

			// Compile path for one of the keys:
			compiledPath, err := compiler.Compile("my_string_map.key2")
			Expect(err).ToNot(HaveOccurred())

			// Verify key exists initially:
			_, ok := compiledPath.Get(object)
			Expect(ok).To(BeTrue())

			// Clear the key:
			compiledPath.Clear(object)

			// Verify the key is completely removed:
			_, ok = compiledPath.Get(object)
			Expect(ok).To(BeFalse())

			// Verify other keys still exist:
			key1Path, err := compiler.Compile("my_string_map.key1")
			Expect(err).ToNot(HaveOccurred())
			value1, ok := key1Path.Get(object)
			Expect(ok).To(BeTrue())
			Expect(value1.String()).To(Equal("value1"))

			key3Path, err := compiler.Compile("my_string_map.key3")
			Expect(err).ToNot(HaveOccurred())
			value3, ok := key3Path.Get(object)
			Expect(ok).To(BeTrue())
			Expect(value3.String()).To(Equal("value3"))
		})

		It("Should set list elements to zero values", func() {
			// Create object with list:
			object := testsv1.Object_builder{
				MyStringList: []string{
					"first",
					"second",
					"third",
				},
			}.Build()

			// Compile path for middle element:
			compiledPath, err := compiler.Compile("my_string_list.1")
			Expect(err).ToNot(HaveOccurred())

			// Verify element exists initially:
			value, ok := compiledPath.Get(object)
			Expect(ok).To(BeTrue())
			Expect(value.String()).To(Equal("second"))

			// Clear the element:
			compiledPath.Clear(object)

			// Verify the element is now empty but still exists:
			value, ok = compiledPath.Get(object)
			Expect(ok).To(BeTrue())
			Expect(value.String()).To(Equal(""))

			// Verify other elements are unchanged:
			firstPath, err := compiler.Compile("my_string_list.0")
			Expect(err).ToNot(HaveOccurred())
			firstValue, ok := firstPath.Get(object)
			Expect(ok).To(BeTrue())
			Expect(firstValue.String()).To(Equal("first"))

			thirdPath, err := compiler.Compile("my_string_list.2")
			Expect(err).ToNot(HaveOccurred())
			thirdValue, ok := thirdPath.Get(object)
			Expect(ok).To(BeTrue())
			Expect(thirdValue.String()).To(Equal("third"))
		})
	})
})
