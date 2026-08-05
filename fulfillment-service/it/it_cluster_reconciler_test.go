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
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	kubeerrors "k8s.io/apimachinery/pkg/api/errors"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac-operator/api/v1alpha1"
)

var _ = Describe("Cluster reconciler", func() {
	var (
		ctx             context.Context
		clustersClient  publicv1.ClustersClient
		hostTypesClient privatev1.HostTypesClient
		hostTypeId      string
		templatesClient privatev1.ClusterTemplatesClient
		templateId      string
	)

	makeAny := func(value proto.Message) *anypb.Any {
		result, err := anypb.New(value)
		Expect(err).ToNot(HaveOccurred())
		return result
	}

	BeforeEach(func() {
		// Create a context:
		ctx = context.Background()

		// Create the clients:
		clustersClient = publicv1.NewClustersClient(tool.ExternalView().UserConn())
		hostTypesClient = privatev1.NewHostTypesClient(tool.InternalView().AdminConn())
		templatesClient = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())

		// Create a host type for testing:
		hostTypeId = fmt.Sprintf("my_host_type_%s", uuid.New())
		_, err := hostTypesClient.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Id: hostTypeId,
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Create a template for testing:
		templateId = fmt.Sprintf("my_template_%s", uuid.New())
		_, err = templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          templateId,
				Title:       "My template %s",
				Description: "My template.",
				Parameters: []*privatev1.ClusterTemplateParameterDefinition{
					privatev1.ClusterTemplateParameterDefinition_builder{
						Name:        "my",
						Type:        "type.googleapis.com/google.protobuf.StringValue",
						Title:       "My required parameter",
						Description: "My required parameter.",
						Required:    true,
					}.Build(),
					privatev1.ClusterTemplateParameterDefinition_builder{
						Name:        "your",
						Type:        "type.googleapis.com/google.protobuf.StringValue",
						Title:       "Your optional parameter",
						Description: "Your optional parameter.",
						Required:    false,
						Default:     makeAny(wrapperspb.String("your_default")),
					}.Build(),
				},
				NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
					"my_node_set": privatev1.ClusterTemplateNodeSet_builder{
						HostType: hostTypeId,
						Size:     3,
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

	It("Creates the Kubernetes object when a cluster is created", func() {
		// Create the cluster
		response, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					Template: templateId,
					TemplateParameters: map[string]*anypb.Any{
						"my": makeAny(wrapperspb.String("my_value")),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := response.GetObject()
		DeferCleanup(func() {
			_, err := clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// Check that the Kubernetes object is eventually created:
		kubeClient := tool.KubeClient()
		clusterOrderList := &osacv1alpha1.ClusterOrderList{}
		var kubeObject *osacv1alpha1.ClusterOrder
		Eventually(
			func(g Gomega) {
				err := kubeClient.List(ctx, clusterOrderList, crclient.MatchingLabels{
					labels.ClusterOrderUuid: object.GetId(),
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(clusterOrderList.Items).To(HaveLen(1))
				kubeObject = &clusterOrderList.Items[0]
			},
			time.Minute,
			time.Second,
		).Should(Succeed())

		// Check that namespace is correct:
		Expect(kubeObject.GetNamespace()).To(Equal(hubNamespace))

		// Verify that the node sets from the template are reflected in the Kubernetes object:
		Expect(kubeObject.Spec.NodeRequests).To(HaveLen(1))
		Expect(kubeObject.Spec.NodeRequests[0].ResourceClass).To(Equal(hostTypeId))
		Expect(kubeObject.Spec.NodeRequests[0].NumberOfNodes).To(BeNumerically("==", 3))

		// Verify that the template parameters are reflected in the Kubernetes object:
		Expect(kubeObject.Spec.TemplateParameters).To(MatchJSON(`{
			"my": "my_value",
			"your": "your_default"
		}`))
	})

	It("Deletes the Kubernetes object when a cluster is deleted", func() {
		// Create the cluster
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					Template: templateId,
					TemplateParameters: map[string]*anypb.Any{
						"my": makeAny(wrapperspb.String("my_value")),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()

		// Wait for the corresponding Kubernetes object to be created:
		kubeClient := tool.KubeClient()
		clusterOrderList := &osacv1alpha1.ClusterOrderList{}
		var clusterOrderObj *osacv1alpha1.ClusterOrder
		Eventually(
			func(g Gomega) {
				err := kubeClient.List(ctx, clusterOrderList, crclient.MatchingLabels{
					labels.ClusterOrderUuid: object.GetId(),
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(clusterOrderList.Items).To(HaveLen(1))
				clusterOrderObj = &clusterOrderList.Items[0]
			},
			time.Minute,
			time.Second,
		).Should(Succeed())

		// Delete the cluster:
		_, err = clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
			Id: object.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Verify that the corresponding Kubernetes object is eventually deleted:
		clusterOrderKey := crclient.ObjectKey{
			Namespace: clusterOrderObj.GetNamespace(),
			Name:      clusterOrderObj.GetName(),
		}
		Eventually(
			func(g Gomega) {
				err := kubeClient.Get(ctx, clusterOrderKey, clusterOrderObj)
				if err != nil {
					g.Expect(kubeerrors.IsNotFound(err)).To(BeTrue())
				} else {
					g.Expect(clusterOrderObj.GetDeletionTimestamp()).ToNot(BeNil())
				}
			},
			time.Minute,
			time.Second,
		).Should(Succeed())
	})

	It("Updates the Kubernetes object when a cluster node set size is changed", func() {
		// Create the cluster with initial node set size:
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					Template: templateId,
					TemplateParameters: map[string]*anypb.Any{
						"my": makeAny(wrapperspb.String("my_value")),
					},
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"my_node_set": publicv1.ClusterNodeSet_builder{
							HostType: hostTypeId,
							Size:     3,
						}.Build(),
					},
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

		// Wait for the corresponding Kubernetes object to be created:
		kubeClient := tool.KubeClient()
		clusterOrderList := &osacv1alpha1.ClusterOrderList{}
		var clusterOrderObj *osacv1alpha1.ClusterOrder
		Eventually(
			func(g Gomega) {
				err := kubeClient.List(ctx, clusterOrderList, crclient.MatchingLabels{
					labels.ClusterOrderUuid: object.GetId(),
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(clusterOrderList.Items).To(HaveLen(1))
				clusterOrderObj = &clusterOrderList.Items[0]
			},
			time.Minute,
			time.Second,
		).Should(Succeed())

		// Verify the initial node set size in the Kubernetes object:
		Expect(clusterOrderObj.Spec.NodeRequests).To(HaveLen(1))
		Expect(clusterOrderObj.Spec.NodeRequests[0].ResourceClass).To(Equal(hostTypeId))
		Expect(clusterOrderObj.Spec.NodeRequests[0].NumberOfNodes).To(BeNumerically("==", 3))

		// Update the cluster to change the node set size
		_, err = clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: publicv1.Cluster_builder{
				Id: object.GetId(),
				Spec: publicv1.ClusterSpec_builder{
					Template: templateId,
					TemplateParameters: map[string]*anypb.Any{
						"my": makeAny(wrapperspb.String("my_value")),
					},
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"my_node_set": publicv1.ClusterNodeSet_builder{
							HostType: hostTypeId,
							Size:     5,
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Verify that the ClusterOrder is updated to reflect the new size
		clusterOrderKey := crclient.ObjectKey{
			Namespace: clusterOrderObj.GetNamespace(),
			Name:      clusterOrderObj.GetName(),
		}
		Eventually(
			func(g Gomega) {
				err := kubeClient.Get(ctx, clusterOrderKey, clusterOrderObj)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(clusterOrderObj.Spec.NodeRequests).To(HaveLen(1))
				g.Expect(clusterOrderObj.Spec.NodeRequests[0].ResourceClass).To(Equal(hostTypeId))
				g.Expect(clusterOrderObj.Spec.NodeRequests[0].NumberOfNodes).To(BeNumerically("==", 5))
			},
			time.Minute,
			time.Second,
		).Should(Succeed())
	})
})
