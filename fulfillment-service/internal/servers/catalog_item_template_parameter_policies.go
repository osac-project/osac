/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
*/

package servers

import (
	"sort"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/osac-project/osac/fulfillment-service/internal/utils"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// validateCatalogItemTemplateParameterPolicies checks that every governed parameter exists on
// the selected Template and that each locked/default value has the Template's declared type.
// For example, a Template integer parameter cannot receive a string default. It checks names
// in sorted order for stable errors and does not change the Template or policies.
func validateCatalogItemTemplateParameterPolicies(template utils.Template, policies map[string]*privatev1.TemplateParameterPolicy) error {
	definitions := make(map[string]utils.TemplateParameterDefinition)
	for _, parameter := range template.GetParameters() {
		definitions[parameter.GetName()] = parameter
	}
	names := make([]string, 0, len(policies))
	for name := range policies {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		field := "template_parameters." + name
		definition, ok := definitions[name]
		if !ok {
			return catalogItemPolicyError(field, "parameter is not defined by the template")
		}
		if err := validateCatalogItemTemplateParameterPolicy(definition, policies[name], field); err != nil {
			return err
		}
	}
	return nil
}

// applyCatalogItemTemplateParameterPolicies combines the caller's Template parameters with the
// Catalog Item's rules. For example, if "size" is locked to 2, a caller-supplied "size" is
// rejected; if it is editable with default 2, the caller's value wins and 2 is used only when
// omitted. The returned map is a copy; Template defaults and required-parameter checks run later.
func applyCatalogItemTemplateParameterPolicies(
	template utils.Template,
	policies map[string]*privatev1.TemplateParameterPolicy,
	provided map[string]*anypb.Any,
) (map[string]*anypb.Any, error) {
	if err := validateCatalogItemTemplateParameterPolicies(template, policies); err != nil {
		return nil, err
	}
	result := make(map[string]*anypb.Any, len(provided)+len(policies))
	for name, value := range provided {
		result[name] = cloneMessage(value)
	}
	names := make([]string, 0, len(policies))
	for name := range policies {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		policy := policies[name]
		_, supplied := provided[name]
		if supplied && policy.HasLocked() {
			return nil, catalogItemPolicyError("template_parameters."+name, "field is not editable")
		}
		if supplied {
			continue
		}
		if policy.HasLocked() {
			result[name] = cloneMessage(policy.GetLocked())
		} else if value := policy.GetEditable().GetDefaultValue(); value != nil {
			result[name] = cloneMessage(value)
		}
	}
	return result, nil
}

// validateCatalogItemTemplateParameterPolicy checks one policy against its Template definition.
// It validates the supported type, selected behavior, and encoded payload without changing the policy.
func validateCatalogItemTemplateParameterPolicy(
	definition utils.TemplateParameterDefinition,
	policy *privatev1.TemplateParameterPolicy,
	field string,
) error {
	var valueType proto.Message
	switch definition.GetType() {
	case "type.googleapis.com/google.protobuf.BoolValue":
		valueType = &wrapperspb.BoolValue{}
	case "type.googleapis.com/google.protobuf.Int32Value":
		valueType = &wrapperspb.Int32Value{}
	case "type.googleapis.com/google.protobuf.Int64Value":
		valueType = &wrapperspb.Int64Value{}
	case "type.googleapis.com/google.protobuf.FloatValue":
		valueType = &wrapperspb.FloatValue{}
	case "type.googleapis.com/google.protobuf.DoubleValue":
		valueType = &wrapperspb.DoubleValue{}
	case "type.googleapis.com/google.protobuf.StringValue":
		valueType = &wrapperspb.StringValue{}
	case "type.googleapis.com/google.protobuf.BytesValue":
		valueType = &wrapperspb.BytesValue{}
	case "type.googleapis.com/google.protobuf.Timestamp":
		valueType = &timestamppb.Timestamp{}
	case "type.googleapis.com/google.protobuf.Duration":
		valueType = &durationpb.Duration{}
	default:
		return catalogItemPolicyError(field, "template parameter type does not support catalog policies")
	}
	if policy == nil {
		return catalogItemPolicyError(field, "policy is required")
	}
	var value *anypb.Any
	switch {
	case policy.HasLocked():
		value = policy.GetLocked()
		if value == nil {
			return catalogItemPolicyError(field, "locked value is required")
		}
	case policy.HasEditable():
		if policy.GetEditable() == nil {
			return catalogItemPolicyError(field, "editable policy is required")
		}
		value = policy.GetEditable().GetDefaultValue()
	default:
		return catalogItemPolicyError(field, "policy has no behavior")
	}
	if value == nil {
		return nil
	}
	if value.GetTypeUrl() != definition.GetType() {
		return catalogItemPolicyError(field, "value type must match the template parameter type")
	}
	if err := anypb.UnmarshalTo(value, valueType, proto.UnmarshalOptions{}); err != nil {
		return catalogItemPolicyError(field, "invalid parameter payload")
	}
	if valid, ok := valueType.(interface{ CheckValid() error }); ok {
		if err := valid.CheckValid(); err != nil {
			return catalogItemPolicyError(field, err.Error())
		}
	}
	return nil
}
