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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	bmfv1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Bare Metal Instance Catalog Items", Label("catalog-items"), func() {
	Context("Provisioning and field governance", func() {
		It("materializes bare metal instance typed policies and replaces editable attachments", func(ctx context.Context) {
			By("authoring a tenant offering with hardware, image, and network policies")
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			otherNetwork := createCatalogItemNetworkInClassFixture(ctx, usersGroup, "", network.networkClassID)
			instanceType := createCatalogItemBareMetalInstanceTypeFixture(ctx, usersGroup)
			image := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
			template := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, bareMetalInstanceCatalogItemParameterDefinitions())
			fields := publicv1.BareMetalInstanceCatalogItemFields_builder{
				InstanceType: publicv1.BareMetalInstanceTypeLocalReferenceFieldPolicy_builder{
					Locked: publicv1.BareMetalInstanceTypeLocalReference_builder{Id: instanceType}.Build(),
				}.Build(),
				DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
					Locked: publicv1.DiskImageReference_builder{Id: image.GetId()}.Build(),
				}.Build(),
				SshPublicKey: publicv1.StringFieldPolicy_builder{
					Editable: publicv1.EditableStringField_builder{DefaultValue: new(catalogItemFixtureSSHPublicKey)}.Build(),
				}.Build(),
				UserData: publicv1.StringFieldPolicy_builder{Locked: new("")}.Build(),
				RunStrategy: publicv1.BareMetalInstanceRunStrategyFieldPolicy_builder{
					Editable: publicv1.EditableBareMetalInstanceRunStrategyField_builder{DefaultValue: new(publicv1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED)}.Build(),
				}.Build(),

				NetworkAttachments: publicv1.BareMetalNetworkAttachmentListFieldPolicy_builder{
					Editable: publicv1.EditableBareMetalNetworkAttachmentList_builder{
						DefaultValue: publicv1.BareMetalNetworkAttachmentList_builder{Items: []*publicv1.BareMetalNetworkAttachment{network.bareMetalInstanceAttachment()}}.Build(),
					}.Build(),
				}.Build(),
				AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{Locked: new(false)}.Build(),
			}.Build()

			item := createBareMetalInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.BareMetalInstanceCatalogItem_builder{
				Metadata:           publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
				Template:           publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build(),
				Published:          true,
				Fields:             fields,
				TemplateParameters: catalogItemParameterPolicies(),
			}.Build())

			By("creating an instance with a caller-selected network attachment")
			request := publicv1.BareMetalInstanceSpec_builder{
				CatalogItem:        publicv1.BareMetalInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
				NetworkAttachments: []*publicv1.BareMetalNetworkAttachment{otherNetwork.bareMetalInstanceAttachment()},
				TemplateParameters: map[string]*anypb.Any{"size": catalogItemParameterValue(wrapperspb.Int32(0))},
			}.Build()
			created, e := createBareMetalInstanceFixture(ctx, tool.ExternalView().UserConn(), request)
			Expect(e).NotTo(HaveOccurred())

			By("checking stored hardware, image, attachment, and Template inputs")
			client := publicv1.NewBareMetalInstancesClient(tool.ExternalView().UserConn())
			persisted, e := client.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{Id: created.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			spec := persisted.GetObject().GetSpec()
			Expect(spec.GetInstanceType().GetId()).To(Equal(instanceType))

			Expect(spec.GetDiskImage().GetId()).To(Equal(image.GetId()))
			Expect(spec.GetDiskImage().GetShared()).To(BeTrue())
			Expect(spec.GetSshPublicKey()).To(Equal(catalogItemFixtureSSHPublicKey))
			Expect(spec.HasUserData()).To(BeTrue())
			Expect(spec.GetUserData()).To(BeEmpty())
			Expect(spec.GetRunStrategy()).To(Equal(publicv1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED))
			Expect(spec.HasAutoExternalIpAttachment()).To(BeTrue())
			Expect(spec.GetAutoExternalIpAttachment()).To(BeFalse())
			Expect(spec.GetNetworkAttachments()).To(HaveLen(1))
			Expect(spec.GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal(otherNetwork.subnetID))
			Expect(spec.GetNetworkAttachments()[0].GetSecurityGroups()[0].GetId()).To(Equal(otherNetwork.securityGroupID))
			Expect(spec.GetTemplate().GetId()).To(Equal(template))
			Expect(proto.Equal(spec.GetTemplateParameters()["enabled"], catalogItemParameterValue(wrapperspb.Bool(false)))).To(BeTrue())
			Expect(proto.Equal(spec.GetTemplateParameters()["size"], catalogItemParameterValue(wrapperspb.Int32(0)))).To(BeTrue())
			Expect(proto.Equal(spec.GetTemplateParameters()["ordinary"], catalogItemParameterValue(wrapperspb.String("template")))).To(BeTrue())

			By("rejecting explicit values for locked policies")
			type invalidInput struct {
				name string
				set  func(*publicv1.BareMetalInstanceSpec)
			}
			inputs := []invalidInput{
				{
					name: "identical locked type",
					set: func(s *publicv1.BareMetalInstanceSpec) {
						s.SetInstanceType(publicv1.BareMetalInstanceTypeLocalReference_builder{Id: instanceType}.Build())
					},
				},
				{
					name: "identical locked image reference",
					set: func(s *publicv1.BareMetalInstanceSpec) {
						s.SetDiskImage(publicv1.DiskImageReference_builder{Id: image.GetId()}.Build())
					},
				},
				{
					name: "empty user data",
					set: func(s *publicv1.BareMetalInstanceSpec) {
						s.SetUserData("")
					},
				},
				{
					name: "explicit false",
					set: func(s *publicv1.BareMetalInstanceSpec) {
						s.SetAutoExternalIpAttachment(false)
					},
				},
				{
					name: "locked parameter",
					set: func(s *publicv1.BareMetalInstanceSpec) {
						s.GetTemplateParameters()["enabled"] = catalogItemParameterValue(wrapperspb.Bool(false))
					},
				},
			}
			for _, input := range inputs {
				By(input.name)
				s := proto.Clone(request).(*publicv1.BareMetalInstanceSpec)
				input.set(s)
				_, e = createBareMetalInstanceFixture(ctx, tool.ExternalView().UserConn(), s)
				expectCatalogItemStatusCode(e, codes.InvalidArgument)
			}

			By("using the default image and network when the caller omits them")
			defaulted, e := createBareMetalInstanceFixture(ctx, tool.ExternalView().UserConn(), publicv1.BareMetalInstanceSpec_builder{
				CatalogItem:        publicv1.BareMetalInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
				NetworkAttachments: []*publicv1.BareMetalNetworkAttachment{},
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			persistedDefaults, e := client.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{Id: defaulted.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			defaulted = persistedDefaults.GetObject()
			Expect(proto.Equal(defaulted.GetSpec().GetTemplateParameters()["size"], catalogItemParameterValue(wrapperspb.Int32(20)))).To(BeTrue())
			Expect(defaulted.GetSpec().GetDiskImage().GetId()).To(Equal(image.GetId()))
			Expect(defaulted.GetSpec().GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal(network.subnetID))
		})

		It("rejects Create with more than one network attachment", func(ctx context.Context) {
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			otherNetwork := createCatalogItemNetworkInClassFixture(ctx, usersGroup, "", network.networkClassID)
			instanceType := createCatalogItemBareMetalInstanceTypeFixture(ctx, usersGroup)
			image := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
			template := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, bareMetalInstanceCatalogItemParameterDefinitions())
			item := createBareMetalInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.BareMetalInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
				Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
					InstanceType: publicv1.BareMetalInstanceTypeLocalReferenceFieldPolicy_builder{
						Locked: publicv1.BareMetalInstanceTypeLocalReference_builder{Id: instanceType}.Build(),
					}.Build(),
					DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
						Locked: publicv1.DiskImageReference_builder{Id: image.GetId()}.Build(),
					}.Build(),
					SshPublicKey: publicv1.StringFieldPolicy_builder{
						Editable: publicv1.EditableStringField_builder{DefaultValue: new(catalogItemFixtureSSHPublicKey)}.Build(),
					}.Build(),
					NetworkAttachments: publicv1.BareMetalNetworkAttachmentListFieldPolicy_builder{
						Editable: publicv1.EditableBareMetalNetworkAttachmentList_builder{
							DefaultValue: publicv1.BareMetalNetworkAttachmentList_builder{
								Items: []*publicv1.BareMetalNetworkAttachment{network.bareMetalInstanceAttachment()},
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
				TemplateParameters: catalogItemParameterPolicies(),
			}.Build())

			_, err := createBareMetalInstanceFixture(ctx, tool.ExternalView().UserConn(), publicv1.BareMetalInstanceSpec_builder{
				CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
				NetworkAttachments: []*publicv1.BareMetalNetworkAttachment{
					network.bareMetalInstanceAttachment(),
					otherNetwork.bareMetalInstanceAttachment(),
				},
			}.Build())
			expectCatalogItemStatusCode(err, codes.InvalidArgument)
			Expect(err.Error()).To(ContainSubstring("at most one network attachment"))
		})

		It("applies editable DiskImage and Template defaults and validates dry-run authentication", func(ctx context.Context) {
			By("authoring a shared offering with editable image and external-IP defaults")
			instanceType := createCatalogItemBareMetalInstanceTypeFixture(ctx, usersGroup)
			defaultImage := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
			overrideImage := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
			template := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, []*privatev1.BareMetalInstanceTemplateParameterDefinition{
				privatev1.BareMetalInstanceTemplateParameterDefinition_builder{
					Name: "ordinary", Type: "type.googleapis.com/google.protobuf.StringValue",
					Default: catalogItemParameterValue(wrapperspb.String("template")),
				}.Build(),
			})
			item := createBareMetalInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.BareMetalInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
					DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
						Editable: publicv1.EditableDiskImageReferenceField_builder{
							DefaultValue: publicv1.DiskImageReference_builder{Id: defaultImage.GetId()}.Build(),
						}.Build(),
					}.Build(),
					AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{
						Editable: publicv1.EditableBoolField_builder{DefaultValue: new(true)}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			client := publicv1.NewBareMetalInstancesClient(tool.ExternalView().UserConn())
			externalIPs := privatev1.NewExternalIPsClient(tool.InternalView().AdminConn())
			beforeExternalIPs, err := externalIPs.List(ctx, privatev1.ExternalIPsListRequest_builder{}.Build())
			Expect(err).NotTo(HaveOccurred())
			dry := metadata.AppendToOutgoingContext(ctx, "x-dry-run", "true")
			spec := publicv1.BareMetalInstanceSpec_builder{
				CatalogItem:  publicv1.BareMetalInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
				InstanceType: publicv1.BareMetalInstanceTypeLocalReference_builder{Id: instanceType}.Build(),
				SshPublicKey: new(catalogItemFixtureSSHPublicKey),
			}.Build()
			name := catalogItemFixtureName()
			By("validating a dry-run instance without storing it or allocating an external IP")
			result, e := client.Create(dry, publicv1.BareMetalInstancesCreateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: name}.Build(),
					Spec:     spec,
				}.Build(),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(result.GetObject().GetSpec().GetDiskImage().GetId()).To(Equal(defaultImage.GetId()))
			Expect(proto.Equal(result.GetObject().GetSpec().GetTemplateParameters()["ordinary"], catalogItemParameterValue(wrapperspb.String("template")))).To(BeTrue())
			listed, e := client.List(ctx, publicv1.BareMetalInstancesListRequest_builder{Filter: new("this.metadata.name == '" + name + "'")}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(listed.GetItems()).To(BeEmpty())
			afterExternalIPs, err := externalIPs.List(ctx, privatev1.ExternalIPsListRequest_builder{}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(afterExternalIPs.GetTotal()).To(Equal(beforeExternalIPs.GetTotal()))
			By("creating an instance with caller-selected image and external-IP values")
			overrideSpec := proto.Clone(spec).(*publicv1.BareMetalInstanceSpec)
			overrideSpec.SetDiskImage(publicv1.DiskImageReference_builder{Id: overrideImage.GetId()}.Build())
			overrideSpec.SetAutoExternalIpAttachment(false)
			override, e := createBareMetalInstanceFixture(ctx, tool.ExternalView().UserConn(), overrideSpec)
			Expect(e).NotTo(HaveOccurred())
			persisted, e := client.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{Id: override.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(persisted.GetObject().GetSpec().GetDiskImage().GetId()).To(Equal(overrideImage.GetId()))
			By("rejecting a request that names both the offering and its Template")
			spec.SetTemplate(publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build())
			_, e = client.Create(dry, publicv1.BareMetalInstancesCreateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec:     spec,
				}.Build(),
			}.Build())
			expectCatalogItemStatusCode(e, codes.InvalidArgument)
			By("rejecting a request without an SSH public key")
			spec.ClearTemplate()
			spec.ClearSshPublicKey()
			_, e = client.Create(dry, publicv1.BareMetalInstancesCreateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec:     spec,
				}.Build(),
			}.Build())
			expectCatalogItemStatusCode(e, codes.InvalidArgument)
			By("creating directly from the Template without catalog provenance")
			spec.ClearCatalogItem()
			spec.SetTemplate(publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build())
			spec.SetSshPublicKey(catalogItemFixtureSSHPublicKey)
			spec.SetAutoExternalIpAttachment(false)
			spec.SetDiskImage(publicv1.DiskImageReference_builder{Id: overrideImage.GetId()}.Build())
			direct, e := createBareMetalInstanceFixture(ctx, tool.ExternalView().UserConn(), spec)
			Expect(e).NotTo(HaveOccurred())
			Expect(direct.GetSpec().HasCatalogItem()).To(BeFalse())
			Expect(direct.GetSpec().GetDiskImage().GetId()).To(Equal(overrideImage.GetId()))
			Expect(proto.Equal(direct.GetSpec().GetTemplateParameters()["ordinary"], catalogItemParameterValue(wrapperspb.String("template")))).To(BeTrue())
			By("rejecting creation without a Catalog Item or Template")
			spec.ClearTemplate()
			_, e = client.Create(dry, publicv1.BareMetalInstancesCreateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec:     spec,
				}.Build(),
			}.Build())
			expectCatalogItemStatusCode(e, codes.InvalidArgument)
		})

		DescribeTable("rejects unusable networking after catalog item policy resolution", func(ctx context.Context, callerOverride bool, expectedCode codes.Code) {

			template := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)
			image := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			item := createBareMetalInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.BareMetalInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
				Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
					NetworkAttachments: publicv1.BareMetalNetworkAttachmentListFieldPolicy_builder{
						Editable: publicv1.EditableBareMetalNetworkAttachmentList_builder{
							DefaultValue: publicv1.BareMetalNetworkAttachmentList_builder{Items: []*publicv1.BareMetalNetworkAttachment{network.bareMetalInstanceAttachment()}}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			spec := publicv1.BareMetalInstanceSpec_builder{
				CatalogItem:  publicv1.BareMetalInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
				DiskImage:    publicv1.DiskImageReference_builder{Id: image.GetId()}.Build(),
				InstanceType: publicv1.BareMetalInstanceTypeLocalReference_builder{Id: createCatalogItemBareMetalInstanceTypeFixture(ctx, usersGroup)}.Build(),
				SshPublicKey: new(catalogItemFixtureSSHPublicKey),
			}.Build()
			client := publicv1.NewBareMetalInstancesClient(tool.ExternalView().UserConn())
			dry := metadata.AppendToOutgoingContext(ctx, "x-dry-run", "true")
			request := publicv1.BareMetalInstancesCreateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec:     spec,
				}.Build(),
			}.Build()
			_, err := client.Create(dry, request)
			Expect(err).NotTo(HaveOccurred())

			if callerOverride {
				By("replacing the editable attachment with a security group from another virtual network")
				other := createCatalogItemNetworkInClassFixture(ctx, usersGroup, "", network.networkClassID)
				attachment := network.bareMetalInstanceAttachment()
				attachment.SetSecurityGroups([]*publicv1.SecurityGroupLocalReference{
					publicv1.SecurityGroupLocalReference_builder{Id: other.securityGroupID}.Build(),
				})
				spec.SetNetworkAttachments([]*publicv1.BareMetalNetworkAttachment{attachment})
			} else {
				By("making the default subnet pending after the catalog item was published")
				setCatalogItemSubnetFixtureState(ctx, network.subnetID, privatev1.SubnetState_SUBNET_STATE_PENDING)
			}
			_, err = client.Create(dry, request)
			expectCatalogItemStatusCode(err, expectedCode)
			Expect(err.Error()).To(ContainSubstring("spec.network_attachments[0]"))

		},
			Entry("caller override: security group belongs to a different virtual network", true, codes.InvalidArgument),
			Entry("catalog item default: subnet is no longer ready", false, codes.FailedPrecondition),
		)
		It("checks user-data Secret conflicts after defaults and releases the conflict when the policy is cleared", func(ctx context.Context) {
			secret := createCatalogItemUserDataSecretFixture(ctx, usersGroup)
			image := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
			template := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)
			item := createBareMetalInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.BareMetalInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
					UserData: publicv1.StringFieldPolicy_builder{
						Editable: publicv1.EditableStringField_builder{DefaultValue: new("#cloud-config\n")}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			client := publicv1.NewBareMetalInstancesClient(tool.ExternalView().UserConn())
			request := publicv1.BareMetalInstancesCreateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec: publicv1.BareMetalInstanceSpec_builder{
						CatalogItem:    publicv1.BareMetalInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
						DiskImage:      publicv1.DiskImageReference_builder{Id: image.GetId()}.Build(),
						UserDataSecret: publicv1.SecretLocalReference_builder{Id: secret}.Build(),
						InstanceType:   publicv1.BareMetalInstanceTypeLocalReference_builder{Id: createCatalogItemBareMetalInstanceTypeFixture(ctx, usersGroup)}.Build(),
					}.Build(),
				}.Build(),
			}.Build()
			dry := metadata.AppendToOutgoingContext(ctx, "x-dry-run", "true")
			_, err := client.Create(dry, request)
			expectCatalogItemStatusCode(err, codes.InvalidArgument)
			By("treating an explicit empty string as present for mutual exclusivity")
			request.GetObject().GetSpec().SetUserData("")
			_, err = client.Create(dry, request)
			expectCatalogItemStatusCode(err, codes.InvalidArgument)
			By("clearing the catalog item policy before using the Secret on its own")
			_, err = publicv1.NewBareMetalInstanceCatalogItemsClient(tool.ExternalView().AdminConn()).Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Id:     item.GetId(),
					Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields.user_data"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			request.GetObject().GetSpec().ClearUserData()
			response, err := client.Create(dry, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetUserDataSecret().GetId()).To(Equal(secret))
			Expect(response.GetObject().GetSpec().HasUserData()).To(BeFalse())
		})
	})
	Context("Template parameters", func() {
		It("distinguishes editable required input from Template defaults and invalid values", func(ctx context.Context) {
			By("publishing a bare metal instance offering with a required editable parameter")
			image := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
			template := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, bareMetalInstanceCatalogItemParameterDefinitions())
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			item := createBareMetalInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.BareMetalInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build(),
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
			client := publicv1.NewBareMetalInstancesClient(tool.ExternalView().UserConn())
			request := publicv1.BareMetalInstancesCreateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec: publicv1.BareMetalInstanceSpec_builder{
						CatalogItem:        publicv1.BareMetalInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
						DiskImage:          publicv1.DiskImageReference_builder{Id: image.GetId()}.Build(),
						NetworkAttachments: []*publicv1.BareMetalNetworkAttachment{network.bareMetalInstanceAttachment()},
						SshPublicKey:       new(catalogItemFixtureSSHPublicKey),
					}.Build(),
				}.Build(),
			}.Build()
			dry := metadata.AppendToOutgoingContext(ctx, "x-dry-run", "true")

			By("rejecting a bare metal instance request that omits the required parameter")
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
			image := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
			template := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)
			tenant, conn := createCatalogItemTenantAdminFixture(ctx)
			items := publicv1.NewBareMetalInstanceCatalogItemsClient(conn)
			owned := createBareMetalInstanceCatalogItemFixture(ctx, conn, publicv1.BareMetalInstanceCatalogItem_builder{
				Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template: publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build(),
			}.Build())
			Expect(owned.GetMetadata().GetTenant()).To(Equal(tenant), "tenant authoring scope")
			memberConn := createCatalogItemMemberFixture(ctx, tenant)
			memberItems := publicv1.NewBareMetalInstanceCatalogItemsClient(memberConn)

			By("publishing governed defaults for tenant members")
			_, err := items.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{Id: owned.GetId(), Published: true,
					Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
						UserData:                 publicv1.StringFieldPolicy_builder{Editable: publicv1.EditableStringField_builder{DefaultValue: new("#cloud-config\n# tenant")}.Build()}.Build(),
						AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{Locked: new(false)}.Build(),
					}.Build(),
				}.Build(), UpdateMask: catalogItemUpdateMask("published", "fields"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			visible, err := memberItems.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{Id: owned.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(visible.GetObject().GetFields().GetUserData().GetEditable().GetDefaultValue()).To(Equal("#cloud-config\n# tenant"))
			Expect(visible.GetObject().GetFields().GetAutoExternalIpAttachment().HasLocked()).To(BeTrue())
			Expect(visible.GetObject().GetFields().GetAutoExternalIpAttachment().GetLocked()).To(BeFalse())

			By("provisioning a bare metal instance as a member with an editable override")
			network := createCatalogItemNetworkFixture(ctx, tenant, "")
			spec := publicv1.BareMetalInstanceSpec_builder{
				CatalogItem:        publicv1.BareMetalInstanceCatalogItemReference_builder{Id: owned.GetId()}.Build(),
				DiskImage:          publicv1.DiskImageReference_builder{Id: image.GetId()}.Build(),
				UserData:           new("#cloud-config\n# member"),
				NetworkAttachments: []*publicv1.BareMetalNetworkAttachment{network.bareMetalInstanceAttachment()},
				InstanceType:       publicv1.BareMetalInstanceTypeLocalReference_builder{Id: createCatalogItemBareMetalInstanceTypeFixture(ctx, tenant)}.Build(),
			}.Build()
			created, err := createBareMetalInstanceFixture(ctx, memberConn, spec)
			Expect(err).NotTo(HaveOccurred())
			objects := publicv1.NewBareMetalInstancesClient(memberConn)
			stored, err := objects.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{Id: created.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(stored.GetObject().GetMetadata().GetTenant()).To(Equal(tenant))
			Expect(stored.GetObject().GetSpec().GetUserData()).To(Equal("#cloud-config\n# member"))
			Expect(stored.GetObject().GetSpec().HasAutoExternalIpAttachment()).To(BeTrue())
			Expect(stored.GetObject().GetSpec().GetAutoExternalIpAttachment()).To(BeFalse())

			By("hiding the tenant offering from another tenant")
			outsider := publicv1.NewBareMetalInstanceCatalogItemsClient(tool.ExternalView().UserConn())
			_, err = outsider.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{Id: owned.GetId()}.Build())
			expectCatalogItemStatusCode(err, codes.NotFound)
			hidden, err := outsider.List(ctx, publicv1.BareMetalInstanceCatalogItemsListRequest_builder{Filter: new("this.id == '" + owned.GetId() + "'")}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(hidden.GetItems()).To(BeEmpty())
			_, err = createBareMetalInstanceFixture(ctx, tool.ExternalView().UserConn(), publicv1.BareMetalInstanceSpec_builder{
				CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: owned.GetId()}.Build(),
			}.Build())
			expectCatalogItemStatusCode(err, codes.NotFound)

			By("preventing a member from deleting the tenant offering")
			_, err = memberItems.Delete(ctx, publicv1.BareMetalInstanceCatalogItemsDeleteRequest_builder{Id: owned.GetId()}.Build())
			expectCatalogItemStatusCode(err, codes.PermissionDenied)

			By("unpublishing the offering to stop new member provisioning")
			_, err = items.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object:     publicv1.BareMetalInstanceCatalogItem_builder{Id: owned.GetId(), Published: false}.Build(),
				UpdateMask: catalogItemUpdateMask("published"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = createBareMetalInstanceFixture(ctx, memberConn, spec)
			expectCatalogItemStatusCode(err, codes.NotFound)
		})

		It("lists shared published items and protects provider authoring", func(ctx context.Context) {
			template := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)
			_, conn := createCatalogItemTenantAdminFixture(ctx)
			items := publicv1.NewBareMetalInstanceCatalogItemsClient(conn)
			user := publicv1.NewBareMetalInstanceCatalogItemsClient(tool.ExternalView().UserConn())
			shared := createBareMetalInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.BareMetalInstanceCatalogItem_builder{
				Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template: publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build(),
			}.Build())
			Expect(shared.GetMetadata().GetTenant()).To(Equal("shared"))
			_, err := user.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{Id: shared.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = items.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object:     publicv1.BareMetalInstanceCatalogItem_builder{Id: shared.GetId(), Title: "Foreign edit"}.Build(),
				UpdateMask: catalogItemUpdateMask("title"),
			}.Build())
			expectCatalogItemStatusCode(err, codes.PermissionDenied)
			listed, err := user.List(ctx, publicv1.BareMetalInstanceCatalogItemsListRequest_builder{Filter: new("this.id == '" + shared.GetId() + "' && this.published")}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetItems()).To(BeEmpty())
			provider := publicv1.NewBareMetalInstanceCatalogItemsClient(tool.ExternalView().AdminConn())
			_, err = provider.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object:     publicv1.BareMetalInstanceCatalogItem_builder{Id: shared.GetId(), Published: true}.Build(),
				UpdateMask: catalogItemUpdateMask("published"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			listed, err = user.List(ctx, publicv1.BareMetalInstanceCatalogItemsListRequest_builder{Filter: new("this.id == '" + shared.GetId() + "' && this.published")}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetItems()).To(HaveLen(1))
		})
		It("keeps unmasked policies and atomically rejects invalid merged candidates", func(ctx context.Context) {
			By("authoring an offering with two field policies")
			template := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)
			item := createBareMetalInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.BareMetalInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
					UserData:                 publicv1.StringFieldPolicy_builder{Locked: new("initial")}.Build(),
					AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{Locked: new(false)}.Build(),
				}.Build(),
			}.Build())
			client := publicv1.NewBareMetalInstanceCatalogItemsClient(tool.ExternalView().AdminConn())

			By("editing one policy while retaining the other")
			_, err := client.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Id: item.GetId(),
					Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
						UserData: publicv1.StringFieldPolicy_builder{
							Editable: publicv1.EditableStringField_builder{DefaultValue: new("edited")}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields.user_data"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			before, err := client.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(before.GetObject().GetFields().GetAutoExternalIpAttachment().HasLocked()).To(BeTrue())
			Expect(before.GetObject().GetFields().GetAutoExternalIpAttachment().GetLocked()).To(BeFalse())
			Expect(before.GetObject().GetFields().GetUserData().HasLocked()).To(BeFalse())
			Expect(before.GetObject().GetFields().GetUserData().GetEditable().GetDefaultValue()).To(Equal("edited"))

			By("rejecting an invalid policy without changing the offering title")
			_, err = client.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Id:    item.GetId(),
					Title: "must not persist",
					Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
						UserData: publicv1.StringFieldPolicy_builder{}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("title", "fields.user_data"),
			}.Build())
			expectCatalogItemStatusCode(err, codes.InvalidArgument)
			after, err := client.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(before.GetObject(), after.GetObject())).To(BeTrue())

			By("clearing all field policies explicitly")
			_, err = client.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Id:     item.GetId(),
					Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			after, err = client.Get(ctx, publicv1.BareMetalInstanceCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(after.GetObject().GetFields().GetUserData()).To(BeNil())
			Expect(after.GetObject().GetFields().GetAutoExternalIpAttachment()).To(BeNil())
		})
		DescribeTable("rejects authored empty network collections without creating an offering", func(ctx context.Context, policy func() *publicv1.BareMetalNetworkAttachmentListFieldPolicy) {
			template := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)
			client := publicv1.NewBareMetalInstanceCatalogItemsClient(tool.ExternalView().AdminConn())
			name := catalogItemFixtureName()
			_, err := client.Create(ctx, publicv1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{Name: name, Tenant: usersGroup}.Build(),
					Template: publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build(),
					Fields:   publicv1.BareMetalInstanceCatalogItemFields_builder{NetworkAttachments: policy()}.Build(),
				}.Build(),
			}.Build())
			expectCatalogItemStatusCode(err, codes.InvalidArgument)
			list, err := client.List(ctx, publicv1.BareMetalInstanceCatalogItemsListRequest_builder{Filter: new("this.metadata.name == '" + name + "'")}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(list.GetItems()).To(BeEmpty())
		},
			Entry("locked list", func() *publicv1.BareMetalNetworkAttachmentListFieldPolicy {
				return publicv1.BareMetalNetworkAttachmentListFieldPolicy_builder{
					Locked: publicv1.BareMetalNetworkAttachmentList_builder{}.Build(),
				}.Build()
			}),
			Entry("editable default list", func() *publicv1.BareMetalNetworkAttachmentListFieldPolicy {
				return publicv1.BareMetalNetworkAttachmentListFieldPolicy_builder{
					Editable: publicv1.EditableBareMetalNetworkAttachmentList_builder{
						DefaultValue: publicv1.BareMetalNetworkAttachmentList_builder{}.Build(),
					}.Build(),
				}.Build()
			}),
		)
	})
	Context("Referenced objects", func() {
		It("protects its immutable Template in a draft and releases it on catalog item deletion", func(ctx context.Context) {
			template := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)
			item := createBareMetalInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.BareMetalInstanceCatalogItem_builder{
				Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template: publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build(),
			}.Build())
			templates := privatev1.NewBareMetalInstanceTemplatesClient(tool.InternalView().AdminConn())
			_, err := templates.Delete(ctx, privatev1.BareMetalInstanceTemplatesDeleteRequest_builder{Id: template}.Build())
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)
			_, err = publicv1.NewBareMetalInstanceCatalogItemsClient(tool.ExternalView().AdminConn()).Delete(ctx, publicv1.BareMetalInstanceCatalogItemsDeleteRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = templates.Delete(ctx, privatev1.BareMetalInstanceTemplatesDeleteRequest_builder{Id: template}.Build())
			Expect(err).NotTo(HaveOccurred())
		})
		It("protects referenced objects through publication and policy changes", func(ctx context.Context) {
			By("authoring a published bare metal offering with a locked dependency")
			template := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)
			id := createCatalogItemBareMetalInstanceTypeFixture(ctx, usersGroup)
			items := publicv1.NewBareMetalInstanceCatalogItemsClient(tool.ExternalView().AdminConn())
			dependencies := privatev1.NewBareMetalInstanceTypesClient(tool.InternalView().AdminConn())
			item := createBareMetalInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.BareMetalInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
				Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
					InstanceType: publicv1.BareMetalInstanceTypeLocalReferenceFieldPolicy_builder{
						Locked: publicv1.BareMetalInstanceTypeLocalReference_builder{Id: id}.Build(),
					}.Build(),
				}.Build(),
			}.Build())

			By("blocking deletion while the dependency is locked")
			_, err := dependencies.Delete(ctx, privatev1.BareMetalInstanceTypesDeleteRequest_builder{Id: id}.Build())
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)

			By("retaining protection after unpublishing and switching to an editable default")
			_, err = items.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Id:        item.GetId(),
					Published: false,
					Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
						InstanceType: publicv1.BareMetalInstanceTypeLocalReferenceFieldPolicy_builder{
							Editable: publicv1.EditableBareMetalInstanceTypeLocalReferenceField_builder{
								DefaultValue: publicv1.BareMetalInstanceTypeLocalReference_builder{Id: id}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("published", "fields.instance_type"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = dependencies.Delete(ctx, privatev1.BareMetalInstanceTypesDeleteRequest_builder{Id: id}.Build())
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)

			By("clearing the dependency policy")
			_, err = items.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{Id: item.GetId()}.Build(), UpdateMask: catalogItemUpdateMask("fields.instance_type"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())

			By("releasing the dependency after the last policy reference is cleared")
			_, err = dependencies.Delete(ctx, privatev1.BareMetalInstanceTypesDeleteRequest_builder{Id: id}.Build())
			Expect(err).NotTo(HaveOccurred())
		})

	})
	Context("Lifecycle independence", func() {
		It("keeps resolved inputs independent of policy edits and reconciles a restart after catalog item deletion", func(ctx context.Context) {
			By("creating an instance from the original catalog policy")
			image := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
			instanceType := createCatalogItemBareMetalInstanceTypeFixture(ctx, usersGroup)
			template := createCatalogItemBareMetalInstanceTemplateFixture(ctx, nil, nil)
			items := publicv1.NewBareMetalInstanceCatalogItemsClient(tool.ExternalView().AdminConn())
			item := createBareMetalInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.BareMetalInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.BareMetalInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
					UserData: publicv1.StringFieldPolicy_builder{
						Editable: publicv1.EditableStringField_builder{DefaultValue: new("#cloud-config\n# first")}.Build(),
					}.Build(),
					RunStrategy: publicv1.BareMetalInstanceRunStrategyFieldPolicy_builder{Locked: new(publicv1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_HALTED)}.Build(),
				}.Build(),
			}.Build())
			request := publicv1.BareMetalInstanceSpec_builder{
				CatalogItem:  publicv1.BareMetalInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
				DiskImage:    publicv1.DiskImageReference_builder{Id: image.GetId()}.Build(),
				InstanceType: publicv1.BareMetalInstanceTypeLocalReference_builder{Id: instanceType}.Build(),
			}.Build()
			first, e := createBareMetalInstanceFixture(ctx, tool.ExternalView().UserConn(), request)
			Expect(e).NotTo(HaveOccurred())
			By("changing the policy and checking that only new instances receive it")
			_, e = items.Update(ctx, publicv1.BareMetalInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.BareMetalInstanceCatalogItem_builder{
					Id: item.GetId(),
					Fields: publicv1.BareMetalInstanceCatalogItemFields_builder{
						UserData: publicv1.StringFieldPolicy_builder{Locked: new("#cloud-config\n# second")}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields.user_data"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			second, e := createBareMetalInstanceFixture(ctx, tool.ExternalView().UserConn(), request)
			Expect(e).NotTo(HaveOccurred())
			client := publicv1.NewBareMetalInstancesClient(tool.ExternalView().UserConn())
			persistedSecond, e := client.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{Id: second.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			second = persistedSecond.GetObject()
			Expect(second.GetSpec().GetUserData()).To(Equal("#cloud-config\n# second"))
			stored, e := client.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{Id: first.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(stored.GetObject().GetSpec().GetUserData()).To(Equal("#cloud-config\n# first"))
			By("updating the original instance without reapplying the catalog policy")
			_, e = client.Update(ctx, publicv1.BareMetalInstancesUpdateRequest_builder{
				Object:     publicv1.BareMetalInstance_builder{Id: first.GetId(), Spec: publicv1.BareMetalInstanceSpec_builder{RunStrategy: new(publicv1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS)}.Build()}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.run_strategy"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			live, e := client.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{Id: first.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(live.GetObject().GetSpec().GetRunStrategy()).To(Equal(publicv1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS))

			By("restarting through the public API after catalog deletion")
			// The instance retains provenance, but reconciliation uses its materialized Template.
			_, e = items.Delete(ctx, publicv1.BareMetalInstanceCatalogItemsDeleteRequest_builder{Id: item.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			_, e = client.Update(ctx, publicv1.BareMetalInstancesUpdateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Id: first.GetId(),
					Spec: publicv1.BareMetalInstanceSpec_builder{
						CatalogItem:    publicv1.BareMetalInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
						RunStrategy:    new(publicv1.BareMetalInstanceRunStrategy_BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS),
						RestartTrigger: 1,
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.catalog_item",
					"spec.run_strategy",
					"spec.restart_trigger"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				objects := &bmfv1.BareMetalInstanceList{}
				g.Expect(tool.KubeClient().List(ctx, objects, crclient.MatchingLabels{labels.BareMetalInstanceUuid: first.GetId()})).To(Succeed())
				g.Expect(objects.Items).To(HaveLen(1))
				g.Expect(objects.Items[0].Spec.TemplateID).To(Equal(template))
				g.Expect(objects.Items[0].Spec.RestartTrigger).To(Equal(int64(1)))
			}, time.Minute, time.Second).Should(Succeed())
			internal := privatev1.NewBareMetalInstancesClient(tool.InternalView().AdminConn())
			By("restarting through the private API after catalog deletion")
			_, e = internal.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
				Object: privatev1.BareMetalInstance_builder{
					Id: first.GetId(),
					Spec: privatev1.BareMetalInstanceSpec_builder{
						CatalogItem:    privatev1.BareMetalInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
						RestartTrigger: 2,
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.catalog_item",
					"spec.restart_trigger"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				objects := &bmfv1.BareMetalInstanceList{}
				g.Expect(tool.KubeClient().List(ctx, objects, crclient.MatchingLabels{labels.BareMetalInstanceUuid: first.GetId()})).To(Succeed())
				g.Expect(objects.Items).To(HaveLen(1))
				g.Expect(objects.Items[0].Spec.RestartTrigger).To(Equal(int64(2)))
			}, time.Minute, time.Second).Should(Succeed())

			By("rejecting provenance clearing through the public API")
			_, e = client.Update(ctx, publicv1.BareMetalInstancesUpdateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Id:   first.GetId(),
					Spec: publicv1.BareMetalInstanceSpec_builder{}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.catalog_item"),
			}.Build())
			expectCatalogItemStatusCode(e, codes.InvalidArgument)
			By("rejecting provenance mutation through the public API")
			_, e = client.Update(ctx, publicv1.BareMetalInstancesUpdateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Id: first.GetId(),
					Spec: publicv1.BareMetalInstanceSpec_builder{
						CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: "different"}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.catalog_item"),
			}.Build())
			expectCatalogItemStatusCode(e, codes.InvalidArgument)

			By("rejecting provenance mutation and clearing through the private API")
			for _, tc := range []struct {
				name      string
				reference *privatev1.BareMetalInstanceCatalogItemReference
			}{
				{"different catalog item", privatev1.BareMetalInstanceCatalogItemReference_builder{Id: "different"}.Build()},
				{"cleared catalog item", nil},
			} {
				By(tc.name)
				_, e = internal.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
					Object: privatev1.BareMetalInstance_builder{
						Id:   first.GetId(),
						Spec: privatev1.BareMetalInstanceSpec_builder{CatalogItem: tc.reference}.Build(),
					}.Build(),
					UpdateMask: catalogItemUpdateMask("spec.catalog_item"),
				}.Build())
				expectCatalogItemStatusCode(e, codes.InvalidArgument)
			}
			persisted, e := client.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{Id: first.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(proto.Equal(persisted.GetObject().GetSpec().GetCatalogItem(), first.GetSpec().GetCatalogItem())).To(BeTrue())
			Expect(persisted.GetObject().GetSpec().GetTemplate().GetId()).To(Equal(template))

		})
	})
})
