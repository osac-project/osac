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
	"fmt"
	"strings"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("User data secret validation", func() {
	var secretsDao *dao.GenericDAO[*privatev1.Secret]

	BeforeEach(func() {
		var err error
		secretsDao, err = dao.NewGenericDAO[*privatev1.Secret]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	createSecret := func(data map[string][]byte) *privatev1.Secret {
		name := fmt.Sprintf("userdata-%s", uuid.NewString()[:8])
		response, err := secretsDao.Create().SetObject(privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_USER_DATA,
			Data:     data,
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

	DescribeTable("compares Secret reference identities", func(existing, updated *privatev1.SecretLocalReference, same bool) {
		Expect(sameSecretReference(existing, updated)).To(Equal(same))
	},
		Entry("both omitted", nil, nil, true),
		Entry("only existing omitted", nil, privatev1.SecretLocalReference_builder{Id: "id"}.Build(), false),
		Entry("only updated omitted", privatev1.SecretLocalReference_builder{Id: "id"}.Build(), nil, false),
		Entry("same ID with name omitted", privatev1.SecretLocalReference_builder{Id: "id", Name: "name"}.Build(), privatev1.SecretLocalReference_builder{Id: "id"}.Build(), true),
		Entry("same ID with different names", privatev1.SecretLocalReference_builder{Id: "id", Name: "old"}.Build(), privatev1.SecretLocalReference_builder{Id: "id", Name: "new"}.Build(), true),
		Entry("same name without updated ID", privatev1.SecretLocalReference_builder{Id: "id", Name: "name"}.Build(), privatev1.SecretLocalReference_builder{Name: "name"}.Build(), true),
		Entry("different IDs with same name", privatev1.SecretLocalReference_builder{Id: "old", Name: "name"}.Build(), privatev1.SecretLocalReference_builder{Id: "new", Name: "name"}.Build(), false),
		Entry("different names without updated ID", privatev1.SecretLocalReference_builder{Id: "id", Name: "old"}.Build(), privatev1.SecretLocalReference_builder{Name: "new"}.Build(), false),
	)

	DescribeTable("preserves user data Secret identity in both immutability checks", func(updated *privatev1.SecretLocalReference, invalid bool) {
		existingSource := privatev1.UserDataSource_builder{
			Secret: privatev1.SecretLocalReference_builder{Id: "secret-id", Name: "secret-name"}.Build(),
		}.Build()
		var updatedSource *privatev1.UserDataSource
		if updated != nil {
			updatedSource = privatev1.UserDataSource_builder{Secret: updated}.Build()
		}
		mask := &fieldmaskpb.FieldMask{Paths: []string{"spec.user_data_source"}}
		for _, err := range []error{
			validateComputeInstanceImmutability(
				privatev1.ComputeInstance_builder{Spec: privatev1.ComputeInstanceSpec_builder{UserDataSource: existingSource}.Build()}.Build(),
				privatev1.ComputeInstance_builder{Spec: privatev1.ComputeInstanceSpec_builder{UserDataSource: updatedSource}.Build()}.Build(), mask),
			validateBareMetalUserDataImmutability(
				privatev1.BareMetalInstanceSpec_builder{UserDataSource: existingSource}.Build(),
				privatev1.BareMetalInstanceSpec_builder{UserDataSource: updatedSource}.Build(), true),
		} {
			if invalid {
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("user_data_source.secret is immutable"))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		}
	},
		Entry("accepts the same ID", privatev1.SecretLocalReference_builder{Id: "secret-id"}.Build(), false),
		Entry("accepts the same name", privatev1.SecretLocalReference_builder{Name: "secret-name"}.Build(), false),
		Entry("accepts the canonical reference", privatev1.SecretLocalReference_builder{Id: "secret-id", Name: "secret-name"}.Build(), false),
		Entry("rejects another ID with the same name", privatev1.SecretLocalReference_builder{Id: "other-id", Name: "secret-name"}.Build(), true),
		Entry("rejects another name", privatev1.SecretLocalReference_builder{Name: "other-name"}.Build(), true),
		Entry("rejects clearing the reference", nil, true),
	)

	DescribeTable("resolves user data Secret identity before checking updates", func(bareMetal, byName, replaced bool) {
		secret := createSecret(map[string][]byte{userDataSecretDataKey: []byte("#cloud-config")})
		existingID := secret.GetId()
		if replaced {
			existingID = "previous-secret-id"
		}
		existingSource := privatev1.UserDataSource_builder{
			Secret: privatev1.SecretLocalReference_builder{Id: existingID, Name: secret.GetMetadata().GetName()}.Build(),
		}.Build()
		ref := privatev1.SecretLocalReference_builder{Id: secret.GetId()}.Build()
		if byName {
			ref = privatev1.SecretLocalReference_builder{Name: secret.GetMetadata().GetName()}.Build()
		}
		updatedSource := privatev1.UserDataSource_builder{Secret: ref}.Build()
		mask := &fieldmaskpb.FieldMask{Paths: []string{"spec.user_data_source"}}
		metadata := privatev1.Metadata_builder{Name: "instance", Tenant: testTenant}.Build()
		var result *privatev1.UserDataSource
		var updateErr error
		if bareMetal {
			server, err := NewPrivateBareMetalInstancesServer().SetLogger(logger).
				SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).NotTo(HaveOccurred())
			created, err := server.generic.dao.Create().SetObject(privatev1.BareMetalInstance_builder{
				Metadata: metadata,
				Spec:     privatev1.BareMetalInstanceSpec_builder{UserDataSource: existingSource}.Build(),
			}.Build()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			response, err := server.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id:   created.GetObject().GetId(),
					Spec: privatev1.BareMetalInstanceSpec_builder{UserDataSource: updatedSource}.Build(),
				}.Build(),
				UpdateMask: mask,
			}.Build())
			updateErr = err
			result = response.GetObject().GetSpec().GetUserDataSource()
		} else {
			server, err := NewPrivateComputeInstancesServer().SetLogger(logger).
				SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
			Expect(err).NotTo(HaveOccurred())
			created, err := server.generic.dao.Create().SetObject(privatev1.ComputeInstance_builder{
				Metadata: metadata,
				Spec:     privatev1.ComputeInstanceSpec_builder{UserDataSource: existingSource}.Build(),
			}.Build()).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			response, err := server.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id:   created.GetObject().GetId(),
					Spec: privatev1.ComputeInstanceSpec_builder{UserDataSource: updatedSource}.Build(),
				}.Build(),
				UpdateMask: mask,
			}.Build())
			updateErr = err
			result = response.GetObject().GetSpec().GetUserDataSource()
		}
		if replaced {
			Expect(grpcstatus.Code(updateErr)).To(Equal(grpccodes.InvalidArgument))
			Expect(updateErr.Error()).To(ContainSubstring("user_data_source.secret is immutable"))
			return
		}
		Expect(updateErr).NotTo(HaveOccurred())
		Expect(result.GetSecret().GetId()).To(Equal(secret.GetId()))
		Expect(result.GetSecret().GetName()).To(Equal(secret.GetMetadata().GetName()))
	},
		Entry("compute ID-only reference", false, false, false),
		Entry("compute name-only reference", false, true, false),
		Entry("bare-metal ID-only reference", true, false, false),
		Entry("bare-metal name-only reference", true, true, false),
		Entry("compute name resolving to another ID", false, true, true),
		Entry("bare-metal name resolving to another ID", true, true, true),
	)

	It("validates catalog Secret references while deferring their content format", func() {
		secret := createSecret(map[string][]byte{userDataSecretDataKey: []byte("#cloud-config")})
		source := privatev1.UserDataSource_builder{
			Secret: privatev1.SecretLocalReference_builder{Name: secret.GetMetadata().GetName()}.Build(),
		}.Build()
		policy := privatev1.UserDataSourceFieldPolicy_builder{Locked: source}.Build()
		Expect(validateCatalogItemUserDataSourcePolicy(ctx, referenceScope{tenant: testTenant}, policy, secretsDao, isPureXMLNoDTD)).To(Succeed())
		Expect(source.GetSecret().GetId()).To(Equal(secret.GetId()))
		Expect(source.GetSecret().GetName()).To(Equal(secret.GetMetadata().GetName()))

		source.SetSecret(privatev1.SecretLocalReference_builder{Id: "missing-secret"}.Build())
		Expect(validateCatalogItemUserDataSourcePolicy(ctx, referenceScope{tenant: testTenant}, policy, secretsDao, isPureXMLNoDTD)).To(HaveOccurred())
	})

	It("resolves id-only and name-only references to canonical id and name", func() {
		secret := createSecret(map[string][]byte{userDataSecretDataKey: []byte("#cloud-config")})
		for _, ref := range []*privatev1.SecretLocalReference{
			privatev1.SecretLocalReference_builder{Id: secret.GetId()}.Build(),
			privatev1.SecretLocalReference_builder{Name: secret.GetMetadata().GetName()}.Build(),
		} {
			resolved, err := validateUserDataSecret(ctx, logger, secretsDao, nil, ref)
			Expect(err).ToNot(HaveOccurred())
			Expect(resolved.GetId()).To(Equal(secret.GetId()))
			Expect(resolved.GetName()).To(Equal(secret.GetMetadata().GetName()))
		}
	})

	It("rejects a Secret that is not explicitly typed as user data", func() {
		name := fmt.Sprintf("opaque-%s", uuid.NewString()[:8])
		created, err := secretsDao.Create().SetObject(privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{Name: name, Tenant: testTenant}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_OPAQUE,
			Data:     map[string][]byte{userDataSecretDataKey: []byte("#cloud-config")},
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		_, err = validateUserDataSecret(ctx, logger, secretsDao, nil,
			privatev1.SecretLocalReference_builder{Id: created.GetObject().GetId()}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("expected SECRET_TYPE_USER_DATA"))
	})

	It("rejects a Secret without a non-empty userdata entry", func() {
		secret := createSecret(map[string][]byte{"other": []byte("value")})
		_, err := validateUserDataSecret(ctx, logger, secretsDao, nil,
			privatev1.SecretLocalReference_builder{Id: secret.GetId()}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("non-empty 'userdata' entry"))
	})

	It("rejects a Secret whose userdata entry exceeds 64 KiB", func() {
		secret := createSecret(map[string][]byte{
			userDataSecretDataKey: []byte(strings.Repeat("x", bareMetalInstanceUserDataMaxBytes+1)),
		})
		_, err := validateUserDataSecret(ctx, logger, secretsDao, nil,
			privatev1.SecretLocalReference_builder{Id: secret.GetId()}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("exceeds the maximum"))
	})

	It("rejects a reference that specifies neither id nor name", func() {
		_, err := validateUserDataSecret(ctx, logger, secretsDao, nil,
			privatev1.SecretLocalReference_builder{}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("must specify id or name"))
	})

	It("rejects a shared-tenant secret", func() {
		// The shared tenant is seeded by migrations, so the secret can reference it directly.
		name := fmt.Sprintf("shared-userdata-%s", uuid.NewString()[:8])
		created, err := secretsDao.Create().SetObject(privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{Name: name, Tenant: auth.SharedTenant}.Build(),
			Type:     privatev1.SecretType_SECRET_TYPE_USER_DATA,
			Data:     map[string][]byte{userDataSecretDataKey: []byte("#cloud-config")},
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		_, err = validateUserDataSecret(ctx, logger, secretsDao, nil,
			privatev1.SecretLocalReference_builder{Id: created.GetObject().GetId()}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("shared secrets cannot be used"))
	})

	It("rejects a Secret owned by another tenant", func() {
		const otherTenant = "other-userdata-tenant"
		tenantsDao, err := dao.NewGenericDAO[*privatev1.Tenant]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
		_, err = tenantsDao.Create().SetObject(privatev1.Tenant_builder{
			Id: otherTenant,
			Metadata: privatev1.Metadata_builder{
				Name:   otherTenant,
				Tenant: otherTenant,
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		created, err := secretsDao.Create().SetObject(privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   fmt.Sprintf("other-userdata-%s", uuid.NewString()[:8]),
				Tenant: otherTenant,
			}.Build(),
			Type: privatev1.SecretType_SECRET_TYPE_USER_DATA,
			Data: map[string][]byte{userDataSecretDataKey: []byte("#cloud-config")},
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		restrictedTenancy := auth.NewMockTenancyLogic(ctrl)
		restrictedVisibility, err := auth.NewVisibility().
			AddVisibleTenants(auth.SharedTenant, testTenant).
			Build()
		Expect(err).ToNot(HaveOccurred())
		restrictedTenancy.EXPECT().DetermineVisibility(gomock.Any()).
			Return(restrictedVisibility, nil).AnyTimes()
		restrictedSecretsDao, err := dao.NewGenericDAO[*privatev1.Secret]().
			SetLogger(logger).
			SetTenancyLogic(restrictedTenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = validateUserDataSecret(ctx, logger, restrictedSecretsDao, nil,
			privatev1.SecretLocalReference_builder{Id: created.GetObject().GetId()}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("there is no secret"))
	})

	It("returns Internal when a Vault-backed Secret can't be loaded without a store", func() {
		updated, err := secretsDao.Create().SetObject(privatev1.Secret_builder{
			Metadata: privatev1.Metadata_builder{
				Name:   fmt.Sprintf("vault-userdata-%s", uuid.NewString()[:8]),
				Tenant: testTenant,
			}.Build(),
			Backend: privatev1.SecretBackend_SECRET_BACKEND_VAULT,
			Type:    privatev1.SecretType_SECRET_TYPE_USER_DATA,
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		_, err = validateUserDataSecret(ctx, logger, secretsDao, nil,
			privatev1.SecretLocalReference_builder{Id: updated.GetObject().GetId()}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Internal))
	})

	It("rejects a user data source without a selected branch", func() {
		server, err := NewPrivateComputeInstancesServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		for _, spec := range []proto.Message{
			privatev1.ComputeInstanceSpec_builder{UserDataSource: &privatev1.UserDataSource{}}.Build(),
			privatev1.BareMetalInstanceSpec_builder{UserDataSource: &privatev1.UserDataSource{}}.Build(),
		} {
			Expect(server.generic.validator.Validate(spec)).To(HaveOccurred())
		}
	})

	It("allows Compute inline user data to migrate atomically to a Secret reference", func() {
		server, err := NewPrivateComputeInstancesServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		created, err := server.generic.dao.Create().SetObject(privatev1.ComputeInstance_builder{
			Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("ci-%s", uuid.NewString()[:8]), Tenant: testTenant}.Build(),
			Spec:     privatev1.ComputeInstanceSpec_builder{UserDataSource: privatev1.UserDataSource_builder{Inline: new("inline")}.Build()}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		request := privatev1.ComputeInstancesUpdateRequest_builder{
			Object: privatev1.ComputeInstance_builder{
				Id: created.GetObject().GetId(),
				Spec: privatev1.ComputeInstanceSpec_builder{
					UserDataSource: privatev1.UserDataSource_builder{Secret: privatev1.SecretLocalReference_builder{Id: "secret-id", Name: "secret-name"}.Build()}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.user_data_source"}},
		}.Build()
		Expect(server.validateAndResolveUserDataSecret(ctx, request.GetObject().GetSpec(), false)).To(Succeed())
		candidate := proto.Clone(created.GetObject()).(*privatev1.ComputeInstance)
		candidate.GetSpec().SetUserDataSource(privatev1.UserDataSource_builder{Secret: request.GetObject().GetSpec().GetUserDataSource().GetSecret()}.Build())
		Expect(validateComputeInstanceImmutability(
			created.GetObject(), candidate, request.GetUpdateMask(),
		)).To(Succeed())
	})

	It("rejects changing an existing Compute Secret reference", func() {
		server, err := NewPrivateComputeInstancesServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		created, err := server.generic.dao.Create().SetObject(privatev1.ComputeInstance_builder{
			Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("ci-%s", uuid.NewString()[:8]), Tenant: testTenant}.Build(),
			Spec: privatev1.ComputeInstanceSpec_builder{
				UserDataSource: privatev1.UserDataSource_builder{Secret: privatev1.SecretLocalReference_builder{Id: "secret-id", Name: "secret-name"}.Build()}.Build(),
			}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		request := privatev1.ComputeInstancesUpdateRequest_builder{
			Object: privatev1.ComputeInstance_builder{
				Id: created.GetObject().GetId(),
				Spec: privatev1.ComputeInstanceSpec_builder{
					UserDataSource: privatev1.UserDataSource_builder{Secret: privatev1.SecretLocalReference_builder{Id: "other-id", Name: "other-name"}.Build()}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.user_data_source.secret"}},
		}.Build()
		candidate := proto.Clone(created.GetObject()).(*privatev1.ComputeInstance)
		candidate.GetSpec().SetUserDataSource(privatev1.UserDataSource_builder{Secret: request.GetObject().GetSpec().GetUserDataSource().GetSecret()}.Build())
		err = validateComputeInstanceImmutability(created.GetObject(), candidate, request.GetUpdateMask())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("user_data_source.secret is immutable"))
	})

	It("rejects inline data and a reference together", func() {
		for _, spec := range []proto.Message{&privatev1.ComputeInstanceSpec{}, &privatev1.BareMetalInstanceSpec{}} {
			err := protojson.Unmarshal([]byte(`{"user_data_source":{"inline":"inline","secret":{"id":"secret"}}}`), spec)
			Expect(err).To(HaveOccurred())
		}
	})

	It("allows BareMetal inline user data to migrate atomically to a Secret reference", func() {
		server, err := NewPrivateBareMetalInstancesServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		created, err := server.generic.dao.Create().SetObject(privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("bmi-%s", uuid.NewString()[:8]), Tenant: testTenant}.Build(),
			Spec:     privatev1.BareMetalInstanceSpec_builder{UserDataSource: privatev1.UserDataSource_builder{Inline: new("inline")}.Build()}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		request := privatev1.BareMetalInstancesUpdateRequest_builder{
			Object: privatev1.BareMetalInstance_builder{
				Id: created.GetObject().GetId(),
				Spec: privatev1.BareMetalInstanceSpec_builder{
					UserDataSource: privatev1.UserDataSource_builder{Secret: privatev1.SecretLocalReference_builder{Id: "secret-id", Name: "secret-name"}.Build()}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.user_data_source"}},
		}.Build()
		Expect(server.validateAndResolveUserDataSecret(ctx, request.GetObject().GetSpec(), false)).To(Succeed())
		candidate := proto.Clone(created.GetObject()).(*privatev1.BareMetalInstance)
		candidate.GetSpec().SetUserDataSource(privatev1.UserDataSource_builder{Secret: request.GetObject().GetSpec().GetUserDataSource().GetSecret()}.Build())
		Expect(validateBareMetalImmutability(
			created.GetObject(), candidate, request.GetUpdateMask(),
		)).To(Succeed())
	})

	It("allows a BareMetal Secret reference to be assigned when no user data exists", func() {
		server, err := NewPrivateBareMetalInstancesServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		created, err := server.generic.dao.Create().SetObject(privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("bmi-%s", uuid.NewString()[:8]), Tenant: testTenant}.Build(),
			Spec:     privatev1.BareMetalInstanceSpec_builder{}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		request := privatev1.BareMetalInstancesUpdateRequest_builder{
			Object: privatev1.BareMetalInstance_builder{
				Id: created.GetObject().GetId(),
				Spec: privatev1.BareMetalInstanceSpec_builder{
					UserDataSource: privatev1.UserDataSource_builder{Secret: privatev1.SecretLocalReference_builder{Id: "secret-id", Name: "secret-name"}.Build()}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.user_data_source.secret"}},
		}.Build()
		Expect(server.validateAndResolveUserDataSecret(ctx, request.GetObject().GetSpec(), false)).To(Succeed())
		candidate := proto.Clone(created.GetObject()).(*privatev1.BareMetalInstance)
		candidate.GetSpec().SetUserDataSource(privatev1.UserDataSource_builder{Secret: request.GetObject().GetSpec().GetUserDataSource().GetSecret()}.Build())
		Expect(validateBareMetalImmutability(
			created.GetObject(), candidate, request.GetUpdateMask(),
		)).To(Succeed())
	})
})
