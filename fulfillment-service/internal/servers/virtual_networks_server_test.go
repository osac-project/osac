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
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
	"github.com/osac-project/fulfillment-service/internal/collections"
	"github.com/osac-project/fulfillment-service/internal/database"
	"github.com/osac-project/fulfillment-service/internal/database/dao"
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

		// Create a default NetworkClass for tests:
		ncDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		nc := privatev1.NetworkClass_builder{
			Id:                     "default",
			ImplementationStrategy: "ovn-kubernetes",
			Metadata: privatev1.Metadata_builder{
				Tenant: auth.SharedTenant,
			}.Build(),
			IsDefault: new(true),
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
					Spec: privatev1.VirtualNetworkSpec_builder{
						Region:                 "us-east-1",
						NetworkClass:           "default",
						ImplementationStrategy: "ovn-kubernetes",
						Ipv4Cidr:               new("10.0.0.0/16"),
						Capabilities: privatev1.VirtualNetworkCapabilities_builder{
							EnableIpv4: true,
						}.Build(),
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
			Expect(publicObj.GetSpec().GetNetworkClass()).To(Equal("default"))
		})

		It("Update object", func() {
			// Create the object via the private server:
			privateObj := createVirtualNetwork()

			// Update the object via public server:
			updateResponse, err := publicServer.Update(ctx, publicv1.VirtualNetworksUpdateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Id: privateObj.GetId(),
					Metadata: publicv1.Metadata_builder{
						Name: "updated-name",
					}.Build(),
					Spec: publicv1.VirtualNetworkSpec_builder{
						NetworkClass: "default",
						Ipv4Cidr:     new("10.0.0.0/16"),
						Capabilities: publicv1.VirtualNetworkCapabilities_builder{
							EnableIpv4: true,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetMetadata().GetName()).To(Equal("updated-name"))

			// Get and verify via public server:
			getResponse, err := publicServer.Get(ctx, publicv1.VirtualNetworksGetRequest_builder{
				Id: privateObj.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetName()).To(Equal("updated-name"))
		})

		It("Update object preserves CIDRs when explicitly repeated in request", func() {
			// Create the object via the private server:
			privateObj := createVirtualNetwork()

			// Update with CIDRs explicitly repeated — must pass, CIDRs preserved.
			updateResponse, err := publicServer.Update(ctx, publicv1.VirtualNetworksUpdateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Id: privateObj.GetId(),
					Metadata: publicv1.Metadata_builder{
						Name: "cidr-explicit",
					}.Build(),
					Spec: publicv1.VirtualNetworkSpec_builder{
						NetworkClass: "default",
						Ipv4Cidr:     new("10.0.0.0/16"),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetIpv4Cidr()).To(Equal("10.0.0.0/16"))
		})

		It("Update object preserves CIDRs when omitted from request body", func() {
			// Create the object via the private server:
			privateObj := createVirtualNetwork()

			// Update body omits CIDR fields entirely. The private server's
			// validateImmutableFieldsVirtualNetwork backfills CIDRs from the existing object.
			updateResponse, err := publicServer.Update(ctx, publicv1.VirtualNetworksUpdateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Id: privateObj.GetId(),
					Metadata: publicv1.Metadata_builder{
						Name: "cidr-omitted",
					}.Build(),
					Spec: publicv1.VirtualNetworkSpec_builder{
						NetworkClass: "default",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetIpv4Cidr()).To(Equal("10.0.0.0/16"))
		})

		It("Update object rejects CIDR change via public API (CR-001)", func() {
			// Create with a known CIDR via the private server:
			privateObj := createVirtualNetwork()

			// Update via public server with a different CIDR — must be rejected as immutable.
			_, err := publicServer.Update(ctx, publicv1.VirtualNetworksUpdateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Id: privateObj.GetId(),
					Spec: publicv1.VirtualNetworkSpec_builder{
						NetworkClass: "default",
						Ipv4Cidr:     new("192.168.0.0/16"),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(err.Error()).To(ContainSubstring("immutable"))
		})

		It("Update object rejects stale version when lock is enabled", func() {
			// Create the object via the private server:
			privateObj := createVirtualNetwork()

			// Attempt an update via public server with lock enabled and a wrong version — expect codes.Aborted:
			_, err := publicServer.Update(ctx, publicv1.VirtualNetworksUpdateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Id: privateObj.GetId(),
					Metadata: publicv1.Metadata_builder{
						Name:    "locked-update",
						Version: math.MaxInt32,
					}.Build(),
					Spec: publicv1.VirtualNetworkSpec_builder{
						NetworkClass: "default",
						Ipv4Cidr:     new("10.0.0.0/16"),
					}.Build(),
				}.Build(),
				Lock: true,
			}.Build())
			Expect(err).To(HaveOccurred())
			st, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(st.Code()).To(Equal(grpccodes.Aborted))
		})

		It("Create object via public API sets default region", func() {
			// Create a VirtualNetwork via the public server (no region field):
			createResponse, err := publicServer.Create(ctx, publicv1.VirtualNetworksCreateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "public-vn",
					}.Build(),
					Spec: publicv1.VirtualNetworkSpec_builder{
						NetworkClass: "default",
						Ipv4Cidr:     new("10.0.0.0/16"),
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

		It("Public Create without network_class auto-populates from default NC", func() {
			// Create VN via public server WITHOUT network_class (should use default NC):
			createResponse, err := publicServer.Create(ctx, publicv1.VirtualNetworksCreateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "default-nc-vn",
					}.Build(),
					Spec: publicv1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("10.2.0.0/16"),
						Capabilities: publicv1.VirtualNetworkCapabilities_builder{
							EnableIpv4: true,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Verify the VN's network_class is the default NC's ID:
			Expect(createResponse.GetObject().GetSpec().GetNetworkClass()).To(Equal("default"))
		})

		It("Public Create honors explicit network_class instead of using default", func() {
			// Create a second non-default NetworkClass via DAO:
			ncDao, ncErr := dao.NewGenericDAO[*privatev1.NetworkClass]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(ncErr).ToNot(HaveOccurred())

			altNC := privatev1.NetworkClass_builder{
				ImplementationStrategy: "ovn-kubernetes",
				Metadata: privatev1.Metadata_builder{
					Tenant: "shared",
				}.Build(),
				Capabilities: privatev1.NetworkClassCapabilities_builder{
					SupportsIpv4: true,
				}.Build(),
				Status: privatev1.NetworkClassStatus_builder{
					State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
				}.Build(),
			}.Build()
			createNcResponse, ncErr := ncDao.Create().SetObject(altNC).Do(ctx)
			Expect(ncErr).ToNot(HaveOccurred())
			altNCId := createNcResponse.GetObject().GetId()

			// Create VN via public server with explicit network_class pointing to altNC:
			createResponse, err := publicServer.Create(ctx, publicv1.VirtualNetworksCreateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "explicit-nc-vn",
					}.Build(),
					Spec: publicv1.VirtualNetworkSpec_builder{
						NetworkClass: altNCId,
						Ipv4Cidr:     new("10.1.0.0/16"),
						Capabilities: publicv1.VirtualNetworkCapabilities_builder{
							EnableIpv4: true,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Verify the VN uses the alternate NC, NOT the default:
			Expect(createResponse.GetObject().GetSpec().GetNetworkClass()).To(Equal(altNCId))
		})

		It("Public Update preserves network_class (AddIgnoredFields on inMapper)", func() {
			// Create VN via private server with explicit network_class:
			privateObj := createVirtualNetwork()
			Expect(privateObj.GetSpec().GetNetworkClass()).To(Equal("default"))

			// Update via public server changing only metadata (no network_class in request):
			updateResponse, err := publicServer.Update(ctx, publicv1.VirtualNetworksUpdateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Id: privateObj.GetId(),
					Metadata: publicv1.Metadata_builder{
						Name: "renamed-vn",
					}.Build(),
					Spec: publicv1.VirtualNetworkSpec_builder{
						Ipv4Cidr: new("10.0.0.0/16"),
						Capabilities: publicv1.VirtualNetworkCapabilities_builder{
							EnableIpv4: true,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			// The network_class should still be "default" even though it was not in the public update request:
			Expect(updateResponse.GetObject().GetSpec().GetNetworkClass()).To(Equal("default"))
			Expect(updateResponse.GetObject().GetMetadata().GetName()).To(Equal("renamed-vn"))
		})

		It("Public Update rejects explicit network_class change with immutability error", func() {
			// Create VN via private server with explicit network_class:
			privateObj := createVirtualNetwork()
			Expect(privateObj.GetSpec().GetNetworkClass()).To(Equal("default"))

			// Update via public server with a different network_class:
			_, err := publicServer.Update(ctx, publicv1.VirtualNetworksUpdateRequest_builder{
				Object: publicv1.VirtualNetwork_builder{
					Id: privateObj.GetId(),
					Spec: publicv1.VirtualNetworkSpec_builder{
						NetworkClass: "different-nc",
						Ipv4Cidr:     new("10.0.0.0/16"),
						Capabilities: publicv1.VirtualNetworkCapabilities_builder{
							EnableIpv4: true,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("spec.network_class"))
			Expect(status.Message()).To(ContainSubstring("immutable"))
		})

		It("Public Create without network_class when no default exists returns InvalidArgument", func() {
			// Remove the default NC that BeforeEach created:
			ncDao, ncErr := dao.NewGenericDAO[*privatev1.NetworkClass]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(ncErr).ToNot(HaveOccurred())
			_, ncErr = ncDao.Delete().SetId("default").Do(ctx)
			Expect(ncErr).ToNot(HaveOccurred())

			// Attempt public Create without network_class (no default is configured):
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
			Expect(err.Error()).To(ContainSubstring("no default NetworkClass is configured"))
		})
	})
})
