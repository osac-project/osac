/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package metering

import (
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// The registry is the metering source contract, so the test pins the fields of every resource
// exactly. Adding or changing a tracked field is a contract change: update this table together
// with the annotations and the migration that installs the triggers.
func TestResources(t *testing.T) {
	resources, err := Resources()
	if err != nil {
		t.Fatalf("failed to load the tracked fields: %v", err)
	}

	expected := []Resource{
		{
			Message: "osac.private.v1.BareMetalInstance",
			Fields: []Field{
				{Path: "spec.catalog_item", Kind: KindConstant},
				{Path: "spec.instance_type", Kind: KindConstant},
				{Path: "status.cluster", Kind: KindFixedOnceSet, OwnerResource: "Cluster"},
				{Path: "status.node_set", Kind: KindFixedOnceSet},
				{Path: "status.state", Kind: KindVaries, TransitionTime: "status.state_transition_time"},
			},
		},
		{
			Message: "osac.private.v1.Cluster",
			Fields: []Field{
				{Path: "spec.template", Kind: KindConstant},
				{Path: "spec.version", Kind: KindVaries, TransitionTime: "status.version_transition_time"},
				{Path: "status.state", Kind: KindVaries, TransitionTime: "status.state_transition_time"},
			},
		},
		{
			Message: "osac.private.v1.ComputeInstance",
			Fields: []Field{
				{Path: "spec.boot_disk.size_gib", Kind: KindConstant},
				{Path: "spec.catalog_item", Kind: KindConstant},
				{Path: "spec.disk_image", Kind: KindConstant},
				{Path: "status.instance_type", Kind: KindVaries, TransitionTime: "status.instance_type_transition_time"},
				{Path: "status.state", Kind: KindVaries, TransitionTime: "status.state_transition_time"},
			},
		},
		{
			Message: "osac.private.v1.ExternalIP",
			Fields: []Field{
				{Path: "spec.pool", Kind: KindConstant},
				{Path: "status.address", Kind: KindFixedOnceSet},
				{Path: "status.attached", Kind: KindVaries, TransitionTime: "status.attachment_transition_time"},
				{Path: "status.attribution", Kind: KindVaries, TransitionTime: "status.attachment_transition_time"},
				{Path: "status.state", Kind: KindVaries, TransitionTime: "status.state_transition_time"},
			},
		},
		{
			Message: "osac.private.v1.NATGateway",
			Fields: []Field{
				{Path: "spec.external_ip", Kind: KindConstant},
				{Path: "spec.virtual_network", Kind: KindConstant},
				{Path: "status.state", Kind: KindVaries, TransitionTime: "status.state_transition_time"},
			},
		},
		{
			Message: "osac.private.v1.Volume",
			Fields: []Field{
				{Path: "spec.storage_tier", Kind: KindConstant},
				{Path: "status.protocol", Kind: KindConstant},
				{Path: "status.provisioned_size_gib", Kind: KindFixedOnceSet},
				{Path: "status.state", Kind: KindVaries, TransitionTime: "status.state_transition_time"},
				{Path: "status.vendor_volume_id", Kind: KindFixedOnceSet},
			},
		},
	}

	if !reflect.DeepEqual(resources, expected) {
		t.Fatalf("the tracked fields don't match the expected contract:\nexpected: %#v\nactual:   %#v", expected, resources)
	}
}

// TestResourcesRejectsUnrenderableKinds checks that an annotated field whose proto kind the
// fold cannot render fails validation at startup. A float, for example, would be tracked in
// the registry but never produce a fact, silently unbilled forever. A renderable kind on the
// same synthetic resource passes, proving the rejection is about the kind.
func TestResourcesRejectsUnrenderableKinds(t *testing.T) {
	registryOf := func(fieldType descriptorpb.FieldDescriptorProto_Type) error {
		options := &descriptorpb.FieldOptions{}
		proto.SetExtension(options, privatev1.E_Metering, &privatev1.MeteringFieldOptions{
			Kind: privatev1.MeteringFieldKind_METERING_FIELD_KIND_CONSTANT,
		})
		file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
			Name:    proto.String("osac/private/v1/renderable_registry_test.proto"),
			Package: proto.String("osac.private.v1"),
			Syntax:  proto.String("proto3"),
			MessageType: []*descriptorpb.DescriptorProto{
				{
					Name: proto.String("RatedResource"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING, "", nil),
						fieldDesc("metadata", 2, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE,
							".osac.private.v1.RatedResourceMetadata", nil),
						fieldDesc("spec", 3, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE,
							".osac.private.v1.RatedResourceSpec", nil),
						fieldDesc("status", 4, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE,
							".osac.private.v1.RatedResourceStatus", nil),
					},
				},
				{
					Name: proto.String("RatedResourceMetadata"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("tenant", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING, "", nil),
					},
				},
				{
					Name: proto.String("RatedResourceSpec"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("ratio", 1, fieldType, "", options),
					},
				},
				{Name: proto.String("RatedResourceStatus")},
			},
		}, nil)
		if err != nil {
			t.Fatalf("building the synthetic proto: %v", err)
		}
		files := &protoregistry.Files{}
		if err := files.RegisterFile(file); err != nil {
			t.Fatalf("registering the synthetic proto: %v", err)
		}
		_, err = resourcesOf(files)
		return err
	}

	err := registryOf(descriptorpb.FieldDescriptorProto_TYPE_FLOAT)
	if err == nil {
		t.Fatal("an annotated float field must fail validation")
	}
	if !strings.Contains(err.Error(), "spec.ratio") || !strings.Contains(err.Error(), "float") {
		t.Fatalf("the error must name the field and its kind, got: %v", err)
	}

	if err := registryOf(descriptorpb.FieldDescriptorProto_TYPE_STRING); err != nil {
		t.Fatalf("an annotated string field must pass validation: %v", err)
	}
}

// fieldDesc builds one field of the synthetic resource messages. Only the annotated field
// carries options.
func fieldDesc(
	name string, number int32, fieldType descriptorpb.FieldDescriptorProto_Type,
	typeName string, options *descriptorpb.FieldOptions,
) *descriptorpb.FieldDescriptorProto {
	field := &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(number),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:   fieldType.Enum(),
	}
	if typeName != "" {
		field.TypeName = proto.String(typeName)
	}
	field.Options = options
	return field
}
