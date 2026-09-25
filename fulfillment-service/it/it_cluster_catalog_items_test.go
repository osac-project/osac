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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var _ = Describe("Cluster Catalog Items", Label("catalog-items"), func() {
	Context("Provisioning and field governance", func() {
		It("resolves typed fields and atomic node maps with locked governance policies", func(ctx context.Context) {
			By("authoring a tenant offering with network and node-set policies")
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			secret := createCatalogItemPullSecretFixture(ctx, usersGroup)
			host := createCatalogItemHostTypeFixture(ctx)
			extraBmit := createCatalogItemBareMetalInstanceTypeFixture(ctx, "shared")
			version := createCatalogItemClusterVersionFixture(ctx, "4.20.0")
			overrideVersion := createCatalogItemClusterVersionFixture(ctx, "4.21.0")
			template := createCatalogItemClusterTemplateFixture(ctx, host, privatev1.ClusterTemplateSpecDefaults_builder{
				Network: privatev1.ClusterNetwork_builder{PodCidr: new("10.128.0.0/14"), ServiceCidr: new("172.30.0.0/16")}.Build(),
			}.Build(), clusterCatalogItemParameterDefinitions())
			fields := publicv1.ClusterCatalogItemFields_builder{
				Version: publicv1.ClusterVersionReferenceFieldPolicy_builder{
					Editable: publicv1.EditableClusterVersionReferenceField_builder{
						DefaultValue: publicv1.ClusterVersionReference_builder{Id: version}.Build(),
					}.Build(),
				}.Build(),
				SshPublicKey: publicv1.StringFieldPolicy_builder{
					Editable: publicv1.EditableStringField_builder{DefaultValue: new(catalogItemFixtureSSHPublicKey)}.Build(),
				}.Build(),
				PullSecretSecret: publicv1.SecretReferenceFieldPolicy_builder{
					Locked: publicv1.SecretLocalReference_builder{Id: secret}.Build(),
				}.Build(),
				Network: publicv1.ClusterNetworkFieldPolicies_builder{
					PodCidr: publicv1.StringFieldPolicy_builder{Locked: new("10.132.1.1/14")}.Build(),
					ServiceCidr: publicv1.StringFieldPolicy_builder{
						Editable: publicv1.EditableStringField_builder{DefaultValue: new("172.31.0.0/16")}.Build(),
					}.Build(),
				}.Build(),
				NetworkAttachment: publicv1.ClusterNetworkAttachmentFieldPolicy_builder{Locked: network.clusterAttachment()}.Build(),

				NodeSets: publicv1.ClusterNodeSetMapPolicy_builder{
					Editable: publicv1.EditableClusterNodeSetMap_builder{
						DefaultValue: publicv1.ClusterNodeSetMap_builder{
							Items: map[string]*publicv1.ClusterTemplateNodeSet{
								"workers": publicv1.ClusterTemplateNodeSet_builder{Size: 4}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build(),
				AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{Locked: new(false)}.Build(),
			}.Build()

			items := publicv1.NewClusterCatalogItemsClient(tool.ExternalView().AdminConn())
			item := createClusterCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ClusterCatalogItem_builder{
				Metadata:           publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
				Template:           publicv1.ClusterTemplateReference_builder{Id: template}.Build(),
				Published:          true,
				Fields:             fields,
				TemplateParameters: catalogItemParameterPolicies(),
			}.Build())
			Expect(item.GetFields().GetNetwork().GetPodCidr().GetLocked()).To(Equal("10.132.0.0/14"))

			By("creating a cluster with caller-selected version and node set")
			request := publicv1.ClusterSpec_builder{
				Version:     publicv1.ClusterVersionReference_builder{Id: overrideVersion}.Build(),
				CatalogItem: publicv1.ClusterCatalogItemReference_builder{Id: item.GetId()}.Build(),
				Network:     publicv1.ClusterNetwork_builder{ServiceCidr: new("172.32.0.0/16")}.Build(),
				NodeSets: map[string]*publicv1.ClusterNodeSet{
					"extra": publicv1.ClusterNodeSet_builder{
						BaremetalInstanceType: publicv1.BareMetalInstanceTypeLocalReference_builder{Id: extraBmit}.Build(),
						Size:                  new(int32(3)),
					}.Build(),
				},
				TemplateParameters: map[string]*anypb.Any{"size": catalogItemParameterValue(wrapperspb.Int32(0))},
			}.Build()
			created, e := createClusterFixture(ctx, tool.ExternalView().UserConn(), request)
			Expect(e).NotTo(HaveOccurred())

			By("checking the stored cluster's policy resolution and Template authority")
			client := publicv1.NewClustersClient(tool.ExternalView().UserConn())
			persisted, e := client.Get(ctx, publicv1.ClustersGetRequest_builder{Id: created.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			spec := persisted.GetObject().GetSpec()
			Expect(spec.GetVersion().GetId()).To(Equal(overrideVersion))

			Expect(spec.GetNetwork().GetPodCidr()).To(Equal("10.132.0.0/14"))
			Expect(spec.GetNetwork().GetServiceCidr()).To(Equal("172.32.0.0/16"))
			Expect(spec.GetSshPublicKey()).To(Equal(catalogItemFixtureSSHPublicKey))
			Expect(spec.GetPullSecretSecret().GetId()).To(Equal(secret))
			Expect(spec.GetNetworkAttachment().GetSubnet().GetId()).To(Equal(network.subnetID))
			Expect(spec.GetNetworkAttachment().GetSecurityGroups()[0].GetId()).To(Equal(network.securityGroupID))
			Expect(spec.GetNodeSets()).To(HaveLen(1))
			Expect(spec.GetNodeSets()).NotTo(HaveKey("workers"))
			Expect(spec.HasAutoExternalIpAttachment()).To(BeTrue())
			Expect(spec.GetAutoExternalIpAttachment()).To(BeFalse())
			Expect(spec.GetTemplate().GetId()).To(Equal(template))
			Expect(proto.Equal(spec.GetTemplateParameters()["enabled"], catalogItemParameterValue(wrapperspb.Bool(false)))).To(BeTrue())
			Expect(proto.Equal(spec.GetTemplateParameters()["size"], catalogItemParameterValue(wrapperspb.Int32(0)))).To(BeTrue())
			Expect(proto.Equal(spec.GetTemplateParameters()["ordinary"], catalogItemParameterValue(wrapperspb.String("template")))).To(BeTrue())
			By("materializing the omitted map default")
			defaulted, e := createClusterFixture(ctx, tool.ExternalView().UserConn(), publicv1.ClusterSpec_builder{
				CatalogItem: publicv1.ClusterCatalogItemReference_builder{Id: item.GetId()}.Build(),
				NodeSets:    map[string]*publicv1.ClusterNodeSet{},
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			persistedDefaults, e := client.Get(ctx, publicv1.ClustersGetRequest_builder{Id: defaulted.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			defaulted = persistedDefaults.GetObject()
			Expect(defaulted.GetSpec().GetVersion().GetId()).To(Equal(version))
			Expect(proto.Equal(defaulted.GetSpec().GetTemplateParameters()["size"], catalogItemParameterValue(wrapperspb.Int32(20)))).To(BeTrue())
			Expect(defaulted.GetSpec().GetNodeSets()["workers"].GetSize()).To(Equal(int32(4)))

			By("rejecting caller inputs that conflict with locked policies")
			type invalidInput struct {
				name string
				set  func(*publicv1.ClusterSpec)
			}
			inputs := []invalidInput{
				{
					name: "identical locked CIDR",
					set: func(s *publicv1.ClusterSpec) {
						s.GetNetwork().SetPodCidr("10.132.0.0/14")
					},
				},
				{
					name: "identical locked Secret",
					set: func(s *publicv1.ClusterSpec) {
						s.SetPullSecretSecret(publicv1.SecretLocalReference_builder{Id: secret}.Build())
					},
				},
				{
					name: "identical locked attachment",
					set: func(s *publicv1.ClusterSpec) {
						s.SetNetworkAttachment(network.clusterAttachment())
					},
				},
				{
					name: "explicit false",
					set: func(s *publicv1.ClusterSpec) {
						s.SetAutoExternalIpAttachment(false)
					},
				},
				{
					name: "invalid numeric zero",
					set: func(s *publicv1.ClusterSpec) {
						s.GetNodeSets()["extra"].SetSize(0)
					},
				},
				{
					name: "locked parameter",
					set: func(s *publicv1.ClusterSpec) {
						s.GetTemplateParameters()["enabled"] = catalogItemParameterValue(wrapperspb.Bool(false))
					},
				},
			}
			for _, input := range inputs {
				By(input.name)
				s := proto.Clone(request).(*publicv1.ClusterSpec)
				input.set(s)
				_, e = createClusterFixture(ctx, tool.ExternalView().UserConn(), s)
				expectCatalogItemStatusCode(e, codes.InvalidArgument)
			}
			By("switching the whole-map policy to locked and rejecting identical caller input")
			locked := publicv1.ClusterNodeSetMap_builder{
				Items: map[string]*publicv1.ClusterTemplateNodeSet{
					"workers": publicv1.ClusterTemplateNodeSet_builder{Size: 4}.Build(),
				},
			}.Build()
			_, e = items.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Id: item.GetId(),
					Fields: publicv1.ClusterCatalogItemFields_builder{
						NodeSets: publicv1.ClusterNodeSetMapPolicy_builder{Locked: locked}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields.node_sets"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			_, e = createClusterFixture(ctx, tool.ExternalView().UserConn(), publicv1.ClusterSpec_builder{
				CatalogItem: publicv1.ClusterCatalogItemReference_builder{Id: item.GetId()}.Build(),
				NodeSets: map[string]*publicv1.ClusterNodeSet{
					"workers": publicv1.ClusterNodeSet_builder{Size: new(int32(4))}.Build(),
				},
			}.Build())
			expectCatalogItemStatusCode(e, codes.InvalidArgument)
		})
		It("falls through to Template and system values and supports dry-run and direct creation", func(ctx context.Context) {
			By("authoring a shared offering with Template and catalog defaults")
			host := createCatalogItemHostTypeFixture(ctx)
			version := createCatalogItemClusterVersionFixture(ctx, "4.20.0")
			template := createCatalogItemClusterTemplateFixture(ctx, host, privatev1.ClusterTemplateSpecDefaults_builder{
				Version:      privatev1.ClusterVersionReference_builder{Id: version}.Build(),
				SshPublicKey: new(catalogItemFixtureSSHPublicKey),
			}.Build(), nil)
			item := createClusterCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ClusterCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.ClusterTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ClusterCatalogItemFields_builder{
					Version: publicv1.ClusterVersionReferenceFieldPolicy_builder{
						Editable: publicv1.EditableClusterVersionReferenceField_builder{}.Build(),
					}.Build(),
					AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{
						Editable: publicv1.EditableBoolField_builder{DefaultValue: new(true)}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			client := publicv1.NewClustersClient(tool.ExternalView().UserConn())
			networks := privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
			subnets := privatev1.NewSubnetsClient(tool.InternalView().AdminConn())
			beforeNetworks, err := networks.List(ctx, privatev1.VirtualNetworksListRequest_builder{}.Build())
			Expect(err).NotTo(HaveOccurred())
			beforeSubnets, err := subnets.List(ctx, privatev1.SubnetsListRequest_builder{}.Build())
			Expect(err).NotTo(HaveOccurred())
			dry := metadata.AppendToOutgoingContext(ctx, "x-dry-run", "true")
			name := catalogItemFixtureName()
			By("validating a dry-run cluster without storing network resources")
			result, e := client.Create(dry, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{Name: name}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						CatalogItem: publicv1.ClusterCatalogItemReference_builder{Id: item.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(result.GetObject().GetSpec().GetVersion().GetId()).To(Equal(version))
			Expect(result.GetObject().GetSpec().GetSshPublicKey()).To(Equal(catalogItemFixtureSSHPublicKey))
			listed, e := client.List(ctx, publicv1.ClustersListRequest_builder{Filter: new("this.metadata.name == '" + name + "'")}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(listed.GetItems()).To(BeEmpty())
			afterNetworks, err := networks.List(ctx, privatev1.VirtualNetworksListRequest_builder{}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(afterNetworks.GetTotal()).To(Equal(beforeNetworks.GetTotal()))
			afterSubnets, err := subnets.List(ctx, privatev1.SubnetsListRequest_builder{}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(afterSubnets.GetTotal()).To(Equal(beforeSubnets.GetTotal()))
			By("rejecting a request that names both the offering and its Template")
			_, e = client.Create(dry, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template:    publicv1.ClusterTemplateReference_builder{Id: template}.Build(),
						CatalogItem: publicv1.ClusterCatalogItemReference_builder{Id: item.GetId()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			expectCatalogItemStatusCode(e, codes.InvalidArgument)
			By("rejecting creation without a Catalog Item or Template")
			_, e = client.Create(dry, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec:     publicv1.ClusterSpec_builder{}.Build(),
				}.Build(),
			}.Build())
			expectCatalogItemStatusCode(e, codes.InvalidArgument)
			By("creating directly from a Template with its version default")
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			direct, e := createClusterFixture(ctx, tool.ExternalView().UserConn(), publicv1.ClusterSpec_builder{
				Template:          publicv1.ClusterTemplateReference_builder{Id: template}.Build(),
				NetworkAttachment: network.clusterAttachment(),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(direct.GetSpec().HasCatalogItem()).To(BeFalse())
			Expect(direct.GetSpec().GetVersion().GetId()).To(Equal(version))
			By("falling back to the system version without an authored default")
			systemTemplate := createCatalogItemClusterTemplateFixture(ctx, host, nil, nil)
			system, e := createClusterFixture(ctx, tool.ExternalView().UserConn(), publicv1.ClusterSpec_builder{
				Template:          publicv1.ClusterTemplateReference_builder{Id: systemTemplate}.Build(),
				NetworkAttachment: network.clusterAttachment(),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(system.GetSpec().GetVersion().GetId()).NotTo(BeEmpty())
		})

		It("validates caller compatibility and checks readiness again after policy materialization", func(ctx context.Context) {
			template := createCatalogItemClusterTemplateFixture(ctx, createCatalogItemHostTypeFixture(ctx), nil, nil)
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			other := createCatalogItemNetworkInClassFixture(ctx, usersGroup, "", network.networkClassID)
			item := createClusterCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ClusterCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
				Template:  publicv1.ClusterTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ClusterCatalogItemFields_builder{
					NetworkAttachment: publicv1.ClusterNetworkAttachmentFieldPolicy_builder{
						Editable: publicv1.EditableClusterNetworkAttachmentField_builder{DefaultValue: network.clusterAttachment()}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			bad := network.clusterAttachment()
			bad.SetSecurityGroups([]*publicv1.SecurityGroupLocalReference{
				publicv1.SecurityGroupLocalReference_builder{Id: other.securityGroupID}.Build(),
			})
			client := publicv1.NewClustersClient(tool.ExternalView().UserConn())
			request := publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						CatalogItem:       publicv1.ClusterCatalogItemReference_builder{Id: item.GetId()}.Build(),
						NetworkAttachment: bad,
					}.Build(),
				}.Build(),
			}.Build()
			dry := metadata.AppendToOutgoingContext(ctx, "x-dry-run", "true")
			_, err := client.Create(dry, request)
			expectCatalogItemStatusCode(err, codes.InvalidArgument)
			request.GetObject().GetSpec().ClearNetworkAttachment()
			setCatalogItemSubnetFixtureState(ctx, network.subnetID, privatev1.SubnetState_SUBNET_STATE_PENDING)
			_, err = client.Create(dry, request)
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)
		})
	})
	Context("Template parameters", func() {
		It("distinguishes editable required input from Template defaults and invalid values", func(ctx context.Context) {
			By("publishing a cluster offering with a required editable parameter")
			template := createCatalogItemClusterTemplateFixture(ctx, createCatalogItemHostTypeFixture(ctx), nil, clusterCatalogItemParameterDefinitions())
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			item := createClusterCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ClusterCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.ClusterTemplateReference_builder{Id: template}.Build(),
				Published: true,
				TemplateParameters: map[string]*publicv1.TemplateParameterPolicy{
					"enabled": publicv1.TemplateParameterPolicy_builder{
						Editable: publicv1.EditableTemplateParameter_builder{}.Build(),
					}.Build(),
					"size": publicv1.TemplateParameterPolicy_builder{
						Editable: publicv1.EditableTemplateParameter_builder{}.Build(),
					}.Build(),
				},
			}.Build())
			client := publicv1.NewClustersClient(tool.ExternalView().UserConn())
			request := publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						CatalogItem:       publicv1.ClusterCatalogItemReference_builder{Id: item.GetId()}.Build(),
						NetworkAttachment: network.clusterAttachment(),
					}.Build(),
				}.Build(),
			}.Build()
			dry := metadata.AppendToOutgoingContext(ctx, "x-dry-run", "true")

			By("rejecting a cluster request that omits the required parameter")
			_, err := client.Create(dry, request)
			expectCatalogItemStatusCode(err, codes.InvalidArgument)

			By("accepting caller input and applying the Template size default")
			request.GetObject().GetSpec().SetTemplateParameters(map[string]*anypb.Any{
				"enabled":  catalogItemParameterValue(wrapperspb.Bool(false)),
				"ordinary": catalogItemParameterValue(wrapperspb.String("user")),
			})
			response, err := client.Create(dry, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(response.GetObject().GetSpec().GetTemplateParameters()["size"], catalogItemParameterValue(wrapperspb.Int32(10)))).To(BeTrue())
			Expect(proto.Equal(response.GetObject().GetSpec().GetTemplateParameters()["ordinary"], catalogItemParameterValue(wrapperspb.String("user")))).To(BeTrue())

			By("rejecting malformed and unknown Template parameters")
			for _, tc := range []struct {
				name       string
				parameters map[string]*anypb.Any
			}{
				{
					name: "wrong parameter type",
					parameters: map[string]*anypb.Any{
						"enabled": catalogItemParameterValue(wrapperspb.String("false")),
					},
				},
				{
					name: "unknown parameter",
					parameters: map[string]*anypb.Any{
						"enabled": catalogItemParameterValue(wrapperspb.Bool(false)),
						"unknown": catalogItemParameterValue(wrapperspb.Bool(true)),
					},
				},
			} {
				By(tc.name)
				request.GetObject().GetSpec().SetTemplateParameters(tc.parameters)
				_, err = client.Create(dry, request)
				expectCatalogItemStatusCode(err, codes.InvalidArgument)
			}
		})
	})

	Context("Authoring and publication", func() {
		It("lets a Tenant Admin publish governed items for members of that tenant", func(ctx context.Context) {
			By("creating a tenant-owned draft through the Tenant Admin public API")
			version := createCatalogItemClusterVersionFixture(ctx, "4.20.0")
			template := createCatalogItemClusterTemplateFixture(ctx, createCatalogItemHostTypeFixture(ctx), privatev1.ClusterTemplateSpecDefaults_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: version}.Build(),
			}.Build(), nil)
			tenant, conn := createCatalogItemTenantAdminFixture(ctx)
			items := publicv1.NewClusterCatalogItemsClient(conn)
			owned := createClusterCatalogItemFixture(ctx, conn, publicv1.ClusterCatalogItem_builder{
				Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template: publicv1.ClusterTemplateReference_builder{Id: template}.Build(),
			}.Build())
			Expect(owned.GetMetadata().GetTenant()).To(Equal(tenant), "tenant authoring scope")

			By("publishing governed fields for members of the tenant")
			memberConn := createCatalogItemMemberFixture(ctx, tenant)
			memberItems := publicv1.NewClusterCatalogItemsClient(memberConn)
			_, err := items.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{Id: owned.GetId(), Published: true,
					Fields: publicv1.ClusterCatalogItemFields_builder{
						SshPublicKey:             publicv1.StringFieldPolicy_builder{Editable: publicv1.EditableStringField_builder{DefaultValue: new(catalogItemFixtureSSHPublicKey)}.Build()}.Build(),
						AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{Locked: new(false)}.Build(),
					}.Build(),
				}.Build(), UpdateMask: catalogItemUpdateMask("published", "fields"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			visible, err := memberItems.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{Id: owned.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(visible.GetObject().GetFields().GetSshPublicKey().GetEditable().GetDefaultValue()).To(Equal(catalogItemFixtureSSHPublicKey))
			Expect(visible.GetObject().GetFields().GetAutoExternalIpAttachment().HasLocked()).To(BeTrue())
			Expect(visible.GetObject().GetFields().GetAutoExternalIpAttachment().GetLocked()).To(BeFalse())

			By("provisioning a cluster as a member with an editable override")
			network := createCatalogItemNetworkFixture(ctx, tenant, "")
			spec := publicv1.ClusterSpec_builder{
				CatalogItem:       publicv1.ClusterCatalogItemReference_builder{Id: owned.GetId()}.Build(),
				SshPublicKey:      new(catalogItemFixtureSSHPublicKey + " member"),
				NetworkAttachment: network.clusterAttachment(),
			}.Build()
			created, err := createClusterFixture(ctx, memberConn, spec)
			Expect(err).NotTo(HaveOccurred())
			objects := publicv1.NewClustersClient(memberConn)
			stored, err := objects.Get(ctx, publicv1.ClustersGetRequest_builder{Id: created.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(stored.GetObject().GetMetadata().GetTenant()).To(Equal(tenant))
			Expect(stored.GetObject().GetSpec().GetSshPublicKey()).To(Equal(catalogItemFixtureSSHPublicKey + " member"))
			Expect(stored.GetObject().GetSpec().HasAutoExternalIpAttachment()).To(BeTrue())
			Expect(stored.GetObject().GetSpec().GetAutoExternalIpAttachment()).To(BeFalse())

			By("hiding the tenant offering from an unrelated tenant")
			outsider := publicv1.NewClusterCatalogItemsClient(tool.ExternalView().UserConn())
			_, err = outsider.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{Id: owned.GetId()}.Build())
			expectCatalogItemStatusCode(err, codes.NotFound)
			hidden, err := outsider.List(ctx, publicv1.ClusterCatalogItemsListRequest_builder{Filter: new("this.id == '" + owned.GetId() + "'")}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(hidden.GetItems()).To(BeEmpty())
			_, err = createClusterFixture(ctx, tool.ExternalView().UserConn(), publicv1.ClusterSpec_builder{
				CatalogItem: publicv1.ClusterCatalogItemReference_builder{Id: owned.GetId()}.Build(),
			}.Build())
			expectCatalogItemStatusCode(err, codes.NotFound)

			By("denying catalog deletion to a tenant member")
			_, err = memberItems.Delete(ctx, publicv1.ClusterCatalogItemsDeleteRequest_builder{Id: owned.GetId()}.Build())
			expectCatalogItemStatusCode(err, codes.PermissionDenied)

			By("stopping new member provisioning after unpublishing")
			_, err = items.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object:     publicv1.ClusterCatalogItem_builder{Id: owned.GetId(), Published: false}.Build(),
				UpdateMask: catalogItemUpdateMask("published"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = createClusterFixture(ctx, memberConn, spec)
			expectCatalogItemStatusCode(err, codes.NotFound)
		})

		It("lists shared published items and protects provider authoring", func(ctx context.Context) {
			template := createCatalogItemClusterTemplateFixture(ctx, createCatalogItemHostTypeFixture(ctx), nil, nil)
			_, conn := createCatalogItemTenantAdminFixture(ctx)
			items := publicv1.NewClusterCatalogItemsClient(conn)
			user := publicv1.NewClusterCatalogItemsClient(tool.ExternalView().UserConn())
			shared := createClusterCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ClusterCatalogItem_builder{
				Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template: publicv1.ClusterTemplateReference_builder{Id: template}.Build(),
			}.Build())
			Expect(shared.GetMetadata().GetTenant()).To(Equal("shared"))
			_, err := user.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{Id: shared.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = items.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object:     publicv1.ClusterCatalogItem_builder{Id: shared.GetId(), Title: "Foreign edit"}.Build(),
				UpdateMask: catalogItemUpdateMask("title"),
			}.Build())
			expectCatalogItemStatusCode(err, codes.PermissionDenied)
			listed, err := user.List(ctx, publicv1.ClusterCatalogItemsListRequest_builder{Filter: new("this.id == '" + shared.GetId() + "' && this.published")}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetItems()).To(BeEmpty())
			provider := publicv1.NewClusterCatalogItemsClient(tool.ExternalView().AdminConn())
			_, err = provider.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object:     publicv1.ClusterCatalogItem_builder{Id: shared.GetId(), Published: true}.Build(),
				UpdateMask: catalogItemUpdateMask("published"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			listed, err = user.List(ctx, publicv1.ClusterCatalogItemsListRequest_builder{Filter: new("this.id == '" + shared.GetId() + "' && this.published")}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetItems()).To(HaveLen(1))
		})
		It("keeps unmasked policies and atomically rejects invalid merged candidates", func(ctx context.Context) {
			By("authoring an offering with two field policies")
			template := createCatalogItemClusterTemplateFixture(ctx, createCatalogItemHostTypeFixture(ctx), nil, nil)
			item := createClusterCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ClusterCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.ClusterTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ClusterCatalogItemFields_builder{
					SshPublicKey:             publicv1.StringFieldPolicy_builder{Locked: new(catalogItemFixtureSSHPublicKey)}.Build(),
					AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{Locked: new(false)}.Build(),
				}.Build(),
			}.Build())
			client := publicv1.NewClusterCatalogItemsClient(tool.ExternalView().AdminConn())

			By("editing one policy while retaining the other")
			_, err := client.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Id: item.GetId(),
					Fields: publicv1.ClusterCatalogItemFields_builder{
						SshPublicKey: publicv1.StringFieldPolicy_builder{
							Editable: publicv1.EditableStringField_builder{DefaultValue: new(catalogItemFixtureSSHPublicKey + " edited")}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields.ssh_public_key"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			before, err := client.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(before.GetObject().GetFields().GetAutoExternalIpAttachment().HasLocked()).To(BeTrue())
			Expect(before.GetObject().GetFields().GetAutoExternalIpAttachment().GetLocked()).To(BeFalse())
			Expect(before.GetObject().GetFields().GetSshPublicKey().HasLocked()).To(BeFalse())
			Expect(before.GetObject().GetFields().GetSshPublicKey().GetEditable().GetDefaultValue()).To(Equal(catalogItemFixtureSSHPublicKey + " edited"))

			By("rejecting an invalid policy without changing the offering title")
			_, err = client.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Id:    item.GetId(),
					Title: "must not persist",
					Fields: publicv1.ClusterCatalogItemFields_builder{
						SshPublicKey: publicv1.StringFieldPolicy_builder{}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("title",
					"fields.ssh_public_key"),
			}.Build())
			expectCatalogItemStatusCode(err, codes.InvalidArgument)
			after, err := client.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(before.GetObject(), after.GetObject())).To(BeTrue())

			By("clearing all field policies explicitly")
			_, err = client.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Id:     item.GetId(),
					Fields: publicv1.ClusterCatalogItemFields_builder{}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			after, err = client.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(after.GetObject().GetFields().GetSshPublicKey()).To(BeNil())
			Expect(after.GetObject().GetFields().GetAutoExternalIpAttachment()).To(BeNil())
		})
		DescribeTable("rejects invalid governed node maps without changing the stored catalog item", func(ctx context.Context, invalidPolicy func(context.Context) *publicv1.ClusterNodeSetMapPolicy) {
			host := createCatalogItemHostTypeFixture(ctx)
			template := createCatalogItemClusterTemplateFixture(ctx, host, nil, nil)
			item := createClusterCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ClusterCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
				Template:  publicv1.ClusterTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ClusterCatalogItemFields_builder{
					NodeSets: publicv1.ClusterNodeSetMapPolicy_builder{
						Locked: publicv1.ClusterNodeSetMap_builder{
							Items: map[string]*publicv1.ClusterTemplateNodeSet{
								"workers": publicv1.ClusterTemplateNodeSet_builder{Size: 4}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			client := publicv1.NewClusterCatalogItemsClient(tool.ExternalView().AdminConn())
			before, err := client.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = client.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Id: item.GetId(), Title: "must not persist",
					Fields: publicv1.ClusterCatalogItemFields_builder{NodeSets: invalidPolicy(ctx)}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("title", "fields.node_sets"),
			}.Build())
			expectCatalogItemStatusCode(err, codes.InvalidArgument)
			after, err := client.Get(ctx, publicv1.ClusterCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(before.GetObject(), after.GetObject())).To(BeTrue())
		},
			Entry("empty locked map", func(context.Context) *publicv1.ClusterNodeSetMapPolicy {
				return publicv1.ClusterNodeSetMapPolicy_builder{Locked: publicv1.ClusterNodeSetMap_builder{}.Build()}.Build()
			}),
			Entry("empty editable default map", func(context.Context) *publicv1.ClusterNodeSetMapPolicy {
				return publicv1.ClusterNodeSetMapPolicy_builder{
					Editable: publicv1.EditableClusterNodeSetMap_builder{DefaultValue: publicv1.ClusterNodeSetMap_builder{}.Build()}.Build(),
				}.Build()
			}),
			Entry("zero node count", func(context.Context) *publicv1.ClusterNodeSetMapPolicy {
				return publicv1.ClusterNodeSetMapPolicy_builder{
					Locked: publicv1.ClusterNodeSetMap_builder{
						Items: map[string]*publicv1.ClusterTemplateNodeSet{
							"workers": publicv1.ClusterTemplateNodeSet_builder{Size: 0}.Build(),
						},
					}.Build(),
				}.Build()
			}),
			Entry("additional key without HostType", func(context.Context) *publicv1.ClusterNodeSetMapPolicy {
				return publicv1.ClusterNodeSetMapPolicy_builder{
					Locked: publicv1.ClusterNodeSetMap_builder{
						Items: map[string]*publicv1.ClusterTemplateNodeSet{
							"extra": publicv1.ClusterTemplateNodeSet_builder{Size: 3}.Build(),
						},
					}.Build(),
				}.Build()
			}),
		)

	})
	Context("Referenced objects", func() {
		It("protects its immutable Template in a draft and releases it on catalog item deletion", func(ctx context.Context) {
			template := createCatalogItemClusterTemplateFixture(ctx, createCatalogItemHostTypeFixture(ctx), nil, nil)
			item := createClusterCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ClusterCatalogItem_builder{
				Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template: publicv1.ClusterTemplateReference_builder{Id: template}.Build(),
			}.Build())
			templates := privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
			_, err := templates.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{Id: template}.Build())
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)
			_, err = publicv1.NewClusterCatalogItemsClient(tool.ExternalView().AdminConn()).Delete(ctx, publicv1.ClusterCatalogItemsDeleteRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = templates.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{Id: template}.Build())
			Expect(err).NotTo(HaveOccurred())
		})
		It("protects referenced objects through publication and policy changes", func(ctx context.Context) {
			By("authoring a published cluster offering with a locked dependency")
			template := createCatalogItemClusterTemplateFixture(ctx, createCatalogItemHostTypeFixture(ctx), nil, nil)
			id := createCatalogItemClusterVersionFixture(ctx, "4.20.0")
			items := publicv1.NewClusterCatalogItemsClient(tool.ExternalView().AdminConn())
			dependencies := privatev1.NewClusterVersionsClient(tool.InternalView().AdminConn())
			item := createClusterCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ClusterCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
				Template:  publicv1.ClusterTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ClusterCatalogItemFields_builder{
					Version: publicv1.ClusterVersionReferenceFieldPolicy_builder{
						Locked: publicv1.ClusterVersionReference_builder{Id: id}.Build(),
					}.Build(),
				}.Build(),
			}.Build())

			By("blocking deletion while the dependency is locked")
			_, err := dependencies.Delete(ctx, privatev1.ClusterVersionsDeleteRequest_builder{Id: id}.Build())
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)

			By("retaining protection after unpublishing and switching to an editable default")
			_, err = items.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Id:        item.GetId(),
					Published: false,
					Fields: publicv1.ClusterCatalogItemFields_builder{
						Version: publicv1.ClusterVersionReferenceFieldPolicy_builder{
							Editable: publicv1.EditableClusterVersionReferenceField_builder{
								DefaultValue: publicv1.ClusterVersionReference_builder{Id: id}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("published", "fields.version"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = dependencies.Delete(ctx, privatev1.ClusterVersionsDeleteRequest_builder{Id: id}.Build())
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)

			By("clearing the dependency policy")
			_, err = items.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{Id: item.GetId()}.Build(), UpdateMask: catalogItemUpdateMask("fields.version"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())

			By("releasing the dependency after the last policy reference is cleared")
			_, err = dependencies.Delete(ctx, privatev1.ClusterVersionsDeleteRequest_builder{Id: id}.Build())
			Expect(err).NotTo(HaveOccurred())
		})

	})
	Context("Lifecycle independence", func() {
		It("applies policy edits only to future clusters and permits ordinary version updates after catalog item deletion", func(ctx context.Context) {
			By("creating a cluster from the original version policy")
			host := createCatalogItemHostTypeFixture(ctx)
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			firstVersion := createCatalogItemClusterVersionFixture(ctx, "4.20.0")
			secondVersion := createCatalogItemClusterVersionFixture(ctx, "4.21.0")
			template := createCatalogItemClusterTemplateFixture(ctx, host, nil, nil)
			items := publicv1.NewClusterCatalogItemsClient(tool.ExternalView().AdminConn())
			item := createClusterCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ClusterCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.ClusterTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ClusterCatalogItemFields_builder{
					Version: publicv1.ClusterVersionReferenceFieldPolicy_builder{
						Locked: publicv1.ClusterVersionReference_builder{Id: firstVersion}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			request := publicv1.ClusterSpec_builder{
				CatalogItem:       publicv1.ClusterCatalogItemReference_builder{Id: item.GetId()}.Build(),
				NetworkAttachment: network.clusterAttachment(),
			}.Build()
			first, e := createClusterFixture(ctx, tool.ExternalView().UserConn(), request)
			Expect(e).NotTo(HaveOccurred())
			By("changing the policy and checking that only new clusters receive it")
			_, e = items.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object: publicv1.ClusterCatalogItem_builder{
					Id: item.GetId(),
					Fields: publicv1.ClusterCatalogItemFields_builder{
						Version: publicv1.ClusterVersionReferenceFieldPolicy_builder{
							Locked: publicv1.ClusterVersionReference_builder{Id: secondVersion}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			second, e := createClusterFixture(ctx, tool.ExternalView().UserConn(), request)
			Expect(e).NotTo(HaveOccurred())
			client := publicv1.NewClustersClient(tool.ExternalView().UserConn())
			persistedSecond, e := client.Get(ctx, publicv1.ClustersGetRequest_builder{Id: second.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			second = persistedSecond.GetObject()
			Expect(second.GetSpec().GetVersion().GetId()).To(Equal(secondVersion))
			stored, e := client.Get(ctx, publicv1.ClustersGetRequest_builder{Id: first.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(stored.GetObject().GetSpec().GetVersion().GetId()).To(Equal(firstVersion))
			By("updating the original cluster through its ordinary version field")
			_, e = client.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object:     publicv1.Cluster_builder{Id: first.GetId(), Spec: publicv1.ClusterSpec_builder{Version: publicv1.ClusterVersionReference_builder{Id: secondVersion}.Build()}.Build()}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.version"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			liveUpdate, e := client.Get(ctx, publicv1.ClustersGetRequest_builder{Id: first.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(liveUpdate.GetObject().GetSpec().GetVersion().GetId()).To(Equal(secondVersion))

			By("unpublishing the catalog item while keeping the existing cluster usable")
			_, e = items.Update(ctx, publicv1.ClusterCatalogItemsUpdateRequest_builder{
				Object:     publicv1.ClusterCatalogItem_builder{Id: item.GetId(), Published: false}.Build(),
				UpdateMask: catalogItemUpdateMask("published"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			_, e = createClusterFixture(ctx, tool.ExternalView().UserConn(), request)
			expectCatalogItemStatusCode(e, codes.NotFound)
			_, e = client.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object:     publicv1.Cluster_builder{Id: first.GetId(), Spec: publicv1.ClusterSpec_builder{Version: publicv1.ClusterVersionReference_builder{Id: firstVersion}.Build()}.Build()}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.version"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())

			By("updating through the public API after catalog deletion")
			// The stored catalog reference is provenance; ordinary updates use the materialized inputs.
			_, e = items.Delete(ctx, publicv1.ClusterCatalogItemsDeleteRequest_builder{Id: item.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			_, e = client.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: first.GetId(),
					Spec: publicv1.ClusterSpec_builder{
						CatalogItem: publicv1.ClusterCatalogItemReference_builder{Id: item.GetId()}.Build(),
						Version:     publicv1.ClusterVersionReference_builder{Id: secondVersion}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.catalog_item",
					"spec.version"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			updated, e := client.Get(ctx, publicv1.ClustersGetRequest_builder{Id: first.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(updated.GetObject().GetSpec().GetVersion().GetId()).To(Equal(secondVersion))

			internal := privatev1.NewClustersClient(tool.InternalView().AdminConn())
			By("updating through the private API after catalog deletion")
			_, e = internal.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: first.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: item.GetId()}.Build(),
						Version:     privatev1.ClusterVersionReference_builder{Id: firstVersion}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.catalog_item",
					"spec.version"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			privateUpdated, e := internal.Get(ctx, privatev1.ClustersGetRequest_builder{Id: first.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(privateUpdated.GetObject().GetSpec().GetVersion().GetId()).To(Equal(firstVersion))

			By("rejecting provenance mutation through the public API")
			_, e = client.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: first.GetId(),
					Spec: publicv1.ClusterSpec_builder{
						CatalogItem: publicv1.ClusterCatalogItemReference_builder{Id: "different"}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.catalog_item"),
			}.Build())
			expectCatalogItemStatusCode(e, codes.InvalidArgument)
			By("rejecting provenance clearing through the public API")
			_, e = client.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id:   first.GetId(),
					Spec: publicv1.ClusterSpec_builder{}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.catalog_item"),
			}.Build())
			expectCatalogItemStatusCode(e, codes.InvalidArgument)

			By("rejecting provenance mutation and clearing through the private API")
			for _, tc := range []struct {
				name      string
				reference *privatev1.ClusterCatalogItemReference
			}{
				{"different catalog item", privatev1.ClusterCatalogItemReference_builder{Id: "different"}.Build()},
				{"cleared catalog item", nil},
			} {
				By(tc.name)
				_, e = internal.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id:   first.GetId(),
						Spec: privatev1.ClusterSpec_builder{CatalogItem: tc.reference}.Build(),
					}.Build(),
					UpdateMask: catalogItemUpdateMask("spec.catalog_item"),
				}.Build())
				expectCatalogItemStatusCode(e, codes.InvalidArgument)
			}
			persisted, e := client.Get(ctx, publicv1.ClustersGetRequest_builder{Id: first.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(proto.Equal(persisted.GetObject().GetSpec().GetCatalogItem(), first.GetSpec().GetCatalogItem())).To(BeTrue())
			Expect(persisted.GetObject().GetSpec().GetTemplate().GetId()).To(Equal(template))

		})
	})
})
