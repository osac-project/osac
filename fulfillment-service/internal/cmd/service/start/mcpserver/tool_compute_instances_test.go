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
	"fmt"

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
		handler := handleCreateComputeInstance(createPrerequisiteClients(instances))
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
		Entry("an empty attachment list", CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: "catalog-item-1", NetworkAttachments: []ComputeInstanceNetworkAttachmentInput{},
		}, "network_attachments must contain at least one"),
		Entry("more than one attachment", CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: "catalog-item-1", NetworkAttachments: []ComputeInstanceNetworkAttachmentInput{
				{SubnetID: "subnet-1"}, {SubnetID: "subnet-2"},
			},
		}, "network_attachments must contain at most one"),
		Entry("an attachment without a subnet", CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: "catalog-item-1", NetworkAttachments: []ComputeInstanceNetworkAttachmentInput{{}},
		}, "network_attachments[0].subnet_id is required"),
		Entry("an empty security group ID", CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: "catalog-item-1", NetworkAttachments: []ComputeInstanceNetworkAttachmentInput{{
				SubnetID: "subnet-1", SecurityGroupIDs: []string{""},
			}},
		}, "network_attachments[0].security_group_ids[0] is required"),
	)

	It("Rejects more than the supported number of raw security groups before preflight", func() {
		instances := &mockComputeInstancesClient{
			createFunc: func(
				_ context.Context, _ *publicv1.ComputeInstancesCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.ComputeInstancesCreateResponse, error) {
				return nil, errors.New("Create should not be called")
			},
		}
		securityGroupIDs := make([]string, maxPreflightSecurityGroups+1)
		for index := range securityGroupIDs {
			securityGroupIDs[index] = fmt.Sprintf("security-group-%d", index)
		}

		handler := handleCreateComputeInstance(createPrerequisiteClients(instances))
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
			Name:        "demo-vm",
			CatalogItem: "catalog-item-1",
			NetworkAttachments: []ComputeInstanceNetworkAttachmentInput{{
				SubnetID: "subnet-1", SecurityGroupIDs: securityGroupIDs,
			}},
		})
		envelope := decodeToolError(err)
		Expect(output).To(BeZero())
		Expect(envelope.Category).To(Equal("invalid_request"))
		Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
		Expect(envelope.Details).To(ConsistOf(
			fmt.Sprintf("network_attachments must reference at most %d security groups", maxPreflightSecurityGroups),
		))
	})

	It("Deduplicates repeated security group references before creating proto objects", func() {
		var capturedObject *publicv1.ComputeInstance
		instances := &mockComputeInstancesClient{
			createFunc: func(
				_ context.Context, request *publicv1.ComputeInstancesCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.ComputeInstancesCreateResponse, error) {
				capturedObject = request.GetObject()
				return publicv1.ComputeInstancesCreateResponse_builder{}.Build(), nil
			},
		}

		handler := handleCreateComputeInstance(createPrerequisiteClients(instances))
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
			Name:        "demo-vm",
			CatalogItem: "catalog-item-1",
			NetworkAttachments: []ComputeInstanceNetworkAttachmentInput{{
				SubnetID: "subnet-1",
				SecurityGroupIDs: []string{
					"security-group-1", "security-group-1", "security-group-2", "security-group-2",
				},
			}},
		})
		Expect(err).ToNot(HaveOccurred())
		securityGroups := capturedObject.GetSpec().GetNetworkAttachments()[0].GetSecurityGroups()
		Expect(securityGroups).To(HaveLen(2))
		Expect(securityGroups[0].GetId()).To(Equal("security-group-1"))
		Expect(securityGroups[1].GetId()).To(Equal("security-group-2"))
	})

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

	It("Normalizes a missing public reference to a safe not-found error", func() {
		instances := &mockComputeInstancesClient{
			createFunc: func(
				_ context.Context, _ *publicv1.ComputeInstancesCreateRequest, _ ...grpc.CallOption,
			) (*publicv1.ComputeInstancesCreateResponse, error) {
				return nil, status.Error(codes.InvalidArgument, "reference validation failed: not found")
			},
		}

		handler := handleCreateComputeInstance(createPrerequisiteClients(instances))
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: "catalog-item-1",
		})
		envelope := decodeToolError(err)
		Expect(output).To(BeZero())
		Expect(envelope.Category).To(Equal("not_found"))
		Expect(envelope.GRPCCode).To(Equal("NotFound"))
		Expect(envelope.Details).To(BeEmpty())
		Expect(err.Error()).ToNot(ContainSubstring("reference validation failed"))
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

	It("Selects an eligible image and Project and preserves its tenant and name", func() {
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
		clients.projects = &mockProjectsClient{listFunc: func(
			_ context.Context, _ *publicv1.ProjectsListRequest, _ ...grpc.CallOption,
		) (*publicv1.ProjectsListResponse, error) {
			return publicv1.ProjectsListResponse_builder{
				Items: []*publicv1.Project{publicv1.Project_builder{
					Id: "project-1",
					Metadata: publicv1.Metadata_builder{
						Name: "team-a.project-1", Tenant: "tenant-2",
					}.Build(),
					Status: publicv1.ProjectStatus_builder{
						State: publicv1.ProjectState_PROJECT_STATE_ACTIVE,
					}.Build(),
				}.Build()},
			}.Build(), nil
		}}

		handler := handleCreateComputeInstance(clients)
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: "catalog-item-1", DiskImageID: "image-1", ProjectID: "project-1",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(capturedObject.GetMetadata().GetTenant()).To(Equal("tenant-2"))
		Expect(capturedObject.GetMetadata().GetProject()).To(Equal("team-a.project-1"))
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
					Id:       "instance-1",
					Metadata: publicv1.Metadata_builder{Version: 8}.Build(),
					Status: publicv1.ComputeInstanceStatus_builder{
						State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
					}.Build(),
				}.Build(),
			}.Build(), nil
		}
		clients := createPrerequisiteClients(instances)

		bootDiskSize := int32(50)
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

	It("Rejects a catalog-locked field before the public update", func() {
		instances := instancesWithCurrent(7)
		instances.updateFunc = func(
			_ context.Context, _ *publicv1.ComputeInstancesUpdateRequest, _ ...grpc.CallOption,
		) (*publicv1.ComputeInstancesUpdateResponse, error) {
			return nil, errors.New("Update should not be called")
		}
		clients := createPrerequisiteClients(instances)
		clients.catalogItems = &mockComputeInstanceCatalogItemsClient{
			getFunc: func(
				_ context.Context, _ *publicv1.ComputeInstanceCatalogItemsGetRequest, _ ...grpc.CallOption,
			) (*publicv1.ComputeInstanceCatalogItemsGetResponse, error) {
				return publicv1.ComputeInstanceCatalogItemsGetResponse_builder{
					Object: publicv1.ComputeInstanceCatalogItem_builder{
						Id: "catalog-item-1",
						Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
							InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{
								Locked: publicv1.InstanceTypeReference_builder{Id: "u1-small"}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handleUpdateComputeInstance(clients)
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), UpdateComputeInstanceInput{
			ID: "instance-1", Version: 7, InstanceTypeID: "u1-large",
		})
		envelope := decodeToolError(err)
		Expect(output).To(BeZero())
		Expect(envelope.Category).To(Equal("invalid_request"))
		Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
		Expect(envelope.Details).To(ConsistOf("instance_type is locked by the selected catalog item"))
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
