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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

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
						Status: publicv1.ComputeInstanceStatus_builder{
							State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING,
						}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handleCreateComputeInstance(instances)
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
			Name:        "demo-vm",
			CatalogItem: "catalog-item-1",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output).To(Equal(CreateComputeInstanceOutput{
			ID:    "instance-1",
			State: "COMPUTE_INSTANCE_STATE_STARTING",
		}))
		Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
		Expect(capturedObject.GetMetadata().GetName()).To(Equal("demo-vm"))
		Expect(capturedObject.GetMetadata().GetTenant()).To(BeEmpty())
		Expect(capturedObject.GetSpec().GetCatalogItem().GetId()).To(Equal("catalog-item-1"))
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
		handler := handleCreateComputeInstance(instances)
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

			handler := handleCreateComputeInstance(instances)
			_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), input)
			Expect(err).To(MatchError(ContainSubstring(expectedError)))
		},
		Entry("an empty boot disk", CreateComputeInstanceInput{
			Name: "demo-vm", CatalogItem: "catalog-item-1", BootDisk: &ComputeInstanceBootDiskInput{},
		}, "boot_disk must include"),
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

	It("Hides a ComputeInstance Create error description", func() {
		instances := &mockComputeInstancesClient{
			createFunc: func(
				ctx context.Context, request *publicv1.ComputeInstancesCreateRequest, options ...grpc.CallOption,
			) (*publicv1.ComputeInstancesCreateResponse, error) {
				return nil, status.Error(codes.InvalidArgument, "private SQL host and customer data")
			},
		}

		handler := handleCreateComputeInstance(instances)
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), CreateComputeInstanceInput{
			Name:        "demo-vm",
			CatalogItem: "catalog-item-1",
		})
		Expect(err).To(MatchError("public API rejected the request"))
	})
})

func int32Pointer(value int32) *int32 {
	return &value
}

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
		Expect(err).To(MatchError("public API is unavailable"))
	})
})
