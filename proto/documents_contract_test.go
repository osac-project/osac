/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package proto_test

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	_ "github.com/osac-project/osac/proto/gen/osac/private/v1"
	_ "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func findMessage(t *testing.T, name string) protoreflect.MessageDescriptor {
	t.Helper()
	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(name))
	if err != nil {
		t.Fatalf("failed to find %s: %v", name, err)
	}
	message, ok := descriptor.(protoreflect.MessageDescriptor)
	if !ok {
		t.Fatalf("%s is not a message", name)
	}
	return message
}

func findService(t *testing.T, name string) protoreflect.ServiceDescriptor {
	t.Helper()
	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(name))
	if err != nil {
		t.Fatalf("failed to find %s: %v", name, err)
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		t.Fatalf("%s is not a service", name)
	}
	return service
}

func TestDocumentContract(t *testing.T) {
	privateDocument := findMessage(t, "osac.private.v1.Document")
	publicDocument := findMessage(t, "osac.public.v1.Document")

	for _, document := range []protoreflect.MessageDescriptor{privateDocument, publicDocument} {
		if got := document.Fields().ByName("id"); got == nil || got.Number() != 1 {
			t.Fatalf("%s.id is not field 1", document.FullName())
		}
		if got := document.Fields().ByName("metadata"); got == nil || got.Number() != 2 {
			t.Fatalf("%s.metadata is not field 2", document.FullName())
		}
		if got := document.Fields().ByName("spec"); got == nil || got.Number() != 3 {
			t.Fatalf("%s.spec is not field 3", document.FullName())
		}
		if document.Fields().ByName("status") != nil {
			t.Fatalf("%s unexpectedly has status", document.FullName())
		}
	}

	privateSpec := findMessage(t, "osac.private.v1.DocumentSpec")
	publicSpec := findMessage(t, "osac.public.v1.DocumentSpec")
	for _, spec := range []protoreflect.MessageDescriptor{privateSpec, publicSpec} {
		if got := spec.Fields().ByName("owner"); got == nil || got.Number() != 1 {
			t.Fatalf("%s.owner is not field 1", spec.FullName())
		}
		if got := spec.Fields().ByName("fallback"); got == nil || got.Number() != 2 {
			t.Fatalf("%s.fallback is not field 2", spec.FullName())
		}
		if got := spec.Fields().ByName("variants"); got == nil || got.Number() != 3 || !got.IsMap() {
			t.Fatalf("%s.variants is not a map at field 3", spec.FullName())
		}
	}

	ownerBranches := map[string]protoreflect.FieldNumber{
		"add_on_operator":                  1,
		"bare_metal_instance":              2,
		"bare_metal_instance_catalog_item": 3,
		"bare_metal_instance_template":     4,
		"bare_metal_instance_type":         5,
		"cluster":                          6,
		"cluster_catalog_item":             7,
		"cluster_template":                 8,
		"cluster_version":                  9,
		"compute_instance":                 10,
		"compute_instance_catalog_item":    11,
		"compute_instance_template":        12,
		"disk_image":                       13,
		"external_ip":                      14,
		"external_ip_attachment":           15,
		"external_ip_pool":                 16,
		"fabric_domain":                    17,
		"hub":                              18,
		"identity_provider":                19,
		"instance_type":                    20,
		"nat_gateway":                      21,
		"network_class":                    22,
		"project":                          23,
		"project_membership":               24,
		"quota_default_limit":              25,
		"quota_limit":                      26,
		"quota_limit_request":              27,
		"quota_threshold":                  28,
		"role":                             29,
		"role_binding":                     30,
		"secret":                           31,
		"security_group":                   32,
		"service_account":                  33,
		"storage_backend":                  34,
		"storage_tier":                     35,
		"subnet":                           36,
		"tenant":                           37,
		"user":                             38,
		"virtual_network":                  39,
		"volume":                           40,
	}

	for _, ownerName := range []string{"osac.private.v1.DocumentOwner", "osac.public.v1.DocumentOwner"} {
		owner := findMessage(t, ownerName)
		oneof := owner.Oneofs().ByName("object")
		if oneof == nil {
			t.Fatalf("%s has no object oneof", owner.FullName())
		}
		if got := oneof.Fields().Len(); got != len(ownerBranches) {
			t.Fatalf("%s.object has %d branches, want %d", owner.FullName(), got, len(ownerBranches))
		}
		for name, number := range ownerBranches {
			field := oneof.Fields().ByName(protoreflect.Name(name))
			if field == nil {
				t.Fatalf("%s.object is missing branch %s", owner.FullName(), name)
			}
			if field.Number() != number {
				t.Fatalf("%s.object.%s is field %d, want %d", owner.FullName(), name, field.Number(), number)
			}
		}
	}

	for _, variantName := range []string{"osac.private.v1.DocumentVariant", "osac.public.v1.DocumentVariant"} {
		variant := findMessage(t, variantName)
		if got := variant.Fields().ByName("title"); got == nil || got.Number() != 1 || got.Kind() != protoreflect.StringKind {
			t.Fatalf("%s.title is not a string at field 1", variant.FullName())
		}
		if got := variant.Fields().ByName("content"); got == nil || got.Number() != 2 || got.Kind() != protoreflect.BytesKind {
			t.Fatalf("%s.content is not bytes at field 2", variant.FullName())
		}
	}

	privateMetadata := findMessage(t, "osac.private.v1.Metadata")
	publicMetadata := findMessage(t, "osac.public.v1.Metadata")
	for _, metadata := range []protoreflect.MessageDescriptor{privateMetadata, publicMetadata} {
		description := metadata.Fields().ByName("description")
		if description == nil || description.Number() != 10 || description.Kind() != protoreflect.StringKind {
			t.Fatalf("%s.description is not a string at field 10", metadata.FullName())
		}
		if metadata.ReservedNames().Len() != 0 {
			t.Fatalf("%s still has reserved field names", metadata.FullName())
		}
		if metadata.ReservedRanges().Len() != 0 {
			t.Fatalf("%s still has reserved field ranges", metadata.FullName())
		}
	}

	privateService := findService(t, "osac.private.v1.Documents")
	for _, method := range []protoreflect.Name{"List", "Get", "Create", "Update", "Delete", "Signal"} {
		if privateService.Methods().ByName(method) == nil {
			t.Fatalf("%s is missing %s", privateService.FullName(), method)
		}
	}

	publicService := findService(t, "osac.public.v1.Documents")
	for _, method := range []protoreflect.Name{"List", "Get"} {
		if publicService.Methods().ByName(method) == nil {
			t.Fatalf("%s is missing %s", publicService.FullName(), method)
		}
	}
	for _, method := range []protoreflect.Name{"Create", "Update", "Delete", "Signal"} {
		if publicService.Methods().ByName(method) != nil {
			t.Fatalf("%s unexpectedly exposes %s", publicService.FullName(), method)
		}
	}
}
