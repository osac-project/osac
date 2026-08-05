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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
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

		It("Can't create a tenant, not implemented", func() {
			// Try to create the tenant via the public server. It should fail with an Unimplemented error.
			_, err := publicServer.Create(ctx, publicv1.TenantsCreateRequest_builder{
				Object: publicv1.Tenant_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "my-tenant",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.Unimplemented))
		})

		It("Can't update a tenant, not implemented", func() {
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

			// Try to update the tenant via the public server. It should fail with an Unimplemented error.
			_, err = publicServer.Update(ctx, publicv1.TenantsUpdateRequest_builder{
				Object: publicv1.Tenant_builder{
					Id: id,
					Metadata: publicv1.Metadata_builder{
						Name: "my-tenant",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.Unimplemented))
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
	})
})
