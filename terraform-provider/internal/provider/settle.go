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

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	gen "github.com/osac-project/osac/terraform-provider/internal/gen/resource_bare_metal_instance"
)

// useStateForUnknown recursively adds the UseStateForUnknown plan modifier to every computed attribute of a
// generated resource schema. The generator marks everything as optional and computed, and the attributes the
// configuration leaves unset would otherwise be planned as unknown, so every plan of an unchanged object would
// show them as "known after apply" and trigger a pointless update. Nested objects get the modifier both on the
// object itself — for the case where the configuration leaves the whole object unset — and on their attributes,
// for the case where the configuration sets only some of them.
func useStateForUnknown(generated schema.Schema) schema.Schema {
	for name, attribute := range generated.Attributes {
		generated.Attributes[name] = useStateForUnknownAttribute(attribute)
	}
	return generated
}

// useStateForUnknownAttribute adds the UseStateForUnknown plan modifier to the attribute when it is computed,
// recursing into the attributes of nested objects.
func useStateForUnknownAttribute(attribute schema.Attribute) schema.Attribute {
	switch typed := attribute.(type) {
	case schema.StringAttribute:
		if typed.Computed {
			typed.PlanModifiers = append(typed.PlanModifiers, stringplanmodifier.UseStateForUnknown())
		}
		return typed
	case schema.BoolAttribute:
		if typed.Computed {
			typed.PlanModifiers = append(typed.PlanModifiers, boolplanmodifier.UseStateForUnknown())
		}
		return typed
	case schema.Int64Attribute:
		if typed.Computed {
			typed.PlanModifiers = append(typed.PlanModifiers, int64planmodifier.UseStateForUnknown())
		}
		return typed
	case schema.ListAttribute:
		if typed.Computed {
			typed.PlanModifiers = append(typed.PlanModifiers, listplanmodifier.UseStateForUnknown())
		}
		return typed
	case schema.MapAttribute:
		if typed.Computed {
			typed.PlanModifiers = append(typed.PlanModifiers, mapplanmodifier.UseStateForUnknown())
		}
		return typed
	case schema.SingleNestedAttribute:
		if typed.Computed {
			typed.PlanModifiers = append(typed.PlanModifiers, objectplanmodifier.UseStateForUnknown())
		}
		for name, nested := range typed.Attributes {
			typed.Attributes[name] = useStateForUnknownAttribute(nested)
		}
		return typed
	case schema.ListNestedAttribute:
		if typed.Computed {
			typed.PlanModifiers = append(typed.PlanModifiers, listplanmodifier.UseStateForUnknown())
		}
		for name, nested := range typed.NestedObject.Attributes {
			typed.NestedObject.Attributes[name] = useStateForUnknownAttribute(nested)
		}
		return typed
	case schema.MapNestedAttribute:
		if typed.Computed {
			typed.PlanModifiers = append(typed.PlanModifiers, mapplanmodifier.UseStateForUnknown())
		}
		for name, nested := range typed.NestedObject.Attributes {
			typed.NestedObject.Attributes[name] = useStateForUnknownAttribute(nested)
		}
		return typed
	default:
		return attribute
	}
}

// settleModel replaces the unknown values of a just-created or just-updated model with nulls, so that it can be
// saved as state: the framework doesn't allow unknown values in the state, and the computed attributes that the
// server doesn't return in the create and update responses are left unknown by the plan.
func settleModel(ctx context.Context, model *gen.BareMetalInstanceModel) {
	if value, ok := settle(ctx, model.Metadata).(gen.MetadataValue); ok {
		model.Metadata = value
	}
	if value, ok := settle(ctx, model.Spec).(gen.SpecValue); ok {
		model.Spec = value
	}
	if value, ok := settle(ctx, model.Status).(gen.StatusValue); ok {
		model.Status = value
	}
	if model.Id.IsUnknown() {
		model.Id = types.StringNull()
	}
}

// settle recursively replaces the unknown values with nulls, keeping the known ones.
func settle(ctx context.Context, value attr.Value) attr.Value {
	if value.IsNull() || value.IsUnknown() {
		if value.IsUnknown() {
			return nullValue(ctx, value)
		}
		return value
	}
	if object, ok := value.(basetypes.ObjectValuable); ok {
		contents, _ := object.ToObjectValue(ctx)
		attributes := contents.Attributes()
		settled := make(map[string]attr.Value, len(attributes))
		changed := false
		for name, attribute := range attributes {
			result := settle(ctx, attribute)
			if !result.Equal(attribute) {
				changed = true
			}
			settled[name] = result
		}
		if !changed {
			return value
		}
		objectType := value.Type(ctx)
		if custom, ok := objectType.(basetypes.ObjectTypable); ok {
			attributeTypes := objectType.(attr.TypeWithAttributeTypes).AttributeTypes()
			newObject, _ := types.ObjectValue(attributeTypes, settled)
			result, _ := custom.ValueFromObject(ctx, newObject)
			return result
		}
		newObject, _ := types.ObjectValue(
			objectType.(attr.TypeWithAttributeTypes).AttributeTypes(), settled)
		return newObject
	}
	if list, ok := value.(basetypes.ListValuable); ok {
		contents, _ := list.ToListValue(ctx)
		elements := contents.Elements()
		settled := make([]attr.Value, len(elements))
		changed := false
		for i, element := range elements {
			result := settle(ctx, element)
			if !result.Equal(element) {
				changed = true
			}
			settled[i] = result
		}
		if !changed {
			return value
		}
		newList, _ := types.ListValue(contents.ElementType(ctx), settled)
		return newList
	}
	if mapping, ok := value.(basetypes.MapValuable); ok {
		contents, _ := mapping.ToMapValue(ctx)
		elements := contents.Elements()
		settled := make(map[string]attr.Value, len(elements))
		changed := false
		for name, element := range elements {
			result := settle(ctx, element)
			if !result.Equal(element) {
				changed = true
			}
			settled[name] = result
		}
		if !changed {
			return value
		}
		newMap, _ := types.MapValue(contents.ElementType(ctx), settled)
		return newMap
	}
	return value
}

// nullValue returns the null value of the same type as the given value.
func nullValue(ctx context.Context, value attr.Value) attr.Value {
	valueType := value.Type(ctx)
	terraformType := valueType.TerraformType(ctx)
	result, err := valueType.ValueFromTerraform(ctx, tftypes.NewValue(terraformType, nil))
	if err != nil {
		return value
	}
	return result
}
