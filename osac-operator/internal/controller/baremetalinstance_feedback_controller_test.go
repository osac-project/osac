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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	bmfov1alpha1 "github.com/osac-project/bare-metal-fulfillment-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac-operator/internal/api/osac/private/v1"
)

type mockBareMetalInstancesClient struct {
	signalCalled bool
	signalCount  int
	signalID     string
	signalError  error
}

func (m *mockBareMetalInstancesClient) List(_ context.Context, _ *privatev1.BareMetalInstancesListRequest, _ ...grpc.CallOption) (*privatev1.BareMetalInstancesListResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockBareMetalInstancesClient) Get(_ context.Context, _ *privatev1.BareMetalInstancesGetRequest, _ ...grpc.CallOption) (*privatev1.BareMetalInstancesGetResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockBareMetalInstancesClient) Create(_ context.Context, _ *privatev1.BareMetalInstancesCreateRequest, _ ...grpc.CallOption) (*privatev1.BareMetalInstancesCreateResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockBareMetalInstancesClient) Update(_ context.Context, _ *privatev1.BareMetalInstancesUpdateRequest, _ ...grpc.CallOption) (*privatev1.BareMetalInstancesUpdateResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockBareMetalInstancesClient) Delete(_ context.Context, _ *privatev1.BareMetalInstancesDeleteRequest, _ ...grpc.CallOption) (*privatev1.BareMetalInstancesDeleteResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockBareMetalInstancesClient) Signal(_ context.Context, in *privatev1.BareMetalInstancesSignalRequest, _ ...grpc.CallOption) (*privatev1.BareMetalInstancesSignalResponse, error) {
	m.signalCalled = true
	m.signalCount++
	m.signalID = in.GetId()
	if m.signalError != nil {
		return nil, m.signalError
	}
	return &privatev1.BareMetalInstancesSignalResponse{}, nil
}

var _ = Describe("bareMetalInstanceStatusChangedPredicate", func() {
	var pred = bareMetalInstanceStatusChangedPredicate()

	It("should pass Create events", func() {
		e := event.CreateEvent{
			Object: &bmfov1alpha1.BareMetalInstance{},
		}
		Expect(pred.Create(e)).To(BeTrue())
	})

	It("should pass Delete events", func() {
		e := event.DeleteEvent{
			Object: &bmfov1alpha1.BareMetalInstance{},
		}
		Expect(pred.Delete(e)).To(BeTrue())
	})

	It("should pass Update events when status phase changes", func() {
		old := &bmfov1alpha1.BareMetalInstance{}
		old.Status.Phase = bmfov1alpha1.BareMetalInstancePhaseProgressing

		new := old.DeepCopy()
		new.Status.Phase = bmfov1alpha1.BareMetalInstancePhaseReady

		e := event.UpdateEvent{ObjectOld: old, ObjectNew: new}
		Expect(pred.Update(e)).To(BeTrue())
	})

	It("should pass Update events when status conditions change", func() {
		old := &bmfov1alpha1.BareMetalInstance{}

		new := old.DeepCopy()
		new.Status.Conditions = []metav1.Condition{
			{Type: "Ready", Status: metav1.ConditionTrue, Reason: "AsExpected"},
		}

		e := event.UpdateEvent{ObjectOld: old, ObjectNew: new}
		Expect(pred.Update(e)).To(BeTrue())
	})

	It("should pass Update events when deletionTimestamp is set", func() {
		now := metav1.Now()
		old := &bmfov1alpha1.BareMetalInstance{}
		old.Status.Phase = bmfov1alpha1.BareMetalInstancePhaseProgressing

		new := old.DeepCopy()
		new.DeletionTimestamp = &now

		e := event.UpdateEvent{ObjectOld: old, ObjectNew: new}
		Expect(pred.Update(e)).To(BeTrue())
	})

	It("should filter Update events when only metadata changes", func() {
		old := &bmfov1alpha1.BareMetalInstance{}
		old.Status.Phase = bmfov1alpha1.BareMetalInstancePhaseProgressing

		new := old.DeepCopy()
		new.Annotations = map[string]string{"foo": "bar"}

		e := event.UpdateEvent{ObjectOld: old, ObjectNew: new}
		Expect(pred.Update(e)).To(BeFalse())
	})

	It("should filter Update events when only spec changes", func() {
		old := &bmfov1alpha1.BareMetalInstance{
			Spec: bmfov1alpha1.BareMetalInstanceSpec{
				HostType:       "default",
				ExternalHostID: "ext-1",
				TemplateID:     "tpl-1",
			},
		}
		old.Status.Phase = bmfov1alpha1.BareMetalInstancePhaseProgressing

		new := old.DeepCopy()
		new.Spec.RunStrategy = bmfov1alpha1.RunStrategyAlways

		e := event.UpdateEvent{ObjectOld: old, ObjectNew: new}
		Expect(pred.Update(e)).To(BeFalse())
	})
})

var _ = Describe("BareMetalInstanceFeedbackReconciler", func() {
	const (
		resourceName = "test-bmi"
		bmiID        = "test-bmi-id"
		bmiNS        = "osac-baremetalinstance"
	)

	var (
		ctx                context.Context
		typeNamespacedName types.NamespacedName
		mockClient         *mockBareMetalInstancesClient
		reconciler         *BareMetalInstanceFeedbackReconciler
	)

	BeforeEach(func() {
		ctx = context.Background()
		typeNamespacedName = types.NamespacedName{
			Name:      resourceName,
			Namespace: bmiNS,
		}
		mockClient = &mockBareMetalInstancesClient{}
		reconciler = &BareMetalInstanceFeedbackReconciler{
			hubClient:                  k8sClient,
			bareMetalInstancesClient:   mockClient,
			bareMetalInstanceNamespace: bmiNS,
		}

		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: bmiNS,
			},
		}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: bmiNS}, namespace)
		if err != nil && apierrors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		}
	})

	Context("When reconciling a resource that doesn't exist", func() {
		It("should return without error and not signal", func() {
			request := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "non-existent",
					Namespace: bmiNS,
				},
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeFalse())
		})
	})

	Context("When reconciling a resource without the BMI ID label", func() {
		BeforeEach(func() {
			bmi := &bmfov1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: bmiNS,
				},
				Spec: bmfov1alpha1.BareMetalInstanceSpec{
					HostType:       "default",
					ExternalHostID: "ext-123",
					TemplateID:     "test_template",
				},
			}
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
		})

		AfterEach(func() {
			bmi := &bmfov1alpha1.BareMetalInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, bmi)
			if err == nil {
				bmi.Finalizers = nil
				_ = k8sClient.Update(ctx, bmi)
				_ = k8sClient.Delete(ctx, bmi)
			}
		})

		It("should skip reconciliation", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeFalse())
		})

		It("should remove feedback finalizer from CR without BMI ID label being deleted", func() {
			bmi := &bmfov1alpha1.BareMetalInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, bmi)).To(Succeed())
			bmi.Finalizers = []string{osacBareMetalInstanceFeedbackFinalizer}
			Expect(k8sClient.Update(ctx, bmi)).To(Succeed())

			Expect(k8sClient.Get(ctx, typeNamespacedName, bmi)).To(Succeed())
			Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())

			updated := &bmfov1alpha1.BareMetalInstance{}
			err = k8sClient.Get(ctx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeFalse())
		})
	})

	Context("When reconciling a valid resource", func() {
		BeforeEach(func() {
			mockClient.signalCalled = false
			mockClient.signalCount = 0

			bmi := &bmfov1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: bmiNS,
					Labels: map[string]string{
						osacBareMetalInstanceIDLabel: bmiID,
					},
				},
				Spec: bmfov1alpha1.BareMetalInstanceSpec{
					HostType:       "default",
					ExternalHostID: "ext-123",
					TemplateID:     "test_template",
				},
			}
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())
		})

		AfterEach(func() {
			bmi := &bmfov1alpha1.BareMetalInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, bmi)
			if err == nil {
				bmi.Finalizers = nil
				_ = k8sClient.Update(ctx, bmi)
				Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
			}
		})

		It("should add feedback finalizer and call Signal", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())

			updated := &bmfov1alpha1.BareMetalInstance{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacBareMetalInstanceFeedbackFinalizer)).To(BeTrue())

			Expect(mockClient.signalCalled).To(BeTrue())
			Expect(mockClient.signalID).To(Equal(bmiID))
		})

	})

	Context("When reconciling a resource being deleted with feedback finalizer as last", func() {
		BeforeEach(func() {
			bmi := &bmfov1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: bmiNS,
					Labels: map[string]string{
						osacBareMetalInstanceIDLabel: bmiID,
					},
					Finalizers: []string{osacBareMetalInstanceFeedbackFinalizer},
				},
				Spec: bmfov1alpha1.BareMetalInstanceSpec{
					HostType:       "default",
					ExternalHostID: "ext-123",
					TemplateID:     "test_template",
				},
			}
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			Expect(k8sClient.Get(ctx, typeNamespacedName, bmi)).To(Succeed())
			Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
		})

		AfterEach(func() {
			bmi := &bmfov1alpha1.BareMetalInstance{}
			err := k8sClient.Get(ctx, typeNamespacedName, bmi)
			if err == nil {
				bmi.Finalizers = nil
				Expect(k8sClient.Update(ctx, bmi)).To(Succeed())
			}
		})

		It("should remove finalizer and call Signal", func() {
			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeTrue())
			Expect(mockClient.signalID).To(Equal(bmiID))

			updated := &bmfov1alpha1.BareMetalInstance{}
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

			updated := &bmfov1alpha1.BareMetalInstance{}
			err = k8sClient.Get(ctx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("When reconciling a resource being deleted with multiple finalizers", func() {
		const deletingResourceName = "test-bmi-multi-fin"

		var deletingNamespacedName types.NamespacedName

		BeforeEach(func() {
			deletingNamespacedName = types.NamespacedName{
				Name:      deletingResourceName,
				Namespace: bmiNS,
			}

			bmi := &bmfov1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deletingResourceName,
					Namespace: bmiNS,
					Labels: map[string]string{
						osacBareMetalInstanceIDLabel: bmiID,
					},
					Finalizers: []string{"other-finalizer", osacBareMetalInstanceFeedbackFinalizer},
				},
				Spec: bmfov1alpha1.BareMetalInstanceSpec{
					HostType:       "default",
					ExternalHostID: "ext-456",
					TemplateID:     "test_template",
				},
			}
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			Expect(k8sClient.Get(ctx, deletingNamespacedName, bmi)).To(Succeed())
			Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
		})

		AfterEach(func() {
			bmi := &bmfov1alpha1.BareMetalInstance{}
			err := k8sClient.Get(ctx, deletingNamespacedName, bmi)
			if err == nil {
				bmi.Finalizers = nil
				Expect(k8sClient.Update(ctx, bmi)).To(Succeed())
			}
		})

		It("should not remove finalizer and not signal when other finalizers present", func() {
			request := reconcile.Request{
				NamespacedName: deletingNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeFalse())

			updated := &bmfov1alpha1.BareMetalInstance{}
			Expect(k8sClient.Get(ctx, deletingNamespacedName, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, osacBareMetalInstanceFeedbackFinalizer)).To(BeTrue())
		})
	})
})
