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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// catalogItemFixtureSSHPublicKey is a valid public key for provisioning authentication.
const catalogItemFixtureSSHPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8K1ZuSC7tmzxD5LJJXwkCfStVEjzXWYCFhJaLBxWAn test@example.com"

func catalogItemFixtureName() string {
	return "catalog-item-" + uuid.New()[24:]
}

func catalogItemUpdateMask(paths ...string) *fieldmaskpb.FieldMask {
	return &fieldmaskpb.FieldMask{Paths: paths}
}

func catalogItemParameterValue(value proto.Message) *anypb.Any {
	GinkgoHelper()
	result, err := anypb.New(value)
	Expect(err).NotTo(HaveOccurred())
	return result
}

func expectCatalogItemStatusCode(err error, expected codes.Code) {
	GinkgoHelper()
	Expect(err).To(HaveOccurred())
	Expect(status.Code(err)).To(Equal(expected), "unexpected gRPC error: %v", err)
}

// Cleanup waits for archival before removing dependencies. The harness reuses its Kind cluster.
func deferCatalogItemFixtureDeletion(deleteDependency func(context.Context) error, deleted func(context.Context) (bool, error)) {
	GinkgoHelper()
	DeferCleanup(func(ctx context.Context) {
		err := deleteDependency(ctx)
		if status.Code(err) == codes.NotFound {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		if deleted != nil {
			Eventually(func() bool {
				removed, err := deleted(ctx)
				if err != nil {
					if status.Code(err) == codes.NotFound {
						return true
					}
					// Transient error during async deletion — retry.
					return false
				}
				return removed
			}, 5*time.Minute, 2*time.Second).Should(BeTrue(), "fixture was not removed")
		}
	})
}

func catalogItemFixtureMetadata(tenant, project string) *privatev1.Metadata {
	return privatev1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: tenant, Project: project}.Build()
}

func createCatalogItemProjectFixture(ctx context.Context, tenant string) string {
	GinkgoHelper()
	client := privatev1.NewProjectsClient(tool.InternalView().AdminConn())
	name := catalogItemFixtureName()
	result, err := client.Create(ctx, privatev1.ProjectsCreateRequest_builder{
		Object: privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{Name: name, Tenant: tenant}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, e := client.Delete(ctx, privatev1.ProjectsDeleteRequest_builder{Id: result.GetObject().GetId()}.Build())
		return e
	}, nil)
	return name
}

func createCatalogItemDiskImageFixture(ctx context.Context, tenant, name string) *privatev1.DiskImage {
	GinkgoHelper()
	return createCatalogItemDiskImageInProjectFixture(ctx, tenant, "", name)
}

func createCatalogItemDiskImageInProjectFixture(ctx context.Context, tenant, project, name string) *privatev1.DiskImage {
	GinkgoHelper()
	client := privatev1.NewDiskImagesClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
		Object: privatev1.DiskImage_builder{
			Metadata: privatev1.Metadata_builder{Name: name, Tenant: tenant, Project: project}.Build(),
			Spec: privatev1.DiskImageSpec_builder{
				SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:     "quay.io/containerdisks/fedora:41",
				GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
				Architecture:  []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64},
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	object := response.GetObject()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{Id: object.GetId()}.Build())
		return err
	}, nil)
	return object
}

func createCatalogItemComputeInstanceTypeFixture(ctx context.Context) string {
	GinkgoHelper()
	client := privatev1.NewInstanceTypesClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
		Object: privatev1.InstanceType_builder{
			Metadata: catalogItemFixtureMetadata("shared", ""),
			Spec:     privatev1.InstanceTypeSpec_builder{Vcpus: 2, MemoryGib: 4}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := response.GetObject().GetId()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, privatev1.InstanceTypesDeleteRequest_builder{Id: id}.Build())
		return err
	}, nil)
	return id
}

func createCatalogItemStorageTierFixture(ctx context.Context) string {
	GinkgoHelper()
	backends := privatev1.NewStorageBackendsClient(tool.InternalView().AdminConn())
	backend, err := backends.Create(ctx, privatev1.StorageBackendsCreateRequest_builder{
		Object: privatev1.StorageBackend_builder{
			Metadata: catalogItemFixtureMetadata("shared", ""),
			Spec: privatev1.StorageBackendSpec_builder{
				Provider: "test",
				Endpoint: "https://storage.example.com",
				Credentials: privatev1.StorageBackendCredentials_builder{
					Username: "integration-test",
					Password: tool.secret,
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	backendID := backend.GetObject().GetId()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := backends.Delete(ctx, privatev1.StorageBackendsDeleteRequest_builder{Id: backendID}.Build())
		return err
	}, nil)
	client := privatev1.NewStorageTiersClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.StorageTiersCreateRequest_builder{
		Object: privatev1.StorageTier_builder{
			Metadata: catalogItemFixtureMetadata("shared", ""),
			Spec: privatev1.StorageTierSpec_builder{
				Protocol: privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
				Backends: []*privatev1.BackendAssociation{
					privatev1.BackendAssociation_builder{BackendId: backendID}.Build(),
				},
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := response.GetObject().GetId()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, privatev1.StorageTiersDeleteRequest_builder{Id: id}.Build())
		return err
	}, nil)
	return id
}

type catalogItemNetworkFixture struct {
	subnetID, securityGroupID, virtualNetworkID, networkClassID string
}

func createCatalogItemNetworkClassFixture(ctx context.Context) string {
	GinkgoHelper()
	classes := privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())
	class, err := classes.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
		Object: privatev1.NetworkClass_builder{Metadata: catalogItemFixtureMetadata("shared", ""), Title: "Catalog item integration network", FabricManager: new("netris")}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	classID := class.GetObject().GetId()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := classes.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{Id: classID}.Build())
		return err
	}, nil)
	waitForNetworkClassReady(ctx, classes, classID)
	return classID
}

func createCatalogItemNetworkFixture(ctx context.Context, tenant, project string) catalogItemNetworkFixture {
	GinkgoHelper()
	return createCatalogItemNetworkInClassFixture(ctx, tenant, project, createCatalogItemNetworkClassFixture(ctx))
}

func createCatalogItemSubnetInClassFixture(ctx context.Context, tenant, project, classID string) catalogItemNetworkFixture {
	GinkgoHelper()

	networks := privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
	network, err := networks.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
		Object: privatev1.VirtualNetwork_builder{
			Metadata: catalogItemFixtureMetadata(tenant, project),
			Spec: privatev1.VirtualNetworkSpec_builder{
				NetworkClass: privatev1.NetworkClassReference_builder{Id: classID}.Build(),
				Region:       "us-east-1",
				Ipv4Cidr:     new("10.100.0.0/16"),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	networkID := network.GetObject().GetId()

	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := networks.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{Id: networkID}.Build())
		return err
	}, func(ctx context.Context) (bool, error) {
		_, err := networks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: networkID}.Build())
		if status.Code(err) == codes.NotFound {
			return true, nil
		}
		if err != nil {
			return false, nil
		}
		_, err = privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn()).Signal(ctx, privatev1.VirtualNetworksSignalRequest_builder{Id: networkID}.Build())
		if status.Code(err) == codes.NotFound {
			return true, nil
		}
		return false, nil
	})

	Eventually(func() privatev1.VirtualNetworkState {
		r, e := networks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: networkID}.Build())
		Expect(e).NotTo(HaveOccurred())
		return r.GetObject().GetStatus().GetState()
	}, time.Minute, time.Second).Should(Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
	// Catalog provisioning needs a ready network, so advance this fixture from Pending to Ready.
	currentVirtualNetwork, err := networks.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: networkID}.Build())
	Expect(err).NotTo(HaveOccurred())
	currentVirtualNetwork.GetObject().SetStatus(privatev1.VirtualNetworkStatus_builder{State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY}.Build())
	_, err = networks.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{Object: currentVirtualNetwork.GetObject(), UpdateMask: catalogItemUpdateMask("status.state")}.Build())
	Expect(err).NotTo(HaveOccurred())

	subnets := privatev1.NewSubnetsClient(tool.InternalView().AdminConn())
	subnet, err := subnets.Create(ctx, privatev1.SubnetsCreateRequest_builder{
		Object: privatev1.Subnet_builder{
			Metadata: catalogItemFixtureMetadata(tenant, project),
			Spec: privatev1.SubnetSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: networkID}.Build(),
				Ipv4Cidr:       new("10.100.1.0/24"),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	subnetID := subnet.GetObject().GetId()

	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := subnets.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{Id: subnetID}.Build())
		return err
	}, func(ctx context.Context) (bool, error) {
		_, err := subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
		if status.Code(err) == codes.NotFound {
			return true, nil
		}
		if err != nil {
			return false, nil
		}
		_, err = privatev1.NewSubnetsClient(tool.InternalView().AdminConn()).Signal(ctx, privatev1.SubnetsSignalRequest_builder{Id: subnetID}.Build())
		if status.Code(err) == codes.NotFound {
			return true, nil
		}
		return false, nil
	})

	Eventually(func() privatev1.SubnetState {
		r, e := subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
		Expect(e).NotTo(HaveOccurred())
		return r.GetObject().GetStatus().GetState()
	}, time.Minute, time.Second).Should(Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
	// Catalog provisioning needs a ready Subnet, so advance this fixture from Pending to Ready.
	currentSubnet, err := subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
	Expect(err).NotTo(HaveOccurred())
	currentSubnet.GetObject().SetStatus(privatev1.SubnetStatus_builder{State: privatev1.SubnetState_SUBNET_STATE_READY}.Build())
	_, err = subnets.Update(ctx, privatev1.SubnetsUpdateRequest_builder{Object: currentSubnet.GetObject(), UpdateMask: catalogItemUpdateMask("status.state")}.Build())
	Expect(err).NotTo(HaveOccurred())
	return catalogItemNetworkFixture{subnetID: subnetID, virtualNetworkID: networkID, networkClassID: classID}
}

func createCatalogItemNetworkInClassFixture(ctx context.Context, tenant, project, classID string) catalogItemNetworkFixture {
	GinkgoHelper()
	network := createCatalogItemSubnetInClassFixture(ctx, tenant, project, classID)

	groups := privatev1.NewSecurityGroupsClient(tool.InternalView().AdminConn())
	group, err := groups.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
		Object: privatev1.SecurityGroup_builder{
			Metadata: catalogItemFixtureMetadata(tenant, project),
			Spec: privatev1.SecurityGroupSpec_builder{
				VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: network.virtualNetworkID}.Build(),
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	groupID := group.GetObject().GetId()

	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := groups.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{Id: groupID}.Build())
		return err
	}, func(ctx context.Context) (bool, error) {
		_, err := groups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: groupID}.Build())
		if status.Code(err) == codes.NotFound {
			return true, nil
		}
		if err != nil {
			return false, nil
		}
		_, err = privatev1.NewSecurityGroupsClient(tool.InternalView().AdminConn()).Signal(ctx, privatev1.SecurityGroupsSignalRequest_builder{Id: groupID}.Build())
		if status.Code(err) == codes.NotFound {
			return true, nil
		}
		return false, nil
	})

	Eventually(func() privatev1.SecurityGroupState {
		r, e := groups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: groupID}.Build())
		Expect(e).NotTo(HaveOccurred())
		return r.GetObject().GetStatus().GetState()
	}, time.Minute, time.Second).Should(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING))
	// The Security Group must be ready before it can join a resource attachment.
	currentSecurityGroup, err := groups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: groupID}.Build())
	Expect(err).NotTo(HaveOccurred())
	currentSecurityGroup.GetObject().SetStatus(privatev1.SecurityGroupStatus_builder{State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY}.Build())
	_, err = groups.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{Object: currentSecurityGroup.GetObject(), UpdateMask: catalogItemUpdateMask("status.state")}.Build())
	Expect(err).NotTo(HaveOccurred())
	network.securityGroupID = groupID
	return network
}

func (f catalogItemNetworkFixture) computeInstanceAttachment() *publicv1.ComputeNetworkAttachment {
	attachment := publicv1.ComputeNetworkAttachment_builder{
		Subnet: publicv1.SubnetLocalReference_builder{Id: f.subnetID}.Build(),
	}.Build()
	if f.securityGroupID != "" {
		attachment.SetSecurityGroups([]*publicv1.SecurityGroupLocalReference{
			publicv1.SecurityGroupLocalReference_builder{Id: f.securityGroupID}.Build(),
		})
	}
	return attachment
}

func (f catalogItemNetworkFixture) bareMetalInstanceAttachment() *publicv1.BareMetalNetworkAttachment {
	attachment := publicv1.BareMetalNetworkAttachment_builder{
		Subnet: publicv1.SubnetLocalReference_builder{Id: f.subnetID}.Build(),
	}.Build()
	if f.securityGroupID != "" {
		attachment.SetSecurityGroups([]*publicv1.SecurityGroupLocalReference{
			publicv1.SecurityGroupLocalReference_builder{Id: f.securityGroupID}.Build(),
		})
	}
	return attachment
}

func (f catalogItemNetworkFixture) clusterAttachment() *publicv1.ClusterNetworkAttachment {
	attachment := publicv1.ClusterNetworkAttachment_builder{
		Subnet: publicv1.SubnetLocalReference_builder{Id: f.subnetID}.Build(),
	}.Build()
	if f.securityGroupID != "" {
		attachment.SetSecurityGroups([]*publicv1.SecurityGroupLocalReference{
			publicv1.SecurityGroupLocalReference_builder{Id: f.securityGroupID}.Build(),
		})
	}
	return attachment
}

func createCatalogItemPullSecretFixture(ctx context.Context, tenant string) string {
	GinkgoHelper()
	return createCatalogItemSecretFixture(ctx, tenant, privatev1.SecretType_SECRET_TYPE_PULL_SECRET,
		map[string][]byte{".dockerconfigjson": []byte(dockerConfigJSON)})
}

func createCatalogItemUserDataSecretFixture(ctx context.Context, tenant string) string {
	GinkgoHelper()
	return createCatalogItemSecretFixture(ctx, tenant, privatev1.SecretType_SECRET_TYPE_USER_DATA,
		map[string][]byte{"userdata": []byte("#cloud-config\n")})
}

func createCatalogItemSecretFixture(ctx context.Context, tenant string, secretType privatev1.SecretType, data map[string][]byte) string {
	GinkgoHelper()
	client := privatev1.NewSecretsClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.SecretsCreateRequest_builder{
		Object: privatev1.Secret_builder{
			Metadata: catalogItemFixtureMetadata(tenant, ""),
			Type:     secretType,
			Data:     data,
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := response.GetObject().GetId()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, privatev1.SecretsDeleteRequest_builder{Id: id}.Build())
		return err
	}, nil)
	return id
}

func computeInstanceCatalogItemParameterDefinitions() []*privatev1.ComputeInstanceTemplateParameterDefinition {
	return []*privatev1.ComputeInstanceTemplateParameterDefinition{
		privatev1.ComputeInstanceTemplateParameterDefinition_builder{Name: "enabled", Type: "type.googleapis.com/google.protobuf.BoolValue", Required: true}.Build(),
		privatev1.ComputeInstanceTemplateParameterDefinition_builder{Name: "size", Type: "type.googleapis.com/google.protobuf.Int32Value", Default: catalogItemParameterValue(wrapperspb.Int32(10))}.Build(),
		privatev1.ComputeInstanceTemplateParameterDefinition_builder{
			Name:    "ordinary",
			Type:    "type.googleapis.com/google.protobuf.StringValue",
			Default: catalogItemParameterValue(wrapperspb.String("template")),
		}.Build(),
	}
}

func catalogItemParameterPolicies() map[string]*publicv1.TemplateParameterPolicy {
	return map[string]*publicv1.TemplateParameterPolicy{
		"enabled": publicv1.TemplateParameterPolicy_builder{Locked: catalogItemParameterValue(wrapperspb.Bool(false))}.Build(),
		"size": publicv1.TemplateParameterPolicy_builder{
			Editable: publicv1.EditableTemplateParameter_builder{DefaultValue: catalogItemParameterValue(wrapperspb.Int32(20))}.Build(),
		}.Build(),
	}
}

func createCatalogItemHostTypeFixture(ctx context.Context) string {
	GinkgoHelper()
	client := privatev1.NewHostTypesClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.HostTypesCreateRequest_builder{
		Object: privatev1.HostType_builder{
			Metadata: catalogItemFixtureMetadata("shared", ""),
			Id:       fmt.Sprintf("catalog_item_host_%s", uuid.New()[24:]),
			Interfaces: []*privatev1.NetworkInterface{
				privatev1.NetworkInterface_builder{Name: "data-0", Role: "fabric"}.Build(),
			},
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := response.GetObject().GetId()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, privatev1.HostTypesDeleteRequest_builder{Id: id}.Build())
		return err
	}, nil)
	return id
}

// Keycloak break-glass administration does not imply OSAC tenant-admin permissions.
// Grant that realm role before logging in so the public API exercises genuine tenant scope.
func createCatalogItemTenantAdminFixture(ctx context.Context) (string, *grpc.ClientConn) {
	GinkgoHelper()
	tenants := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
	name := catalogItemFixtureName()
	created, err := tenants.Create(ctx, privatev1.TenantsCreateRequest_builder{
		Object: privatev1.Tenant_builder{Metadata: privatev1.Metadata_builder{Name: name}.Build()}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := created.GetObject().GetId()
	DeferCleanup(func(ctx context.Context) {
		// Authentication creates fulfillment User records in the tenant's root project.
		users := privatev1.NewUsersClient(tool.InternalView().AdminConn())
		listed, err := users.List(ctx, privatev1.UsersListRequest_builder{Filter: new("this.metadata.tenant == '" + name + "'")}.Build())
		Expect(err).NotTo(HaveOccurred())
		for _, user := range listed.GetItems() {
			_, err = users.Delete(ctx, privatev1.UsersDeleteRequest_builder{Id: user.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) bool {
				_, err := users.Get(ctx, privatev1.UsersGetRequest_builder{Id: user.GetId()}.Build())
				if status.Code(err) == codes.NotFound {
					return true
				}
				g.Expect(err).NotTo(HaveOccurred())
				_, err = users.Signal(ctx, privatev1.UsersSignalRequest_builder{Id: user.GetId()}.Build())
				if status.Code(err) == codes.NotFound {
					return true
				}
				g.Expect(err).NotTo(HaveOccurred())
				return false
			}, time.Minute, time.Second).Should(BeTrue())
		}
		deleteTenant(ctx, tenants, privatev1.NewProjectsClient(tool.InternalView().AdminConn()), id, name)
	})

	waitForTenantSynced(ctx, tenants, id)
	tenant, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: id}.Build())
	Expect(err).NotTo(HaveOccurred())

	roleStatus, body, err := tool.KeycloakAdminRequest(ctx, http.MethodGet, "/roles/tenant-admin", nil)
	Expect(err).NotTo(HaveOccurred())
	if roleStatus == http.StatusNotFound {
		code, _, e := tool.KeycloakAdminRequest(ctx, http.MethodPost, "/roles", map[string]any{"name": "tenant-admin"})
		Expect(e).NotTo(HaveOccurred())
		Expect(code).To(Equal(http.StatusCreated))
		roleStatus, body, err = tool.KeycloakAdminRequest(ctx, http.MethodGet, "/roles/tenant-admin", nil)
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(roleStatus).To(Equal(http.StatusOK))
	var role map[string]any
	Expect(json.Unmarshal(body, &role)).To(Succeed())
	code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodPost, "/users/"+tenant.GetObject().GetStatus().GetBreakGlassUserId()+"/role-mappings/realm", []map[string]any{role})
	Expect(err).NotTo(HaveOccurred())
	Expect(code).To(Equal(http.StatusNoContent))

	_, token := loginAsBreakGlass(ctx, tenants, name, id)
	conn, err := tool.makeGrpcConn(externalServiceAddr, token)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(conn.Close)
	return name, conn
}

func createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx context.Context, parameters []*privatev1.ComputeInstanceTemplateParameterDefinition) string {
	GinkgoHelper()
	instanceType := createCatalogItemComputeInstanceTypeFixture(ctx)
	image := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
	tier := createCatalogItemStorageTierFixture(ctx)
	return createCatalogItemComputeInstanceTemplateFixture(ctx, privatev1.ComputeInstanceTemplateSpecDefaults_builder{
		InstanceType: privatev1.InstanceTypeReference_builder{Id: instanceType}.Build(),
		DiskImage:    privatev1.DiskImageReference_builder{Id: image.GetId()}.Build(),
		BootDisk: privatev1.ComputeInstanceDisk_builder{
			SizeGib:     new(int32(30)),
			StorageTier: privatev1.StorageTierReference_builder{Id: tier}.Build(),
		}.Build(),
		RunStrategy: new(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS),
	}.Build(), parameters)
}

func createCatalogItemComputeInstanceTemplateFixture(ctx context.Context, defaults *privatev1.ComputeInstanceTemplateSpecDefaults, parameters []*privatev1.ComputeInstanceTemplateParameterDefinition) string {
	GinkgoHelper()
	client := privatev1.NewComputeInstanceTemplatesClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
		Object: privatev1.ComputeInstanceTemplate_builder{
			Id:           "catalog_item_compute_instance_" + uuid.New()[24:],
			Metadata:     catalogItemFixtureMetadata("shared", ""),
			Title:        "Catalog item integration Template",
			SpecDefaults: defaults,
			Parameters:   parameters,
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := response.GetObject().GetId()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, privatev1.ComputeInstanceTemplatesDeleteRequest_builder{Id: id}.Build())
		return err
	}, nil)
	return id
}

func createCatalogItemClusterTemplateFixture(ctx context.Context, host string, defaults *privatev1.ClusterTemplateSpecDefaults, parameters []*privatev1.ClusterTemplateParameterDefinition) string {
	GinkgoHelper()
	client := privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
	nodes := map[string]*privatev1.ClusterTemplateNodeSet{}
	if host != "" {
		nodes["workers"] = privatev1.ClusterTemplateNodeSet_builder{
			HostType: privatev1.HostTypeReference_builder{Id: host}.Build(),
			Size:     2,
		}.Build()
	}
	response, err := client.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
		Object: privatev1.ClusterTemplate_builder{
			Id:           "catalog_item_cluster_" + uuid.New()[24:],
			Metadata:     catalogItemFixtureMetadata("shared", ""),
			Title:        "Catalog item integration Template",
			SpecDefaults: defaults,
			Parameters:   parameters,
			NodeSets:     nodes,
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := response.GetObject().GetId()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{Id: id}.Build())
		return err
	}, nil)
	return id
}

func createCatalogItemBareMetalInstanceTemplateFixture(ctx context.Context, defaults *privatev1.BareMetalInstanceTemplateSpecDefaults, parameters []*privatev1.BareMetalInstanceTemplateParameterDefinition) string {
	GinkgoHelper()
	client := privatev1.NewBareMetalInstanceTemplatesClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
		Object: privatev1.BareMetalInstanceTemplate_builder{
			Id:           "catalog_item_bare_metal_instance_" + uuid.New()[24:],
			Metadata:     catalogItemFixtureMetadata("shared", ""),
			Title:        "Catalog item integration Template",
			SpecDefaults: defaults,
			Parameters:   parameters,
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := response.GetObject().GetId()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, privatev1.BareMetalInstanceTemplatesDeleteRequest_builder{Id: id}.Build())
		return err
	}, nil)
	return id
}

func createCatalogItemBareMetalInstanceTypeFixture(ctx context.Context, tenant string) string {
	GinkgoHelper()
	client := privatev1.NewBareMetalInstanceTypesClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
		Object: privatev1.BareMetalInstanceType_builder{
			Metadata: catalogItemFixtureMetadata(tenant, ""),
			Spec: privatev1.BareMetalInstanceTypeSpec_builder{
				Hardware: privatev1.BareMetalHardwareSpec_builder{
					Cpu:    privatev1.BareMetalCPUSpec_builder{Cores: 4, Architecture: "x86_64", ThreadsPerCore: 2}.Build(),
					Memory: privatev1.BareMetalMemorySpec_builder{TotalGb: 16}.Build(),
					NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
						privatev1.BareMetalNetworkPortSpec_builder{
							Name:  "data-0",
							Role:  "fabric",
							Type:  "Ethernet",
							Speed: "25Gbps",
						}.Build(),
					},
				}.Build(),
				HostLabelSelector: privatev1.BareMetalLabelSelector_builder{MatchLabels: map[string]string{"osac.openshift.io/host-type": "compute"}}.Build(),
				Description:       "Catalog item integration hardware",
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := response.GetObject().GetId()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, privatev1.BareMetalInstanceTypesDeleteRequest_builder{Id: id}.Build())
		return err
	}, nil)
	return id
}

func createCatalogItemClusterVersionFixture(ctx context.Context, version string) string {
	GinkgoHelper()
	client := privatev1.NewClusterVersionsClient(tool.InternalView().AdminConn())
	response, err := client.Create(ctx, privatev1.ClusterVersionsCreateRequest_builder{
		Object: privatev1.ClusterVersion_builder{
			Metadata: catalogItemFixtureMetadata("shared", ""),
			Spec:     privatev1.ClusterVersionSpec_builder{Version: version, Image: "quay.io/openshift-release-dev/ocp-release:" + version + "-multi"}.Build(),
		}.Build(),
	}.Build())
	Expect(err).NotTo(HaveOccurred())
	id := response.GetObject().GetId()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, privatev1.ClusterVersionsDeleteRequest_builder{Id: id}.Build())
		return err
	}, nil)
	return id
}

func createComputeInstanceCatalogItemFixture(ctx context.Context, conn *grpc.ClientConn, object *publicv1.ComputeInstanceCatalogItem) *publicv1.ComputeInstanceCatalogItem {
	GinkgoHelper()
	client := publicv1.NewComputeInstanceCatalogItemsClient(conn)
	response, err := client.Create(ctx, publicv1.ComputeInstanceCatalogItemsCreateRequest_builder{Object: object}.Build())
	Expect(err).NotTo(HaveOccurred())
	result := response.GetObject()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, publicv1.ComputeInstanceCatalogItemsDeleteRequest_builder{Id: result.GetId()}.Build())
		return err
	}, nil)
	return result
}

func createClusterCatalogItemFixture(ctx context.Context, conn *grpc.ClientConn, object *publicv1.ClusterCatalogItem) *publicv1.ClusterCatalogItem {
	GinkgoHelper()
	client := publicv1.NewClusterCatalogItemsClient(conn)
	response, err := client.Create(ctx, publicv1.ClusterCatalogItemsCreateRequest_builder{Object: object}.Build())
	Expect(err).NotTo(HaveOccurred())
	result := response.GetObject()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, publicv1.ClusterCatalogItemsDeleteRequest_builder{Id: result.GetId()}.Build())
		return err
	}, nil)
	return result
}

func createBareMetalInstanceCatalogItemFixture(ctx context.Context, conn *grpc.ClientConn, object *publicv1.BareMetalInstanceCatalogItem) *publicv1.BareMetalInstanceCatalogItem {
	GinkgoHelper()
	client := publicv1.NewBareMetalInstanceCatalogItemsClient(conn)
	response, err := client.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{Object: object}.Build())
	Expect(err).NotTo(HaveOccurred())
	result := response.GetObject()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, publicv1.BareMetalInstanceCatalogItemsDeleteRequest_builder{Id: result.GetId()}.Build())
		return err
	}, nil)
	return result
}

func createComputeInstanceFixture(ctx context.Context, conn *grpc.ClientConn, spec *publicv1.ComputeInstanceSpec) (*publicv1.ComputeInstance, error) {
	GinkgoHelper()
	client := publicv1.NewComputeInstancesClient(conn)
	response, err := client.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
		Object: publicv1.ComputeInstance_builder{
			Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
			Spec:     spec,
		}.Build(),
	}.Build())
	if err != nil {
		return nil, err
	}
	result := response.GetObject()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, publicv1.ComputeInstancesDeleteRequest_builder{Id: result.GetId()}.Build())
		return err
	}, func(ctx context.Context) (bool, error) {
		_, err := client.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: result.GetId()}.Build())
		if status.Code(err) == codes.NotFound {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		_, err = privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn()).Signal(ctx, privatev1.ComputeInstancesSignalRequest_builder{Id: result.GetId()}.Build())
		if status.Code(err) == codes.NotFound {
			return true, nil
		}
		return false, err
	})
	return result, nil
}

func createClusterFixture(ctx context.Context, conn *grpc.ClientConn, spec *publicv1.ClusterSpec) (*publicv1.Cluster, error) {
	GinkgoHelper()
	client := publicv1.NewClustersClient(conn)
	response, err := client.Create(ctx, publicv1.ClustersCreateRequest_builder{
		Object: publicv1.Cluster_builder{
			Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
			Spec:     spec,
		}.Build(),
	}.Build())
	if err != nil {
		return nil, err
	}
	result := response.GetObject()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, publicv1.ClustersDeleteRequest_builder{Id: result.GetId()}.Build())
		return err
	}, func(ctx context.Context) (bool, error) {
		_, err := client.Get(ctx, publicv1.ClustersGetRequest_builder{Id: result.GetId()}.Build())
		if status.Code(err) == codes.NotFound {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		_, err = privatev1.NewClustersClient(tool.InternalView().AdminConn()).Signal(ctx, privatev1.ClustersSignalRequest_builder{Id: result.GetId()}.Build())
		if status.Code(err) == codes.NotFound {
			return true, nil
		}
		return false, err
	})
	return result, nil
}

func createBareMetalInstanceFixture(ctx context.Context, conn *grpc.ClientConn, spec *publicv1.BareMetalInstanceSpec) (*publicv1.BareMetalInstance, error) {
	GinkgoHelper()
	client := publicv1.NewBareMetalInstancesClient(conn)
	response, err := client.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
		Object: publicv1.BareMetalInstance_builder{
			Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
			Spec:     spec,
		}.Build(),
	}.Build())
	if err != nil {
		return nil, err
	}
	result := response.GetObject()
	deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
		_, err := client.Delete(ctx, publicv1.BareMetalInstancesDeleteRequest_builder{Id: result.GetId()}.Build())
		return err
	}, func(ctx context.Context) (bool, error) {
		_, err := client.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{Id: result.GetId()}.Build())
		if status.Code(err) == codes.NotFound {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		_, err = privatev1.NewBareMetalInstancesClient(tool.InternalView().AdminConn()).Signal(ctx, privatev1.BareMetalInstancesSignalRequest_builder{Id: result.GetId()}.Build())
		if status.Code(err) == codes.NotFound {
			return true, nil
		}
		return false, err
	})
	return result, nil
}

func clusterCatalogItemParameterDefinitions() []*privatev1.ClusterTemplateParameterDefinition {
	result := []*privatev1.ClusterTemplateParameterDefinition{}
	for _, p := range computeInstanceCatalogItemParameterDefinitions() {
		result = append(result, privatev1.ClusterTemplateParameterDefinition_builder{Name: p.GetName(), Type: p.GetType(), Default: p.GetDefault(), Required: p.GetRequired()}.Build())
	}
	return result
}

func bareMetalInstanceCatalogItemParameterDefinitions() []*privatev1.BareMetalInstanceTemplateParameterDefinition {
	result := []*privatev1.BareMetalInstanceTemplateParameterDefinition{}
	for _, p := range computeInstanceCatalogItemParameterDefinitions() {
		result = append(result, privatev1.BareMetalInstanceTemplateParameterDefinition_builder{Name: p.GetName(), Type: p.GetType(), Default: p.GetDefault(), Required: p.GetRequired()}.Build())
	}
	return result
}

func setCatalogItemSubnetFixtureState(ctx context.Context, id string, state privatev1.SubnetState) {
	GinkgoHelper()
	client := privatev1.NewSubnetsClient(tool.InternalView().AdminConn())
	current, err := client.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
	Expect(err).NotTo(HaveOccurred())
	current.GetObject().GetStatus().SetState(state)
	_, err = client.Update(ctx, privatev1.SubnetsUpdateRequest_builder{Object: current.GetObject(), UpdateMask: catalogItemUpdateMask("status.state")}.Build())
	Expect(err).NotTo(HaveOccurred())
}

// createCatalogItemMemberFixture authenticates an ordinary member of the item's tenant.
func createCatalogItemMemberFixture(ctx context.Context, tenant string) *grpc.ClientConn {
	GinkgoHelper()
	name := catalogItemFixtureName()
	code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodPost, "/users", map[string]any{"username": name, "enabled": true, "firstName": name, "lastName": "Test"})
	Expect(err).NotTo(HaveOccurred())
	Expect(code).To(Equal(http.StatusCreated))
	_, err = tool.keycloakEnsureUserReady(ctx, name)
	Expect(err).NotTo(HaveOccurred())
	// The tenant fixture deletes fulfillment users and their IDP accounts together.

	Expect(tool.ensureUserInOrg(ctx, name, tenant)).To(Succeed())
	token, err := tool.makeKeycloakTokenSource(ctx, name, usersPassword)
	Expect(err).NotTo(HaveOccurred())
	conn, err := tool.makeGrpcConn(externalServiceAddr, token)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(conn.Close)
	return conn
}
