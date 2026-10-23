/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

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
	"google.golang.org/grpc/status"
)

var _ = Describe("Compute instance network attachment status", Label("compute-networking"), func() {
	It("persists per-attachment status and exposes it through the public API", func(ctx context.Context) {
		ensureTenantDefaultNetworkingFixture(ctx, usersGroup, "")
		network := getTenantDefaultNetworkFixture(ctx, usersGroup, "")
		template := createCatalogItemComputeInstanceProvisioningTemplateFixture(ctx, nil)
		publicClient := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
		created, err := publicClient.Create(ctx, publicv1.ComputeInstancesCreateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Metadata: publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Spec: publicv1.ComputeInstanceSpec_builder{
					Template:           publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
					NetworkAttachments: []*publicv1.ComputeNetworkAttachment{network.computeInstanceAttachment()},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		instance := created.GetObject()
		deferComputeInstanceDeletion(instance.GetId(), tool.ExternalView().UserConn())

		attachmentStatus := privatev1.ComputeNetworkAttachmentStatus_builder{
			SubnetRef: network.subnetID,
			IpAddress: "192.0.2.42",
		}.Build()
		privateClient := privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn())
		_, err = privateClient.Update(ctx, privatev1.ComputeInstancesUpdateRequest_builder{
			Object: privatev1.ComputeInstance_builder{
				Id: instance.GetId(),
				Status: privatev1.ComputeInstanceStatus_builder{
					ComputeNetworkAttachmentStatuses: []*privatev1.ComputeNetworkAttachmentStatus{attachmentStatus},
				}.Build(),
			}.Build(),
			UpdateMask: catalogItemUpdateMask("status.compute_network_attachment_statuses"),
		}.Build())
		Expect(err).NotTo(HaveOccurred())

		spoofedStatus := publicv1.ComputeNetworkAttachmentStatus_builder{
			SubnetRef: "spoofed-subnet",
			IpAddress: "192.0.2.99",
		}.Build()
		response, err := publicClient.Get(ctx,
			publicv1.ComputeInstancesGetRequest_builder{Id: instance.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		publicObject := response.GetObject()
		statuses := publicObject.GetStatus().GetComputeNetworkAttachmentStatuses()
		Expect(statuses).To(HaveLen(1))
		Expect(statuses[0].GetSubnetRef()).To(Equal(network.subnetID))
		Expect(statuses[0].GetIpAddress()).To(Equal("192.0.2.42"))
		publicObject.SetStatus(nil)
		publicObject.GetMetadata().SetLabels(map[string]string{"it": "updated"})
		updateResponse, err := publicClient.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
			Object: publicObject,
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		// The user edit persisted and the Update response carries the feedback:
		Expect(updateResponse.GetObject().GetMetadata().GetLabels()).To(Equal(
			map[string]string{"it": "updated"}))
		statuses = updateResponse.GetObject().GetStatus().GetComputeNetworkAttachmentStatuses()
		Expect(statuses).To(HaveLen(1))
		Expect(statuses[0].GetSubnetRef()).To(Equal(network.subnetID))
		Expect(statuses[0].GetIpAddress()).To(Equal("192.0.2.42"))

		response, err = publicClient.Get(ctx,
			publicv1.ComputeInstancesGetRequest_builder{Id: instance.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		publicObject = response.GetObject()
		statuses = publicObject.GetStatus().GetComputeNetworkAttachmentStatuses()
		Expect(statuses).To(HaveLen(1))
		Expect(statuses[0].GetSubnetRef()).To(Equal(network.subnetID))
		Expect(statuses[0].GetIpAddress()).To(Equal("192.0.2.42"))
		publicObject.GetStatus().SetComputeNetworkAttachmentStatuses(
			[]*publicv1.ComputeNetworkAttachmentStatus{spoofedStatus},
		)
		_, err = publicClient.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
			Object: publicObject,
		}.Build())
		Expect(err).NotTo(HaveOccurred())

		_, err = publicClient.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
			Object: publicv1.ComputeInstance_builder{
				Id: instance.GetId(),
				Status: publicv1.ComputeInstanceStatus_builder{
					ComputeNetworkAttachmentStatuses: []*publicv1.ComputeNetworkAttachmentStatus{spoofedStatus},
				}.Build(),
			}.Build(),
			UpdateMask: catalogItemUpdateMask("status.compute_network_attachment_statuses"),
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		for _, path := range []string{
			"status.state",
			"status. compute_network_attachment_statuses",
			"status..compute_network_attachment_statuses",
			" status.compute_network_attachment_statuses ",
		} {
			_, err = publicClient.Update(ctx, publicv1.ComputeInstancesUpdateRequest_builder{
				Object: publicv1.ComputeInstance_builder{
					Id: instance.GetId(),
					Status: publicv1.ComputeInstanceStatus_builder{
						ComputeNetworkAttachmentStatuses: []*publicv1.ComputeNetworkAttachmentStatus{spoofedStatus},
					}.Build(),
				}.Build(),
				UpdateMask: catalogItemUpdateMask(path),
			}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument), "mask path %q", path)
		}

		response, err = publicClient.Get(ctx,
			publicv1.ComputeInstancesGetRequest_builder{Id: instance.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		statuses = response.GetObject().GetStatus().GetComputeNetworkAttachmentStatuses()
		Expect(statuses).To(HaveLen(1))
		Expect(statuses[0].GetSubnetRef()).To(Equal(network.subnetID))
		Expect(statuses[0].GetIpAddress()).To(Equal("192.0.2.42"))
	})
})
