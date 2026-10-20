/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Package rules loads meter definitions from rule revisions. A revision is an immutable
// ConfigMap named osac-metering-rules-<NNNN> holding the complete meter definitions and the
// time it takes effect. Billing behavior is data; the code that evaluates it is not.
package rules

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"gopkg.in/yaml.v3"

	"github.com/osac-project/osac/proto/metering"
)

// NamePrefix is the prefix of every rule revision ConfigMap.
const NamePrefix = "osac-metering-rules-"

// Meter says, for one resource type, when the resource is billable and how its usage is
// dimensioned.
type Meter struct {
	Resource   string       `yaml:"resource"`
	Meter      string       `yaml:"meter"`
	Component  string       `yaml:"component"`
	Unit       string       `yaml:"unit"`
	Billable   BillableWhen `yaml:"billable_when"`
	Multiplier string       `yaml:"multiplier"`
	Dimensions []string     `yaml:"dimensions"`
}

// BillableWhen says when a meter bills.
type BillableWhen struct {
	// States lists the states the resource bills in.
	States []string `yaml:"states"`

	// Once lists the states that confirm a deferred billable span. A span that never
	// reaches one of them bills nothing.
	Once []string `yaml:"once"`

	// Fields holds conditions on fields that are fixed at creation or set once later.
	// The value is the expected text, or "set" for presence.
	Fields map[string]string `yaml:"fields"`
}

// EffectiveComponent returns the component heartbeats carry. It defaults to the meter name.
func (m Meter) EffectiveComponent() string {
	if m.Component != "" {
		return m.Component
	}
	return m.Meter
}

// Revision is one rule revision: complete meter definitions and the time they take effect.
type Revision struct {
	// Name is the ConfigMap name, for example osac-metering-rules-0002.
	Name string

	// Number is the zero-padded revision number, for example "0002".
	Number string

	// EffectiveAt is when the revision replaces the previous one.
	EffectiveAt time.Time

	// Meters are the complete meter definitions.
	Meters []Meter

	// MetersYAML is the raw meters document, recorded so a revision can never change.
	MetersYAML string
}

// Parse parses a revision from its ConfigMap name and data. It fails when the meters are not
// valid: rules are data, so a bad revision must stop metering rather than bill wrong.
func Parse(name, effectiveAtText, metersYAML string) (Revision, error) {
	number := strings.TrimPrefix(name, NamePrefix)
	if number == name || len(number) != 4 {
		return Revision{}, fmt.Errorf("revision ConfigMap %q is not named %s<NNNN>", name, NamePrefix)
	}
	if _, err := strconv.Atoi(number); err != nil {
		return Revision{}, fmt.Errorf("revision number %q of %s is not a number", number, name)
	}
	effectiveAt, err := time.Parse(time.RFC3339, effectiveAtText)
	if err != nil {
		return Revision{}, fmt.Errorf("revision %s has an invalid effective_at %q", number, effectiveAtText)
	}

	var meters []Meter
	if err := yaml.Unmarshal([]byte(metersYAML), &meters); err != nil {
		return Revision{}, fmt.Errorf("revision %s has invalid meters: %w", number, err)
	}
	revision := Revision{
		Name: name, Number: number, EffectiveAt: effectiveAt.UTC(),
		Meters: meters, MetersYAML: metersYAML,
	}
	if err := revision.validate(); err != nil {
		return Revision{}, err
	}
	return revision, nil
}

// validate checks the meters against the tracked-field registry and the dimension table, so a
// revision can only use fields the fold records.
func (r Revision) validate() error {
	resources, err := metering.Resources()
	if err != nil {
		return fmt.Errorf("revision %s: reading the field registry: %w", r.Number, err)
	}
	byName := make(map[string]metering.Resource, len(resources))
	for _, resource := range resources {
		short := shortMessageName(string(resource.Message))
		byName[short] = resource
	}

	if len(r.Meters) == 0 {
		return fmt.Errorf("revision %s defines no meters", r.Number)
	}
	seen := map[string]bool{}
	for i, meter := range r.Meters {
		where := fmt.Sprintf("revision %s meter %d", r.Number, i)
		resource, known := byName[meter.Resource]
		if !known {
			return fmt.Errorf("%s names unknown resource %q", where, meter.Resource)
		}
		if meter.Meter == "" {
			return fmt.Errorf("%s has no meter name", where)
		}
		if meter.Unit == "" {
			return fmt.Errorf("%s has no unit", where)
		}
		key := meter.Resource + "/" + meter.Meter
		if seen[key] {
			return fmt.Errorf("%s repeats meter %s of %s", where, meter.Meter, meter.Resource)
		}
		seen[key] = true

		tracked := map[string]bool{}
		for _, field := range resource.Fields {
			tracked[field.Path] = true
		}
		for _, extra := range extraFields[meter.Resource] {
			tracked[extra] = true
		}

		if len(meter.Billable.States) == 0 {
			return fmt.Errorf("%s bills in no state", where)
		}
		knownStates, err := stateNames(resource)
		if err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		billable := map[string]bool{}
		for _, name := range meter.Billable.States {
			billable[name] = true
		}
		for _, list := range [][]string{meter.Billable.States, meter.Billable.Once} {
			for _, name := range list {
				if !knownStates[name] {
					return fmt.Errorf(
						"%s names state %q, which %s never has", where, name, meter.Resource,
					)
				}
			}
		}
		for _, name := range meter.Billable.Once {
			// A once state outside the billable states can never confirm a span, so a
			// rule author error here would silently bill nothing.
			if !billable[name] {
				return fmt.Errorf(
					"%s confirms state %q, which it does not bill in", where, name,
				)
			}
		}
		for _, condition := range sortedKeys(meter.Billable.Fields) {
			path, known := DimensionPath(meter.Resource, condition)
			if !known || !tracked[path] {
				return fmt.Errorf(
					"%s conditions on field %q, which metering does not track", where, condition,
				)
			}
		}
		if meter.Multiplier != "" {
			path, known := DimensionPath(meter.Resource, meter.Multiplier)
			if !known || !tracked[path] {
				return fmt.Errorf(
					"%s multiplies by %q, which metering does not track", where, meter.Multiplier,
				)
			}
		}
		for _, dimension := range meter.Dimensions {
			path, known := DimensionPath(meter.Resource, dimension)
			if !known || !tracked[path] {
				return fmt.Errorf(
					"%s dimensions by %q, which metering does not track", where, dimension,
				)
			}
		}
	}
	return nil
}

// shortMessageName strips the package of a full proto message name.
func shortMessageName(full string) string {
	if index := strings.LastIndex(full, "."); index >= 0 {
		return full[index+1:]
	}
	return full
}

// stateNames returns the state names a resource can have, in the short form rules and history
// use.
func stateNames(resource metering.Resource) (map[string]bool, error) {
	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(resource.Message)
	if err != nil {
		return nil, fmt.Errorf("finding message %s: %w", resource.Message, err)
	}
	message, ok := descriptor.(protoreflect.MessageDescriptor)
	if !ok {
		return nil, fmt.Errorf("%s is not a message", resource.Message)
	}
	stateField, _, err := walkTo(message, "status.state")
	if err != nil {
		return nil, err
	}
	if stateField.Kind() != protoreflect.EnumKind {
		return nil, fmt.Errorf("status.state of %s is not an enum", resource.Message)
	}
	prefix := enumValuePrefix(stateField.Enum())
	names := map[string]bool{}
	values := stateField.Enum().Values()
	for i := range values.Len() {
		names[strings.TrimPrefix(string(values.Get(i).Name()), prefix)] = true
	}
	return names, nil
}

// walkTo resolves a field path against a message descriptor.
func walkTo(
	message protoreflect.MessageDescriptor, path string,
) (protoreflect.FieldDescriptor, protoreflect.MessageDescriptor, error) {
	current := message
	segments := strings.Split(path, ".")
	for i, segment := range segments {
		field := current.Fields().ByName(protoreflect.Name(segment))
		if field == nil {
			return nil, nil, fmt.Errorf("message %s has no field %s", current.FullName(), segment)
		}
		if i == len(segments)-1 {
			return field, current, nil
		}
		if field.Kind() != protoreflect.MessageKind {
			return nil, nil, fmt.Errorf("field %s of %s is not a message", segment, current.FullName())
		}
		current = field.Message()
	}
	return nil, nil, fmt.Errorf("empty field path")
}

// enumValuePrefix returns the longest prefix ending in an underscore that every value of the
// enum starts with, or an empty string when the values share none.
func enumValuePrefix(descriptor protoreflect.EnumDescriptor) string {
	values := descriptor.Values()
	prefix := string(values.Get(0).Name())
	for i := 1; i < values.Len(); i++ {
		for !strings.HasPrefix(string(values.Get(i).Name()), prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	if index := strings.LastIndex(prefix, "_"); index >= 0 {
		return prefix[:index+1]
	}
	return ""
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
