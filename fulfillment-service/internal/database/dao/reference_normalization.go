/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dao

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

// normalizeReferenceSelectors converts legacy shared selectors to tenant selectors and removes
// the legacy field before an object is written to JSONB.
func normalizeReferenceSelectors(message protoreflect.Message) error {
	return visitFullReferences(message, func(reference protoreflect.Message) error {
		descriptor := reference.Descriptor()
		fields := descriptor.Fields()
		tenantField := fields.ByName("tenant")
		sharedField := fields.ByName("shared")
		tenant := reference.Get(tenantField).String()
		shared := reference.Get(sharedField).Bool()
		if shared && tenant != "" && tenant != auth.SharedTenant {
			return fmt.Errorf("%s has conflicting tenant and shared selectors", descriptor.FullName())
		}
		if shared && tenant == "" {
			reference.Set(tenantField, protoreflect.ValueOfString(auth.SharedTenant))
		}
		reference.Clear(sharedField)
		return nil
	})
}

// canonicalizeSharedTenantSelectors keeps the compatibility representation stable
// in memory and in API responses. Older clients still use shared=true, while new
// clients use tenant="shared"; marshalData strips shared before persistence.
func canonicalizeSharedTenantSelectors(message protoreflect.Message) {
	_ = visitFullReferences(message, func(reference protoreflect.Message) error {
		fields := reference.Descriptor().Fields()
		tenantField := fields.ByName("tenant")
		sharedField := fields.ByName("shared")
		tenant := reference.Get(tenantField).String()
		shared := reference.Get(sharedField).Bool()
		if shared && tenant == "" {
			reference.Set(tenantField, protoreflect.ValueOfString(auth.SharedTenant))
		}
		if tenant == auth.SharedTenant || (tenant == "" && shared) {
			reference.Set(sharedField, protoreflect.ValueOfBool(true))
		}
		return nil
	})
}

func visitFullReferences(message protoreflect.Message, visit func(protoreflect.Message) error) error {
	descriptor := message.Descriptor()
	if isFullReferenceDescriptor(descriptor) {
		return visit(message)
	}

	fields := descriptor.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		if field.Kind() != protoreflect.MessageKind {
			continue
		}

		switch {
		case field.IsMap():
			if field.MapValue().Kind() != protoreflect.MessageKind {
				continue
			}
			values := message.Get(field).Map()
			var keys []protoreflect.MapKey
			values.Range(func(key protoreflect.MapKey, _ protoreflect.Value) bool {
				keys = append(keys, key)
				return true
			})
			for _, key := range keys {
				if err := visitFullReferences(values.Get(key).Message(), visit); err != nil {
					return err
				}
			}
		case field.IsList():
			values := message.Get(field).List()
			for i := 0; i < values.Len(); i++ {
				if err := visitFullReferences(values.Get(i).Message(), visit); err != nil {
					return err
				}
			}
		case message.Has(field):
			if err := visitFullReferences(message.Get(field).Message(), visit); err != nil {
				return err
			}
		}
	}
	return nil
}

func isFullReferenceDescriptor(descriptor protoreflect.MessageDescriptor) bool {
	if !strings.HasSuffix(string(descriptor.Name()), "Reference") {
		return false
	}
	fields := descriptor.Fields()
	tenant := fields.ByName("tenant")
	shared := fields.ByName("shared")
	return tenant != nil && tenant.Kind() == protoreflect.StringKind &&
		shared != nil && shared.Kind() == protoreflect.BoolKind
}
