/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const clusterTestSSHPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8K1ZuSC7tmzxD5LJJXwkCfStVEjzXWYCFhJaLBxWAn test@example.com"

func seedSharedClusterProject() {
	GinkgoHelper()
	projectsDao, err := dao.NewGenericDAO[*privatev1.Project]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	_, err = projectsDao.Create().SetObject(privatev1.Project_builder{
		Metadata: privatev1.Metadata_builder{
			Name: "offerings.gpu", Tenant: auth.SharedTenant,
		}.Build(),
	}.Build()).Do(ctx)
	Expect(err).ToNot(HaveOccurred())
}

var _ = Describe("Cluster SSH key Secret references", func() {
	newServer := func(store vault.SecretStore, hubFetchers ...HubSecretFetcher) *PrivateClustersServer {
		builder := NewPrivateClustersServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy)
		if store != nil {
			builder.SetSecretStore(store)
		}
		if len(hubFetchers) > 0 {
			builder.SetHubSecretFetcher(hubFetchers[0])
		}
		server, err := builder.Build()
		Expect(err).ToNot(HaveOccurred())
		return server
	}
	seedSecret := func(server *PrivateClustersServer, secret *privatev1.Secret) {
		_, err := server.secretsDao.Create().SetObject(secret).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
	}
	clusterWithRef := func(ref *privatev1.SecretLocalReference) *privatev1.Cluster {
		return privatev1.Cluster_builder{
			Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
			Spec:     privatev1.ClusterSpec_builder{SshKey: ref}.Build(),
		}.Build()
	}
	currentCluster := func() *privatev1.Cluster {
		return privatev1.Cluster_builder{Spec: privatev1.ClusterSpec_builder{
			SshKey: privatev1.SecretLocalReference_builder{Id: "key-a", Name: "first"}.Build(),
		}.Build()}.Build()
	}

	It("rejects changing the immutable SSH-key Secret reference", func() {
		candidate := privatev1.Cluster_builder{Spec: privatev1.ClusterSpec_builder{
			SshKey: privatev1.SecretLocalReference_builder{Id: "key-b", Name: "second"}.Build(),
		}.Build()}.Build()

		err := validateClusterTemplateImmutability(currentCluster(), candidate,
			&fieldmaskpb.FieldMask{Paths: []string{"spec.ssh_key"}})
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("ssh_key is immutable"))
	})

	It("restores the canonical immutable reference when the same ID is supplied", func() {
		candidate := privatev1.Cluster_builder{Spec: privatev1.ClusterSpec_builder{
			SshKey: privatev1.SecretLocalReference_builder{Id: "key-a"}.Build(),
		}.Build()}.Build()

		Expect(validateClusterTemplateImmutability(currentCluster(), candidate,
			&fieldmaskpb.FieldMask{Paths: []string{"spec.ssh_key"}})).To(Succeed())
		Expect(candidate.GetSpec().GetSshKey().GetId()).To(Equal("key-a"))
		Expect(candidate.GetSpec().GetSshKey().GetName()).To(Equal("first"))
	})

	It("preserves the immutable reference when a legacy unmasked update omits it", func() {
		candidate := privatev1.Cluster_builder{Spec: privatev1.ClusterSpec_builder{}.Build()}.Build()

		Expect(validateClusterTemplateImmutability(currentCluster(), candidate, nil)).To(Succeed())
		Expect(candidate.GetSpec().GetSshKey().GetId()).To(Equal("key-a"))
		Expect(candidate.GetSpec().GetSshKey().GetName()).To(Equal("first"))
	})

	It("canonicalizes an authorized tenant Secret by name", func() {
		server := newServer(nil)
		seedSecret(server, privatev1.Secret_builder{
			Id:       "cluster-key-id",
			Metadata: privatev1.Metadata_builder{Name: "cluster-key", Tenant: testTenant}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data:     map[string][]byte{"public_key": []byte(clusterTestSSHPublicKey)},
		}.Build())
		cluster := clusterWithRef(privatev1.SecretLocalReference_builder{Name: "cluster-key"}.Build())

		Expect(server.validateSSHKey(ctx, cluster, nil)).To(Succeed())
		Expect(cluster.GetSpec().GetSshKey().GetId()).To(Equal("cluster-key-id"))
		Expect(cluster.GetSpec().GetSshKey().GetName()).To(Equal("cluster-key"))
	})

	It("rejects a Secret from another tenant", func() {
		server := newServer(nil)
		seedSecret(server, privatev1.Secret_builder{
			Id:       "shared-key-id",
			Metadata: privatev1.Metadata_builder{Name: "shared-key", Tenant: auth.SharedTenant}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data:     map[string][]byte{"public_key": []byte(clusterTestSSHPublicKey)},
		}.Build())

		err := server.validateSSHKey(ctx,
			clusterWithRef(privatev1.SecretLocalReference_builder{Id: "shared-key-id"}.Build()), nil)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("not found"))
	})

	It("rejects a Secret from another project in the same tenant", func() {
		server := newServer(nil)
		projectsDao, err := dao.NewGenericDAO[*privatev1.Project]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = projectsDao.Create().SetObject(privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{
				Name: "other-project", Tenant: testTenant,
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		seedSecret(server, privatev1.Secret_builder{
			Id: "other-project-key-id",
			Metadata: privatev1.Metadata_builder{
				Name: "other-project-key", Tenant: testTenant, Project: "other-project",
			}.Build(),
			Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data: map[string][]byte{"public_key": []byte(clusterTestSSHPublicKey)},
		}.Build())

		err = server.validateSSHKey(ctx,
			clusterWithRef(privatev1.SecretLocalReference_builder{Id: "other-project-key-id"}.Build()), nil)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("not found"))
	})

	It("rejects a missing Secret", func() {
		server := newServer(nil)

		err := server.validateSSHKey(ctx,
			clusterWithRef(privatev1.SecretLocalReference_builder{Name: "missing-key"}.Build()), nil)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("not found"))
	})

	It("allows a shared-project Secret only when inherited from its shared template", func() {
		server := newServer(nil)
		seedSharedClusterProject()
		seedSecret(server, privatev1.Secret_builder{
			Id: "inherited-key-id",
			Metadata: privatev1.Metadata_builder{
				Name: "inherited-key", Tenant: auth.SharedTenant, Project: "offerings.gpu",
			}.Build(),
			Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data: map[string][]byte{"public_key": []byte(clusterTestSSHPublicKey)},
		}.Build())
		cluster := clusterWithRef(privatev1.SecretLocalReference_builder{Name: "inherited-key"}.Build())

		inheritedScope := privatev1.Metadata_builder{
			Tenant: auth.SharedTenant, Project: "offerings.gpu",
		}.Build()
		Expect(server.validateSSHKey(ctx, cluster, inheritedScope)).To(Succeed())
		Expect(cluster.GetSpec().GetSshKey().GetId()).To(Equal("inherited-key-id"))
	})

	DescribeTable("rejects an invalid Secret",
		func(secretType privatev1.SecretType, data map[string][]byte, message string) {
			server := newServer(nil)
			seedSecret(server, privatev1.Secret_builder{
				Id:       "invalid-key-id",
				Metadata: privatev1.Metadata_builder{Name: "invalid-key", Tenant: testTenant}.Build(),
				Type:     secretType,
				Data:     data,
			}.Build())
			err := server.validateSSHKey(ctx,
				clusterWithRef(privatev1.SecretLocalReference_builder{Id: "invalid-key-id"}.Build()), nil)
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(status.Convert(err).Message()).To(ContainSubstring(message))
		},
		Entry("wrong type", privatev1.SecretType_SECRET_TYPE_VALUE,
			map[string][]byte{"value": []byte("value")}, "expected SECRET_TYPE_SSH_PUBLIC_KEY"),
		Entry("missing public_key", privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			map[string][]byte{"other": []byte("value")}, "non-empty 'public_key'"),
		Entry("empty public_key", privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			map[string][]byte{"public_key": {}}, "non-empty 'public_key'"),
		Entry("malformed public_key", privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			map[string][]byte{"public_key": []byte("not-an-ssh-key")}, "invalid OpenSSH public key"),
	)

	It("loads and validates Vault-backed key data", func() {
		store := vault.NewMockSecretStore(ctrl)
		server := newServer(store)
		seedSecret(server, privatev1.Secret_builder{
			Id:       "vault-key-id",
			Metadata: privatev1.Metadata_builder{Name: "vault-key", Tenant: testTenant}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Backend:  privatev1.SecretBackend_SECRET_BACKEND_VAULT,
		}.Build())
		store.EXPECT().Fetch(gomock.Any(), testTenant, "", "vault-key").
			Return(map[string][]byte{"public_key": []byte(clusterTestSSHPublicKey)}, nil)

		Expect(server.validateSSHKey(ctx,
			clusterWithRef(privatev1.SecretLocalReference_builder{Id: "vault-key-id"}.Build()), nil)).To(Succeed())
	})

	It("loads and validates a direct HUB-backed key", func() {
		coordinates := map[string]string{
			CoordinateHubID: "hub-a", CoordinateNamespace: "keys", CoordinateSecretName: "direct-key",
		}
		fetcher := NewMockHubSecretFetcher(ctrl)
		server := newServer(nil, fetcher)
		seedSecret(server, privatev1.Secret_builder{
			Id:          "hub-direct-key-id",
			Metadata:    privatev1.Metadata_builder{Name: "hub-direct-key", Tenant: testTenant}.Build(),
			Type:        privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Backend:     privatev1.SecretBackend_SECRET_BACKEND_HUB,
			Coordinates: coordinates,
		}.Build())
		fetcher.EXPECT().Fetch(gomock.Any(), coordinates).
			Return(map[string][]byte{"public_key": []byte(clusterTestSSHPublicKey)}, nil)
		cluster := clusterWithRef(privatev1.SecretLocalReference_builder{Name: "hub-direct-key"}.Build())

		Expect(server.validateSSHKey(ctx, cluster, nil)).To(Succeed())
		Expect(cluster.GetSpec().GetSshKey().GetId()).To(Equal("hub-direct-key-id"))
	})

	It("loads and validates an inherited HUB-backed key", func() {
		coordinates := map[string]string{
			CoordinateHubID: "hub-a", CoordinateNamespace: "keys", CoordinateSecretName: "inherited-key",
		}
		fetcher := NewMockHubSecretFetcher(ctrl)
		server := newServer(nil, fetcher)
		seedSharedClusterProject()
		seedSecret(server, privatev1.Secret_builder{
			Id: "hub-inherited-key-id",
			Metadata: privatev1.Metadata_builder{
				Name: "hub-inherited-key", Tenant: auth.SharedTenant, Project: "offerings.gpu",
			}.Build(),
			Type:        privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Backend:     privatev1.SecretBackend_SECRET_BACKEND_HUB,
			Coordinates: coordinates,
		}.Build())
		fetcher.EXPECT().Fetch(gomock.Any(), coordinates).
			Return(map[string][]byte{"public_key": []byte(clusterTestSSHPublicKey)}, nil)
		cluster := clusterWithRef(privatev1.SecretLocalReference_builder{Name: "hub-inherited-key"}.Build())
		inheritedScope := privatev1.Metadata_builder{
			Tenant: auth.SharedTenant, Project: "offerings.gpu",
		}.Build()

		Expect(server.validateSSHKey(ctx, cluster, inheritedScope)).To(Succeed())
		Expect(cluster.GetSpec().GetSshKey().GetId()).To(Equal("hub-inherited-key-id"))
	})

	It("preserves HUB status codes without exposing backend details", func() {
		coordinates := map[string]string{
			CoordinateHubID: "private-hub", CoordinateNamespace: "private-keys", CoordinateSecretName: "private-key",
		}
		fetcher := NewMockHubSecretFetcher(ctrl)
		server := newServer(nil, fetcher)
		seedSecret(server, privatev1.Secret_builder{
			Id:          "hub-error-key-id",
			Metadata:    privatev1.Metadata_builder{Name: "hub-error-key", Tenant: testTenant}.Build(),
			Type:        privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Backend:     privatev1.SecretBackend_SECRET_BACKEND_HUB,
			Coordinates: coordinates,
		}.Build())
		fetcher.EXPECT().Fetch(gomock.Any(), coordinates).
			Return(nil, status.Error(codes.Unavailable, "private-hub/private-keys/private-key is unavailable"))

		err := server.validateSSHKey(ctx,
			clusterWithRef(privatev1.SecretLocalReference_builder{Id: "hub-error-key-id"}.Build()), nil)
		Expect(status.Code(err)).To(Equal(codes.Unavailable))
		Expect(status.Convert(err).Message()).To(Equal("failed to resolve ssh_key reference"))
		Expect(status.Convert(err).Message()).ToNot(ContainSubstring("private-hub"))
	})

	It("canonicalizes a ClusterTemplate SSH-key default", func() {
		templateServer, err := NewPrivateClusterTemplatesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = templateServer.secretsDao.Create().SetObject(privatev1.Secret_builder{
			Id:       "template-key-id",
			Metadata: privatev1.Metadata_builder{Name: "template-key", Tenant: testTenant}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data:     map[string][]byte{"public_key": []byte(clusterTestSSHPublicKey)},
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		template := privatev1.ClusterTemplate_builder{
			Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
			SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
				SshKey: privatev1.SecretLocalReference_builder{Name: "template-key"}.Build(),
			}.Build(),
		}.Build()

		Expect(templateServer.validateSpecDefaultsSSHKey(ctx, template)).To(Succeed())
		Expect(template.GetSpecDefaults().GetSshKey().GetId()).To(Equal("template-key-id"))
		Expect(template.GetSpecDefaults().GetSshKey().GetName()).To(Equal("template-key"))
	})

	It("returns an actionable error when Vault data cannot be loaded", func() {
		store := vault.NewMockSecretStore(ctrl)
		server := newServer(store)
		seedSecret(server, privatev1.Secret_builder{
			Id:       "unavailable-key-id",
			Metadata: privatev1.Metadata_builder{Name: "unavailable-key", Tenant: testTenant}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Backend:  privatev1.SecretBackend_SECRET_BACKEND_VAULT,
		}.Build())
		store.EXPECT().Fetch(gomock.Any(), testTenant, "", "unavailable-key").
			Return(nil, errors.New("store unavailable"))

		err := server.validateSSHKey(ctx,
			clusterWithRef(privatev1.SecretLocalReference_builder{Id: "unavailable-key-id"}.Build()), nil)
		Expect(status.Code(err)).To(Equal(codes.Internal))
		Expect(status.Convert(err).Message()).To(ContainSubstring("failed to resolve ssh_key reference"))
	})

	It("canonicalizes a tenant Catalog Item SSH-key default", func() {
		server := newServer(nil)
		seedSecret(server, privatev1.Secret_builder{
			Id:       "catalog-key-id",
			Metadata: privatev1.Metadata_builder{Name: "catalog-key", Tenant: testTenant}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data:     map[string][]byte{"public_key": []byte(clusterTestSSHPublicKey)},
		}.Build())
		item := privatev1.ClusterCatalogItem_builder{
			Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
			Fields: privatev1.ClusterCatalogItemFields_builder{
				SshKey: privatev1.SecretReferenceFieldPolicy_builder{
					Editable: privatev1.EditableSecretReferenceField_builder{
						DefaultValue: privatev1.SecretLocalReference_builder{Name: "catalog-key"}.Build(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build()

		Expect(validateAndCanonicalizeClusterCatalogItemPolicies(ctx, item, nil, nil, server.secretsDao, nil, nil)).To(Succeed())
		ref := item.GetFields().GetSshKey().GetEditable().GetDefaultValue()
		Expect(ref.GetId()).To(Equal("catalog-key-id"))
		Expect(ref.GetName()).To(Equal("catalog-key"))
	})

	It("applies a Catalog Item SSH-key default without overriding inline compatibility", func() {
		policy := privatev1.ClusterCatalogItemFields_builder{
			SshKey: privatev1.SecretReferenceFieldPolicy_builder{
				Editable: privatev1.EditableSecretReferenceField_builder{
					DefaultValue: privatev1.SecretLocalReference_builder{Id: "catalog-key-id"}.Build(),
				}.Build(),
			}.Build(),
		}.Build()
		spec := privatev1.ClusterSpec_builder{}.Build()
		Expect(applyClusterCatalogItemPolicies(spec, policy)).To(Succeed())
		Expect(spec.GetSshKey().GetId()).To(Equal("catalog-key-id"))

		inline := clusterTestSSHPublicKey
		spec = privatev1.ClusterSpec_builder{SshPublicKey: &inline}.Build()
		Expect(applyClusterCatalogItemPolicies(spec, policy)).To(Succeed())
		Expect(spec.GetSshKey()).To(BeNil())
		Expect(spec.GetSshPublicKey()).To(Equal(inline))
	})

	It("applies a locked Catalog Item SSH-key and rejects caller key input", func() {
		policy := privatev1.ClusterCatalogItemFields_builder{
			SshKey: privatev1.SecretReferenceFieldPolicy_builder{
				Locked: privatev1.SecretLocalReference_builder{Id: "catalog-key-id"}.Build(),
			}.Build(),
		}.Build()
		spec := privatev1.ClusterSpec_builder{}.Build()
		Expect(applyClusterCatalogItemPolicies(spec, policy)).To(Succeed())
		Expect(spec.GetSshKey().GetId()).To(Equal("catalog-key-id"))

		explicit := privatev1.ClusterSpec_builder{
			SshKey: privatev1.SecretLocalReference_builder{Id: "caller-key-id"}.Build(),
		}.Build()
		Expect(applyClusterCatalogItemPolicies(explicit, policy)).To(MatchError(ContainSubstring("not editable")))

		inline := clusterTestSSHPublicKey
		explicit = privatev1.ClusterSpec_builder{SshPublicKey: &inline}.Build()
		Expect(applyClusterCatalogItemPolicies(explicit, policy)).To(MatchError(ContainSubstring("not editable")))
	})

	It("rejects competing inline and Secret Catalog policies", func() {
		inline := clusterTestSSHPublicKey
		item := privatev1.ClusterCatalogItem_builder{
			Fields: privatev1.ClusterCatalogItemFields_builder{
				SshPublicKey: privatev1.StringFieldPolicy_builder{Locked: &inline}.Build(),
				SshKey: privatev1.SecretReferenceFieldPolicy_builder{
					Locked: privatev1.SecretLocalReference_builder{Name: "catalog-key"}.Build(),
				}.Build(),
			}.Build(),
		}.Build()

		err := validateAndCanonicalizeClusterCatalogItemPolicies(ctx, item, nil, nil, nil, nil, nil)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("cannot be configured with fields.ssh_public_key"))
	})

	It("rejects a shared Catalog Item with a tenant-local SSH-key default", func() {
		item := privatev1.ClusterCatalogItem_builder{
			Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
			Fields: privatev1.ClusterCatalogItemFields_builder{
				SshKey: privatev1.SecretReferenceFieldPolicy_builder{
					Editable: privatev1.EditableSecretReferenceField_builder{
						DefaultValue: privatev1.SecretLocalReference_builder{Name: "tenant-key"}.Build(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build()

		err := validateAndCanonicalizeClusterCatalogItemPolicies(ctx, item, nil, nil, nil, nil, nil)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("shared catalog item"))
	})
})
