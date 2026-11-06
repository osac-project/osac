/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

// Package output provides the shared -o table/json/yaml machinery for the `get` subcommands that render one
// proto message: format validation, the JSON/YAML dispatch over protojson, and the table dash placeholder.
// Each command keeps its own table renderer.
package output

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/terminal"
)

// The supported -o values.
const (
	Table = "table"
	Json  = "json"
	Yaml  = "yaml"
)

// FlagHelp documents the -o flag next to Validate, which enforces the values it names. The {{ bt }} markers are
// the CLI help templating's backtick shorthand.
const FlagHelp = `_FORMAT_ - Output format. Must be one of {{ bt }}table{{ bt }}, {{ bt }}json{{ bt }} or {{ bt }}yaml{{ bt }}.`

// Validate fails when format is not one of Table, Json or Yaml.
func Validate(format string) error {
	if format != Table && format != Json && format != Yaml {
		return fmt.Errorf("unknown output format '%s', should be '%s', '%s' or '%s'", format, Table, Json, Yaml)
	}
	return nil
}

// Render writes message to console as JSON or YAML per format, encoding through protojson with proto field
// names. The table format is the caller's: every command owns its table renderer and dispatches Table itself,
// so Render only sees Json or Yaml. label names the entity in encoding and rendering error messages.
func Render(ctx context.Context, console *terminal.Console, format string, label string, message proto.Message) error {
	data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to encode %s: %w", label, err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("failed to encode %s: %w", label, err)
	}
	if format == Json {
		if err := console.RenderJson(ctx, value); err != nil {
			return fmt.Errorf("failed to render %s: %w", label, err)
		}
	} else {
		if err := console.RenderYaml(ctx, value); err != nil {
			return fmt.Errorf("failed to render %s: %w", label, err)
		}
	}
	return nil
}

// OrDash returns value, or "-" when it is empty, so a table can tell "not supplied" apart from a rendering
// bug.
func OrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
