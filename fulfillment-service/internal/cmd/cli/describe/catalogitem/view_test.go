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
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func printView(v view, colored bool) string {
	GinkgoHelper()
	var output bytes.Buffer
	Expect(render(&output, v, colored)).To(Succeed())
	return output.String()
}

func requireContains(output string, values ...string) {
	GinkgoHelper()
	for _, value := range values {
		Expect(output).To(ContainSubstring(value))
	}
}

func parameter(value string) *anypb.Any {
	GinkgoHelper()
	result, err := anypb.New(wrapperspb.String(value))
	Expect(err).NotTo(HaveOccurred())
	return result
}

var _ = Describe("Catalog item views", func() {
	It("renders compute instance policies and template parameters", func() {
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
				"exposed_ports": publicv1.TemplateParameterPolicy_builder{Locked: parameter("22/tcp,5432/tcp")}.Build(),
			},
		}.Build()
		output := printView(computeView(item), false)
		requireContains(output, "PostgreSQL | VM", "Database-ready VM Choose a larger disk.", "Scope:      Shared", "Published:  Yes",
			"ocp-virt-vm (shared)", "postgresql (shared)", "type-id (project: models)", "default: 80 GiB", "LOCKED    local",
			"LOCKED    #cloud-config (3 lines; see get -o yaml)", "default: HALTED", "LOCKED    false", "LOCKED    (empty)",
			"EDITABLE  default: 1 item", "Subnet: private", `"22/tcp,5432/tcp" (string)`)
		Expect(output).NotTo(ContainSubstring("users:"))
		Expect(output).To(MatchRegexp(`SSH key\s+EDITABLE\n`))
		requireContains(output, "exposed_ports", "z_optional")
		Expect(strings.Index(output, "exposed_ports")).To(BeNumerically("<", strings.Index(output, "z_optional")))
		requireContains(output, "Full catalog item definition: osac get computeinstancecatalogitem postgresql --tenant shared -o yaml")
	})

	It("renders cluster policies", func() {
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
				NodeSets: publicv1.ClusterNodeSetMapPolicy_builder{Locked: publicv1.ClusterNodeSetMap_builder{Items: map[string]*publicv1.ClusterCatalogNodeSet{
					"workers": publicv1.ClusterCatalogNodeSet_builder{Size: 3, BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Name: "compute", Shared: true}.Build()}.Build(),
				}}.Build()}.Build(),
				AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{Editable: publicv1.EditableBoolField_builder{DefaultValue: new(false)}.Build()}.Build(),
				NetworkAttachment:        publicv1.ClusterNetworkAttachmentFieldPolicy_builder{Locked: publicv1.ClusterNetworkAttachment_builder{Subnet: publicv1.SubnetLocalReference_builder{Name: "net"}.Build()}.Build()}.Build(),
			}.Build(),
		}.Build()
		output := printView(clusterView(item), false)
		requireContains(output, "Scope:      Tenant (project: platform)", "template-id (shared, project: infra)", "default: 4-20 (shared)",
			"LOCKED    pull", `"10.0.0.0/16"`, `default: "172.30.0.0/16"`, "workers: 3 nodes; bare metal instance type: compute (shared)",
			"default: false", "subnet: net", "Published:  No",
			"Full catalog item definition: osac get clustercatalogitem cluster-item -o yaml")
		Expect(output).NotTo(ContainSubstring("--tenant shared"))
	})

	It("renders editable cluster node sets in sorted order with unresolved references", func() {
		var rows []row
		addNodeSets(&rows, publicv1.ClusterNodeSetMapPolicy_builder{
			Editable: publicv1.EditableClusterNodeSetMap_builder{
				DefaultValue: publicv1.ClusterNodeSetMap_builder{Items: map[string]*publicv1.ClusterCatalogNodeSet{
					"workers": publicv1.ClusterCatalogNodeSet_builder{
						Size: 3, BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: "type-id", Project: "infra"}.Build(),
					}.Build(),
					"accelerators": publicv1.ClusterCatalogNodeSet_builder{Size: 0}.Build(),
				}}.Build(),
			}.Build(),
		}.Build())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].state).To(Equal("EDITABLE"))
		Expect(rows[0].value).To(Equal("default: 2 items"))
		Expect(rows[0].details).To(Equal([]string{
			"accelerators: 0 nodes; bare metal instance type: -",
			"workers: 3 nodes; bare metal instance type: type-id (project: infra)",
		}))
	})

	It("renders bare metal policies", func() {
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
		output := printView(bareMetalView(item), false)
		requireContains(output, "LOCKED    \"\"", "default: #cloud-config (2 lines; see get -o yaml)", "LOCKED    HALTED", "bm-subnet; interface: eno1; primary: false", "bm-type-id (shared)", "default: rhel (shared)",
			"Full catalog item definition: osac get baremetalinstancecatalogitem bm -o yaml")
		Expect(output).NotTo(ContainSubstring("--tenant shared"))
	})

	It("sanitizes terminal controls without changing the colored layout", func() {
		v := view{name: "a\x1b[31m\tname\u202e", id: "id", title: "title\x1b[31m", description: "hello\rworld", kind: "clustercatalogitem",
			fields: []row{{label: "Disk image", state: "LOCKED", value: "image"}, {label: "SSH public key", state: "EDITABLE"}}}
		plain := printView(v, false)
		for _, control := range []string{"\x1b", "\t", "\r", "\u202e"} {
			Expect(plain).NotTo(ContainSubstring(control))
		}
		colored := printView(v, true)
		Expect(colored).To(ContainSubstring("\x1b["))
		withoutANSI := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(colored, "")
		Expect(withoutANSI).To(Equal(plain))
	})

	It("renders Markdown descriptions without raw markers in plain output", func() {
		v := view{title: "Offering", description: "An **important** offering with `code`.\n\n- First\n- Second", kind: "clustercatalogitem"}
		plain := printView(v, false)
		Expect(plain).NotTo(ContainSubstring("**important**"))
		Expect(plain).NotTo(ContainSubstring("- First"))
		requireContains(plain, "important", "code", "First", "Second")
		Expect(plain).NotTo(ContainSubstring("\x1b["))
		Expect(printView(v, true)).To(ContainSubstring("\x1b["))
	})

	It("summarizes long structured row values", func() {
		short := strings.Repeat("界", 50) // 100 display cells.
		long := short + "界"
		v := view{kind: "computeinstancecatalogitem", fields: []row{
			{label: "Small value", state: "LOCKED", value: short, details: []string{short}},
			{label: "Large reference", state: "LOCKED", value: long, details: []string{long}},
		}}
		output := printView(v, false)
		Expect(strings.Count(output, short)).To(Equal(3))
		Expect(strings.Count(output, long)).To(Equal(1))
		Expect(strings.Count(output, "(long value; see get -o yaml)")).To(Equal(1))
	})

	It("summarizes a long cluster attachment", func() {
		attachment := publicv1.ClusterNetworkAttachment_builder{
			Subnet: publicv1.SubnetLocalReference_builder{Name: "private"}.Build(),
			SecurityGroups: []*publicv1.SecurityGroupLocalReference{
				publicv1.SecurityGroupLocalReference_builder{Name: strings.Repeat("group", 21)}.Build(),
			},
		}.Build()
		Expect(clusterAttachment(attachment)).To(Equal("(long value; see get -o yaml)"))
	})

	Describe("template parameter formatting", func() {
		DescribeTable("supported values",
			func(value proto.Message, want string) {
				encoded, err := anypb.New(value)
				Expect(err).NotTo(HaveOccurred())
				Expect(formatParameter(encoded)).To(Equal(want))
			},
			Entry("empty string", wrapperspb.String(""), `"" (string)`),
			Entry("sanitized string", wrapperspb.String("\x1b[31m"), `" [31m" (string)`),
			Entry("explicit false", wrapperspb.Bool(false), "false (boolean)"),
			Entry("int32 zero", wrapperspb.Int32(0), "0 (int32)"),
			Entry("int64", wrapperspb.Int64(42), "42 (int64)"),
			Entry("float", wrapperspb.Float(1.5), "1.5 (float)"),
			Entry("double", wrapperspb.Double(2.5), "2.5 (double)"),
			Entry("bytes", wrapperspb.Bytes([]byte("abc")), "(3 bytes; see get -o yaml)"),
			Entry("timestamp", timestamppb.New(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)), "2026-01-02T03:04:05Z (timestamp)"),
			Entry("duration", durationpb.New(3*time.Second), "3s (duration)"),
		)

		It("reports an invalid Any", func() {
			Expect(formatParameter(&anypb.Any{TypeUrl: "broken"})).To(ContainSubstring("unavailable"))
		})
	})

	It("preserves an explicitly empty string default", func() {
		var rows []row
		addString(&rows, "SSH public key", publicv1.StringFieldPolicy_builder{
			Editable: publicv1.EditableStringField_builder{DefaultValue: new("")}.Build(),
		}.Build(), false)
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].value).To(Equal(`default: ""`))
	})
})
