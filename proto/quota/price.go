/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package quota

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
)

// TypeLookup resolves the type a reference points at, so the price can apply the type's
// annotations. Enforcement wires the database; tests use in-memory lookups.
type TypeLookup interface {
	// Type returns the message of the named kind of type, or nil when the lookup
	// doesn't know it.
	Type(kind, name string) protoreflect.Message
}

// Quote is the full cost of one request.
type Quote struct {
	// Dimensions maps each dimension to the amount the request uses, including what
	// OSAC will create for it.
	Dimensions map[string]int64

	// Creates are the resources OSAC will create for the request, one entry per
	// resource.
	Creates []PlannedCreate
}

// PlannedCreate is one resource OSAC will create for a request.
type PlannedCreate struct {
	// Kind is the created resource's kind, such as "Volume".
	Kind string

	// Fields are the field paths of the new resource and the values the create assigns
	// to them, from the annotation's set mappings.
	Fields map[string]protoreflect.Value
}

// kindsByName caches the registry built from the compiled private protos.
var kindsByName = sync.OnceValues(func() (map[string]*Kind, error) {
	kinds, err := Kinds()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*Kind, len(kinds))
	for i := range kinds {
		byName[kinds[i].Name] = &kinds[i]
	}
	return byName, nil
})

// Price computes the price of one request message: the dimensions and amounts it uses,
// including what OSAC will create for it. A kind without annotations prices nothing. Each
// through reference is resolved with the lookup, and a reference the lookup doesn't know
// is an error: a price that silently skipped a resource's type would undercount it.
func Price(message protoreflect.Message, types TypeLookup) (Quote, error) {
	byName, err := kindsByName()
	if err != nil {
		return Quote{}, err
	}
	kind := byName[shortName(message.Descriptor().FullName())]
	if kind == nil {
		return Quote{}, nil
	}
	dimensions, creates, err := priceOf(message, kind, types, byName)
	if err != nil {
		return Quote{}, err
	}
	return Quote{Dimensions: dimensions, Creates: creates}, nil
}

// priceOf prices one message against its kind's annotations: its own dimensions, what its
// through references use, and what OSAC will create for it.
func priceOf(
	message protoreflect.Message,
	kind *Kind,
	types TypeLookup,
	byName map[string]*Kind,
) (map[string]int64, []PlannedCreate, error) {
	dimensions := map[string]int64{}
	var creates []PlannedCreate

	if kind.Dimension != "" {
		dimension, err := resolveDimension(kind.Dimension, message)
		if err != nil {
			return nil, nil, fmt.Errorf("kind '%s': %w", kind.Name, err)
		}
		dimensions[dimension]++
	}

	for _, field := range kind.Fields {
		var err error
		switch {
		case field.Dimension != "":
			err = addFieldDimension(dimensions, message, field)
		case field.Through != "":
			err = addThrough(dimensions, message, field, types, byName)
		case field.Creates != nil:
			var fieldCreates []PlannedCreate
			fieldCreates, err = addCreates(dimensions, message, field, types, byName)
			creates = append(creates, fieldCreates...)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return dimensions, creates, nil
}

// addFieldDimension adds the value of an annotated field to its dimension.
func addFieldDimension(dimensions map[string]int64, message protoreflect.Message, field Field) error {
	value, _, ok, err := navigate(message, field.Path)
	if err != nil || !ok {
		return err
	}
	dimension, err := resolveDimension(field.Dimension, message)
	if err != nil {
		return fmt.Errorf("field '%s': %w", field.Path, err)
	}
	dimensions[dimension] += value.Int()
	return nil
}

// addThrough adds what a referenced type uses. With a larger_of path, the resource uses
// the larger amount on each dimension: while a resize is in progress the resource keeps
// running the old type, so counting only the asked type would free quota the resource
// still uses.
func addThrough(
	dimensions map[string]int64,
	message protoreflect.Message,
	field Field,
	types TypeLookup,
	byName map[string]*Kind,
) error {
	asked, _, ok, err := navigate(message, field.Path)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("field '%s' resolves through a type but isn't set", field.Path)
	}
	askedDimensions, err := throughDimensions(asked.Message(), field, types, byName)
	if err != nil {
		return err
	}
	runningDimensions := map[string]int64{}
	if field.LargerOf != "" {
		running, _, ok, err := navigate(message, field.LargerOf)
		if err != nil {
			return err
		}
		if ok {
			runningDimensions, err = throughDimensions(running.Message(), field, types, byName)
			if err != nil {
				return err
			}
		}
	}
	mergeMax(dimensions, askedDimensions, runningDimensions)
	return nil
}

// throughDimensions prices the type a reference points at.
func throughDimensions(
	reference protoreflect.Message,
	field Field,
	types TypeLookup,
	byName map[string]*Kind,
) (map[string]int64, error) {
	name, err := referenceName(reference)
	if err != nil {
		return nil, fmt.Errorf("field '%s': %w", field.Path, err)
	}
	typed := types.Type(field.Through, name)
	if typed == nil {
		return nil, fmt.Errorf("field '%s': no '%s' named '%s'", field.Path, field.Through, name)
	}
	if shortName(typed.Descriptor().FullName()) != field.Through {
		return nil, fmt.Errorf(
			"field '%s': the lookup returned '%s' for a '%s'",
			field.Path, typed.Descriptor().FullName(), field.Through,
		)
	}
	dimensions, _, err := priceOf(typed, byName[field.Through], types, byName)
	if err != nil {
		return nil, err
	}
	return dimensions, nil
}

// addCreates expands what OSAC will create for an annotated field's value: the price of
// each created resource, and the planned creates themselves.
func addCreates(
	dimensions map[string]int64,
	message protoreflect.Message,
	field Field,
	types TypeLookup,
	byName map[string]*Kind,
) ([]PlannedCreate, error) {
	value, descriptor, ok, err := navigate(message, field.Path)
	if err != nil || !ok {
		return nil, err
	}
	creates := field.Creates

	var items []protoreflect.Value
	if descriptor.Kind() == protoreflect.BoolKind {
		// A bool field has no item: the count alone decides, and the registry
		// validates that a bool creates neither reads a count field nor sets fields.
		if value.Bool() {
			items = []protoreflect.Value{protoreflect.Value{}}
		}
	} else {
		items, err = itemsOf(value, descriptor, creates.Each)
		if err != nil {
			return nil, err
		}
	}

	var planned []PlannedCreate
	for _, item := range items {
		count, err := createCount(creates, item)
		if err != nil {
			return nil, err
		}
		for i := 0; i < count; i++ {
			create, plannedMessage, err := plannedCreate(creates, item, byName)
			if err != nil {
				return nil, err
			}
			createdDimensions, _, err := priceOf(plannedMessage, byName[creates.Kind], types, byName)
			if err != nil {
				return nil, err
			}
			mergeSum(dimensions, createdDimensions)
			planned = append(planned, create)
		}
	}
	return planned, nil
}

// itemsOf returns the items a creates annotation creates resources for: the field's
// value, each entry of its list, or each value of its map.
func itemsOf(
	value protoreflect.Value, field protoreflect.FieldDescriptor, each string,
) ([]protoreflect.Value, error) {
	switch {
	case field.IsMap():
		mapValue := value.Map()
		var keys []string
		mapValue.Range(func(key protoreflect.MapKey, _ protoreflect.Value) bool {
			keys = append(keys, key.String())
			return true
		})
		sort.Strings(keys)
		items := make([]protoreflect.Value, 0, len(keys))
		for _, key := range keys {
			items = append(items, mapValue.Get(protoreflect.ValueOfString(key).MapKey()))
		}
		return items, nil
	case field.IsList():
		return listItems(value.List()), nil
	case each != "":
		listField := value.Message().Descriptor().Fields().ByName(protoreflect.Name(each))
		if listField == nil {
			return nil, fmt.Errorf(
				"message '%s' has no field '%s'", value.Message().Descriptor().FullName(), each,
			)
		}
		return listItems(value.Message().Get(listField).List()), nil
	default:
		return []protoreflect.Value{value}, nil
	}
}

// listItems returns the entries of a list.
func listItems(list protoreflect.List) []protoreflect.Value {
	items := make([]protoreflect.Value, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		items = append(items, list.Get(i))
	}
	return items
}

// createCount says how many resources to create for one item.
func createCount(creates *Creates, item protoreflect.Value) (int, error) {
	if creates.Count > 0 {
		return int(creates.Count), nil
	}
	if creates.CountField == "" {
		return 1, nil
	}
	value, _, ok, err := navigate(item.Message(), creates.CountField)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("creates count field '%s' isn't set", creates.CountField)
	}
	return int(value.Int()), nil
}

// plannedCreate builds one planned resource: its set fields, and the message the price
// walks.
func plannedCreate(
	creates *Creates, item protoreflect.Value, byName map[string]*Kind,
) (PlannedCreate, protoreflect.Message, error) {
	kind := byName[creates.Kind]
	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(kind.Message)
	if err != nil {
		return PlannedCreate{}, nil, err
	}
	plannedMessage := dynamicpb.NewMessage(descriptor.(protoreflect.MessageDescriptor))

	fields := map[string]protoreflect.Value{}
	for _, target := range sortedKeys(creates.Set) {
		value, source, ok, err := navigate(item.Message(), creates.Set[target])
		if err != nil {
			return PlannedCreate{}, nil, err
		}
		if !ok {
			return PlannedCreate{}, nil, fmt.Errorf(
				"creates set path '%s' isn't set", creates.Set[target],
			)
		}
		targetField, err := resolveField(descriptor.(protoreflect.MessageDescriptor), target)
		if err != nil {
			return PlannedCreate{}, nil, err
		}
		converted, err := convert(targetField, source, value)
		if err != nil {
			return PlannedCreate{}, nil, err
		}
		if err := setPath(plannedMessage, target, converted); err != nil {
			return PlannedCreate{}, nil, err
		}
		fields[target] = converted
	}
	return PlannedCreate{Kind: creates.Kind, Fields: fields}, plannedMessage, nil
}

// setPath sets the value at a field path of a message, creating the intermediate
// messages.
func setPath(message protoreflect.Message, path string, value protoreflect.Value) error {
	segments := strings.Split(path, ".")
	current := message
	for i, segment := range segments {
		field := current.Descriptor().Fields().ByName(protoreflect.Name(segment))
		if field == nil {
			return fmt.Errorf("message '%s' has no field '%s'", current.Descriptor().FullName(), segment)
		}
		if i == len(segments)-1 {
			current.Set(field, value)
			return nil
		}
		current = current.Mutable(field).Message()
	}
	return nil
}

// convert turns a value read from an item into the value of the target field of a
// created resource. The registry validated the pairing at startup.
func convert(
	target, source protoreflect.FieldDescriptor, value protoreflect.Value,
) (protoreflect.Value, error) {
	if target.Kind() == source.Kind() {
		return value, nil
	}
	if target.Kind() == protoreflect.Int64Kind && source.Kind() == protoreflect.Int32Kind {
		return protoreflect.ValueOfInt64(value.Int()), nil
	}
	return protoreflect.Value{}, fmt.Errorf(
		"value of kind '%s' doesn't fit field '%s' of kind '%s'",
		source.Kind(), target.Name(), target.Kind(),
	)
}

// mergeSum adds each dimension of the source to the destination.
func mergeSum(dst, src map[string]int64) {
	for dimension, amount := range src {
		dst[dimension] += amount
	}
}

// mergeMax adds to the destination the larger amount of each dimension in a and b.
func mergeMax(dst, a, b map[string]int64) {
	for dimension, amount := range a {
		if b[dimension] > amount {
			amount = b[dimension]
		}
		dst[dimension] += amount
	}
	for dimension, amount := range b {
		if _, ok := a[dimension]; !ok {
			dst[dimension] += amount
		}
	}
}

// navigate reads the value at a field path from the message, and the descriptor of the
// field it names. ok is false when a field along the path isn't set.
func navigate(
	message protoreflect.Message, path string,
) (value protoreflect.Value, field protoreflect.FieldDescriptor, ok bool, err error) {
	current := message
	segments := strings.Split(path, ".")
	for i, segment := range segments {
		field := current.Descriptor().Fields().ByName(protoreflect.Name(segment))
		if field == nil {
			return protoreflect.Value{}, nil, false, fmt.Errorf(
				"message '%s' has no field '%s'", current.Descriptor().FullName(), segment,
			)
		}
		if !current.Has(field) {
			return protoreflect.Value{}, nil, false, nil
		}
		if i == len(segments)-1 {
			return current.Get(field), field, true, nil
		}
		if field.Kind() != protoreflect.MessageKind || field.IsList() || field.IsMap() {
			return protoreflect.Value{}, nil, false, fmt.Errorf(
				"field '%s' of '%s' isn't a message", segment, current.Descriptor().FullName(),
			)
		}
		current = current.Get(field).Message()
	}
	return protoreflect.Value{}, nil, false, fmt.Errorf("empty field path")
}

// resolveDimension fills the brace paths of a dimension template from the message, such
// as "storage.{spec.storage_tier}.gib" with the volume's tier.
func resolveDimension(template string, message protoreflect.Message) (string, error) {
	var result strings.Builder
	rest := template
	for {
		start := strings.Index(rest, "{")
		if start < 0 {
			break
		}
		end := strings.Index(rest[start:], "}")
		if end < 0 {
			return "", fmt.Errorf("dimension '%s' has an unclosed brace", template)
		}
		path := rest[start+1 : start+end]
		value, _, ok, err := navigate(message, path)
		if err != nil {
			return "", err
		}
		if !ok || value.String() == "" {
			return "", fmt.Errorf("dimension '%s' reads '%s', which isn't set", template, path)
		}
		result.WriteString(rest[:start])
		result.WriteString(value.String())
		rest = rest[start+end+1:]
	}
	result.WriteString(rest)
	return result.String(), nil
}

// referenceName reads the name a type reference points at.
func referenceName(reference protoreflect.Message) (string, error) {
	field := reference.Descriptor().Fields().ByName("name")
	if field == nil || field.Kind() != protoreflect.StringKind {
		return "", fmt.Errorf(
			"reference '%s' has no name", reference.Descriptor().FullName(),
		)
	}
	name := reference.Get(field).String()
	if name == "" {
		return "", fmt.Errorf("reference '%s' names nothing", reference.Descriptor().FullName())
	}
	return name, nil
}
