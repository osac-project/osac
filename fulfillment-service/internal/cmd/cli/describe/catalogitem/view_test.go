/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package catalogitem

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func printView(t *testing.T, v view, colored bool) string {
	t.Helper()
	var output bytes.Buffer
	if err := render(&output, v, colored); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func requireContains(t *testing.T, output string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(output, value) {
			t.Errorf("missing %q in output:\n%s", value, output)
		}
	}
}

func parameter(t *testing.T, value string) *anypb.Any {
	t.Helper()
	result, err := anypb.New(wrapperspb.String(value))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestComputeView(t *testing.T) {
	item := publicv1.ComputeInstanceCatalogItem_builder{
		Id: "item-id", Title: "PostgreSQL | VM", Description: "Database-ready VM\nChoose a larger disk.", Published: true,
		Metadata: publicv1.Metadata_builder{Name: "postgresql", Tenant: "shared"}.Build(),
		Template: publicv1.ComputeInstanceTemplateReference_builder{Name: "ocp-virt-vm", Shared: true}.Build(),
		Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
			DiskImage:    publicv1.DiskImageReferenceFieldPolicy_builder{Locked: publicv1.DiskImageReference_builder{Name: "postgresql", Shared: true}.Build()}.Build(),
			InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{Locked: publicv1.InstanceTypeReference_builder{Id: "type-id", Project: "models"}.Build()}.Build(),
			BootDisk: publicv1.ComputeInstanceBootDiskFieldPolicies_builder{
				SizeGib:     publicv1.Int32FieldPolicy_builder{Editable: publicv1.EditableInt32Field_builder{DefaultValue: new(int32(80))}.Build()}.Build(),
				StorageTier: publicv1.StorageTierReferenceFieldPolicy_builder{Locked: publicv1.StorageTierReference_builder{Name: "local"}.Build()}.Build(),
			}.Build(),
			SshKey:                   publicv1.SecretReferenceFieldPolicy_builder{Editable: publicv1.EditableSecretReferenceField_builder{}.Build()}.Build(),
			UserData:                 publicv1.StringFieldPolicy_builder{Locked: new("#cloud-config\nusers:\n  - name: dev")}.Build(),
			RunStrategy:              publicv1.ComputeInstanceRunStrategyFieldPolicy_builder{Editable: publicv1.EditableComputeInstanceRunStrategyField_builder{DefaultValue: new(publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED)}.Build()}.Build(),
			AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{Locked: new(false)}.Build(),
			AdditionalDisks:          publicv1.ComputeInstanceDiskListFieldPolicy_builder{Locked: publicv1.ComputeInstanceDiskList_builder{}.Build()}.Build(),
			NetworkAttachments: publicv1.ComputeNetworkAttachmentListFieldPolicy_builder{Editable: publicv1.EditableComputeNetworkAttachmentList_builder{
				DefaultValue: publicv1.ComputeNetworkAttachmentList_builder{Items: []*publicv1.ComputeNetworkAttachment{
					publicv1.ComputeNetworkAttachment_builder{Subnet: publicv1.SubnetLocalReference_builder{Name: "private"}.Build()}.Build(),
				}}.Build(),
			}.Build()}.Build(),
		}.Build(),
		TemplateParameters: map[string]*publicv1.TemplateParameterPolicy{
			"z_optional":    publicv1.TemplateParameterPolicy_builder{Editable: publicv1.EditableTemplateParameter_builder{}.Build()}.Build(),
			"exposed_ports": publicv1.TemplateParameterPolicy_builder{Locked: parameter(t, "22/tcp,5432/tcp")}.Build(),
		},
	}.Build()
	output := printView(t, computeView(item), false)
	requireContains(t, output, "PostgreSQL | VM", "Database-ready VM Choose a larger disk.", "Scope:      Shared", "Published:  Yes",
		"ocp-virt-vm (shared)", "postgresql (shared)", "type-id (project: models)", "default: 80 GiB", "LOCKED    local",
		"LOCKED    #cloud-config (3 lines; see get -o yaml)", "default: HALTED", "LOCKED    false", "LOCKED    (empty)",
		"EDITABLE  default: 1 item", "Subnet: private", `"22/tcp,5432/tcp" (string)`)
	if strings.Contains(output, "users:") {
		t.Fatal("cloud-init body should not be printed")
	}
	if !regexp.MustCompile(`SSH key\s+EDITABLE\n`).MatchString(output) {
		t.Fatalf("editable field without default should have an empty value column:\n%s", output)
	}
	if strings.Index(output, "exposed_ports") > strings.Index(output, "z_optional") {
		t.Fatal("template parameters should be sorted")
	}
	requireContains(t, output, "Full catalog item definition: osac get computeinstancecatalogitem postgresql --tenant shared -o yaml")
}

func TestClusterView(t *testing.T) {
	item := publicv1.ClusterCatalogItem_builder{
		Id: "cluster-id", Metadata: publicv1.Metadata_builder{Name: "cluster-item", Tenant: "org", Project: "platform"}.Build(),
		Template: publicv1.ClusterTemplateReference_builder{Id: "template-id", Shared: true, Project: "infra"}.Build(),
		Fields: publicv1.ClusterCatalogItemFields_builder{
			Version:          publicv1.ClusterVersionReferenceFieldPolicy_builder{Editable: publicv1.EditableClusterVersionReferenceField_builder{DefaultValue: publicv1.ClusterVersionReference_builder{Name: "4-20", Shared: true}.Build()}.Build()}.Build(),
			SshPublicKey:     publicv1.StringFieldPolicy_builder{Editable: publicv1.EditableStringField_builder{}.Build()}.Build(),
			PullSecretSecret: publicv1.SecretReferenceFieldPolicy_builder{Locked: publicv1.SecretLocalReference_builder{Name: "pull"}.Build()}.Build(),
			Network: publicv1.ClusterNetworkFieldPolicies_builder{
				PodCidr:     publicv1.StringFieldPolicy_builder{Locked: new("10.0.0.0/16")}.Build(),
				ServiceCidr: publicv1.StringFieldPolicy_builder{Editable: publicv1.EditableStringField_builder{DefaultValue: new("172.30.0.0/16")}.Build()}.Build(),
			}.Build(),
			NodeSets: publicv1.ClusterNodeSetMapPolicy_builder{Locked: publicv1.ClusterNodeSetMap_builder{Items: map[string]*publicv1.ClusterTemplateNodeSet{
				"workers": publicv1.ClusterTemplateNodeSet_builder{Size: 3, HostType: publicv1.HostTypeReference_builder{Name: "compute", Shared: true}.Build()}.Build(),
			}}.Build()}.Build(),
			AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{Editable: publicv1.EditableBoolField_builder{DefaultValue: new(false)}.Build()}.Build(),
			NetworkAttachment:        publicv1.ClusterNetworkAttachmentFieldPolicy_builder{Locked: publicv1.ClusterNetworkAttachment_builder{Subnet: publicv1.SubnetLocalReference_builder{Name: "net"}.Build()}.Build()}.Build(),
		}.Build(),
	}.Build()
	output := printView(t, clusterView(item), false)
	requireContains(t, output, "Scope:      Tenant (project: platform)", "template-id (shared, project: infra)", "default: 4-20 (shared)",
		"LOCKED    pull", `"10.0.0.0/16"`, `default: "172.30.0.0/16"`, "workers: 3 nodes; host type: compute (shared)",
		"default: false", "subnet: net", "Published:  No",
		"Full catalog item definition: osac get clustercatalogitem cluster-item -o yaml")
	if strings.Contains(output, "--tenant shared") {
		t.Fatal("tenant catalog item should use the current tenant")
	}
}

func TestBareMetalView(t *testing.T) {
	item := publicv1.BareMetalInstanceCatalogItem_builder{
		Id: "bm-id", Metadata: publicv1.Metadata_builder{Name: "bm", Tenant: "org"}.Build(),
		Template: publicv1.BareMetalInstanceTemplateReference_builder{Name: "baremetal"}.Build(),
		Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
			SshPublicKey: publicv1.StringFieldPolicy_builder{Locked: new("")}.Build(),
			UserData:     publicv1.StringFieldPolicy_builder{Editable: publicv1.EditableStringField_builder{DefaultValue: new("#cloud-config\nfoo: bar")}.Build()}.Build(),
			RunStrategy:  publicv1.BareMetalInstanceRunStrategyFieldPolicy_builder{Locked: new(publicv1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED)}.Build(),
			NetworkAttachments: publicv1.BareMetalNetworkAttachmentListFieldPolicy_builder{Locked: publicv1.BareMetalNetworkAttachmentList_builder{Items: []*publicv1.BareMetalNetworkAttachment{
				publicv1.BareMetalNetworkAttachment_builder{Subnet: publicv1.SubnetLocalReference_builder{Name: "bm-subnet"}.Build(), Interface: new("eno1"), Primary: new(false)}.Build(),
			}}.Build()}.Build(),
			AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{Editable: publicv1.EditableBoolField_builder{}.Build()}.Build(),
			InstanceType:             publicv1.BareMetalInstanceTypeReferenceFieldPolicy_builder{Locked: publicv1.BareMetalInstanceTypeReference_builder{Id: "bm-type-id", Shared: true}.Build()}.Build(),
			DiskImage:                publicv1.DiskImageReferenceFieldPolicy_builder{Editable: publicv1.EditableDiskImageReferenceField_builder{DefaultValue: publicv1.DiskImageReference_builder{Name: "rhel", Shared: true}.Build()}.Build()}.Build(),
		}.Build(),
	}.Build()
	output := printView(t, bareMetalView(item), false)
	requireContains(t, output, "LOCKED    \"\"", "default: #cloud-config (2 lines; see get -o yaml)", "LOCKED    HALTED", "bm-subnet; interface: eno1; primary: false", "bm-type-id (shared)", "default: rhel (shared)",
		"Full catalog item definition: osac get baremetalinstancecatalogitem bm -o yaml")
	if strings.Contains(output, "--tenant shared") {
		t.Fatal("tenant catalog item should use the current tenant")
	}
}

func TestRenderingSafetyAndColor(t *testing.T) {
	v := view{name: "a\x1b[31m\tname", id: "id", title: "title\x1b[31m", description: "hello\rworld", kind: "clustercatalogitem",
		fields: []row{{label: "Disk image", state: "LOCKED", value: "image"}, {label: "SSH public key", state: "EDITABLE"}}}
	plain := printView(t, v, false)
	if strings.Contains(plain, "\x1b") || strings.Contains(plain, "\t") || strings.Contains(plain, "\r") {
		t.Fatalf("untrusted text reached output as terminal controls: %q", plain)
	}
	colored := printView(t, v, true)
	if !strings.Contains(colored, "\x1b[") {
		t.Fatal("expected ANSI styles")
	}
	withoutANSI := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(colored, "")
	if withoutANSI != plain {
		t.Fatalf("color changed layout:\nplain: %q\ncolored: %q", plain, withoutANSI)
	}
}

func TestMarkdownDescription(t *testing.T) {
	v := view{title: "Offering", description: "An **important** offering with `code`.\n\n- First\n- Second", kind: "clustercatalogitem"}
	plain := printView(t, v, false)
	if strings.Contains(plain, "**important**") || strings.Contains(plain, "- First") {
		t.Fatalf("description contains raw Markdown: %q", plain)
	}
	requireContains(t, plain, "important", "code", "First", "Second")
	if strings.Contains(plain, "\x1b[") {
		t.Fatalf("plain description contains ANSI escapes: %q", plain)
	}
	colored := printView(t, v, true)
	if !strings.Contains(colored, "\x1b[") {
		t.Fatalf("colored description has no styles: %q", colored)
	}
}

func TestLongRowValues(t *testing.T) {
	short := strings.Repeat("界", 50) // 100 display cells.
	long := short + "界"
	v := view{kind: "computeinstancecatalogitem", fields: []row{
		{label: "Small value", state: "LOCKED", value: short, details: []string{short}},
		{label: "Large reference", state: "LOCKED", value: long, details: []string{long}},
	}}
	output := printView(t, v, false)
	if strings.Count(output, short) != 3 || strings.Count(output, long) != 1 {
		t.Fatalf("locked values and short details should remain visible: %q", output)
	}
	if strings.Count(output, "(long value; see get -o yaml)") != 1 {
		t.Fatalf("only long structured details should be summarized: %q", output)
	}
}

func TestLongClusterAttachment(t *testing.T) {
	attachment := publicv1.ClusterNetworkAttachment_builder{
		Subnet: publicv1.SubnetLocalReference_builder{Name: "private"}.Build(),
		SecurityGroups: []*publicv1.SecurityGroupLocalReference{
			publicv1.SecurityGroupLocalReference_builder{Name: strings.Repeat("group", 21)}.Build(),
		},
	}.Build()
	if got := clusterAttachment(attachment); got != "(long value; see get -o yaml)" {
		t.Fatalf("long attachment = %q", got)
	}
}

func TestParameterFormatting(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  string
	}{
		{value: "", want: `"" (string)`},
		{value: "\x1b[31m", want: `" [31m" (string)`},
	} {
		if got := formatParameter(parameter(t, tt.value)); got != tt.want {
			t.Errorf("formatParameter(%q) = %q, want %q", tt.value, got, tt.want)
		}
	}
	boolValue, err := anypb.New(wrapperspb.Bool(false))
	if err != nil {
		t.Fatal(err)
	}
	if got := formatParameter(boolValue); got != "false (boolean)" {
		t.Fatalf("explicit false parameter = %q", got)
	}
	if got := formatParameter(&anypb.Any{TypeUrl: "broken"}); !strings.Contains(got, "unavailable") {
		t.Fatalf("invalid Any = %q", got)
	}
	for _, tc := range []struct {
		message proto.Message
		want    string
	}{
		{wrapperspb.Int32(0), "0 (int32)"},
		{wrapperspb.Int64(42), "42 (int64)"},
		{wrapperspb.Float(1.5), "1.5 (float)"},
		{wrapperspb.Double(2.5), "2.5 (double)"},
		{wrapperspb.Bytes([]byte("abc")), "(3 bytes; see get -o yaml)"},
		{timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)), "2026-01-02T03:04:05Z (timestamp)"},
		{durationpb.New(3 * time.Second), "3s (duration)"},
	} {
		encoded, err := anypb.New(tc.message)
		if err != nil {
			t.Fatal(err)
		}
		if got := formatParameter(encoded); got != tc.want {
			t.Errorf("formatParameter(%T) = %q, want %q", tc.message, got, tc.want)
		}
	}
}

func TestExplicitEmptyDefault(t *testing.T) {
	var rows []row
	addString(&rows, "SSH public key", publicv1.StringFieldPolicy_builder{
		Editable: publicv1.EditableStringField_builder{DefaultValue: new("")}.Build(),
	}.Build(), false)
	if len(rows) != 1 || rows[0].value != `default: ""` {
		t.Fatalf("explicit empty default was lost: %+v", rows)
	}
}
