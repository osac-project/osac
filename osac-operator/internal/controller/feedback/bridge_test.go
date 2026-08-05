/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package feedback

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
)

func TestFeedback(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Feedback Suite")
}

const (
	testName      = "test-resource"
	testNamespace = "test-ns"
	testID        = "resource-123"
	testFinalizer = "osac.openshift.io/test-feedback"
	otherFin      = "osac.openshift.io/test-resource"
	testIDLabel   = "osac.openshift.io/test-uuid"
)

type tracker struct {
	fetchCalls  int
	saveCalls   int
	signalCalls int
	signalIDs   []string
	savedRemote *privatev1.Subnet

	fetchResult *privatev1.Subnet
	fetchErr    error
	saveErr     error
	signalErr   error

	syncUpdateCalls int
	syncDeleteCalls int
	syncUpdateFn    func(ctx context.Context, obj *v1alpha1.Subnet, remote *privatev1.Subnet) error
	syncDeleteFn    func(ctx context.Context, obj *v1alpha1.Subnet, remote *privatev1.Subnet) error
}

func newTracker() *tracker {
	return &tracker{
		fetchResult: &privatev1.Subnet{
			Id:       testID,
			Metadata: &privatev1.Metadata{Name: testName},
			Spec:     &privatev1.SubnetSpec{},
			Status:   &privatev1.SubnetStatus{State: privatev1.SubnetState_SUBNET_STATE_PENDING},
		},
	}
}

func (t *tracker) fetch(_ context.Context, id string) (*privatev1.Subnet, error) {
	t.fetchCalls++
	if t.fetchErr != nil {
		return nil, t.fetchErr
	}
	return t.fetchResult, nil
}

func (t *tracker) save(_ context.Context, remote *privatev1.Subnet) error {
	t.saveCalls++
	t.savedRemote = remote
	return t.saveErr
}

func (t *tracker) signal(_ context.Context, id string) error {
	t.signalCalls++
	t.signalIDs = append(t.signalIDs, id)
	return t.signalErr
}

func (t *tracker) syncUpdate(ctx context.Context, obj *v1alpha1.Subnet, remote *privatev1.Subnet) error {
	t.syncUpdateCalls++
	if t.syncUpdateFn != nil {
		return t.syncUpdateFn(ctx, obj, remote)
	}
	return nil
}

func (t *tracker) syncDelete(ctx context.Context, obj *v1alpha1.Subnet, remote *privatev1.Subnet) error {
	t.syncDeleteCalls++
	if t.syncDeleteFn != nil {
		return t.syncDeleteFn(ctx, obj, remote)
	}
	return nil
}

func newFakeClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func newBridge(k8sClient client.Client, trk *tracker) *Bridge[*v1alpha1.Subnet, *privatev1.Subnet] {
	return &Bridge[*v1alpha1.Subnet, *privatev1.Subnet]{
		Client:     k8sClient,
		Finalizer:  testFinalizer,
		IDLabel:    testIDLabel,
		Kind:       "Subnet",
		IDKey:      "subnetID",
		NewObject:  func() *v1alpha1.Subnet { return &v1alpha1.Subnet{} },
		Fetch:      trk.fetch,
		Save:       trk.save,
		Signal:     trk.signal,
		SyncUpdate: trk.syncUpdate,
		SyncDelete: trk.syncDelete,
	}
}

func newRequest() reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{Name: testName, Namespace: testNamespace},
	}
}

var _ = Describe("Bridge", func() {
	var (
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	Context("CR not found", func() {
		It("should return nil when the CR no longer exists", func() {
			trk := newTracker()
			k8sClient := newFakeClient()
			bridge := newBridge(k8sClient, trk)

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).NotTo(HaveOccurred())
			Expect(trk.fetchCalls).To(Equal(0))
		})
	})

	Context("missing ID label", func() {
		It("should ignore a CR without the ID label", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testName,
					Namespace: testNamespace,
					Labels:    map[string]string{},
				},
			}
			trk := newTracker()
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).NotTo(HaveOccurred())
			Expect(trk.fetchCalls).To(Equal(0))
		})

		It("should remove finalizer from a deleting CR without ID label", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testName,
					Namespace:  testNamespace,
					Labels:     map[string]string{},
					Finalizers: []string{testFinalizer},
				},
			}
			trk := newTracker()
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)

			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).NotTo(HaveOccurred())

			updated := &v1alpha1.Subnet{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: testName, Namespace: testNamespace}, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("NotFound during deletion", func() {
		It("should remove finalizer and return nil", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testName,
					Namespace:  testNamespace,
					Labels:     map[string]string{testIDLabel: testID},
					Finalizers: []string{testFinalizer},
				},
			}
			trk := newTracker()
			trk.fetchErr = status.Error(codes.NotFound, "not found")
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)

			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).NotTo(HaveOccurred())
			Expect(trk.saveCalls).To(Equal(0))
			Expect(trk.signalCalls).To(Equal(0))

			updated := &v1alpha1.Subnet{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: testName, Namespace: testNamespace}, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should return nil even when finalizer is already absent", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testName,
					Namespace:  testNamespace,
					Labels:     map[string]string{testIDLabel: testID},
					Finalizers: []string{otherFin},
				},
			}
			trk := newTracker()
			trk.fetchErr = status.Error(codes.NotFound, "not found")
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)

			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).NotTo(HaveOccurred())
			Expect(trk.saveCalls).To(Equal(0))
			Expect(trk.signalCalls).To(Equal(0))

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testName, Namespace: testNamespace}, updated)).To(Succeed())
			Expect(updated.Finalizers).To(ConsistOf(otherFin))
		})

		It("should use custom IsNotFound when provided", func() {
			sentinel := errors.New("custom not found")
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testName,
					Namespace:  testNamespace,
					Labels:     map[string]string{testIDLabel: testID},
					Finalizers: []string{testFinalizer},
				},
			}
			trk := newTracker()
			trk.fetchErr = sentinel
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)
			bridge.IsNotFound = func(err error) bool { return errors.Is(err, sentinel) }

			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).NotTo(HaveOccurred())
			Expect(trk.saveCalls).To(Equal(0))

			updated := &v1alpha1.Subnet{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: testName, Namespace: testNamespace}, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("NotFound when not deleting", func() {
		It("should propagate the error", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testName,
					Namespace: testNamespace,
					Labels:    map[string]string{testIDLabel: testID},
				},
			}
			trk := newTracker()
			trk.fetchErr = status.Error(codes.NotFound, "not found")
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).To(HaveOccurred())
		})
	})

	Context("normal update", func() {
		It("should add finalizer and call SyncUpdate", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testName,
					Namespace: testNamespace,
					Labels:    map[string]string{testIDLabel: testID},
				},
			}
			trk := newTracker()
			trk.syncUpdateFn = func(_ context.Context, _ *v1alpha1.Subnet, remote *privatev1.Subnet) error {
				remote.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_READY)
				return nil
			}
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).NotTo(HaveOccurred())

			Expect(trk.syncUpdateCalls).To(Equal(1))
			Expect(trk.saveCalls).To(Equal(1))
			Expect(trk.savedRemote.GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_READY))

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testName, Namespace: testNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, testFinalizer)).To(BeTrue())
		})

		It("should not save when remote record is unchanged", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testName,
					Namespace:  testNamespace,
					Labels:     map[string]string{testIDLabel: testID},
					Finalizers: []string{testFinalizer},
				},
			}
			trk := newTracker()
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).NotTo(HaveOccurred())
			Expect(trk.syncUpdateCalls).To(Equal(1))
			Expect(trk.saveCalls).To(Equal(0))
		})
	})

	Context("normal delete", func() {
		It("should call SyncDelete and save changes", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testName,
					Namespace:  testNamespace,
					Labels:     map[string]string{testIDLabel: testID},
					Finalizers: []string{testFinalizer, otherFin},
				},
			}
			trk := newTracker()
			trk.syncDeleteFn = func(_ context.Context, _ *v1alpha1.Subnet, remote *privatev1.Subnet) error {
				remote.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_DELETING)
				return nil
			}
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)

			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).NotTo(HaveOccurred())

			Expect(trk.syncDeleteCalls).To(Equal(1))
			Expect(trk.saveCalls).To(Equal(1))
			Expect(trk.signalCalls).To(Equal(0))

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testName, Namespace: testNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, testFinalizer)).To(BeTrue())
		})
	})

	Context("PostSaveOnDelete", func() {
		It("should call PostSaveOnDelete after save and before finalizer removal", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testName,
					Namespace:  testNamespace,
					Labels:     map[string]string{testIDLabel: testID},
					Finalizers: []string{testFinalizer},
				},
			}
			trk := newTracker()
			trk.syncDeleteFn = func(_ context.Context, _ *v1alpha1.Subnet, remote *privatev1.Subnet) error {
				remote.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_DELETING)
				return nil
			}
			postSaveCalled := false
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)
			bridge.PostSaveOnDelete = func(_ context.Context, _ *v1alpha1.Subnet, remote *privatev1.Subnet) error {
				Expect(trk.saveCalls).To(Equal(1))
				Expect(remote).NotTo(BeNil())
				Expect(remote.GetStatus().GetState()).To(Equal(privatev1.SubnetState_SUBNET_STATE_DELETING))
				inHook := &v1alpha1.Subnet{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testName, Namespace: testNamespace}, inHook)).To(Succeed())
				Expect(controllerutil.ContainsFinalizer(inHook, testFinalizer)).To(BeTrue())
				postSaveCalled = true
				return nil
			}

			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).NotTo(HaveOccurred())
			Expect(postSaveCalled).To(BeTrue())
			Expect(trk.signalCalls).To(Equal(1))
		})

		It("should propagate PostSaveOnDelete errors", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testName,
					Namespace:  testNamespace,
					Labels:     map[string]string{testIDLabel: testID},
					Finalizers: []string{testFinalizer},
				},
			}
			trk := newTracker()
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)
			bridge.PostSaveOnDelete = func(_ context.Context, _ *v1alpha1.Subnet, _ *privatev1.Subnet) error {
				return errors.New("cross-resource cleanup failed")
			}

			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("cross-resource cleanup failed"))
			Expect(trk.signalCalls).To(Equal(0))
		})

		It("should not call PostSaveOnDelete on the update path", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testName,
					Namespace: testNamespace,
					Labels:    map[string]string{testIDLabel: testID},
				},
			}
			trk := newTracker()
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)
			bridge.PostSaveOnDelete = func(_ context.Context, _ *v1alpha1.Subnet, _ *privatev1.Subnet) error {
				Fail("PostSaveOnDelete should not be called on update path")
				return nil
			}

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).NotTo(HaveOccurred())
			Expect(trk.syncUpdateCalls).To(Equal(1))
		})
	})

	Context("error propagation", func() {
		It("should propagate Save errors and skip Signal", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testName,
					Namespace:  testNamespace,
					Labels:     map[string]string{testIDLabel: testID},
					Finalizers: []string{testFinalizer},
				},
			}
			trk := newTracker()
			trk.syncDeleteFn = func(_ context.Context, _ *v1alpha1.Subnet, remote *privatev1.Subnet) error {
				remote.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_DELETING)
				return nil
			}
			trk.saveErr = errors.New("save failed")
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)

			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("save failed"))
			Expect(trk.signalCalls).To(Equal(0))

			updated := &v1alpha1.Subnet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testName, Namespace: testNamespace}, updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(updated, testFinalizer)).To(BeTrue())
		})

		It("should propagate SyncUpdate errors and skip Save", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testName,
					Namespace:  testNamespace,
					Labels:     map[string]string{testIDLabel: testID},
					Finalizers: []string{testFinalizer},
				},
			}
			trk := newTracker()
			trk.syncUpdateFn = func(_ context.Context, _ *v1alpha1.Subnet, _ *privatev1.Subnet) error {
				return errors.New("sync update failed")
			}
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("sync update failed"))
			Expect(trk.saveCalls).To(Equal(0))
		})
	})

	Context("last finalizer removal and Signal", func() {
		It("should remove finalizer and call Signal when it is the last one", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testName,
					Namespace:  testNamespace,
					Labels:     map[string]string{testIDLabel: testID},
					Finalizers: []string{testFinalizer},
				},
			}
			trk := newTracker()
			trk.syncDeleteFn = func(_ context.Context, _ *v1alpha1.Subnet, remote *privatev1.Subnet) error {
				remote.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_DELETING)
				return nil
			}
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)

			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).NotTo(HaveOccurred())

			Expect(trk.saveCalls).To(Equal(1))
			Expect(trk.signalCalls).To(Equal(1))
			Expect(trk.signalIDs).To(ConsistOf(testID))

			updated := &v1alpha1.Subnet{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: testName, Namespace: testNamespace}, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should not fail when Signal returns an error", func() {
			cr := &v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       testName,
					Namespace:  testNamespace,
					Labels:     map[string]string{testIDLabel: testID},
					Finalizers: []string{testFinalizer},
				},
			}
			trk := newTracker()
			trk.signalErr = errors.New("signal failed")
			k8sClient := newFakeClient(cr)
			bridge := newBridge(k8sClient, trk)

			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := bridge.Reconcile(ctx, newRequest())
			Expect(err).NotTo(HaveOccurred())
			Expect(trk.signalCalls).To(Equal(1))
		})
	})
})
