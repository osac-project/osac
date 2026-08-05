/*
Copyright (c) 2025 Red Hat Inc.

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

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("Access control", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Public API", func() {
		It("Allows regular users to list cluster templates", func() {
			client := publicv1.NewClusterTemplatesClient(tool.ExternalView().UserConn())
			_, err := client.List(ctx, publicv1.ClusterTemplatesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows regular users to list clusters", func() {
			client := publicv1.NewClustersClient(tool.ExternalView().UserConn())
			_, err := client.List(ctx, publicv1.ClustersListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows regular users to list host types", func() {
			client := publicv1.NewHostTypesClient(tool.ExternalView().UserConn())
			_, err := client.List(ctx, publicv1.HostTypesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows regular users to list compute instance templates", func() {
			client := publicv1.NewComputeInstanceTemplatesClient(tool.ExternalView().UserConn())
			_, err := client.List(ctx, publicv1.ComputeInstanceTemplatesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows regular users to list compute instances", func() {
			client := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
			_, err := client.List(ctx, publicv1.ComputeInstancesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows regular users to list cluster versions", func() {
			client := publicv1.NewClusterVersionsClient(tool.ExternalView().UserConn())
			_, err := client.List(ctx, publicv1.ClusterVersionsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Denies regular users creating cluster versions", func() {
			client := publicv1.NewClusterVersionsClient(tool.ExternalView().UserConn())
			_, err := client.Create(ctx, publicv1.ClusterVersionsCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
		})

		It("Denies regular users updating cluster versions", func() {
			client := publicv1.NewClusterVersionsClient(tool.ExternalView().UserConn())
			_, err := client.Update(ctx, publicv1.ClusterVersionsUpdateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
		})

		It("Denies regular users deleting cluster versions", func() {
			client := publicv1.NewClusterVersionsClient(tool.ExternalView().UserConn())
			_, err := client.Delete(ctx, publicv1.ClusterVersionsDeleteRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
		})

		It("Allows admin users to list cluster templates", func() {
			client := publicv1.NewClusterTemplatesClient(tool.ExternalView().AdminConn())

			_, err := client.List(ctx, publicv1.ClusterTemplatesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows admin users to list clusters", func() {
			client := publicv1.NewClustersClient(tool.ExternalView().AdminConn())
			_, err := client.List(ctx, publicv1.ClustersListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows admin users to list host types", func() {
			client := publicv1.NewHostTypesClient(tool.ExternalView().AdminConn())
			_, err := client.List(ctx, publicv1.HostTypesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows admin users to list compute instance templates", func() {
			client := publicv1.NewComputeInstanceTemplatesClient(tool.ExternalView().AdminConn())
			_, err := client.List(ctx, publicv1.ComputeInstanceTemplatesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows admin users to list compute instances", func() {
			client := publicv1.NewComputeInstancesClient(tool.ExternalView().AdminConn())
			_, err := client.List(ctx, publicv1.ComputeInstancesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows admin users to list cluster versions", func() {
			client := publicv1.NewClusterVersionsClient(tool.ExternalView().AdminConn())
			_, err := client.List(ctx, publicv1.ClusterVersionsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("Private API", func() {
		It("Allows admin users to list host types", func() {
			client := privatev1.NewHostTypesClient(tool.InternalView().AdminConn())
			_, err := client.List(ctx, privatev1.HostTypesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows admin users to list cluster templates", func() {
			client := privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
			_, err := client.List(ctx, privatev1.ClusterTemplatesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows admin users to list hubs", func() {
			client := privatev1.NewHubsClient(tool.InternalView().AdminConn())
			_, err := client.List(ctx, privatev1.HubsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows admin users to list private clusters", func() {
			client := privatev1.NewClustersClient(tool.InternalView().AdminConn())
			_, err := client.List(ctx, privatev1.ClustersListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows admin users to list compute instance templates", func() {
			client := privatev1.NewComputeInstanceTemplatesClient(tool.InternalView().AdminConn())
			_, err := client.List(ctx, privatev1.ComputeInstanceTemplatesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows admin users to list private compute instances", func() {
			client := privatev1.NewComputeInstancesClient(tool.InternalView().AdminConn())
			_, err := client.List(ctx, privatev1.ComputeInstancesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Allows admin users to list private cluster versions", func() {
			client := privatev1.NewClusterVersionsClient(tool.InternalView().AdminConn())
			_, err := client.List(ctx, privatev1.ClusterVersionsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Denies regular users access to host types", func() {
			client := privatev1.NewHostTypesClient(tool.InternalView().UserConn())
			_, err := client.List(ctx, privatev1.HostTypesListRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
		})

		It("Denies regular users access to cluster templates", func() {
			client := privatev1.NewClusterTemplatesClient(tool.InternalView().UserConn())
			_, err := client.List(ctx, privatev1.ClusterTemplatesListRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
		})

		It("Denies regular users access to hubs", func() {
			client := privatev1.NewHubsClient(tool.InternalView().UserConn())
			_, err := client.List(ctx, privatev1.HubsListRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
		})

		It("Denies regular users access to private clusters", func() {
			client := privatev1.NewClustersClient(tool.InternalView().UserConn())
			_, err := client.List(ctx, privatev1.ClustersListRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
		})

		It("Denies regular users access to compute instance templates", func() {
			client := privatev1.NewComputeInstanceTemplatesClient(tool.InternalView().UserConn())
			_, err := client.List(ctx, privatev1.ComputeInstanceTemplatesListRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
		})

		It("Denies regular users access to private compute instances", func() {
			client := privatev1.NewComputeInstancesClient(tool.InternalView().UserConn())
			_, err := client.List(ctx, privatev1.ComputeInstancesListRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
		})

		It("Denies regular users access to private cluster versions", func() {
			client := privatev1.NewClusterVersionsClient(tool.InternalView().UserConn())
			_, err := client.List(ctx, privatev1.ClusterVersionsListRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.PermissionDenied))
		})
	})
})
