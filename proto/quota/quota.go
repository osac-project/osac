/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

// Package quota is the shared registry of each resource kind's price, built from the quota
// annotations on the private protos (see osac/private/v1/quota_type.proto for what the
// annotations mean and examples of each option).
//
// Kinds() walks the compiled proto descriptors of the private package, collects every quota
// annotation, validates it (a dimension's brace paths must name real string fields of the
// kind, a through or creates kind must exist and carry annotations, and every kind must
// either carry annotations or be on the list of kinds that use nothing), and returns the
// annotations per kind. It fails loudly on an annotation that is invalid, so a typo in an
// annotation is a startup failure, not a silently uncounted resource.
//
// Price() computes the full cost of one request: the dimensions and amounts it uses,
// including what OSAC will create for it, with the larger-of rule applied on each
// dimension. Links() exposes the created_for link descriptors for the usage query.
package quota

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

// privatePackage is the package of the private protos, where the quota annotations live.
const privatePackage protoreflect.FullName = "osac.private.v1"

// kindsThatUseNothing lists the kinds that hold no quota: no dimension counts them or
// anything about them, derived from the PRD's dimension table. Every other kind must carry
// annotations, so a new kind can't appear without a decision about its price. The list
// also holds stored kinds that sit outside the standard object shape, such as templates,
// catalog items and reviews, which the registry's net wouldn't otherwise reach.
var kindsThatUseNothing = []string{
	"AddOnOperator",
	"BareMetalInstanceCatalogItem",
	"BareMetalInstanceTemplate",
	"ClusterCatalogItem",
	"ClusterTemplate",
	"ClusterVersion",
	"ComputeInstanceCatalogItem",
	"ComputeInstanceTemplate",
	"Event",
	"ExternalIPAttachment",
	"ExternalIPPool",
	"FabricDomain",
	"Hub",
	"IdentityProvider",
	"NetworkClass",
	"Project",
	"ProjectMembership",
	"QuotaDefaultLimit",
	"QuotaLimit",
	"QuotaLimitRequest",
	"QuotaThreshold",
	"Role",
	"RoleBinding",
	"Secret",
	"SecurityGroup",
	"SelfSubjectAccessReview",
	"ServiceAccount",
	"StorageBackend",
	"StorageTier",
	"Subnet",
	"Tenant",
	"User",
}

// Kind is the quota price of one resource kind, from its annotations.
type Kind struct {
	// Name is the kind's short name, such as "ComputeInstance".
	Name string

	// Message is the full proto name of the kind's message.
	Message protoreflect.FullName

	// Dimension is the kind's message-level dimension: each resource of the kind uses 1
	// of it. On a type, each resource of that type uses 1. Empty when the kind has no
	// message-level dimension.
	Dimension string

	// CreatedFor is the kind's message-level link to the parent it was created for.
	// Nil when the kind has no message-level link.
	CreatedFor *CreatedFor

	// Fields are the kind's annotated fields, sorted by path.
	Fields []Field
}

// Field is one annotated field of a kind. Exactly one of Dimension, Through, Creates and
// CreatedFor is set.
type Field struct {
	// Path is the field's proto path from the resource, such as "spec.instance_type".
	Path string

	// Dimension is the dimension the field's value counts against, with brace paths
	// resolved from the resource. Empty for the other annotations.
	Dimension string

	// Through names the kind of type the field's reference points at, such as
	// "InstanceType". The resource uses what the type's annotations say.
	Through string

	// LargerOf is the path of a second field that points at the same kind of type, such
	// as "status.instance_type". The resource uses the larger amount on each dimension.
	// Empty when the resource uses only the referenced type.
	LargerOf string

	// Creates says what OSAC creates for the field's value. Nil when the field creates
	// nothing.
	Creates *Creates

	// CreatedFor is the link the field itself holds. Nil when the field holds no link.
	CreatedFor *CreatedFor
}

// Creates says what OSAC creates for an annotated field's value.
type Creates struct {
	// Kind is the kind of resource created, such as "Volume".
	Kind string

	// Each names the list field inside the annotated field's message that holds the
	// items, such as "items". Empty when the field's value is itself the item.
	Each string

	// Count is how many resources to create for each item. Zero means CountField
	// decides, and one when neither is set.
	Count int32

	// CountField is the item field that says how many resources to create for it, such
	// as a node set's "size".
	CountField string

	// Set maps each field path of the new resource to the path read from the item, such
	// as "spec.size_gib" from "size_gib".
	Set map[string]string
}

// CreatedFor says how a resource that OSAC creates for a parent points back to it.
type CreatedFor struct {
	// Parent is the parent's kind, such as "ComputeInstance". Empty for label links,
	// whose kind label carries the parent's kind.
	Parent string

	// NamePrefix names the parent's created resources: "{parent}" is the parent's ID, so
	// "ci-{parent}-" names a VM's disk volume "ci-<vm id>-boot". Empty for field and
	// label links.
	NamePrefix string

	// IDLabel and KindLabel name the labels that hold the parent's ID and kind, such as
	// the labels of an automatic external IP address. Empty for field and name links.
	IDLabel   string
	KindLabel string
}

// Kinds returns the quota annotations of every kind that carries them, sorted by name.
// It fails if an annotation is invalid or isn't reachable from a resource message: the
// registry only knows how to price resources whose price is declared where the resource
// is defined.
func Kinds() ([]Kind, error) {
	kinds, _, err := kindsOf(protoregistry.GlobalFiles)
	if err != nil {
		return nil, err
	}
	for _, name := range kindsThatUseNothing {
		// A listed kind can sit outside the object shape, so the check resolves it
		// against the package's messages at large, not only the resource messages.
		full := privatePackage.Append(protoreflect.Name(name))
		descriptor, findErr := protoregistry.GlobalFiles.FindDescriptorByName(full)
		if _, ok := descriptor.(protoreflect.MessageDescriptor); findErr != nil || !ok {
			return nil, fmt.Errorf("kind '%s' is listed as using nothing but doesn't exist", name)
		}
	}
	return kinds, nil
}

// kindsOf collects the registry over one file registry. Kinds reads the compiled private
// protos; tests feed it a synthetic registry. It also returns the universe: every
// resource message of the package, by short name.
func kindsOf(files *protoregistry.Files) (result []Kind, universe map[string]protoreflect.MessageDescriptor, err error) {
	// Collect the annotated fields and message options reachable from each top-level
	// message, so that resource messages and orphaned annotations can be told apart
	// afterwards:
	type root struct {
		message    protoreflect.MessageDescriptor
		messageOpt *privatev1.QuotaMessageOptions
		fields     []annotatedField
	}
	var roots []root
	files.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if file.Package() != privatePackage {
			return true
		}
		messages := file.Messages()
		for i := range messages.Len() {
			message := messages.Get(i)
			var messageOpt *privatev1.QuotaMessageOptions
			if proto.HasExtension(message.Options(), privatev1.E_QuotaMessage) {
				options, ok := proto.GetExtension(
					message.Options(), privatev1.E_QuotaMessage,
				).(*privatev1.QuotaMessageOptions)
				if !ok || options == nil {
					err = fmt.Errorf("message '%s' has an invalid quota annotation", message.FullName())
					return false
				}
				messageOpt = options
			}
			fields, fieldErr := walk(message, nil)
			if fieldErr != nil {
				err = fieldErr
				return false
			}
			roots = append(roots, root{message: message, messageOpt: messageOpt, fields: fields})
			if nestedErr := checkNestedMessageOptions(message); nestedErr != nil {
				err = nestedErr
				return false
			}
		}
		return true
	})
	if err != nil {
		return nil, nil, err
	}

	// The universe is every resource message, annotated or not, so kind names in
	// annotations can be checked against it:
	universe = map[string]protoreflect.MessageDescriptor{}
	for _, r := range roots {
		if isObjectShape(r.message) {
			universe[shortName(r.message.FullName())] = r.message
		}
	}

	// Turn the annotated resource messages into the registry and remember which
	// annotated fields they cover:
	covered := map[protoreflect.FullName]bool{}
	for _, r := range roots {
		if !isObjectShape(r.message) {
			if r.messageOpt != nil {
				return nil, nil, fmt.Errorf(
					"message '%s' has a quota message option but isn't a resource message",
					r.message.FullName(),
				)
			}
			continue
		}
		if r.messageOpt == nil && len(r.fields) == 0 {
			continue
		}
		kind, kindErr := newKind(r.message, r.messageOpt, r.fields, universe)
		if kindErr != nil {
			return nil, nil, kindErr
		}
		result = append(result, *kind)
		for _, annotated := range r.fields {
			covered[annotated.descriptor.FullName()] = true
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })

	// An annotated field that no resource covers can't be priced, so the annotations are
	// wrong rather than the registry incomplete:
	for _, r := range roots {
		for _, annotated := range r.fields {
			if !covered[annotated.descriptor.FullName()] {
				return nil, nil, fmt.Errorf(
					"field '%s' has a quota annotation but isn't part of a resource message",
					annotated.descriptor.FullName(),
				)
			}
		}
	}

	// A through or creates annotation must name a kind that carries annotations: one
	// that uses nothing would price zero, silently.
	annotated := map[string]bool{}
	for _, kind := range result {
		annotated[kind.Name] = true
	}
	for _, kind := range result {
		for _, field := range kind.Fields {
			if field.Through != "" && !annotated[field.Through] {
				return nil, nil, fmt.Errorf(
					"field '%s' of '%s' resolves through '%s', which has no quota annotations",
					field.Path, kind.Message, field.Through,
				)
			}
			if field.Creates != nil && !annotated[field.Creates.Kind] {
				return nil, nil, fmt.Errorf(
					"field '%s' of '%s' creates '%s', which has no quota annotations",
					field.Path, kind.Message, field.Creates.Kind,
				)
			}
		}
	}

	// Every kind must have a price: annotated, or on the list of kinds that use nothing.
	// A kind on the list that carries annotations is a contradiction.
	onList := map[string]bool{}
	for _, name := range kindsThatUseNothing {
		onList[name] = true
	}
	for name := range universe {
		if !annotated[name] && !onList[name] {
			return nil, nil, fmt.Errorf(
				"kind '%s' has no quota annotations and isn't on the list of kinds that use nothing", name,
			)
		}
		if annotated[name] && onList[name] {
			return nil, nil, fmt.Errorf(
				"kind '%s' has quota annotations but is listed as using nothing", name,
			)
		}
	}

	if err := checkCreatesCycles(result); err != nil {
		return nil, nil, err
	}
	return result, universe, nil
}

// shortName returns the message name without its package.
func shortName(full protoreflect.FullName) string {
	return string(full.Name())
}

// annotatedField is a field carrying a quota annotation, with its path from the message
// the walk started at.
type annotatedField struct {
	descriptor protoreflect.FieldDescriptor
	path       string
}

// walk collects the annotated fields of the message and of the singular message fields
// inside it. Annotations on repeated and map fields sit on the field itself, so the walk
// doesn't descend into them.
func walk(message protoreflect.MessageDescriptor, prefix []string) (result []annotatedField, err error) {
	fields := message.Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		segments := append(slices.Clone(prefix), string(field.Name()))
		path := strings.Join(segments, ".")
		if proto.HasExtension(field.Options(), privatev1.E_Quota) {
			options, ok := proto.GetExtension(field.Options(), privatev1.E_Quota).(*privatev1.QuotaFieldOptions)
			if !ok || options == nil {
				return nil, fmt.Errorf("field '%s' has an invalid quota annotation", field.FullName())
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

// checkNestedMessageOptions rejects a quota message option on a message declared inside
// another message: the option belongs on a top-level resource message.
func checkNestedMessageOptions(message protoreflect.MessageDescriptor) error {
	messages := message.Messages()
	for i := range messages.Len() {
		nested := messages.Get(i)
		if proto.HasExtension(nested.Options(), privatev1.E_QuotaMessage) {
			return fmt.Errorf(
				"message '%s' has a quota message option but isn't a resource message", nested.FullName(),
			)
		}
		if err := checkNestedMessageOptions(nested); err != nil {
			return err
		}
	}
	return nil
}

// newKind validates the annotations of one resource message and turns them into a Kind.
func newKind(
	message protoreflect.MessageDescriptor,
	messageOptions *privatev1.QuotaMessageOptions,
	annotated []annotatedField,
	universe map[string]protoreflect.MessageDescriptor,
) (*Kind, error) {
	kind := &Kind{Name: shortName(message.FullName()), Message: message.FullName()}
	where := fmt.Sprintf("kind '%s'", kind.Name)

	if messageOptions != nil {
		if messageOptions.GetDimension() != "" {
			if err := validateDimension(message, messageOptions.GetDimension(), where); err != nil {
				return nil, err
			}
			kind.Dimension = messageOptions.GetDimension()
		}
		createdFor, err := newCreatedFor(messageOptions.GetCreatedFor(), false, where)
		if err != nil {
			return nil, err
		}
		if createdFor != nil && createdFor.Parent != "" {
			if _, ok := universe[createdFor.Parent]; !ok {
				return nil, fmt.Errorf(
					"%s created_for names parent '%s', which doesn't exist", where, createdFor.Parent,
				)
			}
		}
		kind.CreatedFor = createdFor
	}

	for _, annotated := range annotated {
		field, err := newField(message, annotated, universe)
		if err != nil {
			return nil, err
		}
		kind.Fields = append(kind.Fields, *field)
	}
	sort.Slice(kind.Fields, func(i, j int) bool { return kind.Fields[i].Path < kind.Fields[j].Path })
	return kind, nil
}

// newField validates the annotation of one field.
func newField(
	resource protoreflect.MessageDescriptor,
	annotated annotatedField,
	universe map[string]protoreflect.MessageDescriptor,
) (*Field, error) {
	options, _ := proto.GetExtension(
		annotated.descriptor.Options(), privatev1.E_Quota,
	).(*privatev1.QuotaFieldOptions)
	where := fmt.Sprintf("field '%s' of '%s'", annotated.path, resource.FullName())

	field := &Field{Path: annotated.path}
	shapes := 0
	if options.GetDimension() != "" {
		shapes++
	}
	if options.GetThrough() != "" {
		shapes++
	}
	if options.GetCreates() != nil {
		shapes++
	}
	if options.GetCreatedFor() != nil {
		shapes++
	}
	if shapes != 1 {
		return nil, fmt.Errorf(
			"%s must set exactly one of dimension, through, creates and created_for", where,
		)
	}

	switch {
	case options.GetDimension() != "":
		if !integerKind(annotated.descriptor.Kind()) {
			return nil, fmt.Errorf("%s counts a dimension but isn't an integer field", where)
		}
		if err := validateDimension(resource, options.GetDimension(), where); err != nil {
			return nil, err
		}
		field.Dimension = options.GetDimension()

	case options.GetThrough() != "":
		if annotated.descriptor.Kind() != protoreflect.MessageKind ||
			annotated.descriptor.IsList() || annotated.descriptor.IsMap() {
			return nil, fmt.Errorf("%s resolves through a type but isn't a reference", where)
		}
		name := annotated.descriptor.Message().Fields().ByName("name")
		if name == nil || name.Kind() != protoreflect.StringKind {
			return nil, fmt.Errorf(
				"%s resolves through '%s', whose reference has no name field",
				where, options.GetThrough(),
			)
		}
		if _, ok := universe[options.GetThrough()]; !ok {
			return nil, fmt.Errorf(
				"%s resolves through '%s', which doesn't exist", where, options.GetThrough(),
			)
		}
		field.Through = options.GetThrough()
		if larger := options.GetLargerOf(); larger != "" {
			largerField, err := resolveField(resource, larger)
			if err != nil {
				return nil, fmt.Errorf("%s has an invalid larger_of path: %w", where, err)
			}
			if largerField.Kind() != protoreflect.MessageKind ||
				largerField.Message().FullName() != annotated.descriptor.Message().FullName() {
				return nil, fmt.Errorf("%s has a larger_of path that isn't the same kind of reference", where)
			}
			field.LargerOf = larger
		}

	case options.GetCreates() != nil:
		creates, err := newCreates(resource, annotated, options.GetCreates(), universe)
		if err != nil {
			return nil, err
		}
		field.Creates = creates

	case options.GetCreatedFor() != nil:
		if annotated.descriptor.Kind() != protoreflect.MessageKind ||
			annotated.descriptor.IsList() || annotated.descriptor.IsMap() {
			return nil, fmt.Errorf("%s holds a link but isn't a reference", where)
		}
		createdFor, err := newCreatedFor(options.GetCreatedFor(), true, where)
		if err != nil {
			return nil, err
		}
		field.CreatedFor = createdFor
	}

	if field.CreatedFor != nil && field.CreatedFor.Parent != "" {
		if _, ok := universe[field.CreatedFor.Parent]; !ok {
			return nil, fmt.Errorf(
				"%s created_for names parent '%s', which doesn't exist", where, field.CreatedFor.Parent,
			)
		}
	}
	return field, nil
}

// newCreates validates the creates annotation of one field.
func newCreates(
	resource protoreflect.MessageDescriptor,
	annotated annotatedField,
	options *privatev1.QuotaCreatesOptions,
	universe map[string]protoreflect.MessageDescriptor,
) (*Creates, error) {
	where := fmt.Sprintf("field '%s' of '%s'", annotated.path, resource.FullName())
	field := annotated.descriptor
	if options.GetKind() == "" {
		return nil, fmt.Errorf("%s creates resources but names no kind", where)
	}
	target, ok := universe[options.GetKind()]
	if !ok {
		return nil, fmt.Errorf("%s creates '%s', which doesn't exist", where, options.GetKind())
	}

	creates := &Creates{
		Kind:       options.GetKind(),
		Each:       options.GetEach(),
		Count:      options.GetCount(),
		CountField: options.GetCountField(),
		Set:        options.GetSet(),
	}
	if creates.Count > 0 && creates.CountField != "" {
		return nil, fmt.Errorf("%s sets both count and count_field", where)
	}

	// Find the item: the field's value, each entry of its list, or each value of its
	// map. A bool field has no item.
	var item protoreflect.MessageDescriptor
	switch {
	case field.Kind() == protoreflect.BoolKind:
		if creates.Each != "" || creates.CountField != "" || len(creates.Set) > 0 {
			return nil, fmt.Errorf(
				"%s is a bool, so its creates can name no items, count field or set fields", where,
			)
		}
	case field.IsList():
		if creates.Each != "" {
			return nil, fmt.Errorf("%s is a list, so its items are its entries", where)
		}
		item = field.Message()
	case field.IsMap():
		if creates.Each != "" {
			return nil, fmt.Errorf("%s is a map, so its items are its values", where)
		}
		if field.MapKey().Kind() != protoreflect.StringKind {
			return nil, fmt.Errorf("%s is a map with keys that aren't strings", where)
		}
		if field.MapValue().Kind() != protoreflect.MessageKind {
			return nil, fmt.Errorf("%s is a map with values that aren't messages", where)
		}
		item = field.MapValue().Message()
	case field.Kind() == protoreflect.MessageKind && creates.Each != "":
		each, err := resolveField(field.Message(), creates.Each)
		if err != nil {
			return nil, fmt.Errorf("%s has an invalid each path: %w", where, err)
		}
		if !each.IsList() {
			return nil, fmt.Errorf("%s reads items from '%s', which isn't a list", where, creates.Each)
		}
		item = each.Message()
	case field.Kind() == protoreflect.MessageKind:
		item = field.Message()
	default:
		return nil, fmt.Errorf("%s creates resources but is neither a bool, message, list nor map", where)
	}

	if item == nil {
		if creates.CountField != "" || len(creates.Set) > 0 {
			return nil, fmt.Errorf(
				"%s reads a count field or set fields of items that aren't messages", where,
			)
		}
		return creates, nil
	}
	if creates.CountField != "" {
		countField, err := resolveField(item, creates.CountField)
		if err != nil {
			return nil, fmt.Errorf("%s has an invalid count_field path: %w", where, err)
		}
		if !integerKind(countField.Kind()) {
			return nil, fmt.Errorf("%s counts through '%s', which isn't an integer field", where, creates.CountField)
		}
	}
	for _, set := range sortedKeys(creates.Set) {
		source, err := resolveField(item, creates.Set[set])
		if err != nil {
			return nil, fmt.Errorf("%s has an invalid set path: %w", where, err)
		}
		targetField, err := resolveField(target, set)
		if err != nil {
			return nil, fmt.Errorf("%s sets '%s': %w", where, set, err)
		}
		if !assignable(targetField, source) {
			return nil, fmt.Errorf(
				"%s sets '%s' from '%s', whose value doesn't fit", where, set, creates.Set[set],
			)
		}
	}
	return creates, nil
}

// newCreatedFor validates a created_for annotation. A field holds the link itself; a
// message links by name prefix or by labels.
func newCreatedFor(
	options *privatev1.QuotaCreatedForOptions, onField bool, where string,
) (*CreatedFor, error) {
	if options == nil {
		return nil, nil
	}
	createdFor := &CreatedFor{
		Parent:     options.GetParent(),
		NamePrefix: options.GetNamePrefix(),
		IDLabel:    options.GetIdLabel(),
		KindLabel:  options.GetKindLabel(),
	}
	nameLink := createdFor.NamePrefix != ""
	labelLink := createdFor.IDLabel != "" || createdFor.KindLabel != ""
	switch {
	case nameLink && labelLink:
		return nil, fmt.Errorf("%s links by both name and labels", where)
	case nameLink:
		if createdFor.Parent == "" {
			return nil, fmt.Errorf("%s links by name but names no parent kind", where)
		}
	case labelLink:
		if createdFor.IDLabel == "" || createdFor.KindLabel == "" {
			return nil, fmt.Errorf("%s links by labels but names only one of them", where)
		}
		if createdFor.Parent != "" {
			return nil, fmt.Errorf("%s links by labels, so its kind comes from its kind label", where)
		}
	default:
		if !onField {
			return nil, fmt.Errorf("%s links neither by name nor by labels", where)
		}
		if createdFor.Parent == "" {
			return nil, fmt.Errorf("%s holds the link but names no parent kind", where)
		}
	}
	return createdFor, nil
}

// checkCreatesCycles rejects annotations whose creates would recurse: a kind creating
// resources of a kind that creates resources of the first kind again would price forever.
func checkCreatesCycles(kinds []Kind) error {
	creates := map[string][]string{}
	for _, kind := range kinds {
		for _, field := range kind.Fields {
			if field.Creates != nil {
				creates[kind.Name] = append(creates[kind.Name], field.Creates.Kind)
			}
		}
	}
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var visit func(kind string, path []string) error
	visit = func(kind string, path []string) error {
		switch state[kind] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("creates a cycle: %s", strings.Join(slices.Concat(path, []string{kind}), " -> "))
		}
		state[kind] = visiting
		for _, next := range creates[kind] {
			if err := visit(next, slices.Concat(path, []string{kind})); err != nil {
				return err
			}
		}
		state[kind] = done
		return nil
	}
	for _, name := range sortedKeys(creates) {
		if err := visit(name, nil); err != nil {
			return err
		}
	}
	return nil
}

// validateDimension checks that every brace path of a dimension template names a string
// field of the kind, so the price can fill the dimension's name.
func validateDimension(kind protoreflect.MessageDescriptor, dimension, where string) error {
	paths, err := BracePaths(dimension)
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	for _, path := range paths {
		field, err := resolveField(kind, path)
		if err != nil {
			return fmt.Errorf("%s: dimension '%s' reads '%s': %w", where, dimension, path, err)
		}
		if field.Kind() != protoreflect.StringKind {
			return fmt.Errorf("%s: dimension '%s' reads '%s', which isn't a string", where, dimension, path)
		}
	}
	return nil
}

// BracePaths returns the paths in the braces of a dimension template, such as
// "spec.storage_tier" from "storage.{spec.storage_tier}.gib".
func BracePaths(template string) ([]string, error) {
	var paths []string
	rest := template
	for {
		start := strings.Index(rest, "{")
		if start < 0 {
			return paths, nil
		}
		end := strings.Index(rest[start:], "}")
		if end < 0 {
			return nil, fmt.Errorf("dimension '%s' has an unclosed brace", template)
		}
		path := rest[start+1 : start+end]
		if path == "" || strings.ContainsAny(path, "{}") {
			return nil, fmt.Errorf("dimension '%s' has an invalid brace path", template)
		}
		paths = append(paths, path)
		rest = rest[start+end+1:]
	}
}

// resolveField resolves a proto field path against a message and returns the field it
// names.
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

// integerKind reports whether a field of this proto kind can hold an amount.
func integerKind(kind protoreflect.Kind) bool {
	switch kind {
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return true
	}
	return false
}

// assignable reports whether a value read from the source field fits the target field of
// a created resource: the same kind, an int32 into an int64, or the same message type.
func assignable(target, source protoreflect.FieldDescriptor) bool {
	if target.Kind() == protoreflect.Int64Kind &&
		(source.Kind() == protoreflect.Int32Kind || source.Kind() == protoreflect.Int64Kind) {
		return true
	}
	if target.Kind() != source.Kind() {
		return false
	}
	if target.Kind() == protoreflect.MessageKind {
		return target.Message().FullName() == source.Message().FullName()
	}
	return true
}

// sortedKeys returns the keys of a map of strings, so validation and pricing walk
// annotations in a stable order.
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
