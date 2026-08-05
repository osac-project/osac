/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
)

type mockClustersClient struct {
	getResponse    *privatev1.ClustersGetResponse
	getError       error
	updateResponse *privatev1.ClustersUpdateResponse
	updateError    error
	updateCalled   bool
	updateCount    int
	lastUpdate     *privatev1.Cluster
	signalCalled   bool
	signalCount    int
	signalID       string
	signalError    error
}

func (m *mockClustersClient) List(_ context.Context, _ *privatev1.ClustersListRequest, _ ...grpc.CallOption) (*privatev1.ClustersListResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockClustersClient) Get(_ context.Context, _ *privatev1.ClustersGetRequest, _ ...grpc.CallOption) (*privatev1.ClustersGetResponse, error) {
	if m.getError != nil {
		return nil, m.getError
	}
	return m.getResponse, nil
}

func (m *mockClustersClient) Create(_ context.Context, _ *privatev1.ClustersCreateRequest, _ ...grpc.CallOption) (*privatev1.ClustersCreateResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockClustersClient) Delete(_ context.Context, _ *privatev1.ClustersDeleteRequest, _ ...grpc.CallOption) (*privatev1.ClustersDeleteResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockClustersClient) Update(_ context.Context, in *privatev1.ClustersUpdateRequest, _ ...grpc.CallOption) (*privatev1.ClustersUpdateResponse, error) {
	m.updateCalled = true
	m.updateCount++
	m.lastUpdate = in.GetObject()
	if m.updateError != nil {
		return nil, m.updateError
	}
	return m.updateResponse, nil
}

func (m *mockClustersClient) Signal(_ context.Context, in *privatev1.ClustersSignalRequest, _ ...grpc.CallOption) (*privatev1.ClustersSignalResponse, error) {
	m.signalCalled = true
	m.signalCount++
	m.signalID = in.GetId()
	if m.signalError != nil {
		return nil, m.signalError
	}
	return &privatev1.ClustersSignalResponse{}, nil
}

var _ = Describe("ClusterOrder FeedbackReconciler", func() {
	const (
		resourceName   = "test-cluster-order"
		clusterOrderNS = "osac-orders-test"
		clusterID      = "test-cluster-id"
	)

	var (
		testCtx            context.Context
		typeNamespacedName types.NamespacedName
		mockClient         *mockClustersClient
		reconciler         *FeedbackReconciler
	)

	newClusterGetResponse := func() *privatev1.ClustersGetResponse {
		return &privatev1.ClustersGetResponse{
			Object: &privatev1.Cluster{
				Id:     clusterID,
				Spec:   &privatev1.ClusterSpec{},
				Status: &privatev1.ClusterStatus{},
			},
		}
	}

	BeforeEach(func() {
		testCtx = context.Background()
		typeNamespacedName = types.NamespacedName{
			Name:      resourceName,
			Namespace: clusterOrderNS,
		}
		mockClient = &mockClustersClient{}
		reconciler = &FeedbackReconciler{
			bridge:                newClusterOrderFeedbackBridge(k8sClient, mockClient),
			clusterOrderNamespace: clusterOrderNS,
		}

		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterOrderNS,
			},
		}
		err := k8sClient.Get(testCtx, types.NamespacedName{Name: clusterOrderNS}, namespace)
		if err != nil && apierrors.IsNotFound(err) {
			Expect(k8sClient.Create(testCtx, namespace)).To(Succeed())
		}
	})

	Context("When reconciling a resource that doesn't exist", func() {
		It("should return without error and not signal", func() {
			request := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "non-existent",
					Namespace: clusterOrderNS,
				},
			}
			result, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeFalse())
			Expect(mockClient.signalCalled).To(BeFalse())
		})
	})

	Context("When reconciling a resource without the cluster ID label", func() {
		BeforeEach(func() {
			co := &osacv1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: clusterOrderNS,
				},
				Spec: osacv1alpha1.ClusterOrderSpec{
					TemplateID: "test_template",
				},
			}
			Expect(k8sClient.Create(testCtx, co)).To(Succeed())
		})

		AfterEach(func() {
			co := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, co)
			if err == nil {
				co.Finalizers = nil
				_ = k8sClient.Update(testCtx, co)
				_ = k8sClient.Delete(testCtx, co)
			}
		})

		It("should skip reconciliation", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeFalse())
		})

		It("should remove feedback finalizer from CR without cluster ID label being deleted", func() {
			co := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, co)).To(Succeed())
			co.Finalizers = []string{osacClusterOrderFeedbackFinalizer}
			Expect(k8sClient.Update(testCtx, co)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, co)).To(Succeed())
			Expect(k8sClient.Delete(testCtx, co)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())

			updated := &osacv1alpha1.ClusterOrder{}
			err = k8sClient.Get(testCtx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeFalse())
		})
	})

	Context("When reconciling a resource that is being deleted", func() {
		BeforeEach(func() {
			co := &osacv1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: clusterOrderNS,
					Labels: map[string]string{
						osacClusterOrderIDLabel: clusterID,
					},
					Finalizers: []string{osacClusterOrderFeedbackFinalizer},
				},
				Spec: osacv1alpha1.ClusterOrderSpec{
					TemplateID: "test_template",
				},
			}
			Expect(k8sClient.Create(testCtx, co)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, co)).To(Succeed())
			co.Status.Phase = osacv1alpha1.ClusterOrderPhaseDeleting
			Expect(k8sClient.Status().Update(testCtx, co)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, co)).To(Succeed())
			Expect(k8sClient.Delete(testCtx, co)).To(Succeed())

			mockClient.getResponse = newClusterGetResponse()
			mockClient.updateResponse = &privatev1.ClustersUpdateResponse{}
		})

		AfterEach(func() {
			co := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, co)
			if err == nil {
				co.Finalizers = nil
				Expect(k8sClient.Update(testCtx, co)).To(Succeed())
			}
		})

		It("should sync Deleting state to fulfillment service", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate).NotTo(BeNil())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ClusterState_CLUSTER_STATE_DELETING))
		})

		It("should signal and remove finalizer when it's the last one", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeTrue())
			Expect(mockClient.signalID).To(Equal(clusterID))

			updated := &osacv1alpha1.ClusterOrder{}
			err = k8sClient.Get(testCtx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should still remove finalizer when Signal fails", func() {
			mockClient.signalError = errors.New("already archived")

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeTrue())

			updated := &osacv1alpha1.ClusterOrder{}
			err = k8sClient.Get(testCtx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("When reconciling a resource deleted while still in Progressing phase", func() {
		BeforeEach(func() {
			co := &osacv1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: clusterOrderNS,
					Labels: map[string]string{
						osacClusterOrderIDLabel: clusterID,
					},
					Finalizers: []string{osacClusterOrderFeedbackFinalizer},
				},
				Spec: osacv1alpha1.ClusterOrderSpec{
					TemplateID: "test_template",
				},
			}
			Expect(k8sClient.Create(testCtx, co)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, co)).To(Succeed())
			co.Status.Phase = osacv1alpha1.ClusterOrderPhaseProgressing
			Expect(k8sClient.Status().Update(testCtx, co)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, co)).To(Succeed())
			Expect(k8sClient.Delete(testCtx, co)).To(Succeed())

			mockClient.getResponse = newClusterGetResponse()
			mockClient.updateResponse = &privatev1.ClustersUpdateResponse{}
		})

		AfterEach(func() {
			co := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, co)
			if err == nil {
				co.Finalizers = nil
				Expect(k8sClient.Update(testCtx, co)).To(Succeed())
			}
		})

		It("should force DELETING state even from Progressing phase and remove finalizer", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ClusterState_CLUSTER_STATE_DELETING))
			Expect(mockClient.signalCalled).To(BeTrue())
			Expect(mockClient.signalID).To(Equal(clusterID))

			updated := &osacv1alpha1.ClusterOrder{}
			err = k8sClient.Get(testCtx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("When reconciling a resource being deleted with multiple finalizers", func() {
		BeforeEach(func() {
			co := &osacv1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: clusterOrderNS,
					Labels: map[string]string{
						osacClusterOrderIDLabel: clusterID,
					},
					Finalizers: []string{osacFinalizer, osacClusterOrderFeedbackFinalizer},
				},
				Spec: osacv1alpha1.ClusterOrderSpec{
					TemplateID: "test_template",
				},
			}
			Expect(k8sClient.Create(testCtx, co)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, co)).To(Succeed())
			co.Status.Phase = osacv1alpha1.ClusterOrderPhaseDeleting
			Expect(k8sClient.Status().Update(testCtx, co)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, co)).To(Succeed())
			Expect(k8sClient.Delete(testCtx, co)).To(Succeed())

			mockClient.getResponse = newClusterGetResponse()
			mockClient.updateResponse = &privatev1.ClustersUpdateResponse{}
		})

		AfterEach(func() {
			co := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, co)
			if err == nil {
				co.Finalizers = nil
				Expect(k8sClient.Update(testCtx, co)).To(Succeed())
			}
		})

		It("should sync state but NOT signal when other finalizers remain", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeFalse())

			updated := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Finalizers).To(ContainElement(osacClusterOrderFeedbackFinalizer))
		})
	})

	Context("When reconciling a resource being deleted without feedback finalizer", func() {
		BeforeEach(func() {
			co := &osacv1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: clusterOrderNS,
					Labels: map[string]string{
						osacClusterOrderIDLabel: clusterID,
					},
					Finalizers: []string{osacFinalizer},
				},
				Spec: osacv1alpha1.ClusterOrderSpec{
					TemplateID: "test_template",
				},
			}
			Expect(k8sClient.Create(testCtx, co)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, co)).To(Succeed())
			co.Status.Phase = osacv1alpha1.ClusterOrderPhaseDeleting
			Expect(k8sClient.Status().Update(testCtx, co)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, co)).To(Succeed())
			Expect(k8sClient.Delete(testCtx, co)).To(Succeed())

			mockClient.getResponse = newClusterGetResponse()
			mockClient.updateResponse = &privatev1.ClustersUpdateResponse{}
		})

		AfterEach(func() {
			co := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, co)
			if err == nil {
				co.Finalizers = nil
				Expect(k8sClient.Update(testCtx, co)).To(Succeed())
			}
		})

		It("should NOT signal when feedback finalizer is absent", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeFalse())
		})
	})

	Context("When reconciling a resource being deleted and fulfillment-service returns NotFound", func() {
		BeforeEach(func() {
			co := &osacv1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: clusterOrderNS,
					Labels: map[string]string{
						osacClusterOrderIDLabel: clusterID,
					},
					Finalizers: []string{osacClusterOrderFeedbackFinalizer},
				},
				Spec: osacv1alpha1.ClusterOrderSpec{
					TemplateID: "test_template",
				},
			}
			Expect(k8sClient.Create(testCtx, co)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, co)).To(Succeed())
			co.Status.Phase = osacv1alpha1.ClusterOrderPhaseDeleting
			Expect(k8sClient.Status().Update(testCtx, co)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, co)).To(Succeed())
			Expect(k8sClient.Delete(testCtx, co)).To(Succeed())

			mockClient.getError = grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", clusterID)
		})

		AfterEach(func() {
			co := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, co)
			if err == nil {
				co.Finalizers = nil
				Expect(k8sClient.Update(testCtx, co)).To(Succeed())
			}
		})

		It("should remove feedback finalizer and return without error", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeFalse())
			Expect(mockClient.signalCalled).To(BeFalse())

			updated := &osacv1alpha1.ClusterOrder{}
			err = k8sClient.Get(testCtx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("When fulfillment-service returns NotFound for a resource that is NOT being deleted", func() {
		BeforeEach(func() {
			co := &osacv1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: clusterOrderNS,
					Labels: map[string]string{
						osacClusterOrderIDLabel: clusterID,
					},
					Finalizers: []string{osacClusterOrderFeedbackFinalizer},
				},
				Spec: osacv1alpha1.ClusterOrderSpec{
					TemplateID: "test_template",
				},
			}
			Expect(k8sClient.Create(testCtx, co)).To(Succeed())

			mockClient.getError = grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", clusterID)
		})

		AfterEach(func() {
			co := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, co)
			if err == nil {
				co.Finalizers = nil
				_ = k8sClient.Update(testCtx, co)
				_ = k8sClient.Delete(testCtx, co)
			}
		})

		It("should propagate the NotFound error", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(testCtx, request)
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(codes.NotFound))
		})
	})

	Context("When reconciling a valid resource", func() {
		BeforeEach(func() {
			co := &osacv1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: clusterOrderNS,
					Labels: map[string]string{
						osacClusterOrderIDLabel: clusterID,
					},
				},
				Spec: osacv1alpha1.ClusterOrderSpec{
					TemplateID: "test_template",
				},
			}
			Expect(k8sClient.Create(testCtx, co)).To(Succeed())

			mockClient.getResponse = newClusterGetResponse()
			mockClient.updateResponse = &privatev1.ClustersUpdateResponse{}
		})

		AfterEach(func() {
			co := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, co)
			if err == nil {
				co.Finalizers = nil
				_ = k8sClient.Update(testCtx, co)
				_ = k8sClient.Delete(testCtx, co)
			}
		})

		It("should add feedback finalizer on first reconcile", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())

			updated := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Finalizers).To(ContainElement(osacClusterOrderFeedbackFinalizer))
		})

		It("should sync Progressing phase", func() {
			co := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, co)).To(Succeed())
			co.Status.Phase = osacv1alpha1.ClusterOrderPhaseProgressing
			Expect(k8sClient.Status().Update(testCtx, co)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ClusterState_CLUSTER_STATE_PROGRESSING))
		})

		It("should sync Failed phase", func() {
			co := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, co)).To(Succeed())
			co.Status.Phase = osacv1alpha1.ClusterOrderPhaseFailed
			Expect(k8sClient.Status().Update(testCtx, co)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ClusterState_CLUSTER_STATE_FAILED))
		})

		It("should not call update when reconciled twice with same data", func() {
			co := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, co)).To(Succeed())
			co.Status.Phase = osacv1alpha1.ClusterOrderPhaseProgressing
			Expect(k8sClient.Status().Update(testCtx, co)).To(Succeed())

			mockClient.getResponse.GetObject().GetStatus().SetState(privatev1.ClusterState_CLUSTER_STATE_PROGRESSING)

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeFalse())
		})
	})
})
