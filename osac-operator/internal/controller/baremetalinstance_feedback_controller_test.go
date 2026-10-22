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
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type mockBareMetalInstancesClient struct {
	signalCalled bool
	signalCount  int
	signalID     string
	signalError  error
	// onSignal, when set, is invoked at the moment Signal is called. Tests use it
	// to observe hub state (e.g. whether the CR still exists) at signal time so
	// ordering between finalizer removal and signaling is directly asserted.
	onSignal func()
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
	if m.onSignal != nil {
		m.onSignal()
	}
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

	// A provisioning stage advance is carried on the live condition's reason/message,
	// not on its status. The predicate must treat a reason-only transition as a
	// signalable change so freshness flows through the feedback -> Signal path. The
	// reason values below are illustrative: the predicate compares the whole Status,
	// so only that the reason differs matters, not the specific strings.
	It("should pass Update events when only a condition reason changes", func() {
		old := &bmfov1alpha1.BareMetalInstance{}
		old.Status.Conditions = []metav1.Condition{
			{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Progressing"},
		}

		new := old.DeepCopy()
		new.Status.Conditions[0].Reason = "Provisioned"

		e := event.UpdateEvent{ObjectOld: old, ObjectNew: new}
		Expect(pred.Update(e)).To(BeTrue())
	})

	// A step advance within a single stage keeps the same reason and condition status,
	// differing only in the message (e.g. two steps within Network Setup). The predicate
	// must still signal so the API tracks the step without waiting for the periodic resync.
	// As above, the values are illustrative: the predicate compares the whole Status, so
	// only that the message differs matters, not the specific strings.
	It("should pass Update events when only a condition message changes", func() {
		old := &bmfov1alpha1.BareMetalInstance{}
		old.Status.Conditions = []metav1.Condition{
			{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Provisioned", Message: "Provisioning"},
		}

		new := old.DeepCopy()
		new.Status.Conditions[0].Message = "Network Setup"

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
				ExternalHostID: "ext-1",
				TemplateID:     "tpl-1",
				Selector: bmfov1alpha1.HostSelectorSpec{
					HostSelector: map[string]string{"osac.openshift.io/host-type": "default"},
				},
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
					Selector: bmfov1alpha1.HostSelectorSpec{
						HostSelector: map[string]string{"osac.openshift.io/host-type": "default"},
					},
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
					Selector: bmfov1alpha1.HostSelectorSpec{
						HostSelector: map[string]string{"osac.openshift.io/host-type": "default"},
					},
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
					Selector: bmfov1alpha1.HostSelectorSpec{
						HostSelector: map[string]string{"osac.openshift.io/host-type": "default"},
					},
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

		It("removes the finalizer first, then signals after the CR is gone", func() {
			// Observe hub state at signal time: the CR must already be absent,
			// proving the finalizer removal (which physically deletes the last
			// finalizer's object) happened before the signal. Fulfillment releases
			// its record only when the hub CR is gone.
			var crPresentAtSignal bool
			var getErrAtSignal error
			mockClient.onSignal = func() {
				probe := &bmfov1alpha1.BareMetalInstance{}
				getErrAtSignal = k8sClient.Get(ctx, typeNamespacedName, probe)
				crPresentAtSignal = getErrAtSignal == nil
			}

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.signalCount).To(Equal(1))
			Expect(mockClient.signalID).To(Equal(bmiID))
			Expect(crPresentAtSignal).To(BeFalse())
			Expect(apierrors.IsNotFound(getErrAtSignal)).To(BeTrue())

			updated := &bmfov1alpha1.BareMetalInstance{}
			err = k8sClient.Get(ctx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("treats a gRPC NotFound signal as already archived and removes the finalizer", func() {
			mockClient.signalError = grpcstatus.Error(grpccodes.NotFound, "already archived")

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

		It("returns a non-NotFound signal error to retry after removing the finalizer", func() {
			mockClient.signalError = grpcstatus.Error(grpccodes.Unavailable, "fulfillment down")

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unavailable))
			Expect(mockClient.signalCalled).To(BeTrue())

			// The finalizer was already removed (CR physically deleted); the
			// returned error drives the retry, and the follow-up reconcile
			// re-signals through the NotFound path.
			updated := &bmfov1alpha1.BareMetalInstance{}
			getErr := k8sClient.Get(ctx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(getErr)).To(BeTrue())
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
					Selector: bmfov1alpha1.HostSelectorSpec{
						HostSelector: map[string]string{"osac.openshift.io/host-type": "default"},
					},
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

	// Regression: a BMI created and deleted in quick succession can be torn down
	// before the feedback finalizer is ever durably added (the AddFinalizer update
	// keeps conflicting with the delete). Earlier the deletion path returned
	// without signaling whenever the feedback finalizer was absent, so the
	// fulfillment service was never told the CR was gone and its record (and the
	// gRPC BareMetalInstances listing) was orphaned forever.
	Context("When reconciling a resource being deleted without the feedback finalizer", func() {
		const raceResourceName = "test-bmi-race"

		var raceNamespacedName types.NamespacedName

		BeforeEach(func() {
			raceNamespacedName = types.NamespacedName{
				Name:      raceResourceName,
				Namespace: bmiNS,
			}

			// The CR carries the BMI ID label and is being deleted, but the feedback
			// finalizer was never added. A foreign finalizer keeps it observable so
			// the reconcile can run against a deleting object.
			bmi := &bmfov1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      raceResourceName,
					Namespace: bmiNS,
					Labels: map[string]string{
						osacBareMetalInstanceIDLabel: bmiID,
					},
					Finalizers: []string{"other-finalizer"},
				},
				Spec: bmfov1alpha1.BareMetalInstanceSpec{
					Selector: bmfov1alpha1.HostSelectorSpec{
						HostSelector: map[string]string{"osac.openshift.io/host-type": "default"},
					},
					ExternalHostID: "ext-789",
					TemplateID:     "test_template",
				},
			}
			Expect(k8sClient.Create(ctx, bmi)).To(Succeed())

			Expect(k8sClient.Get(ctx, raceNamespacedName, bmi)).To(Succeed())
			Expect(k8sClient.Delete(ctx, bmi)).To(Succeed())
		})

		AfterEach(func() {
			bmi := &bmfov1alpha1.BareMetalInstance{}
			err := k8sClient.Get(ctx, raceNamespacedName, bmi)
			if err == nil {
				bmi.Finalizers = nil
				Expect(k8sClient.Update(ctx, bmi)).To(Succeed())
			}
		})

		It("should still signal the fulfillment service", func() {
			request := reconcile.Request{
				NamespacedName: raceNamespacedName,
			}
			result, err := reconciler.Reconcile(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsZero()).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeTrue())
			Expect(mockClient.signalID).To(Equal(bmiID))
		})

		It("returns a non-NotFound signal error to retry while the CR is retained", func() {
			mockClient.signalError = grpcstatus.Error(grpccodes.Unavailable, "fulfillment down")

			request := reconcile.Request{
				NamespacedName: raceNamespacedName,
			}
			_, err := reconciler.Reconcile(ctx, request)
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unavailable))
			Expect(mockClient.signalCalled).To(BeTrue())
		})
	})

	// Regression: fulfillment releases its record only when the hub CR is
	// physically absent, so the feedback controller must still signal after the
	// CR is gone. The identifier label is unreadable then, so the ID is recovered
	// from the request name ("bmi-<id>"). A name without that prefix is ignored.
	Context("When the CR is already gone (NotFound path)", func() {
		var goneNamespacedName types.NamespacedName

		BeforeEach(func() {
			goneNamespacedName = types.NamespacedName{
				Name:      bareMetalInstanceObjectNamePrefix + bmiID,
				Namespace: bmiNS,
			}
		})

		It("recovers the BMI ID from the request name and signals", func() {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: goneNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.signalCalled).To(BeTrue())
			Expect(mockClient.signalID).To(Equal(bmiID))
		})

		It("treats a gRPC NotFound signal as already archived", func() {
			mockClient.signalError = grpcstatus.Error(grpccodes.NotFound, "already archived")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: goneNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.signalCalled).To(BeTrue())
		})

		It("returns a non-NotFound signal error to retry", func() {
			mockClient.signalError = grpcstatus.Error(grpccodes.Unavailable, "fulfillment down")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: goneNamespacedName})
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.Unavailable))
			Expect(mockClient.signalCalled).To(BeTrue())
		})

		It("ignores a request whose name lacks the bmi- prefix", func() {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "other-object", Namespace: bmiNS},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.signalCalled).To(BeFalse())
		})
	})

	// Regression: with remove-before-signal ordering, a conflict on the
	// finalizer-removal update must requeue WITHOUT signaling. Signaling only
	// after the removal is persisted keeps fulfillment from releasing its record
	// while the hub CR still exists. Uses a fake client that injects a conflict on
	// Update.
	Context("When removing the feedback finalizer conflicts on a deletion", func() {
		It("returns the conflict to retry and does not signal before removal is persisted", func() {
			now := metav1.Now()
			bmi := &bmfov1alpha1.BareMetalInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:              resourceName,
					Namespace:         bmiNS,
					Labels:            map[string]string{osacBareMetalInstanceIDLabel: bmiID},
					Finalizers:        []string{osacBareMetalInstanceFeedbackFinalizer},
					DeletionTimestamp: &now,
				},
				Spec: bmfov1alpha1.BareMetalInstanceSpec{
					Selector: bmfov1alpha1.HostSelectorSpec{
						HostSelector: map[string]string{"osac.openshift.io/host-type": "default"},
					},
					ExternalHostID: "ext-123",
					TemplateID:     "test_template",
				},
			}

			baseClient := fake.NewClientBuilder().
				WithScheme(k8sClient.Scheme()).
				WithObjects(bmi).
				Build()
			conflictClient := interceptor.NewClient(baseClient, interceptor.Funcs{
				Update: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.UpdateOption) error {
					return apierrors.NewConflict(
						schema.GroupResource{Group: "osac.openshift.io", Resource: "baremetalinstances"},
						resourceName, errors.New("injected conflict"),
					)
				},
			})

			conflictReconciler := &BareMetalInstanceFeedbackReconciler{
				hubClient:                  conflictClient,
				bareMetalInstancesClient:   mockClient,
				bareMetalInstanceNamespace: bmiNS,
			}

			request := reconcile.Request{
				NamespacedName: typeNamespacedName,
			}
			_, err := conflictReconciler.Reconcile(ctx, request)
			Expect(apierrors.IsConflict(err)).To(BeTrue())
			Expect(mockClient.signalCalled).To(BeFalse())
		})
	})
})
