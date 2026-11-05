/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package output renders proto messages in the output formats the CLI's read commands share: each command keeps its
// own table layout, while the format validation, the JSON and YAML encoding of proto messages, and the table dash for
// empty values live here so every command renders them identically.
package output

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

// The output formats every read command accepts on its --output flag.
const (
	Table = "table"
	JSON  = "json"
	YAML  = "yaml"
)

// Validate rejects an unknown output format. Commands call it before doing any work.
func Validate(format string) error {
	if format != Table && format != JSON && format != YAML {
		return fmt.Errorf("unknown output format '%s', should be '%s', '%s' or '%s'",
			format, Table, JSON, YAML)
	}
	return nil
}

// Message returns message as a generic JSON value with proto field names, ready for Render.
func Message(message proto.Message) (any, error) {
	data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(message)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// Messages returns every message as one generic JSON array, empty rather than null when there are none.
func Messages[T proto.Message](messages []T) ([]any, error) {
	values := make([]any, 0, len(messages))
	for _, message := range messages {
		value, err := Message(message)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

// Render writes value to the console as JSON or YAML; the table format is each command's own layout.
func Render(ctx context.Context, console *terminal.Console, format string, value any) {
	if format == JSON {
		console.RenderJson(ctx, value)
	} else {
		console.RenderYaml(ctx, value)
	}
}

// OrDash replaces an empty value with a dash for table output.
func OrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
