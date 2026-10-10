/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"fmt"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Virtual networks server", func() {
	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = auth.ContextWithSubject(
			ctx,
			&auth.Subject{
				User:    "system",
				Tenants: collections.NewUniversalSet[string](),
			},
		)

		// Create the singleton NetworkClass for tests:
		ncDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		nc := privatev1.NetworkClass_builder{
			Id:            "default",
			FabricManager: new("ovn-kubernetes"),
			Metadata: privatev1.Metadata_builder{
				Tenant: testTenant,
			}.Build(),
			Capabilities: privatev1.NetworkClassCapabilities_builder{
				SupportsIpv4:      true,
				SupportsIpv6:      true,
				SupportsDualStack: true,
			}.Build(),
			Status: privatev1.NetworkClassStatus_builder{
				State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			}.Build(),
		}.Build()

		createResponse, err := ncDao.Create().
			SetObject(nc).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(createResponse.GetObject().GetId()).To(Equal("default"))
	})

	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewVirtualNetworksServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewVirtualNetworksServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewVirtualNetworksServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewVirtualNetworksServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var (
			publicServer  *VirtualNetworksServer
			privateServer *PrivateVirtualNetworksServer
		)

		BeforeEach(func() {
			var err error

			// Create the public server:
			publicServer, err = NewVirtualNetworksServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create a private server for test data setup (private API requires
			// region which is not exposed in public API):
			privateServer, err = NewPrivateVirtualNetworksServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		// createVirtualNetwork creates a VirtualNetwork via the private server (which accepts
		// region) and returns the created object.
		createVirtualNetwork := func() *privatev1.VirtualNetwork {
			response, err := privateServer.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
				Object: privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:       "us-east-1",
						NetworkClass: privatev1.NetworkClassReference_builder{Id: "default"}.Build(),
						Ipv4Cidr:     new("10.0.0.0/16"),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			return response.GetObject()
		}

		It("List objects", func() {
			// Create a few objects via the private server:
			const count = 10
			for range count {
				createVirtualNetwork()
			}

			// List the objects via public server:
			response, err := publicServer.List(ctx, publicv1.VirtualNetworksListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			// Create a few objects via the private server:
			const count = 10
			for range count {
				createVirtualNetwork()
			}

			// List the objects via public server:
			response, err := publicServer.List(ctx, publicv1.VirtualNetworksListRequest_builder{
				Limit: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with offset", func() {
			// Create a few objects via the private server:
			const count = 10
			for range count {
				createVirtualNetwork()
			}

			// List the objects via public server:
			response, err := publicServer.List(ctx, publicv1.VirtualNetworksListRequest_builder{
				Offset: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", count-1))
		})

		It("List objects with filter", func() {
			// Create a few objects via the private server:
			const count = 10
			var ids []string
			for range count {
				obj := createVirtualNetwork()
				ids = append(ids, obj.GetId())
			}

			// List the objects via public server:
			for _, id := range ids {
				response, err := publicServer.List(ctx, publicv1.VirtualNetworksListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", id)),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", 1))
				Expect(response.GetItems()[0].GetId()).To(Equal(id))
			}
		})

		It("Get object", func() {
			// Create the object via the private server:
			privateObj := createVirtualNetwork()

			// Get it via public server:
			getResponse, err := publicServer.Get(ctx, publicv1.VirtualNetworksGetRequest_builder{
				Id: privateObj.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			publicObj := getResponse.GetObject()
			Expect(publicObj.GetId()).To(Equal(privateObj.GetId()))
		})

		It("Create object via public API sets default region", func() {
			// Create a VirtualNetwork via the public server (no region field):
			createResponse, err := publicServer.Create(ctx, publicv1.VirtualNetworksCreateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "public-vn",
					}.Build(),
					Spec: publicv1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("10.0.0.0/16"),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			publicObj := createResponse.GetObject()

			// Verify via private server that region was set to "default":
			privateGetResponse, err := privateServer.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
				Id: publicObj.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(privateGetResponse.GetObject().GetSpec().GetRegion()).To(Equal("default"))
		})

		It("Delete object", func() {
			// Create the object via the private server:
			privateObj := createVirtualNetwork()

			// Add a finalizer, as otherwise the object will be immediately deleted and archived and it
			// won't be possible to verify the deletion timestamp. This can't be done using the server
			// because this is a public object, and public objects don't have the finalizers field.
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(
				ctx,
				`update virtual_networks set finalizers = '{"a"}' where id = $1`,
				privateObj.GetId(),
			)
			Expect(err).ToNot(HaveOccurred())

			// Delete the object via public server:
			_, err = publicServer.Delete(ctx, publicv1.VirtualNetworksDeleteRequest_builder{
				Id: privateObj.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get and verify via public server:
			getResponse, err := publicServer.Get(ctx, publicv1.VirtualNetworksGetRequest_builder{
				Id: privateObj.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := getResponse.GetObject()
			Expect(object.GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("Public Create without network_class resolves the singleton NetworkClass", func() {
			// Create VN via public server (network_class is not exposed publicly; the
			// private server resolves it from the deployment singleton):
			createResponse, err := publicServer.Create(ctx, publicv1.VirtualNetworksCreateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "default-nc-vn",
					}.Build(),
					Spec: publicv1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("10.2.0.0/16"),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Verify via the private server that network_class is the singleton's ID:
			privateGetResponse, err := privateServer.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(privateGetResponse.GetObject().GetSpec().GetNetworkClass().GetId()).To(Equal("default"))
		})

		It("Public Create without network_class when no singleton exists returns InvalidArgument", func() {
			// Remove the singleton NC that BeforeEach created:
			ncDao, ncErr := dao.NewGenericDAO[*privatev1.NetworkClass]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(ncErr).ToNot(HaveOccurred())
			_, ncErr = ncDao.Delete().SetId("default").Do(ctx)
			Expect(ncErr).ToNot(HaveOccurred())

			// Attempt public Create without network_class (no singleton is configured):
			_, err := publicServer.Create(ctx, publicv1.VirtualNetworksCreateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "no-default-nc-vn",
					}.Build(),
					Spec: publicv1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("10.3.0.0/16"),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("no NetworkClass is configured"))
		})
	})
})
