/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("CLI Catalog Item descriptions", Label("cli", "catalog-items"), func() {
	It("shows stored governed fields for each item type through the public API", func(ctx context.Context) {
		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
		_, stderr, code := tool.LoginCLI(ctx, homeDir, adminUsername, adminsPassword)
		Expect(code).To(Equal(0), "login failed: %s", stderr)

		template := createCatalogItemComputeInstanceTemplateFixture(ctx, nil, nil)
		compute := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
			Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: "shared"}.Build(),
			Title:    "Development VM", Published: true,
			Template: publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
			Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
				SshKey: publicv1.SecretReferenceFieldPolicy_builder{Editable: publicv1.EditableSecretReferenceField_builder{}.Build()}.Build(),
				BootDisk: publicv1.ComputeInstanceBootDiskFieldPolicies_builder{
					SizeGib: publicv1.Int32FieldPolicy_builder{Editable: publicv1.EditableInt32Field_builder{DefaultValue: new(int32(80))}.Build()}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		output, stderr, code := tool.RunCLI(ctx, homeDir, "describe", "computeinstancecatalogitem", compute.GetMetadata().GetName())
		Expect(code).To(Equal(0), "describe compute failed: %s", stderr)
		Expect(output).To(ContainSubstring("Development VM"))
		Expect(output).To(ContainSubstring("Scope:      Shared"))
		Expect(output).To(ContainSubstring("default: 80 GiB"))
		Expect(output).To(MatchRegexp(`SSH key\s+EDITABLE\n`))
		Expect(output).ToNot(ContainSubstring("\x1b["))
		output, stderr, code = tool.RunCLI(ctx, homeDir, "--color", "describe", "computeinstancecatalogitem", compute.GetId())
		Expect(code).To(Equal(0), "describe compute by ID failed: %s", stderr)
		Expect(output).To(ContainSubstring("\x1b["))

		clusterTemplate := createCatalogItemClusterTemplateFixture(ctx, nil, nil)
		cluster := createClusterCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ClusterCatalogItem_builder{
			Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
			Template: publicv1.ClusterTemplateReference_builder{Id: clusterTemplate}.Build(),
			Fields: publicv1.ClusterCatalogItemFields_builder{
				AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{Locked: new(false)}.Build(),
			}.Build(),
		}.Build())
		output, stderr, code = tool.RunCLI(ctx, homeDir, "describe", "clustercatalogitem", cluster.GetId())
		Expect(code).To(Equal(0), "describe cluster failed: %s", stderr)
		Expect(output).To(ContainSubstring("Scope:      Tenant"))
		Expect(output).To(ContainSubstring("LOCKED    false"))
		Expect(output).To(ContainSubstring("(shared)"))

		bareTemplate := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)
		bare := createBareMetalInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.BareMetalInstanceCatalogItem_builder{
			Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
			Template: publicv1.BareMetalInstanceTemplateReference_builder{Id: bareTemplate}.Build(),
			Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
				SshPublicKey: publicv1.StringFieldPolicy_builder{Editable: publicv1.EditableStringField_builder{}.Build()}.Build(),
				UserData:     publicv1.StringFieldPolicy_builder{Locked: new("#cloud-config\nusers: []")}.Build(),
			}.Build(),
		}.Build())
		output, stderr, code = tool.RunCLI(ctx, homeDir, "describe", "baremetalinstancecatalogitem", bare.GetMetadata().GetName())
		Expect(code).To(Equal(0), "describe bare metal failed: %s", stderr)
		Expect(output).To(ContainSubstring("#cloud-config (2 lines; see get -o yaml)"))
		Expect(output).ToNot(ContainSubstring("users: []"))
	})
})
