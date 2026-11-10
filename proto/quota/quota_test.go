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
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// The registry is the quota source contract, so the test pins the annotations of every
// kind exactly. Adding or changing an annotation is a contract change: update this table
// together with the protos.
func TestKinds(t *testing.T) {
	kinds, err := Kinds()
	if err != nil {
		t.Fatalf("failed to load the quota annotations: %v", err)
	}

	expected := []Kind{
		{
			Name:    "BareMetalInstance",
			Message: "osac.private.v1.BareMetalInstance",
			Fields: []Field{
				{Path: "spec.auto_external_ip_attachment", Creates: &Creates{Kind: "ExternalIP"}},
				{Path: "spec.instance_type", Through: "BareMetalInstanceType"},
				{Path: "status.cluster", CreatedFor: &CreatedFor{Parent: "Cluster"}},
			},
		},
		{
			Name:      "BareMetalInstanceType",
			Message:   "osac.private.v1.BareMetalInstanceType",
			Dimension: "bare_metal.instances.{metadata.name}",
		},
		{
			Name:      "Cluster",
			Message:   "osac.private.v1.Cluster",
			Dimension: "caas.control_planes",
			Fields: []Field{
				{Path: "spec.auto_external_ip_attachment", Creates: &Creates{Kind: "ExternalIP", Count: 2}},
				{Path: "spec.node_sets", Creates: &Creates{
					Kind:       "BareMetalInstance",
					CountField: "size",
					Set:        map[string]string{"spec.instance_type": "baremetal_instance_type"},
				}},
			},
		},
		{
			Name:    "ComputeInstance",
			Message: "osac.private.v1.ComputeInstance",
			Fields: []Field{
				{Path: "spec.additional_disks", Creates: &Creates{
					Kind: "Volume",
					Each: "items",
					Set: map[string]string{
						"spec.size_gib":     "size_gib",
						"spec.storage_tier": "storage_tier.name",
					},
				}},
				{Path: "spec.auto_external_ip_attachment", Creates: &Creates{Kind: "ExternalIP"}},
				{Path: "spec.boot_disk", Creates: &Creates{
					Kind: "Volume",
					Set: map[string]string{
						"spec.size_gib":     "size_gib",
						"spec.storage_tier": "storage_tier.name",
					},
				}},
				{Path: "spec.instance_type", Through: "InstanceType", LargerOf: "status.instance_type"},
			},
		},
		{
			Name:      "DiskImage",
			Message:   "osac.private.v1.DiskImage",
			Dimension: "images.count",
		},
		{
			Name:      "ExternalIP",
			Message:   "osac.private.v1.ExternalIP",
			Dimension: "network.external_ips",
			CreatedFor: &CreatedFor{
				IDLabel:   "osac.openshift.io/auto-created-for",
				KindLabel: "osac.openshift.io/auto-created-kind",
			},
		},
		{
			Name:    "InstanceType",
			Message: "osac.private.v1.InstanceType",
			Fields: []Field{
				{Path: "spec.gpu.count", Dimension: "compute.gpu.{spec.gpu.resource_name}"},
				{Path: "spec.memory_gib", Dimension: "compute.memory_gib"},
				{Path: "spec.vcpus", Dimension: "compute.vcpus"},
			},
		},
		{
			Name:      "NATGateway",
			Message:   "osac.private.v1.NATGateway",
			Dimension: "network.nat_gateways",
		},
		{
			Name:      "VirtualNetwork",
			Message:   "osac.private.v1.VirtualNetwork",
			Dimension: "network.virtual_networks",
		},
		{
			Name:      "Volume",
			Message:   "osac.private.v1.Volume",
			Dimension: "storage.volumes",
			CreatedFor: &CreatedFor{
				Parent:     "ComputeInstance",
				NamePrefix: "ci-{parent}-",
			},
			Fields: []Field{
				{Path: "spec.size_gib", Dimension: "storage.{spec.storage_tier}.gib"},
			},
		},
	}

	if !reflect.DeepEqual(kinds, expected) {
		t.Fatalf("the quota annotations don't match the expected contract:\nexpected: %#v\nactual:   %#v", expected, kinds)
	}
}

// TestKindsRejectsUnlistedKind checks that a kind with no annotations fails validation
// unless it is on the list of kinds that use nothing. A silently unpriced kind would let
// tenants hold it outside every limit.
func TestKindsRejectsUnlistedKind(t *testing.T) {
	registryOf := func(annotate bool) error {
		options := &descriptorpb.FieldOptions{}
		if annotate {
			proto.SetExtension(options, privatev1.E_Quota, &privatev1.QuotaFieldOptions{
				Dimension: "test.count",
			})
		}
		file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
			Name:    proto.String("osac/private/v1/unlisted_registry_test.proto"),
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
						fieldDesc("name", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING, "", nil),
					},
				},
				{
					Name: proto.String("RatedResourceSpec"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("count", 1, descriptorpb.FieldDescriptorProto_TYPE_INT32, "", options),
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
		_, _, err = kindsOf(files)
		return err
	}

	err := registryOf(false)
	if err == nil {
		t.Fatal("a kind with no annotations must fail validation")
	}
	if !strings.Contains(err.Error(), "RatedResource") {
		t.Fatalf("the error must name the kind, got: %v", err)
	}

	if err := registryOf(true); err != nil {
		t.Fatalf("an annotated kind must pass validation: %v", err)
	}
}

// fakeTypes resolves types by kind and name, standing in for the database lookup
// enforcement will wire.
type fakeTypes map[string]protoreflect.Message

func (f fakeTypes) Type(kind, name string) protoreflect.Message {
	return f[kind+"/"+name]
}

// instanceTypes builds a lookup of instance types by name.
func instanceTypes(types ...*privatev1.InstanceType) fakeTypes {
	lookup := fakeTypes{}
	for _, typed := range types {
		lookup["InstanceType/"+typed.Metadata.Name] = typed.ProtoReflect()
	}
	return lookup
}

// assertDimensions fails the test unless the quote's dimensions match exactly.
func assertDimensions(t *testing.T, quote Quote, expected map[string]int64) {
	t.Helper()
	if !reflect.DeepEqual(quote.Dimensions, expected) {
		t.Fatalf("the price doesn't match:\nexpected: %#v\nactual:   %#v", expected, quote.Dimensions)
	}
}

// plannedOf returns the planned creates of one kind.
func plannedOf(quote Quote, kind string) []PlannedCreate {
	var planned []PlannedCreate
	for _, create := range quote.Creates {
		if create.Kind == kind {
			planned = append(planned, create)
		}
	}
	return planned
}

func instanceType(name string, vcpus, memoryGib int32, gpu *privatev1.GpuSpec) *privatev1.InstanceType {
	return &privatev1.InstanceType{
		Metadata: &privatev1.Metadata{Name: name},
		Spec:     &privatev1.InstanceTypeSpec{Vcpus: vcpus, MemoryGib: memoryGib, Gpu: gpu},
	}
}

func disk(sizeGib int32, tier string) *privatev1.ComputeInstanceDisk {
	return &privatev1.ComputeInstanceDisk{
		SizeGib:     proto.Int32(sizeGib),
		StorageTier: &privatev1.StorageTierReference{Name: tier},
	}
}

// TestPriceComputeInstance prices a VM with an instance type, a boot disk, additional
// disks, and an automatic external IP: what the type says, plus a volume per disk, plus
// the address.
func TestPriceComputeInstance(t *testing.T) {
	types := instanceTypes(instanceType("gpu-2", 8, 32,
		&privatev1.GpuSpec{ResourceName: "nvidia.com/A100", Count: 2},
	))
	instance := &privatev1.ComputeInstance{
		Spec: &privatev1.ComputeInstanceSpec{
			BootDisk:                 disk(100, "fast"),
			AdditionalDisks:          &privatev1.ComputeInstanceDiskList{Items: []*privatev1.ComputeInstanceDisk{disk(50, "fast"), disk(200, "archive")}},
			InstanceType:             &privatev1.InstanceTypeReference{Name: "gpu-2"},
			AutoExternalIpAttachment: proto.Bool(true),
		},
	}

	quote, err := Price(instance.ProtoReflect(), types)
	if err != nil {
		t.Fatalf("failed to price the compute instance: %v", err)
	}
	assertDimensions(t, quote, map[string]int64{
		"compute.vcpus":               8,
		"compute.memory_gib":          32,
		"compute.gpu.nvidia.com/A100": 2,
		"storage.volumes":             3,
		"storage.fast.gib":            150,
		"storage.archive.gib":         200,
		"network.external_ips":        1,
	})

	volumes := plannedOf(quote, "Volume")
	if len(volumes) != 3 {
		t.Fatalf("expected 3 planned volumes, got %d", len(volumes))
	}
	sizes := map[int64]bool{}
	for _, volume := range volumes {
		sizes[volume.Fields["spec.size_gib"].Int()] = true
		if volume.Fields["spec.storage_tier"].String() == "" {
			t.Fatalf("planned volume %v has no storage tier", volume.Fields)
		}
	}
	if !sizes[100] || !sizes[50] || !sizes[200] {
		t.Fatalf("the planned volumes carry the wrong sizes: %v", sizes)
	}
	if len(plannedOf(quote, "ExternalIP")) != 1 {
		t.Fatalf("expected 1 planned external IP, got %d", len(plannedOf(quote, "ExternalIP")))
	}
}

// TestPriceCluster prices a cluster: one control plane, as many hosts of the node set's
// host type as its size, and two automatic external IP addresses. The hosts count in the
// hosts' own usage query, and the cluster's query adds only what the cluster asks for
// beyond them.
func TestPriceCluster(t *testing.T) {
	types := fakeTypes{
		"BareMetalInstanceType/metal-1tb": (&privatev1.BareMetalInstanceType{
			Metadata: &privatev1.Metadata{Name: "metal-1tb"},
		}).ProtoReflect(),
	}
	cluster := &privatev1.Cluster{
		Metadata: &privatev1.Metadata{Name: "prod"},
		Spec: &privatev1.ClusterSpec{
			NodeSets: map[string]*privatev1.ClusterNodeSet{
				"compute": {
					Size:                  proto.Int32(3),
					BaremetalInstanceType: &privatev1.BareMetalInstanceTypeReference{Name: "metal-1tb"},
				},
			},
			AutoExternalIpAttachment: proto.Bool(true),
		},
	}

	quote, err := Price(cluster.ProtoReflect(), types)
	if err != nil {
		t.Fatalf("failed to price the cluster: %v", err)
	}
	assertDimensions(t, quote, map[string]int64{
		"caas.control_planes":            1,
		"bare_metal.instances.metal-1tb": 3,
		"network.external_ips":           2,
	})
	if len(plannedOf(quote, "BareMetalInstance")) != 3 {
		t.Fatalf("expected 3 planned hosts, got %d", len(plannedOf(quote, "BareMetalInstance")))
	}
	if len(plannedOf(quote, "ExternalIP")) != 2 {
		t.Fatalf("expected 2 planned external IPs, got %d", len(plannedOf(quote, "ExternalIP")))
	}
}

// TestPriceBareMetalInstance prices a standalone host: one of its host type's dimension,
// plus an automatic external IP address when asked for one. A cluster's hosts ask for no
// address of their own.
func TestPriceBareMetalInstance(t *testing.T) {
	types := fakeTypes{
		"BareMetalInstanceType/metal-1tb": (&privatev1.BareMetalInstanceType{
			Metadata: &privatev1.Metadata{Name: "metal-1tb"},
		}).ProtoReflect(),
	}
	instance := &privatev1.BareMetalInstance{
		Spec: &privatev1.BareMetalInstanceSpec{
			InstanceType:             &privatev1.BareMetalInstanceTypeReference{Name: "metal-1tb"},
			AutoExternalIpAttachment: proto.Bool(true),
		},
	}

	quote, err := Price(instance.ProtoReflect(), types)
	if err != nil {
		t.Fatalf("failed to price the bare metal instance: %v", err)
	}
	assertDimensions(t, quote, map[string]int64{
		"bare_metal.instances.metal-1tb": 1,
		"network.external_ips":           1,
	})

	withoutAddress := &privatev1.BareMetalInstance{
		Spec: &privatev1.BareMetalInstanceSpec{
			InstanceType: &privatev1.BareMetalInstanceTypeReference{Name: "metal-1tb"},
		},
	}
	quote, err = Price(withoutAddress.ProtoReflect(), types)
	if err != nil {
		t.Fatalf("failed to price the bare metal instance: %v", err)
	}
	assertDimensions(t, quote, map[string]int64{"bare_metal.instances.metal-1tb": 1})
}

// TestPriceVolume prices a volume: one volume, and its size on its storage tier's
// dimension.
func TestPriceVolume(t *testing.T) {
	volume := &privatev1.Volume{
		Spec: &privatev1.VolumeSpec{StorageTier: "fast", SizeGib: 500},
	}

	quote, err := Price(volume.ProtoReflect(), fakeTypes{})
	if err != nil {
		t.Fatalf("failed to price the volume: %v", err)
	}
	assertDimensions(t, quote, map[string]int64{
		"storage.volumes":  1,
		"storage.fast.gib": 500,
	})
}

// TestPriceSimpleKinds prices the kinds that count 1 per resource.
func TestPriceSimpleKinds(t *testing.T) {
	cases := []struct {
		name     string
		message  protoreflect.Message
		expected map[string]int64
	}{
		{
			name:     "external IP",
			message:  (&privatev1.ExternalIP{}).ProtoReflect(),
			expected: map[string]int64{"network.external_ips": 1},
		},
		{
			name:     "NAT gateway",
			message:  (&privatev1.NATGateway{}).ProtoReflect(),
			expected: map[string]int64{"network.nat_gateways": 1},
		},
		{
			name:     "virtual network",
			message:  (&privatev1.VirtualNetwork{}).ProtoReflect(),
			expected: map[string]int64{"network.virtual_networks": 1},
		},
		{
			name:     "image",
			message:  (&privatev1.DiskImage{}).ProtoReflect(),
			expected: map[string]int64{"images.count": 1},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			quote, err := Price(testCase.message, fakeTypes{})
			if err != nil {
				t.Fatalf("failed to price the %s: %v", testCase.name, err)
			}
			assertDimensions(t, quote, testCase.expected)
			if len(quote.Creates) != 0 {
				t.Fatalf("the %s creates nothing, got %d planned creates", testCase.name, len(quote.Creates))
			}
		})
	}
}

// TestPriceKindThatUsesNothing checks that a kind on the list of kinds that use nothing
// prices nothing: its creates aren't checked, so an empty price must pass, not fail.
func TestPriceKindThatUsesNothing(t *testing.T) {
	quote, err := Price((&privatev1.Subnet{}).ProtoReflect(), fakeTypes{})
	if err != nil {
		t.Fatalf("failed to price the subnet: %v", err)
	}
	if len(quote.Dimensions) != 0 || len(quote.Creates) != 0 {
		t.Fatalf("a subnet uses nothing, got %#v", quote)
	}
}

// TestPriceLargerOf walks the life of a resize. Before OSAC acts, only the asked type
// counts. While the resize is in progress, the VM keeps running the old type, so the
// larger amount on each dimension counts. After the restart, both say the new type. A
// resize up counts the new type at once, because the spec is the larger.
func TestPriceLargerOf(t *testing.T) {
	small := instanceType("small", 2, 4, nil)
	large := instanceType("large", 6, 16, nil)
	types := instanceTypes(small, large)

	instance := func(asked, running string) *privatev1.ComputeInstance {
		instance := &privatev1.ComputeInstance{
			Spec: &privatev1.ComputeInstanceSpec{
				InstanceType: &privatev1.InstanceTypeReference{Name: asked},
			},
		}
		if running != "" {
			instance.Status = &privatev1.ComputeInstanceStatus{
				InstanceType: &privatev1.InstanceTypeReference{Name: running},
			}
		}
		return instance
	}

	cases := []struct {
		name     string
		asked    string
		running  string
		expected map[string]int64
	}{
		{
			name:     "before the resize, only the asked type counts",
			asked:    "large",
			running:  "",
			expected: map[string]int64{"compute.vcpus": 6, "compute.memory_gib": 16},
		},
		{
			name:     "while the resize is in progress, the running type is larger",
			asked:    "small",
			running:  "large",
			expected: map[string]int64{"compute.vcpus": 6, "compute.memory_gib": 16},
		},
		{
			name:     "after the restart, both say the new type",
			asked:    "small",
			running:  "small",
			expected: map[string]int64{"compute.vcpus": 2, "compute.memory_gib": 4},
		},
		{
			name:     "a resize up counts the new type at once",
			asked:    "large",
			running:  "small",
			expected: map[string]int64{"compute.vcpus": 6, "compute.memory_gib": 16},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			quote, err := Price(instance(testCase.asked, testCase.running).ProtoReflect(), types)
			if err != nil {
				t.Fatalf("failed to price the compute instance: %v", err)
			}
			assertDimensions(t, quote, testCase.expected)
		})
	}
}

// TestPriceUnknownType checks that a reference the lookup can't resolve is an error: a
// price that silently skipped a resource's type would undercount it.
func TestPriceUnknownType(t *testing.T) {
	instance := &privatev1.ComputeInstance{
		Spec: &privatev1.ComputeInstanceSpec{
			InstanceType: &privatev1.InstanceTypeReference{Name: "unknown"},
		},
	}

	_, err := Price(instance.ProtoReflect(), instanceTypes())
	if err == nil {
		t.Fatal("an unknown instance type must fail the price")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("the error must name the type, got: %v", err)
	}
}

// TestLinks pins the created_for link descriptors the usage query counts by. The query
// trusts a link only when the linked resource's creator is `system`; that gate is the
// query's rule, and the descriptors here are what it consumes.
func TestLinks(t *testing.T) {
	links, err := Links()
	if err != nil {
		t.Fatalf("failed to load the quota links: %v", err)
	}

	expected := []Link{
		{
			Kind:   "BareMetalInstance",
			Parent: "Cluster",
			Field:  "status.cluster",
		},
		{
			Kind:      "ExternalIP",
			IDLabel:   "osac.openshift.io/auto-created-for",
			KindLabel: "osac.openshift.io/auto-created-kind",
		},
		{
			Kind:       "Volume",
			Parent:     "ComputeInstance",
			NamePrefix: "ci-{parent}-",
		},
	}

	if !reflect.DeepEqual(links, expected) {
		t.Fatalf("the links don't match the expected contract:\nexpected: %#v\nactual:   %#v", expected, links)
	}
}

// TestKindsRejectsInvalidAnnotations checks the validation's loud-failure branches on
// synthetic protos: a bad brace path, a through or creates kind that doesn't exist, a
// created_for with both or neither link shape, a bool creates that sets fields, a creates
// that counts fields of items that aren't messages, and creates that cycle.
func TestKindsRejectsInvalidAnnotations(t *testing.T) {
	quotaField := func(options *privatev1.QuotaFieldOptions) *descriptorpb.FieldOptions {
		fieldOptions := &descriptorpb.FieldOptions{}
		proto.SetExtension(fieldOptions, privatev1.E_Quota, options)
		return fieldOptions
	}
	withMessageOptions := func(
		messages []*descriptorpb.DescriptorProto, options *privatev1.QuotaMessageOptions,
	) []*descriptorpb.DescriptorProto {
		messageOptions := &descriptorpb.MessageOptions{}
		proto.SetExtension(messageOptions, privatev1.E_QuotaMessage, options)
		messages[0].Options = messageOptions
		return messages
	}

	cases := []struct {
		name     string
		messages []*descriptorpb.DescriptorProto
		contains string
	}{
		{
			name: "a dimension with an unclosed brace",
			messages: ratedResource("RatedResource",
				fieldDesc("count", 1, descriptorpb.FieldDescriptorProto_TYPE_INT32, "",
					quotaField(&privatev1.QuotaFieldOptions{Dimension: "test.{spec.count"}))),
			contains: "unclosed brace",
		},
		{
			name: "a through kind that doesn't exist",
			messages: append(ratedResource("RatedResource",
				fieldDesc("type", 1, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE,
					".osac.private.v1.RatedResourceType",
					quotaField(&privatev1.QuotaFieldOptions{Through: "NoSuchKind"}))),
				&descriptorpb.DescriptorProto{
					Name: proto.String("RatedResourceType"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("name", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING, "", nil),
					},
				}),
			contains: "NoSuchKind",
		},
		{
			name: "a creates kind that doesn't exist",
			messages: ratedResource("RatedResource",
				fieldDesc("flag", 1, descriptorpb.FieldDescriptorProto_TYPE_BOOL, "",
					quotaField(&privatev1.QuotaFieldOptions{
						Creates: &privatev1.QuotaCreatesOptions{Kind: "NoSuchKind"},
					}))),
			contains: "NoSuchKind",
		},
		{
			name: "a created_for that links by both name and labels",
			messages: withMessageOptions(ratedResource("RatedResource"),
				&privatev1.QuotaMessageOptions{CreatedFor: &privatev1.QuotaCreatedForOptions{
					NamePrefix: "rr-{parent}-",
					IdLabel:    "osac.test/parent-id",
				}}),
			contains: "both name and labels",
		},
		{
			name: "a created_for that links by neither name nor labels",
			messages: withMessageOptions(ratedResource("RatedResource"),
				&privatev1.QuotaMessageOptions{CreatedFor: &privatev1.QuotaCreatedForOptions{}}),
			contains: "neither by name nor by labels",
		},
		{
			name: "a bool creates that sets fields",
			messages: ratedResource("RatedResource",
				fieldDesc("flag", 1, descriptorpb.FieldDescriptorProto_TYPE_BOOL, "",
					quotaField(&privatev1.QuotaFieldOptions{
						Creates: &privatev1.QuotaCreatesOptions{
							Kind: "RatedResource",
							Set:  map[string]string{"spec.count": "count"},
						},
					}))),
			contains: "is a bool",
		},
		{
			name: "a creates that counts fields of list entries that aren't messages",
			messages: ratedResource("RatedResource",
				repeatedFieldDesc("names", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING, "",
					quotaField(&privatev1.QuotaFieldOptions{
						Creates: &privatev1.QuotaCreatesOptions{
							Kind:       "RatedResource",
							CountField: "count",
						},
					}))),
			contains: "aren't messages",
		},
		{
			name: "a creates whose each names a list of values that aren't messages",
			messages: append(ratedResource("RatedResource",
				fieldDesc("list", 1, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE,
					".osac.private.v1.RatedResourceList",
					quotaField(&privatev1.QuotaFieldOptions{
						Creates: &privatev1.QuotaCreatesOptions{
							Kind:       "RatedResource",
							Each:       "items",
							CountField: "count",
						},
					}))),
				&descriptorpb.DescriptorProto{
					Name: proto.String("RatedResourceList"),
					Field: []*descriptorpb.FieldDescriptorProto{
						repeatedFieldDesc("items", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING, "", nil),
					},
				}),
			contains: "aren't messages",
		},
		{
			name: "creates that cycle",
			messages: append(
				ratedResource("FirstResource",
					fieldDesc("makes_second", 1, descriptorpb.FieldDescriptorProto_TYPE_BOOL, "",
						quotaField(&privatev1.QuotaFieldOptions{
							Creates: &privatev1.QuotaCreatesOptions{Kind: "SecondResource", Count: 1},
						}))),
				ratedResource("SecondResource",
					fieldDesc("makes_first", 1, descriptorpb.FieldDescriptorProto_TYPE_BOOL, "",
						quotaField(&privatev1.QuotaFieldOptions{
							Creates: &privatev1.QuotaCreatesOptions{Kind: "FirstResource", Count: 1},
						})))...),
			contains: "creates a cycle",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := kindsOf(syntheticRegistry(t, testCase.messages))
			if err == nil {
				t.Fatal("the invalid annotation must fail validation")
			}
			if !strings.Contains(err.Error(), testCase.contains) {
				t.Fatalf("the error must say '%s', got: %v", testCase.contains, err)
			}
		})
	}
}

// TestPriceUnsetThrough checks that a through reference that isn't set is an error: a
// price that silently skipped it would undercount the resource.
func TestPriceUnsetThrough(t *testing.T) {
	instance := &privatev1.ComputeInstance{Spec: &privatev1.ComputeInstanceSpec{}}

	_, err := Price(instance.ProtoReflect(), instanceTypes())
	if err == nil || !strings.Contains(err.Error(), "isn't set") {
		t.Fatalf("an unset through reference must fail the price, got: %v", err)
	}
}

// TestResolveDimensionUnclosedBrace checks the price's own brace parse: an unclosed brace
// is an error, not a panic.
func TestResolveDimensionUnclosedBrace(t *testing.T) {
	_, err := resolveDimension("test.{spec.count", (&privatev1.Volume{}).ProtoReflect())
	if err == nil || !strings.Contains(err.Error(), "unclosed brace") {
		t.Fatalf("an unclosed brace must fail the dimension, got: %v", err)
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

// repeatedFieldDesc builds one repeated field of the synthetic resource messages.
func repeatedFieldDesc(
	name string, number int32, fieldType descriptorpb.FieldDescriptorProto_Type,
	typeName string, options *descriptorpb.FieldOptions,
) *descriptorpb.FieldDescriptorProto {
	field := fieldDesc(name, number, fieldType, typeName, options)
	field.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	return field
}

// ratedResource builds the object-shape messages of one synthetic resource kind, with the
// given fields on its spec.
func ratedResource(
	name string, specFields ...*descriptorpb.FieldDescriptorProto,
) []*descriptorpb.DescriptorProto {
	return []*descriptorpb.DescriptorProto{
		{
			Name: proto.String(name),
			Field: []*descriptorpb.FieldDescriptorProto{
				fieldDesc("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING, "", nil),
				fieldDesc("metadata", 2, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE,
					".osac.private.v1."+name+"Metadata", nil),
				fieldDesc("spec", 3, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE,
					".osac.private.v1."+name+"Spec", nil),
				fieldDesc("status", 4, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE,
					".osac.private.v1."+name+"Status", nil),
			},
		},
		{
			Name: proto.String(name + "Metadata"),
			Field: []*descriptorpb.FieldDescriptorProto{
				fieldDesc("name", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING, "", nil),
			},
		},
		{Name: proto.String(name + "Spec"), Field: specFields},
		{Name: proto.String(name + "Status")},
	}
}

// syntheticRegistry builds a registry of one synthetic proto file of the private package,
// so validation runs against annotations the real protos don't carry.
func syntheticRegistry(t *testing.T, messages []*descriptorpb.DescriptorProto) *protoregistry.Files {
	t.Helper()
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:        proto.String("osac/private/v1/synthetic_registry_test.proto"),
		Package:     proto.String("osac.private.v1"),
		Syntax:      proto.String("proto3"),
		MessageType: messages,
	}, nil)
	if err != nil {
		t.Fatalf("building the synthetic proto: %v", err)
	}
	files := &protoregistry.Files{}
	if err := files.RegisterFile(file); err != nil {
		t.Fatalf("registering the synthetic proto: %v", err)
	}
	return files
}
