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
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Public tenants server", func() {
	var (
		privateServer *PrivateTenantsServer
		publicServer  *TenantsServer
	)

	BeforeEach(func() {
		var err error

		// Create the private server:
		privateServer, err = NewPrivateTenantsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Create public server:
		publicServer, err = NewTenantsServer().
			SetLogger(logger).
			SetAttributionLogic(attribution).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Creation", func() {
		It("Can be built if all required parameters are set", func() {
			srv, err := NewTenantsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(srv).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			srv, err := NewTenantsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(srv).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			srv, err := NewTenantsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(srv).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		It("Lists tenants", func() {
			// Create the tenant using the private server, as that isn't implemented for the public server:
			_, err := privateServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: &privatev1.Metadata{
						Name: "my-tenant",
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// List tenants via the public server. It is important to use a filter to skip the builtin
			// tenants.
			listResponse, err := publicServer.List(ctx, publicv1.TenantsListRequest_builder{
				Filter: new("this.metadata.name == 'my-tenant'"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.Size).To(Equal(int32(1)))
			Expect(listResponse.Items).To(HaveLen(1))
			Expect(listResponse.Items[0].Metadata.Name).To(Equal("my-tenant"))
		})

		It("Gets a tenant by identifier", func() {
			// Create the tenant using the private server, as that isn't implemented for the public server:
			createResponse, err := privateServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: &privatev1.Metadata{
						Name: "my-tenant",
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			id := object.GetId()

			// Get the tenant via the public server:
			getResponse, err := publicServer.Get(ctx, publicv1.TenantsGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetId()).To(Equal(id))
			Expect(getResponse.GetObject().GetMetadata().GetName()).To(Equal("my-tenant"))
		})

		It("Exposes only the public onboarding status field", func() {
			fields := (&publicv1.TenantStatus{}).ProtoReflect().Descriptor().Fields()
			Expect(fields.Len()).To(Equal(1))
			Expect(string(fields.Get(0).Name())).To(Equal("onboarded"))
		})

		It("Maps only the public onboarding status field", func() {
			privateMessage := "private detail"
			createResponse, err := privateServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: &privatev1.Metadata{
						Name: "status-tenant",
					},
					Status: privatev1.TenantStatus_builder{
						Onboarded:        true,
						State:            privatev1.TenantState_TENANT_STATE_SYNCED,
						Message:          &privateMessage,
						IdpTenantName:    "idp-tenant",
						BreakGlassUserId: "breakglass-user",
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
			id := createResponse.GetObject().GetId()

			publicGet, err := publicServer.Get(ctx, publicv1.TenantsGetRequest_builder{Id: id}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(publicGet.GetObject().GetStatus().GetOnboarded()).To(BeTrue())

			publicList, err := publicServer.List(ctx, publicv1.TenantsListRequest_builder{
				Filter: new("this.metadata.name == 'status-tenant'"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(publicList.GetItems()).To(HaveLen(1))
			Expect(publicList.GetItems()[0].GetStatus().GetOnboarded()).To(BeTrue())

			privateGet, err := privateServer.Get(ctx, privatev1.TenantsGetRequest_builder{Id: id}.Build())
			Expect(err).ToNot(HaveOccurred())
			privateStatus := privateGet.GetObject().GetStatus()
			Expect(privateStatus.GetOnboarded()).To(BeTrue())
			Expect(privateStatus.GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
			Expect(privateStatus.GetMessage()).To(Equal("private detail"))
			Expect(privateStatus.GetIdpTenantName()).To(Equal("idp-tenant"))
			Expect(privateStatus.GetBreakGlassUserId()).To(Equal("breakglass-user"))
			Expect(privateStatus.GetConditions()).To(HaveLen(1))
		})

		It("Creates a tenant without status", func() {
			response, err := publicServer.Create(ctx, publicv1.TenantsCreateRequest_builder{
				Object: publicv1.Tenant_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "public-tenant",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetId()).To(Equal("public-tenant"))
			Expect(response.GetObject().GetMetadata().GetName()).To(Equal("public-tenant"))
		})

		It("Updates a tenant without status", func() {
			createResponse, err := privateServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: &privatev1.Metadata{
						Name: "update-tenant",
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			updateResponse, err := publicServer.Update(ctx, publicv1.TenantsUpdateRequest_builder{
				Object: publicv1.Tenant_builder{
					Id: id,
					Metadata: publicv1.Metadata_builder{
						Name:   "update-tenant",
						Labels: map[string]string{"public": "true"},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetMetadata().GetLabels()).To(HaveKeyWithValue("public", "true"))
		})

		It("Rejects public tenant creation with status", func() {
			_, err := publicServer.Create(ctx, publicv1.TenantsCreateRequest_builder{
				Object: publicv1.Tenant_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "my-tenant",
					}.Build(),
					Status: publicv1.TenantStatus_builder{}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("status is output-only"))
		})

		It("Rejects public tenant update with status", func() {
			createResponse, err := privateServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: &privatev1.Metadata{
						Name: "my-tenant",
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			_, err = publicServer.Update(ctx, publicv1.TenantsUpdateRequest_builder{
				Object: publicv1.Tenant_builder{
					Id: id,
					Metadata: publicv1.Metadata_builder{
						Name: "my-tenant",
					}.Build(),
					Status: publicv1.TenantStatus_builder{}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.onboarded"}},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("status is output-only"))
		})

		It("Can't delete a tenant, not implemented", func() {
			// Create the tenant using the private server, as that isn't implemented for the public server:
			createResponse, err := privateServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "my-tenant",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			id := object.GetId()

			// Try to delete the tenant via the public server. It should fail with an Unimplemented error.
			_, err = publicServer.Delete(ctx, publicv1.TenantsDeleteRequest_builder{
				Id: id,
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.Unimplemented))
		})

		It("Omits the external billing id", func() {
			billingID := "acct-1"
			created, err := privateServer.Create(ctx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: privatev1.Metadata_builder{Name: "billing-tenant"}.Build(),
					Spec: privatev1.TenantSpec_builder{
						ExternalBillingId: &billingID,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())

			got, err := publicServer.Get(ctx, publicv1.TenantsGetRequest_builder{
				Id: created.GetObject().GetId(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			encoded, err := protojson.Marshal(got.GetObject())
			Expect(err).NotTo(HaveOccurred())
			Expect(string(encoded)).NotTo(ContainSubstring("external_billing_id"))
			Expect(string(encoded)).NotTo(ContainSubstring("externalBillingId"))

			listed, err := publicServer.List(ctx, publicv1.TenantsListRequest_builder{
				Filter: new("this.metadata.name == 'billing-tenant'"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetItems()).To(HaveLen(1))
			encoded, err = protojson.Marshal(listed.GetItems()[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(string(encoded)).NotTo(ContainSubstring("externalBillingId"))
		})
	})
})
