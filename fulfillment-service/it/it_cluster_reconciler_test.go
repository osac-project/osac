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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	corev1 "k8s.io/api/core/v1"
	kubeerrors "k8s.io/apimachinery/pkg/api/errors"
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

func verifyNotFound(g Gomega, err error) {
	g.Expect(err).To(HaveOccurred())
	st, ok := grpcstatus.FromError(err)
	g.Expect(ok).To(BeTrue())
	g.Expect(st.Code()).To(Equal(grpccodes.NotFound))
}

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
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-ht-%s", uuid.New()[24:32]),
				}.Build(),
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
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-tmpl-%s", uuid.New()[24:32]),
				}.Build(),
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

	It("Creates the Kubernetes object when a cluster is created", func() {
		// Create the cluster
		response, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
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
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
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
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					TemplateParameters: map[string]*anypb.Any{
						"my": makeAny(wrapperspb.String("my_value")),
					},
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"my_node_set": publicv1.ClusterNodeSet_builder{
							HostType: publicv1.HostTypeReference_builder{Id: hostTypeId}.Build(),
							Size:     proto.Int32(3),
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
				Metadata: publicv1.Metadata_builder{
					Name: object.GetMetadata().GetName(),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
					TemplateParameters: map[string]*anypb.Any{
						"my": makeAny(wrapperspb.String("my_value")),
					},
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"my_node_set": publicv1.ClusterNodeSet_builder{
							HostType: publicv1.HostTypeReference_builder{Id: hostTypeId}.Build(),
							Size:     proto.Int32(5),
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
	Describe("Manages secrets as part of the cluster lifecycle", func() {
		var (
			secretsClient privatev1.SecretsClient
		)
		BeforeEach(func() {
			secretsClient = privatev1.NewSecretsClient(tool.InternalView().AdminConn())
		})

		It("Creates and deletes hub secrets when a cluster is created and deleted", func() {
			// Create the cluster
			response, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-cluster-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: templateId}.Build(),
						TemplateParameters: map[string]*anypb.Any{
							"my": makeAny(wrapperspb.String("my_value")),
						}}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			DeferCleanup(func() {
				_, err := clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err == nil || grpcstatus.Code(err) == grpccodes.NotFound).To(BeTrue())
			})

			// Arrange the HyperShift resources that a real provisioner would create.
			kubeClient := tool.KubeClient()
			clusterOrderList := &osacv1alpha1.ClusterOrderList{}
			var clusterOrderObj *osacv1alpha1.ClusterOrder
			Eventually(func(g Gomega) {
				err := kubeClient.List(ctx, clusterOrderList, crclient.MatchingLabels{
					labels.ClusterOrderUuid: object.GetId(),
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(clusterOrderList.Items).To(HaveLen(1))
				clusterOrderObj = &clusterOrderList.Items[0]
			}, time.Minute, time.Second).Should(Succeed())

			hostedClusterNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: clusterOrderObj.GetName()},
			}
			err = kubeClient.Create(ctx, hostedClusterNamespace)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				err := kubeClient.Delete(ctx, hostedClusterNamespace)
				Expect(err == nil || kubeerrors.IsNotFound(err)).To(BeTrue())
			})

			kubeconfigSecretObj := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: hostedClusterNamespace.GetName(),
					Name:      "kubeconfig",
				},
				Data: map[string][]byte{"kubeconfig": []byte("my-kubeconfig")},
			}
			passwordSecretObj := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: hostedClusterNamespace.GetName(),
					Name:      "kubeadmin-password",
				},
				Data: map[string][]byte{"password": []byte("my-password")},
			}
			Expect(kubeClient.Create(ctx, kubeconfigSecretObj)).To(Succeed())
			Expect(kubeClient.Create(ctx, passwordSecretObj)).To(Succeed())

			hostedClusterObj := &unstructured.Unstructured{}
			hostedClusterObj.SetGroupVersionKind(gvks.HostedCluster)
			hostedClusterObj.SetNamespace(hostedClusterNamespace.GetName())
			hostedClusterObj.SetName(clusterOrderObj.GetName())
			Expect(kubeClient.Create(ctx, hostedClusterObj)).To(Succeed())

			hostedClusterUpdate := hostedClusterObj.DeepCopy()
			hostedClusterUpdate.Object["status"] = map[string]any{
				"kubeconfig":        map[string]any{"name": kubeconfigSecretObj.GetName()},
				"kubeadminPassword": map[string]any{"name": passwordSecretObj.GetName()},
			}
			Expect(kubeClient.Status().Patch(
				ctx, hostedClusterUpdate, crclient.MergeFrom(hostedClusterObj),
			)).To(Succeed())

			clusterOrderUpdate := clusterOrderObj.DeepCopy()
			clusterOrderUpdate.Status.Phase = osacv1alpha1.ClusterOrderPhaseReady
			clusterOrderUpdate.Status.ClusterReference = &osacv1alpha1.ClusterOrderClusterReferenceType{
				Namespace:         hostedClusterObj.GetNamespace(),
				HostedClusterName: hostedClusterObj.GetName(),
			}
			Expect(kubeClient.Status().Patch(
				ctx, clusterOrderUpdate, crclient.MergeFrom(clusterOrderObj),
			)).To(Succeed())

			privateClustersClient := privatev1.NewClustersClient(tool.InternalView().AdminConn())
			privateClusterResponse, err := privateClustersClient.Get(ctx, privatev1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			privateCluster := privateClusterResponse.GetObject()
			privateCluster.GetStatus().SetState(privatev1.ClusterState_CLUSTER_STATE_READY)
			_, err = privateClustersClient.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privateCluster,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = privateClustersClient.Signal(ctx, privatev1.ClustersSignalRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// The secret references should be populated in the cluster status:
			var kubeconfigID, passwordID string
			Eventually(func(g Gomega) {
				cluster, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{
					Id: object.GetId(),
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				kubeconfigID = cluster.GetObject().GetStatus().GetKubeconfigSecret().GetId()
				passwordID = cluster.GetObject().GetStatus().GetPasswordSecret().GetId()
				g.Expect(kubeconfigID).ToNot(BeEmpty())
				g.Expect(passwordID).ToNot(BeEmpty())
			}, time.Minute, time.Second).Should(Succeed())

			// Make sure the secrets exist:
			configSecret, err := secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{
				Id: kubeconfigID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(configSecret.GetObject().GetData()).To(HaveKey("kubeconfig"))

			passwordSecret, err := secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{
				Id: passwordID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(passwordSecret.GetObject().GetData()).To(HaveKey("password"))

			// Delete the cluster:
			_, err = clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			err = kubeClient.Delete(ctx, clusterOrderObj)
			Expect(err == nil || kubeerrors.IsNotFound(err)).To(BeTrue())

			Eventually(func(g Gomega) {
				_, err := privateClustersClient.Signal(ctx, privatev1.ClustersSignalRequest_builder{
					Id: object.GetId(),
				}.Build())
				if err != nil {
					g.Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
				}

				_, err = secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{
					Id: kubeconfigID,
				}.Build())
				g.Expect(err).To(HaveOccurred())
				verifyNotFound(g, err)
				_, err = secretsClient.Get(ctx, privatev1.SecretsGetRequest_builder{
					Id: passwordID,
				}.Build())
				verifyNotFound(g, err)
			}, time.Minute, time.Second).Should(Succeed())
		})
	})
})
