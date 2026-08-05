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
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

var _ = Describe("Node set removal", func() {
	var (
		ctx               context.Context
		clustersClient    publicv1.ClustersClient
		hostTypesClient   privatev1.HostTypesClient
		templatesClient   privatev1.ClusterTemplatesClient
		workerHostTypeId  string
		storageHostTypeId string
		templateId        string
	)

	BeforeEach(func() {
		ctx = context.Background()

		clustersClient = publicv1.NewClustersClient(tool.ExternalView().UserConn())
		hostTypesClient = privatev1.NewHostTypesClient(tool.InternalView().AdminConn())
		templatesClient = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())

		// Create worker host type:
		workerHostTypeId = fmt.Sprintf("worker_type_%s", uuid.New())
		_, err := hostTypesClient.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Id: workerHostTypeId,
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Create storage host type:
		storageHostTypeId = fmt.Sprintf("storage_type_%s", uuid.New())
		_, err = hostTypesClient.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Id: storageHostTypeId,
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Create a template with 2 node sets:
		templateId = fmt.Sprintf("template_2_nodesets_%s", uuid.New())
		_, err = templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          templateId,
				Title:       "Template with 2 node sets",
				Description: "A template with workers and storage node sets.",
				NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
					"workers": privatev1.ClusterTemplateNodeSet_builder{
						HostType: workerHostTypeId,
						Size:     3,
					}.Build(),
					"storage": privatev1.ClusterTemplateNodeSet_builder{
						HostType: storageHostTypeId,
						Size:     2,
					}.Build(),
				},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	It("Should keep node set removed after edit", func() {
		// Step 1: Create cluster with 2 node sets
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					Template: templateId,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		clusterId := createResponse.Object.Id

		// Step 2: Verify cluster has 2 node sets
		getResponse, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: clusterId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.Object.Spec.NodeSets).To(HaveLen(2))
		Expect(getResponse.Object.Spec.NodeSets).To(HaveKey("workers"))
		Expect(getResponse.Object.Spec.NodeSets).To(HaveKey("storage"))

		// Step 3: Remove the 'storage' node set
		updatedSpec := getResponse.Object.Spec
		delete(updatedSpec.NodeSets, "storage")

		_, err = clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: publicv1.Cluster_builder{
				Id:       clusterId,
				Metadata: getResponse.Object.Metadata,
				Spec:     updatedSpec,
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"spec.node_sets"},
			},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Step 4: Verify the 'storage' node set has been removed
		// This tests the fix for https://github.com/osac-project/issues/issues/251
		getResponse, err = clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: clusterId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.Object.Spec.NodeSets).To(HaveLen(1))
		Expect(getResponse.Object.Spec.NodeSets).To(HaveKey("workers"))
	})
})
