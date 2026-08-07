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
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/gvks"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("Public clusters", func() {
	var (
		ctx             context.Context
		clustersClient  publicv1.ClustersClient
		hostTypesClient privatev1.HostTypesClient
		templatesClient privatev1.ClusterTemplatesClient
		hostTypeId      string
		templateId      string
	)

	BeforeEach(func() {
		// Create a context:
		ctx = context.Background()

		// Create the clients:
		clustersClient = publicv1.NewClustersClient(tool.ExternalView().UserConn())
		hostTypesClient = privatev1.NewHostTypesClient(tool.InternalView().AdminConn())
		templatesClient = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())

		// Create a host type for testing:
		hostTypeId = fmt.Sprintf("my-host-type-%s", uuid.New())
		_, err := hostTypesClient.Create(ctx, privatev1.HostTypesCreateRequest_builder{
			Object: privatev1.HostType_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-ht-%s", uuid.New()[24:32]),
				}.Build(),
				Id: hostTypeId,
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, err := hostTypesClient.Delete(ctx, privatev1.HostTypesDeleteRequest_builder{
				Id: hostTypeId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// Create a template for testing:
		templateId = fmt.Sprintf("my-template-%s", uuid.New())
		_, err = templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-tmpl-%s", uuid.New()[24:32]),
				}.Build(),
				Id:          templateId,
				Title:       "My template %s",
				Description: "My template.",
				NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
					"my-node-set": privatev1.ClusterTemplateNodeSet_builder{
						HostType: privatev1.HostTypeReference_builder{Id: hostTypeId}.Build(),
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

	It("Can get a specific cluster", func() {
		// Create the cluster
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()

		// Delete the cluster after the test:
		DeferCleanup(func() {
			_, err := clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// Get the cluster and verify that the returned data is correct
		response, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: object.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object = response.GetObject()
		metadata := object.GetMetadata()
		Expect(metadata).ToNot(BeNil())
		Expect(metadata.HasCreationTimestamp()).To(BeTrue())
		Expect(metadata.HasDeletionTimestamp()).To(BeFalse())
		spec := object.GetSpec()
		Expect(spec).ToNot(BeNil())
		Expect(spec.GetTemplate().GetId()).To(Equal(templateId))
	})

	It("Can get the list of clusters", func() {
		// Create a cluster
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
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

		// Get the list of clusters and verify that it isn't empty:
		listResponse, err := clustersClient.List(ctx, publicv1.ClustersListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse).ToNot(BeNil())
		items := listResponse.GetItems()
		Expect(items).ToNot(BeNil())
	})

	It("Can create a cluster", func() {
		// Create the cluster:
		response, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		object := response.GetObject()
		DeferCleanup(func() {
			_, err := clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// Verify that the cluster has been created correctly:
		Expect(object).ToNot(BeNil())
		metadata := object.GetMetadata()
		Expect(metadata).ToNot(BeNil())
		Expect(metadata.HasCreationTimestamp()).To(BeTrue())
		Expect(metadata.HasDeletionTimestamp()).To(BeFalse())
		spec := object.GetSpec()
		Expect(spec).ToNot(BeNil())
		Expect(spec.GetTemplate().GetId()).To(Equal(templateId))
	})

	It("Can update a cluster", func() {
		// Create the cluster
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
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

		// Update the cluster:
		updateResponse, err := clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: publicv1.Cluster_builder{
				Id: object.GetId(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"my-node-set": {
							HostType: publicv1.HostTypeReference_builder{Id: hostTypeId}.Build(),
							Size:     4,
						},
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object = updateResponse.GetObject()
		nodeSet := object.GetSpec().GetNodeSets()["my-node-set"]
		Expect(nodeSet).ToNot(BeNil())
		Expect(nodeSet.GetSize()).To(BeNumerically("==", 4))

		// Get the cluster and verify that the returned data is correct
		getResponse, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: object.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object = getResponse.GetObject()
		nodeSet = object.GetSpec().GetNodeSets()["my-node-set"]
		Expect(nodeSet).ToNot(BeNil())
		Expect(nodeSet.GetSize()).To(BeNumerically("==", 4))
	})

	It("Can delete a cluster", func() {
		// Create the cluster
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResponse.GetObject()
		_, err = clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
			Id: object.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Trying to get the deleted object should either fail if the object has been completely deleted and
		// archived, or return an object that has the deletion timestamp set.
		getResponse, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: object.GetId(),
		}.Build())
		if err != nil {
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
		} else {
			object = getResponse.GetObject()
			metadata := object.GetMetadata()
			Expect(metadata.HasDeletionTimestamp()).To(BeTrue())
		}
	})

	It("Returns not found error when getting cluster that doesn't exist", func() {
		_, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: "does-not-exist",
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.NotFound))
	})

	It("Returns not found error when updating cluster that doesn't exist", func() {
		_, err := clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: publicv1.Cluster_builder{
				Id: "does-not-exist",
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("my-name-%s", uuid.New()[24:32]),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.NotFound))
	})

	It("Returns not found error when deleting cluster that doesn't exist", func() {
		_, err := clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
			Id: "does-not-exist",
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.NotFound))
	})

	It("Can get the kubeconfig of a cluster", func() {
		// Create the cluster
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
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

		// Wait till the Kubernetes object has been created:
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

		// Create the Hypershift namespace:
		namespaceObj := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterOrderObj.GetName(),
			},
		}
		err = kubeClient.Create(ctx, namespaceObj)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			err := kubeClient.Delete(ctx, namespaceObj)
			Expect(err).ToNot(HaveOccurred())
		})

		// Create the Hypershift kubeconfig secret:
		secretObj := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespaceObj.GetName(),
				Name:      "kubeconfig",
			},
			Data: map[string][]byte{
				"kubeconfig": []byte("my-kubeconfig"),
			},
		}
		err = kubeClient.Create(ctx, secretObj)
		Expect(err).ToNot(HaveOccurred())

		// Create the Hypershift hosted cluster, and set the reference to the kubeconfig secret:
		hostedClusterObj := &unstructured.Unstructured{}
		hostedClusterObj.SetGroupVersionKind(gvks.HostedCluster)
		hostedClusterObj.SetNamespace(namespaceObj.GetName())
		hostedClusterObj.SetName(clusterOrderObj.GetName())
		err = kubeClient.Create(ctx, hostedClusterObj)
		Expect(err).ToNot(HaveOccurred())
		hostedClusterUpdate := hostedClusterObj.DeepCopy()
		hostedClusterUpdate.Object["status"] = map[string]any{
			"kubeconfig": map[string]any{
				"name": secretObj.GetName(),
			},
		}
		Expect(err).ToNot(HaveOccurred())
		hostedClusterPatch := crclient.MergeFrom(hostedClusterObj)
		err = kubeClient.Status().Patch(ctx, hostedClusterUpdate, hostedClusterPatch)
		Expect(err).ToNot(HaveOccurred())

		// Save the reference to the hosted cluster in the cluster order:
		clusterOrderUpdate := clusterOrderObj.DeepCopy()
		clusterOrderUpdate.Status.ClusterReference = &osacv1alpha1.ClusterOrderClusterReferenceType{
			Namespace:         hostedClusterObj.GetNamespace(),
			HostedClusterName: hostedClusterObj.GetName(),
		}
		clusterOrderPatch := crclient.MergeFrom(clusterOrderObj)
		err = kubeClient.Status().Patch(ctx, clusterOrderUpdate, clusterOrderPatch)
		Expect(err).ToNot(HaveOccurred())

		// Get the kubeconfig:
		var kubeconfig string
		Eventually(
			func(g Gomega) {
				getKubeconfigResponse, err := clustersClient.GetKubeconfig(
					ctx,
					publicv1.ClustersGetKubeconfigRequest_builder{
						Id: object.GetId(),
					}.Build(),
				)
				g.Expect(err).ToNot(HaveOccurred())
				kubeconfig = getKubeconfigResponse.GetKubeconfig()
				g.Expect(kubeconfig).To(Equal("my-kubeconfig"))
			},
			time.Minute,
			time.Second,
		).Should(Succeed())
	})

	It("Can get the kubeconfig of a cluster via HTTP", func() {
		// Create the cluster
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
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

		// Wait till the Kubernetes object has been created:
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

		// Create the Hypershift namespace:
		namespaceObj := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterOrderObj.GetName(),
			},
		}
		err = kubeClient.Create(ctx, namespaceObj)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			err := kubeClient.Delete(ctx, namespaceObj)
			Expect(err).ToNot(HaveOccurred())
		})

		// Create the Hypershift kubeconfig secret:
		secretObj := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespaceObj.GetName(),
				Name:      "kubeconfig",
			},
			Data: map[string][]byte{
				"kubeconfig": []byte("my-kubeconfig"),
			},
		}
		err = kubeClient.Create(ctx, secretObj)
		Expect(err).ToNot(HaveOccurred())

		// Create the Hypershift hosted cluster, and set the reference to the kubeconfig secret:
		hostedClusterObj := &unstructured.Unstructured{}
		hostedClusterObj.SetGroupVersionKind(gvks.HostedCluster)
		hostedClusterObj.SetNamespace(namespaceObj.GetName())
		hostedClusterObj.SetName(clusterOrderObj.GetName())
		err = kubeClient.Create(ctx, hostedClusterObj)
		Expect(err).ToNot(HaveOccurred())
		hostedClusterUpdate := hostedClusterObj.DeepCopy()
		hostedClusterUpdate.Object["status"] = map[string]any{
			"kubeconfig": map[string]any{
				"name": secretObj.GetName(),
			},
		}
		Expect(err).ToNot(HaveOccurred())
		hostedClusterPatch := crclient.MergeFrom(hostedClusterObj)
		err = kubeClient.Status().Patch(ctx, hostedClusterUpdate, hostedClusterPatch)
		Expect(err).ToNot(HaveOccurred())

		// Save the reference to the hosted cluster in the cluster order:
		clusterOrderUpdate := clusterOrderObj.DeepCopy()
		clusterOrderUpdate.Status.ClusterReference = &osacv1alpha1.ClusterOrderClusterReferenceType{
			Namespace:         hostedClusterObj.GetNamespace(),
			HostedClusterName: hostedClusterObj.GetName(),
		}
		clusterOrderPatch := crclient.MergeFrom(clusterOrderObj)
		err = kubeClient.Status().Patch(ctx, clusterOrderUpdate, clusterOrderPatch)
		Expect(err).ToNot(HaveOccurred())

		// Get the kubeconfig:
		Eventually(
			func(g Gomega) {
				getKubeconfigResponse, err := clustersClient.GetKubeconfigViaHttp(
					ctx,
					publicv1.ClustersGetKubeconfigViaHttpRequest_builder{
						Id: object.GetId(),
					}.Build(),
				)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(getKubeconfigResponse.GetContentType()).To(Equal("application/yaml"))
				g.Expect(getKubeconfigResponse.GetData()).To(Equal([]byte("my-kubeconfig")))
			},
			time.Minute,
			time.Second,
		).Should(Succeed())
	})

	It("Can get the admin password of a cluster", func() {
		// Create the cluster
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
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

		// Wait till the Kubernetes object has been created:
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

		// Create the Hypershift namespace:
		namespaceObj := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterOrderObj.GetName(),
			},
		}
		err = kubeClient.Create(ctx, namespaceObj)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			err := kubeClient.Delete(ctx, namespaceObj)
			Expect(err).ToNot(HaveOccurred())
		})

		// Create the Hypershift admin password secret:
		secretObj := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespaceObj.GetName(),
				Name:      "password",
			},
			Data: map[string][]byte{
				"password": []byte("my_password"),
			},
		}
		err = kubeClient.Create(ctx, secretObj)
		Expect(err).ToNot(HaveOccurred())

		// Create the Hypershift hosted cluster, and set the reference to the admin password secret:
		hostedClusterObj := &unstructured.Unstructured{}
		hostedClusterObj.SetGroupVersionKind(gvks.HostedCluster)
		hostedClusterObj.SetNamespace(namespaceObj.GetName())
		hostedClusterObj.SetName(clusterOrderObj.GetName())
		err = kubeClient.Create(ctx, hostedClusterObj)
		Expect(err).ToNot(HaveOccurred())
		hostedClusterUpdate := hostedClusterObj.DeepCopy()
		hostedClusterUpdate.Object["status"] = map[string]any{
			"kubeadminPassword": map[string]any{
				"name": secretObj.GetName(),
			},
		}
		Expect(err).ToNot(HaveOccurred())
		hostedClusterPatch := crclient.MergeFrom(hostedClusterObj)
		err = kubeClient.Status().Patch(ctx, hostedClusterUpdate, hostedClusterPatch)
		Expect(err).ToNot(HaveOccurred())

		// Save the reference to the hosted cluster in the cluster order:
		clusterOrderUpdate := clusterOrderObj.DeepCopy()
		clusterOrderUpdate.Status.ClusterReference = &osacv1alpha1.ClusterOrderClusterReferenceType{
			Namespace:         hostedClusterObj.GetNamespace(),
			HostedClusterName: hostedClusterObj.GetName(),
		}
		clusterOrderPatch := crclient.MergeFrom(clusterOrderObj)
		err = kubeClient.Status().Patch(ctx, clusterOrderUpdate, clusterOrderPatch)
		Expect(err).ToNot(HaveOccurred())

		// Get the admin password (with Eventually to handle cache propagation delay)
		var password string
		Eventually(
			func(g Gomega) {
				getPasswordResponse, err := clustersClient.GetPassword(ctx, publicv1.ClustersGetPasswordRequest_builder{
					Id: object.GetId(),
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				password = getPasswordResponse.GetPassword()
				g.Expect(password).To(Equal("my_password"))
			},
			30*time.Second,
			time.Second,
		).Should(Succeed())
	})

	It("Can get the admin password of a cluster via HTTP", func() {
		// Create the cluster
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
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

		// Wait till the Kubernetes object has been created:
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

		// Create the Hypershift namespace:
		namespaceObj := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterOrderObj.GetName(),
			},
		}
		err = kubeClient.Create(ctx, namespaceObj)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			err := kubeClient.Delete(ctx, namespaceObj)
			Expect(err).ToNot(HaveOccurred())
		})

		// Create the Hypershift admin password secret:
		secretObj := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespaceObj.GetName(),
				Name:      "password",
			},
			Data: map[string][]byte{
				"password": []byte("my_password"),
			},
		}
		err = kubeClient.Create(ctx, secretObj)
		Expect(err).ToNot(HaveOccurred())

		// Create the Hypershift hosted cluster, and set the reference to the admin password secret:
		hostedClusterObj := &unstructured.Unstructured{}
		hostedClusterObj.SetGroupVersionKind(gvks.HostedCluster)
		hostedClusterObj.SetNamespace(namespaceObj.GetName())
		hostedClusterObj.SetName(clusterOrderObj.GetName())
		err = kubeClient.Create(ctx, hostedClusterObj)
		Expect(err).ToNot(HaveOccurred())
		hostedClusterUpdate := hostedClusterObj.DeepCopy()
		hostedClusterUpdate.Object["status"] = map[string]any{
			"kubeadminPassword": map[string]any{
				"name": secretObj.GetName(),
			},
		}
		Expect(err).ToNot(HaveOccurred())
		hostedClusterPatch := crclient.MergeFrom(hostedClusterObj)
		err = kubeClient.Status().Patch(ctx, hostedClusterUpdate, hostedClusterPatch)
		Expect(err).ToNot(HaveOccurred())

		// Save the reference to the hosted cluster in the cluster order:
		clusterOrderUpdate := clusterOrderObj.DeepCopy()
		clusterOrderUpdate.Status.ClusterReference = &osacv1alpha1.ClusterOrderClusterReferenceType{
			Namespace:         hostedClusterObj.GetNamespace(),
			HostedClusterName: hostedClusterObj.GetName(),
		}
		clusterOrderPatch := crclient.MergeFrom(clusterOrderObj)
		err = kubeClient.Status().Patch(ctx, clusterOrderUpdate, clusterOrderPatch)
		Expect(err).ToNot(HaveOccurred())

		// Get the admin password (with Eventually to handle cache propagation delay)
		Eventually(
			func(g Gomega) {
				getPasswordResponse, err := clustersClient.GetPasswordViaHttp(
					ctx,
					publicv1.ClustersGetPasswordViaHttpRequest_builder{
						Id: object.GetId(),
					}.Build(),
				)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(getPasswordResponse.GetContentType()).To(Equal("text/plain"))
				g.Expect(getPasswordResponse.GetData()).To(Equal([]byte("my_password")))
			},
			time.Minute,
			time.Second,
		).Should(Succeed())
	})

	It("Can create a cluster with explicit version", func() {
		// Create a non-default cluster version:
		cvClient := privatev1.NewClusterVersionsClient(tool.InternalView().AdminConn())
		version := nextCVVersion()
		cvResponse, err := cvClient.Create(ctx, privatev1.ClusterVersionsCreateRequest_builder{
			Object: privatev1.ClusterVersion_builder{
				Spec: privatev1.ClusterVersionSpec_builder{
					Version: version,
					Image:   fmt.Sprintf("quay.io/openshift-release-dev/ocp-release:%s-multi", version),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		cvObject := cvResponse.GetObject()
		cvName := cvObject.GetMetadata().GetName()
		Expect(cvName).ToNot(BeEmpty(), "ClusterVersion should have a metadata.name assigned")
		DeferCleanup(func() {
			_, err := cvClient.Delete(ctx, privatev1.ClusterVersionsDeleteRequest_builder{
				Id: cvObject.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// Create a cluster specifying that version:
		createResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					Version:  publicv1.ClusterVersionReference_builder{Name: cvName}.Build(),
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

		// Verify the cluster has the correct version:
		Expect(object.GetSpec().GetVersion().GetName()).To(Equal(cvName))

		// Verify via Get as well:
		getResponse, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
			Id: object.GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse.GetObject().GetSpec().GetVersion().GetName()).To(Equal(cvName))
	})

	It("Sets creator to the ID of the user when creating a cluster", func() {
		// Look up the user to get their ID:
		usersClient := privatev1.NewUsersClient(tool.InternalView().AdminConn())
		listResponse, err := usersClient.List(ctx, privatev1.UsersListRequest_builder{
			Filter: new(fmt.Sprintf("this.spec.username == %q", userUsername)),
			Limit:  new(int32(1)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse.GetSize()).To(Equal(int32(1)))
		user := listResponse.GetItems()[0]
		userID := user.GetId()

		// Create the cluster using the client connection:
		response, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		object := response.GetObject()
		DeferCleanup(func() {
			_, err := clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// Verify that the creator is set to the ID of the authenticated user:
		Expect(object).ToNot(BeNil())
		metadata := object.GetMetadata()
		Expect(metadata).ToNot(BeNil())
		Expect(metadata.GetCreator()).To(Equal(userID))
	})
})
