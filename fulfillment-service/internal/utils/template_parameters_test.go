/*
Copyright (c) 2025 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package utils

import (
	"testing"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

func TestTemplateParameters(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Template parameters")
}

var _ = Describe("ValidateTemplateParameters", func() {
	var (
		template Template
		params   map[string]*anypb.Any
	)

	BeforeEach(func() {
		params = make(map[string]*anypb.Any)
	})

	Context("with valid parameters", func() {
		BeforeEach(func() {
			template = &mockTemplate{
				id: "test-template",
				parameters: []TemplateParameterDefinition{
					&mockParameter{
						name:      "required_param",
						required:  true,
						paramType: "type.googleapis.com/google.protobuf.StringValue",
					},
					&mockParameter{
						name:      "optional_param",
						required:  false,
						paramType: "type.googleapis.com/google.protobuf.StringValue",
					},
				},
			}
		})

		It("should pass validation when all required parameters are provided", func() {
			stringValue := wrapperspb.String("test-value")
			anyValue, err := anypb.New(stringValue)
			Expect(err).ToNot(HaveOccurred())
			params["required_param"] = anyValue

			err = ValidateTemplateParameters(template, params)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should pass validation when all parameters are provided", func() {
			stringValue1 := wrapperspb.String("required-value")
			anyValue1, err := anypb.New(stringValue1)
			Expect(err).ToNot(HaveOccurred())
			params["required_param"] = anyValue1

			stringValue2 := wrapperspb.String("optional-value")
			anyValue2, err := anypb.New(stringValue2)
			Expect(err).ToNot(HaveOccurred())
			params["optional_param"] = anyValue2

			err = ValidateTemplateParameters(template, params)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should pass validation when only required parameters are provided", func() {
			stringValue := wrapperspb.String("required-value")
			anyValue, err := anypb.New(stringValue)
			Expect(err).ToNot(HaveOccurred())
			params["required_param"] = anyValue

			err = ValidateTemplateParameters(template, params)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("with invalid parameters", func() {
		BeforeEach(func() {
			template = &mockTemplate{
				id: "test-template",
				parameters: []TemplateParameterDefinition{
					&mockParameter{
						name:      "valid_param",
						required:  true,
						paramType: "type.googleapis.com/google.protobuf.StringValue",
					},
				},
			}
		})

		It("should return error for invalid parameter name", func() {
			stringValue := wrapperspb.String("test-value")
			anyValue, err := anypb.New(stringValue)
			Expect(err).ToNot(HaveOccurred())
			params["invalid_param"] = anyValue

			err = ValidateTemplateParameters(template, params)
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("template parameter 'invalid_param' doesn't exist"))
			Expect(err.Error()).To(ContainSubstring("valid values for template 'test-template' are 'valid_param'"))
		})

		It("should return error for multiple invalid parameter names", func() {
			stringValue1 := wrapperspb.String("test-value1")
			anyValue1, err := anypb.New(stringValue1)
			Expect(err).ToNot(HaveOccurred())
			params["invalid_param1"] = anyValue1

			stringValue2 := wrapperspb.String("test-value2")
			anyValue2, err := anypb.New(stringValue2)
			Expect(err).ToNot(HaveOccurred())
			params["invalid_param2"] = anyValue2

			err = ValidateTemplateParameters(template, params)
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("template parameters 'invalid_param1' and 'invalid_param2' don't exist"))
		})

		It("should return error for missing required parameter", func() {
			err := ValidateTemplateParameters(template, params)
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("parameter 'valid_param' of template 'test-template' is mandatory"))
		})

		It("should return error for wrong parameter type", func() {
			intValue := wrapperspb.Int32(42)
			anyValue, err := anypb.New(intValue)
			Expect(err).ToNot(HaveOccurred())
			params["valid_param"] = anyValue

			err = ValidateTemplateParameters(template, params)
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("type of parameter 'valid_param' of template 'test-template' should be 'type.googleapis.com/google.protobuf.StringValue'"))
			Expect(err.Error()).To(ContainSubstring("but it is 'type.googleapis.com/google.protobuf.Int32Value'"))
		})
	})

	Context("with empty template", func() {
		BeforeEach(func() {
			template = &mockTemplate{
				id:         "empty-template",
				parameters: []TemplateParameterDefinition{},
			}
		})

		It("should return error for any provided parameters", func() {
			stringValue := wrapperspb.String("test-value")
			anyValue, err := anypb.New(stringValue)
			Expect(err).ToNot(HaveOccurred())
			params["any_param"] = anyValue

			err = ValidateTemplateParameters(template, params)
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("should pass validation with no parameters", func() {
			err := ValidateTemplateParameters(template, params)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("with real protobuf types", func() {
		BeforeEach(func() {
			clusterTemplate := &privatev1.ClusterTemplate{
				Id: "cluster-template",
				Parameters: []*privatev1.ClusterTemplateParameterDefinition{
					{
						Name:     "cluster_name",
						Required: true,
						Type:     "type.googleapis.com/google.protobuf.StringValue",
					},
					{
						Name:     "node_count",
						Required: false,
						Type:     "type.googleapis.com/google.protobuf.Int32Value",
					},
				},
			}
			template = ClusterTemplateAdapter{clusterTemplate}
		})

		It("should validate cluster template parameters correctly", func() {
			stringValue := wrapperspb.String("my-cluster")
			anyValue, err := anypb.New(stringValue)
			Expect(err).ToNot(HaveOccurred())
			params["cluster_name"] = anyValue

			err = ValidateTemplateParameters(template, params)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should validate compute instance template parameters correctly", func() {
			vmTemplate := &privatev1.ComputeInstanceTemplate{
				Id: "vm-template",
				Parameters: []*privatev1.ComputeInstanceTemplateParameterDefinition{
					{
						Name:     "vm_name",
						Required: true,
						Type:     "type.googleapis.com/google.protobuf.StringValue",
					},
				},
			}
			template = ComputeInstanceTemplateAdapter{vmTemplate}

			stringValue := wrapperspb.String("my-vm")
			anyValue, err := anypb.New(stringValue)
			Expect(err).ToNot(HaveOccurred())
			params["vm_name"] = anyValue

			err = ValidateTemplateParameters(template, params)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should validate bare metal instance template parameters correctly", func() {
			bmiTemplate := &privatev1.BareMetalInstanceTemplate{
				Id: "bmi-template",
				Parameters: []*privatev1.BareMetalInstanceTemplateParameterDefinition{
					{
						Name:     "os_version",
						Required: true,
						Type:     "type.googleapis.com/google.protobuf.StringValue",
					},
				},
			}
			template = BareMetalInstanceTemplateAdapter{bmiTemplate}

			stringValue := wrapperspb.String("rhel9.4")
			anyValue, err := anypb.New(stringValue)
			Expect(err).ToNot(HaveOccurred())
			params["os_version"] = anyValue

			err = ValidateTemplateParameters(template, params)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should process bare metal instance template parameters with defaults", func() {
			defaultStringValue := wrapperspb.String("rhel9.4")
			defaultAnyValue, err := anypb.New(defaultStringValue)
			Expect(err).ToNot(HaveOccurred())

			bmiTemplate := &privatev1.BareMetalInstanceTemplate{
				Id: "bmi-template",
				Parameters: []*privatev1.BareMetalInstanceTemplateParameterDefinition{
					{
						Name:     "os_version",
						Required: false,
						Type:     "type.googleapis.com/google.protobuf.StringValue",
						Default:  defaultAnyValue,
					},
				},
			}

			result := ProcessTemplateParametersWithDefaults(
				BareMetalInstanceTemplateAdapter{bmiTemplate}, nil,
			)
			Expect(result).To(HaveKey("os_version"))

			var resultString wrapperspb.StringValue
			err = result["os_version"].UnmarshalTo(&resultString)
			Expect(err).ToNot(HaveOccurred())
			Expect(resultString.Value).To(Equal("rhel9.4"))
		})
	})
})

var _ = Describe("ProcessTemplateParametersWithDefaults", func() {
	var (
		template Template
		params   map[string]*anypb.Any
	)

	BeforeEach(func() {
		params = make(map[string]*anypb.Any)
	})

	Context("with default values", func() {
		BeforeEach(func() {
			defaultStringValue := wrapperspb.String("default-value")
			defaultAnyValue, err := anypb.New(defaultStringValue)
			Expect(err).ToNot(HaveOccurred())

			template = &mockTemplate{
				id: "test-template",
				parameters: []TemplateParameterDefinition{
					&mockParameter{
						name:         "required_param",
						required:     true,
						paramType:    "type.googleapis.com/google.protobuf.StringValue",
						defaultValue: defaultAnyValue,
					},
					&mockParameter{
						name:         "optional_param",
						required:     false,
						paramType:    "type.googleapis.com/google.protobuf.StringValue",
						defaultValue: defaultAnyValue,
					},
				},
			}
		})

		It("should use provided values when available", func() {
			providedStringValue := wrapperspb.String("provided-value")
			providedAnyValue, err := anypb.New(providedStringValue)
			Expect(err).ToNot(HaveOccurred())
			params["required_param"] = providedAnyValue

			result := ProcessTemplateParametersWithDefaults(template, params)

			Expect(result).To(HaveKey("required_param"))
			Expect(result).To(HaveKey("optional_param"))

			// Check that provided value is used
			var resultString wrapperspb.StringValue
			err = result["required_param"].UnmarshalTo(&resultString)
			Expect(err).ToNot(HaveOccurred())
			Expect(resultString.Value).To(Equal("provided-value"))

			// Check that default value is used for optional parameter
			err = result["optional_param"].UnmarshalTo(&resultString)
			Expect(err).ToNot(HaveOccurred())
			Expect(resultString.Value).To(Equal("default-value"))
		})

		It("should use default values when parameters are not provided", func() {
			result := ProcessTemplateParametersWithDefaults(template, params)

			Expect(result).To(HaveKey("required_param"))
			Expect(result).To(HaveKey("optional_param"))

			// Check that default values are used
			var resultString wrapperspb.StringValue
			err := result["required_param"].UnmarshalTo(&resultString)
			Expect(err).ToNot(HaveOccurred())
			Expect(resultString.Value).To(Equal("default-value"))

			err = result["optional_param"].UnmarshalTo(&resultString)
			Expect(err).ToNot(HaveOccurred())
			Expect(resultString.Value).To(Equal("default-value"))
		})

		It("should set correct type URLs", func() {
			result := ProcessTemplateParametersWithDefaults(template, params)

			Expect(result["required_param"].TypeUrl).To(Equal("type.googleapis.com/google.protobuf.StringValue"))
			Expect(result["optional_param"].TypeUrl).To(Equal("type.googleapis.com/google.protobuf.StringValue"))
		})
	})

	Context("with mixed parameter types", func() {
		BeforeEach(func() {
			defaultStringValue := wrapperspb.String("default-string")
			defaultStringAnyValue, err := anypb.New(defaultStringValue)
			Expect(err).ToNot(HaveOccurred())

			defaultIntValue := wrapperspb.Int32(42)
			defaultIntAnyValue, err := anypb.New(defaultIntValue)
			Expect(err).ToNot(HaveOccurred())

			template = &mockTemplate{
				id: "mixed-template",
				parameters: []TemplateParameterDefinition{
					&mockParameter{
						name:         "string_param",
						required:     false,
						paramType:    "type.googleapis.com/google.protobuf.StringValue",
						defaultValue: defaultStringAnyValue,
					},
					&mockParameter{
						name:         "int_param",
						required:     false,
						paramType:    "type.googleapis.com/google.protobuf.Int32Value",
						defaultValue: defaultIntAnyValue,
					},
				},
			}
		})

		It("should handle different parameter types correctly", func() {
			providedStringValue := wrapperspb.String("custom-string")
			providedStringAnyValue, err := anypb.New(providedStringValue)
			Expect(err).ToNot(HaveOccurred())
			params["string_param"] = providedStringAnyValue

			result := ProcessTemplateParametersWithDefaults(template, params)

			// Check string parameter
			var resultString wrapperspb.StringValue
			err = result["string_param"].UnmarshalTo(&resultString)
			Expect(err).ToNot(HaveOccurred())
			Expect(resultString.Value).To(Equal("custom-string"))

			// Check int parameter (should use default)
			var resultInt wrapperspb.Int32Value
			err = result["int_param"].UnmarshalTo(&resultInt)
			Expect(err).ToNot(HaveOccurred())
			Expect(resultInt.Value).To(Equal(int32(42)))
		})
	})

	Context("with empty template", func() {
		BeforeEach(func() {
			template = &mockTemplate{
				id:         "empty-template",
				parameters: []TemplateParameterDefinition{},
			}
		})

		It("should return empty map", func() {
			result := ProcessTemplateParametersWithDefaults(template, params)
			Expect(result).To(BeEmpty())
		})
	})

	Context("with real protobuf types", func() {
		BeforeEach(func() {
			defaultStringValue := wrapperspb.String("default-cluster")
			defaultStringAnyValue, err := anypb.New(defaultStringValue)
			Expect(err).ToNot(HaveOccurred())

			defaultIntValue := wrapperspb.Int32(3)
			defaultIntAnyValue, err := anypb.New(defaultIntValue)
			Expect(err).ToNot(HaveOccurred())

			clusterTemplate := &privatev1.ClusterTemplate{
				Id: "cluster-template",
				Parameters: []*privatev1.ClusterTemplateParameterDefinition{
					{
						Name:     "cluster_name",
						Required: false,
						Type:     "type.googleapis.com/google.protobuf.StringValue",
						Default:  defaultStringAnyValue,
					},
					{
						Name:     "node_count",
						Required: false,
						Type:     "type.googleapis.com/google.protobuf.Int32Value",
						Default:  defaultIntAnyValue,
					},
				},
			}
			template = ClusterTemplateAdapter{clusterTemplate}
		})

		It("should process cluster template parameters with defaults", func() {
			providedStringValue := wrapperspb.String("my-cluster")
			providedStringAnyValue, err := anypb.New(providedStringValue)
			Expect(err).ToNot(HaveOccurred())
			params["cluster_name"] = providedStringAnyValue

			result := ProcessTemplateParametersWithDefaults(template, params)

			Expect(result).To(HaveKey("cluster_name"))
			Expect(result).To(HaveKey("node_count"))

			// Check provided value
			var resultString wrapperspb.StringValue
			err = result["cluster_name"].UnmarshalTo(&resultString)
			Expect(err).ToNot(HaveOccurred())
			Expect(resultString.Value).To(Equal("my-cluster"))

			// Check default value
			var resultInt wrapperspb.Int32Value
			err = result["node_count"].UnmarshalTo(&resultInt)
			Expect(err).ToNot(HaveOccurred())
			Expect(resultInt.Value).To(Equal(int32(3)))
		})
	})

	It("Ignores optional parameter that has no default value", func() {
		template = &mockTemplate{
			id: "my_template",
			parameters: []TemplateParameterDefinition{
				&mockParameter{
					name:         "my_parameter",
					paramType:    "type.googleapis.com/google.protobuf.StringValue",
					required:     false,
					defaultValue: nil,
				},
			},
		}
		result := ProcessTemplateParametersWithDefaults(template, nil)
		Expect(result).To(BeEmpty())
	})
})

var _ = Describe("ConvertTemplateParametersToJSON", func() {
	var params map[string]*anypb.Any

	BeforeEach(func() {
		params = make(map[string]*anypb.Any)
	})

	Context("with empty parameters", func() {
		It("should return empty JSON object", func() {
			result, err := ConvertTemplateParametersToJSON(params)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("{}"))
		})
	})

	Context("with string parameters", func() {
		BeforeEach(func() {
			stringValue, err := anypb.New(wrapperspb.String("test-value"))
			Expect(err).ToNot(HaveOccurred())
			params["string_param"] = stringValue

			emptyStringValue, err := anypb.New(wrapperspb.String(""))
			Expect(err).ToNot(HaveOccurred())
			params["empty_string_param"] = emptyStringValue
		})

		It("should convert string parameters correctly", func() {
			result, err := ConvertTemplateParametersToJSON(params)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(ContainSubstring(`"string_param":"test-value"`))
			Expect(result).To(ContainSubstring(`"empty_string_param":""`))
		})
	})

	Context("with numeric parameters", func() {
		BeforeEach(func() {
			intValue, err := anypb.New(wrapperspb.Int32(42))
			Expect(err).ToNot(HaveOccurred())
			params["int_param"] = intValue

			doubleValue, err := anypb.New(wrapperspb.Double(3.14))
			Expect(err).ToNot(HaveOccurred())
			params["double_param"] = doubleValue

			boolValue, err := anypb.New(wrapperspb.Bool(true))
			Expect(err).ToNot(HaveOccurred())
			params["bool_param"] = boolValue
		})

		It("should convert numeric parameters correctly", func() {
			result, err := ConvertTemplateParametersToJSON(params)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(ContainSubstring(`"int_param":42`))
			Expect(result).To(ContainSubstring(`"double_param":3.14`))
			Expect(result).To(ContainSubstring(`"bool_param":true`))
		})
	})

	Context("with complex parameters", func() {
		BeforeEach(func() {
			// Test with a nested object-like structure
			nestedValue, err := anypb.New(wrapperspb.String(`{"nested": {"key": "value"}}`))
			Expect(err).ToNot(HaveOccurred())
			params["nested_param"] = nestedValue

			// Test with array-like structure
			arrayValue, err := anypb.New(wrapperspb.String(`["item1", "item2", "item3"]`))
			Expect(err).ToNot(HaveOccurred())
			params["array_param"] = arrayValue
		})

		It("should convert complex parameters correctly", func() {
			result, err := ConvertTemplateParametersToJSON(params)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(ContainSubstring(`"nested_param":"{\"nested\": {\"key\": \"value\"}}"`))
			Expect(result).To(ContainSubstring(`"array_param":"[\"item1\", \"item2\", \"item3\"]"`))
		})
	})

	Context("with mixed parameter types", func() {
		BeforeEach(func() {
			stringValue, err := anypb.New(wrapperspb.String("test"))
			Expect(err).ToNot(HaveOccurred())
			params["string_param"] = stringValue

			intValue, err := anypb.New(wrapperspb.Int32(123))
			Expect(err).ToNot(HaveOccurred())
			params["int_param"] = intValue

			boolValue, err := anypb.New(wrapperspb.Bool(false))
			Expect(err).ToNot(HaveOccurred())
			params["bool_param"] = boolValue
		})

		It("should convert mixed parameters correctly", func() {
			result, err := ConvertTemplateParametersToJSON(params)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(ContainSubstring(`"string_param":"test"`))
			Expect(result).To(ContainSubstring(`"int_param":123`))
			Expect(result).To(ContainSubstring(`"bool_param":false`))
		})
	})

	Context("with invalid parameters", func() {
		BeforeEach(func() {
			// Create an invalid Any that will cause unmarshaling to fail
			invalidAny := &anypb.Any{
				TypeUrl: "invalid/type",
				Value:   []byte("invalid data"),
			}
			params["invalid_param"] = invalidAny
		})

		It("should return an error", func() {
			result, err := ConvertTemplateParametersToJSON(params)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to convert parameter 'invalid_param'"))
			Expect(result).To(BeEmpty())
		})
	})

	Context("with nil parameter value", func() {
		BeforeEach(func() {
			params["nil_param"] = nil
		})

		It("should handle nil parameters gracefully", func() {
			// This should not happen in practice as the map should not contain nil values
			// but let's test the behavior
			result, err := ConvertTemplateParametersToJSON(params)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to convert parameter 'nil_param'"))
			Expect(result).To(BeEmpty())
		})
	})

	Context("with special characters in parameter names", func() {
		BeforeEach(func() {
			stringValue, err := anypb.New(wrapperspb.String("value"))
			Expect(err).ToNot(HaveOccurred())
			params["param-with-dashes"] = stringValue
			params["param_with_underscores"] = stringValue
			params["param.with.dots"] = stringValue
		})

		It("should handle special characters in parameter names", func() {
			result, err := ConvertTemplateParametersToJSON(params)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(ContainSubstring(`"param-with-dashes":"value"`))
			Expect(result).To(ContainSubstring(`"param_with_underscores":"value"`))
			Expect(result).To(ContainSubstring(`"param.with.dots":"value"`))
		})
	})
})

// Mock implementations for testing

type mockTemplate struct {
	id         string
	parameters []TemplateParameterDefinition
}

func (m *mockTemplate) GetId() string {
	return m.id
}

func (m *mockTemplate) GetParameters() []TemplateParameterDefinition {
	return m.parameters
}

type mockParameter struct {
	name         string
	required     bool
	paramType    string
	defaultValue *anypb.Any
}

func (m *mockParameter) GetName() string {
	return m.name
}

func (m *mockParameter) GetRequired() bool {
	return m.required
}

func (m *mockParameter) GetType() string {
	return m.paramType
}

func (m *mockParameter) GetDefault() *anypb.Any {
	return m.defaultValue
}
