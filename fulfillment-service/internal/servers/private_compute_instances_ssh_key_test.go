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
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/vault"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("ComputeInstance SSH key reference", func() {
	current := func() *privatev1.ComputeInstance {
		return privatev1.ComputeInstance_builder{
			Spec: privatev1.ComputeInstanceSpec_builder{
				SshKey: privatev1.SecretLocalReference_builder{Id: "key-a", Name: "first"}.Build(),
			}.Build(),
		}.Build()
	}

	It("rejects changing an existing reference", func() {
		candidate := privatev1.ComputeInstance_builder{
			Spec: privatev1.ComputeInstanceSpec_builder{
				SshKey: privatev1.SecretLocalReference_builder{Id: "key-b", Name: "second"}.Build(),
			}.Build(),
		}.Build()

		err := validateComputeInstanceImmutability(current(), candidate, &fieldmaskpb.FieldMask{Paths: []string{"spec.ssh_key"}})
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("ssh_key is immutable"))
	})

	It("accepts an existing reference supplied by ID and restores its canonical form", func() {
		candidate := privatev1.ComputeInstance_builder{
			Spec: privatev1.ComputeInstanceSpec_builder{
				SshKey: privatev1.SecretLocalReference_builder{Id: "key-a"}.Build(),
			}.Build(),
		}.Build()

		err := validateComputeInstanceImmutability(current(), candidate, &fieldmaskpb.FieldMask{Paths: []string{"spec.ssh_key"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(candidate.GetSpec().GetSshKey().GetId()).To(Equal("key-a"))
		Expect(candidate.GetSpec().GetSshKey().GetName()).To(Equal("first"))
	})

	It("accepts an existing reference supplied by name and restores its canonical form", func() {
		candidate := privatev1.ComputeInstance_builder{
			Spec: privatev1.ComputeInstanceSpec_builder{
				SshKey: privatev1.SecretLocalReference_builder{Name: "first"}.Build(),
			}.Build(),
		}.Build()

		err := validateComputeInstanceImmutability(current(), candidate, &fieldmaskpb.FieldMask{Paths: []string{"spec.ssh_key"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(candidate.GetSpec().GetSshKey().GetId()).To(Equal("key-a"))
		Expect(candidate.GetSpec().GetSshKey().GetName()).To(Equal("first"))
	})
})

var _ = Describe("ComputeInstance SSH key validation", func() {
	It("canonicalizes a tenant-scoped Secret reference by name", func() {
		mockStore := vault.NewMockSecretStore(ctrl)
		server, err := NewPrivateComputeInstancesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetSecretStore(mockStore).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = server.secretsDao.Create().SetObject(privatev1.Secret_builder{
			Id: "compute-key",
			Metadata: privatev1.Metadata_builder{
				Name:   "login",
				Tenant: testTenant,
			}.Build(),
			Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			// This Secret already has a public_key, so validation can use it directly.
			Data: map[string][]byte{"public_key": []byte("ssh-ed25519 AAAA login")},
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		instance := privatev1.ComputeInstance_builder{
			Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
			Spec:     privatev1.ComputeInstanceSpec_builder{SshKey: privatev1.SecretLocalReference_builder{Name: "login"}.Build()}.Build(),
		}.Build()
		Expect(server.validateSshPublicKey(ctx, instance, nil)).ToNot(HaveOccurred())
		Expect(instance.GetSpec().GetSshKey().GetId()).To(Equal("compute-key"))
		Expect(instance.GetSpec().GetSshKey().GetName()).To(Equal("login"))
	})

	It("loads Vault-backed SSH key data before validating it", func() {
		mockStore := vault.NewMockSecretStore(ctrl)
		server, err := NewPrivateComputeInstancesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetSecretStore(mockStore).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = server.secretsDao.Create().SetObject(privatev1.Secret_builder{
			Id: "vault-compute-key",
			Metadata: privatev1.Metadata_builder{
				Name:   "vault-login",
				Tenant: testTenant,
			}.Build(),
			Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			// This Secret has no public_key data, so validation fetches the key from the mock Vault store.
			Backend: privatev1.SecretBackend_SECRET_BACKEND_VAULT,
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		fetch := mockStore.EXPECT().
			Fetch(gomock.Any(), testTenant, "", "vault-login")
		// The mock Vault returns a public_key, so validation succeeds.
		fetch.Return(map[string][]byte{"public_key": []byte("ssh-ed25519 AAAA vault-login")}, nil)

		instance := privatev1.ComputeInstance_builder{
			Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				SshKey: privatev1.SecretLocalReference_builder{Id: "vault-compute-key"}.Build(),
			}.Build(),
		}.Build()
		Expect(server.validateSshPublicKey(ctx, instance, nil)).ToNot(HaveOccurred())
		Expect(instance.GetSpec().GetSshKey().GetId()).To(Equal("vault-compute-key"))
	})

	It("returns Internal when Vault-backed SSH key data cannot be fetched", func() {
		mockStore := vault.NewMockSecretStore(ctrl)
		server, err := NewPrivateComputeInstancesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			SetSecretStore(mockStore).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = server.secretsDao.Create().SetObject(privatev1.Secret_builder{
			Id: "unavailable-vault-key",
			Metadata: privatev1.Metadata_builder{
				Name:   "unavailable-login",
				Tenant: testTenant,
			}.Build(),
			Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			// This Secret has no public_key data, so validation fetches the key from the mock Vault store.
			Backend: privatev1.SecretBackend_SECRET_BACKEND_VAULT,
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		fetch := mockStore.EXPECT().
			Fetch(gomock.Any(), testTenant, "", "unavailable-login")
		// The mock Vault returns an error, so validation returns Internal.
		fetch.Return(nil, errors.New("vault unavailable"))

		instance := privatev1.ComputeInstance_builder{
			Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				SshKey: privatev1.SecretLocalReference_builder{Id: "unavailable-vault-key"}.Build(),
			}.Build(),
		}.Build()
		err = server.validateSshPublicKey(ctx, instance, nil)
		Expect(status.Code(err)).To(Equal(codes.Internal))
		Expect(status.Convert(err).Message()).To(ContainSubstring("failed to resolve ssh key reference"))
	})

	It("returns Internal when Vault-backed SSH key data has no configured store", func() {
		// This server has no SecretStore, so it cannot fetch a key from Vault.
		server, err := NewPrivateComputeInstancesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = server.secretsDao.Create().SetObject(privatev1.Secret_builder{
			Id: "unconfigured-vault-key",
			Metadata: privatev1.Metadata_builder{
				Name:   "unconfigured-login",
				Tenant: testTenant,
			}.Build(),
			Type: privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			// Mark the Secret as Vault-backed.
			Backend: privatev1.SecretBackend_SECRET_BACKEND_VAULT,
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		instance := privatev1.ComputeInstance_builder{
			Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				SshKey: privatev1.SecretLocalReference_builder{Id: "unconfigured-vault-key"}.Build(),
			}.Build(),
		}.Build()
		// Validation returns Internal because no SecretStore is configured to fetch the key.
		err = server.validateSshPublicKey(ctx, instance, nil)
		Expect(status.Code(err)).To(Equal(codes.Internal))
		Expect(status.Convert(err).Message()).To(ContainSubstring("failed to resolve ssh key reference"))
	})

	It("rejects a Secret with the wrong type", func() {
		server, err := NewPrivateComputeInstancesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = server.secretsDao.Create().SetObject(privatev1.Secret_builder{
			Id:       "opaque-secret",
			Metadata: privatev1.Metadata_builder{Name: "opaque", Tenant: testTenant}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_VALUE,
			Data:     map[string][]byte{"value": []byte("not-an-ssh-key")},
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		instance := privatev1.ComputeInstance_builder{
			Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				SshKey: privatev1.SecretLocalReference_builder{Id: "opaque-secret"}.Build(),
			}.Build(),
		}.Build()
		err = server.validateSshPublicKey(ctx, instance, nil)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("expected SECRET_TYPE_SSH_PUBLIC_KEY"))
	})

	It("rejects an SSH public key Secret without public_key data", func() {
		server, err := NewPrivateComputeInstancesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = server.secretsDao.Create().SetObject(privatev1.Secret_builder{
			Id:       "missing-public-key",
			Metadata: privatev1.Metadata_builder{Name: "missing-public-key", Tenant: testTenant}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data:     map[string][]byte{"other": []byte("value")},
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		instance := privatev1.ComputeInstance_builder{
			Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				SshKey: privatev1.SecretLocalReference_builder{Id: "missing-public-key"}.Build(),
			}.Build(),
		}.Build()
		err = server.validateSshPublicKey(ctx, instance, nil)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(ContainSubstring("public_key"))
	})

	It("rejects SSH public key Secrets for Windows disk images", func() {
		server, err := NewPrivateComputeInstancesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = server.secretsDao.Create().SetObject(privatev1.Secret_builder{
			Id:       "windows-key",
			Metadata: privatev1.Metadata_builder{Name: "windows-login", Tenant: testTenant}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_SSH_PUBLIC_KEY,
			Data:     map[string][]byte{"public_key": []byte("ssh-ed25519 AAAA windows")},
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = server.diskImagesDao.Create().SetObject(privatev1.DiskImage_builder{
			Id:       "windows-image",
			Metadata: privatev1.Metadata_builder{Name: "windows-image", Tenant: testTenant}.Build(),
			Spec: privatev1.DiskImageSpec_builder{
				GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_WINDOWS,
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		instance := privatev1.ComputeInstance_builder{
			Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				SshKey:    privatev1.SecretLocalReference_builder{Id: "windows-key"}.Build(),
				DiskImage: privatev1.DiskImageReference_builder{Id: "windows-image"}.Build(),
			}.Build(),
		}.Build()
		image, _, imageErr := server.validateDiskImage(ctx, instance)
		Expect(imageErr).ToNot(HaveOccurred())
		err = server.validateSshPublicKey(ctx, instance, image)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(status.Convert(err).Message()).To(Equal("SSH key injection is not supported for Windows instances"))
	})

	It("rejects an empty reference", func() {
		server, err := NewPrivateComputeInstancesServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		instance := privatev1.ComputeInstance_builder{
			Metadata: privatev1.Metadata_builder{Tenant: testTenant}.Build(),
			Spec:     privatev1.ComputeInstanceSpec_builder{SshKey: privatev1.SecretLocalReference_builder{}.Build()}.Build(),
		}.Build()
		err = server.validateSshPublicKey(ctx, instance, nil)
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})
})
