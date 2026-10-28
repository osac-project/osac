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
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
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

// ErrInvalid marks a revision that cannot be parsed: a YAML typo, a bad effective_at,
// an unknown key, a second document, or meters that fail validation. Such a failure is
// permanent, not an outage: no retry fixes it, and the operator intended new pricing at
// the revision's effective time, so callers must stop billing instead of retrying.
var ErrInvalid = errors.New("invalid rule revision")

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

	// TurnsBillableOnce lists the states that confirm a deferred billable span: a span
	// bills once the resource reached one of them, retroactively, and a span that closed
	// before the resource reached one never bills.
	TurnsBillableOnce []string `yaml:"turns_billable_once"`

	// TurnsBillableOnceFrom says whose confirmation gates the meter: "root" (the default)
	// reads the states of the resource's ownership root, "self" reads the resource's own.
	// A resource with no owner always reads its own.
	TurnsBillableOnceFrom string `yaml:"turns_billable_once_from"`

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
// valid: rules are data, so a bad revision must stop metering rather than bill wrong. Every
// failure wraps ErrInvalid, so callers can tell a malformed revision from a failed read.
func Parse(name, effectiveAtText, metersYAML string) (Revision, error) {
	number := strings.TrimPrefix(name, NamePrefix)
	if number == name || len(number) != 4 {
		return Revision{}, fmt.Errorf(
			"%w: revision ConfigMap %q is not named %s<NNNN>", ErrInvalid, name, NamePrefix,
		)
	}
	if _, err := strconv.Atoi(number); err != nil {
		return Revision{}, fmt.Errorf("%w: revision number %q of %s is not a number", ErrInvalid, number, name)
	}
	effectiveAt, err := time.Parse(time.RFC3339, effectiveAtText)
	if err != nil {
		return Revision{}, fmt.Errorf(
			"%w: revision %s has an invalid effective_at %q", ErrInvalid, number, effectiveAtText,
		)
	}

	var meters []Meter
	// Unknown keys reject the revision instead of silently dropping. The gate key was
	// renamed once: [READY] would now load as no gate at all, and failed-installation
	// time would bill.
	decoder := yaml.NewDecoder(strings.NewReader(metersYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&meters); err != nil {
		return Revision{}, fmt.Errorf("%w: revision %s has invalid meters: %w", ErrInvalid, number, err)
	}
	// A second document would load as the first alone and silently drop its meters, so
	// a revision must hold exactly one document.
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Revision{}, fmt.Errorf(
			"%w: revision %s has a second YAML document; only one meters document is allowed",
			ErrInvalid, number,
		)
	}
	revision := Revision{
		Name: name, Number: number, EffectiveAt: effectiveAt.UTC(),
		Meters: meters, MetersYAML: metersYAML,
	}
	if err := revision.validate(); err != nil {
		return Revision{}, fmt.Errorf("%w: %w", ErrInvalid, err)
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
		short := metering.ShortName(string(resource.Message))
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
		for _, list := range [][]string{meter.Billable.States, meter.Billable.TurnsBillableOnce} {
			for _, name := range list {
				if !knownStates[name] {
					return fmt.Errorf(
						"%s names state %q, which %s never has", where, name, meter.Resource,
					)
				}
			}
		}
		for _, name := range meter.Billable.TurnsBillableOnce {
			// A confirming state outside the billable states can never confirm a span, so
			// a rule author error here would silently bill nothing.
			if !billable[name] {
				return fmt.Errorf(
					"%s confirms state %q, which it does not bill in", where, name,
				)
			}
		}
		switch meter.Billable.TurnsBillableOnceFrom {
		case "", "root", "self":
		default:
			return fmt.Errorf(
				"%s turns billable once from %q, want root or self",
				where, meter.Billable.TurnsBillableOnceFrom,
			)
		}
		for _, condition := range slices.Sorted(maps.Keys(meter.Billable.Fields)) {
			if computedDimensions[condition] {
				return fmt.Errorf(
					"%s conditions on field %q, which is computed and cannot be a condition",
					where, condition,
				)
			}
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

	// The registry's ownership edges: which resource type owns which, read through which
	// field.
	owners := metering.Owners(resources)

	// A resource type that owns others gates their billing, so every meter of that type
	// must confirm on the same states: an ambiguous gate is a billing bug, and the
	// calculator cannot pick one.
	roots := map[string]bool{}
	for _, spec := range owners {
		roots[spec.Resource] = true
	}
	for rootType := range roots {
		seen := false
		var shared []string
		for _, meter := range r.Meters {
			if meter.Resource != rootType {
				continue
			}
			if !seen {
				seen = true
				shared = meter.Billable.TurnsBillableOnce
				continue
			}
			if !slices.Equal(shared, meter.Billable.TurnsBillableOnce) {
				return fmt.Errorf(
					"revision %s: meters of %s confirm on different states, so the gate it "+
						"passes to owned resources is ambiguous",
					r.Number, rootType,
				)
			}
		}
	}

	// A revision that bills an owned resource through inheritance must also define the
	// gate its root passes down, or the owned resource would bill unconditionally.
	for resourceType, spec := range owners {
		if !billsInherited(resourceType, r.Meters) {
			continue
		}
		hasRootMeter := false
		for _, meter := range r.Meters {
			if meter.Resource == spec.Resource {
				hasRootMeter = true
				break
			}
		}
		if !hasRootMeter {
			return fmt.Errorf(
				"revision %s bills %s, which %s owns, but defines no meters for %s to "+
					"provide the inherited gate",
				r.Number, resourceType, spec.Resource, spec.Resource,
			)
		}
	}
	return nil
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
	prefix := metering.EnumValuePrefix(stateField.Enum())
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

// billsInherited reports whether the revision bills the owned resource through the
// root's gate. A meter that overrides the gate to self does not inherit, so the root
// needs no gate for it.
func billsInherited(resourceType string, meters []Meter) bool {
	for _, meter := range meters {
		if meter.Resource == resourceType &&
			meter.Billable.TurnsBillableOnceFrom != "self" {
			return true
		}
	}
	return false
}
