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
	"slices"
	"time"

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
			clusterOrder := &osacv1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: clusterOrderNS,
				},
				Spec: osacv1alpha1.ClusterOrderSpec{
					TemplateID: "test_template",
				},
			}
			Expect(k8sClient.Create(testCtx, clusterOrder)).To(Succeed())
		})

		AfterEach(func() {
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)
			if err == nil {
				clusterOrder.Finalizers = nil
				_ = k8sClient.Update(testCtx, clusterOrder)
				_ = k8sClient.Delete(testCtx, clusterOrder)
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
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			clusterOrder.Finalizers = []string{osacClusterOrderFeedbackFinalizer}
			Expect(k8sClient.Update(testCtx, clusterOrder)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			Expect(k8sClient.Delete(testCtx, clusterOrder)).To(Succeed())

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
			clusterOrder := &osacv1alpha1.ClusterOrder{
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
			Expect(k8sClient.Create(testCtx, clusterOrder)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			clusterOrder.Status.Phase = osacv1alpha1.ClusterOrderPhaseDeleting
			Expect(k8sClient.Status().Update(testCtx, clusterOrder)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			Expect(k8sClient.Delete(testCtx, clusterOrder)).To(Succeed())

			mockClient.getResponse = newClusterGetResponse()
			mockClient.updateResponse = &privatev1.ClustersUpdateResponse{}
		})

		AfterEach(func() {
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)
			if err == nil {
				clusterOrder.Finalizers = nil
				Expect(k8sClient.Update(testCtx, clusterOrder)).To(Succeed())
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
			clusterOrder := &osacv1alpha1.ClusterOrder{
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
			Expect(k8sClient.Create(testCtx, clusterOrder)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			clusterOrder.Status.Phase = osacv1alpha1.ClusterOrderPhaseProgressing
			Expect(k8sClient.Status().Update(testCtx, clusterOrder)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			Expect(k8sClient.Delete(testCtx, clusterOrder)).To(Succeed())

			mockClient.getResponse = newClusterGetResponse()
			mockClient.updateResponse = &privatev1.ClustersUpdateResponse{}
		})

		AfterEach(func() {
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)
			if err == nil {
				clusterOrder.Finalizers = nil
				Expect(k8sClient.Update(testCtx, clusterOrder)).To(Succeed())
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
			clusterOrder := &osacv1alpha1.ClusterOrder{
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
			Expect(k8sClient.Create(testCtx, clusterOrder)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			clusterOrder.Status.Phase = osacv1alpha1.ClusterOrderPhaseDeleting
			Expect(k8sClient.Status().Update(testCtx, clusterOrder)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			Expect(k8sClient.Delete(testCtx, clusterOrder)).To(Succeed())

			mockClient.getResponse = newClusterGetResponse()
			mockClient.updateResponse = &privatev1.ClustersUpdateResponse{}
		})

		AfterEach(func() {
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)
			if err == nil {
				clusterOrder.Finalizers = nil
				Expect(k8sClient.Update(testCtx, clusterOrder)).To(Succeed())
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
			clusterOrder := &osacv1alpha1.ClusterOrder{
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
			Expect(k8sClient.Create(testCtx, clusterOrder)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			clusterOrder.Status.Phase = osacv1alpha1.ClusterOrderPhaseDeleting
			Expect(k8sClient.Status().Update(testCtx, clusterOrder)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			Expect(k8sClient.Delete(testCtx, clusterOrder)).To(Succeed())

			mockClient.getResponse = newClusterGetResponse()
			mockClient.updateResponse = &privatev1.ClustersUpdateResponse{}
		})

		AfterEach(func() {
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)
			if err == nil {
				clusterOrder.Finalizers = nil
				Expect(k8sClient.Update(testCtx, clusterOrder)).To(Succeed())
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
			clusterOrder := &osacv1alpha1.ClusterOrder{
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
			Expect(k8sClient.Create(testCtx, clusterOrder)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			clusterOrder.Status.Phase = osacv1alpha1.ClusterOrderPhaseDeleting
			Expect(k8sClient.Status().Update(testCtx, clusterOrder)).To(Succeed())

			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			Expect(k8sClient.Delete(testCtx, clusterOrder)).To(Succeed())

			mockClient.getError = grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", clusterID)
		})

		AfterEach(func() {
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)
			if err == nil {
				clusterOrder.Finalizers = nil
				Expect(k8sClient.Update(testCtx, clusterOrder)).To(Succeed())
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
			clusterOrder := &osacv1alpha1.ClusterOrder{
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
			Expect(k8sClient.Create(testCtx, clusterOrder)).To(Succeed())

			mockClient.getError = grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", clusterID)
		})

		AfterEach(func() {
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)
			if err == nil {
				clusterOrder.Finalizers = nil
				_ = k8sClient.Update(testCtx, clusterOrder)
				_ = k8sClient.Delete(testCtx, clusterOrder)
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
			clusterOrder := &osacv1alpha1.ClusterOrder{
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
			Expect(k8sClient.Create(testCtx, clusterOrder)).To(Succeed())

			mockClient.getResponse = newClusterGetResponse()
			mockClient.updateResponse = &privatev1.ClustersUpdateResponse{}
		})

		AfterEach(func() {
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)
			if err == nil {
				clusterOrder.Finalizers = nil
				_ = k8sClient.Update(testCtx, clusterOrder)
				_ = k8sClient.Delete(testCtx, clusterOrder)
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
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			clusterOrder.Status.Phase = osacv1alpha1.ClusterOrderPhaseProgressing
			Expect(k8sClient.Status().Update(testCtx, clusterOrder)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ClusterState_CLUSTER_STATE_PROGRESSING))
		})

		It("should sync Failed phase", func() {
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			clusterOrder.Status.Phase = osacv1alpha1.ClusterOrderPhaseFailed
			Expect(k8sClient.Status().Update(testCtx, clusterOrder)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ClusterState_CLUSTER_STATE_FAILED))
		})

		It("should sync VIP endpoints to Cluster proto when set in status", func() {
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			clusterOrder.Status.ApiEndpoint = "10.0.0.1"
			clusterOrder.Status.IngressEndpoint = "10.0.0.2"
			Expect(k8sClient.Status().Update(testCtx, clusterOrder)).To(Succeed())

			request := reconcile.Request{NamespacedName: typeNamespacedName}
			_, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetApiEndpoint()).To(Equal("10.0.0.1"))
			Expect(mockClient.lastUpdate.GetStatus().GetIngressEndpoint()).To(Equal("10.0.0.2"))
		})

		It("should not sync VIP endpoints when status fields are empty", func() {
			request := reconcile.Request{NamespacedName: typeNamespacedName}
			_, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.lastUpdate.GetStatus().GetApiEndpoint()).To(BeEmpty())
			Expect(mockClient.lastUpdate.GetStatus().GetIngressEndpoint()).To(BeEmpty())
		})

		It("should not call update when reconciled twice with same data", func() {
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			clusterOrder.Status.Phase = osacv1alpha1.ClusterOrderPhaseProgressing
			Expect(k8sClient.Status().Update(testCtx, clusterOrder)).To(Succeed())

			mockClient.getResponse.GetObject().GetStatus().SetState(privatev1.ClusterState_CLUSTER_STATE_PROGRESSING)

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(testCtx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeFalse())
		})
	})

	Context("When syncing ClusterOrder conditions", func() {
		findRemoteCondition := func(condType privatev1.ClusterConditionType) *privatev1.ClusterCondition {
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == condType {
					return cond
				}
			}
			return nil
		}

		setCRCondition := func(condType string, status metav1.ConditionStatus, reason, message string) {
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			clusterOrder.Status.Conditions = append(clusterOrder.Status.Conditions, metav1.Condition{
				Type:               condType,
				Status:             status,
				Reason:             reason,
				Message:            message,
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(testCtx, clusterOrder)).To(Succeed())
		}

		reconcileOnce := func() {
			_, err := reconciler.Reconcile(testCtx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
		}

		BeforeEach(func() {
			clusterOrder := &osacv1alpha1.ClusterOrder{
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
			Expect(k8sClient.Create(testCtx, clusterOrder)).To(Succeed())

			mockClient.getResponse = newClusterGetResponse()
			mockClient.updateResponse = &privatev1.ClustersUpdateResponse{}
		})

		AfterEach(func() {
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			err := k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)
			if err == nil {
				clusterOrder.Finalizers = nil
				_ = k8sClient.Update(testCtx, clusterOrder)
				_ = k8sClient.Delete(testCtx, clusterOrder)
			}
		})

		It("should map ClusterAvailable to READY with reason preserved (latent bug 1)", func() {
			setCRCondition(osacv1alpha1.ConditionClusterAvailable, metav1.ConditionTrue,
				osacv1alpha1.ReasonAsExpected, "cluster is available")

			reconcileOnce()
			Expect(mockClient.updateCalled).To(BeTrue())

			ready := findRemoteCondition(privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY)
			Expect(ready).NotTo(BeNil())
			Expect(ready.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
			Expect(ready.GetReason()).To(Equal(osacv1alpha1.ReasonAsExpected))
			Expect(ready.GetMessage()).To(Equal("cluster is available"))
			Expect(findRemoteCondition(privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING)).To(BeNil())
		})

		It("should take PROGRESSING status from Progressing and reason/message from the furthest stage (Accepted)", func() {
			// The resource controller sets Accepted and Progressing together. PROGRESSING's
			// status must come from Progressing (the installation-status condition), while its
			// reason/message reflect the furthest installation stage reached - here Accepted.
			// Accepted must not appear as its own fulfillment condition.
			setCRCondition(osacv1alpha1.ConditionAccepted, metav1.ConditionTrue,
				osacv1alpha1.ReasonInitialized, "order accepted")
			setCRCondition(osacv1alpha1.ConditionProgressing, metav1.ConditionTrue,
				osacv1alpha1.ReasonProgressing, "installing")

			reconcileOnce()
			Expect(mockClient.updateCalled).To(BeTrue())

			progressing := findRemoteCondition(privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
			// Furthest stage reached is Accepted, so it drives PROGRESSING's reason/message.
			Expect(progressing.GetReason()).To(Equal(osacv1alpha1.ConditionAccepted))
			Expect(progressing.GetMessage()).To(Equal("Accepted"))
			// Only PROGRESSING is produced; Accepted does not become its own condition.
			Expect(mockClient.lastUpdate.GetStatus().GetConditions()).To(HaveLen(1))
		})

		It("should map Progressing to PROGRESSING preserving False status, reason and message", func() {
			setCRCondition(osacv1alpha1.ConditionProgressing, metav1.ConditionFalse,
				osacv1alpha1.ReasonProvisioningFailed, "provisioning failed")

			reconcileOnce()
			Expect(mockClient.updateCalled).To(BeTrue())

			progressing := findRemoteCondition(privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
			Expect(progressing.GetReason()).To(Equal(osacv1alpha1.ReasonProvisioningFailed))
			Expect(progressing.GetMessage()).To(Equal("provisioning failed"))
		})

		It("should fold installation-step conditions into PROGRESSING's reason/message without changing its status or adding conditions", func() {
			// ControlPlaneCreated and ClusterStorageReady are individual installation steps.
			// They are recognised (never logged as unknown) and refine PROGRESSING's
			// reason/message to the furthest step reached, but must not change PROGRESSING's
			// status or become their own fulfillment conditions.
			setCRCondition(osacv1alpha1.ConditionProgressing, metav1.ConditionTrue,
				osacv1alpha1.ReasonProgressing, "installing")
			setCRCondition(osacv1alpha1.ConditionControlPlaneCreated, metav1.ConditionTrue,
				osacv1alpha1.ReasonAsExpected, "control plane created")
			setCRCondition(string(osacv1alpha1.ClusterOrderConditionClusterStorageReady), metav1.ConditionTrue,
				osacv1alpha1.TenantReasonFound, "storage discovered")

			reconcileOnce()
			Expect(mockClient.updateCalled).To(BeTrue())

			progressing := findRemoteCondition(privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
			// ClusterStorageReady is the furthest stage reached, so it drives reason/message.
			Expect(progressing.GetReason()).To(Equal(string(osacv1alpha1.ClusterOrderConditionClusterStorageReady)))
			Expect(progressing.GetMessage()).To(Equal("Cluster Storage Ready"))
			// Only PROGRESSING is produced; the installation steps do not add their own conditions.
			Expect(mockClient.lastUpdate.GetStatus().GetConditions()).To(HaveLen(1))
		})

		It("should pick the furthest stage for PROGRESSING reason regardless of condition order", func() {
			// Accepted and ControlPlaneCreated are True; ClusterStorageReady is not. The
			// furthest stage is ControlPlaneCreated, selected from the fixed stage order, not
			// from the order the conditions happen to appear in the CR status (here reversed:
			// ControlPlaneCreated is appended before the earlier Accepted stage).
			setCRCondition(osacv1alpha1.ConditionProgressing, metav1.ConditionTrue,
				osacv1alpha1.ReasonProgressing, "installing")
			setCRCondition(osacv1alpha1.ConditionControlPlaneCreated, metav1.ConditionTrue,
				osacv1alpha1.ReasonAsExpected, "control plane created")
			setCRCondition(osacv1alpha1.ConditionAccepted, metav1.ConditionTrue,
				osacv1alpha1.ReasonInitialized, "order accepted")

			reconcileOnce()

			progressing := findRemoteCondition(privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.GetReason()).To(Equal(osacv1alpha1.ConditionControlPlaneCreated))
			Expect(progressing.GetMessage()).To(Equal("Control Plane Created"))
		})

		It("should keep PROGRESSING's own reason/message when it is True but no installation stage is reached yet", func() {
			// Progressing is True but none of the installation-step conditions is set
			// (furthest stage is empty). The overlay must leave PROGRESSING's reason/message
			// as copied from the Progressing condition itself, not blank them or panic.
			setCRCondition(osacv1alpha1.ConditionProgressing, metav1.ConditionTrue,
				osacv1alpha1.ReasonProgressing, "installing")

			reconcileOnce()
			Expect(mockClient.updateCalled).To(BeTrue())

			progressing := findRemoteCondition(privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
			// No stage reached, so reason/message stay as the Progressing condition set them.
			Expect(progressing.GetReason()).To(Equal(osacv1alpha1.ReasonProgressing))
			Expect(progressing.GetMessage()).To(Equal("installing"))
			Expect(mockClient.lastUpdate.GetStatus().GetConditions()).To(HaveLen(1))
		})

		It("should not create a PROGRESSING condition from a stage when Progressing itself is absent", func() {
			// A stage condition is True but the Progressing condition is missing. The overlay
			// only refines an existing PROGRESSING condition; it must not fabricate one from a
			// stage. Phase forces an update so the (empty) conditions list can be asserted.
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			clusterOrder.Status.Phase = osacv1alpha1.ClusterOrderPhaseProgressing
			clusterOrder.Status.Conditions = append(clusterOrder.Status.Conditions, metav1.Condition{
				Type:               osacv1alpha1.ConditionControlPlaneCreated,
				Status:             metav1.ConditionTrue,
				Reason:             osacv1alpha1.ReasonAsExpected,
				Message:            "control plane created",
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(testCtx, clusterOrder)).To(Succeed())

			reconcileOnce()
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ClusterState_CLUSTER_STATE_PROGRESSING))
			// The stage neither becomes its own condition nor conjures a PROGRESSING one.
			Expect(findRemoteCondition(privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING)).To(BeNil())
			Expect(mockClient.lastUpdate.GetStatus().GetConditions()).To(BeEmpty())
		})

		It("should explicitly ignore NamespaceCreated without producing a proto condition", func() {
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			clusterOrder.Status.Phase = osacv1alpha1.ClusterOrderPhaseProgressing
			clusterOrder.Status.Conditions = append(clusterOrder.Status.Conditions, metav1.Condition{
				Type:               osacv1alpha1.ConditionNamespaceCreated,
				Status:             metav1.ConditionTrue,
				Reason:             osacv1alpha1.ReasonAsExpected,
				Message:            "namespace created",
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(testCtx, clusterOrder)).To(Succeed())

			reconcileOnce()
			// Phase forces an update so we can assert the conditions list, and confirm
			// the ignored condition did not silently create a proto condition.
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ClusterState_CLUSTER_STATE_PROGRESSING))
			Expect(mockClient.lastUpdate.GetStatus().GetConditions()).To(BeEmpty())
		})

		It("should not surface an unmapped, non-ignored condition as a proto condition (no silent default)", func() {
			clusterOrder := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
			clusterOrder.Status.Phase = osacv1alpha1.ClusterOrderPhaseProgressing
			clusterOrder.Status.Conditions = append(clusterOrder.Status.Conditions, metav1.Condition{
				Type:               "SomeFutureCondition",
				Status:             metav1.ConditionTrue,
				Reason:             osacv1alpha1.ReasonAsExpected,
				Message:            "a condition with no mapping yet",
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(testCtx, clusterOrder)).To(Succeed())

			reconcileOnce()
			// Phase forces an update so we can assert the conditions list: an unknown
			// condition is logged and dropped, never silently mapped to a proto condition.
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ClusterState_CLUSTER_STATE_PROGRESSING))
			Expect(mockClient.lastUpdate.GetStatus().GetConditions()).To(BeEmpty())
		})

		It("should fill PROGRESSING from Progressing regardless of condition order and not invert once the cluster is available", func() {
			// A finished cluster: Progressing is False, every installation step is True, and
			// the cluster is Available. PROGRESSING must follow Progressing (False) - a
			// completed step must not report the cluster as still installing - and the result
			// must be the same no matter what order the conditions are stored in.
			buildConditions := func() []metav1.Condition {
				now := metav1.NewTime(time.Now().UTC())
				return []metav1.Condition{
					{Type: osacv1alpha1.ConditionAccepted, Status: metav1.ConditionTrue, Reason: osacv1alpha1.ReasonInitialized, Message: "accepted", LastTransitionTime: now},
					{Type: osacv1alpha1.ConditionProgressing, Status: metav1.ConditionFalse, Reason: osacv1alpha1.ReasonAsExpected, Message: "installation complete", LastTransitionTime: now},
					{Type: osacv1alpha1.ConditionControlPlaneCreated, Status: metav1.ConditionTrue, Reason: osacv1alpha1.ReasonAsExpected, Message: "control plane created", LastTransitionTime: now},
					{Type: osacv1alpha1.ConditionControlPlaneAvailable, Status: metav1.ConditionTrue, Reason: osacv1alpha1.ReasonAsExpected, Message: "control plane available", LastTransitionTime: now},
					{Type: string(osacv1alpha1.ClusterOrderConditionClusterStorageReady), Status: metav1.ConditionTrue, Reason: osacv1alpha1.TenantReasonFound, Message: "storage ready", LastTransitionTime: now},
					{Type: osacv1alpha1.ConditionClusterAvailable, Status: metav1.ConditionTrue, Reason: osacv1alpha1.ReasonAsExpected, Message: "cluster available", LastTransitionTime: now},
					{Type: osacv1alpha1.ConditionNamespaceCreated, Status: metav1.ConditionTrue, Reason: osacv1alpha1.ReasonAsExpected, Message: "namespace created", LastTransitionTime: now},
				}
			}

			setConditions := func(conditions []metav1.Condition) {
				clusterOrder := &osacv1alpha1.ClusterOrder{}
				Expect(k8sClient.Get(testCtx, typeNamespacedName, clusterOrder)).To(Succeed())
				clusterOrder.Status.Conditions = conditions
				Expect(k8sClient.Status().Update(testCtx, clusterOrder)).To(Succeed())
			}

			assertResult := func() {
				// Two fulfillment conditions only: PROGRESSING and READY. The installation
				// steps do not add their own conditions and NamespaceCreated is ignored.
				Expect(mockClient.lastUpdate.GetStatus().GetConditions()).To(HaveLen(2))

				ready := findRemoteCondition(privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY)
				Expect(ready).NotTo(BeNil())
				Expect(ready.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))

				progressing := findRemoteCondition(privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING)
				Expect(progressing).NotTo(BeNil())
				// Follows Progressing (False); a completed step never flips it back to True.
				Expect(progressing.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
				Expect(progressing.GetMessage()).To(Equal("installation complete"))
			}

			// Stored order.
			setConditions(buildConditions())
			reconcileOnce()
			Expect(mockClient.updateCalled).To(BeTrue())
			assertResult()

			// Reversed order - the fulfillment result must be identical. Reset the remote so
			// the second reconcile rebuilds the conditions from an empty cluster.
			reversed := buildConditions()
			slices.Reverse(reversed)
			mockClient.getResponse = newClusterGetResponse()
			mockClient.updateCalled = false
			mockClient.lastUpdate = nil
			setConditions(reversed)
			reconcileOnce()
			Expect(mockClient.updateCalled).To(BeTrue())
			assertResult()
		})
	})

	Context("ClusterOrder condition mapping completeness", func() {
		// Tripwire: every ClusterOrder condition must be handled by exactly one of the three
		// dispositions in feedback_controller.go - mapped to a fulfillment condition, listed
		// as a provisioning stage (refines PROGRESSING's reason/message), or explicitly
		// unsurfaced. When a new ClusterOrder condition is added to the API, add it here and
		// wire it into one of the three - otherwise this test fails, instead of the condition
		// being silently dropped at runtime.
		knownClusterOrderConditions := []string{
			osacv1alpha1.ConditionAccepted,
			osacv1alpha1.ConditionNamespaceCreated,
			osacv1alpha1.ConditionControlPlaneCreated,
			osacv1alpha1.ConditionControlPlaneAvailable,
			osacv1alpha1.ConditionClusterAvailable,
			string(osacv1alpha1.ClusterOrderConditionClusterStorageReady),
			osacv1alpha1.ConditionProgressing,
			osacv1alpha1.ConditionDeleting,
		}

		It("handles every known ClusterOrder condition exactly once", func() {
			for _, condition := range knownClusterOrderConditions {
				_, mapped := clusterOrderConditionMappings[condition]
				stage := slices.Contains(clusterOrderProvisioningStages, condition)
				_, unsurfaced := clusterOrderUnsurfacedConditions[condition]

				handledCount := 0
				for _, handled := range []bool{mapped, stage, unsurfaced} {
					if handled {
						handledCount++
					}
				}
				Expect(handledCount).To(Equal(1),
					"condition %q must be handled by exactly one of: mapping, provisioning stage, unsurfaced (got %d)",
					condition, handledCount)
			}
		})

		It("does not reference any unknown ClusterOrder condition", func() {
			known := map[string]struct{}{}
			for _, condition := range knownClusterOrderConditions {
				known[condition] = struct{}{}
			}
			for condition := range clusterOrderConditionMappings {
				_, ok := known[condition]
				Expect(ok).To(BeTrue(), "mapped condition %q is not a known ClusterOrder condition", condition)
			}
			for _, condition := range clusterOrderProvisioningStages {
				_, ok := known[condition]
				Expect(ok).To(BeTrue(), "provisioning-stage condition %q is not a known ClusterOrder condition", condition)
			}
			for condition := range clusterOrderUnsurfacedConditions {
				_, ok := known[condition]
				Expect(ok).To(BeTrue(), "unsurfaced condition %q is not a known ClusterOrder condition", condition)
			}
		})
	})
})

var _ = Describe("humanizeConditionName", func() {
	DescribeTable("splits a PascalCase condition name into words",
		func(name, expected string) {
			Expect(humanizeConditionName(name)).To(Equal(expected))
		},
		Entry("single word", "Ready", "Ready"),
		Entry("two words", "ClusterStorageReady", "Cluster Storage Ready"),
		Entry("three words", "ControlPlaneCreated", "Control Plane Created"),
		Entry("leading acronym", "CSIDriverReady", "CSI Driver Ready"),
		Entry("trailing acronym", "EnableTLS", "Enable TLS"),
		Entry("acronym-only", "TLS", "TLS"),
		Entry("acronym then word", "TLSReady", "TLS Ready"),
		Entry("empty string", "", ""),
	)
})
