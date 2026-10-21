/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// GenericMapperBuilder contains the data and logic neede to create new generic mapper.
type GenericMapperBuilder[From, To proto.Message] struct {
	logger        *slog.Logger
	strict        bool
	ignoredFields []any
}

// GenericMapper is knows how to map the private representation of an object to the corresponding public representation,
// and the other way around.
type GenericMapper[From, To proto.Message] struct {
	logger                *slog.Logger
	strict                bool
	ignoredFieldNames     map[protoreflect.Name]bool
	ignoredFieldFullNames map[protoreflect.FullName]bool
}

// NewGenericMapper creates a builder that can then be used to configure and create a new generic mapper.
func NewGenericMapper[From, To proto.Message]() *GenericMapperBuilder[From, To] {
	return &GenericMapperBuilder[From, To]{}
}

// SetLogger sets the logger. This is mandatory.
func (b *GenericMapperBuilder[From, To]) SetLogger(value *slog.Logger) *GenericMapperBuilder[From, To] {
	b.logger = value
	return b
}

// SetStrict sets the flag if the copy should be strict, meaning that it will fail if there are fields in the `from`
// object that don't exist in the `to` object. This is optional and the default is `false`.
func (b *GenericMapperBuilder[From, To]) SetStrict(value bool) *GenericMapperBuilder[From, To] {
	b.strict = value
	return b
}

// AddIgnoredFields adds a set of fields to be omitted when mapping. The values passed can be of the following types:
//
// string - This should be a field name, for example 'status' and then any field with that name in any object will
// be ignored.
//
// protoreflect.Name - Like string.
//
// protoreflect.FullName - This indicates a field of a particular type. For example, if the value is
// 'osac.public.v1.Cluster.status' then only the 'status' field of the 'osac.public.v1.Cluster' object will be ignored.
func (b *GenericMapperBuilder[Fro, To]) AddIgnoredFields(values ...any) *GenericMapperBuilder[Fro, To] {
	b.ignoredFields = append(b.ignoredFields, values...)
	return b
}

// Build uses the configuration stored in the builder to create and configure a new generic mapper.
func (b *GenericMapperBuilder[From, To]) Build() (result *GenericMapper[From, To], err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Create the sets of ignored fields:
	ignoredFieldNames := make(map[protoreflect.Name]bool)
	ignoredFieldFullNames := make(map[protoreflect.FullName]bool)
	for i, ignoredField := range b.ignoredFields {
		switch ignoredField := ignoredField.(type) {
		case string:
			if strings.Contains(ignoredField, ".") {
				ignoredFieldFullNames[protoreflect.FullName(ignoredField)] = true
			} else {
				ignoredFieldNames[protoreflect.Name(ignoredField)] = true
			}
		case protoreflect.Name:
			ignoredFieldNames[ignoredField] = true
		case protoreflect.FullName:
			ignoredFieldFullNames[ignoredField] = true
		default:
			err = fmt.Errorf(
				"ignored fields should be strings or protocol buffers field names, but value %d is "+
					"of type '%T'",
				i, ignoredField,
			)
			return
		}
	}

	// Create and populate the object:
	result = &GenericMapper[From, To]{
		logger:                b.logger,
		ignoredFieldNames:     ignoredFieldNames,
		ignoredFieldFullNames: ignoredFieldFullNames,
		strict:                b.strict,
	}
	return
}

// Copy copies the data from the `from` object into the `to` object.
func (m *GenericMapper[From, To]) Copy(ctx context.Context, from From, to To) error {
	fromReflect := from.ProtoReflect()
	toReflect := to.ProtoReflect()
	if !fromReflect.IsValid() || !toReflect.IsValid() {
		return nil
	}
	if err := validatePublicObjectMetadata(from); err != nil {
		return err
	}
	if err := m.copyMessage(fromReflect, toReflect); err != nil {
		return err
	}
	if toReflect.Descriptor().ParentFile().Package() == "osac.public.v1" {
		filterReservedMetadata(toReflect)
	}
	return nil
}

const (
	reservedMetadataDomain = "admin.osac.openshift.io"
	reservedMetadataError  = "Label or annotation is reserved for use by the system."
)

func isReservedMetadataKey(key string) bool {
	separator := strings.IndexByte(key, '/')
	if separator < 0 {
		return false
	}
	domain := key[:separator]
	return domain == reservedMetadataDomain || strings.HasSuffix(domain, "."+reservedMetadataDomain)
}

func restoreReservedMetadata(current, candidate proto.Message) {
	if current == nil || candidate == nil {
		return
	}
	currentMessage := current.ProtoReflect()
	candidateMessage := candidate.ProtoReflect()
	if !currentMessage.IsValid() || !candidateMessage.IsValid() ||
		currentMessage.Descriptor().FullName() != candidateMessage.Descriptor().FullName() {
		return
	}
	currentMetadataField := currentMessage.Descriptor().Fields().ByName("metadata")
	candidateMetadataField := candidateMessage.Descriptor().Fields().ByName("metadata")
	if currentMetadataField == nil || candidateMetadataField == nil ||
		currentMetadataField.Message() == nil || candidateMetadataField.Message() == nil ||
		!currentMessage.Has(currentMetadataField) {
		return
	}
	currentMetadata := currentMessage.Get(currentMetadataField).Message()
	hasCurrentReservedMetadata := false
	for _, name := range []protoreflect.Name{"labels", "annotations"} {
		currentField := currentMetadata.Descriptor().Fields().ByName(name)
		if currentField == nil || !currentField.IsMap() || currentField.MapValue().Kind() != protoreflect.StringKind {
			continue
		}
		currentMetadata.Get(currentField).Map().Range(func(key protoreflect.MapKey, _ protoreflect.Value) bool {
			if isReservedMetadataKey(key.String()) {
				hasCurrentReservedMetadata = true
				return false
			}
			return true
		})
	}
	if !candidateMessage.Has(candidateMetadataField) && !hasCurrentReservedMetadata {
		return
	}
	candidateMetadata := candidateMessage.Mutable(candidateMetadataField).Message()
	for _, name := range []protoreflect.Name{"labels", "annotations"} {
		currentField := currentMetadata.Descriptor().Fields().ByName(name)
		candidateField := candidateMetadata.Descriptor().Fields().ByName(name)
		if currentField == nil || candidateField == nil || !currentField.IsMap() || !candidateField.IsMap() ||
			currentField.MapValue().Kind() != protoreflect.StringKind || candidateField.MapValue().Kind() != protoreflect.StringKind {
			continue
		}
		candidateValues := candidateMetadata.Mutable(candidateField).Map()
		var candidateReservedKeys []protoreflect.MapKey
		candidateValues.Range(func(key protoreflect.MapKey, _ protoreflect.Value) bool {
			if isReservedMetadataKey(key.String()) {
				candidateReservedKeys = append(candidateReservedKeys, key)
			}
			return true
		})
		for _, key := range candidateReservedKeys {
			candidateValues.Clear(key)
		}
		currentMetadata.Get(currentField).Map().Range(func(key protoreflect.MapKey, value protoreflect.Value) bool {
			if isReservedMetadataKey(key.String()) {
				candidateValues.Set(key, value)
			}
			return true
		})
	}
}

func validatePublicMetadata(message protoreflect.Message) error {
	if !message.IsValid() {
		return nil
	}
	if message.Descriptor().FullName() == "osac.public.v1.Metadata" {
		for _, name := range []protoreflect.Name{"labels", "annotations"} {
			field := message.Descriptor().Fields().ByName(name)
			if field == nil || !message.Has(field) {
				continue
			}
			values := message.Get(field).Map()
			reserved := false
			values.Range(func(key protoreflect.MapKey, _ protoreflect.Value) bool {
				reserved = isReservedMetadataKey(key.String())
				return !reserved
			})
			if reserved {
				return grpcstatus.Error(grpccodes.InvalidArgument, reservedMetadataError)
			}
		}
	}

	fields := message.Descriptor().Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		if !message.Has(field) {
			continue
		}
		if field.IsList() {
			if field.Message() == nil {
				continue
			}
			list := message.Get(field).List()
			for j := range list.Len() {
				if err := validatePublicMetadata(list.Get(j).Message()); err != nil {
					return err
				}
			}
			continue
		}
		if field.IsMap() {
			if field.MapValue().Message() == nil {
				continue
			}
			var validationErr error
			message.Get(field).Map().Range(func(_ protoreflect.MapKey, value protoreflect.Value) bool {
				validationErr = validatePublicMetadata(value.Message())
				return validationErr == nil
			})
			if validationErr != nil {
				return validationErr
			}
			continue
		}
		if field.Message() != nil {
			if err := validatePublicMetadata(message.Get(field).Message()); err != nil {
				return err
			}
		}
	}
	return nil
}

func validatePublicObjectMetadata(object proto.Message) error {
	if object == nil {
		return nil
	}
	message := object.ProtoReflect()
	if !message.IsValid() || message.Descriptor().ParentFile().Package() != "osac.public.v1" {
		return nil
	}
	return validatePublicMetadata(message)
}

func publicInputMappingError(err error, message string) error {
	if grpcstatus.Code(err) == grpccodes.InvalidArgument {
		return err
	}
	return grpcstatus.Error(grpccodes.Internal, message)
}

func filterReservedMetadata(message protoreflect.Message) {
	if !message.IsValid() {
		return
	}
	if message.Descriptor().FullName() == "osac.public.v1.Metadata" {
		for _, name := range []protoreflect.Name{"labels", "annotations"} {
			field := message.Descriptor().Fields().ByName(name)
			if field == nil || !message.Has(field) {
				continue
			}
			values := message.Get(field).Map()
			var reservedKeys []protoreflect.MapKey
			values.Range(func(key protoreflect.MapKey, _ protoreflect.Value) bool {
				if isReservedMetadataKey(key.String()) {
					reservedKeys = append(reservedKeys, key)
				}
				return true
			})
			for _, key := range reservedKeys {
				values.Clear(key)
			}
		}
	}

	fields := message.Descriptor().Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		if !message.Has(field) {
			continue
		}
		if field.IsList() {
			if field.Message() == nil {
				continue
			}
			list := message.Get(field).List()
			for j := range list.Len() {
				filterReservedMetadata(list.Get(j).Message())
			}
			continue
		}
		if field.IsMap() {
			if field.MapValue().Message() == nil {
				continue
			}
			values := message.Get(field).Map()
			values.Range(func(_ protoreflect.MapKey, value protoreflect.Value) bool {
				filterReservedMetadata(value.Message())
				return true
			})
			continue
		}
		if field.Message() != nil {
			filterReservedMetadata(message.Get(field).Message())
		}
	}
}

func (m *GenericMapper[From, To]) copyMessage(from, to protoreflect.Message) error {
	fromFields := from.Descriptor().Fields()
	toFields := to.Descriptor().Fields()
	for i := range fromFields.Len() {
		fromField := fromFields.Get(i)
		if m.shouldIgnore(fromField) {
			continue
		}
		toField := toFields.ByName(fromField.Name())
		if toField == nil {
			if m.strict {
				return fmt.Errorf(
					"type '%s' doesn't have a '%s' field",
					to.Descriptor().FullName(),
					fromField.Name(),
				)
			}
			continue
		}
		if m.shouldIgnore(toField) {
			continue
		}
		if !from.Has(fromField) {
			to.Clear(toField)
			continue
		}
		fromValue := from.Get(fromField)
		switch {
		case fromField.IsList():
			fromList := fromValue.List()
			toList := to.Mutable(toField).List()
			err := m.copyList(fromList, toList, fromField)
			if err != nil {
				return err
			}
		case fromField.IsMap():
			fromMap := fromValue.Map()
			toMap := to.Mutable(toField).Map()
			err := m.copyMap(fromMap, toMap, fromField.MapValue())
			if err != nil {
				return err
			}
		case fromField.Message() != nil:
			fromMessage := fromValue.Message()
			toMessage := to.Mutable(toField).Message()
			err := m.copyMessage(fromMessage, toMessage)
			if err != nil {
				return err
			}
		case fromField.Kind() == protoreflect.BytesKind:
			to.Set(toField, m.cloneBytes(fromValue))
		default:
			to.Set(toField, fromValue)
		}
	}
	return nil
}

func (m *GenericMapper[From, To]) copyList(from, to protoreflect.List, field protoreflect.FieldDescriptor) error {
	to.Truncate(0)
	for i := range from.Len() {
		fromValue := from.Get(i)
		switch {
		case field.Message() != nil:
			toValue := to.NewElement()
			err := m.copyMessage(fromValue.Message(), toValue.Message())
			if err != nil {
				return err
			}
			to.Append(toValue)
		case field.Kind() == protoreflect.BytesKind:
			to.Append(m.cloneBytes(fromValue))
		default:
			to.Append(fromValue)
		}
	}
	return nil
}

func (m *GenericMapper[From, To]) copyMap(from, to protoreflect.Map, field protoreflect.FieldDescriptor) (err error) {
	from.Range(func(fromKey protoreflect.MapKey, fromValue protoreflect.Value) bool {
		switch {
		case field.Message() != nil:
			toValue := to.NewValue()
			err = m.copyMessage(fromValue.Message(), toValue.Message())
			if err != nil {
				return false
			}
			to.Set(fromKey, toValue)
		case field.Kind() == protoreflect.BytesKind:
			to.Set(fromKey, m.cloneBytes(fromValue))
		default:
			to.Set(fromKey, fromValue)
		}
		return true
	})
	return
}

// Merge merges the data from the `from` object into the `to` object.
func (m *GenericMapper[From, To]) Merge(ctx context.Context, from From, to To) error {
	fromReflect := from.ProtoReflect()
	toReflect := to.ProtoReflect()
	if !fromReflect.IsValid() || !toReflect.IsValid() {
		return nil
	}
	return m.mergeMessage(fromReflect, toReflect)
}

func (m *GenericMapper[From, To]) mergeMessage(from, to protoreflect.Message) error {
	fromFields := from.Descriptor().Fields()
	toFields := to.Descriptor().Fields()
	for i := range fromFields.Len() {
		fromField := fromFields.Get(i)
		if m.shouldIgnore(fromField) {
			continue
		}
		toField := toFields.ByName(fromField.Name())
		if toField == nil {
			if m.strict {
				return fmt.Errorf(
					"type '%s' doesn't have a '%s' field",
					to.Descriptor().FullName(),
					fromField.Name(),
				)
			} else {
				continue
			}
		}
		if m.shouldIgnore(toField) {
			continue
		}
		if !from.Has(fromField) {
			continue
		}
		fromValue := from.Get(fromField)
		switch {
		case fromField.IsList():
			fromList := fromValue.List()
			toList := to.Mutable(toField).List()
			err := m.mergeList(fromList, toList, fromField)
			if err != nil {
				return err
			}
		case fromField.IsMap():
			fromMap := fromValue.Map()
			toMap := to.Mutable(toField).Map()
			err := m.mergeMap(fromMap, toMap, fromField.MapValue())
			if err != nil {
				return err
			}
		case fromField.Message() != nil:
			fromMessage := fromValue.Message()
			toMessage := to.Mutable(toField).Message()
			err := m.mergeMessage(fromMessage, toMessage)
			if err != nil {
				return err
			}
		case fromField.Kind() == protoreflect.BytesKind:
			to.Set(toField, m.cloneBytes(fromValue))
		default:
			to.Set(toField, fromValue)
		}
	}
	return nil
}

func (m *GenericMapper[From, To]) mergeList(from, to protoreflect.List, field protoreflect.FieldDescriptor) error {
	for i := range from.Len() {
		fromValue := from.Get(i)
		switch {
		case field.Message() != nil:
			toValue := to.NewElement()
			err := m.copyMessage(fromValue.Message(), toValue.Message())
			if err != nil {
				return err
			}
			to.Append(toValue)
		case field.Kind() == protoreflect.BytesKind:
			to.Append(m.cloneBytes(fromValue))
		default:
			to.Append(fromValue)
		}
	}
	return nil
}

func (m *GenericMapper[From, To]) mergeMap(from, to protoreflect.Map, field protoreflect.FieldDescriptor) (err error) {
	from.Range(func(fromKey protoreflect.MapKey, fromValue protoreflect.Value) bool {
		switch {
		case field.Message() != nil:
			if to.Has(fromKey) {
				toValue := to.Get(fromKey)
				err = m.mergeMessage(fromValue.Message(), toValue.Message())
				if err != nil {
					return false
				}
			} else {
				toValue := to.NewValue()
				err = m.copyMessage(fromValue.Message(), toValue.Message())
				if err != nil {
					return false
				}
				to.Set(fromKey, toValue)
			}
		case field.Kind() == protoreflect.BytesKind:
			to.Set(fromKey, m.cloneBytes(fromValue))
		default:
			to.Set(fromKey, fromValue)
		}
		return true
	})
	return
}

func (m *GenericMapper[From, To]) shouldIgnore(field protoreflect.FieldDescriptor) bool {
	return m.ignoredFieldNames[field.Name()] || m.ignoredFieldFullNames[field.FullName()]
}

func (m *GenericMapper[From, To]) cloneBytes(v protoreflect.Value) protoreflect.Value {
	return protoreflect.ValueOfBytes(slices.Clone(v.Bytes()))
}
