/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
)

// mockComputeInstancesClient is a mock implementation of ComputeInstancesClient for testing.
type mockComputeInstancesClient struct {
	getResponse    *privatev1.ComputeInstancesGetResponse
	getError       error
	updateResponse *privatev1.ComputeInstancesUpdateResponse
	updateError    error
	updateCalled   bool
	updateCount    int
	lastUpdate     *privatev1.ComputeInstance
	signalCalled   bool
	signalCount    int
	signalID       string
	signalError    error
}

func (m *mockComputeInstancesClient) List(ctx context.Context, in *privatev1.ComputeInstancesListRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesListResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockComputeInstancesClient) Get(ctx context.Context, in *privatev1.ComputeInstancesGetRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesGetResponse, error) {
	if m.getError != nil {
		return nil, m.getError
	}
	return m.getResponse, nil
}

func (m *mockComputeInstancesClient) Create(ctx context.Context, in *privatev1.ComputeInstancesCreateRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesCreateResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockComputeInstancesClient) Delete(ctx context.Context, in *privatev1.ComputeInstancesDeleteRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesDeleteResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockComputeInstancesClient) Update(ctx context.Context, in *privatev1.ComputeInstancesUpdateRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesUpdateResponse, error) {
	m.updateCalled = true
	m.updateCount++
	m.lastUpdate = in.GetObject()
	if m.updateError != nil {
		return nil, m.updateError
	}
	return m.updateResponse, nil
}

func (m *mockComputeInstancesClient) Signal(ctx context.Context, in *privatev1.ComputeInstancesSignalRequest, opts ...grpc.CallOption) (*privatev1.ComputeInstancesSignalResponse, error) {
	m.signalCalled = true
	m.signalCount++
	m.signalID = in.GetId()
	if m.signalError != nil {
		return nil, m.signalError
	}
	return &privatev1.ComputeInstancesSignalResponse{}, nil
}

var _ = Describe("ComputeInstanceFeedbackReconciler", func() {
	const (
		resourceName      = "test-ci"
		vmNamespace       = "default"
		ciID              = "test-ci-id"
		computeInstanceNS = "osac-computeinstance"
	)

	var (
		ctx                context.Context
		typeNamespacedName types.NamespacedName
		mockClient         *mockComputeInstancesClient
		reconciler         *ComputeInstanceFeedbackReconciler
	)

	BeforeEach(func() {
		ctx = context.Background()
		typeNamespacedName = types.NamespacedName{
			Name:      resourceName,
			Namespace: computeInstanceNS,
		}
		mockClient = &mockComputeInstancesClient{}
		reconciler = &ComputeInstanceFeedbackReconciler{
			bridge:                   newComputeInstanceFeedbackBridge(k8sClient, mockClient),
			computeInstanceNamespace: computeInstanceNS,
		}

		// Create the namespace if it doesn't exist
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: computeInstanceNS,
			},
		}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: computeInstanceNS}, namespace)
		if err != nil && apierrors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		}
	})

	Context("When reconciling a resource that doesn't exist", func() {
		It("should return without error and not signal", func() {
			request := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "non-existent",
					Namespace: computeInstanceNS,
				},
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeFalse())
			Expect(mockClient.signalCalled).To(BeFalse())
		})
	})

	Context("When reconciling a resource without the VM ID label", func() {
		BeforeEach(func() {
			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: computeInstanceNS,
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())
		})

		AfterEach(func() {
			vm := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, vm)
			if err == nil {
				vm.Finalizers = nil
				_ = k8sClient.Update(ctx, vm)
				_ = k8sClient.Delete(ctx, vm)
			}
		})

		It("should skip reconciliation", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeFalse())
		})

		It("should remove feedback finalizer from CR without CI ID label being deleted", func() {
			// Add the feedback finalizer and trigger deletion
			vm := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Finalizers = []string{osacComputeInstanceFeedbackFinalizer}
			Expect(k8sClient.Update(ctx, vm)).To(Succeed())

			// Delete triggers DeletionTimestamp
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			Expect(k8sClient.Delete(ctx, vm)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())

			// Verify CR is gone (finalizer removed, GC proceeded)
			updated := &osacv1alpha1.ComputeInstance{}
			err = k8sClient.Get(ctx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeFalse())
		})
	})

	Context("When reconciling a resource that is being deleted", func() {
		BeforeEach(func() {
			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: computeInstanceNS,
					Labels: map[string]string{
						osacComputeInstanceIDLabel: ciID,
					},
					Finalizers: []string{osacComputeInstanceFeedbackFinalizer},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())

			// Set the phase to Deleting (as the main controller would do in handleDelete)
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseDeleting
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			// Delete the CR (sets DeletionTimestamp, CR stays because of finalizer)
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			Expect(k8sClient.Delete(ctx, vm)).To(Succeed())

			mockClient.getResponse = &privatev1.ComputeInstancesGetResponse{
				Object: &privatev1.ComputeInstance{
					Id:   ciID,
					Spec: &privatev1.ComputeInstanceSpec{},
					Status: &privatev1.ComputeInstanceStatus{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
					},
				},
			}
			mockClient.updateResponse = &privatev1.ComputeInstancesUpdateResponse{}
		})

		AfterEach(func() {
			vm := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, vm)
			if err == nil {
				// Force delete by removing finalizers
				vm.Finalizers = nil
				Expect(k8sClient.Update(ctx, vm)).To(Succeed())
			}
		})

		It("should sync Deleting state to fulfillment service", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate).NotTo(BeNil())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING))
		})

		It("should signal and remove finalizer when it's the last one", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeTrue())
			Expect(mockClient.signalID).To(Equal(ciID))

			// CR should be gone (finalizer removed, GC proceeds)
			updated := &osacv1alpha1.ComputeInstance{}
			err = k8sClient.Get(ctx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should still remove finalizer when Signal fails", func() {
			mockClient.signalError = errors.New("already archived")

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeTrue())

			// Finalizer should still be removed despite Signal error
			updated := &osacv1alpha1.ComputeInstance{}
			err = k8sClient.Get(ctx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

	})

	Context("When reconciling a resource being deleted with multiple finalizers", func() {
		const deletingResourceName = "test-ci-multi-fin"

		var deletingNamespacedName types.NamespacedName

		BeforeEach(func() {
			deletingNamespacedName = types.NamespacedName{
				Name:      deletingResourceName,
				Namespace: computeInstanceNS,
			}

			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deletingResourceName,
					Namespace: computeInstanceNS,
					Labels: map[string]string{
						osacComputeInstanceIDLabel: ciID,
					},
					Finalizers: []string{osacComputeInstanceFinalizer, osacComputeInstanceFeedbackFinalizer},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())

			Expect(k8sClient.Get(ctx, deletingNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseDeleting
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			Expect(k8sClient.Get(ctx, deletingNamespacedName, vm)).To(Succeed())
			Expect(k8sClient.Delete(ctx, vm)).To(Succeed())

			mockClient.getResponse = &privatev1.ComputeInstancesGetResponse{
				Object: &privatev1.ComputeInstance{
					Id:   ciID,
					Spec: &privatev1.ComputeInstanceSpec{},
					Status: &privatev1.ComputeInstanceStatus{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
					},
				},
			}
			mockClient.updateResponse = &privatev1.ComputeInstancesUpdateResponse{}
		})

		AfterEach(func() {
			vm := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(ctx, deletingNamespacedName, vm)
			if err == nil {
				vm.Finalizers = nil
				Expect(k8sClient.Update(ctx, vm)).To(Succeed())
			}
		})

		It("should not signal when other finalizers are still present", func() {
			request := reconcile.Request{
				NamespacedName: deletingNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			// Should sync state but NOT signal
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeFalse())

			// Finalizer should still be present
			updated := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, deletingNamespacedName, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacComputeInstanceFeedbackFinalizer)).To(BeTrue())
		})
	})

	Context("When reconciling a resource being deleted without feedback finalizer", func() {
		const deletingResourceName = "test-ci-no-fb-fin"

		var deletingNamespacedName types.NamespacedName

		BeforeEach(func() {
			deletingNamespacedName = types.NamespacedName{
				Name:      deletingResourceName,
				Namespace: computeInstanceNS,
			}

			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deletingResourceName,
					Namespace: computeInstanceNS,
					Labels: map[string]string{
						osacComputeInstanceIDLabel: ciID,
					},
					Finalizers: []string{osacComputeInstanceFinalizer},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())

			Expect(k8sClient.Get(ctx, deletingNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseDeleting
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			Expect(k8sClient.Get(ctx, deletingNamespacedName, vm)).To(Succeed())
			Expect(k8sClient.Delete(ctx, vm)).To(Succeed())

			mockClient.getResponse = &privatev1.ComputeInstancesGetResponse{
				Object: &privatev1.ComputeInstance{
					Id:   ciID,
					Spec: &privatev1.ComputeInstanceSpec{},
					Status: &privatev1.ComputeInstanceStatus{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
					},
				},
			}
			mockClient.updateResponse = &privatev1.ComputeInstancesUpdateResponse{}
		})

		AfterEach(func() {
			vm := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(ctx, deletingNamespacedName, vm)
			if err == nil {
				vm.Finalizers = nil
				Expect(k8sClient.Update(ctx, vm)).To(Succeed())
			}
		})

		It("should not add finalizer to CR being deleted without feedback finalizer", func() {
			request := reconcile.Request{
				NamespacedName: deletingNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())

			// Verify feedback finalizer was NOT added
			updated := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, deletingNamespacedName, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacComputeInstanceFeedbackFinalizer)).To(BeFalse())
			// Signal should NOT be called (our finalizer isn't present)
			Expect(mockClient.signalCalled).To(BeFalse())
		})
	})

	Context("When reconciling a resource being deleted and fulfillment-service returns NotFound", func() {
		BeforeEach(func() {
			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: computeInstanceNS,
					Labels: map[string]string{
						osacComputeInstanceIDLabel: ciID,
					},
					Finalizers: []string{osacComputeInstanceFeedbackFinalizer},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())

			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseDeleting
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			Expect(k8sClient.Delete(ctx, vm)).To(Succeed())

			mockClient.getError = grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", ciID)
		})

		AfterEach(func() {
			vm := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, vm)
			if err == nil {
				vm.Finalizers = nil
				Expect(k8sClient.Update(ctx, vm)).To(Succeed())
			}
		})

		It("should remove feedback finalizer and return without error", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeFalse())
			Expect(mockClient.signalCalled).To(BeFalse())

			updated := &osacv1alpha1.ComputeInstance{}
			err = k8sClient.Get(ctx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("When fulfillment-service returns NotFound for a resource that is NOT being deleted", func() {
		BeforeEach(func() {
			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: computeInstanceNS,
					Labels: map[string]string{
						osacComputeInstanceIDLabel: ciID,
					},
					Finalizers: []string{osacComputeInstanceFeedbackFinalizer},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())

			mockClient.getError = grpcstatus.Errorf(codes.NotFound, "object with identifier '%s' not found", ciID)
		})

		AfterEach(func() {
			vm := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, vm)
			if err == nil {
				vm.Finalizers = nil
				_ = k8sClient.Update(ctx, vm)
				_ = k8sClient.Delete(ctx, vm)
			}
		})

		It("should propagate the NotFound error", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(codes.NotFound))
		})
	})

	Context("When reconciling a valid resource", func() {
		BeforeEach(func() {
			// Reset mock client state
			mockClient.updateCalled = false
			mockClient.lastUpdate = nil

			vm := &osacv1alpha1.ComputeInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: computeInstanceNS,
					Labels: map[string]string{
						osacComputeInstanceIDLabel: ciID,
					},
				},
				Spec: newTestComputeInstanceSpec("test_template"),
			}
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())
			// Update status separately since Status is a subresource - need to get fresh copy
			err := k8sClient.Get(ctx, typeNamespacedName, vm)
			Expect(err).NotTo(HaveOccurred())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseRunning
			vm.Status.Conditions = []metav1.Condition{
				{
					Type:               string(osacv1alpha1.ComputeInstanceConditionConfigurationApplied),
					Status:             metav1.ConditionFalse,
					Reason:             "AsExpected",
					Message:            "Applying configuration",
					LastTransitionTime: metav1.NewTime(time.Now().UTC()),
				},
			}
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			// Setup mock response
			mockClient.getResponse = &privatev1.ComputeInstancesGetResponse{
				Object: &privatev1.ComputeInstance{
					Id:   ciID,
					Spec: &privatev1.ComputeInstanceSpec{},
					Status: &privatev1.ComputeInstanceStatus{
						State: privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED,
					},
				},
			}
			mockClient.updateResponse = &privatev1.ComputeInstancesUpdateResponse{}
		})

		AfterEach(func() {
			vm := &osacv1alpha1.ComputeInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, vm)
			if err == nil {
				// Remove finalizers so the CR can be deleted
				vm.Finalizers = nil
				_ = k8sClient.Update(ctx, vm)
				Expect(k8sClient.Delete(ctx, vm)).To(Succeed())
			}
		})

		It("should add feedback finalizer on first reconcile", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())

			// Verify feedback finalizer was added
			updated := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacComputeInstanceFeedbackFinalizer)).To(BeTrue())
		})

		It("should successfully sync conditions and phase", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate).NotTo(BeNil())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))
		})

		It("should sync ConfigurationApplied condition", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			// Check that the condition was synced
			vm := mockClient.lastUpdate
			found := false
			for _, cond := range vm.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_CONFIGURATION_APPLIED {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
					Expect(cond.GetMessage()).To(Equal("Applying configuration"))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync Starting phase", func() {
			vm := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseStarting
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING))
		})

		It("should sync Failed phase", func() {
			vm := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseFailed
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED))
		})

		It("should sync Deleting phase", func() {
			vm := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseDeleting
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING))
		})

		It("should update only once when reconciliation is run twice with same data", func() {
			// Reset update count
			mockClient.updateCount = 0
			mockClient.updateCalled = false

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}

			// First reconciliation - should trigger an update
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.updateCount).To(Equal(1))
			Expect(mockClient.updateCalled).To(BeTrue())

			// Second reconciliation with same data - should NOT trigger another update
			// because the VM object in the fulfillment service now matches what we're trying to sync
			// We need to update the mock's getResponse to reflect the state after the first update
			mockClient.getResponse = &privatev1.ComputeInstancesGetResponse{
				Object: mockClient.lastUpdate,
			}

			// Run reconciliation again
			result, err = reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			// Update count should still be 1, not 2, because only the timestamp changed, not the status
			Expect(mockClient.updateCount).To(Equal(1))
		})

		It("should sync lastRestartedAt when set in K8s CR", func() {
			// Get the ComputeInstance and update its status with lastRestartedAt
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())

			// Use time without nanoseconds to match protobuf precision
			restartTime := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
			computeInstance.Status.LastRestartedAt = &restartTime
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate).NotTo(BeNil())

			// Verify lastRestartedAt was synced
			Expect(mockClient.lastUpdate.GetStatus().HasLastRestartedAt()).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetLastRestartedAt().AsTime()).To(Equal(restartTime.Time))
		})

		It("should not set lastRestartedAt when nil in K8s CR", func() {
			// Get the ComputeInstance - lastRestartedAt should be nil by default
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			Expect(computeInstance.Status.LastRestartedAt).To(BeNil())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate).NotTo(BeNil())

			// Verify lastRestartedAt was NOT set (still using default from mock)
			Expect(mockClient.lastUpdate.GetStatus().HasLastRestartedAt()).To(BeFalse())
		})

		It("should sync RestartInProgress condition when set to True", func() {
			// Get the ComputeInstance and add RestartInProgress condition
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())

			restartInProgressMessage := "Restart initiated at 2026-02-01T10:20:58Z"
			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionRestartInProgress),
				Status:             metav1.ConditionTrue,
				Reason:             "RestartInProgress",
				Message:            restartInProgressMessage,
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			// Verify RestartInProgress condition was synced
			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_IN_PROGRESS {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
					Expect(cond.GetMessage()).To(Equal(restartInProgressMessage))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync RestartFailed condition when set to True", func() {
			// Get the ComputeInstance and add RestartFailed condition
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())

			restartFailedMessage := "No VirtualMachine reference found"
			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionRestartFailed),
				Status:             metav1.ConditionTrue,
				Reason:             "NoVMReference",
				Message:            restartFailedMessage,
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			// Verify RestartFailed condition was synced
			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_FAILED {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
					Expect(cond.GetMessage()).To(Equal(restartFailedMessage))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync Stopping phase", func() {
			vm := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseStopping
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING))
		})

		It("should sync Stopped phase", func() {
			vm := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhaseStopped
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED))
		})

		It("should sync Paused phase", func() {
			vm := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, vm)).To(Succeed())
			vm.Status.Phase = osacv1alpha1.ComputeInstancePhasePaused
			Expect(k8sClient.Status().Update(ctx, vm)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetState()).To(Equal(privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED))
		})

		It("should sync Provisioned condition when True", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())

			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionProvisioned),
				Status:             metav1.ConditionTrue,
				Reason:             osacv1alpha1.ReasonInfrastructureReady,
				Message:            "All infrastructure resources provisioned successfully",
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
					Expect(cond.GetReason()).To(Equal(osacv1alpha1.ReasonInfrastructureReady))
					Expect(cond.GetMessage()).To(Equal("All infrastructure resources provisioned successfully"))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync Provisioned condition when False", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionProvisioned),
				Status:             metav1.ConditionFalse,
				Reason:             osacv1alpha1.ReasonProvisioningStorage,
				Message:            "Creating DataVolumes for boot disk (20GiB)",
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
					Expect(cond.GetMessage()).To(Equal("Creating DataVolumes for boot disk (20GiB)"))
					Expect(cond.GetReason()).To(Equal(osacv1alpha1.ReasonProvisioningStorage))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync Ready condition with Reason", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionReady),
				Status:             metav1.ConditionTrue,
				Reason:             osacv1alpha1.ReasonAsExpected,
				Message:            "",
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_READY {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
					Expect(cond.GetReason()).To(Equal(osacv1alpha1.ReasonAsExpected))
					Expect(cond.GetMessage()).To(Equal(""))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync Ready condition when False with reason and message", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			msg := "VirtualMachine not yet created, waiting for provisioning"
			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionReady),
				Status:             metav1.ConditionFalse,
				Reason:             osacv1alpha1.ReasonWaitingForVM,
				Message:            msg,
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_READY {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
					Expect(cond.GetReason()).To(Equal(osacv1alpha1.ReasonWaitingForVM))
					Expect(cond.GetMessage()).To(Equal(msg))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync RestartRequired condition when True", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionRestartRequired),
				Status:             metav1.ConditionTrue,
				Reason:             "AsExpected",
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_REQUIRED {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync RestartRequired condition when False", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.Conditions = append(computeInstance.Status.Conditions, metav1.Condition{
				Type:               string(osacv1alpha1.ComputeInstanceConditionRestartRequired),
				Status:             metav1.ConditionFalse,
				Reason:             "AsExpected",
				LastTransitionTime: metav1.NewTime(time.Now().UTC()),
			})
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())

			found := false
			for _, cond := range mockClient.lastUpdate.GetStatus().GetConditions() {
				if cond.GetType() == privatev1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_REQUIRED {
					Expect(cond.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_FALSE))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())
		})

		It("should sync external IP address from status", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.ExternalIPAddress = "10.0.0.100"
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetExternalIpAddress()).To(Equal("10.0.0.100"))
		})

		It("should sync internal IP address from CR status", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.IPAddress = "192.168.1.50"
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetInternalIpAddress()).To(Equal("192.168.1.50"))
		})

		It("should sync both external and internal IP addresses when both are set", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.ExternalIPAddress = "10.0.0.100"
			computeInstance.Status.IPAddress = "192.168.1.50"
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetExternalIpAddress()).To(Equal("10.0.0.100"))
			Expect(mockClient.lastUpdate.GetStatus().GetInternalIpAddress()).To(Equal("192.168.1.50"))
		})

		It("should clear IP addresses when neither external IP status nor internal IP is set", func() {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetExternalIpAddress()).To(BeEmpty())
			Expect(mockClient.lastUpdate.GetStatus().GetInternalIpAddress()).To(BeEmpty())
		})

		It("should clear external IP when status field is cleared after detach", func() {
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.ExternalIPAddress = "10.0.0.100"
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.lastUpdate.GetStatus().GetExternalIpAddress()).To(Equal("10.0.0.100"))

			// Simulate detach: clear the status field
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())
			computeInstance.Status.ExternalIPAddress = ""
			Expect(k8sClient.Status().Update(ctx, computeInstance)).To(Succeed())

			mockClient.updateCalled = false
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
			Expect(mockClient.lastUpdate.GetStatus().GetExternalIpAddress()).To(BeEmpty())
		})

		It("should not crash when restart conditions are not present", func() {
			// RestartInProgress and RestartFailed conditions should not be present by default
			computeInstance := &osacv1alpha1.ComputeInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, computeInstance)).To(Succeed())

			// Verify conditions don't include restart conditions
			for _, cond := range computeInstance.Status.Conditions {
				Expect(cond.Type).NotTo(Equal(string(osacv1alpha1.ComputeInstanceConditionRestartInProgress)))
				Expect(cond.Type).NotTo(Equal(string(osacv1alpha1.ComputeInstanceConditionRestartFailed)))
			}

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.updateCalled).To(BeTrue())
		})
	})
})
