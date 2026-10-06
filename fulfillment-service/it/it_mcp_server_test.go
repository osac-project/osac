/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/service/start/mcpserver"
	"github.com/osac-project/osac/fulfillment-service/internal/network"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const defaultNetworkLabel = "osac.openshift.io/default"

var _ = Describe("MCP server", func() {
	var (
		ctx                            context.Context
		catalogItemsClient             privatev1.ComputeInstanceCatalogItemsClient
		computeInstanceTemplatesClient privatev1.ComputeInstanceTemplatesClient
		computeInstancesClient         publicv1.ComputeInstancesClient
		diskImagesClient               privatev1.DiskImagesClient
		instanceTypesClient            privatev1.InstanceTypesClient
		storageBackendsClient          privatev1.StorageBackendsClient
		storageTiersClient             privatev1.StorageTiersClient
		subnetsClient                  privatev1.SubnetsClient
		virtualNetworksClient          privatev1.VirtualNetworksClient
		networkClassesClient           privatev1.NetworkClassesClient
		securityGroupsClient           privatev1.SecurityGroupsClient

		catalogItemID          string
		computeInstanceID      string
		otherComputeInstanceID string
		templateID             string
		diskImageID            string
		instanceTypeID         string
		storageBackendID       string
		storageTierID          string
		subnetID               string
		virtualNetworkID       string
		networkClassID         string
		securityGroupID        string
		mcpGrpcConn            *grpc.ClientConn
		mcpHTTPServer          *httptest.Server
		mcpClient              *http.Client
	)

	BeforeEach(func() {
		ctx = context.Background()
		catalogItemID, computeInstanceID, otherComputeInstanceID = "", "", ""
		templateID, diskImageID, instanceTypeID = "", "", ""
		storageBackendID, storageTierID, subnetID = "", "", ""
		virtualNetworkID, networkClassID, securityGroupID = "", "", ""
		mcpGrpcConn, mcpHTTPServer, mcpClient = nil, nil, nil
		adminConn := tool.InternalView().AdminConn()
		catalogItemsClient = privatev1.NewComputeInstanceCatalogItemsClient(adminConn)
		computeInstanceTemplatesClient = privatev1.NewComputeInstanceTemplatesClient(adminConn)
		computeInstancesClient = publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
		diskImagesClient = privatev1.NewDiskImagesClient(adminConn)
		instanceTypesClient = privatev1.NewInstanceTypesClient(adminConn)
		storageBackendsClient = privatev1.NewStorageBackendsClient(adminConn)
		storageTiersClient = privatev1.NewStorageTiersClient(adminConn)
		subnetsClient = privatev1.NewSubnetsClient(adminConn)
		virtualNetworksClient = privatev1.NewVirtualNetworksClient(adminConn)
		networkClassesClient = privatev1.NewNetworkClassesClient(adminConn)
		securityGroupsClient = privatev1.NewSecurityGroupsClient(adminConn)

		storageBackendResponse, err := storageBackendsClient.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
			Object: privatev1.StorageBackend_builder{
				Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("mcp-storage-backend-%s", uuid.New()[24:32])}.Build(),
				Spec: privatev1.StorageBackendSpec_builder{
					Provider:    "test",
					Description: "Storage backend for MCP compute instance integration testing",
					Endpoint:    "https://test-backend.example.com",
					Credentials: privatev1.StorageBackendCredentials_builder{
						Username: "test-user",
						Password: "test-credential", //nolint:goconst // test-only fake provider credential
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		storageBackendID = storageBackendResponse.GetObject().GetId()
		DeferCleanup(func() {
			_, err := storageBackendsClient.Delete(ctx, privatev1.StorageBackendsDeleteRequest_builder{Id: storageBackendID}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		storageTierResponse, err := storageTiersClient.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
			Object: privatev1.StorageTier_builder{
				Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("mcp-storage-tier-%s", uuid.New()[24:32])}.Build(),
				Spec: privatev1.StorageTierSpec_builder{
					Description: "Storage tier for MCP compute instance integration testing",
					Protocol:    privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
					Backends: []*privatev1.BackendAssociation{
						privatev1.BackendAssociation_builder{BackendId: storageBackendID}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		storageTierID = storageTierResponse.GetObject().GetId()
		DeferCleanup(func() {
			_, err := storageTiersClient.Delete(ctx, privatev1.StorageTiersDeleteRequest_builder{Id: storageTierID}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		instanceTypeResponse, err := instanceTypesClient.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
			Object: privatev1.InstanceType_builder{
				Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("mcp-instance-type-%s", uuid.New()[24:32])}.Build(),
				Spec:     privatev1.InstanceTypeSpec_builder{Vcpus: 2, MemoryGib: 4}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		instanceTypeID = instanceTypeResponse.GetObject().GetId()
		DeferCleanup(func() {
			_, err := instanceTypesClient.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{Id: instanceTypeID}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		diskImageResponse, err := diskImagesClient.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
			Object: privatev1.DiskImage_builder{
				Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("mcp-disk-image-%s", uuid.New()[24:32])}.Build(),
				Spec: privatev1.DiskImageSpec_builder{
					SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
					SourceRef:     "quay.io/containerdisks/fedora:41",
					GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
					Architecture:  []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		diskImageID = diskImageResponse.GetObject().GetId()
		DeferCleanup(func() {
			_, err := diskImagesClient.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{Id: diskImageID}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		templateID = fmt.Sprintf("mcp-compute-template-%s", uuid.New())
		_, err = computeInstanceTemplatesClient.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
			Object: privatev1.ComputeInstanceTemplate_builder{
				Id:          templateID,
				Metadata:    privatev1.Metadata_builder{Name: fmt.Sprintf("mcp-compute-template-%s", uuid.New()[24:32])}.Build(),
				Title:       "MCP compute instance template",
				Description: "Template for MCP compute instance integration testing",
				SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
					InstanceType: privatev1.InstanceTypeReference_builder{Id: instanceTypeID}.Build(),
					DiskImage:    privatev1.DiskImageReference_builder{Id: diskImageID}.Build(),
					BootDisk: privatev1.ComputeInstanceDisk_builder{
						SizeGib:     proto.Int32(20),
						StorageTier: privatev1.StorageTierReference_builder{Id: storageTierID}.Build(),
					}.Build(),
					RunStrategy: privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, err := computeInstanceTemplatesClient.Delete(ctx, privatev1.ComputeInstanceTemplatesDeleteRequest_builder{Id: templateID}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		networkClassResponse, err := networkClassesClient.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Metadata:      privatev1.Metadata_builder{Name: fmt.Sprintf("mcp-network-class-%s", uuid.New()[24:32])}.Build(),
				Title:         "MCP network class",
				FabricManager: proto.String("netris"),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassID = networkClassResponse.GetObject().GetId()
		DeferCleanup(func() {
			_, err := networkClassesClient.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{Id: networkClassID}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		virtualNetworkID = fmt.Sprintf("mcp-virtual-network-%s", uuid.New())
		_, err = virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Id: virtualNetworkID,
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("mcp-virtual-network-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: networkClassID}.Build(),
					Region:       "us-east-1",
					Ipv4Cidr:     proto.String("10.200.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, err := virtualNetworksClient.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: virtualNetworkID}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
		Eventually(func(g Gomega) {
			response, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: virtualNetworkID}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())
		virtualNetworkResponse, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: virtualNetworkID}.Build())
		Expect(err).ToNot(HaveOccurred())
		virtualNetwork := virtualNetworkResponse.GetObject()
		virtualNetwork.SetStatus(privatev1.VirtualNetworkStatus_builder{State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY}.Build())
		_, err = virtualNetworksClient.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
			Object:     virtualNetwork,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		subnetID = fmt.Sprintf("mcp-subnet-%s", uuid.New())
		_, err = subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Id: subnetID,
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("mcp-subnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
					Labels: map[string]string{defaultNetworkLabel: "true"},
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
					Ipv4Cidr:       proto.String("10.200.1.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			subnetResponse, err := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
			Expect(err).ToNot(HaveOccurred())
			subnet := subnetResponse.GetObject()
			labels := subnet.GetMetadata().GetLabels()
			delete(labels, defaultNetworkLabel)
			subnet.GetMetadata().SetLabels(labels)
			_, err = subnetsClient.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
				Object:     subnet,
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = subnetsClient.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{Id: subnetID}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
		Eventually(func(g Gomega) {
			response, err := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())
		subnetResponse, err := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
		Expect(err).ToNot(HaveOccurred())
		subnet := subnetResponse.GetObject()
		subnet.SetStatus(privatev1.SubnetStatus_builder{State: privatev1.SubnetState_SUBNET_STATE_READY}.Build())
		_, err = subnetsClient.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
			Object:     subnet,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		securityGroupResponse, err := securityGroupsClient.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("mcp-security-group-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		securityGroupID = securityGroupResponse.GetObject().GetId()
		DeferCleanup(func() {
			_, err := securityGroupsClient.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{Id: securityGroupID}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
		Eventually(func(g Gomega) {
			response, err := securityGroupsClient.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: securityGroupID}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())
		securityGroupGetResponse, err := securityGroupsClient.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: securityGroupID}.Build())
		Expect(err).ToNot(HaveOccurred())
		securityGroup := securityGroupGetResponse.GetObject()
		securityGroup.SetStatus(privatev1.SecurityGroupStatus_builder{State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY}.Build())
		_, err = securityGroupsClient.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
			Object:     securityGroup,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		catalogItemID = fmt.Sprintf("mcp-compute-catalog-item-%s", uuid.New())
		_, err = catalogItemsClient.Create(ctx, privatev1.ComputeInstanceCatalogItemsCreateRequest_builder{
			Object: privatev1.ComputeInstanceCatalogItem_builder{
				Id:          catalogItemID,
				Metadata:    privatev1.Metadata_builder{Name: fmt.Sprintf("mcp-compute-catalog-item-%s", uuid.New()[24:32])}.Build(),
				Title:       "MCP compute instance",
				Description: "Catalog item for MCP compute instance integration testing",
				Published:   true,
				Template:    privatev1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
				Fields: privatev1.ComputeInstanceCatalogItemFields_builder{
					InstanceType: privatev1.InstanceTypeReferenceFieldPolicy_builder{
						Editable: privatev1.EditableInstanceTypeReferenceField_builder{}.Build(),
					}.Build(),
					BootDisk: privatev1.ComputeInstanceBootDiskFieldPolicies_builder{
						SizeGib: privatev1.Int32FieldPolicy_builder{
							Editable: privatev1.EditableInt32Field_builder{}.Build(),
						}.Build(),
						StorageTier: privatev1.StorageTierReferenceFieldPolicy_builder{
							Editable: privatev1.EditableStorageTierReferenceField_builder{}.Build(),
						}.Build(),
					}.Build(),
					NetworkAttachments: privatev1.ComputeNetworkAttachmentListFieldPolicy_builder{
						Editable: privatev1.EditableComputeNetworkAttachmentList_builder{}.Build(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, err := catalogItemsClient.Delete(ctx, privatev1.ComputeInstanceCatalogItemsDeleteRequest_builder{Id: catalogItemID}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		jwksCache, err := auth.NewJwksCache().
			SetLogger(logger).
			SetCaPool(tool.CaPool()).
			AddIssuers(fmt.Sprintf("https://%s/realms/osac", keycloakAddr)).
			Build()
		Expect(err).ToNot(HaveOccurred())
		jwtValidator, err := auth.NewJwtValidator().
			SetLogger(logger).
			SetJwksCache(jwksCache).
			SetExpirationLeeway(5 * time.Second).
			Build()
		Expect(err).ToNot(HaveOccurred())

		mcpGrpcConn, err = network.NewGrpcClient().
			SetLogger(logger).
			SetCaPool(tool.CaPool()).
			SetAddress(externalServiceAddr).
			SetUserAgent("fulfillment-mcp-server-it").
			Build()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(mcpGrpcConn.Close()).To(Succeed()) })
		handler, err := mcpserver.NewHandler(mcpserver.NewServerDeps(mcpGrpcConn), jwtValidator, "", "")
		Expect(err).ToNot(HaveOccurred())
		mcpHTTPServer = httptest.NewServer(handler)
		DeferCleanup(mcpHTTPServer.Close)
		mcpClient = &http.Client{
			Transport: ghttp.RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
				token, err := tool.UserTokenSource().Token(request.Context())
				if err != nil {
					return nil, err
				}
				request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.Access))
				return http.DefaultTransport.RoundTrip(request)
			}),
		}
	})

	It("drives ComputeInstance discovery, creation, inspection, and deletion over real HTTP with bearer token forwarding", func() {
		transport := &mcp.StreamableClientTransport{Endpoint: mcpHTTPServer.URL, HTTPClient: mcpClient}
		client := mcp.NewClient(&mcp.Implementation{Name: "it-mcp-client", Version: "0.1.0"}, nil)
		session, err := client.Connect(ctx, transport, nil)
		Expect(err).ToNot(HaveOccurred())
		defer func() { Expect(session.Close()).To(Succeed()) }()
		listedTools, err := session.ListTools(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(listedTools.Tools).To(HaveLen(4))
		Expect([]string{
			listedTools.Tools[0].Name, listedTools.Tools[1].Name,
			listedTools.Tools[2].Name, listedTools.Tools[3].Name,
		}).To(ConsistOf("list_resources", "get_resource", "create_compute_instance", "delete_compute_instance"))

		assertDiscoverable := func(resourceType mcpserver.ResourceType, id string) map[string]any {
			listOutput, err := callMCPTool[mcpserver.ListResourcesOutput](ctx, session, "list_resources", mcpserver.ListResourcesInput{
				ResourceType: resourceType,
				Filter:       fmt.Sprintf("this.id == %q", id),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(listOutput.Items).To(HaveLen(1))
			Expect(listOutput.Items[0].ID).To(Equal(id))

			resource, err := callMCPTool[mcpserver.GetResourceOutput](ctx, session, "get_resource", mcpserver.GetResourceInput{
				ResourceType: resourceType,
				ID:           id,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(resource.Resource).To(HaveKeyWithValue("id", id))
			return resource.Resource
		}

		catalogItem := assertDiscoverable(mcpserver.ResourceTypeComputeInstanceCatalogItem, catalogItemID)
		Expect(catalogItem).To(HaveKeyWithValue("title", "MCP compute instance"))
		template := assertDiscoverable(mcpserver.ResourceTypeComputeInstanceTemplate, templateID)
		specDefaults, ok := template["specDefaults"].(map[string]any)
		Expect(ok).To(BeTrue())
		bootDisk, ok := specDefaults["bootDisk"].(map[string]any)
		Expect(ok).To(BeTrue())
		storageTier, ok := bootDisk["storageTier"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(storageTier).To(HaveKeyWithValue("id", storageTierID))
		instanceType := assertDiscoverable(mcpserver.ResourceTypeInstanceType, instanceTypeID)
		assertDiscoverable(mcpserver.ResourceTypeDiskImage, diskImageID)
		storageTierResource := assertDiscoverable(mcpserver.ResourceTypeStorageTier, storageTierID)
		assertDiscoverable(mcpserver.ResourceTypeVirtualNetwork, virtualNetworkID)
		subnet := assertDiscoverable(mcpserver.ResourceTypeSubnet, subnetID)
		securityGroup := assertDiscoverable(mcpserver.ResourceTypeSecurityGroup, securityGroupID)

		catalogItemResourceID, ok := catalogItem["id"].(string)
		Expect(ok).To(BeTrue())
		instanceTypeResourceID, ok := instanceType["id"].(string)
		Expect(ok).To(BeTrue())
		storageTierResourceID, ok := storageTierResource["id"].(string)
		Expect(ok).To(BeTrue())
		subnetResourceID, ok := subnet["id"].(string)
		Expect(ok).To(BeTrue())
		securityGroupResourceID, ok := securityGroup["id"].(string)
		Expect(ok).To(BeTrue())

		bootDiskSize := int32(30)
		created, err := callMCPTool[mcpserver.CreateComputeInstanceOutput](
			ctx, session, "create_compute_instance", mcpserver.CreateComputeInstanceInput{
				Name:           fmt.Sprintf("mcp-compute-instance-%s", uuid.New()[24:32]),
				CatalogItem:    catalogItemResourceID,
				InstanceTypeID: instanceTypeResourceID,
				BootDisk: &mcpserver.ComputeInstanceBootDiskInput{
					SizeGiB:       &bootDiskSize,
					StorageTierID: storageTierResourceID,
				},
				NetworkAttachments: []mcpserver.ComputeInstanceNetworkAttachmentInput{{
					SubnetID:         subnetResourceID,
					SecurityGroupIDs: []string{securityGroupResourceID},
				}},
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(created.ID).ToNot(BeEmpty())
		computeInstanceID = created.ID
		DeferCleanup(func() {
			if computeInstanceID == "" {
				return
			}
			_, err := computeInstancesClient.Delete(ctx, publicv1.ComputeInstancesDeleteRequest_builder{Id: computeInstanceID}.Build())
			if status.Code(err) != codes.NotFound {
				Expect(err).ToNot(HaveOccurred())
			}
			waitForMCPComputeInstanceDeletion(ctx, computeInstanceID)
		})

		createdList, err := callMCPTool[mcpserver.ListResourcesOutput](ctx, session, "list_resources", mcpserver.ListResourcesInput{
			ResourceType: mcpserver.ResourceTypeComputeInstance,
			Filter:       fmt.Sprintf("this.id == %q", computeInstanceID),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(createdList.Items).To(ConsistOf(HaveField("ID", computeInstanceID)))

		otherNetwork := createCatalogItemNetworkInClassFixture(ctx, "development", "", networkClassID)
		otherTokenSource, err := tool.makeKeycloakTokenSource(ctx, "ben", usersPassword)
		Expect(err).ToNot(HaveOccurred())
		otherHTTPClient := &http.Client{Transport: ghttp.RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
			token, e := otherTokenSource.Token(request.Context())
			if e != nil {
				return nil, e
			}
			request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.Access))
			return http.DefaultTransport.RoundTrip(request)
		})}
		otherClient := mcp.NewClient(&mcp.Implementation{Name: "it-mcp-other-tenant", Version: "0.1.0"}, nil)
		otherSession, err := otherClient.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint: mcpHTTPServer.URL, HTTPClient: otherHTTPClient,
		}, nil)
		Expect(err).ToNot(HaveOccurred())
		defer func() { Expect(otherSession.Close()).To(Succeed()) }()

		otherCreated, err := callMCPTool[mcpserver.CreateComputeInstanceOutput](
			ctx, otherSession, "create_compute_instance", mcpserver.CreateComputeInstanceInput{
				Name:        fmt.Sprintf("mcp-other-compute-%s", uuid.New()[24:32]),
				CatalogItem: catalogItemID,
				NetworkAttachments: []mcpserver.ComputeInstanceNetworkAttachmentInput{{
					SubnetID: otherNetwork.subnetID, SecurityGroupIDs: []string{otherNetwork.securityGroupID},
				}},
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(otherCreated.ID).ToNot(BeEmpty())
		otherComputeInstanceID = otherCreated.ID
		DeferCleanup(func() {
			_, err := privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn()).Delete(
				ctx, privatev1.ComputeInstancesDeleteRequest_builder{Id: otherComputeInstanceID}.Build(),
			)
			if status.Code(err) != codes.NotFound {
				Expect(err).ToNot(HaveOccurred())
			}
			waitForMCPComputeInstanceDeletion(ctx, otherComputeInstanceID)
		})

		hiddenList, err := callMCPTool[mcpserver.ListResourcesOutput](ctx, session, "list_resources", mcpserver.ListResourcesInput{
			ResourceType: mcpserver.ResourceTypeComputeInstance,
			Filter:       fmt.Sprintf("this.id == %q", otherComputeInstanceID),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(hiddenList.Items).To(BeEmpty())

		_, err = callMCPTool[mcpserver.DeleteComputeInstanceOutput](
			ctx, otherSession, "delete_compute_instance", mcpserver.DeleteComputeInstanceInput{ID: computeInstanceID},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tool \"delete_compute_instance\" returned an error result"))
		Expect(err.Error()).To(MatchRegexp(`(?i)(not.?found|permission.?denied)`))
		stillPresent, err := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: computeInstanceID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(stillPresent.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())

		invalidName := fmt.Sprintf("mcp-invalid-compute-%s", uuid.New()[24:32])
		_, err = callMCPTool[mcpserver.CreateComputeInstanceOutput](
			ctx, session, "create_compute_instance", mcpserver.CreateComputeInstanceInput{
				Name: invalidName, CatalogItem: catalogItemID,
				NetworkAttachments: []mcpserver.ComputeInstanceNetworkAttachmentInput{
					{SubnetID: subnetID}, {SubnetID: otherNetwork.subnetID},
				},
			},
		)
		Expect(err).To(MatchError(ContainSubstring("network_attachments must contain at most one attachment")))
		deniedName := fmt.Sprintf("mcp-denied-compute-%s", uuid.New()[24:32])
		_, err = callMCPTool[mcpserver.CreateComputeInstanceOutput](
			ctx, session, "create_compute_instance", mcpserver.CreateComputeInstanceInput{
				Name: deniedName, CatalogItem: catalogItemID,
				NetworkAttachments: []mcpserver.ComputeInstanceNetworkAttachmentInput{{
					SubnetID: otherNetwork.subnetID, SecurityGroupIDs: []string{otherNetwork.securityGroupID},
				}},
			},
		)
		Expect(err).To(MatchError(And(
			ContainSubstring("network_attachments[0]: subnet"),
			ContainSubstring("does not exist"),
		)))
		adminInstances := privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn())
		for _, name := range []string{invalidName, deniedName} {
			invalidList, e := adminInstances.List(ctx, privatev1.ComputeInstancesListRequest_builder{
				Filter: proto.String(fmt.Sprintf("this.metadata.name == %q", name)),
			}.Build())
			Expect(e).ToNot(HaveOccurred())
			Expect(invalidList.GetItems()).To(BeEmpty())
		}

		_, err = callMCPTool[mcpserver.DeleteComputeInstanceOutput](
			ctx, session, "delete_compute_instance", mcpserver.DeleteComputeInstanceInput{},
		)
		Expect(err).To(HaveOccurred())
		stillPresent, err = computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: computeInstanceID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(stillPresent.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())

		instance, err := callMCPTool[mcpserver.GetResourceOutput](ctx, session, "get_resource", mcpserver.GetResourceInput{
			ResourceType: mcpserver.ResourceTypeComputeInstance,
			ID:           computeInstanceID,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(instance.Resource).To(HaveKeyWithValue("id", computeInstanceID))

		usersClient := privatev1.NewUsersClient(tool.InternalView().AdminConn())
		usersResponse, err := usersClient.List(ctx, privatev1.UsersListRequest_builder{
			Filter: proto.String(fmt.Sprintf("this.spec.username == %q", userUsername)),
			Limit:  proto.Int32(1),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(usersResponse.GetSize()).To(Equal(int32(1)))
		createdInstance, err := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: computeInstanceID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(createdInstance.GetObject().GetMetadata().GetCreator()).To(Equal(usersResponse.GetItems()[0].GetId()))
		Expect(createdInstance.GetObject().GetMetadata().GetTenant()).To(Equal(usersGroup))
		Expect(createdInstance.GetObject().GetSpec().GetInstanceType().GetId()).To(Equal(instanceTypeID))
		Expect(createdInstance.GetObject().GetSpec().GetBootDisk().GetSizeGib()).To(Equal(bootDiskSize))
		Expect(createdInstance.GetObject().GetSpec().GetBootDisk().GetStorageTier().GetId()).To(Equal(storageTierID))
		Expect(createdInstance.GetObject().GetSpec().GetNetworkAttachments()).To(HaveLen(1))
		Expect(createdInstance.GetObject().GetSpec().GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal(subnetID))
		Expect(createdInstance.GetObject().GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()).To(HaveLen(1))
		Expect(createdInstance.GetObject().GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()[0].GetId()).To(Equal(securityGroupID))

		deleted, err := callMCPTool[mcpserver.DeleteComputeInstanceOutput](
			ctx, session, "delete_compute_instance", mcpserver.DeleteComputeInstanceInput{ID: computeInstanceID},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(deleted.ID).To(Equal(computeInstanceID))
		waitForMCPComputeInstanceDeletion(ctx, computeInstanceID)
		computeInstanceID = ""
	})
})

func waitForMCPComputeInstanceDeletion(ctx context.Context, id string) {
	GinkgoHelper()
	client := privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn())
	Eventually(func() bool {
		_, err := client.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: id}.Build())
		if status.Code(err) == codes.NotFound {
			return true
		}
		Expect(err).ToNot(HaveOccurred())
		_, err = client.Signal(ctx, privatev1.ComputeInstancesSignalRequest_builder{Id: id}.Build())
		if status.Code(err) == codes.NotFound {
			return true
		}
		Expect(err).ToNot(HaveOccurred())
		return false
	}, 5*time.Minute, 2*time.Second).Should(BeTrue(), "compute instance was not removed before its network")
}

func callMCPTool[Out any](ctx context.Context, session *mcp.ClientSession, name string, arguments any) (Out, error) {
	var output Out
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return output, fmt.Errorf("failed to call tool %q: %w", name, err)
	}
	if result.IsError {
		return output, fmt.Errorf("tool %q returned an error result: %+v", name, result.Content)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return output, fmt.Errorf("failed to marshal structured content from tool %q: %w", name, err)
	}
	if err := json.Unmarshal(raw, &output); err != nil {
		return output, fmt.Errorf("failed to unmarshal structured content from tool %q: %w", name, err)
	}
	return output, nil
}
