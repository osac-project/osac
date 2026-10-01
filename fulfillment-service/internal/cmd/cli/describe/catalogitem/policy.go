/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package catalogitem

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func appendPolicyRow(rows *[]row, label string, locked, hasDefault bool, lockedValue, defaultValue string) {
	r := row{label: label, state: "EDITABLE"}
	if locked {
		r.state = "LOCKED"
		r.value = lockedValue
	} else if hasDefault {
		r.value = "default: " + defaultValue
	}
	*rows = append(*rows, r)
}

func addString(rows *[]row, label string, p *publicv1.StringFieldPolicy, userData bool) {
	if p == nil {
		return
	}
	appendPolicyRow(rows, label, p.HasLocked(), p.GetEditable().HasDefaultValue(),
		formatText(p.GetLocked(), userData), formatText(p.GetEditable().GetDefaultValue(), userData))
}

func addBool(rows *[]row, label string, p *publicv1.BoolFieldPolicy) {
	if p == nil {
		return
	}
	appendPolicyRow(rows, label, p.HasLocked(), p.GetEditable().HasDefaultValue(),
		strconv.FormatBool(p.GetLocked()), strconv.FormatBool(p.GetEditable().GetDefaultValue()))
}

func addInt32(rows *[]row, label string, p *publicv1.Int32FieldPolicy, unit string) {
	if p == nil {
		return
	}
	appendPolicyRow(rows, label, p.HasLocked(), p.GetEditable().HasDefaultValue(),
		fmt.Sprintf("%d%s", p.GetLocked(), unit), fmt.Sprintf("%d%s", p.GetEditable().GetDefaultValue(), unit))
}

func formatText(value string, userData bool) string {
	lines := strings.Split(strings.TrimSuffix(value, "\n"), "\n")
	if len(lines) > 1 || len(value) > 100 {
		if userData && strings.TrimSpace(lines[0]) == "#cloud-config" {
			return fmt.Sprintf("#cloud-config (%d lines; see get -o yaml)", len(lines))
		}
		if len(lines) > 1 {
			return fmt.Sprintf("(%d lines; see get -o yaml)", len(lines))
		}
		return fmt.Sprintf("(%d characters; see get -o yaml)", len([]rune(value)))
	}
	return strconv.Quote(clean(value))
}

type localReference interface {
	GetId() string
	GetName() string
}

type fullReference interface {
	localReference
	GetProject() string
	GetShared() bool
}

func formatRef(ref localReference) string {
	if name := ref.GetName(); name != "" {
		return name
	}
	if id := ref.GetId(); id != "" {
		return id
	}
	return "-"
}

func formatFullRef(ref fullReference) string {
	name := formatRef(ref)
	var scope []string
	if ref.GetShared() {
		scope = append(scope, "shared")
	}
	if ref.GetProject() != "" {
		scope = append(scope, "project: "+ref.GetProject())
	}
	if len(scope) > 0 {
		name += " (" + strings.Join(scope, ", ") + ")"
	}
	return name
}

func addParameters(rows *[]row, parameters map[string]*publicv1.TemplateParameterPolicy) {
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		policy := parameters[key]
		if policy == nil {
			continue
		}
		appendPolicyRow(rows, key, policy.HasLocked(), policy.GetEditable().GetDefaultValue() != nil,
			formatParameter(policy.GetLocked()), formatParameter(policy.GetEditable().GetDefaultValue()))
	}
}

func formatParameter(value *anypb.Any) string {
	if value == nil {
		return "-"
	}
	decoded, err := value.UnmarshalNew()
	if err != nil {
		return "(unavailable; see get -o yaml)"
	}
	switch v := decoded.(type) {
	case *wrapperspb.StringValue:
		return formatText(v.GetValue(), false) + " (string)"
	case *wrapperspb.BoolValue:
		return fmt.Sprintf("%t (boolean)", v.GetValue())
	case *wrapperspb.Int32Value:
		return fmt.Sprintf("%d (int32)", v.GetValue())
	case *wrapperspb.Int64Value:
		return fmt.Sprintf("%d (int64)", v.GetValue())
	case *wrapperspb.FloatValue:
		return fmt.Sprintf("%g (float)", v.GetValue())
	case *wrapperspb.DoubleValue:
		return fmt.Sprintf("%g (double)", v.GetValue())
	case *wrapperspb.BytesValue:
		return fmt.Sprintf("(%d bytes; see get -o yaml)", len(v.GetValue()))
	case *timestamppb.Timestamp:
		if err := v.CheckValid(); err == nil {
			return v.AsTime().Format("2006-01-02T15:04:05Z07:00") + " (timestamp)"
		}
	case *durationpb.Duration:
		if err := v.CheckValid(); err == nil {
			return v.AsDuration().String() + " (duration)"
		}
	}
	return "(unavailable; see get -o yaml)"
}

func addCollection(rows *[]row, label string, locked, hasDefault bool, lockedItems, defaultItems []string) {
	items := defaultItems
	if locked {
		items = lockedItems
	}
	value := ""
	if locked || hasDefault {
		if len(items) == 0 {
			value = "(empty)"
		} else if len(items) == 1 {
			value = "1 item"
		} else {
			value = fmt.Sprintf("%d items", len(items))
		}
	}
	appendPolicyRow(rows, label, locked, hasDefault, value, value)
	if len(items) > 4 {
		(*rows)[len(*rows)-1].details = []string{"See get -o yaml for individual items."}
	} else if locked || hasDefault {
		(*rows)[len(*rows)-1].details = items
	}
}
