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

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
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
			Data:     data,
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		return response.GetObject()
	}

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

	It("rejects a Secret without a non-empty userdata entry", func() {
		secret := createSecret(map[string][]byte{"other": []byte("value")})
		_, err := validateUserDataSecret(ctx, logger, secretsDao, nil,
			privatev1.SecretLocalReference_builder{Id: secret.GetId()}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		Expect(err.Error()).To(ContainSubstring("non-empty 'userdata' entry"))
	})

	It("rejects inline data and a reference together", func() {
		computeServer, err := NewPrivateComputeInstancesServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		err = computeServer.validateUserDataMutualExclusion(privatev1.ComputeInstanceSpec_builder{
			UserData:       new("inline"),
			UserDataSecret: privatev1.SecretLocalReference_builder{Id: "secret"}.Build(),
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	It("allows BareMetal inline user data to migrate atomically to a Secret reference", func() {
		server, err := NewPrivateBareMetalInstancesServer().SetLogger(logger).
			SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		created, err := server.generic.dao.Create().SetObject(privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("bmi-%s", uuid.NewString()[:8]), Tenant: testTenant}.Build(),
			Spec:     privatev1.BareMetalInstanceSpec_builder{UserData: new("inline")}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())

		request := privatev1.BareMetalInstancesUpdateRequest_builder{
			Object: privatev1.BareMetalInstance_builder{
				Id: created.GetObject().GetId(),
				Spec: privatev1.BareMetalInstanceSpec_builder{
					UserDataSecret: privatev1.SecretLocalReference_builder{Id: "secret-id", Name: "secret-name"}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.user_data", "spec.user_data_secret"}},
		}.Build()
		Expect(server.validateUserDataMutualExclusionForUpdate(ctx, request)).To(Succeed())
		Expect(server.validateImmutability(ctx, request)).To(Succeed())
	})
})
