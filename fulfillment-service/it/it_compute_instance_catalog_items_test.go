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
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var _ = Describe("Compute Instance Catalog Items", Label("catalog-items"), func() {
	Context("Provisioning and field governance", func() {
		It("materializes typed policies before Template defaults and persists explicit scalar presence", func(ctx context.Context) {
			By("authoring a tenant offering with locked and editable VM policies")
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			instanceType := createCatalogItemComputeInstanceTypeFixture(ctx)
			image := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
			overrideImage := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
			tier := createCatalogItemStorageTierFixture(ctx)
			template := createCatalogItemComputeInstanceTemplateFixture(ctx, privatev1.ComputeInstanceTemplateSpecDefaults_builder{
				BootDisk:    privatev1.ComputeInstanceDisk_builder{SizeGib: new(int32(10))}.Build(),
				RunStrategy: new(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS),
			}.Build(), computeInstanceCatalogItemParameterDefinitions())
			fields := publicv1.ComputeInstanceCatalogItemFields_builder{
				InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{
					Locked: publicv1.InstanceTypeReference_builder{Id: instanceType}.Build(),
				}.Build(),
				DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
					Editable: publicv1.EditableDiskImageReferenceField_builder{
						DefaultValue: publicv1.DiskImageReference_builder{Name: image.GetMetadata().GetName(), Shared: true}.Build(),
					}.Build(),
				}.Build(),
				UserData: publicv1.StringFieldPolicy_builder{Locked: new("")}.Build(),
				RunStrategy: publicv1.ComputeInstanceRunStrategyFieldPolicy_builder{
					Editable: publicv1.EditableComputeInstanceRunStrategyField_builder{}.Build(),
				}.Build(),

				BootDisk: publicv1.ComputeInstanceBootDiskFieldPolicies_builder{
					SizeGib: publicv1.Int32FieldPolicy_builder{
						Editable: publicv1.EditableInt32Field_builder{DefaultValue: new(int32(30))}.Build(),
					}.Build(),
					StorageTier: publicv1.StorageTierReferenceFieldPolicy_builder{
						Locked: publicv1.StorageTierReference_builder{Id: tier}.Build(),
					}.Build(),
				}.Build(),

				AdditionalDisks: publicv1.ComputeInstanceDiskListFieldPolicy_builder{
					Editable: publicv1.EditableComputeInstanceDiskList_builder{
						DefaultValue: publicv1.ComputeInstanceDiskList_builder{
							Items: []*publicv1.ComputeInstanceDisk{
								publicv1.ComputeInstanceDisk_builder{
									SizeGib:     new(int32(50)),
									StorageTier: publicv1.StorageTierReference_builder{Id: tier}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build(),

				NetworkAttachments: publicv1.ComputeNetworkAttachmentListFieldPolicy_builder{
					Locked: publicv1.ComputeNetworkAttachmentList_builder{Items: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()}}.Build(),
				}.Build(),
				AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{Locked: new(false)}.Build(),
			}.Build()

			item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata:           publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
				Template:           publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
				Published:          true,
				Fields:             fields,
				TemplateParameters: catalogItemParameterPolicies(),
			}.Build())

			By("creating a VM with caller-selected disk inputs")
			request := publicv1.ComputeInstanceSpec_builder{
				DiskImage:   publicv1.DiskImageReference_builder{Id: overrideImage.GetId()}.Build(),
				CatalogItem: publicv1.ComputeInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
				BootDisk:    publicv1.ComputeInstanceDisk_builder{SizeGib: new(int32(100))}.Build(),
				AdditionalDisks: []*publicv1.ComputeInstanceDisk{
					publicv1.ComputeInstanceDisk_builder{
						SizeGib:     new(int32(70)),
						StorageTier: publicv1.StorageTierReference_builder{Id: tier}.Build(),
					}.Build(),
				},
				TemplateParameters: map[string]*anypb.Any{"size": catalogItemParameterValue(wrapperspb.Int32(0))},
			}.Build()
			created, err := createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(), request)
			Expect(err).NotTo(HaveOccurred())

			By("checking the stored VM's materialized policies and Template inputs")
			client := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
			response, err := client.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: created.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			spec := response.GetObject().GetSpec()
			Expect(spec.GetInstanceType().GetId()).To(Equal(instanceType))
			Expect(spec.GetInstanceType().GetShared()).To(BeTrue())
			Expect(spec.GetDiskImage().GetId()).To(Equal(overrideImage.GetId()))
			Expect(spec.GetDiskImage().GetShared()).To(BeTrue())
			Expect(spec.HasUserData()).To(BeTrue())
			Expect(spec.GetUserData()).To(BeEmpty())
			Expect(spec.HasAutoExternalIpAttachment()).To(BeTrue())
			Expect(spec.GetAutoExternalIpAttachment()).To(BeFalse())
			Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(100)))
			Expect(spec.GetBootDisk().GetStorageTier().GetId()).To(Equal(tier))
			Expect(spec.GetAdditionalDisks()).To(HaveLen(1))
			Expect(spec.GetAdditionalDisks()[0].GetSizeGib()).To(Equal(int32(70)))
			Expect(spec.GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal(network.subnetID))
			Expect(spec.GetNetworkAttachments()[0].GetSecurityGroups()[0].GetId()).To(Equal(network.securityGroupID))
			Expect(spec.GetRunStrategy()).To(Equal(publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS))
			Expect(proto.Equal(spec.GetTemplateParameters()["enabled"], catalogItemParameterValue(wrapperspb.Bool(false)))).To(BeTrue())
			Expect(proto.Equal(spec.GetTemplateParameters()["size"], catalogItemParameterValue(wrapperspb.Int32(0)))).To(BeTrue())
			Expect(proto.Equal(spec.GetTemplateParameters()["ordinary"], catalogItemParameterValue(wrapperspb.String("template")))).To(BeTrue())
			Expect(spec.GetTemplate().GetId()).To(Equal(template))
			By("rejecting explicit locked inputs without persisting partially resolved candidates")
			type invalidInput struct {
				name string
				set  func(*publicv1.ComputeInstanceSpec)
			}
			inputs := []invalidInput{
				{
					name: "identical InstanceType",
					set: func(s *publicv1.ComputeInstanceSpec) {
						s.SetInstanceType(publicv1.InstanceTypeReference_builder{Id: instanceType}.Build())
					},
				},
				{
					name: "empty user data",
					set: func(s *publicv1.ComputeInstanceSpec) {
						s.SetUserData("")
					},
				},
				{
					name: "false external IP",
					set: func(s *publicv1.ComputeInstanceSpec) {
						s.SetAutoExternalIpAttachment(false)
					},
				},
				{
					name: "identical storage tier",
					set: func(s *publicv1.ComputeInstanceSpec) {
						s.GetBootDisk().SetStorageTier(publicv1.StorageTierReference_builder{Id: tier}.Build())
					},
				},
				{
					name: "identical network",
					set: func(s *publicv1.ComputeInstanceSpec) {
						s.SetNetworkAttachments([]*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()})
					},
				},
				{
					name: "locked parameter",
					set: func(s *publicv1.ComputeInstanceSpec) {
						s.GetTemplateParameters()["enabled"] = catalogItemParameterValue(wrapperspb.Bool(false))
					},
				},
			}
			for _, input := range inputs {
				By(input.name)
				candidate := proto.Clone(request).(*publicv1.ComputeInstanceSpec)
				input.set(candidate)
				name := catalogItemFixtureName()
				_, err = client.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
					Object: publicv1.ComputeInstance_builder{
						Metadata: publicv1.Metadata_builder{Name: name}.Build(),
						Spec:     candidate,
					}.Build(),
				}.Build())
				expectCatalogItemStatusCode(err, codes.InvalidArgument)
				list, e := client.List(ctx, publicv1.ComputeInstancesListRequest_builder{Filter: new("this.metadata.name == '" + name + "'")}.Build())
				Expect(e).NotTo(HaveOccurred())
				Expect(list.GetItems()).To(BeEmpty())
			}
			By("using catalog item defaults when editable collections and numeric fields are omitted")
			defaulted, err := createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(), publicv1.ComputeInstanceSpec_builder{
				CatalogItem:     publicv1.ComputeInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
				AdditionalDisks: []*publicv1.ComputeInstanceDisk{},
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			persistedDefaults, err := client.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: defaulted.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			defaulted = persistedDefaults.GetObject()
			Expect(defaulted.GetSpec().GetDiskImage().GetId()).To(Equal(image.GetId()))
			Expect(proto.Equal(defaulted.GetSpec().GetTemplateParameters()["size"], catalogItemParameterValue(wrapperspb.Int32(20)))).To(BeTrue())
			Expect(defaulted.GetSpec().GetBootDisk().GetSizeGib()).To(Equal(int32(30)))
			Expect(defaulted.GetSpec().GetAdditionalDisks()[0].GetSizeGib()).To(Equal(int32(50)))
			By("materializing an authored locked empty disk list instead of the previous editable default")
			_, err = publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().AdminConn()).Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{
					Id: item.GetId(),
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
						AdditionalDisks: publicv1.ComputeInstanceDiskListFieldPolicy_builder{Locked: publicv1.ComputeInstanceDiskList_builder{}.Build()}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields.additional_disks"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			emptyDisks, err := createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(), publicv1.ComputeInstanceSpec_builder{
				CatalogItem: publicv1.ComputeInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			persistedEmptyDisks, err := client.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: emptyDisks.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(persistedEmptyDisks.GetObject().GetSpec().GetAdditionalDisks()).To(BeEmpty())
			_, err = createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(), request)
			expectCatalogItemStatusCode(err, codes.InvalidArgument)

		})
		It("rejects an unresolved required instance type and accepts an editable caller value", func(ctx context.Context) {
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			instanceType := createCatalogItemComputeInstanceTypeFixture(ctx)
			image := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
			tier := createCatalogItemStorageTierFixture(ctx)
			template := createCatalogItemComputeInstanceTemplateFixture(ctx, privatev1.ComputeInstanceTemplateSpecDefaults_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: image.GetId()}.Build(),
				BootDisk: privatev1.ComputeInstanceDisk_builder{
					SizeGib:     new(int32(30)),
					StorageTier: privatev1.StorageTierReference_builder{Id: tier}.Build(),
				}.Build(),
				RunStrategy: new(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS),
			}.Build(), nil)
			item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
					InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{
						Editable: publicv1.EditableInstanceTypeReferenceField_builder{}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			client := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
			name := catalogItemFixtureName()
			spec := publicv1.ComputeInstanceSpec_builder{
				CatalogItem:        publicv1.ComputeInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
				NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
			}.Build()
			_, err := client.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: name}.Build(),
					Spec:     spec,
				}.Build(),
			}.Build())
			expectCatalogItemStatusCode(err, codes.InvalidArgument)
			Expect(status.Convert(err).Message()).To(ContainSubstring("instance_type"))
			listed, err := client.List(ctx, publicv1.ComputeInstancesListRequest_builder{Filter: new("this.metadata.name == '" + name + "'")}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetItems()).To(BeEmpty())
			spec.SetInstanceType(publicv1.InstanceTypeReference_builder{Id: instanceType}.Build())
			created, err := createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(), spec)
			Expect(err).NotTo(HaveOccurred())
			persisted, err := client.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: created.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(persisted.GetObject().GetSpec().GetInstanceType().GetId()).To(Equal(instanceType))
		})
		It("validates dry-run candidates and direct provisioning without networking or persistence side effects", func(ctx context.Context) {
			By("authoring a shared offering with user-data and external-IP defaults")
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
			item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
					UserData: publicv1.StringFieldPolicy_builder{
						Editable: publicv1.EditableStringField_builder{DefaultValue: new("catalog item")}.Build(),
					}.Build(),
					AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{
						Editable: publicv1.EditableBoolField_builder{DefaultValue: new(true)}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			client := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
			externalIPs := privatev1.NewExternalIPsClient(tool.InternalView().AdminConn())
			before, e := externalIPs.List(ctx, privatev1.ExternalIPsListRequest_builder{}.Build())
			Expect(e).NotTo(HaveOccurred())
			name := catalogItemFixtureName()
			spec := publicv1.ComputeInstanceSpec_builder{
				CatalogItem:        publicv1.ComputeInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
				NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
			}.Build()
			dry := metadata.AppendToOutgoingContext(ctx, "x-dry-run", "true")

			By("validating a dry-run VM without storing it or allocating an external IP")
			result, e := client.Create(dry, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: name}.Build(),
					Spec:     spec,
				}.Build(),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(result.GetObject().GetSpec().GetUserData()).To(Equal("catalog item"))
			Expect(result.GetObject().GetSpec().GetAutoExternalIpAttachment()).To(BeTrue())
			listed, e := client.List(ctx, publicv1.ComputeInstancesListRequest_builder{Filter: new("this.metadata.name == '" + name + "'")}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(listed.GetItems()).To(BeEmpty())
			after, e := externalIPs.List(ctx, privatev1.ExternalIPsListRequest_builder{}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(after.GetTotal()).To(Equal(before.GetTotal()))

			By("rejecting a request that names both the offering and its Template")
			spec.SetTemplate(publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build())
			_, e = client.Create(dry, publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec:     spec,
				}.Build(),
			}.Build())
			expectCatalogItemStatusCode(e, codes.InvalidArgument)

			By("creating directly from the Template with caller-supplied values")
			spec.ClearCatalogItem()
			spec.SetAutoExternalIpAttachment(false)
			spec.SetUserData("direct")
			direct, e := createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(), spec)
			Expect(e).NotTo(HaveOccurred())
			Expect(direct.GetSpec().HasCatalogItem()).To(BeFalse())
			Expect(direct.GetSpec().GetUserData()).To(Equal("direct"))

			By("rejecting creation without a Catalog Item or Template")
			spec.ClearTemplate()
			_, e = createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(), spec)
			expectCatalogItemStatusCode(e, codes.InvalidArgument)
		})
		It("validates caller compatibility and checks readiness again after policy materialization", func(ctx context.Context) {
			template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			other := createCatalogItemNetworkInClassFixture(ctx, usersGroup, "", network.networkClassID)
			item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
				Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
					NetworkAttachments: publicv1.ComputeNetworkAttachmentListFieldPolicy_builder{
						Editable: publicv1.EditableComputeNetworkAttachmentList_builder{
							DefaultValue: publicv1.ComputeNetworkAttachmentList_builder{Items: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()}}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			bad := network.computeInstanceAttachment()
			bad.SetSecurityGroups([]*publicv1.SecurityGroupLocalReference{
				publicv1.SecurityGroupLocalReference_builder{Id: other.securityGroupID}.Build(),
			})
			client := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
			request := publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						CatalogItem:        publicv1.ComputeInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{bad},
					}.Build(),
				}.Build(),
			}.Build()
			dry := metadata.AppendToOutgoingContext(ctx, "x-dry-run", "true")
			_, err := client.Create(dry, request)
			expectCatalogItemStatusCode(err, codes.InvalidArgument)
			request.GetObject().GetSpec().SetNetworkAttachments(nil)
			setCatalogItemSubnetFixtureState(ctx, network.subnetID, privatev1.SubnetState_SUBNET_STATE_PENDING)
			_, err = client.Create(dry, request)
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)
		})

		It("checks user-data Secret conflicts after defaults and releases the conflict when the policy is cleared", func(ctx context.Context) {
			secret := createCatalogItemUserDataSecretFixture(ctx, usersGroup)
			template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
			item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
					UserData: publicv1.StringFieldPolicy_builder{
						Editable: publicv1.EditableStringField_builder{DefaultValue: new("#cloud-config\n")}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			client := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
			request := publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						CatalogItem:        publicv1.ComputeInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
						UserDataSecret:     publicv1.SecretLocalReference_builder{Id: secret}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{createCatalogItemNetworkFixture(ctx, usersGroup, "").computeInstanceAttachment()},
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
			_, err = publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().AdminConn()).Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{
					Id:     item.GetId(),
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{}.Build(),
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
		It("changes parameter policies only for future provisioning and permits ungoverned structured input", func(ctx context.Context) {
			By("publishing a VM offering with a locked size and an ungoverned structured parameter")
			template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, []*privatev1.ComputeInstanceTemplateParameterDefinition{
				privatev1.ComputeInstanceTemplateParameterDefinition_builder{Name: "size", Type: "type.googleapis.com/google.protobuf.Int32Value", Default: catalogItemParameterValue(wrapperspb.Int32(10))}.Build(),
				privatev1.ComputeInstanceTemplateParameterDefinition_builder{Name: "structured", Type: "type.googleapis.com/google.protobuf.Value"}.Build(),
			})
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template: publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(), Published: true,
				TemplateParameters: map[string]*publicv1.TemplateParameterPolicy{"size": publicv1.TemplateParameterPolicy_builder{Locked: catalogItemParameterValue(wrapperspb.Int32(20))}.Build()},
			}.Build())
			structured := catalogItemParameterValue(structpb.NewStructValue(&structpb.Struct{Fields: map[string]*structpb.Value{"key": structpb.NewStringValue("value")}}))
			spec := publicv1.ComputeInstanceSpec_builder{
				CatalogItem:        publicv1.ComputeInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
				NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
				TemplateParameters: map[string]*anypb.Any{"structured": structured},
			}.Build()
			first, err := createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(), spec)
			Expect(err).NotTo(HaveOccurred())

			By("rejecting a caller override while size is locked")
			items := publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().AdminConn())
			objects := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
			dry := metadata.AppendToOutgoingContext(ctx, "x-dry-run", "true")
			request := publicv1.ComputeInstancesCreateRequest_builder{Object: publicv1.ComputeInstance_builder{Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(), Spec: spec}.Build()}.Build()
			spec.GetTemplateParameters()["size"] = catalogItemParameterValue(wrapperspb.Int32(30))
			_, err = objects.Create(dry, request)
			expectCatalogItemStatusCode(err, codes.InvalidArgument)

			By("allowing caller input and a default after making size editable")
			_, err = items.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{Id: item.GetId(), TemplateParameters: map[string]*publicv1.TemplateParameterPolicy{
					"size": publicv1.TemplateParameterPolicy_builder{Editable: publicv1.EditableTemplateParameter_builder{DefaultValue: catalogItemParameterValue(wrapperspb.Int32(40))}.Build()}.Build(),
				}}.Build(), UpdateMask: catalogItemUpdateMask("template_parameters"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			overridden, err := objects.Create(dry, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(overridden.GetObject().GetSpec().GetTemplateParameters()["size"], catalogItemParameterValue(wrapperspb.Int32(30)))).To(BeTrue())
			delete(spec.GetTemplateParameters(), "size")
			defaulted, err := objects.Create(dry, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(defaulted.GetObject().GetSpec().GetTemplateParameters()["size"], catalogItemParameterValue(wrapperspb.Int32(40)))).To(BeTrue())

			By("falling back to the Template default after clearing the size policy")
			_, err = items.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{Id: item.GetId()}.Build(), UpdateMask: catalogItemUpdateMask("template_parameters"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			fallback, err := objects.Create(dry, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(fallback.GetObject().GetSpec().GetTemplateParameters()["size"], catalogItemParameterValue(wrapperspb.Int32(10)))).To(BeTrue())

			By("keeping the original VM's resolved size and structured input")
			persisted, err := objects.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: first.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(persisted.GetObject().GetSpec().GetTemplateParameters()["size"], catalogItemParameterValue(wrapperspb.Int32(20)))).To(BeTrue())
			Expect(proto.Equal(persisted.GetObject().GetSpec().GetTemplateParameters()["structured"], structured)).To(BeTrue())
		})

		It("distinguishes editable required input from Template defaults and invalid values", func(ctx context.Context) {
			By("publishing a VM offering with a required editable parameter")
			template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, computeInstanceCatalogItemParameterDefinitions())
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
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
			client := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
			request := publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						CatalogItem:        publicv1.ComputeInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
					}.Build(),
				}.Build(),
			}.Build()
			dry := metadata.AppendToOutgoingContext(ctx, "x-dry-run", "true")

			By("rejecting a VM request that omits the required parameter")
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
			template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
			tenant, conn := createCatalogItemTenantAdminFixture(ctx)
			items := publicv1.NewComputeInstanceCatalogItemsClient(conn)
			owned := createComputeInstanceCatalogItemFixture(ctx, conn, publicv1.ComputeInstanceCatalogItem_builder{
				Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template: publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
			}.Build())
			Expect(owned.GetMetadata().GetTenant()).To(Equal(tenant), "tenant authoring scope")

			By("publishing governed fields for members of the tenant")
			memberConn := createCatalogItemMemberFixture(ctx, tenant)
			memberItems := publicv1.NewComputeInstanceCatalogItemsClient(memberConn)
			_, err := items.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{Id: owned.GetId(), Published: true,
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
						UserData:                 publicv1.StringFieldPolicy_builder{Editable: publicv1.EditableStringField_builder{DefaultValue: new("tenant default")}.Build()}.Build(),
						AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{Locked: new(false)}.Build(),
					}.Build(),
				}.Build(), UpdateMask: catalogItemUpdateMask("published", "fields"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			visible, err := memberItems.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: owned.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(visible.GetObject().GetFields().GetUserData().GetEditable().GetDefaultValue()).To(Equal("tenant default"))
			Expect(visible.GetObject().GetFields().GetAutoExternalIpAttachment().HasLocked()).To(BeTrue())
			Expect(visible.GetObject().GetFields().GetAutoExternalIpAttachment().GetLocked()).To(BeFalse())

			By("provisioning a VM as a member with an editable override")
			network := createCatalogItemNetworkFixture(ctx, tenant, "")
			spec := publicv1.ComputeInstanceSpec_builder{
				CatalogItem:        publicv1.ComputeInstanceCatalogItemReference_builder{Id: owned.GetId()}.Build(),
				UserData:           new("member input"),
				NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
			}.Build()
			created, err := createComputeInstanceFixture(ctx, memberConn, spec)
			Expect(err).NotTo(HaveOccurred())
			objects := publicv1.NewComputeInstancesClient(memberConn)
			stored, err := objects.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: created.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(stored.GetObject().GetMetadata().GetTenant()).To(Equal(tenant))
			Expect(stored.GetObject().GetSpec().GetUserData()).To(Equal("member input"))
			Expect(stored.GetObject().GetSpec().HasAutoExternalIpAttachment()).To(BeTrue())
			Expect(stored.GetObject().GetSpec().GetAutoExternalIpAttachment()).To(BeFalse())

			By("hiding the tenant offering from an unrelated tenant")
			outsider := publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().UserConn())
			_, err = outsider.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: owned.GetId()}.Build())
			expectCatalogItemStatusCode(err, codes.NotFound)
			hidden, err := outsider.List(ctx, publicv1.ComputeInstanceCatalogItemsListRequest_builder{Filter: new("this.id == '" + owned.GetId() + "'")}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(hidden.GetItems()).To(BeEmpty())
			_, err = createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(), publicv1.ComputeInstanceSpec_builder{
				CatalogItem: publicv1.ComputeInstanceCatalogItemReference_builder{Id: owned.GetId()}.Build(),
			}.Build())
			expectCatalogItemStatusCode(err, codes.NotFound)

			By("denying catalog deletion to a tenant member")
			_, err = memberItems.Delete(ctx, publicv1.ComputeInstanceCatalogItemsDeleteRequest_builder{Id: owned.GetId()}.Build())
			expectCatalogItemStatusCode(err, codes.PermissionDenied)

			By("stopping new member provisioning after unpublishing")
			_, err = items.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object:     publicv1.ComputeInstanceCatalogItem_builder{Id: owned.GetId(), Published: false}.Build(),
				UpdateMask: catalogItemUpdateMask("published"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = createComputeInstanceFixture(ctx, memberConn, spec)
			expectCatalogItemStatusCode(err, codes.NotFound)
		})

		It("lists shared published items and protects provider authoring", func(ctx context.Context) {
			template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
			_, conn := createCatalogItemTenantAdminFixture(ctx)
			items := publicv1.NewComputeInstanceCatalogItemsClient(conn)
			user := publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().UserConn())
			shared := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template: publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
			}.Build())
			Expect(shared.GetMetadata().GetTenant()).To(Equal("shared"))
			_, err := user.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: shared.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = items.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object:     publicv1.ComputeInstanceCatalogItem_builder{Id: shared.GetId(), Title: "Foreign edit"}.Build(),
				UpdateMask: catalogItemUpdateMask("title"),
			}.Build())
			expectCatalogItemStatusCode(err, codes.PermissionDenied)
			listed, err := user.List(ctx, publicv1.ComputeInstanceCatalogItemsListRequest_builder{Filter: new("this.id == '" + shared.GetId() + "' && this.published")}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetItems()).To(BeEmpty())
			provider := publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().AdminConn())
			_, err = provider.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object:     publicv1.ComputeInstanceCatalogItem_builder{Id: shared.GetId(), Published: true}.Build(),
				UpdateMask: catalogItemUpdateMask("published"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			listed, err = user.List(ctx, publicv1.ComputeInstanceCatalogItemsListRequest_builder{Filter: new("this.id == '" + shared.GetId() + "' && this.published")}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetItems()).To(HaveLen(1))
		})
		It("keeps unmasked policies and atomically rejects invalid merged candidates", func(ctx context.Context) {
			By("authoring an offering with two field policies")
			template := createCatalogItemComputeInstanceTemplateFixture(ctx, nil, nil)
			item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
					UserData:                 publicv1.StringFieldPolicy_builder{Locked: new("initial")}.Build(),
					AutoExternalIpAttachment: publicv1.BoolFieldPolicy_builder{Locked: new(false)}.Build(),
				}.Build(),
			}.Build())
			client := publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().AdminConn())

			By("editing one policy while retaining the other")
			_, err := client.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{
					Id: item.GetId(),
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
						UserData: publicv1.StringFieldPolicy_builder{
							Editable: publicv1.EditableStringField_builder{DefaultValue: new("edited")}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields.user_data"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			before, err := client.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(before.GetObject().GetFields().GetAutoExternalIpAttachment().HasLocked()).To(BeTrue())
			Expect(before.GetObject().GetFields().GetAutoExternalIpAttachment().GetLocked()).To(BeFalse())
			Expect(before.GetObject().GetFields().GetUserData().HasLocked()).To(BeFalse())
			Expect(before.GetObject().GetFields().GetUserData().GetEditable().GetDefaultValue()).To(Equal("edited"))

			By("rejecting an invalid policy without changing the offering title")
			_, err = client.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{
					Id:    item.GetId(),
					Title: "must not persist",
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
						UserData: publicv1.StringFieldPolicy_builder{}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("title", "fields.user_data"),
			}.Build())
			expectCatalogItemStatusCode(err, codes.InvalidArgument)
			after, err := client.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(before.GetObject(), after.GetObject())).To(BeTrue())

			By("clearing all field policies explicitly")
			_, err = client.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{
					Id:     item.GetId(),
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			after, err = client.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(after.GetObject().GetFields().GetUserData()).To(BeNil())
			Expect(after.GetObject().GetFields().GetAutoExternalIpAttachment()).To(BeNil())
		})

		DescribeTable("rejects authored empty network collections without creating an offering", func(ctx context.Context, policy func() *publicv1.ComputeNetworkAttachmentListFieldPolicy) {
			template := createCatalogItemComputeInstanceTemplateFixture(ctx, nil, nil)
			client := publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().AdminConn())
			name := catalogItemFixtureName()
			_, err := client.Create(ctx, publicv1.ComputeInstanceCatalogItemsCreateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{Name: name, Tenant: usersGroup}.Build(),
					Template: publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
					Fields:   publicv1.ComputeInstanceCatalogItemFields_builder{NetworkAttachments: policy()}.Build(),
				}.Build(),
			}.Build())
			expectCatalogItemStatusCode(err, codes.InvalidArgument)
			list, err := client.List(ctx, publicv1.ComputeInstanceCatalogItemsListRequest_builder{Filter: new("this.metadata.name == '" + name + "'")}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(list.GetItems()).To(BeEmpty())
		},
			Entry("locked list", func() *publicv1.ComputeNetworkAttachmentListFieldPolicy {
				return publicv1.ComputeNetworkAttachmentListFieldPolicy_builder{
					Locked: publicv1.ComputeNetworkAttachmentList_builder{}.Build(),
				}.Build()
			}),
			Entry("editable default list", func() *publicv1.ComputeNetworkAttachmentListFieldPolicy {
				return publicv1.ComputeNetworkAttachmentListFieldPolicy_builder{
					Editable: publicv1.EditableComputeNetworkAttachmentList_builder{
						DefaultValue: publicv1.ComputeNetworkAttachmentList_builder{}.Build(),
					}.Build(),
				}.Build()
			}),
		)
	})
	Context("Referenced objects", func() {
		It("protects its immutable Template in a draft and releases it on catalog item deletion", func(ctx context.Context) {
			template := createCatalogItemComputeInstanceTemplateFixture(ctx, nil, nil)
			item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template: publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
			}.Build())
			templates := privatev1.NewComputeInstanceTemplatesClient(tool.InternalView().AdminConn())
			_, err := templates.Delete(ctx, privatev1.ComputeInstanceTemplatesDeleteRequest_builder{Id: template}.Build())
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)
			_, err = publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().AdminConn()).Delete(ctx, publicv1.ComputeInstanceCatalogItemsDeleteRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = templates.Delete(ctx, privatev1.ComputeInstanceTemplatesDeleteRequest_builder{Id: template}.Build())
			Expect(err).NotTo(HaveOccurred())
		})
		It("protects referenced objects through publication and policy changes", func(ctx context.Context) {
			By("authoring a published compute offering with a locked dependency")
			template := createCatalogItemComputeInstanceTemplateFixture(ctx, nil, nil)
			id := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName()).GetId()
			items := publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().AdminConn())
			dependencies := privatev1.NewDiskImagesClient(tool.InternalView().AdminConn())
			item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
				Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
					DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
						Locked: publicv1.DiskImageReference_builder{Id: id}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			other := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template: publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
				Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
					DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
						Locked: publicv1.DiskImageReference_builder{Id: id}.Build(),
					}.Build(),
				}.Build(),
			}.Build())

			By("blocking deletion while the dependency is locked")
			_, err := dependencies.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{Id: id}.Build())
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)

			By("retaining protection after unpublishing and switching to an editable default")
			_, err = items.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{
					Id:        item.GetId(),
					Published: false,
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
						DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
							Editable: publicv1.EditableDiskImageReferenceField_builder{
								DefaultValue: publicv1.DiskImageReference_builder{Id: id}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("published", "fields.disk_image"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = dependencies.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{Id: id}.Build())
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)

			By("clearing the dependency policy")
			_, err = items.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{Id: item.GetId()}.Build(), UpdateMask: catalogItemUpdateMask("fields.disk_image"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = dependencies.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{Id: id}.Build())
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)

			By("deleting the second offering before releasing the shared disk image")
			_, err = items.Delete(ctx, publicv1.ComputeInstanceCatalogItemsDeleteRequest_builder{Id: other.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = dependencies.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{Id: id}.Build())

			Expect(err).NotTo(HaveOccurred())
		})

		It("uses catalog item owner scope for names, preserves selected scope, and rejects wrong-project local inputs", func(ctx context.Context) {
			By("authoring a project offering that resolves a same-name disk image in its own project")
			project := createCatalogItemProjectFixture(ctx, usersGroup)
			otherProject := createCatalogItemProjectFixture(ctx, usersGroup)
			name := catalogItemFixtureName()
			image := createCatalogItemDiskImageInProjectFixture(ctx, usersGroup, project, name)
			otherImage := createCatalogItemDiskImageInProjectFixture(ctx, usersGroup, otherProject, name)
			network := createCatalogItemNetworkFixture(ctx, usersGroup, project)
			foreignNetwork := createCatalogItemNetworkInClassFixture(ctx, usersGroup, otherProject, network.networkClassID)
			template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
			item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup, Project: project}.Build(),
				Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
					DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
						Locked: publicv1.DiskImageReference_builder{Name: name, Project: project}.Build(),
					}.Build(),
					NetworkAttachments: publicv1.ComputeNetworkAttachmentListFieldPolicy_builder{
						Editable: publicv1.EditableComputeNetworkAttachmentList_builder{
							DefaultValue: publicv1.ComputeNetworkAttachmentList_builder{Items: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()}}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(item.GetFields().GetDiskImage().GetLocked().GetId()).To(Equal(image.GetId()))
			Expect(item.GetFields().GetDiskImage().GetLocked().GetProject()).To(Equal(project))

			By("resolving the offering and network defaults within the selected project")
			// The provider can see both projects; dependencies must still respect resource ownership.
			client := publicv1.NewComputeInstancesClient(tool.ExternalView().AdminConn())
			dry := metadata.AppendToOutgoingContext(ctx, "x-dry-run", "true")
			request := publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup, Project: project}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						CatalogItem: publicv1.ComputeInstanceCatalogItemReference_builder{Name: item.GetMetadata().GetName(), Project: project}.Build(),
					}.Build(),
				}.Build(),
			}.Build()
			result, err := client.Create(dry, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.GetObject().GetSpec().GetCatalogItem().GetId()).To(Equal(item.GetId()))
			Expect(result.GetObject().GetSpec().GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal(network.subnetID))

			By("rejecting a network attachment from another project")
			request.GetObject().GetSpec().SetNetworkAttachments([]*publicv1.ComputeNetworkAttachment{foreignNetwork.computeInstanceAttachment()})
			_, err = client.Create(dry, request)
			expectCatalogItemStatusCode(err, codes.InvalidArgument)

			By("retargeting the disk image policy to the same-name image in another project")
			items := publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().AdminConn())
			_, err = items.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{
					Id: item.GetId(),
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
						DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
							Locked: publicv1.DiskImageReference_builder{Name: name, Project: otherProject}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields.disk_image"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			updated, err := items.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.GetObject().GetFields().GetDiskImage().GetLocked().GetId()).To(Equal(otherImage.GetId()))
			Expect(updated.GetObject().GetFields().GetDiskImage().GetLocked().GetProject()).To(Equal(otherProject))

			By("rejecting a local network policy that points outside the offering's project")
			_, err = items.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{
					Id: item.GetId(),
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
						NetworkAttachments: publicv1.ComputeNetworkAttachmentListFieldPolicy_builder{
							Locked: publicv1.ComputeNetworkAttachmentList_builder{Items: []*publicv1.ComputeNetworkAttachment{foreignNetwork.computeInstanceAttachment()}}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields.network_attachments"),
			}.Build())
			expectCatalogItemStatusCode(err, codes.InvalidArgument)
		})
		It("canonicalizes names in owner scope and revalidates retained dependencies on configuration changes", func(ctx context.Context) {
			By("resolving a same-name disk image in the catalog owner's tenant")
			template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
			name := catalogItemFixtureName()
			shared := createCatalogItemDiskImageFixture(ctx, "shared", name)
			local := createCatalogItemDiskImageFixture(ctx, usersGroup, name)
			client := publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().AdminConn())
			item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
				Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
					DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
						Locked: publicv1.DiskImageReference_builder{Name: name}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(item.GetFields().GetDiskImage().GetLocked().GetId()).To(Equal(local.GetId()))
			Expect(item.GetFields().GetDiskImage().GetLocked().GetShared()).To(BeFalse())
			for _, tc := range []struct {
				name      string
				reference *publicv1.DiskImageReference
				code      codes.Code
			}{
				{"mismatched ID and name", publicv1.DiskImageReference_builder{Id: local.GetId(), Name: "disagrees"}.Build(), codes.InvalidArgument},
				{"shared image in an invalid project", publicv1.DiskImageReference_builder{Name: name, Shared: true, Project: "invalid"}.Build(), codes.NotFound},
			} {
				By(tc.name)
				_, err := client.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
					Object: publicv1.ComputeInstanceCatalogItem_builder{
						Id: item.GetId(),
						Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
							DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{Locked: tc.reference}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: catalogItemUpdateMask("fields.disk_image"),
				}.Build())
				expectCatalogItemStatusCode(err, tc.code)
			}
			By("switching the policy explicitly to the shared disk image")
			_, err := client.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{
					Id: item.GetId(),
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
						DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
							Locked: publicv1.DiskImageReference_builder{Name: name, Shared: true}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields.disk_image"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			By("revalidating a retained reference as the image becomes deprecated and obsolete")
			images := privatev1.NewDiskImagesClient(tool.InternalView().AdminConn())
			_, err = images.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Id:   shared.GetId(),
					Spec: privatev1.DiskImageSpec_builder{Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.lifecycle"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			dry := metadata.AppendToOutgoingContext(ctx, "x-dry-run", "true")
			resources := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
			request := publicv1.ComputeInstancesCreateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						CatalogItem:        publicv1.ComputeInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
						NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
					}.Build(),
				}.Build(),
			}.Build()
			result, err := resources.Create(dry, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.GetWarnings()).To(ContainElement(ContainSubstring("deprecated")))
			_, err = images.Update(ctx, privatev1.DiskImagesUpdateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Id:   shared.GetId(),
					Spec: privatev1.DiskImageSpec_builder{Lifecycle: privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.lifecycle"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = resources.Create(dry, request)
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)

			By("retiring the offering without changing its obsolete image policy")
			_, err = client.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{Id: item.GetId(), Title: "Retired offering", Published: false}.Build(),
				UpdateMask: catalogItemUpdateMask("title",
					"published"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			before, err := client.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())

			By("rejecting publication and field changes that revalidate the obsolete image")
			for _, tc := range []struct {
				name string
				mask string
			}{
				{name: "republishing the offering", mask: "published"},
				{name: "changing an unrelated field policy", mask: "fields.user_data"},
			} {
				By(tc.name)
				_, err = client.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
					Object: publicv1.ComputeInstanceCatalogItem_builder{
						Id:        item.GetId(),
						Published: true,
						Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
							UserData: publicv1.StringFieldPolicy_builder{Locked: new("changed")}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: catalogItemUpdateMask(tc.mask),
				}.Build())
				expectCatalogItemStatusCode(err, codes.FailedPrecondition)
				after, e := client.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
				Expect(e).NotTo(HaveOccurred())
				Expect(proto.Equal(before.GetObject(), after.GetObject())).To(BeTrue())
			}
		})
		It("rejects foreign-tenant and shared local policies after resolving references", func(ctx context.Context) {
			template := createCatalogItemComputeInstanceTemplateFixture(ctx, nil, nil)
			tenant, _ := createCatalogItemTenantAdminFixture(ctx)
			foreign := createCatalogItemDiskImageFixture(ctx, tenant, catalogItemFixtureName())
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			client := publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().AdminConn())
			for _, tc := range []struct {
				name      string
				candidate *publicv1.ComputeInstanceCatalogItem
			}{
				{"foreign tenant disk image", publicv1.ComputeInstanceCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName(), Tenant: usersGroup}.Build(),
					Template: publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
						DiskImage: publicv1.DiskImageReferenceFieldPolicy_builder{
							Locked: publicv1.DiskImageReference_builder{Id: foreign.GetId()}.Build(),
						}.Build(),
					}.Build(),
				}.Build()},
				{"shared catalog with tenant-local network", publicv1.ComputeInstanceCatalogItem_builder{
					Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
					Template: publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
						NetworkAttachments: publicv1.ComputeNetworkAttachmentListFieldPolicy_builder{
							Locked: publicv1.ComputeNetworkAttachmentList_builder{Items: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()}}.Build(),
						}.Build(),
					}.Build(),
				}.Build()},
			} {
				By(tc.name)
				_, err := client.Create(ctx, publicv1.ComputeInstanceCatalogItemsCreateRequest_builder{Object: tc.candidate}.Build())
				expectCatalogItemStatusCode(err, codes.InvalidArgument)
			}
		})

		It("keeps protection until the last reference is cleared and transfers protection on replacement", func(ctx context.Context) {
			By("authoring an offering that references the same storage tier in boot and additional disks")
			template := createCatalogItemComputeInstanceTemplateFixture(ctx, nil, nil)
			oldTier := createCatalogItemStorageTierFixture(ctx)
			newTier := createCatalogItemStorageTierFixture(ctx)
			item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template: publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
				Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
					BootDisk: publicv1.ComputeInstanceBootDiskFieldPolicies_builder{
						StorageTier: publicv1.StorageTierReferenceFieldPolicy_builder{
							Locked: publicv1.StorageTierReference_builder{Id: oldTier}.Build(),
						}.Build(),
					}.Build(),
					AdditionalDisks: publicv1.ComputeInstanceDiskListFieldPolicy_builder{
						Locked: publicv1.ComputeInstanceDiskList_builder{
							Items: []*publicv1.ComputeInstanceDisk{
								publicv1.ComputeInstanceDisk_builder{
									SizeGib:     new(int32(10)),
									StorageTier: publicv1.StorageTierReference_builder{Id: oldTier}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			client := publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().AdminConn())
			tiers := privatev1.NewStorageTiersClient(tool.InternalView().AdminConn())

			By("clearing the boot disk policy while the additional disk still protects the old tier")
			_, err := client.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{
					Id: item.GetId(),
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
						BootDisk: publicv1.ComputeInstanceBootDiskFieldPolicies_builder{}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields.boot_disk.storage_tier"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = tiers.Delete(ctx, privatev1.StorageTiersDeleteRequest_builder{Id: oldTier}.Build())
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)

			By("replacing the additional disk tier and transferring deletion protection")
			_, err = client.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{
					Id: item.GetId(),
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
						AdditionalDisks: publicv1.ComputeInstanceDiskListFieldPolicy_builder{
							Locked: publicv1.ComputeInstanceDiskList_builder{
								Items: []*publicv1.ComputeInstanceDisk{
									publicv1.ComputeInstanceDisk_builder{
										SizeGib:     new(int32(20)),
										StorageTier: publicv1.StorageTierReference_builder{Id: newTier}.Build(),
									}.Build(),
								},
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields.additional_disks"),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = tiers.Delete(ctx, privatev1.StorageTiersDeleteRequest_builder{Id: oldTier}.Build())
			Expect(err).NotTo(HaveOccurred())
			_, err = tiers.Delete(ctx, privatev1.StorageTiersDeleteRequest_builder{Id: newTier}.Build())
			expectCatalogItemStatusCode(err, codes.FailedPrecondition)

			By("rejecting invalid disk policies without changing the stored offering")
			before, err := client.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			for _, tc := range []struct {
				name   string
				fields *publicv1.ComputeInstanceCatalogItemFields
			}{
				{"zero boot disk size", publicv1.ComputeInstanceCatalogItemFields_builder{
					BootDisk: publicv1.ComputeInstanceBootDiskFieldPolicies_builder{
						SizeGib: publicv1.Int32FieldPolicy_builder{Locked: new(int32(0))}.Build(),
					}.Build(),
				}.Build()},
				{"negative additional disk size", publicv1.ComputeInstanceCatalogItemFields_builder{
					AdditionalDisks: publicv1.ComputeInstanceDiskListFieldPolicy_builder{
						Locked: publicv1.ComputeInstanceDiskList_builder{
							Items: []*publicv1.ComputeInstanceDisk{
								publicv1.ComputeInstanceDisk_builder{
									SizeGib:     new(int32(-1)),
									StorageTier: publicv1.StorageTierReference_builder{Id: newTier}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build()},
			} {
				By(tc.name)
				_, err = client.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
					Object: publicv1.ComputeInstanceCatalogItem_builder{Id: item.GetId(), Title: "must not persist", Fields: tc.fields}.Build(),
					UpdateMask: catalogItemUpdateMask("title",
						"fields"),
				}.Build())
				expectCatalogItemStatusCode(err, codes.InvalidArgument)
				after, e := client.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
				Expect(e).NotTo(HaveOccurred())
				Expect(proto.Equal(before.GetObject(), after.GetObject())).To(BeTrue())
			}
		})
	})
	Context("Lifecycle independence", func() {
		It("applies policy edits only to future compute instances and preserves provenance after catalog item deletion", func(ctx context.Context) {
			By("creating a VM from the original catalog policy")
			network := createCatalogItemNetworkFixture(ctx, usersGroup, "")
			template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
			items := publicv1.NewComputeInstanceCatalogItemsClient(tool.ExternalView().AdminConn())
			item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(), publicv1.ComputeInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
					RunStrategy: publicv1.ComputeInstanceRunStrategyFieldPolicy_builder{Locked: new(publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED)}.Build(),
					UserData: publicv1.StringFieldPolicy_builder{
						Editable: publicv1.EditableStringField_builder{DefaultValue: new("first")}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			spec := publicv1.ComputeInstanceSpec_builder{
				CatalogItem:        publicv1.ComputeInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
				NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
			}.Build()
			first, e := createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(), spec)
			Expect(e).NotTo(HaveOccurred())
			By("changing the policy and checking that only new VMs receive it")
			_, e = items.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{
					Id: item.GetId(),
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
						UserData: publicv1.StringFieldPolicy_builder{Locked: new("second")}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields.user_data"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			second, e := createComputeInstanceFixture(ctx, tool.ExternalView().UserConn(), spec)
			Expect(e).NotTo(HaveOccurred())
			client := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
			persistedSecond, e := client.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: second.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			second = persistedSecond.GetObject()
			Expect(second.GetSpec().GetUserData()).To(Equal("second"))
			Expect(second.GetSpec().GetRunStrategy()).To(Equal(publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED))
			stored, e := client.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: first.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(stored.GetObject().GetSpec().GetUserData()).To(Equal("first"))
			By("clearing one policy without changing other fields or the immutable Template")
			_, e = items.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{
					Id:     item.GetId(),
					Fields: publicv1.ComputeInstanceCatalogItemFields_builder{}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("fields.user_data"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			cleared, e := items.Get(ctx, publicv1.ComputeInstanceCatalogItemsGetRequest_builder{Id: item.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(cleared.GetObject().GetFields().GetUserData()).To(BeNil())
			Expect(cleared.GetObject().GetFields().GetRunStrategy().HasLocked()).To(BeTrue())
			other := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
			_, e = items.Update(ctx, publicv1.ComputeInstanceCatalogItemsUpdateRequest_builder{
				Object: publicv1.ComputeInstanceCatalogItem_builder{
					Id:       item.GetId(),
					Template: publicv1.ComputeInstanceTemplateReference_builder{Id: other}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("template"),
			}.Build())
			expectCatalogItemStatusCode(e, codes.InvalidArgument)
			By("updating the original VM without reapplying the catalog policy")
			_, e = client.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object:     publicv1.ComputeInstance_builder{Id: first.GetId(), Spec: publicv1.ComputeInstanceSpec_builder{RunStrategy: new(publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS)}.Build()}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.run_strategy"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			liveUpdate, e := client.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: first.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(liveUpdate.GetObject().GetSpec().GetRunStrategy()).To(Equal(publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS))

			_, e = client.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object:     publicv1.ComputeInstance_builder{Id: first.GetId(), Spec: publicv1.ComputeInstanceSpec_builder{RunStrategy: new(publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED)}.Build()}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.run_strategy"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())

			By("deleting the catalog item while retaining the VM's provenance")
			// Existing VMs keep materialized inputs, so later updates do not resolve the deleted item.
			_, e = items.Delete(ctx, publicv1.ComputeInstanceCatalogItemsDeleteRequest_builder{Id: item.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			By("normal updates through both APIs no longer resolve the removed catalog item")
			_, e = client.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Id: first.GetId(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						CatalogItem: publicv1.ComputeInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
						RunStrategy: new(publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.catalog_item",
					"spec.run_strategy"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			updated, e := client.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: first.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(updated.GetObject().GetSpec().GetRunStrategy()).To(Equal(publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS))

			private := privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn())
			_, e = private.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
				Object: privatev1.ComputeInstance_builder{
					Id: first.GetId(),
					Spec: privatev1.ComputeInstanceSpec_builder{
						CatalogItem: privatev1.ComputeInstanceCatalogItemReference_builder{Id: item.GetId()}.Build(),
						RunStrategy: new(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED),
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.catalog_item",
					"spec.run_strategy"),
			}.Build())
			Expect(e).NotTo(HaveOccurred())
			privateUpdated, e := private.Get(ctx, privatev1.ComputeInstancesGetRequest_builder{Id: first.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(privateUpdated.GetObject().GetSpec().GetRunStrategy()).To(Equal(privatev1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED))

			By("rejecting public provenance mutation through whole-field and nested masks")
			for _, mask := range []string{"spec.catalog_item", "spec.catalog_item.name", "spec.catalog_item.shared"} {
				By(mask)
				_, e = client.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
					Object: publicv1.ComputeInstance_builder{
						Id: first.GetId(),
						Spec: publicv1.ComputeInstanceSpec_builder{
							CatalogItem: publicv1.ComputeInstanceCatalogItemReference_builder{Id: item.GetId(), Name: "different", Shared: false}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: catalogItemUpdateMask(mask),
				}.Build())
				expectCatalogItemStatusCode(e, codes.InvalidArgument)
			}
			_, e = client.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Id:   first.GetId(),
					Spec: publicv1.ComputeInstanceSpec_builder{}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask("spec.catalog_item"),
			}.Build())
			expectCatalogItemStatusCode(e, codes.InvalidArgument)
			By("rejecting provenance mutation and clearing through the private API")
			for _, tc := range []struct {
				name      string
				reference *privatev1.ComputeInstanceCatalogItemReference
			}{
				{"different catalog item", privatev1.ComputeInstanceCatalogItemReference_builder{Id: "different"}.Build()},
				{"cleared catalog item", nil},
			} {
				By(tc.name)
				_, e = private.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
					Object: privatev1.ComputeInstance_builder{
						Id:   first.GetId(),
						Spec: privatev1.ComputeInstanceSpec_builder{CatalogItem: tc.reference}.Build(),
					}.Build(),
					UpdateMask: catalogItemUpdateMask("spec.catalog_item"),
				}.Build())
				expectCatalogItemStatusCode(e, codes.InvalidArgument)
			}
			persisted, e := client.Get(ctx, publicv1.ComputeInstancesGetRequest_builder{Id: first.GetId()}.Build())
			Expect(e).NotTo(HaveOccurred())
			Expect(proto.Equal(persisted.GetObject().GetSpec().GetCatalogItem(), first.GetSpec().GetCatalogItem())).To(BeTrue())
			Expect(persisted.GetObject().GetSpec().GetTemplate().GetId()).To(Equal(template))

		})
	})
})
