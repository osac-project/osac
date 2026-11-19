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
// annotations. Enforcement wires the database; tests use in-memory lookups. The lookup
// only knows quota kinds: a set path that must resolve a reference of a kind no quota
// annotation maps, such as a storage tier, fails the price, so callers must resolve
// such references before the price runs and store both the id and the name.
type TypeLookup interface {
	// Type returns the message of the named kind of type the key points at, or nil when
	// the lookup doesn't know it. A key is a reference's id when set, else its name:
	// references are id-or-name with id winning, the way RefKeyStr reads them
	// (fulfillment-service/internal/controllers/references.go).
	Type(kind, key string) protoreflect.Message
}

// Quote is the full cost of one request.
type Quote struct {
	// Dimensions maps each dimension to the amount the request uses, including what
	// OSAC will create for it.
	Dimensions map[string]int64

	// Creates are the resources OSAC will create for the request: one entry per planned
	// resource, whose Count says how many resources OSAC creates with its fields, such
	// as the hosts of one node set. The plans are one level deep: a resource a planned
	// resource itself creates counts in Dimensions but produces no plan entry of its
	// own. Nothing in the check consumes the plans; they serve debugging and tests.
	Creates []PlannedCreate
}

// PlannedCreate is one planned resource: the fields OSAC creates resources with, and how
// many it creates with them.
type PlannedCreate struct {
	// Kind is the created resource's kind, such as "Volume".
	Kind string

	// Count is how many resources OSAC creates with these fields, such as a node set's
	// size. Identical resources share one entry.
	Count int

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

// A PriceOption adjusts what the price counts.
type PriceOption func(*priceOptions)

// priceOptions holds the adjustments a caller makes to a price.
type priceOptions struct {
	// uncreated names the kinds the installation never creates for a request, so their
	// planned creates and dimensions count toward no limit.
	uncreated map[string]bool
}

// WithoutCreatesOf names kinds the installation will not create for any request, such as
// the automatic addresses an installation with provider networking disabled never
// creates. Their planned creates and dimensions count toward no limit, so a check the
// price feeds refuses nothing the system will not actually create.
func WithoutCreatesOf(kinds ...string) PriceOption {
	return func(options *priceOptions) {
		for _, kind := range kinds {
			if options.uncreated == nil {
				options.uncreated = map[string]bool{}
			}
			options.uncreated[kind] = true
		}
	}
}

// Price computes the price of one request message: the dimensions and amounts it uses,
// including what OSAC will create for it. A kind without annotations prices nothing. Each
// through reference is resolved with the lookup, and a reference the lookup doesn't know
// is an error: a price that silently skipped a resource's type would undercount it.
func Price(message protoreflect.Message, types TypeLookup, opts ...PriceOption) (Quote, error) {
	options := priceOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	byName, err := kindsByName()
	if err != nil {
		return Quote{}, err
	}
	kind := byName[shortName(message.Descriptor().FullName())]
	if kind == nil {
		return Quote{}, nil
	}
	dimensions, creates, err := priceOf(message, kind, types, byName, options.uncreated)
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
	uncreated map[string]bool,
) (map[string]int64, []PlannedCreate, error) {
	dimensions := map[string]int64{}
	var creates []PlannedCreate

	if kind.Dimension != "" {
		dimension, err := resolveDimension(kind.Dimension, message)
		if err != nil {
			return nil, nil, fmt.Errorf("kind '%s': %w", kind.Name, err)
		}
		if err := addDimension(dimensions, dimension, 1); err != nil {
			return nil, nil, fmt.Errorf("kind '%s': %w", kind.Name, err)
		}
	}

	for _, field := range kind.Fields {
		var err error
		switch {
		case field.Dimension != "":
			err = addFieldDimension(dimensions, message, field)
		case field.Through != "":
			err = addThrough(dimensions, message, field, types, byName, uncreated)
		case field.Creates != nil:
			var fieldCreates []PlannedCreate
			fieldCreates, err = addCreates(dimensions, message, field, types, byName, uncreated)
			creates = append(creates, fieldCreates...)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return dimensions, creates, nil
}

// addDimension adds amount to the dimension's total and fails the price when the sum
// cannot fit in an int64: a wrapped negative amount would drop out of the check's added
// map, which keeps only positive amounts, and let a request past its limit.
func addDimension(dimensions map[string]int64, dimension string, amount int64) error {
	sum := dimensions[dimension] + amount
	if (amount > 0 && sum < dimensions[dimension]) || (amount < 0 && sum > dimensions[dimension]) {
		return fmt.Errorf(
			"dimension '%s' overflows: %d and %d don't fit", dimension, dimensions[dimension], amount,
		)
	}
	dimensions[dimension] = sum
	return nil
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
	if err := addDimension(dimensions, dimension, value.Int()); err != nil {
		return fmt.Errorf("field '%s': %w", field.Path, err)
	}
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
	uncreated map[string]bool,
) error {
	asked, _, ok, err := navigate(message, field.Path)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("field '%s' resolves through a type but isn't set", field.Path)
	}
	askedDimensions, err := throughDimensions(asked.Message(), field, types, byName, uncreated)
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
			runningDimensions, err = throughDimensions(running.Message(), field, types, byName, uncreated)
			if err != nil {
				return err
			}
		}
	}
	if err := mergeMax(dimensions, askedDimensions, runningDimensions); err != nil {
		return err
	}
	return nil
}

// throughDimensions prices the type a reference points at.
func throughDimensions(
	reference protoreflect.Message,
	field Field,
	types TypeLookup,
	byName map[string]*Kind,
	uncreated map[string]bool,
) (map[string]int64, error) {
	key, err := referenceKey(reference)
	if err != nil {
		return nil, fmt.Errorf("field '%s': %w", field.Path, err)
	}
	typed := types.Type(field.Through, key)
	if typed == nil {
		return nil, fmt.Errorf("field '%s': no '%s' matches '%s'", field.Path, field.Through, key)
	}
	if shortName(typed.Descriptor().FullName()) != field.Through {
		return nil, fmt.Errorf(
			"field '%s': the lookup returned '%s' for a '%s'",
			field.Path, typed.Descriptor().FullName(), field.Through,
		)
	}
	dimensions, _, err := priceOf(typed, byName[field.Through], types, byName, uncreated)
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
	uncreated map[string]bool,
) ([]PlannedCreate, error) {
	creates := field.Creates
	// A kind the installation never creates counts toward no limit, the same way its
	// writer creates nothing for it: provider networking disabled means no automatic
	// addresses, so a request that asks for them adds nothing.
	if uncreated[creates.Kind] {
		return nil, nil
	}
	value, descriptor, ok, err := navigate(message, field.Path)
	if err != nil || !ok {
		return nil, err
	}

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
		if count <= 0 {
			continue
		}
		// The resources of one item are identical, so one planned message prices them
		// all: a count such as a node set's size has no upper bound, and materializing
		// a message per resource would spend the request's memory and time before any
		// limit applies.
		create, plannedMessage, err := plannedCreate(creates, item, types, byName, uncreated)
		if err != nil {
			return nil, err
		}
		createdDimensions, _, err := priceOf(plannedMessage, byName[creates.Kind], types, byName, uncreated)
		if err != nil {
			return nil, err
		}
		for dimension, amount := range createdDimensions {
			// A count such as a node set's size is caller-supplied and unbounded, so
			// the product can overflow int64: a wrapped negative amount would drop out
			// of the check's added map and let the request past its limit.
			total := amount * int64(count)
			if amount != 0 && total/amount != int64(count) {
				return nil, fmt.Errorf(
					"dimension '%s' overflows: %d resources of %d each", dimension, count, amount,
				)
			}
			if err := addDimension(dimensions, dimension, total); err != nil {
				return nil, err
			}
		}
		create.Count = count
		planned = append(planned, create)
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
		// The path resolves the way validation resolves it, so a nested path such as
		// "wrapper.items" reaches the list inside the wrapper message.
		list, _, ok, err := navigate(value.Message(), each)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		return listItems(list.List()), nil
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
	creates *Creates, item protoreflect.Value, types TypeLookup, byName map[string]*Kind,
	uncreated map[string]bool,
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
			// The path can read the name of a reference given by id only: the created
			// resource stores the referenced row's name, so the price looks the row up.
			// A reference that also names a row needs no lookup: the servers resolve
			// references before the check runs and store the row's id and name, so the
			// supplied name already is the resolved row's name. Resolving the id again
			// would also fail for rows no quota kind maps, such as a storage tier.
			var resolved bool
			value, source, resolved, err = referencedName(item.Message(), creates.Set[target], types)
			if err != nil {
				return PlannedCreate{}, nil, err
			}
			if !resolved {
				return PlannedCreate{}, nil, fmt.Errorf(
					"creates set path '%s' isn't set", creates.Set[target],
				)
			}
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

// mergeMax adds to the destination the larger amount of each dimension in a and b.
func mergeMax(dst, a, b map[string]int64) error {
	for dimension, amount := range a {
		if b[dimension] > amount {
			amount = b[dimension]
		}
		if err := addDimension(dst, dimension, amount); err != nil {
			return err
		}
	}
	for dimension, amount := range b {
		if _, ok := a[dimension]; !ok {
			if err := addDimension(dst, dimension, amount); err != nil {
				return err
			}
		}
	}
	return nil
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

// referenceKey reads the key a type reference points at: the id when set, else the name,
// the way RefKeyStr resolves references everywhere else in the service
// (fulfillment-service/internal/controllers/references.go). The lookup resolves the key
// however it stores its rows.
func referenceKey(reference protoreflect.Message) (string, error) {
	if idField := reference.Descriptor().Fields().ByName("id"); idField != nil &&
		idField.Kind() == protoreflect.StringKind {
		if id := reference.Get(idField).String(); id != "" {
			return id, nil
		}
	}
	nameField := reference.Descriptor().Fields().ByName("name")
	if nameField == nil || nameField.Kind() != protoreflect.StringKind {
		return "", fmt.Errorf(
			"reference '%s' has no name", reference.Descriptor().FullName(),
		)
	}
	name := reference.Get(nameField).String()
	if name == "" {
		return "", fmt.Errorf("reference '%s' names nothing", reference.Descriptor().FullName())
	}
	return name, nil
}

// referencedName resolves the value of a set path that reads the name of a reference
// given by id only: the created resource stores the referenced row's name, such as a
// volume's storage tier, so the price looks the row up through the type lookup and
// copies its name. ok is false when the path reads no such reference or the reference
// carries no id.
func referencedName(
	item protoreflect.Message, path string, types TypeLookup,
) (value protoreflect.Value, field protoreflect.FieldDescriptor, ok bool, err error) {
	segments := strings.Split(path, ".")
	if len(segments) < 2 || segments[len(segments)-1] != "name" {
		return protoreflect.Value{}, nil, false, nil
	}
	reference, _, found, err := navigate(item, strings.Join(segments[:len(segments)-1], "."))
	if err != nil || !found {
		return protoreflect.Value{}, nil, false, err
	}
	message := reference.Message()
	idField := message.Descriptor().Fields().ByName("id")
	nameField := message.Descriptor().Fields().ByName("name")
	if idField == nil || idField.Kind() != protoreflect.StringKind ||
		nameField == nil || nameField.Kind() != protoreflect.StringKind {
		return protoreflect.Value{}, nil, false, nil
	}
	id := message.Get(idField).String()
	if id == "" {
		return protoreflect.Value{}, nil, false, nil
	}
	// The reference's kind is its message's name without the Reference suffix, such as
	// StorageTier from StorageTierReference.
	kind := strings.TrimSuffix(shortName(message.Descriptor().FullName()), "Reference")
	row := types.Type(kind, id)
	if row == nil {
		return protoreflect.Value{}, nil, false, fmt.Errorf(
			"set path '%s' references '%s' '%s', which the lookup doesn't know", path, kind, id,
		)
	}
	metadata := row.Descriptor().Fields().ByName("metadata")
	var rowName protoreflect.FieldDescriptor
	if metadata != nil && metadata.Kind() == protoreflect.MessageKind {
		rowName = metadata.Message().Fields().ByName("name")
	}
	if rowName == nil || rowName.Kind() != protoreflect.StringKind {
		return protoreflect.Value{}, nil, false, fmt.Errorf(
			"set path '%s' references '%s' '%s', whose row names nothing", path, kind, id,
		)
	}
	name := row.Get(metadata).Message().Get(rowName).String()
	if name == "" {
		return protoreflect.Value{}, nil, false, fmt.Errorf(
			"set path '%s' references '%s' '%s', whose row names nothing", path, kind, id,
		)
	}
	return protoreflect.ValueOfString(name), nameField, true, nil
}
