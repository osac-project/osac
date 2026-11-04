/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package mcpserver

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func createPrerequisiteClients(instances *mockComputeInstancesClient) computeInstanceClients {
	return computeInstanceClients{
		instances: instances,
		catalogItems: &mockComputeInstanceCatalogItemsClient{
			getFunc: func(
				_ context.Context, request *publicv1.ComputeInstanceCatalogItemsGetRequest, _ ...grpc.CallOption,
			) (*publicv1.ComputeInstanceCatalogItemsGetResponse, error) {
				return publicv1.ComputeInstanceCatalogItemsGetResponse_builder{
					Object: publicv1.ComputeInstanceCatalogItem_builder{
						Id:        request.GetId(),
						Published: true,
						Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
							InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{
								Editable: publicv1.EditableInstanceTypeReferenceField_builder{}.Build(),
							}.Build(),
							BootDisk: publicv1.ComputeInstanceBootDiskFieldPolicies_builder{
								SizeGib: publicv1.Int32FieldPolicy_builder{
									Editable: publicv1.EditableInt32Field_builder{}.Build(),
								}.Build(),
								StorageTier: publicv1.StorageTierReferenceFieldPolicy_builder{
									Editable: publicv1.EditableStorageTierReferenceField_builder{}.Build(),
								}.Build(),
							}.Build(),
							RunStrategy: publicv1.ComputeInstanceRunStrategyFieldPolicy_builder{
								Editable: publicv1.EditableComputeInstanceRunStrategyField_builder{}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build(), nil
			},
		},
	}
}

var _ = Describe("handleCreateComputeInstance", func() {
	It("Builds a compute instance from the catalog item and uses the exchanged API token", func() {
		var capturedToken string
		var capturedObject *publicv1.ComputeInstance
		instances := &mockComputeInstancesClient{
			createFunc: func(
				ctx context.Context, request *publicv1.ComputeInstancesCreateRequest, options ...grpc.CallOption,
			) (*publicv1.ComputeInstancesCreateResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedObject = request.GetObject()
				return publicv1.ComputeInstancesCreateResponse_builder{
					Object: publicv1.ComputeInstance_builder{
						Id: "instance-1",
						Metadata: publicv1.Metadata_builder{
							Version: 7,
						}.Build(),
						Status: publicv1.ComputeInstanceStatus_builder{
							State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
						}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handleCreateComputeInstance(createPrerequisiteClients(instances))
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
			Name:        "demo-vm",
			CatalogItem: "catalog-item-1",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output).To(Equal(CreateComputeInstanceOutput{
			ID:      "instance-1",
			Version: 7,
			State:   "COMPUTE_INSTANCE_STATE_STARTING",
		}))
		Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
		Expect(capturedObject.GetMetadata().GetName()).To(Equal("demo-vm"))
		Expect(capturedObject.GetMetadata().GetTenant()).To(BeEmpty())
		Expect(capturedObject.GetSpec().GetCatalogItem().GetId()).To(Equal("catalog-item-1"))
	})

	It("Requires a separate call for each changed create request", func() {
		var capturedNames []string
		instances := &mockComputeInstancesClient{
			createFunc: func(
				_ context.Context, request *publicv1.ComputeInstancesCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.ComputeInstancesCreateResponse, error) {
				capturedNames = append(capturedNames, request.GetObject().GetMetadata().GetName())
				return publicv1.ComputeInstancesCreateResponse_builder{}.Build(), nil
			},
		}

		handler := handleCreateComputeInstance(createPrerequisiteClients(instances))
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
			Name: "demo-vm-1", CatalogItem: "catalog-item-1",
		})
		Expect(err).ToNot(HaveOccurred())
		_, _, err = handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
			Name: "demo-vm-2", CatalogItem: "catalog-item-1",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(capturedNames).To(Equal([]string{"demo-vm-1", "demo-vm-2"}))
	})

	It("Applies typed, discoverable field overrides onto the compute instance spec", func() {
		var capturedObject *publicv1.ComputeInstance
		instances := &mockComputeInstancesClient{
			createFunc: func(
				ctx context.Context, request *publicv1.ComputeInstancesCreateRequest, options ...grpc.CallOption,
			) (*publicv1.ComputeInstancesCreateResponse, error) {
				capturedObject = request.GetObject()
				return publicv1.ComputeInstancesCreateResponse_builder{
					Object: publicv1.ComputeInstance_builder{Id: "instance-1"}.Build(),
				}.Build(), nil
			},
		}

		bootDiskSize := int32(40)
		clients := createPrerequisiteClients(instances)
		clients.instanceTypes = &mockInstanceTypesClient{getFunc: func(
			_ context.Context, _ *publicv1.InstanceTypesGetRequest, _ ...grpc.CallOption,
		) (*publicv1.InstanceTypesGetResponse, error) {
			return publicv1.InstanceTypesGetResponse_builder{
				Object: publicv1.InstanceType_builder{Id: "u1-medium"}.Build(),
			}.Build(), nil
		}}
		clients.storageTiers = &mockStorageTiersClient{getFunc: func(
			_ context.Context, _ *publicv1.StorageTiersGetRequest, _ ...grpc.CallOption,
		) (*publicv1.StorageTiersGetResponse, error) {
			return publicv1.StorageTiersGetResponse_builder{
				Object: publicv1.StorageTier_builder{Id: "local"}.Build(),
			}.Build(), nil
		}}
		clients.subnets = &mockSubnetsClient{getFunc: func(
			_ context.Context, _ *publicv1.SubnetsGetRequest, _ ...grpc.CallOption,
		) (*publicv1.SubnetsGetResponse, error) {
			return publicv1.SubnetsGetResponse_builder{
				Object: publicv1.Subnet_builder{Id: "subnet-1"}.Build(),
			}.Build(), nil
		}}
		clients.securityGroups = &mockSecurityGroupsClient{listFunc: func(
			_ context.Context, _ *publicv1.SecurityGroupsListRequest, _ ...grpc.CallOption,
		) (*publicv1.SecurityGroupsListResponse, error) {
			return publicv1.SecurityGroupsListResponse_builder{
				Items: []*publicv1.SecurityGroup{
					publicv1.SecurityGroup_builder{Id: "security-group-1"}.Build(),
					publicv1.SecurityGroup_builder{Id: "security-group-2"}.Build(),
				},
			}.Build(), nil
		}}
		handler := handleCreateComputeInstance(clients)
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
			Name:           "demo-vm",
			CatalogItem:    "catalog-item-1",
			InstanceTypeID: "u1-medium",
			BootDisk: &ComputeInstanceBootDiskInput{
				SizeGiB:       &bootDiskSize,
				StorageTierID: "local",
			},
			NetworkAttachments: []ComputeInstanceNetworkAttachmentInput{{
				SubnetID:         "subnet-1",
				SecurityGroupIDs: []string{"security-group-1", "security-group-2"},
			}},
		})
		Expect(err).ToNot(HaveOccurred())
		spec := capturedObject.GetSpec()
		Expect(spec.GetInstanceType().GetId()).To(Equal("u1-medium"))
		Expect(spec.GetBootDisk().GetSizeGib()).To(Equal(int32(40)))
		Expect(spec.GetBootDisk().GetStorageTier().GetId()).To(Equal("local"))
		Expect(spec.GetNetworkAttachments()).To(HaveLen(1))
		Expect(spec.GetNetworkAttachments()[0].GetSubnet().GetId()).To(Equal("subnet-1"))
		Expect(spec.GetNetworkAttachments()[0].GetSecurityGroups()).To(HaveLen(2))
		Expect(spec.GetNetworkAttachments()[0].GetSecurityGroups()[0].GetId()).To(Equal("security-group-1"))
		Expect(spec.GetNetworkAttachments()[0].GetSecurityGroups()[1].GetId()).To(Equal("security-group-2"))
	})

	DescribeTable("Rejects malformed typed field overrides before creating a compute instance",
		func(input CreateComputeInstanceInput, expectedError string) {
			instances := &mockComputeInstancesClient{
				createFunc: func(
					ctx context.Context, request *publicv1.ComputeInstancesCreateRequest, options ...grpc.CallOption,
				) (*publicv1.ComputeInstancesCreateResponse, error) {
					return nil, errors.New("Create should not be called")
				},
			}

			handler := handleCreateComputeInstance(createPrerequisiteClients(instances))
			_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), input)
			Expect(err).To(MatchError(ContainSubstring(expectedError)))
		},
		Entry("an empty boot disk", CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: "catalog-item-1", BootDisk: &ComputeInstanceBootDiskInput{},
		}, "boot_disk must include"),
		Entry("a missing name", CreateComputeInstanceInput{
			CatalogItem: "catalog-item-1",
		}, "name is required"),
		Entry("a blank catalog item", CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: " ",
		}, "catalog_item is required"),
		Entry("a non-positive boot disk size", CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: "catalog-item-1", BootDisk: &ComputeInstanceBootDiskInput{SizeGiB: int32Pointer(0)},
		}, "boot_disk.size_gib must be greater than zero"),
		Entry("more than one attachment", CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: "catalog-item-1", NetworkAttachments: []ComputeInstanceNetworkAttachmentInput{
				{SubnetID: "subnet-1"}, {SubnetID: "subnet-2"},
			},
		}, "network_attachments must contain at most one"),
		Entry("an empty security group ID", CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: "catalog-item-1", NetworkAttachments: []ComputeInstanceNetworkAttachmentInput{{
				SubnetID: "subnet-1", SecurityGroupIDs: []string{""},
			}},
		}, "network_attachments[0].security_group_ids[0] is required"),
	)

	DescribeTable("Leaves the network attachment to the tenant defaults",
		func(input []ComputeInstanceNetworkAttachmentInput, attachments int) {
			var capturedObject *publicv1.ComputeInstance
			instances := &mockComputeInstancesClient{
				createFunc: func(
					ctx context.Context, request *publicv1.ComputeInstancesCreateRequest, options ...grpc.CallOption,
				) (*publicv1.ComputeInstancesCreateResponse, error) {
					capturedObject = request.GetObject()
					return publicv1.ComputeInstancesCreateResponse_builder{Object: request.GetObject()}.Build(), nil
				},
			}
			clients := createPrerequisiteClients(instances)
			clients.securityGroups = &mockSecurityGroupsClient{
				listFunc: func(
					_ context.Context, request *publicv1.SecurityGroupsListRequest, _ ...grpc.CallOption,
				) (*publicv1.SecurityGroupsListResponse, error) {
					return publicv1.SecurityGroupsListResponse_builder{
						Items: []*publicv1.SecurityGroup{
							publicv1.SecurityGroup_builder{Id: "security-group-1"}.Build(),
						},
					}.Build(), nil
				},
			}
			handler := handleCreateComputeInstance(clients)
			_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
				Name: "demo-vm", CatalogItem: "catalog-item-1", NetworkAttachments: input,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(capturedObject.GetSpec().GetNetworkAttachments()).To(HaveLen(attachments))
			for _, attachment := range capturedObject.GetSpec().GetNetworkAttachments() {
				Expect(attachment.HasSubnet()).To(BeFalse())
			}
		},
		Entry("an empty attachment list", []ComputeInstanceNetworkAttachmentInput{}, 0),
		Entry("an attachment without a subnet", []ComputeInstanceNetworkAttachmentInput{{
			SecurityGroupIDs: []string{"security-group-1"},
		}}, 1),
	)

	It("Hides a ComputeInstance Create error description", func() {
		instances := &mockComputeInstancesClient{
			createFunc: func(
				ctx context.Context, request *publicv1.ComputeInstancesCreateRequest, options ...grpc.CallOption,
			) (*publicv1.ComputeInstancesCreateResponse, error) {
				return nil, status.Error(codes.InvalidArgument, "private SQL host and customer data")
			},
		}

		handler := handleCreateComputeInstance(createPrerequisiteClients(instances))
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
			Name:        "demo-vm",
			CatalogItem: "catalog-item-1",
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("invalid_request"))
		Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
		Expect(err.Error()).ToNot(ContainSubstring("private SQL host"))
	})

	It("Returns a safe authorization error when the public API denies creation", func() {
		instances := &mockComputeInstancesClient{
			createFunc: func(
				_ context.Context, _ *publicv1.ComputeInstancesCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.ComputeInstancesCreateResponse, error) {
				return nil, status.Error(codes.PermissionDenied, "tenant is not allowed")
			},
		}

		handler := handleCreateComputeInstance(createPrerequisiteClients(instances))
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: "catalog-item-1",
		})
		envelope := decodeToolError(err)
		Expect(output).To(BeZero())
		Expect(envelope.Category).To(Equal("authorization"))
		Expect(envelope.GRPCCode).To(Equal("PermissionDenied"))
		Expect(envelope.Retryable).To(BeFalse())
		Expect(err.Error()).ToNot(ContainSubstring("tenant is not allowed"))
	})

	It("Preserves a selected Project from another tenant in the public create object", func() {
		var capturedObject *publicv1.ComputeInstance
		instances := &mockComputeInstancesClient{
			createFunc: func(
				_ context.Context, request *publicv1.ComputeInstancesCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.ComputeInstancesCreateResponse, error) {
				capturedObject = request.GetObject()
				return publicv1.ComputeInstancesCreateResponse_builder{}.Build(), nil
			},
		}
		clients := createPrerequisiteClients(instances)
		clients.diskImages = &mockDiskImagesClient{getFunc: func(
			_ context.Context, _ *publicv1.DiskImagesGetRequest, _ ...grpc.CallOption,
		) (*publicv1.DiskImagesGetResponse, error) {
			return publicv1.DiskImagesGetResponse_builder{
				Object: publicv1.DiskImage_builder{Id: "image-1"}.Build(),
			}.Build(), nil
		}}
		clients.projects = &mockProjectsClient{listFunc: func(
			_ context.Context, _ *publicv1.ProjectsListRequest, _ ...grpc.CallOption,
		) (*publicv1.ProjectsListResponse, error) {
			return publicv1.ProjectsListResponse_builder{
				Items: []*publicv1.Project{{
					Id:       "project-1",
					Metadata: publicv1.Metadata_builder{Name: "team-b.project-1", Tenant: "tenant-b"}.Build(),
					Status: publicv1.ProjectStatus_builder{
						State: publicv1.ProjectState_PROJECT_STATE_ACTIVE,
					}.Build(),
				}},
			}.Build(), nil
		}}

		handler := handleCreateComputeInstance(clients)
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: "catalog-item-1", DiskImageID: "image-1", ProjectID: "project-1",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(capturedObject.GetMetadata().GetProject()).To(Equal("team-b.project-1"))
		Expect(capturedObject.GetMetadata().GetTenant()).To(Equal("tenant-b"))
		Expect(capturedObject.GetSpec().GetDiskImage().GetId()).To(Equal("image-1"))
	})

	It("Names a missing prerequisite and stops before the dependent create", func() {
		instances := &mockComputeInstancesClient{
			createFunc: func(
				_ context.Context, _ *publicv1.ComputeInstancesCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.ComputeInstancesCreateResponse, error) {
				return nil, errors.New("Create should not be called")
			},
		}
		clients := createPrerequisiteClients(instances)
		clients.projects = &mockProjectsClient{listFunc: func(
			_ context.Context, _ *publicv1.ProjectsListRequest, _ ...grpc.CallOption,
		) (*publicv1.ProjectsListResponse, error) {
			return publicv1.ProjectsListResponse_builder{}.Build(), nil
		}}

		handler := handleCreateComputeInstance(clients)
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: "catalog-item-1", ProjectID: "missing-project",
		})
		envelope := decodeToolError(err)
		Expect(output).To(BeZero())
		Expect(envelope.Category).To(Equal("not_found"))
		Expect(envelope.GRPCCode).To(Equal("NotFound"))
		Expect(envelope.Details).To(ConsistOf("Project missing-project is missing"))
		Expect(err.Error()).ToNot(ContainSubstring("private project details"))
	})

	It("Names an unauthorized prerequisite boundary and stops before the dependent create", func() {
		instances := &mockComputeInstancesClient{
			createFunc: func(
				_ context.Context, _ *publicv1.ComputeInstancesCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.ComputeInstancesCreateResponse, error) {
				return nil, errors.New("Create should not be called")
			},
		}
		clients := createPrerequisiteClients(instances)
		clients.projects = &mockProjectsClient{listFunc: func(
			_ context.Context, _ *publicv1.ProjectsListRequest, _ ...grpc.CallOption,
		) (*publicv1.ProjectsListResponse, error) {
			return nil, status.Error(codes.PermissionDenied, "private authorization details")
		}}

		handler := handleCreateComputeInstance(clients)
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: "catalog-item-1", ProjectID: "project-1",
		})
		envelope := decodeToolError(err)
		Expect(output).To(BeZero())
		Expect(envelope.Category).To(Equal("authorization"))
		Expect(envelope.GRPCCode).To(Equal("PermissionDenied"))
		Expect(envelope.Details).To(ConsistOf(
			"The caller cannot access or create Project project-1 with the caller's existing permissions",
		))
		Expect(err.Error()).ToNot(ContainSubstring("private authorization details"))
	})
})

func int32Pointer(value int32) *int32 {
	return &value
}

func currentComputeInstance(version int32) *publicv1.ComputeInstance {
	return publicv1.ComputeInstance_builder{
		Id:       "instance-1",
		Metadata: publicv1.Metadata_builder{Version: version}.Build(),
		Spec: publicv1.ComputeInstanceSpec_builder{
			CatalogItem: publicv1.ComputeInstanceCatalogItemReference_builder{Id: "catalog-item-1"}.Build(),
		}.Build(),
	}.Build()
}

func instancesWithCurrent(version int32) *mockComputeInstancesClient {
	return &mockComputeInstancesClient{
		getFunc: func(
			_ context.Context, _ *publicv1.ComputeInstancesGetRequest, _ ...grpc.CallOption,
		) (*publicv1.ComputeInstancesGetResponse, error) {
			return publicv1.ComputeInstancesGetResponse_builder{Object: currentComputeInstance(version)}.Build(), nil
		},
	}
}

var _ = Describe("handleUpdateComputeInstance", func() {
	currentInstance := func(version int32) *publicv1.ComputeInstance {
		return publicv1.ComputeInstance_builder{
			Id:       "instance-1",
			Metadata: publicv1.Metadata_builder{Version: version}.Build(),
			Spec: publicv1.ComputeInstanceSpec_builder{
				CatalogItem: publicv1.ComputeInstanceCatalogItemReference_builder{Id: "catalog-item-1"}.Build(),
			}.Build(),
		}.Build()
	}

	instancesWithCurrent := func(version int32) *mockComputeInstancesClient {
		return &mockComputeInstancesClient{
			getFunc: func(
				_ context.Context, _ *publicv1.ComputeInstancesGetRequest, _ ...grpc.CallOption,
			) (*publicv1.ComputeInstancesGetResponse, error) {
				return publicv1.ComputeInstancesGetResponse_builder{Object: currentInstance(version)}.Build(), nil
			},
		}
	}

	It("Applies supported fields with an exact mask, current version, and lock", func() {
		bootDiskSize := int32(50)
		instances := instancesWithCurrent(7)
		instances.updateFunc = func(
			_ context.Context, request *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
		) (*publicv1.ComputeInstancesUpdateResponse, error) {
			Expect(request.GetLock()).To(BeTrue())
			Expect(request.GetObject().GetMetadata().GetVersion()).To(Equal(int32(7)))
			Expect(request.GetUpdateMask().GetPaths()).To(ConsistOf(
				"spec.instance_type", "spec.boot_disk.size_gib", "spec.run_strategy",
			))
			Expect(request.GetObject().GetSpec().GetInstanceType().GetId()).To(Equal("u1-large"))
			Expect(request.GetObject().GetSpec().GetBootDisk().GetSizeGib()).To(Equal(int32(50)))
			Expect(request.GetObject().GetSpec().GetRunStrategy()).To(Equal(
				publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED,
			))
			return publicv1.ComputeInstancesUpdateResponse_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{Version: 8}.Build(),
					Status: publicv1.ComputeInstanceStatus_builder{
						State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
					}.Build(),
				}.Build(),
			}.Build(), nil
		}
		clients := createPrerequisiteClients(instances)
		clients.instanceTypes = &mockInstanceTypesClient{getFunc: func(
			_ context.Context, _ *publicv1.InstanceTypesGetRequest, _ ...grpc.CallOption,
		) (*publicv1.InstanceTypesGetResponse, error) {
			return publicv1.InstanceTypesGetResponse_builder{
				Object: publicv1.InstanceType_builder{Id: "u1-large"}.Build(),
			}.Build(), nil
		}}

		handler := handleUpdateComputeInstance(clients)
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), UpdateComputeInstanceInput{
			ID: "instance-1", Version: 7, InstanceTypeID: "u1-large",
			BootDisk:    &ComputeInstanceBootDiskInput{SizeGiB: &bootDiskSize},
			RunStrategy: "HALTED",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output).To(Equal(UpdateComputeInstanceOutput{
			ID: "instance-1", Version: 8, State: "COMPUTE_INSTANCE_STATE_RUNNING",
		}))
	})

	It("Preserves public update warnings in typed output", func() {
		instances := instancesWithCurrent(7)
		instances.updateFunc = func(
			_ context.Context, _ *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
		) (*publicv1.ComputeInstancesUpdateResponse, error) {
			return publicv1.ComputeInstancesUpdateResponse_builder{
				Object: publicv1.ComputeInstance_builder{
					Id:       "instance-1",
					Metadata: publicv1.Metadata_builder{Version: 8}.Build(),
					Status: publicv1.ComputeInstanceStatus_builder{
						State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
					}.Build(),
				}.Build(),
				Warnings: []string{"Instance type 'u1-large' is deprecated"},
			}.Build(), nil
		}

		handler := handleUpdateComputeInstance(createPrerequisiteClients(instances))
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), UpdateComputeInstanceInput{
			ID: "instance-1", Version: 7, RunStrategy: "HALTED",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output).To(Equal(UpdateComputeInstanceOutput{
			ID: "instance-1", Version: 8, State: "COMPUTE_INSTANCE_STATE_RUNNING", Warnings: []string{
				"Instance type 'u1-large' is deprecated",
			},
		}))
	})

	It("Uses the requested ID when the public update response omits it", func() {
		instances := instancesWithCurrent(7)
		instances.updateFunc = func(
			_ context.Context, _ *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
		) (*publicv1.ComputeInstancesUpdateResponse, error) {
			return publicv1.ComputeInstancesUpdateResponse_builder{
				Object: publicv1.ComputeInstance_builder{
					Metadata: publicv1.Metadata_builder{Version: 8}.Build(),
					Status: publicv1.ComputeInstanceStatus_builder{
						State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
					}.Build(),
				}.Build(),
			}.Build(), nil
		}

		handler := handleUpdateComputeInstance(createPrerequisiteClients(instances))
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), UpdateComputeInstanceInput{
			ID: "instance-1", Version: 7, RunStrategy: "HALTED",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output).To(Equal(UpdateComputeInstanceOutput{
			ID: "instance-1", Version: 8, State: "COMPUTE_INSTANCE_STATE_RUNNING",
		}))
	})

	It("Rejects a stale version before the public update", func() {
		instances := instancesWithCurrent(7)
		instances.updateFunc = func(
			_ context.Context, _ *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
		) (*publicv1.ComputeInstancesUpdateResponse, error) {
			return nil, errors.New("Update should not be called")
		}

		handler := handleUpdateComputeInstance(createPrerequisiteClients(instances))
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), UpdateComputeInstanceInput{
			ID: "instance-1", Version: 6, RunStrategy: "HALTED",
		})
		envelope := decodeToolError(err)
		Expect(output).To(BeZero())
		Expect(envelope.Category).To(Equal("conflict"))
		Expect(envelope.GRPCCode).To(Equal("Aborted"))
		Expect(envelope.Message).To(Equal("The resource version is stale. Retrieve the current version and retry."))
	})

	It("Maps a public optimistic-lock conflict to a stale version error", func() {
		instances := instancesWithCurrent(7)
		instances.updateFunc = func(
			_ context.Context, _ *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
		) (*publicv1.ComputeInstancesUpdateResponse, error) {
			return nil, status.Error(codes.Aborted, "version changed")
		}

		handler := handleUpdateComputeInstance(createPrerequisiteClients(instances))
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), UpdateComputeInstanceInput{
			ID: "instance-1", Version: 7, RunStrategy: "HALTED",
		})
		envelope := decodeToolError(err)
		Expect(output).To(BeZero())
		Expect(envelope.Category).To(Equal("conflict"))
		Expect(envelope.GRPCCode).To(Equal("Aborted"))
		Expect(envelope.Message).To(Equal("The resource version is stale. Retrieve the current version and retry."))
	})

	DescribeTable("Maps public update preconditions to a safe state conflict",
		func(privateDetail string) {
			instances := instancesWithCurrent(7)
			instances.updateFunc = func(
				_ context.Context, _ *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
			) (*publicv1.ComputeInstancesUpdateResponse, error) {
				return nil, status.Error(codes.FailedPrecondition, privateDetail)
			}

			handler := handleUpdateComputeInstance(createPrerequisiteClients(instances))
			_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), UpdateComputeInstanceInput{
				ID: "instance-1", Version: 7, RunStrategy: "HALTED",
			})
			envelope := decodeToolError(err)
			Expect(output).To(BeZero())
			Expect(envelope.Category).To(Equal("conflict"))
			Expect(envelope.GRPCCode).To(Equal("FailedPrecondition"))
			Expect(envelope.Message).To(Equal("The update conflicts with the current compute instance state."))
			Expect(envelope.Details).To(ConsistOf(
				"Choose an available instance type with the same GPU configuration.",
			))
			Expect(err.Error()).ToNot(ContainSubstring(privateDetail))
		},
		Entry("obsolete instance type", "instance type 'secret-obsolete' is obsolete and cannot be used"),
		Entry("incompatible GPU configuration", "cannot change GPU configuration from 'gpu-a' to 'gpu-b'"),
	)

	It("Treats absent and editable catalog policies as allowed and rejects locked update fields", func() {
		fieldCases := []struct {
			name         string
			input        UpdateComputeInstanceInput
			absent       func() *publicv1.ComputeInstanceCatalogItemFields
			editable     func() *publicv1.ComputeInstanceCatalogItemFields
			locked       func() *publicv1.ComputeInstanceCatalogItemFields
			lockedDetail string
		}{
			{
				name:  "instance_type",
				input: UpdateComputeInstanceInput{ID: "instance-1", Version: 7, InstanceTypeID: "u1-large"},
				absent: func() *publicv1.ComputeInstanceCatalogItemFields {
					return publicv1.ComputeInstanceCatalogItemFields_builder{}.Build()
				},
				editable: func() *publicv1.ComputeInstanceCatalogItemFields {
					return publicv1.ComputeInstanceCatalogItemFields_builder{
						InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{
							Editable: publicv1.EditableInstanceTypeReferenceField_builder{}.Build(),
						}.Build(),
					}.Build()
				},
				locked: func() *publicv1.ComputeInstanceCatalogItemFields {
					return publicv1.ComputeInstanceCatalogItemFields_builder{
						InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{
							Locked: publicv1.InstanceTypeReference_builder{Id: "u1-small"}.Build(),
						}.Build(),
					}.Build()
				},
				lockedDetail: "instance_type is locked by the selected catalog item",
			},
			{
				name:  "run_strategy",
				input: UpdateComputeInstanceInput{ID: "instance-1", Version: 7, RunStrategy: "HALTED"},
				absent: func() *publicv1.ComputeInstanceCatalogItemFields {
					return publicv1.ComputeInstanceCatalogItemFields_builder{}.Build()
				},
				editable: func() *publicv1.ComputeInstanceCatalogItemFields {
					return publicv1.ComputeInstanceCatalogItemFields_builder{
						RunStrategy: publicv1.ComputeInstanceRunStrategyFieldPolicy_builder{
							Editable: publicv1.EditableComputeInstanceRunStrategyField_builder{}.Build(),
						}.Build(),
					}.Build()
				},
				locked: func() *publicv1.ComputeInstanceCatalogItemFields {
					return publicv1.ComputeInstanceCatalogItemFields_builder{
						RunStrategy: publicv1.ComputeInstanceRunStrategyFieldPolicy_builder{
							Locked: publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS.Enum(),
						}.Build(),
					}.Build()
				},
				lockedDetail: "run_strategy is locked by the selected catalog item",
			},
		}

		for _, fieldCase := range fieldCases {
			variants := []struct {
				name   string
				fields func() *publicv1.ComputeInstanceCatalogItemFields
				locked bool
			}{
				{name: "absent policy", fields: fieldCase.absent},
				{name: "editable policy", fields: fieldCase.editable},
				{name: "locked policy", fields: fieldCase.locked, locked: true},
			}
			for _, variant := range variants {
				By(fieldCase.name + ": " + variant.name)
				updateCalls := 0
				instances := instancesWithCurrent(7)
				instances.updateFunc = func(
					_ context.Context, _ *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
				) (*publicv1.ComputeInstancesUpdateResponse, error) {
					updateCalls++
					return publicv1.ComputeInstancesUpdateResponse_builder{
						Object: publicv1.ComputeInstance_builder{
							Id:       "instance-1",
							Metadata: publicv1.Metadata_builder{Version: 8}.Build(),
						}.Build(),
					}.Build(), nil
				}
				clients := createPrerequisiteClients(instances)
				clients.catalogItems = &mockComputeInstanceCatalogItemsClient{
					getFunc: func(
						_ context.Context, _ *publicv1.ComputeInstanceCatalogItemsGetRequest, _ ...grpc.CallOption,
					) (*publicv1.ComputeInstanceCatalogItemsGetResponse, error) {
						return publicv1.ComputeInstanceCatalogItemsGetResponse_builder{
							Object: publicv1.ComputeInstanceCatalogItem_builder{
								Id:     "catalog-item-1",
								Fields: variant.fields(),
							}.Build(),
						}.Build(), nil
					},
				}
				clients.instanceTypes = &mockInstanceTypesClient{getFunc: func(
					_ context.Context, _ *publicv1.InstanceTypesGetRequest, _ ...grpc.CallOption,
				) (*publicv1.InstanceTypesGetResponse, error) {
					return publicv1.InstanceTypesGetResponse_builder{
						Object: publicv1.InstanceType_builder{Id: "u1-large"}.Build(),
					}.Build(), nil
				}}
				clients.storageTiers = &mockStorageTiersClient{getFunc: func(
					_ context.Context, _ *publicv1.StorageTiersGetRequest, _ ...grpc.CallOption,
				) (*publicv1.StorageTiersGetResponse, error) {
					return publicv1.StorageTiersGetResponse_builder{
						Object: publicv1.StorageTier_builder{Id: "fast"}.Build(),
					}.Build(), nil
				}}

				_, output, err := handleUpdateComputeInstance(clients)(
					context.Background(), requestWithToken("raw-bearer-value"), fieldCase.input,
				)
				if variant.locked {
					envelope := decodeToolError(err)
					Expect(output).To(BeZero())
					Expect(updateCalls).To(BeZero())
					Expect(envelope.Category).To(Equal("invalid_request"))
					Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
					Expect(envelope.Details).To(ConsistOf(fieldCase.lockedDetail))
				} else {
					Expect(err).ToNot(HaveOccurred())
					Expect(updateCalls).To(Equal(1))
					Expect(output.Version).To(Equal(int32(8)))
				}
			}
		}
	})

	It("Requires a supported field and a current version", func() {
		instances := &mockComputeInstancesClient{}
		handler := handleUpdateComputeInstance(createPrerequisiteClients(instances))
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), UpdateComputeInstanceInput{
			ID: "instance-1", Version: 7,
		})
		Expect(decodeToolError(err).Details).To(ConsistOf("at least one supported update field is required"))

		_, _, err = handler(context.Background(), requestWithToken("raw-bearer-value"), UpdateComputeInstanceInput{
			ID: "instance-1", Version: -1, RunStrategy: "HALTED",
		})
		Expect(decodeToolError(err).Details).To(ConsistOf("version must be greater than zero"))

		_, _, err = handler(context.Background(), requestWithToken("raw-bearer-value"), UpdateComputeInstanceInput{
			ID: "instance-1", Version: 0, RunStrategy: "HALTED",
		})
		Expect(decodeToolError(err).Details).To(ConsistOf("version must be greater than zero"))
	})
})

var _ = Describe("handleSetComputeInstancePower", func() {
	DescribeTable("Applies a requested power action as the caller",
		func(action string, expectedRunStrategy publicv1.ComputeInstanceRunStrategy, expectedOutput string) {
			var capturedToken string
			var capturedRequest *publicv1.ComputeInstancesUpdateRequest
			instances := instancesWithCurrent(7)
			instances.updateFunc = func(
				ctx context.Context, request *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
			) (*publicv1.ComputeInstancesUpdateResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedRequest = request
				return publicv1.ComputeInstancesUpdateResponse_builder{
					Object: publicv1.ComputeInstance_builder{
						Id:       "instance-1",
						Metadata: publicv1.Metadata_builder{Version: 8}.Build(),
						Spec:     publicv1.ComputeInstanceSpec_builder{RunStrategy: expectedRunStrategy.Enum()}.Build(),
						Status: publicv1.ComputeInstanceStatus_builder{
							State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING,
						}.Build(),
					}.Build(),
				}.Build(), nil
			}

			handler := handleSetComputeInstancePower(instances)
			_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), SetComputeInstancePowerInput{
				ID: "instance-1", Version: 7, Action: action,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(output).To(Equal(SetComputeInstancePowerOutput{
				ID: "instance-1", Version: 8, State: "COMPUTE_INSTANCE_STATE_STOPPING",
				RunStrategy: expectedOutput,
			}))
			Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
			Expect(capturedRequest.GetLock()).To(BeTrue())
			Expect(capturedRequest.GetObject().GetId()).To(Equal("instance-1"))
			Expect(capturedRequest.GetObject().GetMetadata().GetVersion()).To(Equal(int32(7)))
			Expect(capturedRequest.GetUpdateMask().GetPaths()).To(ConsistOf("spec.run_strategy"))
			Expect(capturedRequest.GetObject().GetSpec().GetRunStrategy()).To(Equal(expectedRunStrategy))
		},
		Entry("start", "start", publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_ALWAYS, "ALWAYS"),
		Entry("stop", "stop", publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED, "HALTED"),
	)

	It("Returns the public update response without no-op special-casing", func() {
		instances := instancesWithCurrent(7)
		instances.updateFunc = func(
			_ context.Context, _ *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
		) (*publicv1.ComputeInstancesUpdateResponse, error) {
			return publicv1.ComputeInstancesUpdateResponse_builder{
				Object: publicv1.ComputeInstance_builder{
					Id:       "instance-1",
					Metadata: publicv1.Metadata_builder{Version: 7}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						RunStrategy: publicv1.ComputeInstanceRunStrategy_COMPUTE_INSTANCE_RUN_STRATEGY_HALTED.Enum(),
					}.Build(),
					Status: publicv1.ComputeInstanceStatus_builder{
						State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED,
					}.Build(),
				}.Build(),
			}.Build(), nil
		}

		handler := handleSetComputeInstancePower(instances)
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), SetComputeInstancePowerInput{
			ID: "instance-1", Version: 7, Action: "stop",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output).To(Equal(SetComputeInstancePowerOutput{
			ID: "instance-1", Version: 7, State: "COMPUTE_INSTANCE_STATE_STOPPED",
			RunStrategy: "HALTED",
		}))
	})

	It("Rejects a stale version before the public update", func() {
		instances := instancesWithCurrent(7)
		instances.updateFunc = func(
			_ context.Context, _ *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
		) (*publicv1.ComputeInstancesUpdateResponse, error) {
			return nil, errors.New("Update should not be called")
		}

		handler := handleSetComputeInstancePower(instances)
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), SetComputeInstancePowerInput{
			ID: "instance-1", Version: 6, Action: "start",
		})
		envelope := decodeToolError(err)
		Expect(output).To(BeZero())
		Expect(envelope.Category).To(Equal("conflict"))
		Expect(envelope.GRPCCode).To(Equal("Aborted"))
		Expect(envelope.Message).To(Equal("The resource version is stale. Retrieve the current version and retry."))
	})

	It("Maps a public optimistic-lock conflict to a stale version error", func() {
		instances := instancesWithCurrent(7)
		instances.updateFunc = func(
			_ context.Context, _ *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
		) (*publicv1.ComputeInstancesUpdateResponse, error) {
			return nil, status.Error(codes.Aborted, "version changed")
		}

		handler := handleSetComputeInstancePower(instances)
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), SetComputeInstancePowerInput{
			ID: "instance-1", Version: 7, Action: "start",
		})
		envelope := decodeToolError(err)
		Expect(output).To(BeZero())
		Expect(envelope.Category).To(Equal("conflict"))
		Expect(envelope.GRPCCode).To(Equal("Aborted"))
	})

	It("Rejects invalid input before the public API", func() {
		instances := &mockComputeInstancesClient{}
		handler := handleSetComputeInstancePower(instances)
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), SetComputeInstancePowerInput{
			ID: "instance-1", Version: 7, Action: "restart",
		})
		Expect(decodeToolError(err).Details).To(ConsistOf("action must be start or stop"))

		_, _, err = handler(context.Background(), requestWithToken("raw-bearer-value"), SetComputeInstancePowerInput{
			ID: "instance-1", Version: 0, Action: "start",
		})
		Expect(decodeToolError(err).Details).To(ConsistOf("version must be greater than zero"))
	})

	It("Returns a safe authorization error when the public update is denied", func() {
		instances := instancesWithCurrent(7)
		instances.updateFunc = func(
			_ context.Context, _ *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
		) (*publicv1.ComputeInstancesUpdateResponse, error) {
			return nil, status.Error(codes.PermissionDenied, "tenant is not allowed")
		}

		handler := handleSetComputeInstancePower(instances)
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), SetComputeInstancePowerInput{
			ID: "instance-1", Version: 7, Action: "start",
		})
		envelope := decodeToolError(err)
		Expect(output).To(BeZero())
		Expect(envelope.Category).To(Equal("authorization"))
		Expect(envelope.GRPCCode).To(Equal("PermissionDenied"))
		Expect(err.Error()).ToNot(ContainSubstring("tenant is not allowed"))
	})
})

var _ = Describe("handleRestartComputeInstance", func() {
	It("Generates the restart timestamp at the edge and uses the caller token", func() {
		var capturedToken string
		var capturedRequest *publicv1.ComputeInstancesUpdateRequest
		before := time.Now().UTC()
		instances := instancesWithCurrent(7)
		instances.updateFunc = func(
			ctx context.Context, request *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
		) (*publicv1.ComputeInstancesUpdateResponse, error) {
			capturedToken = forwardedToken(ctx)
			capturedRequest = request
			return publicv1.ComputeInstancesUpdateResponse_builder{
				Object: publicv1.ComputeInstance_builder{
					Id:       "instance-1",
					Metadata: publicv1.Metadata_builder{Version: 8}.Build(),
					Spec: publicv1.ComputeInstanceSpec_builder{
						RestartRequestedAt: request.GetObject().GetSpec().GetRestartRequestedAt(),
					}.Build(),
					Status: publicv1.ComputeInstanceStatus_builder{
						State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
					}.Build(),
				}.Build(),
			}.Build(), nil
		}

		handler := handleRestartComputeInstance(instances)
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), RestartComputeInstanceInput{
			ID: "instance-1", Version: 7,
		})
		after := time.Now().UTC()
		Expect(err).ToNot(HaveOccurred())
		Expect(output.ID).To(Equal("instance-1"))
		Expect(output.Version).To(Equal(int32(8)))
		Expect(output.State).To(Equal("COMPUTE_INSTANCE_STATE_RUNNING"))
		Expect(output.RestartRequestedAt).ToNot(BeEmpty())
		acceptedAt, parseErr := time.Parse(time.RFC3339Nano, output.RestartRequestedAt)
		Expect(parseErr).ToNot(HaveOccurred())
		Expect(acceptedAt).To(BeTemporally(">=", before))
		Expect(acceptedAt).To(BeTemporally("<=", after))
		Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
		Expect(capturedRequest.GetLock()).To(BeTrue())
		Expect(capturedRequest.GetObject().GetId()).To(Equal("instance-1"))
		Expect(capturedRequest.GetObject().GetMetadata().GetVersion()).To(Equal(int32(7)))
		Expect(capturedRequest.GetUpdateMask().GetPaths()).To(ConsistOf("spec.restart_requested_at"))
		Expect(capturedRequest.GetObject().GetSpec().GetRestartRequestedAt().AsTime()).To(BeTemporally("~", acceptedAt))
	})

	It("Rejects a stale version before the public update", func() {
		instances := instancesWithCurrent(7)
		instances.updateFunc = func(
			_ context.Context, _ *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
		) (*publicv1.ComputeInstancesUpdateResponse, error) {
			return nil, errors.New("Update should not be called")
		}

		handler := handleRestartComputeInstance(instances)
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), RestartComputeInstanceInput{
			ID: "instance-1", Version: 6,
		})
		envelope := decodeToolError(err)
		Expect(output).To(BeZero())
		Expect(envelope.Category).To(Equal("conflict"))
		Expect(envelope.GRPCCode).To(Equal("Aborted"))
		Expect(envelope.Message).To(Equal("The resource version is stale. Retrieve the current version and retry."))
	})

	It("Maps a public optimistic-lock conflict to a stale version error", func() {
		instances := instancesWithCurrent(7)
		instances.updateFunc = func(
			_ context.Context, _ *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
		) (*publicv1.ComputeInstancesUpdateResponse, error) {
			return nil, status.Error(codes.Aborted, "version changed")
		}

		handler := handleRestartComputeInstance(instances)
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), RestartComputeInstanceInput{
			ID: "instance-1", Version: 7,
		})
		envelope := decodeToolError(err)
		Expect(output).To(BeZero())
		Expect(envelope.Category).To(Equal("conflict"))
		Expect(envelope.GRPCCode).To(Equal("Aborted"))
	})

	It("Rejects invalid input before the public API", func() {
		instances := &mockComputeInstancesClient{}
		handler := handleRestartComputeInstance(instances)
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), RestartComputeInstanceInput{
			ID: " ", Version: 7,
		})
		Expect(decodeToolError(err).Details).To(ConsistOf("id is required"))

		_, _, err = handler(context.Background(), requestWithToken("raw-bearer-value"), RestartComputeInstanceInput{
			ID: "instance-1", Version: 0,
		})
		Expect(decodeToolError(err).Details).To(ConsistOf("version must be greater than zero"))
	})
})

var _ = Describe("handleDeleteComputeInstance", func() {
	It("Deletes the requested compute instance and uses the exchanged API token", func() {
		var capturedToken string
		instances := &mockComputeInstancesClient{
			deleteFunc: func(
				ctx context.Context, request *publicv1.ComputeInstancesDeleteRequest, options ...grpc.CallOption,
			) (*publicv1.ComputeInstancesDeleteResponse, error) {
				capturedToken = forwardedToken(ctx)
				Expect(request.GetId()).To(Equal("instance-1"))
				return publicv1.ComputeInstancesDeleteResponse_builder{}.Build(), nil
			},
		}

		handler := handleDeleteComputeInstance(instances)
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), DeleteComputeInstanceInput{ID: "instance-1"})
		Expect(err).ToNot(HaveOccurred())
		Expect(output).To(Equal(DeleteComputeInstanceOutput{ID: "instance-1"}))
		Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
	})

	It("Rejects a blank delete target before calling the public API", func() {
		instances := &mockComputeInstancesClient{
			deleteFunc: func(
				_ context.Context, _ *publicv1.ComputeInstancesDeleteRequest, _ ...grpc.CallOption,
			) (*publicv1.ComputeInstancesDeleteResponse, error) {
				return nil, errors.New("Delete should not be called")
			},
		}

		handler := handleDeleteComputeInstance(instances)
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), DeleteComputeInstanceInput{ID: " "})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("invalid_request"))
		Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
	})

	It("Hides a ComputeInstance Delete error description", func() {
		instances := &mockComputeInstancesClient{
			deleteFunc: func(
				ctx context.Context, request *publicv1.ComputeInstancesDeleteRequest, options ...grpc.CallOption,
			) (*publicv1.ComputeInstancesDeleteResponse, error) {
				return nil, status.Error(codes.Unavailable, "private SQL host and customer data")
			},
		}

		handler := handleDeleteComputeInstance(instances)
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), DeleteComputeInstanceInput{ID: "instance-1"})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("service_unavailable"))
		Expect(envelope.GRPCCode).To(Equal("Unavailable"))
		Expect(envelope.Retryable).To(BeTrue())
		Expect(err.Error()).ToNot(ContainSubstring("private SQL host"))
	})
})
