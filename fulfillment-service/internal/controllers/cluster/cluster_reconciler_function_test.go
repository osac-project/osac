/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cluster

import (
	"context"
	"fmt"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers"
	"github.com/osac-project/osac/fulfillment-service/internal/controllers/finalizers"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/annotations"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/masks"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("validateTenant", func() {
	It("should succeed when a tenant is assigned", func() {
		t := &task{
			cluster: privatev1.Cluster_builder{
				Id: "test-cluster",
				Metadata: privatev1.Metadata_builder{
					Tenant: "tenant-1",
				}.Build(),
			}.Build(),
		}

		err := t.validateTenant()
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail when tenant is empty", func() {
		t := &task{
			cluster: privatev1.Cluster_builder{
				Id: "test-cluster",
				Metadata: privatev1.Metadata_builder{
					Tenant: "",
				}.Build(),
			}.Build(),
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})

	It("should fail when metadata is missing", func() {
		t := &task{
			cluster: privatev1.Cluster_builder{
				Id: "test-cluster",
			}.Build(),
		}

		err := t.validateTenant()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant"))
	})
})

var _ = Describe("update tenant annotation", func() {
	const (
		clusterID    = "test-cluster-id"
		tenantName   = "my-tenant"
		hubID        = "test-hub"
		hubNamespace = "test-ns"
	)

	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("should set tenant annotation when creating a new ClusterOrder CR", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		cluster := privatev1.Cluster_builder{
			Id: clusterID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: &privatev1.ClusterTemplateReference{Name: "test-template"},
			}.Build(),
			Status: privatev1.ClusterStatus_builder{
				State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				Hub:   hubID,
			}.Build(),
		}.Build()

		t := &task{
			r: &function{
				logger:         logger,
				hubCache:       hubCache,
				maskCalculator: nil,
			},
			cluster: cluster,
		}

		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify the ClusterOrder CR was created with the tenant annotation
		list := &osacv1alpha1.ClusterOrderList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))

		createdCR := list.Items[0]
		Expect(createdCR.GetAnnotations()).To(HaveKeyWithValue(annotations.Tenant, tenantName))
		Expect(createdCR.GetLabels()).To(HaveKeyWithValue(labels.ClusterOrderUuid, clusterID))
	})

	It("should update ClusterOrder when node set size changes on a ready cluster", func() {
		// Create an existing ClusterOrder with size 3:
		existingOrder := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "order-abc",
				Namespace: hubNamespace,
				Labels: map[string]string{
					labels.ClusterOrderUuid: clusterID,
				},
				Annotations: map[string]string{
					annotations.Tenant: tenantName,
				},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID: "test-template",
				NodeRequests: []osacv1alpha1.NodeRequest{
					{
						ResourceClass: "gpu.gb200",
						NumberOfNodes: 3,
					},
				},
			},
		}

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existingOrder).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		// Create a cluster in READY state with updated node set size (5):
		cluster := privatev1.Cluster_builder{
			Id: clusterID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: &privatev1.ClusterTemplateReference{Name: "test-template"},
				NodeSets: map[string]*privatev1.ClusterNodeSet{
					"gpu.gb200": privatev1.ClusterNodeSet_builder{
						HostType: &privatev1.HostTypeReference{Name: "gpu.gb200"},
						Size:     5,
					}.Build(),
				},
			}.Build(),
			Status: privatev1.ClusterStatus_builder{
				State: privatev1.ClusterState_CLUSTER_STATE_READY,
				Hub:   hubID,
			}.Build(),
		}.Build()

		t := &task{
			r: &function{
				logger:         logger,
				hubCache:       hubCache,
				maskCalculator: nil,
			},
			cluster: cluster,
		}

		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify the ClusterOrder was patched with the new size:
		list := &osacv1alpha1.ClusterOrderList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))

		updatedCR := list.Items[0]
		Expect(updatedCR.Spec.NodeRequests).To(HaveLen(1))
		Expect(updatedCR.Spec.NodeRequests[0].ResourceClass).To(Equal("gpu.gb200"))
		Expect(updatedCR.Spec.NodeRequests[0].NumberOfNodes).To(Equal(5))
	})

	It("should update ClusterOrder when node set size changes on a progressing cluster", func() {
		// Create an existing ClusterOrder with size 3:
		existingOrder := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "order-abc",
				Namespace: hubNamespace,
				Labels: map[string]string{
					labels.ClusterOrderUuid: clusterID,
				},
				Annotations: map[string]string{
					annotations.Tenant: tenantName,
				},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID: "test-template",
				NodeRequests: []osacv1alpha1.NodeRequest{
					{
						ResourceClass: "gpu.gb200",
						NumberOfNodes: 3,
					},
				},
			},
		}

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existingOrder).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		// Create a cluster in PROGRESSING state with updated node set size (5):
		cluster := privatev1.Cluster_builder{
			Id: clusterID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: &privatev1.ClusterTemplateReference{Name: "test-template"},
				NodeSets: map[string]*privatev1.ClusterNodeSet{
					"gpu.gb200": privatev1.ClusterNodeSet_builder{
						HostType: &privatev1.HostTypeReference{Name: "gpu.gb200"},
						Size:     5,
					}.Build(),
				},
			}.Build(),
			Status: privatev1.ClusterStatus_builder{
				State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				Hub:   hubID,
			}.Build(),
		}.Build()

		t := &task{
			r: &function{
				logger:         logger,
				hubCache:       hubCache,
				maskCalculator: nil,
			},
			cluster: cluster,
		}

		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify the ClusterOrder was patched with the new size:
		list := &osacv1alpha1.ClusterOrderList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))

		updatedCR := list.Items[0]
		Expect(updatedCR.Spec.NodeRequests).To(HaveLen(1))
		Expect(updatedCR.Spec.NodeRequests[0].ResourceClass).To(Equal("gpu.gb200"))
		Expect(updatedCR.Spec.NodeRequests[0].NumberOfNodes).To(Equal(5))
	})

	It("should not update ClusterOrder when cluster is in failed state", func() {
		// Create an existing ClusterOrder with size 3:
		existingOrder := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "order-abc",
				Namespace: hubNamespace,
				Labels: map[string]string{
					labels.ClusterOrderUuid: clusterID,
				},
				Annotations: map[string]string{
					annotations.Tenant: tenantName,
				},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID: "test-template",
				NodeRequests: []osacv1alpha1.NodeRequest{
					{
						ResourceClass: "gpu.gb200",
						NumberOfNodes: 3,
					},
				},
			},
		}

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existingOrder).
			Build()

		// No hubCache expectation — the reconciler should return before touching the hub.

		// Create a cluster in FAILED state with updated node set size (5):
		cluster := privatev1.Cluster_builder{
			Id: clusterID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: &privatev1.ClusterTemplateReference{Name: "test-template"},
				NodeSets: map[string]*privatev1.ClusterNodeSet{
					"gpu.gb200": privatev1.ClusterNodeSet_builder{
						HostType: &privatev1.HostTypeReference{Name: "gpu.gb200"},
						Size:     5,
					}.Build(),
				},
			}.Build(),
			Status: privatev1.ClusterStatus_builder{
				State: privatev1.ClusterState_CLUSTER_STATE_FAILED,
				Hub:   hubID,
			}.Build(),
		}.Build()

		t := &task{
			r: &function{
				logger:         logger,
				hubCache:       nil,
				maskCalculator: nil,
			},
			cluster: cluster,
		}

		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify the ClusterOrder was NOT patched — size should still be 3:
		list := &osacv1alpha1.ClusterOrderList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))

		unchangedCR := list.Items[0]
		Expect(unchangedCR.Spec.NodeRequests).To(HaveLen(1))
		Expect(unchangedCR.Spec.NodeRequests[0].NumberOfNodes).To(Equal(3))
	})

	It("should map explicit cluster fields to ClusterOrder CR spec", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		pullSecret := "my-pull-secret"
		sshKey := "ssh-ed25519 AAAA..."
		versionName := "4-17-0"
		resolvedImage := "quay.io/openshift-release-dev/ocp-release:4.17.0-multi"
		podCIDR := "10.128.0.0/14"
		serviceCIDR := "172.30.0.0/16"

		clusterVersionsClient := NewMockClusterVersionsClient(ctrl)
		clusterVersionsClient.EXPECT().
			List(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(privatev1.ClusterVersionsListResponse_builder{
				Total: 1,
				Size:  1,
				Items: []*privatev1.ClusterVersion{
					privatev1.ClusterVersion_builder{
						Spec: privatev1.ClusterVersionSpec_builder{
							Image: resolvedImage,
						}.Build(),
					}.Build(),
				},
			}.Build(), nil)

		cluster := privatev1.Cluster_builder{
			Id: clusterID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template:     &privatev1.ClusterTemplateReference{Name: "test-template"},
				PullSecret:   &pullSecret,
				SshPublicKey: &sshKey,
				Version:      &privatev1.ClusterVersionReference{Name: versionName},
				Network: privatev1.ClusterNetwork_builder{
					PodCidr:     &podCIDR,
					ServiceCidr: &serviceCIDR,
				}.Build(),
			}.Build(),
			Status: privatev1.ClusterStatus_builder{
				State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				Hub:   hubID,
			}.Build(),
		}.Build()

		t := &task{
			r: &function{
				logger:                logger,
				hubCache:              hubCache,
				clusterVersionsClient: clusterVersionsClient,
				maskCalculator:        nil,
			},
			cluster: cluster,
		}

		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())

		// Verify the ClusterOrder CR spec contains the explicit fields
		list := &osacv1alpha1.ClusterOrderList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))

		createdCR := list.Items[0]
		Expect(createdCR.Spec.PullSecret).To(Equal(pullSecret))
		Expect(createdCR.Spec.SSHPublicKey).To(Equal(sshKey))
		Expect(createdCR.Spec.ReleaseImage).To(Equal(resolvedImage))
		Expect(createdCR.Spec.Network).ToNot(BeNil())
		Expect(createdCR.Spec.Network.PodCIDR).To(Equal(podCIDR))
		Expect(createdCR.Spec.Network.ServiceCIDR).To(Equal(serviceCIDR))
	})

	It("should resolve ReleaseImage via ClusterVersion on every reconcile", func() {
		resolvedImage := "quay.io/openshift-release-dev/ocp-release:4.17.0-multi"
		versionName := "4-17-0"

		// Existing ClusterOrder has size 3 and a previously resolved ReleaseImage:
		existingOrder := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "order-abc",
				Namespace: hubNamespace,
				Labels: map[string]string{
					labels.ClusterOrderUuid: clusterID,
				},
				Annotations: map[string]string{
					annotations.Tenant: tenantName,
				},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test-template",
				ReleaseImage: resolvedImage,
				NodeRequests: []osacv1alpha1.NodeRequest{
					{
						ResourceClass: "gpu.gb200",
						NumberOfNodes: 3,
					},
				},
			},
		}

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existingOrder).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		clusterVersionsClient := NewMockClusterVersionsClient(ctrl)
		clusterVersionsClient.EXPECT().
			List(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(privatev1.ClusterVersionsListResponse_builder{
				Total: 1,
				Size:  1,
				Items: []*privatev1.ClusterVersion{
					privatev1.ClusterVersion_builder{
						Spec: privatev1.ClusterVersionSpec_builder{
							Image: resolvedImage,
						}.Build(),
					}.Build(),
				},
			}.Build(), nil)

		// Cluster spec has updated node-set size (5), simulating a resize:
		cluster := privatev1.Cluster_builder{
			Id: clusterID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: &privatev1.ClusterTemplateReference{Name: "test-template"},
				Version:  &privatev1.ClusterVersionReference{Name: versionName},
				NodeSets: map[string]*privatev1.ClusterNodeSet{
					"gpu.gb200": privatev1.ClusterNodeSet_builder{
						HostType: &privatev1.HostTypeReference{Name: "gpu.gb200"},
						Size:     5,
					}.Build(),
				},
			}.Build(),
			Status: privatev1.ClusterStatus_builder{
				State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				Hub:   hubID,
			}.Build(),
		}.Build()

		t := &task{
			r: &function{
				logger:                logger,
				hubCache:              hubCache,
				clusterVersionsClient: clusterVersionsClient,
				maskCalculator:        nil,
			},
			cluster: cluster,
		}

		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())

		list := &osacv1alpha1.ClusterOrderList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))

		patchedCR := list.Items[0]
		Expect(patchedCR.Spec.ReleaseImage).To(Equal(resolvedImage))
		Expect(patchedCR.Spec.NodeRequests).To(HaveLen(1))
		Expect(patchedCR.Spec.NodeRequests[0].NumberOfNodes).To(Equal(5))
	})

	It("should update ReleaseImage when version changes", func() {
		oldImage := "quay.io/openshift-release-dev/ocp-release:4.17.0-multi"
		newImage := "quay.io/openshift-release-dev/ocp-release:4.18.0-multi"
		newVersionName := "4-18-0"

		// Existing ClusterOrder was created with the old version:
		existingOrder := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "order-abc",
				Namespace: hubNamespace,
				Labels: map[string]string{
					labels.ClusterOrderUuid: clusterID,
				},
				Annotations: map[string]string{
					annotations.Tenant: tenantName,
				},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test-template",
				ReleaseImage: oldImage,
				NodeRequests: []osacv1alpha1.NodeRequest{
					{
						ResourceClass: "gpu.gb200",
						NumberOfNodes: 3,
					},
				},
			},
		}

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existingOrder).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil)

		// Mock returns the new version's image:
		clusterVersionsClient := NewMockClusterVersionsClient(ctrl)
		clusterVersionsClient.EXPECT().
			List(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(privatev1.ClusterVersionsListResponse_builder{
				Total: 1,
				Size:  1,
				Items: []*privatev1.ClusterVersion{
					privatev1.ClusterVersion_builder{
						Spec: privatev1.ClusterVersionSpec_builder{
							Image: newImage,
						}.Build(),
					}.Build(),
				},
			}.Build(), nil)

		// Cluster spec now references the new version:
		cluster := privatev1.Cluster_builder{
			Id: clusterID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: &privatev1.ClusterTemplateReference{Name: "test-template"},
				Version:  &privatev1.ClusterVersionReference{Name: newVersionName},
				NodeSets: map[string]*privatev1.ClusterNodeSet{
					"gpu.gb200": privatev1.ClusterNodeSet_builder{
						HostType: &privatev1.HostTypeReference{Name: "gpu.gb200"},
						Size:     3,
					}.Build(),
				},
			}.Build(),
			Status: privatev1.ClusterStatus_builder{
				State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				Hub:   hubID,
			}.Build(),
		}.Build()

		t := &task{
			r: &function{
				logger:                logger,
				hubCache:              hubCache,
				clusterVersionsClient: clusterVersionsClient,
				maskCalculator:        nil,
			},
			cluster: cluster,
		}

		err := t.update(ctx)
		Expect(err).ToNot(HaveOccurred())

		list := &osacv1alpha1.ClusterOrderList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))

		patchedCR := list.Items[0]
		Expect(patchedCR.Spec.ReleaseImage).To(Equal(newImage))
	})
})

var _ = Describe("resolveVersionImage", func() {
	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("should return error when gRPC List fails", func() {
		clusterVersionsClient := NewMockClusterVersionsClient(ctrl)
		clusterVersionsClient.EXPECT().
			List(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil, fmt.Errorf("connection refused"))

		t := &task{
			r: &function{
				clusterVersionsClient: clusterVersionsClient,
			},
		}

		_, err := t.resolveVersionImage(ctx, "4-17-0")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to look up cluster version '4-17-0'"))
	})

	It("should return error when version is not found", func() {
		clusterVersionsClient := NewMockClusterVersionsClient(ctrl)
		clusterVersionsClient.EXPECT().
			List(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(privatev1.ClusterVersionsListResponse_builder{}.Build(), nil)

		t := &task{
			r: &function{
				clusterVersionsClient: clusterVersionsClient,
			},
		}

		_, err := t.resolveVersionImage(ctx, "nonexistent")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cluster version not found: 'nonexistent'"))
	})
})

var _ = Describe("update version resolution failure", func() {
	const (
		clusterID    = "test-cluster-id"
		tenantName   = "my-tenant"
		hubID        = "test-hub"
		hubNamespace = "test-ns"
	)

	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("should set ResourcesUnavailable condition when cluster version is not found", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{Namespace: hubNamespace, Client: fakeClient}, nil)

		clusterVersionsClient := NewMockClusterVersionsClient(ctrl)
		clusterVersionsClient.EXPECT().
			List(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(privatev1.ClusterVersionsListResponse_builder{}.Build(), nil)

		var updateReq *privatev1.ClustersUpdateRequest
		clustersClient := NewMockClustersClient(ctrl)
		clustersClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ClustersUpdateRequest, opts ...grpc.CallOption) (*privatev1.ClustersUpdateResponse, error) {
				updateReq = req
				return &privatev1.ClustersUpdateResponse{Object: req.GetObject()}, nil
			}).
			Times(1)

		versionName := "nonexistent-version"
		cluster := privatev1.Cluster_builder{
			Id: clusterID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: &privatev1.ClusterTemplateReference{Name: "test-template"},
				Version:  &privatev1.ClusterVersionReference{Name: versionName},
			}.Build(),
			Status: privatev1.ClusterStatus_builder{
				State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				Hub:   hubID,
			}.Build(),
		}.Build()

		f := &function{
			logger:                logger,
			hubCache:              hubCache,
			clustersClient:        clustersClient,
			clusterVersionsClient: clusterVersionsClient,
			maskCalculator:        nil,
		}

		err := f.run(ctx, cluster)
		Expect(err).ToNot(HaveOccurred())

		Expect(updateReq).ToNot(BeNil(), "Update should have been called")
		conditions := updateReq.GetObject().GetStatus().GetConditions()
		found := false
		for _, c := range conditions {
			if c.GetType() == privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING {
				Expect(c.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
				Expect(c.GetReason()).To(Equal("ResourcesUnavailable"))
				Expect(c.GetMessage()).To(ContainSubstring("cluster version not found"))
				found = true
			}
		}
		Expect(found).To(BeTrue(), "should have set ResourcesUnavailable condition")
	})
})

// newTaskForDelete creates a task configured for testing delete() with hub-dependent paths.
func newTaskForDelete(clusterID, hubID string, hubCache controllers.HubCache) *task {
	cluster := privatev1.Cluster_builder{
		Id: clusterID,
		Metadata: privatev1.Metadata_builder{
			Finalizers: []string{finalizers.Controller},
		}.Build(),
		Status: privatev1.ClusterStatus_builder{
			Hub: hubID,
		}.Build(),
	}.Build()

	f := &function{
		logger:   logger,
		hubCache: hubCache,
	}

	return &task{
		r:       f,
		cluster: cluster,
	}
}

// hasFinalizer checks if the fulfillment-controller finalizer is present on the cluster.
func hasFinalizer(cluster *privatev1.Cluster) bool {
	return slices.Contains(cluster.GetMetadata().GetFinalizers(), finalizers.Controller)
}

var _ = Describe("delete", func() {
	const (
		clusterID = "cluster-delete-id"
		hubID     = "test-hub"
	)

	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("should remove finalizer when hub cache returns ErrHubNotFound", func() {
		// This test verifies the core behavior: when a hub is decommissioned/deleted,
		// the reconciler removes its finalizer to allow the cluster to be archived.
		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(nil, controllers.ErrHubNotFound)

		t := newTaskForDelete(clusterID, hubID, hubCache)
		Expect(hasFinalizer(t.cluster)).To(BeTrue())

		err := t.delete(ctx)
		// Should return nil (not propagate the error)
		Expect(err).ToNot(HaveOccurred())
		// Finalizer should be removed to allow archiving
		Expect(hasFinalizer(t.cluster)).To(BeFalse())
	})
})

var _ = Describe("hub persistence", func() {
	const (
		clusterID    = "test-cluster-hub"
		tenantName   = "test-tenant"
		hubID        = "test-hub-123"
		hubNamespace = "hub-123-ns"
	)

	var (
		ctx  context.Context
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)
	})

	It("should select hub and return without creating ClusterOrder", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil).
			AnyTimes()

		hubsClient := controllers.NewMockHubsClient(ctrl)
		hubsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.HubsListResponse{
				Items: []*privatev1.Hub{
					privatev1.Hub_builder{Id: hubID}.Build(),
				},
			}, nil)

		clustersClient := NewMockClustersClient(ctrl)
		clustersClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ClustersUpdateRequest, opts ...grpc.CallOption) (*privatev1.ClustersUpdateResponse, error) {
				Expect(req.GetObject().GetStatus().GetHub()).To(Equal(hubID))
				return &privatev1.ClustersUpdateResponse{Object: req.GetObject()}, nil
			}).
			AnyTimes()

		cluster := privatev1.Cluster_builder{
			Id: clusterID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: &privatev1.ClusterTemplateReference{Name: "test-template"},
			}.Build(),
			Status: privatev1.ClusterStatus_builder{
				State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				Hub:   "",
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			clustersClient: clustersClient,
			hubsClient:     hubsClient,
			maskCalculator: nil,
		}

		err := f.run(ctx, cluster)
		Expect(err).ToNot(HaveOccurred())

		Expect(cluster.GetStatus().GetHub()).To(Equal(hubID))

		list := &osacv1alpha1.ClusterOrderList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(BeEmpty(), "ClusterOrder should NOT be created on first reconcile")
	})

	It("should skip hub selection if already set", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil).
			AnyTimes()

		hubsClient := controllers.NewMockHubsClient(ctrl)

		clustersClient := NewMockClustersClient(ctrl)
		clustersClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ClustersUpdateRequest, opts ...grpc.CallOption) (*privatev1.ClustersUpdateResponse, error) {
				Expect(req.GetUpdateMask().GetPaths()).ToNot(ContainElement("status.hub"))
				return &privatev1.ClustersUpdateResponse{Object: req.GetObject()}, nil
			}).
			AnyTimes()

		cluster := privatev1.Cluster_builder{
			Id: clusterID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: &privatev1.ClusterTemplateReference{Name: "test-template"},
			}.Build(),
			Status: privatev1.ClusterStatus_builder{
				State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				Hub:   hubID,
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			clustersClient: clustersClient,
			hubsClient:     hubsClient,
			maskCalculator: nil,
		}

		err := f.run(ctx, cluster)
		Expect(err).ToNot(HaveOccurred())

		list := &osacv1alpha1.ClusterOrderList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].GetNamespace()).To(Equal(hubNamespace))
	})

	It("should create ClusterOrder on second reconcile after hub is persisted", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{
				Namespace: hubNamespace,
				Client:    fakeClient,
			}, nil).
			AnyTimes()

		hubsClient := controllers.NewMockHubsClient(ctrl)
		hubsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.HubsListResponse{
				Items: []*privatev1.Hub{
					privatev1.Hub_builder{Id: hubID}.Build(),
				},
			}, nil).
			AnyTimes()

		clustersClient := NewMockClustersClient(ctrl)
		clustersClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&privatev1.ClustersUpdateResponse{}, nil).
			AnyTimes()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			clustersClient: clustersClient,
			hubsClient:     hubsClient,
			maskCalculator: nil,
		}

		// First reconcile: hub empty → select hub, return without creating CR
		cluster1 := privatev1.Cluster_builder{
			Id: clusterID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: &privatev1.ClusterTemplateReference{Name: "test-template"},
			}.Build(),
			Status: privatev1.ClusterStatus_builder{
				State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				Hub:   "",
			}.Build(),
		}.Build()

		err := f.run(ctx, cluster1)
		Expect(err).ToNot(HaveOccurred())

		list := &osacv1alpha1.ClusterOrderList{}
		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(BeEmpty(), "no CR on first reconcile")

		// Second reconcile: hub already set → creates CR
		cluster2 := privatev1.Cluster_builder{
			Id: clusterID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: &privatev1.ClusterTemplateReference{Name: "test-template"},
			}.Build(),
			Status: privatev1.ClusterStatus_builder{
				State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				Hub:   hubID,
			}.Build(),
		}.Build()

		err = f.run(ctx, cluster2)
		Expect(err).ToNot(HaveOccurred())

		err = fakeClient.List(ctx, list)
		Expect(err).ToNot(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].GetNamespace()).To(Equal(hubNamespace))
	})

	It("should set ResourcesUnavailable condition when no hubs are available", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		_ = fakeClient

		hubsClient := controllers.NewMockHubsClient(ctrl)
		hubsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.HubsListResponse{
				Items: []*privatev1.Hub{},
			}, nil).
			AnyTimes()

		clustersClient := NewMockClustersClient(ctrl)
		clustersClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ClustersUpdateRequest, opts ...grpc.CallOption) (*privatev1.ClustersUpdateResponse, error) {
				return &privatev1.ClustersUpdateResponse{Object: req.GetObject()}, nil
			}).
			AnyTimes()

		cluster := privatev1.Cluster_builder{
			Id: clusterID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: &privatev1.ClusterTemplateReference{Name: "test-template"},
			}.Build(),
			Status: privatev1.ClusterStatus_builder{
				State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				Hub:   "",
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			clustersClient: clustersClient,
			hubsClient:     hubsClient,
			maskCalculator: nil,
		}

		err := f.run(ctx, cluster)
		Expect(err).ToNot(HaveOccurred())

		conditions := cluster.GetStatus().GetConditions()
		found := false
		for _, c := range conditions {
			if c.GetType() == privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING {
				Expect(c.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
				Expect(c.GetReason()).To(Equal("ResourcesUnavailable"))
				found = true
			}
		}
		Expect(found).To(BeTrue(), "should have set ResourcesUnavailable condition")
	})

	It("should handle cluster with no status without panicking", func() {
		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{Namespace: hubNamespace, Client: fakeClient}, nil).
			AnyTimes()

		hubsClient := controllers.NewMockHubsClient(ctrl)
		hubsClient.EXPECT().
			List(gomock.Any(), gomock.Any()).
			Return(&privatev1.HubsListResponse{
				Items: []*privatev1.Hub{privatev1.Hub_builder{Id: hubID}.Build()},
			}, nil).
			AnyTimes()

		clustersClient := NewMockClustersClient(ctrl)
		clustersClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ClustersUpdateRequest, opts ...grpc.CallOption) (*privatev1.ClustersUpdateResponse, error) {
				return &privatev1.ClustersUpdateResponse{Object: req.GetObject()}, nil
			}).
			AnyTimes()

		cluster := privatev1.Cluster_builder{
			Id: clusterID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: &privatev1.ClusterTemplateReference{Name: "test-template"},
			}.Build(),
			// No Status field — run() must initialize it without panicking
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			clustersClient: clustersClient,
			hubsClient:     hubsClient,
			maskCalculator: nil,
		}

		Expect(func() { f.run(ctx, cluster) }).ToNot(Panic())
	})
})

var _ = Describe("Kubernetes validation error handling", func() {
	It("should set state to FAILED when K8s Create returns Invalid error", func() {
		ctx := context.Background()
		ctrl := gomock.NewController(GinkgoT())
		DeferCleanup(ctrl.Finish)

		const (
			clusterID    = "cluster-invalid-test"
			tenantName   = "test-tenant"
			hubID        = "hub-1"
			hubNamespace = "test-ns"
		)

		scheme := runtime.NewScheme()
		Expect(osacv1alpha1.AddToScheme(scheme)).To(Succeed())

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client clnt.WithWatch, obj clnt.Object, opts ...clnt.CreateOption) error {
					return apierrors.NewInvalid(
						schema.GroupKind{Group: "osac.openshift.io", Kind: "ClusterOrder"},
						"order-test",
						field.ErrorList{field.Invalid(field.NewPath("spec", "templateID"), "", "invalid template")},
					)
				},
			}).
			Build()

		hubCache := controllers.NewMockHubCache(ctrl)
		hubCache.EXPECT().
			Get(gomock.Any(), hubID).
			Return(&controllers.HubEntry{Namespace: hubNamespace, Client: fakeClient}, nil)

		clustersClient := NewMockClustersClient(ctrl)
		clustersClient.EXPECT().
			Update(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(ctx context.Context, req *privatev1.ClustersUpdateRequest, opts ...grpc.CallOption) (*privatev1.ClustersUpdateResponse, error) {
				return &privatev1.ClustersUpdateResponse{Object: req.GetObject()}, nil
			}).
			MinTimes(1)

		cluster := privatev1.Cluster_builder{
			Id: clusterID,
			Metadata: privatev1.Metadata_builder{
				Finalizers: []string{finalizers.Controller},
				Tenant:     tenantName,
			}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template: &privatev1.ClusterTemplateReference{Name: "test-template"},
			}.Build(),
			Status: privatev1.ClusterStatus_builder{
				State: privatev1.ClusterState_CLUSTER_STATE_PROGRESSING,
				Hub:   hubID,
			}.Build(),
		}.Build()

		f := &function{
			logger:         logger,
			hubCache:       hubCache,
			clustersClient: clustersClient,
			maskCalculator: masks.NewCalculator().Build(),
		}

		err := f.run(ctx, cluster)
		Expect(err).ToNot(HaveOccurred())

		Expect(cluster.GetStatus().GetState()).To(
			Equal(privatev1.ClusterState_CLUSTER_STATE_FAILED),
		)

		conditions := cluster.GetStatus().GetConditions()
		var progressingCondition *privatev1.ClusterCondition
		for _, c := range conditions {
			if c.GetType() == privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING {
				progressingCondition = c
				break
			}
		}
		Expect(progressingCondition).ToNot(BeNil())
		Expect(progressingCondition.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
		Expect(progressingCondition.GetReason()).To(Equal("ValidationFailed"))
		Expect(progressingCondition.GetMessage()).To(ContainSubstring("invalid template"))
	})
})
