//
// Copyright (c) 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
// the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package provider

import (
	"context"
	"fmt"
	"math/big"

	"encoding/json/jsontext"
	json "encoding/json/v2"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// valueOfJSON converts a JSON value, taken directly from a REST response, into a generated custom value type. The
// REST gateway marshals with the same protojson options that the schema generation uses, so no intermediate
// representation is needed.
func valueOfJSON[V attr.Value](ctx context.Context, data jsontext.Value, zero V) V {
	var decoded any
	err := json.Unmarshal(data, &decoded)
	if err != nil {
		return nullTyped(ctx, zero)
	}
	valueType := zero.Type(ctx)
	terraformValue, err := jsonToTerraformValue(ctx, decoded, valueType.TerraformType(ctx))
	if err != nil {
		return nullTyped(ctx, zero)
	}
	value, err := valueType.ValueFromTerraform(ctx, terraformValue)
	if err != nil {
		return nullTyped(ctx, zero)
	}
	result, _ := value.(V)
	return result
}

// nullTyped returns the null value of the type of the given generated value.
func nullTyped[V attr.Value](ctx context.Context, zero V) V {
	result, _ := nullValueOf(ctx, zero.Type(ctx)).(V)
	return result
}

// nullValueOf returns the null value of the given type.
func nullValueOf(ctx context.Context, valueType attr.Type) attr.Value {
	result, err := valueType.ValueFromTerraform(
		ctx, tftypes.NewValue(valueType.TerraformType(ctx), nil),
	)
	if err != nil {
		return nil
	}
	return result
}

// jsonToTerraformValue converts a value decoded from JSON into a Terraform value of the given Terraform type. Keys
// that are absent from the JSON object, which the gateway omits for unpopulated fields, become null values.
func jsonToTerraformValue(ctx context.Context, data any, terraformType tftypes.Type) (tftypes.Value, error) {
	if data == nil {
		return tftypes.NewValue(terraformType, nil), nil
	}
	if terraformType.Is(tftypes.String) {
		text, ok := data.(string)
		if !ok {
			return tftypes.Value{}, fmt.Errorf("expected a string, but got %T", data)
		}
		return tftypes.NewValue(tftypes.String, text), nil
	}
	if terraformType.Is(tftypes.Bool) {
		flag, ok := data.(bool)
		if !ok {
			return tftypes.Value{}, fmt.Errorf("expected a boolean, but got %T", data)
		}
		return tftypes.NewValue(tftypes.Bool, flag), nil
	}
	if terraformType.Is(tftypes.Number) {
		switch number := data.(type) {
		case float64:
			return tftypes.NewValue(tftypes.Number, number), nil
		case string:
			return tftypes.NewValue(tftypes.Number, number), nil
		default:
			return tftypes.Value{}, fmt.Errorf("expected a number, but got %T", data)
		}
	}
	switch typedType := terraformType.(type) {
	case tftypes.Object:
		object, ok := data.(map[string]any)
		if !ok {
			return tftypes.Value{}, fmt.Errorf("expected an object, but got %T", data)
		}
		attributes := make(map[string]tftypes.Value, len(typedType.AttributeTypes))
		for name, attributeType := range typedType.AttributeTypes {
			value, err := jsonToTerraformValue(ctx, object[name], attributeType)
			if err != nil {
				return tftypes.Value{}, fmt.Errorf("%s: %w", name, err)
			}
			attributes[name] = value
		}
		return tftypes.NewValue(typedType, attributes), nil
	case tftypes.List:
		items, ok := data.([]any)
		if !ok {
			return tftypes.Value{}, fmt.Errorf("expected a list, but got %T", data)
		}
		elementType := typedType.ElementType
		elements := make([]tftypes.Value, 0, len(items))
		for _, item := range items {
			value, err := jsonToTerraformValue(ctx, item, elementType)
			if err != nil {
				return tftypes.Value{}, err
			}
			elements = append(elements, value)
		}
		return tftypes.NewValue(typedType, elements), nil
	case tftypes.Map:
		entries, ok := data.(map[string]any)
		if !ok {
			return tftypes.Value{}, fmt.Errorf("expected a map, but got %T", data)
		}
		elementType := typedType.ElementType
		values := make(map[string]tftypes.Value, len(entries))
		for name, entry := range entries {
			value, err := jsonToTerraformValue(ctx, entry, elementType)
			if err != nil {
				return tftypes.Value{}, fmt.Errorf("%s: %w", name, err)
			}
			values[name] = value
		}
		return tftypes.NewValue(typedType, values), nil
	default:
		return tftypes.Value{}, fmt.Errorf("unsupported Terraform type %s", terraformType)
	}
}

// terraformToJSON converts a Terraform value into a JSON encodable value. Null and unknown values become nil, so
// that callers can omit them from request bodies.
func terraformToJSON(ctx context.Context, value tftypes.Value) (any, error) {
	if !value.IsKnown() || value.IsNull() {
		return nil, nil
	}
	terraformType := value.Type()
	if terraformType.Is(tftypes.String) {
		var result string
		if err := value.As(&result); err != nil {
			return nil, err
		}
		return result, nil
	}
	if terraformType.Is(tftypes.Bool) {
		var result bool
		if err := value.As(&result); err != nil {
			return nil, err
		}
		return result, nil
	}
	if terraformType.Is(tftypes.Number) {
		var result *big.Float
		if err := value.As(&result); err != nil {
			return nil, err
		}
		return jsontext.Value(result.Text('f', -1)), nil
	}
	switch terraformType.(type) {
	case tftypes.Object:
		var attributes map[string]tftypes.Value
		if err := value.As(&attributes); err != nil {
			return nil, err
		}
		result := make(map[string]any, len(attributes))
		for name, attribute := range attributes {
			converted, err := terraformToJSON(ctx, attribute)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			if converted == nil {
				continue
			}
			result[name] = converted
		}
		return result, nil
	case tftypes.List, tftypes.Set, tftypes.Tuple:
		var elements []tftypes.Value
		if err := value.As(&elements); err != nil {
			return nil, err
		}
		result := make([]any, 0, len(elements))
		for _, element := range elements {
			converted, err := terraformToJSON(ctx, element)
			if err != nil {
				return nil, err
			}
			if converted == nil {
				continue
			}
			result = append(result, converted)
		}
		return result, nil
	case tftypes.Map:
		var entries map[string]tftypes.Value
		if err := value.As(&entries); err != nil {
			return nil, err
		}
		result := make(map[string]any, len(entries))
		for name, entry := range entries {
			converted, err := terraformToJSON(ctx, entry)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			if converted == nil {
				continue
			}
			result[name] = converted
		}
		return result, nil
	}
	return nil, fmt.Errorf("unsupported Terraform type %s", terraformType)
}
