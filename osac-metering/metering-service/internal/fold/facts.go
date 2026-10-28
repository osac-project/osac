/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Package fold turns event topic records into history rows. It is the only code that reads
// event topics.
package fold

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"github.com/osac-project/osac/proto/metering"

	"github.com/osac-project/osac-metering/internal/database"
)

// ErrForeignTenant marks an event whose payload names a tenant other than the topic it was
// consumed from. Folding it would bill this tenant for another tenant's resource, so the
// fold skips the record and reports the mismatch.
var ErrForeignTenant = errors.New("event of another tenant")

// Skip-worthy extraction failures. Each names a record that can never be billed correctly:
// folding it would silently under- or over-bill, and inventing the missing data would bill
// the wrong minutes. The fold skips such records and reports them.
var (
	// ErrUnknownPayload marks an event whose payload this build cannot read: a payload of
	// a kind this build does not know, or no payload at all.
	ErrUnknownPayload = errors.New("event with no readable payload")

	// ErrMissingResourceID marks a tracked payload without a resource id.
	ErrMissingResourceID = errors.New("tracked payload without a resource id")

	// ErrMissingCreationTime marks a tracked payload without a creation timestamp.
	ErrMissingCreationTime = errors.New("tracked payload without a creation timestamp")

	// ErrMissingDeletionTime marks a deletion event without an envelope timestamp.
	ErrMissingDeletionTime = errors.New("deletion event without a timestamp")
)

// FactExtractor pulls the billable facts out of events: for each event it walks the
// payload message against the shared registry of tracked fields, so fulfillment's
// triggers and this fold cannot drift apart.
type FactExtractor struct {
	resources map[protoreflect.FullName]metering.Resource
}

// NewFactExtractor creates the fact extractor from the tracked-field registry.
func NewFactExtractor() (*FactExtractor, error) {
	resources, err := metering.Resources()
	if err != nil {
		return nil, fmt.Errorf("reading the metering field registry: %w", err)
	}
	byName := make(map[protoreflect.FullName]metering.Resource, len(resources))
	for _, resource := range resources {
		byName[resource.Message] = resource
	}
	return &FactExtractor{resources: byName}, nil
}

// Facts returns the facts the event carries for the tenant. Events whose payload is not a
// billable resource carry none. An event that names a billable resource but cannot be
// folded correctly — misrouted, unreadable, or undatable — returns an error the caller
// must skip loudly, never fold with invented data. Each fact is derived from the event
// alone, so the same set of events always produces the same facts, whatever their order or
// how often each is applied.
func (e *FactExtractor) Facts(tenant string, event *privatev1.Event) ([]database.Fact, error) {
	if tenant == "" {
		return nil, fmt.Errorf("event %s has no tenant", event.GetId())
	}
	payload, resourceType := payloadOf(event)
	if payload == nil || !payload.IsValid() {
		// A payload this build cannot read is version skew or a broken record: folding it
		// would silently under-bill, because the offset advances either way.
		return nil, fmt.Errorf(
			"%w: event %s carries no payload this build recognizes", ErrUnknownPayload, event.GetId(),
		)
	}
	// The topic decides the billing tenant. An event whose payload names a different tenant
	// is misrouted data: folding it would bill this tenant for another tenant's resource.
	// The fold skips such records; a payload without a tenant keeps trusting the topic.
	if eventTenant := stringOf(payload, "metadata.tenant"); eventTenant != "" && eventTenant != tenant {
		return nil, fmt.Errorf(
			"%w: event %s carries tenant %q on the topic of %q",
			ErrForeignTenant, event.GetId(), eventTenant, tenant,
		)
	}
	resource, tracked := e.resources[resourceType]
	if !tracked {
		return nil, nil
	}
	resourceID := resourceIDOf(payload)
	if resourceID == "" {
		// A tracked resource without an id cannot be keyed into history.
		return nil, fmt.Errorf(
			"%w: event %s carries a %s payload without a resource id",
			ErrMissingResourceID, event.GetId(), metering.ShortName(string(resourceType)),
		)
	}
	typeName := metering.ShortName(string(resourceType))

	creation := timestampAt(payload, "metadata.creation_timestamp")
	if creation.IsZero() {
		// Without a creation time, every creation-keyed fact — created, project, catalog
		// item, template, and the creation fallback of varying fields — would be lost, and
		// inventing a time would bill the wrong minutes.
		return nil, fmt.Errorf(
			"%w: event %s of %s %s carries no creation timestamp",
			ErrMissingCreationTime, event.GetId(), typeName, resourceID,
		)
	}
	var facts []database.Fact
	add := func(field string, since time.Time, value string) {
		facts = append(facts, database.Fact{
			ResourceType: typeName,
			ResourceID:   resourceID,
			Field:        field,
			// Whole seconds: the payload's CR-sourced times truncate to seconds at
			// the source, and the database-clock stamps (creation, deletion envelope)
			// are normalized here so every fact boundary carries the same precision.
			Since: since.UTC().Truncate(time.Second),
			Value: value,
		})
	}

	add("created", creation, "true")

	// Identity dimensions every meter sends. They never change, so they are facts of the
	// resource's creation.
	if project := stringOf(payload, "metadata.project"); project != "" {
		add("metadata.project", creation, project)
	}
	if catalog := referenceOf(payload, "spec.catalog_item"); catalog != "" {
		add("spec.catalog_item", creation, catalog)
	}
	if template := referenceOf(payload, "spec.template"); template != "" {
		add("spec.template", creation, template)
	}

	// Tracked fields from the shared registry. A changing field is a fact once its
	// change time is known: a value with no change time yet is the creation value. A
	// field known at creation or set once later is a fact from the creation.
	for _, field := range resource.Fields {
		value, descriptor, ok := fieldAndValueAt(payload, field.Path)
		if !ok {
			continue
		}
		render := formatValue
		if field.OwnerResource != "" {
			render = formatOwnerReference
		}
		switch field.Kind {
		case metering.KindVaries:
			since := timestampAt(payload, field.TransitionTime)
			if since.IsZero() {
				if creation.IsZero() || !nonZero(value, descriptor) {
					continue
				}
				since = creation
			}
			rendered := render(value, descriptor)
			// A value that renders to nothing carries no fact: attached resources bill
			// standalone under their own identity, so the attribution field, whose value
			// is no longer rendered, records nothing.
			if rendered == "" {
				continue
			}
			add(field.Path, since, rendered)
		case metering.KindConstant, metering.KindFixedOnceSet:
			if !nonZero(value, descriptor) {
				continue
			}
			add(field.Path, creation, render(value, descriptor))
		}
	}

	if event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_DELETED {
		// The envelope timestamp is the authoritative deletion boundary: without it the
		// resource would never stop billing. An absent and an all-zero timestamp behave
		// the same, per timestampAt.
		deleted := timestampAt(event.ProtoReflect(), "timestamp")
		if deleted.IsZero() {
			return nil, fmt.Errorf(
				"%w: deletion event %s of %s %s carries no timestamp",
				ErrMissingDeletionTime, event.GetId(), typeName, resourceID,
			)
		}
		add("deleted", deleted, "true")
	}
	return facts, nil
}

// payloadOf returns the event's payload message and its type name.
func payloadOf(event *privatev1.Event) (protoreflect.Message, protoreflect.FullName) {
	reflection := event.ProtoReflect()
	descriptor := reflection.Descriptor()
	oneof := descriptor.Oneofs().ByName("payload")
	if oneof == nil {
		return nil, ""
	}
	field := reflection.WhichOneof(oneof)
	if field == nil {
		return nil, ""
	}
	return reflection.Get(field).Message(), field.Message().FullName()
}

func resourceIDOf(payload protoreflect.Message) string {
	return stringOf(payload, "id")
}

// valueAt reads the value at a field path. The second return is false when the path can't be
// walked or the value is unset.
func valueAt(message protoreflect.Message, path string) (protoreflect.Value, bool) {
	value, _, ok := fieldAndValueAt(message, path)
	return value, ok
}

// fieldAndValueAt reads the value at a field path together with its field descriptor. The last
// return is false when the path can't be walked.
func fieldAndValueAt(
	message protoreflect.Message, path string,
) (protoreflect.Value, protoreflect.FieldDescriptor, bool) {
	current := message
	segments := strings.Split(path, ".")
	for i, segment := range segments {
		field := current.Descriptor().Fields().ByName(protoreflect.Name(segment))
		if field == nil {
			return protoreflect.Value{}, nil, false
		}
		value := current.Get(field)
		if i == len(segments)-1 {
			return value, field, true
		}
		if field.Kind() != protoreflect.MessageKind || field.IsList() || field.IsMap() {
			return protoreflect.Value{}, nil, false
		}
		if !value.Message().IsValid() {
			return protoreflect.Value{}, nil, false
		}
		current = value.Message()
	}
	return protoreflect.Value{}, nil, false
}

func stringOf(message protoreflect.Message, path string) string {
	value, ok := valueAt(message, path)
	if !ok {
		return ""
	}
	return value.String()
}

// timestampAt reads a timestamp field at a path. It returns the zero time when the field is
// unset, and when it is present but all zero: proto3 renders an unset timestamp as seconds 0
// and nanos 0, which is not a real 1970 date, so the fold must treat it exactly like an
// absent field.
func timestampAt(message protoreflect.Message, path string) time.Time {
	value, ok := valueAt(message, path)
	if !ok || !value.Message().IsValid() {
		return time.Time{}
	}
	ts := value.Message()
	seconds, ok := valueAt(ts, "seconds")
	if !ok {
		return time.Time{}
	}
	nanos, _ := valueAt(ts, "nanos")
	if seconds.Int() == 0 && nanos.Int() == 0 {
		return time.Time{}
	}
	return time.Unix(seconds.Int(), nanos.Int()).UTC()
}

// referenceOf reads a reference message at a path and returns its name when set, else its id.
func referenceOf(message protoreflect.Message, path string) string {
	value, ok := valueAt(message, path)
	if !ok || !value.Message().IsValid() {
		return ""
	}
	return formatReference(value.Message())
}

func formatReference(message protoreflect.Message) string {
	for _, field := range []string{"name", "id"} {
		descriptor := message.Descriptor().Fields().ByName(protoreflect.Name(field))
		if descriptor == nil || descriptor.Kind() != protoreflect.StringKind {
			continue
		}
		if name := message.Get(descriptor).String(); name != "" {
			return name
		}
	}
	return ""
}

// formatOwnerReference renders an owner reference by its id. The id is the stable key the
// calculator resolves roots by: a name can change, and the reference the controller sets
// carries only the id anyway.
func formatOwnerReference(value protoreflect.Value, descriptor protoreflect.FieldDescriptor) string {
	if descriptor.Kind() != protoreflect.MessageKind || !value.Message().IsValid() {
		return ""
	}
	id := value.Message().Descriptor().Fields().ByName("id")
	if id == nil || id.Kind() != protoreflect.StringKind {
		return ""
	}
	return value.Message().Get(id).String()
}

// nonZero reports whether a value carries information. Boolean false does: a detached external
// IP is a fact when its change time is known.
func nonZero(value protoreflect.Value, descriptor protoreflect.FieldDescriptor) bool {
	switch descriptor.Kind() {
	case protoreflect.StringKind:
		return value.String() != ""
	case protoreflect.BoolKind:
		return true
	case protoreflect.Int32Kind, protoreflect.Int64Kind:
		return value.Int() != 0
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind:
		// protoreflect.Value.Int panics on a uint-typed value, and the registry
		// accepts uint fields, so this arm reads the uint accessor.
		return value.Uint() != 0
	case protoreflect.EnumKind:
		return value.Enum() != 0
	case protoreflect.MessageKind:
		return value.Message().IsValid()
	default:
		return false
	}
}

// formatValue renders a proto value as the text history stores.
func formatValue(value protoreflect.Value, descriptor protoreflect.FieldDescriptor) string {
	switch descriptor.Kind() {
	case protoreflect.StringKind:
		return value.String()
	case protoreflect.BoolKind:
		return strconv.FormatBool(value.Bool())
	case protoreflect.Int32Kind, protoreflect.Int64Kind:
		return strconv.FormatInt(value.Int(), 10)
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind:
		// protoreflect.Value.Int panics on a uint-typed value, and the registry
		// accepts uint fields, so this arm reads the uint accessor.
		return strconv.FormatUint(value.Uint(), 10)
	case protoreflect.EnumKind:
		return enumShortName(value.Enum(), descriptor.Enum())
	case protoreflect.MessageKind:
		if !value.Message().IsValid() {
			return ""
		}
		// References render by name or id. A message that is not a reference, today only
		// the external IP attribution, renders to nothing: attachments are ignored for
		// billing, so no fact is derived from them.
		return formatReference(value.Message())
	default:
		return ""
	}
}

// enumShortName strips the prefix the enum's values share, so rules and dimensions use RUNNING
// rather than COMPUTE_INSTANCE_STATE_RUNNING.
func enumShortName(number protoreflect.EnumNumber, descriptor protoreflect.EnumDescriptor) string {
	value := descriptor.Values().ByNumber(number)
	if value == nil {
		return strconv.FormatInt(int64(number), 10)
	}
	return strings.TrimPrefix(string(value.Name()), metering.EnumValuePrefix(descriptor))
}
