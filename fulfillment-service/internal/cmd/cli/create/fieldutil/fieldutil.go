/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package fieldutil

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/fulfillment-service/internal/maputil"
)

// ApplyFields applies --set KEY=VALUE pairs to a proto.Message via JSON round-trip.
// Paths prefixed with "template_parameters." are wrapped in protobuf Any format.
// Other paths use type inference (bool, number, string).
func ApplyFields(spec proto.Message, fields []string) error {
	if len(fields) == 0 {
		return nil
	}

	marshaller := protojson.MarshalOptions{UseProtoNames: true}
	specJSON, err := marshaller.Marshal(spec)
	if err != nil {
		return fmt.Errorf("failed to marshal spec: %w", err)
	}

	var specMap map[string]any
	if err := json.Unmarshal(specJSON, &specMap); err != nil {
		return fmt.Errorf("failed to parse spec: %w", err)
	}

	for _, field := range fields {
		key, value, err := parseField(field)
		if err != nil {
			return err
		}

		if strings.HasPrefix(key, "template_parameters.") {
			paramName := strings.TrimPrefix(key, "template_parameters.")
			if paramName == "" {
				return fmt.Errorf("invalid --set flag %q: parameter name is empty", field)
			}
			setTemplateParameter(specMap, paramName, value)
		} else {
			maputil.SetNestedValue(specMap, key, inferValue(value))
		}
	}

	updatedJSON, err := json.Marshal(specMap)
	if err != nil {
		return fmt.Errorf("failed to serialize updated spec: %w", err)
	}

	proto.Reset(spec)
	if err := protojson.Unmarshal(updatedJSON, spec); err != nil {
		return fmt.Errorf("failed to apply updated spec: %w", err)
	}

	return nil
}

func parseField(field string) (key, value string, err error) {
	parts := strings.SplitN(field, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid --set flag %q: expected KEY=VALUE format", field)
	}
	key = strings.TrimSpace(parts[0])
	if key == "" {
		return "", "", fmt.Errorf("invalid --set flag %q: key is empty", field)
	}
	value = parts[1]
	return key, value, nil
}

func inferValue(s string) any {
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil && strings.Count(s, ".") == 1 {
		return n
	}
	return s
}

func inferAnyType(s string) (typeURL string, value any) {
	if s == "true" || s == "false" {
		return "type.googleapis.com/google.protobuf.BoolValue", s == "true"
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return "type.googleapis.com/google.protobuf.Int64Value", fmt.Sprintf("%d", n)
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil && strings.Count(s, ".") == 1 {
		return "type.googleapis.com/google.protobuf.DoubleValue", n
	}
	return "type.googleapis.com/google.protobuf.StringValue", s
}

func setTemplateParameter(specMap map[string]any, name, rawValue string) {
	tp, ok := specMap["template_parameters"].(map[string]any)
	if !ok {
		tp = map[string]any{}
		specMap["template_parameters"] = tp
	}
	typeURL, value := inferAnyType(rawValue)
	tp[name] = map[string]any{
		"@type": typeURL,
		"value": value,
	}
}
