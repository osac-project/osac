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

	"github.com/google/uuid"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// The fixtures of this file seed parents directly through DAOs, bypassing the create gates of the typed servers, and
// return the unsaved object for the case. The options mirror the builder options of the production server of each
// resource.

// finalizerPlatformCases returns the cases for the platform-scoped resources: catalogs, templates, types and the like.
func finalizerPlatformCases() []finalizerCase {
	return []finalizerCase{
		newFinalizerCase(privatev1.AddOnOperators_ServiceDesc,
			func() *privatev1.AddOnOperator {
				return privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("addon")}.Build(),
					Title:    "GPU Operator",
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.AddOnOperator]) {
				b.AddAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.BareMetalInstanceTypes_ServiceDesc,
			func() *privatev1.BareMetalInstanceType {
				return privatev1.BareMetalInstanceType_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   finalizerName("bmit"),
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.BareMetalInstanceTypeSpec_builder{
						Description: "finalizer CAS fixture",
						Hardware: privatev1.BareMetalHardwareSpec_builder{
							Cpu: privatev1.BareMetalCPUSpec_builder{
								Cores: 8, Architecture: "amd64", ThreadsPerCore: 2,
							}.Build(),
							Memory: privatev1.BareMetalMemorySpec_builder{TotalGb: 32}.Build(),
							NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
								privatev1.BareMetalNetworkPortSpec_builder{
									Name: "data-0", Role: "fabric", Type: "Ethernet", Speed: "25Gbps",
								}.Build(),
							},
						}.Build(),
						HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
							MatchLabels: map[string]string{"profile": "fabric"},
						}.Build(),
					}.Build(),
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.BareMetalInstanceType]) {
				b.SetAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.ClusterTemplates_ServiceDesc,
			func() *privatev1.ClusterTemplate {
				return privatev1.ClusterTemplate_builder{
					Metadata:    privatev1.Metadata_builder{Name: finalizerName("cluster-template")}.Build(),
					Title:       "My title",
					Description: "My description.",
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.ClusterTemplate]) {
				b.AddAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.ClusterVersions_ServiceDesc,
			func() *privatev1.ClusterVersion {
				return privatev1.ClusterVersion_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   finalizerName("cluster-version"),
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.ClusterVersionSpec_builder{
						Version: "4.17.0",
						Image:   "quay.io/openshift-release-dev/ocp-release:4.17.0-multi",
						State:   privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE,
					}.Build(),
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.ClusterVersion]) {
				b.SetAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.ComputeInstanceTemplates_ServiceDesc,
			func() *privatev1.ComputeInstanceTemplate {
				return privatev1.ComputeInstanceTemplate_builder{
					Metadata:    privatev1.Metadata_builder{Name: finalizerName("ci-template")}.Build(),
					Title:       "My title",
					Description: "My description.",
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.ComputeInstanceTemplate]) {
				b.AddAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.DiskImages_ServiceDesc,
			func() *privatev1.DiskImage {
				return privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("disk-image")}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType:   privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:    "quay.io/containerdisks/fedora:41",
						Architecture: []privatev1.Architecture{privatev1.Architecture_ARCHITECTURE_AMD64},
					}.Build(),
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.DiskImage]) {
				b.AddAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.InstanceTypes_ServiceDesc,
			func() *privatev1.InstanceType {
				return privatev1.InstanceType_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("instance-type")}.Build(),
					Spec: privatev1.InstanceTypeSpec_builder{
						Vcpus:       4,
						MemoryGib:   16,
						Description: "Standard 4 vCPUs, 16 GiB RAM.",
					}.Build(),
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.InstanceType]) {
				b.AddAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.NetworkClasses_ServiceDesc,
			func() *privatev1.NetworkClass {
				// The deployment NetworkClass is a singleton, so the case creates it itself:
				return privatev1.NetworkClass_builder{
					Title:         "OSAC NetworkClass",
					FabricManager: "netris",
					Metadata: privatev1.Metadata_builder{
						Name:   "netris",
						Tenant: auth.SharedTenant,
					}.Build(),
					Spec: privatev1.NetworkClassSpec_builder{
						Defaults: privatev1.NetworkDefaults_builder{
							VirtualNetworkIpv4Cidr: "10.250.0.0/16",
							SubnetIpv4Cidr:         "10.250.0.0/24",
						}.Build(),
						EastWestCapabilities: privatev1.NetworkClassEastWestCapabilities_builder{
							SupportsEastWestEthernet: true,
						}.Build(),
						AddressPlan: fixtureAddressPlan(),
					}.Build(),
					Status: privatev1.NetworkClassStatus_builder{
						State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
						Hub:   "network-hub-a",
					}.Build(),
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.NetworkClass]) {
				b.AddAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.StorageBackends_ServiceDesc,
			func() *privatev1.StorageBackend {
				return finalizerStorageBackend()
			},
			func(b *GenericServerBuilder[*privatev1.StorageBackend]) {
				b.AddAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.StorageTiers_ServiceDesc,
			func() *privatev1.StorageTier {
				backend, err := fixtureDAO[*privatev1.StorageBackend]().Create().
					SetObject(withDefaultTenant(finalizerStorageBackend())).Do(ctx)
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				return privatev1.StorageTier_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("tier")}.Build(),
					Spec: privatev1.StorageTierSpec_builder{
						Description: "A test storage tier",
						Protocol:    privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
						Backends: []*privatev1.BackendAssociation{
							privatev1.BackendAssociation_builder{
								BackendId:            backend.GetObject().GetId(),
								MaxReadBandwidthMbs:  1000,
								MaxWriteBandwidthMbs: 500,
								EncryptionEnabled:    true,
							}.Build(),
						},
					}.Build(),
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.StorageTier]) {
				b.AddAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.IdentityProviders_ServiceDesc,
			func() *privatev1.IdentityProvider {
				return privatev1.IdentityProvider_builder{
					Metadata: privatev1.Metadata_builder{
						Name:   finalizerName("idp"),
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.IdentityProviderSpec_builder{
						Title:   "Test OIDC",
						Enabled: true,
						Oidc: privatev1.OidcConfig_builder{
							AuthorizationUrl: "https://example.com/auth",
							TokenUrl:         "https://example.com/token",
							ClientId:         "client-id",
							Issuer:           "https://example.com",
						}.Build(),
					}.Build(),
				}.Build()
			},
		),
	}
}

// withDefaultTenant puts a storage backend in the test tenant, where fixtures are created by default.
func withDefaultTenant(backend *privatev1.StorageBackend) *privatev1.StorageBackend {
	backend.GetMetadata().SetTenant(testTenant)
	return backend
}

// finalizerStorageBackend returns a storage backend that isn't managed by the CSP.
func finalizerStorageBackend() *privatev1.StorageBackend {
	return privatev1.StorageBackend_builder{
		Metadata: privatev1.Metadata_builder{Name: finalizerName("backend")}.Build(),
		Spec: privatev1.StorageBackendSpec_builder{
			Provider: "vast",
			Endpoint: "https://storage.example.com:8443",
			Credentials: privatev1.StorageBackendCredentials_builder{
				Username: "admin",
				Password: testPassword,
			}.Build(),
		}.Build(),
	}.Build()
}

// finalizerTenantCases returns the cases for the resources that belong to a tenant and are created by its users.
func finalizerTenantCases() []finalizerCase {
	return []finalizerCase{
		newFinalizerCase(privatev1.Tenants_ServiceDesc,
			func() *privatev1.Tenant {
				name := finalizerName("tenant")
				return privatev1.Tenant_builder{
					Id:       name,
					Metadata: privatev1.Metadata_builder{Name: name, Tenant: name}.Build(),
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.Tenant]) {
				b.SetTableName("tenants")
			},
		).afterCreate(func(id string) {
			// The database creates a default project for every tenant, and the foreign key from that project
			// makes it impossible to archive the tenant, which the specs on deleting objects need to do:
			tx, err := database.TxFromContext(ctx)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
			_, err = tx.Exec(ctx, `delete from projects where tenant = $1`, id)
			ExpectWithOffset(1, err).ToNot(HaveOccurred())
		}),
		newFinalizerCase(privatev1.Projects_ServiceDesc,
			func() *privatev1.Project {
				return privatev1.Project_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("project")}.Build(),
					Spec: privatev1.ProjectSpec_builder{
						Title:       "My Project",
						Description: new("Test project"),
					}.Build(),
				}.Build()
			},
		),
		newFinalizerCase(privatev1.ProjectMemberships_ServiceDesc,
			func() *privatev1.ProjectMembership {
				return privatev1.ProjectMembership_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("membership")}.Build(),
					Spec: privatev1.ProjectMembershipSpec_builder{
						Role:  privatev1.ProjectMembershipRole_PROJECT_MEMBERSHIP_ROLE_VIEWER,
						Users: []*privatev1.UserReference{privatev1.UserReference_builder{Id: "user-1"}.Build()},
					}.Build(),
				}.Build()
			},
		),
		newFinalizerCase(privatev1.RoleBindings_ServiceDesc,
			func() *privatev1.RoleBinding {
				return privatev1.RoleBinding_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("binding")}.Build(),
					Spec: privatev1.RoleBindingSpec_builder{
						Role:  privatev1.RoleReference_builder{Id: "admin-role-id"}.Build(),
						Users: []*privatev1.UserReference{privatev1.UserReference_builder{Id: "admin-user-1"}.Build()},
					}.Build(),
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.RoleBinding]) {
				b.AddAllowedTenants(auth.SharedTenant, auth.SystemTenant)
			},
		),
		newFinalizerCase(privatev1.Secrets_ServiceDesc,
			func() *privatev1.Secret {
				return privatev1.Secret_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("secret")}.Build(),
					Data:     map[string][]byte{"key": []byte("value")},
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.Secret]) {
				b.AddAllowedTenants(auth.SharedTenant, auth.SystemTenant)
			},
		),
		newFinalizerCase(privatev1.ServiceAccounts_ServiceDesc,
			func() *privatev1.ServiceAccount {
				return privatev1.ServiceAccount_builder{
					Metadata: privatev1.Metadata_builder{
						Name:        finalizerName("robot"),
						Creator:     "admin",
						Annotations: map[string]string{"osac.openshift.io/tenant": testTenant},
					}.Build(),
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.ServiceAccount]) {
				b.AddAllowedTenants(auth.SystemTenant)
			},
		),
		newFinalizerCase(privatev1.ComputeInstanceCatalogItems_ServiceDesc,
			func() *privatev1.ComputeInstanceCatalogItem {
				templateID := fmt.Sprintf("finalizer_cas_%s", uuid.NewString()[:8])
				ExpectWithOffset(1, seedComputeCatalogItemTemplate(ctx, testTenant, "", templateID)).To(Succeed())
				return privatev1.ComputeInstanceCatalogItem_builder{
					Metadata:    privatev1.Metadata_builder{Name: finalizerName("ci-item")}.Build(),
					Title:       "My CI catalog item",
					Description: "My description.",
					Template:    privatev1.ComputeInstanceTemplateReference_builder{Id: templateID}.Build(),
					Published:   true,
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.ComputeInstanceCatalogItem]) {
				b.AddAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.ClusterCatalogItems_ServiceDesc,
			func() *privatev1.ClusterCatalogItem {
				templateID := fmt.Sprintf("finalizer_cas_%s", uuid.NewString()[:8])
				ExpectWithOffset(1, seedClusterCatalogItemTemplate(ctx, testTenant, "", templateID)).To(Succeed())
				return privatev1.ClusterCatalogItem_builder{
					Metadata:    privatev1.Metadata_builder{Name: finalizerName("cluster-item")}.Build(),
					Title:       "My cluster catalog item",
					Description: "My description.",
					Template:    privatev1.ClusterTemplateReference_builder{Id: templateID}.Build(),
					Published:   true,
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.ClusterCatalogItem]) {
				b.AddAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.BareMetalInstanceCatalogItems_ServiceDesc,
			func() *privatev1.BareMetalInstanceCatalogItem {
				templateID := fmt.Sprintf("finalizer_cas_%s", uuid.NewString()[:8])
				ExpectWithOffset(1, seedBareMetalCatalogItemTemplate(ctx, testTenant, "", templateID)).To(Succeed())
				return privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata:    privatev1.Metadata_builder{Name: finalizerName("bmi-item")}.Build(),
					Title:       "My catalog item",
					Description: "A test catalog item.",
					Template:    privatev1.BareMetalInstanceTemplateReference_builder{Id: templateID}.Build(),
					Published:   true,
				}.Build()
			},
			func(b *GenericServerBuilder[*privatev1.BareMetalInstanceCatalogItem]) {
				b.AddAllowedTenants(auth.SharedTenant)
			},
		),
		newFinalizerCase(privatev1.ComputeInstances_ServiceDesc,
			func() *privatev1.ComputeInstance {
				return privatev1.ComputeInstance_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("ci")}.Build(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
					}.Build(),
					Status: privatev1.ComputeInstanceStatus_builder{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
					}.Build(),
				}.Build()
			},
		),
		newFinalizerCase(privatev1.Clusters_ServiceDesc,
			func() *privatev1.Cluster {
				templateID := fmt.Sprintf("finalizer_cas_%s", uuid.NewString()[:8])
				ExpectWithOffset(1, seedClusterCatalogItemTemplate(ctx, testTenant, "", templateID)).To(Succeed())
				return privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("cluster")}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: templateID}.Build(),
					}.Build(),
				}.Build()
			},
		),
		newFinalizerCase(privatev1.BareMetalInstances_ServiceDesc,
			func() *privatev1.BareMetalInstance {
				templateID := fmt.Sprintf("finalizer_cas_%s", uuid.NewString()[:8])
				ExpectWithOffset(1, seedBareMetalCatalogItemTemplate(ctx, testTenant, "", templateID)).To(Succeed())
				return privatev1.BareMetalInstance_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("bmi")}.Build(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: templateID}.Build(),
						DiskImage: privatev1.DiskImageReference_builder{Id: "default-bmi-disk-image"}.Build(),
					}.Build(),
				}.Build()
			},
		),
		newFinalizerCase(privatev1.Volumes_ServiceDesc,
			func() *privatev1.Volume {
				return privatev1.Volume_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("volume")}.Build(),
					Spec: privatev1.VolumeSpec_builder{
						StorageTier: "gold",
						SizeGib:     100,
						AccessMode:  privatev1.VolumeAccessMode_VOLUME_ACCESS_MODE_READ_WRITE_ONCE,
					}.Build(),
				}.Build()
			},
		),
	}
}

// finalizerNetworkingCases returns the cases for the networking resources. Their parents are READY, as they would be
// when a user can reference them.
func finalizerNetworkingCases() []finalizerCase {
	virtualNetwork := func() *privatev1.VirtualNetwork {
		return seedVirtualNetwork(testTenant, "10.1.0.0/16",
			privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
	}
	return []finalizerCase{
		newFinalizerCase(privatev1.VirtualNetworks_ServiceDesc,
			func() *privatev1.VirtualNetwork {
				return privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("vn")}.Build(),
					Spec:     privatev1.VirtualNetworkSpec_builder{Ipv4Cidr: new("10.1.0.0/16")}.Build(),
					Status: privatev1.VirtualNetworkStatus_builder{
						State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
						Hub:   "network-hub-a",
					}.Build(),
				}.Build()
			},
		),
		newFinalizerCase(privatev1.Subnets_ServiceDesc,
			func() *privatev1.Subnet {
				vn := virtualNetwork()
				return privatev1.Subnet_builder{
					Metadata: privatev1.Metadata_builder{
						Name:        finalizerName("subnet"),
						Annotations: map[string]string{"osac.openshift.io/owner-reference": vn.GetId()},
					}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vn.GetId()}.Build(),
						Ipv4Cidr:       new("10.1.0.0/24"),
					}.Build(),
					Status: privatev1.SubnetStatus_builder{
						State: privatev1.SubnetState_SUBNET_STATE_READY,
						Hub:   "network-hub-a",
					}.Build(),
				}.Build()
			},
		),
		newFinalizerCase(privatev1.SecurityGroups_ServiceDesc,
			func() *privatev1.SecurityGroup {
				vn := virtualNetwork()
				return privatev1.SecurityGroup_builder{
					Metadata: privatev1.Metadata_builder{
						Name:        finalizerName("sg"),
						Annotations: map[string]string{"osac.openshift.io/owner-reference": vn.GetId()},
					}.Build(),
					Spec: privatev1.SecurityGroupSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vn.GetId()}.Build(),
					}.Build(),
					Status: privatev1.SecurityGroupStatus_builder{
						State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
						Hub:   "network-hub-a",
					}.Build(),
				}.Build()
			},
		),
		newFinalizerCase(privatev1.ExternalIPs_ServiceDesc,
			func() *privatev1.ExternalIP {
				// Archiving an ExternalIP returns its address to the pool, so the pool has to count it as allocated:
				pool := seedExternalIPPool("203.0.113.0/24", 10)
				pool.GetStatus().SetAllocated(1)
				pool.GetStatus().SetAvailable(9)
				_, err := fixtureDAO[*privatev1.ExternalIPPool]().Update().SetObject(pool).Do(ctx)
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				return privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("eip")}.Build(),
					Spec: privatev1.ExternalIPSpec_builder{
						Pool: privatev1.ExternalIPPoolReference_builder{Id: pool.GetId()}.Build(),
					}.Build(),
					Status: privatev1.ExternalIPStatus_builder{
						State:   privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
						Address: "203.0.113.1",
						Hub:     "network-hub-a",
					}.Build(),
				}.Build()
			},
		),
		newFinalizerCase(privatev1.NATGateways_ServiceDesc,
			func() *privatev1.NATGateway {
				vn := virtualNetwork()
				pool := seedExternalIPPool("198.18.0.0/30", 0)
				eip, err := fixtureDAO[*privatev1.ExternalIP]().Create().SetObject(privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("nat-eip"), Tenant: testTenant}.Build(),
					Spec: privatev1.ExternalIPSpec_builder{
						Pool: privatev1.ExternalIPPoolReference_builder{Id: pool.GetId()}.Build(),
					}.Build(),
					Status: privatev1.ExternalIPStatus_builder{
						State:   privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
						Address: "198.18.0.1",
						Hub:     "network-hub-a",
					}.Build(),
				}.Build()).Do(ctx)
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				return privatev1.NATGateway_builder{
					Metadata: privatev1.Metadata_builder{
						Name:        finalizerName("nat"),
						Annotations: map[string]string{"osac.openshift.io/owner-reference": vn.GetId()},
					}.Build(),
					Spec: privatev1.NATGatewaySpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: vn.GetId()}.Build(),
						ExternalIp:     privatev1.ExternalIPLocalReference_builder{Id: eip.GetObject().GetId()}.Build(),
					}.Build(),
					Status: privatev1.NATGatewayStatus_builder{
						State: privatev1.NATGatewayState_NAT_GATEWAY_STATE_READY,
						Hub:   "network-hub-a",
					}.Build(),
				}.Build()
			},
		),
		newFinalizerCase(privatev1.ExternalIPAttachments_ServiceDesc,
			func() *privatev1.ExternalIPAttachment {
				pool := seedExternalIPPool("203.0.113.0/24", 10)
				eip, err := fixtureDAO[*privatev1.ExternalIP]().Create().SetObject(privatev1.ExternalIP_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("eip"), Tenant: testTenant}.Build(),
					Spec: privatev1.ExternalIPSpec_builder{
						Pool: privatev1.ExternalIPPoolReference_builder{Id: pool.GetId()}.Build(),
					}.Build(),
					Status: privatev1.ExternalIPStatus_builder{
						State:   privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED,
						Address: "203.0.113.1",
						Hub:     "network-hub-a",
					}.Build(),
				}.Build()).Do(ctx)
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				instance, err := fixtureDAO[*privatev1.ComputeInstance]().Create().SetObject(
					privatev1.ComputeInstance_builder{
						Metadata: privatev1.Metadata_builder{Name: finalizerName("ci"), Tenant: testTenant}.Build(),
						Spec: privatev1.ComputeInstanceSpec_builder{
							Template: privatev1.ComputeInstanceTemplateReference_builder{Id: "general.small"}.Build(),
						}.Build(),
					}.Build()).Do(ctx)
				ExpectWithOffset(1, err).ToNot(HaveOccurred())
				return privatev1.ExternalIPAttachment_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("attachment")}.Build(),
					Spec: privatev1.ExternalIPAttachmentSpec_builder{
						ExternalIp:      privatev1.ExternalIPLocalReference_builder{Id: eip.GetObject().GetId()}.Build(),
						ComputeInstance: privatev1.ComputeInstanceLocalReference_builder{Id: instance.GetObject().GetId()}.Build(),
					}.Build(),
				}.Build()
			},
		),
		newFinalizerCase(privatev1.FabricDomains_ServiceDesc,
			func() *privatev1.FabricDomain {
				vn := virtualNetwork()
				return privatev1.FabricDomain_builder{
					Metadata: privatev1.Metadata_builder{Name: finalizerName("domain")}.Build(),
					Spec: privatev1.FabricDomainSpec_builder{
						Type:           privatev1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW,
						VirtualNetwork: vn.GetId(),
						Servers:        []string{"server-a"},
					}.Build(),
				}.Build()
			},
		),
	}
}
