/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/osac-project/osac/fulfillment-service/internal/maputil"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// catalogItem is implemented by ClusterCatalogItem, ComputeInstanceCatalogItem,
// and BareMetalInstanceCatalogItem.
type catalogItem interface {
	GetPublished() bool
	GetMetadata() *privatev1.Metadata
}

// validateFieldDefinitions checks that field definitions are well-formed:
//   - Non-editable fields must have a default value.
//   - Fields with a validation_schema must contain valid JSON.
func validateFieldDefinitions(fieldDefinitions []*privatev1.FieldDefinition) error {
	for _, fd := range fieldDefinitions {
		if !fd.GetEditable() && !fd.HasDefault() {
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"non-editable field '%s' must have a default value", fd.GetPath())
		}
		if schema := fd.GetValidationSchema(); schema != "" {
			var doc any
			if err := json.Unmarshal([]byte(schema), &doc); err != nil {
				return grpcstatus.Errorf(grpccodes.InvalidArgument,
					"field '%s' has invalid validation_schema: %v", fd.GetPath(), err)
			}
		}
	}
	return nil
}

// applyFieldDefinitions validates and applies field definitions from a catalog item against a resource spec.
// Rejects any spec field not listed in field_definitions (except system fields catalog_item and template).
// For non-editable fields: rejects user-provided values; applies the catalog item default.
// For editable fields with user values: validates against the JSON Schema.
// For editable fields without user values: applies the catalog item default.
func applyFieldDefinitions(
	spec proto.Message,
	fieldDefinitions []*privatev1.FieldDefinition,
) error {
	if len(fieldDefinitions) == 0 {
		return nil
	}

	marshaller := protojson.MarshalOptions{UseProtoNames: true}
	specJSON, err := marshaller.Marshal(spec)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to marshal spec: %v", err)
	}

	var specMap map[string]any
	if err := json.Unmarshal(specJSON, &specMap); err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to parse spec: %v", err)
	}

	allowedPaths := map[string]bool{
		"catalog_item": true,
		"template":     true,
	}
	for _, fd := range fieldDefinitions {
		if fd.GetPath() != "" {
			allowedPaths[fd.GetPath()] = true
		}
	}
	var unlisted []string
	for _, path := range collectLeafPaths(specMap, "") {
		if !isPathCovered(path, allowedPaths) {
			unlisted = append(unlisted, path)
		}
	}
	if len(unlisted) > 0 {
		slices.Sort(unlisted)
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"fields not allowed by catalog item: %s", strings.Join(unlisted, ", "))
	}

	compiler := jsonschema.NewCompiler()

	for _, fd := range fieldDefinitions {
		path := fd.GetPath()
		if path == "" {
			continue
		}

		defaultVal := fd.GetDefault()
		userVal, userHasValue := maputil.GetNestedValue(specMap, path)

		if !fd.GetEditable() {
			if userHasValue && userVal != nil {
				return grpcstatus.Errorf(grpccodes.InvalidArgument,
					"field '%s' is not editable", path)
			}
			if err := applyDefault(specMap, path, defaultVal); err != nil {
				return err
			}
		} else {
			if userHasValue && userVal != nil {
				schema := fd.GetValidationSchema()
				if schema != "" {
					if err := validateAgainstSchema(compiler, path, unwrapAnyValue(path, userVal), schema); err != nil {
						return err
					}
				}
			} else {
				if defaultVal == nil {
					return grpcstatus.Errorf(grpccodes.InvalidArgument,
						"field '%s' is required but no value was provided and no default is defined", path)
				}
				if err := applyDefault(specMap, path, defaultVal); err != nil {
					return err
				}
			}
		}
	}

	updatedJSON, err := json.Marshal(specMap)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to serialize updated spec: %v", err)
	}

	proto.Reset(spec)
	if err := protojson.Unmarshal(updatedJSON, spec); err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to apply updated spec: %v", err)
	}

	return nil
}

// normalizeDiskField wraps bare-string disk field values (disk_image, storage_tier)
// as typed reference objects for backward compatibility with legacy catalog item defaults.
func normalizeDiskField(v any, fieldName string) any {
	switch val := v.(type) {
	case string:
		return map[string]any{"name": val}
	case map[string]any:
		if fieldVal, ok := val[fieldName]; ok {
			if s, ok := fieldVal.(string); ok {
				val[fieldName] = map[string]any{"name": s}
			}
		}
		return val
	case []any:
		for i, elem := range val {
			val[i] = normalizeDiskField(elem, fieldName)
		}
		return val
	default:
		return v
	}
}

func applyDefault(specMap map[string]any, path string, defaultVal *structpb.Value) error {
	if defaultVal == nil {
		return nil
	}
	defaultAny, err := defaultVal.MarshalJSON()
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal,
			"failed to marshal default for field '%s': %v", path, err)
	}
	var parsed any
	if err := json.Unmarshal(defaultAny, &parsed); err != nil {
		return grpcstatus.Errorf(grpccodes.Internal,
			"failed to parse default for field '%s': %v", path, err)
	}
	if strings.HasPrefix(path, "template_parameters.") {
		parsed = wrapValueAsAny(parsed)
	}
	// Backward compatibility: normalize bare-string disk_image and storage_tier defaults.
	if path == "disk_image" || path == "boot_disk" || strings.HasPrefix(path, "additional_disks") {
		parsed = normalizeDiskField(parsed, "disk_image")
	}
	if path == "boot_disk" || path == "boot_disk.storage_tier" ||
		strings.HasPrefix(path, "additional_disks") {
		parsed = normalizeDiskField(parsed, "storage_tier")
	}
	maputil.SetNestedValue(specMap, path, parsed)
	return nil
}

// wrapValueAsAny wraps a Go value in the protobuf Any JSON format required by
// map<string, google.protobuf.Any> fields. Int64 values are encoded as strings
// per the proto3 JSON mapping.
func wrapValueAsAny(value any) map[string]any {
	switch v := value.(type) {
	case bool:
		return map[string]any{
			"@type": "type.googleapis.com/google.protobuf.BoolValue",
			"value": v,
		}
	case float64:
		if v == float64(int64(v)) {
			return map[string]any{
				"@type": "type.googleapis.com/google.protobuf.Int64Value",
				"value": fmt.Sprintf("%d", int64(v)),
			}
		}
		return map[string]any{
			"@type": "type.googleapis.com/google.protobuf.DoubleValue",
			"value": v,
		}
	default:
		return map[string]any{
			"@type": "type.googleapis.com/google.protobuf.StringValue",
			"value": fmt.Sprintf("%v", v),
		}
	}
}

// unwrapAnyValue extracts the inner value from a protobuf Any JSON wrapper
// for template_parameters paths. For other paths, returns the value unchanged.
func unwrapAnyValue(path string, val any) any {
	if !strings.HasPrefix(path, "template_parameters.") {
		return val
	}
	m, ok := val.(map[string]any)
	if !ok {
		return val
	}
	v, exists := m["value"]
	if !exists {
		return val
	}
	typeURL, _ := m["@type"].(string)
	if strings.HasSuffix(typeURL, "Int64Value") || strings.HasSuffix(typeURL, "UInt64Value") {
		if s, ok := v.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f
			}
		}
	}
	return v
}

func validateAgainstSchema(compiler *jsonschema.Compiler, path string, value any, schemaStr string) error {
	resourceName := "schema_" + strings.ReplaceAll(path, ".", "_") + ".json"
	var schemaDoc any
	if err := json.Unmarshal([]byte(schemaStr), &schemaDoc); err != nil {
		return grpcstatus.Errorf(grpccodes.Internal,
			"invalid validation schema for field '%s': %v", path, err)
	}
	if err := compiler.AddResource(resourceName, schemaDoc); err != nil {
		return grpcstatus.Errorf(grpccodes.Internal,
			"invalid validation schema for field '%s': %v", path, err)
	}
	schema, err := compiler.Compile(resourceName)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal,
			"failed to compile validation schema for field '%s': %v", path, err)
	}
	if err := schema.Validate(value); err != nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"validation failed for field '%s': %v", path, err)
	}
	return nil
}

func collectLeafPaths(m map[string]any, prefix string) []string {
	var paths []string
	for key, val := range m {
		fullPath := key
		if prefix != "" {
			fullPath = prefix + "." + key
		}
		if nested, ok := val.(map[string]any); ok {
			// Treat template_parameters entries as opaque leaves —
			// their inner @type/value keys are protobuf Any encoding details.
			if prefix == "template_parameters" {
				paths = append(paths, fullPath)
			} else {
				paths = append(paths, collectLeafPaths(nested, fullPath)...)
			}
		} else {
			paths = append(paths, fullPath)
		}
	}
	return paths
}

func isPathCovered(path string, allowedPaths map[string]bool) bool {
	if allowedPaths[path] {
		return true
	}
	for i := range path {
		if path[i] == '.' && allowedPaths[path[:i]] {
			return true
		}
	}
	return false
}

// validateCatalogItemForCreation checks that a catalog item is published and not deleted.
// Tenant visibility is enforced by the GenericDAO's tenancy logic at the query level.
func validateCatalogItemForCreation(item catalogItem, ref string) error {
	if item.GetMetadata().HasDeletionTimestamp() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"catalog item '%s' has been deleted", ref)
	}
	if !item.GetPublished() {
		return grpcstatus.Errorf(grpccodes.NotFound,
			"catalog item '%s' is not published", ref)
	}
	return nil
}

// preserveCatalogItemProvenance validates an Update against stored provenance without reading the catalog.
// ID-only input is accepted; supplied identity or scope must agree. The returned clone preserves
// the original canonical reference even after catalog deletion. An explicit clear is rejected.
func preserveCatalogItemProvenance[T interface {
	fullResourceReference
	proto.Message
}](current, candidate T, mask *fieldmaskpb.FieldMask) (T, error) {
	if proto.Equal(current, candidate) || (mask == nil && !candidate.ProtoReflect().IsValid()) {
		return cloneMessage(current), nil
	}
	if !current.ProtoReflect().IsValid() || !candidate.ProtoReflect().IsValid() ||
		candidate.GetId() != current.GetId() ||
		(candidate.GetName() != "" && candidate.GetName() != current.GetName()) ||
		(candidate.GetProject() != "" && candidate.GetProject() != current.GetProject()) ||
		(candidate.GetShared() && !current.GetShared()) {
		return candidate, grpcstatus.Errorf(grpccodes.InvalidArgument, "cannot change spec.catalog_item from '%s' to '%s': catalog item is immutable", refKey(current), refKey(candidate))
	}
	// A false shared flag has no protobuf presence; a mask targeting that flag makes it explicit.
	for _, path := range mask.GetPaths() {
		if (path == "spec.catalog_item.shared" && candidate.GetShared() != current.GetShared()) ||
			(path == "spec.catalog_item.project" && candidate.GetProject() != current.GetProject()) ||
			(path == "spec.catalog_item.name" && candidate.GetName() != current.GetName()) {
			return candidate, grpcstatus.Errorf(grpccodes.InvalidArgument, "cannot change spec.catalog_item from '%s' to '%s': catalog item is immutable", refKey(current), refKey(candidate))
		}
	}
	return cloneMessage(current), nil
}
