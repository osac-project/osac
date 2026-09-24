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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Labels", func() {
	var (
		ctx                 context.Context
		clustersClient      publicv1.ClustersClient
		instanceTypesClient privatev1.BareMetalInstanceTypesClient
		templatesClient     privatev1.ClusterTemplatesClient
		bmitName            string
		templateId          string
	)

	BeforeEach(func() {
		ctx = context.Background()
		clustersClient = publicv1.NewClustersClient(tool.ExternalView().UserConn())
		instanceTypesClient = privatev1.NewBareMetalInstanceTypesClient(tool.InternalView().AdminConn())
		templatesClient = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())

		bmitName = fmt.Sprintf("test-bmit-%s", uuid.New()[24:32])
		_, err := instanceTypesClient.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: bmitName,
				}.Build(),
				Spec: privatev1.BareMetalInstanceTypeSpec_builder{
					Hardware: privatev1.BareMetalHardwareSpec_builder{
						Cpu:    privatev1.BareMetalCPUSpec_builder{Cores: 32, Architecture: "x86_64", ThreadsPerCore: 2}.Build(),
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
			_, err := instanceTypesClient.Delete(ctx, privatev1.BareMetalInstanceTypesDeleteRequest_builder{
				Id: bmitName,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		templateId = fmt.Sprintf("my-template-%s", uuid.New())
		_, err = templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id: templateId,
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("my-template-%s", uuid.New()[24:32]),
				}.Build(),
				Title:       "My template %s",
				Description: "My template.",
				NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
					"my-node-set": privatev1.ClusterTemplateNodeSet_builder{
						BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: bmitName}.Build(),
						Size:                  3,
					}.Build(),
				},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, err := templatesClient.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
				Id: templateId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	It("Can create a cluster with labels", func() {
		labels := map[string]string{
			"example.com/app": "my-app",
			"simple":          "value",
		}
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name:   fmt.Sprintf("labels-test-%s", uuid.New()[24:32]),
					Labels: labels,
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		DeferCleanup(func() {
			_, err := clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		metadata := object.GetMetadata()
		Expect(metadata).ToNot(BeNil())
		Expect(metadata.GetLabels()).To(HaveKeyWithValue("example.com/app", "my-app"))
		Expect(metadata.GetLabels()).To(HaveKeyWithValue("simple", "value"))

		getResponse, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: object.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		metadata = getResponse.GetObject().GetMetadata()
		Expect(metadata.GetLabels()).To(HaveKeyWithValue("example.com/app", "my-app"))
		Expect(metadata.GetLabels()).To(HaveKeyWithValue("simple", "value"))
	})

	It("Can update a cluster with labels", func() {
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("labels-test-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		DeferCleanup(func() {
			_, err := clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		labels := map[string]string{
			"example.com/updated": "new-value",
			"another":             "second",
		}
		updateResponse, err := clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: publicv1.Cluster_builder{
				Id: object.GetId(),
				Metadata: publicv1.Metadata_builder{
					Name:   object.GetMetadata().GetName(),
					Labels: labels,
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		metadata := updateResponse.GetObject().GetMetadata()
		Expect(metadata.GetLabels()).To(HaveKeyWithValue("example.com/updated", "new-value"))
		Expect(metadata.GetLabels()).To(HaveKeyWithValue("another", "second"))

		getResponse, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: object.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		metadata = getResponse.GetObject().GetMetadata()
		Expect(metadata.GetLabels()).To(HaveKeyWithValue("example.com/updated", "new-value"))
		Expect(metadata.GetLabels()).To(HaveKeyWithValue("another", "second"))
	})

	DescribeTable(
		"Rejects invalid labels on create and update",
		func(key string, value string, expected string) {
			_, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("labels-test-%s", uuid.New()[24:32]),
						Labels: map[string]string{
							key: value,
						},
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(expected))

			createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("labels-test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			DeferCleanup(func() {
				_, err := clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			_, err = clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: object.GetId(),
					Metadata: publicv1.Metadata_builder{
						Name: object.GetMetadata().GetName(),
						Labels: map[string]string{
							key: value,
						},
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok = grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(expected))
		},
		Entry(
			"Invalid label name character",
			"bad^name",
			"value",
			"field 'metadata.labels' key 'bad^name' name must only contain lowercase letters (a-z), "+
				"digits (0-9), hyphens (-), underscores (_) or dots (.), but contains '^' at position 3",
		),
		Entry(
			"Invalid label prefix character",
			"bad_prefix/name",
			"value",
			"field 'metadata.labels' key 'bad_prefix/name' prefix segment must only contain lowercase "+
				"letters (a-z), digits (0-9) and hyphens (-), but contains '_' at position 3",
		),
	)
})
