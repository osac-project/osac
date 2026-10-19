/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Cluster template SSH-key Secret scope", func() {
	var server *PrivateClusterTemplatesServer

	BeforeEach(func() {
		var err error
		server, err = NewPrivateClusterTemplatesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	seedProject := func(name string) {
		projectsDao, err := dao.NewGenericDAO[*privatev1.Project]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = projectsDao.Create().SetObject(privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	}

	seedSSHKey := func(id, name, project string) {
		_, err := server.secretsDao.Create().SetObject(privatev1.Secret_builder{
			Id: id,
			Metadata: privatev1.Metadata_builder{
				Name: name, Tenant: testTenant, Project: project,
			}.Build(),
			Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	}

	It("rejects a create reference visible only in another project", func() {
		seedProject("template-project")
		seedProject("other-project")
		seedSSHKey("other-key-id", "login-key", "other-project")

		response, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "scoped-create-template", Project: "template-project",
				}.Build(),
				SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
					SshKey: privatev1.SecretLocalReference_builder{Name: "login-key"}.Build(),
				}.Build(),
			}.Build(),
		}.Build())

		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("secret 'login-key' not found"))
		Expect(response).To(BeNil())
	})

	It("rejects a masked update reference visible only in another project", func() {
		seedProject("template-project")
		seedProject("other-project")
		seedSSHKey("other-key-id", "login-key", "other-project")

		created, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "scoped-update-template", Project: "template-project",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		response, err := server.Update(ctx, privatev1.ClusterTemplatesUpdateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id: created.GetObject().GetId(),
				SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
					SshKey: privatev1.SecretLocalReference_builder{Name: "login-key"}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec_defaults.ssh_key"}},
		}.Build())

		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("secret 'login-key' not found"))
		Expect(response).To(BeNil())
	})

	It("revalidates an unchanged key when a masked update changes scope", func() {
		seedProject("template-project")
		seedProject("other-project")
		seedSSHKey("template-key-id", "login-key", "template-project")

		created, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "scope-update-template", Project: "template-project",
				}.Build(),
				SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
					SshKey: privatev1.SecretLocalReference_builder{Name: "login-key"}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		response, err := server.Update(ctx, privatev1.ClusterTemplatesUpdateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id: created.GetObject().GetId(),
				Metadata: privatev1.Metadata_builder{
					Project: "other-project",
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.project"}},
		}.Build())

		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("secret 'template-key-id' not found"))
		Expect(response).To(BeNil())
	})

	It("canonicalizes a create reference in the template project", func() {
		seedProject("template-project")
		seedProject("other-project")
		seedSSHKey("other-key-id", "login-key", "other-project")
		seedSSHKey("template-key-id", "login-key", "template-project")

		response, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "canonical-create-template", Project: "template-project",
				}.Build(),
				SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
					SshKey: privatev1.SecretLocalReference_builder{Name: "login-key"}.Build(),
				}.Build(),
			}.Build(),
		}.Build())

		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetSpecDefaults().GetSshKey().GetId()).To(Equal("template-key-id"))
		Expect(response.GetObject().GetSpecDefaults().GetSshKey().GetName()).To(Equal("login-key"))
	})

	It("canonicalizes a masked update reference in the stored template project", func() {
		seedProject("template-project")
		seedProject("other-project")
		seedSSHKey("other-key-id", "login-key", "other-project")
		seedSSHKey("template-key-id", "login-key", "template-project")

		created, err := server.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Metadata: privatev1.Metadata_builder{
					Name: "canonical-update-template", Project: "template-project",
				}.Build(),
				Title: "Preserved title",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		response, err := server.Update(ctx, privatev1.ClusterTemplatesUpdateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id: created.GetObject().GetId(),
				SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
					SshKey: privatev1.SecretLocalReference_builder{Name: "login-key"}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec_defaults.ssh_key"}},
		}.Build())

		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetSpecDefaults().GetSshKey().GetId()).To(Equal("template-key-id"))
		Expect(response.GetObject().GetSpecDefaults().GetSshKey().GetName()).To(Equal("login-key"))
		Expect(response.GetObject().GetTitle()).To(Equal("Preserved title"))
	})
})
