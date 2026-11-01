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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/service/start/mcpserver"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func keycloakIssuer(ctx context.Context) string {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("https://%s/realms/osac/.well-known/openid-configuration", keycloakAddr),
		nil,
	)
	Expect(err).ToNot(HaveOccurred())
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: tool.CaPool().Pool(), MinVersion: tls.VersionTLS12,
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	response, err := client.Do(request)
	Expect(err).ToNot(HaveOccurred())
	defer response.Body.Close()
	Expect(response.StatusCode).To(Equal(http.StatusOK))
	var metadata struct {
		Issuer string `json:"issuer"`
	}
	Expect(json.NewDecoder(response.Body).Decode(&metadata)).To(Succeed())
	Expect(metadata.Issuer).ToNot(BeEmpty())
	return metadata.Issuer
}

var _ = Describe("MCP server", func() {
	var (
		ctx                            context.Context
		catalogItemsClient             privatev1.ComputeInstanceCatalogItemsClient
		computeInstanceTemplatesClient privatev1.ComputeInstanceTemplatesClient
		computeInstancesClient         publicv1.ComputeInstancesClient
		diskImagesClient               privatev1.DiskImagesClient
		instanceTypesClient            privatev1.InstanceTypesClient
		networkClassesClient           privatev1.NetworkClassesClient
		storageBackendsClient          privatev1.StorageBackendsClient
		storageTiersClient             privatev1.StorageTiersClient
		subnetsClient                  privatev1.SubnetsClient
		virtualNetworksClient          privatev1.VirtualNetworksClient
		securityGroupsClient           privatev1.SecurityGroupsClient

		catalogItemID            string
		computeInstanceID        string
		otherComputeInstanceID   string
		templateID               string
		diskImageID              string
		instanceTypeID           string
		compatibleInstanceTypeID string
		deprecatedInstanceTypeID string
		obsoleteInstanceTypeID   string
		storageBackendID         string
		storageTierID            string
		subnetID                 string
		virtualNetworkID         string
		networkClassID           string
		securityGroupID          string
		projectID                string
		projectName              string
		mcpHTTPServer            *httptest.Server
		mcpClient                *http.Client
	)

	BeforeEach(func() {
		ctx = context.Background()
		catalogItemID, computeInstanceID, otherComputeInstanceID = "", "", ""
		templateID, diskImageID, instanceTypeID = "", "", ""
		compatibleInstanceTypeID, deprecatedInstanceTypeID, obsoleteInstanceTypeID = "", "", ""
		storageBackendID, storageTierID, subnetID = "", "", ""
		virtualNetworkID, networkClassID, securityGroupID = "", "", ""
		projectID, projectName = "", ""
		mcpHTTPServer, mcpClient = nil, nil
		adminConn := tool.InternalView().AdminConn()
		catalogItemsClient = privatev1.NewComputeInstanceCatalogItemsClient(adminConn)
		computeInstanceTemplatesClient = privatev1.NewComputeInstanceTemplatesClient(adminConn)
		diskImagesClient = privatev1.NewDiskImagesClient(adminConn)
		instanceTypesClient = privatev1.NewInstanceTypesClient(adminConn)
		networkClassesClient = privatev1.NewNetworkClassesClient(adminConn)
		storageBackendsClient = privatev1.NewStorageBackendsClient(adminConn)
		storageTiersClient = privatev1.NewStorageTiersClient(adminConn)
		subnetsClient = privatev1.NewSubnetsClient(adminConn)
		virtualNetworksClient = privatev1.NewVirtualNetworksClient(adminConn)
		securityGroupsClient = privatev1.NewSecurityGroupsClient(adminConn)

		revokeTenantAdmin := grantMCPDiscoveryTenantAdmin(ctx, userUsername)
		projectConn, err := tool.MakeUserConnection(ctx, userUsername, usersPassword)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = projectConn.Close() })
		project, _ := createMCPProjectFixture(
			ctx,
			publicv1.NewProjectsClient(projectConn),
			userUsername,
			usersGroup,
			"compute",
			"MCP compute instance",
			"Project for MCP compute integration",
		)
		revokeTenantAdmin(ctx)
		projectID = project.GetId()
		projectName = project.GetMetadata().GetName()

		storageBackendResponse, err := storageBackendsClient.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
			Object: privatev1.StorageBackend_builder{
				Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("mcp-storage-backend-%s", uuid.New()[24:32])}.Build(),
				Spec: privatev1.StorageBackendSpec_builder{
					Provider:    "test",
					Description: "Storage backend for MCP compute instance integration testing",
					Endpoint:    "https://test-backend.example.com",
					Credentials: privatev1.StorageBackendCredentials_builder{
						Username: "test-user",
						Password: uuid.New(),
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

		createInstanceType := func(
			state privatev1.InstanceTypeState, deprecation *privatev1.InstanceTypeDeprecation,
		) string {
			instanceTypeResponse, err := instanceTypesClient.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
				Object: privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("mcp-instance-type-%s", uuid.New()[24:32])}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Vcpus: 2, MemoryGib: 4,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			instanceTypeID := instanceTypeResponse.GetObject().GetId()
			if state != privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE {
				paths := []string{"spec.state"}
				spec := privatev1.InstanceTypeSpec_builder{State: state}
				if deprecation != nil {
					paths = append(paths, "spec.deprecation")
					spec.Deprecation = deprecation
				}
				_, err = instanceTypesClient.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id:   instanceTypeID,
						Spec: spec.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}
			DeferCleanup(func() {
				_, err := instanceTypesClient.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{
					Id: instanceTypeID,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
			return instanceTypeID
		}
		instanceTypeID = createInstanceType(privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE, nil)
		compatibleInstanceTypeID = createInstanceType(privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE, nil)
		deprecatedInstanceTypeID = createInstanceType(
			privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
			privatev1.InstanceTypeDeprecation_builder{
				ObsolescenceTimestamp: timestamppb.New(time.Now().Add(time.Hour)),
				Replacement: privatev1.InstanceTypeLocalReference_builder{
					Id: compatibleInstanceTypeID,
				}.Build(),
			}.Build(),
		)
		obsoleteInstanceTypeID = createInstanceType(
			privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE, nil,
		)

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

		templateID = newTemplateID("mcp_compute_template")
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

		networkClassResponse, err := sharedNetworkClass(ctx, networkClassesClient)
		Expect(err).ToNot(HaveOccurred())
		networkClassID = networkClassResponse.GetObject().GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			if securityGroupID != "" {
				Expect(cleanupSecurityGroupFixture(cleanupCtx, securityGroupsClient, securityGroupID)).To(Succeed())
			}
			if subnetID != "" {
				Expect(cleanupSubnetFixture(cleanupCtx, subnetsClient, subnetID)).To(Succeed())
			}
			if virtualNetworkID != "" {
				Expect(cleanupVirtualNetworkFixture(cleanupCtx, virtualNetworksClient, virtualNetworkID)).To(Succeed())
			}
			Expect(cleanupNetworkClassFixture(cleanupCtx, networkClassesClient, networkClassID)).To(Succeed())
		})
		waitForNetworkClassReady(ctx, networkClassesClient, networkClassID)

		virtualNetworkID = fmt.Sprintf("mcp-virtual-network-%s", uuid.New())
		_, err = virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Id: virtualNetworkID,
				Metadata: privatev1.Metadata_builder{
					Name:    fmt.Sprintf("mcp-virtual-network-%s", uuid.New()[24:32]),
					Tenant:  usersGroup,
					Project: projectName,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					Ipv4Cidr: proto.String("10.200.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Eventually(func(g Gomega) {
			response, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: virtualNetworkID}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())
		setComputeInstanceFixtureVirtualNetworkReady(ctx, virtualNetworksClient, virtualNetworkID)

		subnetID = fmt.Sprintf("mcp-subnet-%s", uuid.New())
		_, err = subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Id: subnetID,
				Metadata: privatev1.Metadata_builder{
					Name:    fmt.Sprintf("mcp-subnet-%s", uuid.New()[24:32]),
					Tenant:  usersGroup,
					Project: projectName,
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
					Ipv4Cidr:       proto.String("10.200.1.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Eventually(func(g Gomega) {
			response, err := subnetsClient.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())
		setSubnetReady(ctx, subnetsClient, subnetID)

		securityGroupResponse, err := securityGroupsClient.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{
					Name:    fmt.Sprintf("mcp-security-group-%s", uuid.New()[24:32]),
					Tenant:  usersGroup,
					Project: projectName,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		securityGroupID = securityGroupResponse.GetObject().GetId()
		Eventually(func(g Gomega) {
			response, err := securityGroupsClient.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: securityGroupID}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())
		setSecurityGroupReady(ctx, securityGroupsClient, securityGroupID)

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
					RunStrategy: privatev1.ComputeInstanceRunStrategyFieldPolicy_builder{
						Editable: privatev1.EditableComputeInstanceRunStrategyField_builder{}.Build(),
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

		mcpTokenSource, err := tool.makeKeycloakTokenSource(ctx, userUsername, usersPassword)
		Expect(err).ToNot(HaveOccurred())
		mcpConn, err := tool.makeGrpcConn(externalServiceAddr, mcpTokenSource)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = mcpConn.Close() })
		computeInstancesClient = publicv1.NewComputeInstancesClient(mcpConn)
		mcpHTTPServer = startInProcessMCPServer(ctx, "fulfillment-mcp-server-it")
		mcpClient = &http.Client{
			Transport: ghttp.RoundTripperFunc(func(request *http.Request) (*http.Response, error) {
				token, err := mcpTokenSource.Token(request.Context())
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
		Expect(listedTools.Tools).To(HaveLen(len(expectedMCPServerToolNames)))
		Expect(mcpToolNames(listedTools.Tools)).To(ConsistOf(expectedMCPServerToolNames))

		instancesBefore, err := computeInstancesClient.List(ctx, publicv1.ComputeInstancesListRequest_builder{
			Limit: proto.Int32(1),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		connection, err := callMCPTool[mcpserver.CheckConnectionOutput](
			ctx, session, "check_connection", mcpserver.CheckConnectionInput{},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(connection.Server.Name).To(Equal("osac-deployment-mcp"))
		Expect(connection.Server.Version).ToNot(BeEmpty())
		Expect(connection.Caller.Subject).ToNot(BeEmpty())
		Expect(connection.Caller.Tenants).To(ContainElement(usersGroup))
		Expect(connection.ResourceFamilies).To(HaveLen(11))
		Expect(connection.Tools).To(ConsistOf(expectedMCPServerToolNames))
		Expect(connection.Permissions).To(ConsistOf(
			mcpserver.CheckConnectionPermission{
				Tenant: usersGroup, Service: "osac.public.v1.ComputeInstances", Method: "List", Allowed: true,
			},
			mcpserver.CheckConnectionPermission{
				Tenant: usersGroup, Service: "osac.public.v1.ComputeInstances", Method: "Create", Allowed: true,
			},
			mcpserver.CheckConnectionPermission{
				Tenant: usersGroup, Service: "osac.public.v1.ComputeInstances", Method: "Update", Allowed: true,
			},
		))
		encodedConnection, err := json.Marshal(connection)
		Expect(err).ToNot(HaveOccurred())
		Expect(encodedConnection).ToNot(MatchRegexp(`(?i)"(status|conditions?)"`))
		instancesAfter, err := computeInstancesClient.List(ctx, publicv1.ComputeInstancesListRequest_builder{
			Limit: proto.Int32(1),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(instancesAfter.GetTotal()).To(Equal(instancesBefore.GetTotal()))

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
			Expect(resource.Resource).To(HaveKey("name"))
			Expect(resource.Resource).To(HaveKey("version"))
			Expect(resource.Resource).ToNot(HaveKey("title"))
			Expect(resource.Resource).ToNot(HaveKey("description"))
			Expect(resource.Resource).ToNot(HaveKey("annotations"))
			Expect(resource.Resource).ToNot(HaveKey("status"))
			Expect(resource.Resource).ToNot(HaveKey("conditions"))
			Expect(resource.Resource).ToNot(HaveKey("specDefaults"))
			Expect(resource.Resource).ToNot(HaveKey("spec"))
			Expect(resource.Resource).ToNot(HaveKey("fields"))
			Expect(resource.Resource).ToNot(HaveKey("userData"))
			return resource.Resource
		}

		catalogItem := assertDiscoverable(mcpserver.ResourceTypeComputeInstanceCatalogItem, catalogItemID)
		assertDiscoverable(mcpserver.ResourceTypeComputeInstanceTemplate, templateID)
		instanceType := assertDiscoverable(mcpserver.ResourceTypeInstanceType, instanceTypeID)
		assertDiscoverable(mcpserver.ResourceTypeDiskImage, diskImageID)
		projectList, err := callMCPTool[mcpserver.ListResourcesOutput](ctx, session, "list_resources", mcpserver.ListResourcesInput{
			ResourceType: mcpserver.ResourceTypeProject,
			Filter:       fmt.Sprintf("this.id == %q", projectID),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(projectList.Items).To(HaveLen(1))
		Expect(projectList.Items[0].ID).To(Equal(projectID))
		storageTierResource := assertDiscoverable(mcpserver.ResourceTypeStorageTier, storageTierID)
		assertDiscoverable(mcpserver.ResourceTypeVirtualNetwork, virtualNetworkID)
		subnet := assertDiscoverable(mcpserver.ResourceTypeSubnet, subnetID)
		securityGroup := assertDiscoverable(mcpserver.ResourceTypeSecurityGroup, securityGroupID)

		catalogItemResourceID, ok := catalogItem["id"].(string)
		Expect(ok).To(BeTrue())
		instanceTypeResourceID, ok := instanceType["id"].(string)
		Expect(ok).To(BeTrue())
		projectResourceID := projectList.Items[0].ID
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
				DiskImageID:    diskImageID,
				ProjectID:      projectResourceID,
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
		Expect(created.Version).To(BeNumerically(">=", 0))
		Expect(created.State).ToNot(BeEmpty())
		computeInstanceID = created.ID
		DeferCleanup(func() {
			if computeInstanceID == "" {
				return
			}
			adminInstances := privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn())
			_, err := adminInstances.Delete(ctx, privatev1.ComputeInstancesDeleteRequest_builder{Id: computeInstanceID}.Build())
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

		Eventually(func(g Gomega) {
			response, getErr := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: computeInstanceID,
			}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(
				publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
			))
		}, time.Minute, 100*time.Millisecond).Should(Succeed())
		currentResponse, err := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
			Id: computeInstanceID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		currentVersion := currentResponse.GetObject().GetMetadata().GetVersion()
		staleVersion := currentVersion + 1

		_, err = callMCPTool[mcpserver.UpdateComputeInstanceOutput](
			ctx, session, "update_compute_instance", mcpserver.UpdateComputeInstanceInput{
				ID: computeInstanceID, Version: staleVersion, RunStrategy: "HALTED",
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"category":"conflict"`))
		Expect(err.Error()).To(ContainSubstring(`"grpc_code":"Aborted"`))

		var updated mcpserver.UpdateComputeInstanceOutput
		Eventually(func(g Gomega) {
			retryResponse, err := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: computeInstanceID,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			retryVersion := retryResponse.GetObject().GetMetadata().GetVersion()

			output, updateErr := callMCPTool[mcpserver.UpdateComputeInstanceOutput](
				ctx, session, "update_compute_instance", mcpserver.UpdateComputeInstanceInput{
					ID: computeInstanceID, Version: retryVersion, RunStrategy: "HALTED",
				},
			)
			if updateErr != nil {
				g.Expect(updateErr.Error()).To(ContainSubstring("The resource version is stale"))
				g.Expect(updateErr).ToNot(HaveOccurred())
				return
			}
			updated = output
			g.Expect(updated.ID).To(Equal(computeInstanceID))
			g.Expect(updated.Version).To(BeNumerically(">", retryVersion))
		}, time.Minute, 100*time.Millisecond).Should(Succeed())

		updateInstanceTypeWithRetry := func(instanceTypeID string) mcpserver.UpdateComputeInstanceOutput {
			var output mcpserver.UpdateComputeInstanceOutput
			Eventually(func(g Gomega) {
				response, getErr := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
					Id: computeInstanceID,
				}.Build())
				g.Expect(getErr).ToNot(HaveOccurred())
				version := response.GetObject().GetMetadata().GetVersion()

				update, updateErr := callMCPTool[mcpserver.UpdateComputeInstanceOutput](
					ctx, session, "update_compute_instance", mcpserver.UpdateComputeInstanceInput{
						ID: computeInstanceID, Version: version, InstanceTypeID: instanceTypeID,
					},
				)
				if updateErr != nil {
					g.Expect(updateErr.Error()).To(ContainSubstring("The resource version is stale"))
					g.Expect(updateErr).ToNot(HaveOccurred())
					return
				}
				output = update
				g.Expect(output.Version).To(BeNumerically(">", version))
			}, time.Minute, 100*time.Millisecond).Should(Succeed())
			return output
		}

		compatibleUpdate := updateInstanceTypeWithRetry(compatibleInstanceTypeID)
		Expect(compatibleUpdate.ID).To(Equal(computeInstanceID))
		Expect(compatibleUpdate.Warnings).To(BeEmpty())
		compatibleResponse, err := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
			Id: computeInstanceID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(compatibleResponse.GetObject().GetSpec().GetInstanceType().GetId()).To(Equal(compatibleInstanceTypeID))
		Expect(compatibleResponse.GetObject().GetMetadata().GetVersion()).To(BeNumerically(">=", compatibleUpdate.Version))

		deprecatedUpdate := updateInstanceTypeWithRetry(deprecatedInstanceTypeID)
		Expect(deprecatedUpdate.Warnings).To(HaveLen(1))
		Expect(deprecatedUpdate.Warnings[0]).To(And(
			ContainSubstring("is deprecated"),
			ContainSubstring("will become obsolete"),
			ContainSubstring("Consider using"),
		))
		encodedWarnings, err := json.Marshal(deprecatedUpdate)
		Expect(err).ToNot(HaveOccurred())
		Expect(encodedWarnings).To(ContainSubstring(`"warnings":`))
		Expect(encodedWarnings).To(ContainSubstring("is deprecated"))

		Eventually(func(g Gomega) {
			response, getErr := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
				Id: computeInstanceID,
			}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			version := response.GetObject().GetMetadata().GetVersion()

			_, updateErr := callMCPTool[mcpserver.UpdateComputeInstanceOutput](
				ctx, session, "update_compute_instance", mcpserver.UpdateComputeInstanceInput{
					ID: computeInstanceID, Version: version, InstanceTypeID: obsoleteInstanceTypeID,
				},
			)
			g.Expect(updateErr).To(HaveOccurred())
			g.Expect(updateErr.Error()).To(ContainSubstring(`"category":"conflict"`))
			g.Expect(updateErr.Error()).To(ContainSubstring(`"grpc_code":"FailedPrecondition"`))
			g.Expect(updateErr.Error()).To(ContainSubstring("same GPU configuration"))
		}, time.Minute, 100*time.Millisecond).Should(Succeed())
		rejectedResponse, err := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{
			Id: computeInstanceID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(rejectedResponse.GetObject().GetSpec().GetInstanceType().GetId()).To(Equal(deprecatedInstanceTypeID))

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
			adminInstances := privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn())
			_, err := adminInstances.Delete(
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
		Expect(err.Error()).To(ContainSubstring(`"grpc_code":"NotFound"`))
		stillPresent, err := computeInstancesClient.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: computeInstanceID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(stillPresent.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeFalse())

		missingPrerequisiteName := fmt.Sprintf("mcp-missing-project-compute-%s", uuid.New()[24:32])
		_, err = callMCPTool[mcpserver.CreateComputeInstanceOutput](
			ctx, session, "create_compute_instance", mcpserver.CreateComputeInstanceInput{
				Name: missingPrerequisiteName, CatalogItem: catalogItemID, ProjectID: uuid.New(),
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"category":"not_found"`))
		Expect(err.Error()).To(ContainSubstring(`"grpc_code":"NotFound"`))

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
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`"category":"not_found"`))
		Expect(err.Error()).To(ContainSubstring(`"grpc_code":"NotFound"`))
		deniedEnvelope := regexp.MustCompile(`"category":"([^"]+)".*"grpc_code":"([^"]+)"`).FindStringSubmatch(err.Error())
		Expect(deniedEnvelope).To(HaveLen(3))
		Expect(deniedEnvelope[1]).To(Equal("not_found"))
		Expect(deniedEnvelope[2]).To(Equal("NotFound"))
		adminInstances := privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn())
		for _, name := range []string{missingPrerequisiteName, invalidName, deniedName} {
			invalidList, e := adminInstances.List(ctx, privatev1.ComputeInstancesListRequest_builder{
				Filter: proto.String(fmt.Sprintf("this.metadata.name == %q", name)),
			}.Build())
			Expect(e).ToNot(HaveOccurred())
			Expect(invalidList.GetItems()).To(BeEmpty())
		}

		dispatchName := fmt.Sprintf("mcp-dispatch-compute-%s", uuid.New()[24:32])
		_, err = callMCPTool[mcpserver.CreateComputeInstanceOutput](
			ctx, session, "create_compute_instance", map[string]any{
				"service": "osac.public.v1.ComputeInstances",
				"method":  "Delete",
				"payload": map[string]any{"id": computeInstanceID},
				"name":    dispatchName,
			},
		)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unexpected additional properties"))
		dispatchList, err := adminInstances.List(ctx, privatev1.ComputeInstancesListRequest_builder{
			Filter: proto.String(fmt.Sprintf("this.metadata.name == %q", dispatchName)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(dispatchList.GetItems()).To(BeEmpty())

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
		Expect(createdInstance.GetObject().GetMetadata().GetProject()).To(Equal(projectName))
		Expect(createdInstance.GetObject().GetSpec().GetDiskImage().GetId()).To(Equal(diskImageID))
		Expect(createdInstance.GetObject().GetSpec().GetRunStrategy()).To(Equal(
			publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED,
		))
		Expect(createdInstance.GetObject().GetSpec().GetInstanceType().GetId()).To(Equal(deprecatedInstanceTypeID))
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
		for _, content := range result.Content {
			if textContent, ok := content.(*mcp.TextContent); ok {
				return output, fmt.Errorf("tool %q returned an error result: %s", name, textContent.Text)
			}
		}
		return output, fmt.Errorf("tool %q returned an error result with no text content", name)
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
