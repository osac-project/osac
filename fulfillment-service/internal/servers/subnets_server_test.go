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
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"k8s.io/client-go/rest"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Subnets server", func() {
	var virtualNetworkID string

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

		createNCResponse, err := ncDao.Create().
			SetObject(nc).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(createNCResponse.GetObject().GetId()).To(Equal("default"))

		// Create a default VirtualNetwork for tests:
		vnDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			Build()
		Expect(err).ToNot(HaveOccurred())

		vn := privatev1.VirtualNetwork_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: testTenant,
			}.Build(),
			Spec: privatev1.VirtualNetworkSpec_builder{
				Region:       "us-east-1",
				NetworkClass: privatev1.NetworkClassReference_builder{Id: "default"}.Build(),
				Ipv4Cidr:     new("10.0.0.0/16"),
			}.Build(),
			Status: privatev1.VirtualNetworkStatus_builder{
				State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
			}.Build(),
		}.Build()

		createVNResponse, err := vnDao.Create().
			SetObject(vn).
			Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		virtualNetworkID = createVNResponse.GetObject().GetId()
		Expect(virtualNetworkID).ToNot(BeEmpty())
	})

	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewSubnetsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewSubnetsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewSubnetsServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewSubnetsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *SubnetsServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewSubnetsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		setupCudnEvpnPublicServer := func(vmCount int) (*SubnetsServer, *httptest.Server) {
			ncDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).NotTo(HaveOccurred())
			networkClassResponse, err := ncDao.Get().SetId("default").Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			networkClass := networkClassResponse.GetObject()
			networkClass.SetK8SManager("cudn_evpn")
			_, err = ncDao.Update().SetObject(networkClass).Do(ctx)
			Expect(err).NotTo(HaveOccurred())

			vnDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).NotTo(HaveOccurred())
			vnResponse, err := vnDao.Get().SetId(virtualNetworkID).Do(ctx)
			Expect(err).NotTo(HaveOccurred())
			virtualNetwork := vnResponse.GetObject()
			virtualNetwork.GetStatus().SetHub("test-hub")
			_, err = vnDao.Update().SetObject(virtualNetwork).Do(ctx)
			Expect(err).NotTo(HaveOccurred())

			hubServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch request.URL.Path {
				case "/apis/osac.openshift.io/v1alpha1/namespaces/networking/subnets":
					_, _ = writer.Write([]byte(`{"apiVersion":"osac.openshift.io/v1alpha1","kind":"SubnetList","metadata":{"resourceVersion":"1"},"items":[{"apiVersion":"osac.openshift.io/v1alpha1","kind":"Subnet","metadata":{"name":"subnet-generated-first"}}]}`))
				case "/apis/kubevirt.io/v1/namespaces/subnet-generated-first/virtualmachines":
					items := `[]`
					if vmCount > 0 {
						items = `[{"apiVersion":"kubevirt.io/v1","kind":"VirtualMachine","metadata":{"name":"vm-one"}}]`
					}
					_, _ = fmt.Fprintf(writer, `{"apiVersion":"kubevirt.io/v1","kind":"VirtualMachineList","metadata":{"resourceVersion":"1"},"items":%s}`, items)
				default:
					http.NotFound(writer, request)
				}
			}))

			publicServer, err := NewSubnetsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetHubClientProvider(&stubHubClientProvider{
					config:    &rest.Config{Host: hubServer.URL},
					namespace: "networking",
				}).
				Build()
			Expect(err).NotTo(HaveOccurred())
			return publicServer, hubServer
		}

		createPublicSubnet := func(publicServer *SubnetsServer, name, cidr string) (*publicv1.Subnet, error) {
			response, err := publicServer.Create(ctx, publicv1.SubnetsCreateRequest_builder{
				Object: publicv1.Subnet_builder{
					Metadata: publicv1.Metadata_builder{Name: name}.Build(),
					Spec: publicv1.SubnetSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
						Ipv4Cidr:       new(cidr),
					}.Build(),
				}.Build(),
			}.Build())
			if err != nil {
				return nil, err
			}
			return response.GetObject(), nil
		}

		It("allows a VM-free second cudn_evpn Subnet through the public API", func() {
			publicServer, hubServer := setupCudnEvpnPublicServer(0)
			defer hubServer.Close()

			_, err := createPublicSubnet(publicServer, "first-subnet", "10.0.1.0/24")
			Expect(err).NotTo(HaveOccurred())
			second, err := createPublicSubnet(publicServer, "second-subnet", "10.0.2.0/24")
			Expect(err).NotTo(HaveOccurred())
			Expect(second.GetMetadata().GetName()).To(Equal("second-subnet"))
		})

		It("rejects a VM-present second cudn_evpn Subnet through the public API", func() {
			publicServer, hubServer := setupCudnEvpnPublicServer(1)
			defer hubServer.Close()

			first, err := createPublicSubnet(publicServer, "first-subnet", "10.0.1.0/24")
			Expect(err).NotTo(HaveOccurred())
			_, err = createPublicSubnet(publicServer, "second-subnet", "10.0.2.0/24")
			Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring(first.GetMetadata().GetName()))
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, publicv1.SubnetsCreateRequest_builder{
				Object: publicv1.Subnet_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: publicv1.SubnetSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
						Ipv4Cidr:       new("10.0.1.0/24"),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
		})

		It("List objects", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, publicv1.SubnetsCreateRequest_builder{
					Object: publicv1.Subnet_builder{
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: publicv1.SubnetSpec_builder{
							VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
							Ipv4Cidr:       new(fmt.Sprintf("10.0.%d.0/24", i+1)),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, publicv1.SubnetsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, publicv1.SubnetsCreateRequest_builder{
					Object: publicv1.Subnet_builder{
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: publicv1.SubnetSpec_builder{
							VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
							Ipv4Cidr:       new(fmt.Sprintf("10.0.%d.0/24", i+1)),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, publicv1.SubnetsListRequest_builder{
				Limit: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with offset", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, publicv1.SubnetsCreateRequest_builder{
					Object: publicv1.Subnet_builder{
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: publicv1.SubnetSpec_builder{
							VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
							Ipv4Cidr:       new(fmt.Sprintf("10.0.%d.0/24", i+1)),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, publicv1.SubnetsListRequest_builder{
				Offset: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", count-1))
		})

		It("List objects with filter", func() {
			// Create a few objects:
			const count = 10
			var objects []*publicv1.Subnet
			for i := range count {
				response, err := server.Create(ctx, publicv1.SubnetsCreateRequest_builder{
					Object: publicv1.Subnet_builder{
						Metadata: publicv1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Spec: publicv1.SubnetSpec_builder{
							VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
							Ipv4Cidr:       new(fmt.Sprintf("10.0.%d.0/24", i+1)),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				objects = append(objects, response.GetObject())
			}

			// List the objects:
			for _, object := range objects {
				response, err := server.List(ctx, publicv1.SubnetsListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", object.GetId())),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", 1))
				Expect(response.GetItems()[0].GetId()).To(Equal(object.GetId()))
			}
		})

		It("Get object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.SubnetsCreateRequest_builder{
				Object: publicv1.Subnet_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: publicv1.SubnetSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
						Ipv4Cidr:       new("10.0.1.0/24"),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get it:
			getResponse, err := server.Get(ctx, publicv1.SubnetsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Delete object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.SubnetsCreateRequest_builder{
				Object: publicv1.Subnet_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Spec: publicv1.SubnetSpec_builder{
						VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
						Ipv4Cidr:       new("10.0.1.0/24"),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Add a finalizer, as otherwise the object will be immediately deleted and archived and it
			// won't be possible to verify the deletion timestamp. This can't be done using the server
			// because this is a public object, and public objects don't have the finalizers field.
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(
				ctx,
				`update subnets set finalizers = '{"a"}' where id = $1`,
				object.GetId(),
			)
			Expect(err).ToNot(HaveOccurred())

			// Delete the object:
			_, err = server.Delete(ctx, publicv1.SubnetsDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get and verify:
			getResponse, err := server.Get(ctx, publicv1.SubnetsGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})
	})
})
