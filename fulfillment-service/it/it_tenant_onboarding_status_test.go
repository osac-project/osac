/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Tenant onboarding status", func() {
	var (
		ctx           context.Context
		publicClient  publicv1.TenantsClient
		privateClient privatev1.TenantsClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		publicClient = publicv1.NewTenantsClient(tool.ExternalView().AdminConn())
		privateClient = privatev1.NewTenantsClient(tool.InternalView().AdminConn())
	})

	It("Exposes only the public onboarding status field", func() {
		name := fmt.Sprintf("onboarding-%s", uuid.New())

		privateCreate, err := privateClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: &privatev1.Metadata{Name: name},
				Status: privatev1.TenantStatus_builder{
					Onboarded:     true,
					State:         privatev1.TenantState_TENANT_STATE_SYNCED,
					IdpTenantName: name,
					Conditions: []*privatev1.TenantCondition{
						privatev1.TenantCondition_builder{
							Type:   privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY,
							Status: privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := privateCreate.GetObject().GetId()

		DeferCleanup(func(cleanupCtx context.Context) {
			_, err := privateClient.Delete(cleanupCtx, privatev1.TenantsDeleteRequest_builder{Id: id}.Build())
			if grpcstatus.Code(err) == grpccodes.NotFound {
				return
			}
			Expect(err).ToNot(HaveOccurred())
		})

		publicGet, err := publicClient.Get(ctx, publicv1.TenantsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(publicGet.GetObject().GetStatus().GetOnboarded()).To(BeTrue())

		privateGet, err := privateClient.Get(ctx, privatev1.TenantsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(privateGet.GetObject().GetStatus().GetOnboarded()).To(BeTrue())
		Expect(privateGet.GetObject().GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
		defaultNetworking := findTenantCondition(
			privateGet.GetObject().GetStatus().GetConditions(),
			privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY,
		)
		Expect(defaultNetworking).NotTo(BeNil())
		Expect(defaultNetworking.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
	})

	It("Rejects public status writes", func() {
		name := fmt.Sprintf("onboarding-write-%s", uuid.New())

		_, err := publicClient.Create(ctx, publicv1.TenantsCreateRequest_builder{
			Object: publicv1.Tenant_builder{
				Metadata: &publicv1.Metadata{Name: name},
				Status:   publicv1.TenantStatus_builder{}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("status is output-only"))

		privateCreate, err := privateClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{Metadata: &privatev1.Metadata{Name: name}}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id := privateCreate.GetObject().GetId()

		DeferCleanup(func(cleanupCtx context.Context) {
			_, err := privateClient.Delete(cleanupCtx, privatev1.TenantsDeleteRequest_builder{Id: id}.Build())
			if grpcstatus.Code(err) == grpccodes.NotFound {
				return
			}
			Expect(err).ToNot(HaveOccurred())
		})

		_, err = publicClient.Update(ctx, publicv1.TenantsUpdateRequest_builder{
			Object: publicv1.Tenant_builder{
				Id:       id,
				Metadata: &publicv1.Metadata{Name: name},
				Status:   publicv1.TenantStatus_builder{}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.onboarded"}},
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok = grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("status is output-only"))
	})
})
