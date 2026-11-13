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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Cluster with ClusterNetworkAttachment", func() {
	var (
		ctx context.Context

		// Public API client for cluster operations.
		clustersClient        publicv1.ClustersClient
		privateClustersClient privatev1.ClustersClient

		// Private API clients for fixture setup (networking resources, instance types, templates).
		instanceTypesClient   privatev1.BareMetalInstanceTypesClient
		templatesClient       privatev1.ClusterTemplatesClient
		networkClassesClient  privatev1.NetworkClassesClient
		virtualNetworksClient privatev1.VirtualNetworksClient
		subnetsClient         privatev1.SubnetsClient
		securityGroupsClient  privatev1.SecurityGroupsClient

		// Shared fixture identifiers.
		bmitName         string
		templateId       string
		networkClassId   string
		virtualNetworkId string
		subnetId         string
		securityGroupId  string
	)

	BeforeEach(func() {
		ctx = context.Background()

		// Create the clients:
		clustersClient = publicv1.NewClustersClient(tool.ExternalView().UserConn())
		privateClustersClient = privatev1.NewClustersClient(tool.InternalView().AdminConn())
		instanceTypesClient = privatev1.NewBareMetalInstanceTypesClient(tool.InternalView().AdminConn())
		templatesClient = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
		networkClassesClient = privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())
		virtualNetworksClient = privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
		subnetsClient = privatev1.NewSubnetsClient(tool.InternalView().AdminConn())
		securityGroupsClient = privatev1.NewSecurityGroupsClient(tool.InternalView().AdminConn())

		// Create a BareMetalInstanceType for testing:
		bmitName = fmt.Sprintf("test-bmit-%s", uuid.New()[24:32])
		_, err := instanceTypesClient.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: bmitName,
				}.Build(),
				Spec: privatev1.BareMetalInstanceTypeSpec_builder{
					Hardware: privatev1.BareMetalHardwareSpec_builder{
						Cpu:    privatev1.BareMetalCPUSpec_builder{Cores: 32, Architecture: "amd64", ThreadsPerCore: 2}.Build(),
						Memory: privatev1.BareMetalMemorySpec_builder{TotalGb: 128}.Build(),
						NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
							privatev1.BareMetalNetworkPortSpec_builder{
								Name:  "eth0",
								Role:  "fabric",
								Type:  "Ethernet",
								Speed: "10Gbps",
							}.Build(),
						},
					}.Build(),
					HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
						MatchLabels: map[string]string{"hardware.profile": "compute"},
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = instanceTypesClient.Delete(ctx, privatev1.BareMetalInstanceTypesDeleteRequest_builder{
				Id: bmitName,
			}.Build())
		})

		// Create a template for testing:
		templateId = newTemplateID("my_template")
		_, err = templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:    templateId,
				Title: "My template",
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-tmpl-%s", uuid.New()[24:32]),
				}.Build(),
				Description: "My template.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, _ = templatesClient.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
				Id: templateId,
			}.Build())
		})

		// Create a NetworkClass:
		ncResp, err := sharedNetworkClass(ctx, networkClassesClient)
		Expect(err).ToNot(HaveOccurred())
		networkClassId = ncResp.GetObject().GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupNetworkClassFixture(cleanupCtx, networkClassesClient, networkClassId)).To(Succeed())
		})

		// Create a VirtualNetwork:
		virtualNetworkId = fmt.Sprintf("test-vnet-%s", uuid.New())
		_, err = virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-vnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Id: virtualNetworkId,
				Spec: privatev1.VirtualNetworkSpec_builder{
					Ipv4Cidr: new("10.100.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupVirtualNetworkFixture(cleanupCtx, virtualNetworksClient, virtualNetworkId)).To(Succeed())
		})

		// Set VirtualNetwork to READY state. In IT there is no osac-operator feedback controller.
		setComputeInstanceFixtureVirtualNetworkReady(ctx, virtualNetworksClient, virtualNetworkId)

		// Create a Subnet in the VirtualNetwork:
		subnetId = fmt.Sprintf("test-subnet-%s", uuid.New())
		_, err = subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-subnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Id: subnetId,
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
					Ipv4Cidr:       new("10.100.1.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupSubnetFixture(cleanupCtx, subnetsClient, subnetId)).To(Succeed())
		})

		// Wait for the subnet reconciler to reach PENDING, then set to READY:
		setSubnetReady(ctx, subnetsClient, subnetId)

		// Create a SecurityGroup in the same VirtualNetwork:
		sgResp, err := securityGroupsClient.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-sg-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		securityGroupId = sgResp.GetObject().GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupSecurityGroupFixture(cleanupCtx, securityGroupsClient, securityGroupId)).To(Succeed())
		})

		// Wait for the security group reconciler to reach PENDING, then set to READY:
		setSecurityGroupReady(ctx, securityGroupsClient, securityGroupId)
	})

	It("Can create a cluster with network attachment referencing a READY subnet and security group", func() {
		// Create the cluster with an explicit network attachment:
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NodeSets: testClusterNodeSets(bmitName, 3),
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build(),
						SecurityGroups: []*publicv1.SecurityGroupLocalReference{
							publicv1.SecurityGroupLocalReference_builder{Id: securityGroupId}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupClusterFixture(cleanupCtx, clustersClient, privateClustersClient, object.GetId())).To(Succeed())
		})

		// Verify the network attachment is set correctly via Get:
		getResponse, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: object.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		spec := getResponse.GetObject().GetSpec()
		Expect(spec.GetNetworkAttachment()).ToNot(BeNil())
		Expect(spec.GetNetworkAttachment().GetSubnet().GetId()).To(Equal(subnetId))
		Expect(spec.GetNetworkAttachment().GetSecurityGroups()).To(HaveLen(1))
		Expect(spec.GetNetworkAttachment().GetSecurityGroups()[0].GetId()).To(Equal(securityGroupId))
	})

	Context("Tenant-default networking", func() {
		// Tenant defaults live in the tenant's default project, which has no name.
		const tenantDefaultProject = ""
		var tenant string

		BeforeEach(func() {
			tenants := privatev1.NewTenantsClient(tool.InternalView().ControllerConn())
			metadata := catalogItemUnmanagedFixtureMetadata("", "")
			created, err := tenants.Create(ctx, privatev1.TenantsCreateRequest_builder{
				Object: privatev1.Tenant_builder{
					Metadata: metadata,
					// Start with a ready condition; the controller may refresh it during setup.
					Status: privatev1.TenantStatus_builder{
						State: privatev1.TenantState_TENANT_STATE_FAILED,
						Conditions: []*privatev1.TenantCondition{
							privatev1.TenantCondition_builder{
								Type:   privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY,
								Status: privatev1.ConditionStatus_CONDITION_STATUS_TRUE,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			tenant = created.GetObject().GetId()
			DeferCleanup(func(cleanupCtx context.Context) {
				deleteTenant(cleanupCtx, tenants, privatev1.NewProjectsClient(tool.InternalView().AdminConn()),
					tenant, metadata.GetName())
			})
			Expect(setTenantDefaultNetworkingCondition(ctx, tenant, true)).To(Succeed())
			clustersClient = publicv1.NewClustersClient(tool.ExternalView().AdminConn())
		})

		setVirtualNetworkPending := func(updateCtx context.Context, id string) {
			response, err := virtualNetworksClient.Get(updateCtx,
				privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
			Expect(err).NotTo(HaveOccurred())
			object := response.GetObject()
			if !object.HasStatus() {
				object.SetStatus(&privatev1.VirtualNetworkStatus{})
			}
			if object.GetStatus().GetState() != privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING {
				object.GetStatus().SetState(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING)
				_, err = virtualNetworksClient.Update(updateCtx, privatev1.VirtualNetworksUpdateRequest_builder{
					Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
				}.Build())
				Expect(err).NotTo(HaveOccurred())
			}
			Eventually(func(g Gomega) {
				current, err := virtualNetworksClient.Get(updateCtx,
					privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(current.GetObject().GetStatus().GetState()).To(
					Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
			}, time.Minute, time.Second).Should(Succeed())
		}

		newIsolatedVirtualNetwork := func(project string, defaultLabelled bool) string {
			networks := privatev1.NewVirtualNetworksClient(tool.InternalView().ControllerConn())
			id := fmt.Sprintf("test-vnet-%s", uuid.New())
			name := fmt.Sprintf("test-vnet-%s", uuid.New()[24:32])
			resourceProject := project
			labels := map[string]string{}
			ipv4Cidr := "10.100.0.0/16"
			if defaultLabelled {
				resourceProject = ""
				name = "default"
				labels[tenantDefaultNetworkingLabel] = "true"
				networkClass, err := networkClassesClient.Get(ctx,
					privatev1.NetworkClassesGetRequest_builder{Id: networkClassId}.Build())
				Expect(err).NotTo(HaveOccurred())
				ipv4Cidr = networkClass.GetObject().GetSpec().GetDefaults().GetVirtualNetworkIpv4Cidr()
			}
			if defaultLabelled {
				// The Tenant onboarding reconciler can recover this fixture from
				// FAILED and provision its own default VNet. Give the fixture the
				// canonical name so the reconciler adopts it instead of creating a
				// second default-labeled network.
				filter := fmt.Sprintf(`this.metadata.tenant == %q && this.metadata.name == %q && `+
					`this.metadata.labels[%q] == "true"`, tenant, name, tenantDefaultNetworkingLabel)
				response, err := networks.List(ctx,
					privatev1.VirtualNetworksListRequest_builder{Filter: &filter}.Build())
				Expect(err).NotTo(HaveOccurred())
				if len(response.GetItems()) > 0 {
					Expect(response.GetItems()).To(HaveLen(1))
					id = response.GetItems()[0].GetId()
				} else {
					_, err = networks.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
						Object: privatev1.VirtualNetwork_builder{
							Id: id,
							Metadata: privatev1.Metadata_builder{
								Name: name, Tenant: tenant, Project: resourceProject,
								Annotations: map[string]string{"osac.openshift.io/management-state": "unmanaged"},
								Labels:      labels,
							}.Build(),
							Spec: privatev1.VirtualNetworkSpec_builder{Ipv4Cidr: new(ipv4Cidr)}.Build(),
						}.Build(),
					}.Build())
					if grpcstatus.Code(err) == grpccodes.AlreadyExists {
						response, err = networks.List(ctx,
							privatev1.VirtualNetworksListRequest_builder{Filter: &filter}.Build())
						Expect(err).NotTo(HaveOccurred())
						Expect(response.GetItems()).To(HaveLen(1))
						id = response.GetItems()[0].GetId()
					} else {
						Expect(err).NotTo(HaveOccurred())
					}
				}
				// Keep tenant defaulting from creating resources before the fixture
				// has created the Subnet and SecurityGroup needed by this case.
				setVirtualNetworkPending(ctx, id)
				allDefaultsFilter := fmt.Sprintf(`this.metadata.tenant == %q && this.metadata.labels[%q] == "true"`,
					tenant, tenantDefaultNetworkingLabel)
				allDefaults, err := networks.List(ctx,
					privatev1.VirtualNetworksListRequest_builder{Filter: &allDefaultsFilter}.Build())
				Expect(err).NotTo(HaveOccurred())
				Expect(allDefaults.GetItems()).To(HaveLen(1))
				Expect(allDefaults.GetItems()[0].GetMetadata().GetName()).To(Equal("default"))
			} else {
				_, err := networks.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
					Object: privatev1.VirtualNetwork_builder{
						Id: id,
						Metadata: privatev1.Metadata_builder{
							Name: name, Tenant: tenant, Project: resourceProject,
							Annotations: map[string]string{"osac.openshift.io/management-state": "unmanaged"},
							Labels:      labels,
						}.Build(),
						Spec: privatev1.VirtualNetworkSpec_builder{Ipv4Cidr: new(ipv4Cidr)}.Build(),
					}.Build(),
				}.Build())
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func(cleanupCtx context.Context) {
					Expect(cleanupVirtualNetworkFixture(cleanupCtx, networks, id)).To(Succeed())
				})
				setComputeInstanceFixtureVirtualNetworkReady(ctx, networks, id)
			}
			return id
		}

		waitForSubnetPending := func(subnets privatev1.SubnetsClient, id string) {
			Eventually(func(g Gomega) {
				probeCtx, cancel := context.WithTimeout(ctx, computeInstanceFixtureProbeTimeout)
				defer cancel()
				resp, err := subnets.Get(probeCtx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(resp.GetObject().GetStatus().GetState()).To(
					Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
			}, time.Minute, time.Second).Should(Succeed())
		}

		waitForSecurityGroupPending := func(groups privatev1.SecurityGroupsClient, id string) {
			Eventually(func(g Gomega) {
				probeCtx, cancel := context.WithTimeout(ctx, computeInstanceFixtureProbeTimeout)
				defer cancel()
				resp, err := groups.Get(probeCtx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(resp.GetObject().GetStatus().GetState()).To(
					Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING))
			}, time.Minute, time.Second).Should(Succeed())
		}

		newProjectSubnet := func(project, virtualNetworkId string, defaultLabelled bool, state string) string {
			subnets := privatev1.NewSubnetsClient(tool.InternalView().ControllerConn())
			id := fmt.Sprintf("test-subnet-%s", uuid.New())
			name := fmt.Sprintf("test-subnet-%s", uuid.New()[24:32])
			resourceProject := project
			labels := map[string]string{}
			ipv4Cidr := "10.100.1.0/24"
			if defaultLabelled {
				resourceProject = ""
				labels[tenantDefaultNetworkingLabel] = "true"
				name = "default-ipv4"
				setComputeInstanceFixtureVirtualNetworkReady(ctx, virtualNetworksClient, virtualNetworkId)
				networkClass, err := networkClassesClient.Get(ctx,
					privatev1.NetworkClassesGetRequest_builder{Id: networkClassId}.Build())
				Expect(err).NotTo(HaveOccurred())
				ipv4Cidr = networkClass.GetObject().GetSpec().GetDefaults().GetSubnetIpv4Cidr()
			}
			finalizers := []string{}
			if state == "deleting" {
				finalizers = []string{"test.osac.openshift.io/hold"}
			}
			annotations := map[string]string{"osac.openshift.io/management-state": "unmanaged"}
			if defaultLabelled {
				annotations["osac.openshift.io/owner-reference"] = virtualNetworkId
			}
			subnetObject := privatev1.Subnet_builder{
				Id: id,
				Metadata: privatev1.Metadata_builder{
					Name: name, Tenant: tenant, Project: resourceProject,
					Annotations: annotations, Labels: labels, Finalizers: finalizers,
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
					Ipv4Cidr:       new(ipv4Cidr),
				}.Build(),
			}.Build()
			if defaultLabelled {
				filter := fmt.Sprintf(`this.metadata.tenant == %q && this.metadata.name == %q && `+
					`this.metadata.labels[%q] == "true" && this.spec.virtual_network.id == %q`,
					tenant, name, tenantDefaultNetworkingLabel, virtualNetworkId)
				existing, err := subnets.List(ctx, privatev1.SubnetsListRequest_builder{Filter: &filter}.Build())
				Expect(err).NotTo(HaveOccurred())
				if len(existing.GetItems()) > 0 {
					Expect(existing.GetItems()).To(HaveLen(1))
					id = existing.GetItems()[0].GetId()
				} else {
					_, err = subnets.Create(ctx, privatev1.SubnetsCreateRequest_builder{Object: subnetObject}.Build())
					if grpcstatus.Code(err) == grpccodes.AlreadyExists {
						existing, err = subnets.List(ctx,
							privatev1.SubnetsListRequest_builder{Filter: &filter}.Build())
						Expect(err).NotTo(HaveOccurred())
						Expect(existing.GetItems()).To(HaveLen(1))
						id = existing.GetItems()[0].GetId()
					} else {
						Expect(err).NotTo(HaveOccurred())
					}
				}
			} else {
				_, err := subnets.Create(ctx, privatev1.SubnetsCreateRequest_builder{Object: subnetObject}.Build())
				Expect(err).NotTo(HaveOccurred())
			}
			DeferCleanup(func(cleanupCtx context.Context) {
				Expect(cleanupSubnetFixture(cleanupCtx, subnets, id)).To(Succeed())
			})
			if state == "deleting" {
				response, err := subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
				Expect(err).NotTo(HaveOccurred())
				metadata := response.GetObject().GetMetadata()
				hasHoldFinalizer := false
				for _, finalizer := range metadata.GetFinalizers() {
					if finalizer == "test.osac.openshift.io/hold" {
						hasHoldFinalizer = true
						break
					}
				}
				if !hasHoldFinalizer {
					metadata.SetFinalizers(append(metadata.GetFinalizers(), "test.osac.openshift.io/hold"))
					_, err = subnets.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
						Object:     response.GetObject(),
						UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
					}.Build())
					Expect(err).NotTo(HaveOccurred())
				}
			}
			switch state {
			case "ready":
				setSubnetReady(ctx, subnets, id)
			case "pending":
				waitForSubnetPending(subnets, id)
			case "failed":
				waitForSubnetPending(subnets, id)
				resp, getErr := subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
				Expect(getErr).NotTo(HaveOccurred())
				subnet := resp.GetObject()
				subnet.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_FAILED)
				_, updateErr := subnets.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
					Object: subnet, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
				}.Build())
				Expect(updateErr).NotTo(HaveOccurred())
				Eventually(func(g Gomega) {
					current, err := subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(current.GetObject().GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_FAILED))
				}, time.Minute, time.Second).Should(Succeed())
			case "deleting":
				waitForSubnetPending(subnets, id)
				resp, getErr := subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
				Expect(getErr).NotTo(HaveOccurred())
				Expect(resp.GetObject().GetMetadata().GetFinalizers()).To(ContainElement("test.osac.openshift.io/hold"))
				_, deleteErr := subnets.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{Id: id}.Build())
				Expect(deleteErr).NotTo(HaveOccurred())
				Eventually(func(g Gomega) {
					current, err := subnets.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(current.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
					g.Expect(current.GetObject().GetMetadata().GetFinalizers()).To(ContainElement("test.osac.openshift.io/hold"))
				}, time.Minute, time.Second).Should(Succeed())
			}
			return id
		}

		newProjectSecurityGroup := func(project, virtualNetworkId string, defaultLabelled bool, state string) string {
			groups := privatev1.NewSecurityGroupsClient(tool.InternalView().ControllerConn())
			id := fmt.Sprintf("test-sg-%s", uuid.New())
			name := fmt.Sprintf("test-sg-%s", uuid.New()[24:32])
			resourceProject := project
			labels := map[string]string{}
			if defaultLabelled {
				resourceProject = ""
				name = "default"
				labels[tenantDefaultNetworkingLabel] = "true"
				setComputeInstanceFixtureVirtualNetworkReady(ctx, virtualNetworksClient, virtualNetworkId)
			}
			finalizers := []string{}
			if state == "deleting" {
				finalizers = []string{"test.osac.openshift.io/hold"}
			}
			annotations := map[string]string{"osac.openshift.io/management-state": "unmanaged"}
			if defaultLabelled {
				annotations["osac.openshift.io/owner-reference"] = virtualNetworkId
			}
			groupObject := privatev1.SecurityGroup_builder{
				Id: id,
				Metadata: privatev1.Metadata_builder{
					Name: name, Tenant: tenant, Project: resourceProject,
					Annotations: annotations, Labels: labels, Finalizers: finalizers,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
				}.Build(),
			}.Build()
			if defaultLabelled {
				filter := fmt.Sprintf(`this.metadata.tenant == %q && this.metadata.name == %q && `+
					`this.metadata.labels[%q] == "true" && this.spec.virtual_network.id == %q`,
					tenant, name, tenantDefaultNetworkingLabel, virtualNetworkId)
				existing, err := groups.List(ctx, privatev1.SecurityGroupsListRequest_builder{Filter: &filter}.Build())
				Expect(err).NotTo(HaveOccurred())
				if len(existing.GetItems()) > 0 {
					Expect(existing.GetItems()).To(HaveLen(1))
					id = existing.GetItems()[0].GetId()
				} else {
					_, err = groups.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{Object: groupObject}.Build())
					if grpcstatus.Code(err) == grpccodes.AlreadyExists {
						existing, err = groups.List(ctx,
							privatev1.SecurityGroupsListRequest_builder{Filter: &filter}.Build())
						Expect(err).NotTo(HaveOccurred())
						Expect(existing.GetItems()).To(HaveLen(1))
						id = existing.GetItems()[0].GetId()
					} else {
						Expect(err).NotTo(HaveOccurred())
					}
				}
			} else {
				_, err := groups.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{Object: groupObject}.Build())
				Expect(err).NotTo(HaveOccurred())
			}
			DeferCleanup(func(cleanupCtx context.Context) {
				Expect(cleanupSecurityGroupFixture(cleanupCtx, groups, id)).To(Succeed())
			})
			if state == "deleting" {
				response, err := groups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
				Expect(err).NotTo(HaveOccurred())
				metadata := response.GetObject().GetMetadata()
				hasHoldFinalizer := false
				for _, finalizer := range metadata.GetFinalizers() {
					if finalizer == "test.osac.openshift.io/hold" {
						hasHoldFinalizer = true
						break
					}
				}
				if !hasHoldFinalizer {
					metadata.SetFinalizers(append(metadata.GetFinalizers(), "test.osac.openshift.io/hold"))
					_, err = groups.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
						Object:     response.GetObject(),
						UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
					}.Build())
					Expect(err).NotTo(HaveOccurred())
				}
			}
			switch state {
			case "ready":
				setSecurityGroupReady(ctx, groups, id)
			case "pending":
				waitForSecurityGroupPending(groups, id)
			case "failed":
				waitForSecurityGroupPending(groups, id)
				resp, getErr := groups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
				Expect(getErr).NotTo(HaveOccurred())
				group := resp.GetObject()
				group.GetStatus().SetState(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED)
				_, updateErr := groups.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
					Object: group, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
				}.Build())
				Expect(updateErr).NotTo(HaveOccurred())
				Eventually(func(g Gomega) {
					current, err := groups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(current.GetObject().GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED))
				}, time.Minute, time.Second).Should(Succeed())
			case "deleting":
				waitForSecurityGroupPending(groups, id)
				resp, getErr := groups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
				Expect(getErr).NotTo(HaveOccurred())
				Expect(resp.GetObject().GetMetadata().GetFinalizers()).To(ContainElement("test.osac.openshift.io/hold"))
				_, deleteErr := groups.Delete(ctx, privatev1.SecurityGroupsDeleteRequest_builder{Id: id}.Build())
				Expect(deleteErr).NotTo(HaveOccurred())
				Eventually(func(g Gomega) {
					current, err := groups.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: id}.Build())
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(current.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
					g.Expect(current.GetObject().GetMetadata().GetFinalizers()).To(ContainElement("test.osac.openshift.io/hold"))
				}, time.Minute, time.Second).Should(Succeed())
			}
			return id
		}

		newProjectClusterRequest := func(project, clusterId string) *publicv1.ClustersCreateRequest {
			return publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: clusterId,
					Metadata: publicv1.Metadata_builder{
						Name:    fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
						Tenant:  tenant,
						Project: project,
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: templateId, Shared: true}.Build(),
						NodeSets: testClusterNodeSets(bmitName, 3),
					}.Build(),
				}.Build(),
			}.Build()
		}

		expectNoStoredCluster := func(clusterId string) {
			_, getErr := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{Id: clusterId}.Build())
			Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
			_, adminGetErr := privateClustersClient.Get(ctx, privatev1.ClustersGetRequest_builder{Id: clusterId}.Build())
			Expect(grpcstatus.Code(adminGetErr)).To(Equal(grpccodes.NotFound))
		}

		DescribeTable("classifies an omitted attachment with an unusable tenant default",
			func(configure func(project, virtualNetworkId string), code grpccodes.Code, reason string) {
				project := createCatalogItemProjectFixture(ctx, tenant)
				// The defaults live in the tenant's default project, as the default networking manager creates
				// them; the Cluster is in another project of the tenant.
				virtualNetworkId := newIsolatedVirtualNetwork(tenantDefaultProject, true)
				configure(tenantDefaultProject, virtualNetworkId)
				DeferCleanup(func(cleanupCtx context.Context) {
					setVirtualNetworkPending(cleanupCtx, virtualNetworkId)
				})
				Expect(setTenantDefaultNetworkingCondition(ctx, tenant, true)).To(Succeed())

				clusterId := fmt.Sprintf("test-cluster-%s", uuid.New())
				DeferCleanup(func(cleanupCtx context.Context) {
					Expect(cleanupClusterFixture(cleanupCtx, clustersClient, privateClustersClient, clusterId)).To(Succeed())
				})

				response, err := clustersClient.Create(ctx, newProjectClusterRequest(project, clusterId))
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(code))
				Expect(status.Message()).To(ContainSubstring(reason))
				expectNoStoredCluster(clusterId)
			},
			Entry("missing default Subnet",
				func(project, virtualNetworkId string) {},
				grpccodes.InvalidArgument, "no tenant default subnet is available"),
			Entry("pending default Subnet",
				func(project, virtualNetworkId string) {
					newProjectSubnet(project, virtualNetworkId, true, "pending")
					newProjectSecurityGroup(project, virtualNetworkId, true, "ready")
				}, grpccodes.FailedPrecondition, "SUBNET_STATE_PENDING"),
			Entry("failed default Subnet",
				func(project, virtualNetworkId string) {
					newProjectSubnet(project, virtualNetworkId, true, "failed")
				}, grpccodes.FailedPrecondition, "SUBNET_STATE_FAILED"),
			Entry("deleting default Subnet",
				func(project, virtualNetworkId string) {
					newProjectSubnet(project, virtualNetworkId, true, "deleting")
				}, grpccodes.FailedPrecondition, "is being deleted"),
			Entry("missing default SecurityGroup",
				func(project, virtualNetworkId string) {
					// Default SecurityGroups are tenant-scoped, so their decoy network must be too.
					decoyVirtualNetworkId := newIsolatedVirtualNetwork("", false)
					newProjectSecurityGroup(project, decoyVirtualNetworkId, true, "ready")
					newProjectSubnet(project, virtualNetworkId, true, "ready")
				}, grpccodes.InvalidArgument, "no tenant default security group is available"),
			Entry("pending default SecurityGroup",
				func(project, virtualNetworkId string) {
					newProjectSubnet(project, virtualNetworkId, true, "ready")
					newProjectSecurityGroup(project, virtualNetworkId, true, "pending")
				}, grpccodes.FailedPrecondition, "SECURITY_GROUP_STATE_PENDING"),
			Entry("failed default SecurityGroup",
				func(project, virtualNetworkId string) {
					newProjectSubnet(project, virtualNetworkId, true, "ready")
					newProjectSecurityGroup(project, virtualNetworkId, true, "failed")
				}, grpccodes.FailedPrecondition, "SECURITY_GROUP_STATE_FAILED"),
			Entry("deleting default SecurityGroup",
				func(project, virtualNetworkId string) {
					newProjectSubnet(project, virtualNetworkId, true, "ready")
					newProjectSecurityGroup(project, virtualNetworkId, true, "deleting")
				}, grpccodes.FailedPrecondition, "is being deleted"),
		)

		It("resolves ready tenant defaults and stores both references", func() {
			project := createCatalogItemProjectFixture(ctx, tenant)
			virtualNetworkId := newIsolatedVirtualNetwork(tenantDefaultProject, true)
			defaultSubnetId := newProjectSubnet(tenantDefaultProject, virtualNetworkId, true, "ready")
			defaultSecurityGroupId := newProjectSecurityGroup(tenantDefaultProject, virtualNetworkId, true, "ready")
			DeferCleanup(func(cleanupCtx context.Context) {
				setVirtualNetworkPending(cleanupCtx, virtualNetworkId)
			})

			clusterId := fmt.Sprintf("test-cluster-%s", uuid.New())
			DeferCleanup(func(cleanupCtx context.Context) {
				Expect(cleanupClusterFixture(cleanupCtx, clustersClient, privateClustersClient, clusterId)).To(Succeed())
			})

			var createResponse *publicv1.ClustersCreateResponse
			Eventually(func() error {
				if err := setTenantDefaultNetworkingCondition(ctx, tenant, true); err != nil {
					if grpcstatus.Code(err) != grpccodes.Aborted {
						return StopTrying("could not set Tenant networking readiness").Wrap(err)
					}
					return err
				}
				var err error
				createResponse, err = clustersClient.Create(ctx, newProjectClusterRequest(project, clusterId))
				if err != nil && (grpcstatus.Code(err) != grpccodes.FailedPrecondition ||
					grpcstatus.Convert(err).Message() != "tenant default networking is not ready") {
					return StopTrying("Cluster Create failed for a reason other than Tenant readiness").Wrap(err)
				}
				return err
			}, time.Minute, time.Second).Should(Succeed())
			object := createResponse.GetObject()

			spec := object.GetSpec()
			Expect(spec.GetNetworkAttachment()).ToNot(BeNil())
			Expect(spec.GetNetworkAttachment().GetSubnet().GetId()).To(Equal(defaultSubnetId))
			Expect(spec.GetNetworkAttachment().GetSecurityGroups()).To(HaveLen(1))
			Expect(spec.GetNetworkAttachment().GetSecurityGroups()[0].GetId()).To(Equal(defaultSecurityGroupId))

			getResponse, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			spec = getResponse.GetObject().GetSpec()
			Expect(spec.GetNetworkAttachment()).ToNot(BeNil())
			Expect(spec.GetNetworkAttachment().GetSubnet().GetId()).To(Equal(defaultSubnetId))
			Expect(spec.GetNetworkAttachment().GetSecurityGroups()).To(HaveLen(1))
			Expect(spec.GetNetworkAttachment().GetSecurityGroups()[0].GetId()).To(Equal(defaultSecurityGroupId))
		})

		It("preserves a complete explicit attachment without labelled defaults", func() {
			project := createCatalogItemProjectFixture(ctx, tenant)
			virtualNetworkId := newIsolatedVirtualNetwork(project, false)
			explicitSubnetId := newProjectSubnet(project, virtualNetworkId, false, "ready")
			explicitSecurityGroupId := newProjectSecurityGroup(project, virtualNetworkId, false, "ready")

			clusterId := fmt.Sprintf("test-cluster-%s", uuid.New())
			DeferCleanup(func(cleanupCtx context.Context) {
				Expect(cleanupClusterFixture(cleanupCtx, clustersClient, privateClustersClient, clusterId)).To(Succeed())
			})

			request := newProjectClusterRequest(project, clusterId)
			request.GetObject().GetSpec().SetNetworkAttachment(publicv1.ClusterNetworkAttachment_builder{
				Subnet: publicv1.SubnetLocalReference_builder{Id: explicitSubnetId}.Build(),
				SecurityGroups: []*publicv1.SecurityGroupLocalReference{
					publicv1.SecurityGroupLocalReference_builder{Id: explicitSecurityGroupId}.Build(),
				},
			}.Build())

			createResponse, err := clustersClient.Create(ctx, request)
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			spec := object.GetSpec()
			Expect(spec.GetNetworkAttachment().GetSubnet().GetId()).To(Equal(explicitSubnetId))
			Expect(spec.GetNetworkAttachment().GetSecurityGroups()).To(HaveLen(1))
			Expect(spec.GetNetworkAttachment().GetSecurityGroups()[0].GetId()).To(Equal(explicitSecurityGroupId))

			getResponse, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			spec = getResponse.GetObject().GetSpec()
			Expect(spec.GetNetworkAttachment().GetSubnet().GetId()).To(Equal(explicitSubnetId))
			Expect(spec.GetNetworkAttachment().GetSecurityGroups()).To(HaveLen(1))
			Expect(spec.GetNetworkAttachment().GetSecurityGroups()[0].GetId()).To(Equal(explicitSecurityGroupId))
		})
	})

	It("Rejects cluster creation when the referenced subnet is not in READY state", func() {
		// Create a second subnet in the same VirtualNetwork but leave it in PENDING state:
		pendingSubnetId := fmt.Sprintf("test-pending-subnet-%s", uuid.New())
		_, err := subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-pending-sub-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Id: pendingSubnetId,
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
					Ipv4Cidr:       new("10.100.2.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			// Subnet deletion is rejected while any SecurityGroup references its VirtualNetwork.
			Expect(cleanupSecurityGroupFixture(cleanupCtx, securityGroupsClient, securityGroupId)).To(Succeed())
			Expect(cleanupSubnetFixture(cleanupCtx, subnetsClient, pendingSubnetId)).To(Succeed())
		})

		// Wait for the subnet to reach PENDING so the server can find it:
		Eventually(func(g Gomega) {
			probeCtx, cancel := context.WithTimeout(ctx, computeInstanceFixtureProbeTimeout)
			defer cancel()
			resp, err := subnetsClient.Get(probeCtx, privatev1.SubnetsGetRequest_builder{Id: pendingSubnetId}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.SubnetState_SUBNET_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())

		// Attempt to create a cluster referencing the non-READY subnet:
		_, err = clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NodeSets: testClusterNodeSets(bmitName, 3),
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: pendingSubnetId}.Build(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
		Expect(status.Message()).To(ContainSubstring("not in READY state"))
	})

	It("Rejects cluster creation when the referenced subnet does not exist", func() {
		_, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NodeSets: testClusterNodeSets(bmitName, 3),
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: "nonexistent-subnet-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("not found"))
	})

	It("Rejects cluster creation when a security group belongs to a different virtual network", func() {
		// Create a second VirtualNetwork:
		crossVnId := fmt.Sprintf("test-cross-vnet-%s", uuid.New())
		_, err := virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-cross-vnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Id: crossVnId,
				Spec: privatev1.VirtualNetworkSpec_builder{
					Ipv4Cidr: new("10.200.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupVirtualNetworkFixture(cleanupCtx, virtualNetworksClient, crossVnId)).To(Succeed())
		})

		// Set the second VirtualNetwork to READY:
		setComputeInstanceFixtureVirtualNetworkReady(ctx, virtualNetworksClient, crossVnId)

		// Create a SecurityGroup in the second VirtualNetwork:
		crossSgResp, err := securityGroupsClient.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-cross-sg-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: crossVnId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		crossSgId := crossSgResp.GetObject().GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupSecurityGroupFixture(cleanupCtx, securityGroupsClient, crossSgId)).To(Succeed())
		})

		// Wait for the cross-VN security group to reach PENDING, then set to READY:
		setSecurityGroupReady(ctx, securityGroupsClient, crossSgId)

		// Attempt to create a cluster with subnet from VN-A and security group from VN-B:
		_, err = clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NodeSets: testClusterNodeSets(bmitName, 3),
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build(),
						SecurityGroups: []*publicv1.SecurityGroupLocalReference{
							publicv1.SecurityGroupLocalReference_builder{Id: crossSgId}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("different virtual network"))
	})

	It("Rejects updating the subnet in network attachment after cluster creation", func() {
		// Prepare the replacement before cluster creation so cluster state changes
		// cannot race the immutability assertion.
		otherSubnetId := fmt.Sprintf("test-other-subnet-%s", uuid.New())
		_, err := subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-other-sub-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Id: otherSubnetId,
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
					Ipv4Cidr:       new("10.100.3.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupSecurityGroupFixture(cleanupCtx, securityGroupsClient, securityGroupId)).To(Succeed())
			Expect(cleanupSubnetFixture(cleanupCtx, subnetsClient, otherSubnetId)).To(Succeed())
		})

		// Wait for the second subnet to reach PENDING, then set to READY:
		setSubnetReady(ctx, subnetsClient, otherSubnetId)

		// Create a cluster with the shared subnet:
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NodeSets: testClusterNodeSets(bmitName, 3),
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build(),
						SecurityGroups: []*publicv1.SecurityGroupLocalReference{
							publicv1.SecurityGroupLocalReference_builder{Id: securityGroupId}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupClusterFixture(cleanupCtx, clustersClient, privateClustersClient, object.GetId())).To(Succeed())
		})

		// Attempt to update the cluster's network attachment subnet:
		_, err = clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: publicv1.Cluster_builder{
				Id: object.GetId(),
				Metadata: publicv1.Metadata_builder{
					Name: object.GetMetadata().GetName(),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NodeSets: testClusterNodeSets(bmitName, 3),
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: otherSubnetId}.Build(),
						SecurityGroups: []*publicv1.SecurityGroupLocalReference{
							publicv1.SecurityGroupLocalReference_builder{Id: securityGroupId}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.network_attachment.subnet"}},
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("subnet is immutable"))
	})

	It("Rejects updating the security groups in network attachment after cluster creation", func() {
		By("creating a replacement security group before the cluster")
		otherSgResp, err := securityGroupsClient.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-sg-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		otherSgId := otherSgResp.GetObject().GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupSecurityGroupFixture(cleanupCtx, securityGroupsClient, otherSgId)).To(Succeed())
		})
		setSecurityGroupReady(ctx, securityGroupsClient, otherSgId)

		By("creating a cluster with a subnet and security group attachment")
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NodeSets: testClusterNodeSets(bmitName, 3),
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build(),
						SecurityGroups: []*publicv1.SecurityGroupLocalReference{
							publicv1.SecurityGroupLocalReference_builder{Id: securityGroupId}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupClusterFixture(
				cleanupCtx, clustersClient, privateClustersClient, object.GetId(),
			)).To(Succeed())
		})

		By("attempting to replace the attachment security groups")

		_, err = clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: publicv1.Cluster_builder{
				Id: object.GetId(),
				Metadata: publicv1.Metadata_builder{
					Name: object.GetMetadata().GetName(),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NodeSets: testClusterNodeSets(bmitName, 3),
					NetworkAttachment: publicv1.ClusterNetworkAttachment_builder{
						Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build(),
						SecurityGroups: []*publicv1.SecurityGroupLocalReference{
							publicv1.SecurityGroupLocalReference_builder{Id: otherSgId}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.network_attachment.security_groups"}},
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("security groups are immutable"))

		By("confirming the stored attachment is unchanged")
		stored, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{Id: object.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		attachment := stored.GetObject().GetSpec().GetNetworkAttachment()
		Expect(attachment.GetSubnet().GetId()).To(Equal(subnetId))
		Expect(attachment.GetSecurityGroups()).To(HaveLen(1))
		Expect(attachment.GetSecurityGroups()[0].GetId()).To(Equal(securityGroupId))
	})

})
