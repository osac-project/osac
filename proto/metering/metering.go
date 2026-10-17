/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

// Package metering exposes the fields of each billable resource that metering tracks, as
// declared by the metering annotations on the private protos. Fulfillment's database triggers
// and the metering service both take their list from here, so the two can't drift apart.
package metering

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// privatePackage is the package of the private protos, where the metering annotations live.
const privatePackage protoreflect.FullName = "osac.private.v1"

// Kind says how metering tracks a field.
type Kind string

const (
	// KindChanging marks a field whose value changes over the life of the resource. Another
	// field of the resource holds the time of each change.
	KindChanging Kind = "changing"

	// KindFixedAtCreation marks a field that is set when the resource is created and never
	// changes.
	KindFixedAtCreation Kind = "fixed_at_creation"

	// KindSetOnceLater marks a field that starts empty, is set once, and never changes after.
	// Its value is treated as known from creation.
	KindSetOnceLater Kind = "set_once_later"
)

// Field is one field of a resource that metering tracks.
type Field struct {
	// Path is the field's proto path from the resource message, for example "status.state".
	Path string `json:"path"`

	// Kind says how the field is tracked.
	Kind Kind `json:"kind"`

	// TransitionTime is the proto path of the field that holds the time of the last change of
	// this field. Set only for changing fields.
	TransitionTime string `json:"transition_time"`
}

// Resource is a billable resource and the fields metering tracks on it.
type Resource struct {
	// Message is the full name of the resource's proto message.
	Message protoreflect.FullName

	// Fields are the tracked fields, sorted by path.
	Fields []Field
}

// Resources returns every resource that has metering-tracked fields, sorted by message name.
// It fails if an annotation is invalid, or if an annotated field isn't reachable from a
// resource message: the source contract only knows how to track fields of a resource.
func Resources() (result []Resource, err error) {
	// Collect the annotated fields reachable from each top-level message, so that resource
	// messages and orphaned annotations can be told apart afterwards:
	type root struct {
		message protoreflect.MessageDescriptor
		fields  []annotatedField
	}
	var roots []root
	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if file.Package() != privatePackage {
			return true
		}
		messages := file.Messages()
		for i := range messages.Len() {
			message := messages.Get(i)
			fields, fieldErr := walk(message, nil)
			if fieldErr != nil {
				err = fieldErr
				return false
			}
			roots = append(roots, root{message: message, fields: fields})
		}
		return true
	})
	if err != nil {
		return nil, err
	}

	// Turn the resource messages into the registry and remember which annotated fields they
	// cover:
	covered := map[protoreflect.FullName]bool{}
	for _, r := range roots {
		if !isObjectShape(r.message) || len(r.fields) == 0 {
			continue
		}
		fields := make([]Field, 0, len(r.fields))
		for _, annotated := range r.fields {
			field, fieldErr := newField(r.message, annotated)
			if fieldErr != nil {
				return nil, fieldErr
			}
			fields = append(fields, field)
			covered[annotated.descriptor.FullName()] = true
		}
		sort.Slice(fields, func(i, j int) bool { return fields[i].Path < fields[j].Path })
		result = append(result, Resource{Message: r.message.FullName(), Fields: fields})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Message < result[j].Message })

	// An annotated field that no resource covers can't be tracked, so the annotations are
	// wrong rather than the registry incomplete:
	for _, r := range roots {
		for _, annotated := range r.fields {
			if !covered[annotated.descriptor.FullName()] {
				return nil, fmt.Errorf(
					"field '%s' has a metering annotation but isn't part of a resource message",
					annotated.descriptor.FullName(),
				)
			}
		}
	}

	return result, nil
}

// annotatedField is a field carrying a metering annotation, with its path from the message the
// walk started at.
type annotatedField struct {
	descriptor protoreflect.FieldDescriptor
	path       string
}

// walk collects the annotated fields of the message and of the singular message fields inside
// it. Fields inside repeated and map fields have no stable path, so the walk doesn't descend
// into them.
func walk(message protoreflect.MessageDescriptor, prefix []string) (result []annotatedField, err error) {
	fields := message.Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		segments := append(slices.Clone(prefix), string(field.Name()))
		path := strings.Join(segments, ".")
		if proto.HasExtension(field.Options(), privatev1.E_Metering) {
			options, ok := proto.GetExtension(field.Options(), privatev1.E_Metering).(*privatev1.MeteringFieldOptions)
			if !ok || options == nil {
				return nil, fmt.Errorf("field '%s' has an invalid metering annotation", field.FullName())
			}
			if options.GetKind() == privatev1.MeteringFieldKind_METERING_FIELD_KIND_UNSPECIFIED {
				return nil, fmt.Errorf(
					"field '%s' has a metering annotation without a kind", field.FullName(),
				)
			}
			result = append(result, annotatedField{descriptor: field, path: path})
		}
		if field.Kind() == protoreflect.MessageKind && !field.IsList() && !field.IsMap() {
			var nested []annotatedField
			nested, err = walk(field.Message(), segments)
			if err != nil {
				return nil, err
			}
			result = append(result, nested...)
		}
	}
	return result, nil
}

// newField validates the annotation of the field and resolves its transition time path against
// the resource message.
func newField(resource protoreflect.MessageDescriptor, annotated annotatedField) (Field, error) {
	options, _ := proto.GetExtension(
		annotated.descriptor.Options(), privatev1.E_Metering,
	).(*privatev1.MeteringFieldOptions)

	var kind Kind
	switch options.GetKind() {
	case privatev1.MeteringFieldKind_METERING_FIELD_KIND_CHANGING:
		kind = KindChanging
	case privatev1.MeteringFieldKind_METERING_FIELD_KIND_FIXED_AT_CREATION:
		kind = KindFixedAtCreation
	case privatev1.MeteringFieldKind_METERING_FIELD_KIND_SET_ONCE_LATER:
		kind = KindSetOnceLater
	default:
		return Field{}, fmt.Errorf(
			"field '%s' has an unknown metering kind '%s'", annotated.path, options.GetKind(),
		)
	}

	result := Field{
		Path:           annotated.path,
		Kind:           kind,
		TransitionTime: options.GetTransitionTime(),
	}

	if kind == KindChanging {
		if result.TransitionTime == "" {
			return Field{}, fmt.Errorf(
				"changing field '%s' of '%s' must name its transition time field",
				annotated.path, resource.FullName(),
			)
		}
		transitionTime, err := resolveField(resource, result.TransitionTime)
		if err != nil {
			return Field{}, fmt.Errorf(
				"changing field '%s' of '%s' has an invalid transition time path: %w",
				annotated.path, resource.FullName(), err,
			)
		}
		if transitionTime.Message() == nil || transitionTime.Message().FullName() != "google.protobuf.Timestamp" {
			return Field{}, fmt.Errorf(
				"transition time '%s' of changing field '%s' of '%s' isn't a timestamp",
				result.TransitionTime, annotated.path, resource.FullName(),
			)
		}
	} else if result.TransitionTime != "" {
		return Field{}, fmt.Errorf(
			"field '%s' of '%s' has kind '%s' but names a transition time field",
			annotated.path, resource.FullName(), kind,
		)
	}

	return result, nil
}

// resolveField resolves a proto field path against a message and returns the field it names.
func resolveField(message protoreflect.MessageDescriptor, path string) (protoreflect.FieldDescriptor, error) {
	segments := strings.Split(path, ".")
	current := message
	for i, segment := range segments {
		field := current.Fields().ByName(protoreflect.Name(segment))
		if field == nil {
			return nil, fmt.Errorf("message '%s' has no field '%s'", current.FullName(), segment)
		}
		if i < len(segments)-1 {
			if field.Kind() != protoreflect.MessageKind || field.IsList() || field.IsMap() {
				return nil, fmt.Errorf("field '%s' of '%s' isn't a message", segment, current.FullName())
			}
			current = field.Message()
		} else {
			return field, nil
		}
	}
	return nil, fmt.Errorf("empty field path")
}

// isObjectShape reports whether the message is a base resource type: exactly the 'id',
// 'metadata', 'spec' and 'status' fields of the standard object shape.
func isObjectShape(message protoreflect.MessageDescriptor) bool {
	fields := message.Fields()
	if fields.Len() != 4 {
		return false
	}
	id := fields.ByNumber(1)
	metadata := fields.ByNumber(2)
	spec := fields.ByNumber(3)
	status := fields.ByNumber(4)
	return id != nil && id.Name() == "id" && id.Kind() == protoreflect.StringKind &&
		metadata != nil && metadata.Name() == "metadata" && metadata.Kind() == protoreflect.MessageKind &&
		spec != nil && spec.Name() == "spec" && spec.Kind() == protoreflect.MessageKind &&
		status != nil && status.Name() == "status" && status.Kind() == protoreflect.MessageKind
}
