package utils

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dustin/go-humanize/english"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

// TemplateParameterDefinition represents a common interface for template parameter definitions
type TemplateParameterDefinition interface {
	GetName() string
	GetRequired() bool
	GetType() string
	GetDefault() *anypb.Any
}

// Template represents a common interface for templates that have parameters
type Template interface {
	GetId() string
	GetParameters() []TemplateParameterDefinition
}

// ValidateTemplateParameters validates template parameters against a template definition.
// It checks that:
// 1. All specified parameters exist in the template
// 2. All mandatory parameters have values
// 3. Parameter types match the template definition
func ValidateTemplateParameters(
	template Template,
	providedParameters map[string]*anypb.Any,
) error {
	templateParameters := template.GetParameters()
	templateID := template.GetId()
	// Check that all the specified template parameters are in the template:
	var invalidParameterNames []string
	for parameterName := range providedParameters {
		parameterValid := false
		for _, templateParameter := range templateParameters {
			if templateParameter.GetName() == parameterName {
				parameterValid = true
				break
			}
		}
		if !parameterValid {
			invalidParameterNames = append(invalidParameterNames, parameterName)
		}
	}
	if len(invalidParameterNames) > 0 {
		templateParameterNames := make([]string, len(templateParameters))
		for i, templateParameter := range templateParameters {
			templateParameterNames[i] = templateParameter.GetName()
		}
		sort.Strings(templateParameterNames)
		for i, templateParameterName := range templateParameterNames {
			templateParameterNames[i] = fmt.Sprintf("'%s'", templateParameterName)
		}
		sort.Strings(invalidParameterNames)
		for i, invalidParameterName := range invalidParameterNames {
			invalidParameterNames[i] = fmt.Sprintf("'%s'", invalidParameterName)
		}
		if len(invalidParameterNames) == 1 {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"template parameter %s doesn't exist, valid values for template '%s' are %s",
				invalidParameterNames[0],
				templateID,
				english.WordSeries(templateParameterNames, "and"),
			)
		} else {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"template parameters %s don't exist, valid values for template '%s' are %s",
				english.WordSeries(invalidParameterNames, "and"),
				templateID,
				english.WordSeries(templateParameterNames, "and"),
			)
		}
	}

	// Check that all the mandatory parameters have a value:
	for _, templateParameter := range templateParameters {
		if !templateParameter.GetRequired() {
			continue
		}
		templateParameterName := templateParameter.GetName()
		providedParameter := providedParameters[templateParameterName]
		if providedParameter == nil {
			return grpcstatus.Errorf(
				grpccodes.InvalidArgument,
				"parameter '%s' of template '%s' is mandatory",
				templateParameterName, templateID,
			)
		}
	}

	// Check that the parameter values are compatible with the template:
	for parameterName, providedParameter := range providedParameters {
		for _, templateParameter := range templateParameters {
			templateParameterName := templateParameter.GetName()
			if parameterName != templateParameterName {
				continue
			}
			providedParameterType := providedParameter.GetTypeUrl()
			templateParameterType := templateParameter.GetType()
			if providedParameterType != templateParameterType {
				return grpcstatus.Errorf(
					grpccodes.InvalidArgument,
					"type of parameter '%s' of template '%s' should be '%s', "+
						"but it is '%s'",
					parameterName,
					templateID,
					templateParameterType,
					providedParameterType,
				)
			}
		}
	}

	return nil
}

// ProcessTemplateParametersWithDefaults processes template parameters by applying default values for parameters that
// are not provided and have a default value in the template definition. It returns a new map with all template
// parameters that have a value, either provided or default. Note that this doesn't mean *all* the parameters defined
// in the template, as some of them may be optional *and* have no default value.
func ProcessTemplateParametersWithDefaults(
	template Template,
	providedParameters map[string]*anypb.Any,
) map[string]*anypb.Any {
	actualParameters := make(map[string]*anypb.Any)
	templateParameters := template.GetParameters()

	for _, parameterDefinition := range templateParameters {
		parameterName := parameterDefinition.GetName()
		providedValue := providedParameters[parameterName]
		if providedValue != nil {
			actualParameters[parameterName] = proto.Clone(providedValue).(*anypb.Any)
			continue
		}
		defaultValue := parameterDefinition.GetDefault()
		if defaultValue != nil {
			actualParameters[parameterName] = proto.Clone(defaultValue).(*anypb.Any)
			continue
		}
	}

	return actualParameters
}

// ClusterTemplateAdapter adapts ClusterTemplate to the Template interface
type ClusterTemplateAdapter struct {
	*privatev1.ClusterTemplate
}

func (a ClusterTemplateAdapter) GetParameters() []TemplateParameterDefinition {
	params := a.ClusterTemplate.GetParameters()
	result := make([]TemplateParameterDefinition, len(params))
	for i, param := range params {
		result[i] = param
	}
	return result
}

// ComputeInstanceTemplateAdapter adapts ComputeInstanceTemplate to the Template interface
type ComputeInstanceTemplateAdapter struct {
	*privatev1.ComputeInstanceTemplate
}

func (a ComputeInstanceTemplateAdapter) GetParameters() []TemplateParameterDefinition {
	params := a.ComputeInstanceTemplate.GetParameters()
	result := make([]TemplateParameterDefinition, len(params))
	for i, param := range params {
		result[i] = param
	}
	return result
}

// BareMetalInstanceTemplateAdapter adapts BareMetalInstanceTemplate to the Template interface
type BareMetalInstanceTemplateAdapter struct {
	*privatev1.BareMetalInstanceTemplate
}

func (a BareMetalInstanceTemplateAdapter) GetParameters() []TemplateParameterDefinition {
	params := a.BareMetalInstanceTemplate.GetParameters()
	result := make([]TemplateParameterDefinition, len(params))
	for i, param := range params {
		result[i] = param
	}
	return result
}

// ValidateBareMetalInstanceTemplateParameters validates bare metal instance template parameters
func ValidateBareMetalInstanceTemplateParameters(
	template *privatev1.BareMetalInstanceTemplate,
	providedParameters map[string]*anypb.Any,
) error {
	return ValidateTemplateParameters(BareMetalInstanceTemplateAdapter{template}, providedParameters)
}

// ValidateClusterTemplateParameters validates cluster template parameters
func ValidateClusterTemplateParameters(
	template *privatev1.ClusterTemplate,
	providedParameters map[string]*anypb.Any,
) error {
	return ValidateTemplateParameters(ClusterTemplateAdapter{template}, providedParameters)
}

// ValidateComputeInstanceTemplateParameters validates compute instance template parameters
func ValidateComputeInstanceTemplateParameters(
	template *privatev1.ComputeInstanceTemplate,
	providedParameters map[string]*anypb.Any,
) error {
	return ValidateTemplateParameters(ComputeInstanceTemplateAdapter{template}, providedParameters)
}

// ConvertTemplateParametersToJSON converts template parameters from protobuf Any format to JSON string.
// This is used when preparing parameters for Kubernetes controllers that expect JSON format.
func ConvertTemplateParametersToJSON(templateParameters map[string]*anypb.Any) (string, error) {
	paramsJson := map[string]any{}
	for paramName, paramAny := range templateParameters {
		paramJson, err := convertTemplateParam(paramAny)
		if err != nil {
			return "", fmt.Errorf("failed to convert parameter '%s': %w", paramName, err)
		}
		paramsJson[paramName] = paramJson
	}
	paramsBytes, err := json.Marshal(paramsJson)
	if err != nil {
		return "", fmt.Errorf("failed to marshal parameters to JSON: %w", err)
	}
	return string(paramsBytes), nil
}

// convertTemplateParam converts a protobuf Any parameter to a JSON-compatible value.
func convertTemplateParam(paramAny *anypb.Any) (any, error) {
	paramMsg, err := paramAny.UnmarshalNew()
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal parameter: %w", err)
	}
	paramBytes, err := protojson.Marshal(paramMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal parameter to JSON: %w", err)
	}
	var paramValue any
	err = json.Unmarshal(paramBytes, &paramValue)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal parameter from JSON: %w", err)
	}
	return paramValue, nil
}
