/*
Copyright 2026.

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

package networkattachment

import (
	"context"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const testOwnerFinalizer = "osac.openshift.io/test-owner"

func newTestClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func TestDeleteOwnedTreatsMissingAttachmentAsGone(t *testing.T) {
	c := newTestClient(t)

	got, err := DeleteOwned(context.Background(), c, c, client.ObjectKey{Namespace: "net", Name: "missing"}, testOwnerFinalizer)
	if err != nil {
		t.Fatalf("DeleteOwned() error = %v", err)
	}
	if !got {
		t.Fatal("DeleteOwned() = false, want true for a missing attachment")
	}
}

func TestDeleteOwnedRequestsDeletionAndWaitsForFinalizers(t *testing.T) {
	key := client.ObjectKey{Namespace: "net", Name: "na-1"}
	na := &v1alpha1.NetworkAttachment{ObjectMeta: metav1.ObjectMeta{
		Namespace:  key.Namespace,
		Name:       key.Name,
		Finalizers: []string{testOwnerFinalizer, "networking.example.com/cleanup"},
	}}
	c := newTestClient(t, na)

	got, err := DeleteOwned(context.Background(), c, c, key, testOwnerFinalizer)
	if err != nil {
		t.Fatalf("DeleteOwned() error = %v", err)
	}
	if got {
		t.Fatal("DeleteOwned() = true after issuing a delete, want false until the object is gone")
	}

	current := &v1alpha1.NetworkAttachment{}
	if err := c.Get(context.Background(), key, current); err != nil {
		t.Fatalf("get attachment after delete: %v", err)
	}
	if current.DeletionTimestamp.IsZero() {
		t.Fatal("attachment has no deletion timestamp after DeleteOwned")
	}
	if len(current.Finalizers) != 2 {
		t.Fatalf("finalizers after delete = %v, want both finalizers retained", current.Finalizers)
	}
}

func TestDeleteOwnedRemovesOwnerFinalizerOnlyWhenItIsLast(t *testing.T) {
	key := client.ObjectKey{Namespace: "net", Name: "na-1"}
	deletingAt := metav1.NewTime(time.Now())
	na := &v1alpha1.NetworkAttachment{ObjectMeta: metav1.ObjectMeta{
		Namespace:         key.Namespace,
		Name:              key.Name,
		DeletionTimestamp: &deletingAt,
		Finalizers:        []string{testOwnerFinalizer},
	}}
	c := newTestClient(t, na)

	got, err := DeleteOwned(context.Background(), c, c, key, testOwnerFinalizer)
	if err != nil {
		t.Fatalf("DeleteOwned() error = %v", err)
	}
	if got {
		t.Fatal("DeleteOwned() = true after removing the owner finalizer, want false until a later read confirms deletion")
	}

	current := &v1alpha1.NetworkAttachment{}
	err = c.Get(context.Background(), key, current)
	if err == nil && len(current.Finalizers) != 0 {
		t.Fatalf("finalizers after owner finalizer removal = %v, want none", current.Finalizers)
	}
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("get attachment after finalizer removal: %v", err)
	}
}

func TestDeleteOwnedLeavesFinalizersWhenAnotherFinalizerRemains(t *testing.T) {
	key := client.ObjectKey{Namespace: "net", Name: "na-1"}
	deletingAt := metav1.NewTime(time.Now())
	na := &v1alpha1.NetworkAttachment{ObjectMeta: metav1.ObjectMeta{
		Namespace:         key.Namespace,
		Name:              key.Name,
		DeletionTimestamp: &deletingAt,
		Finalizers:        []string{testOwnerFinalizer, "networking.example.com/cleanup"},
	}}
	c := newTestClient(t, na)

	got, err := DeleteOwned(context.Background(), c, c, key, testOwnerFinalizer)
	if err != nil {
		t.Fatalf("DeleteOwned() error = %v", err)
	}
	if got {
		t.Fatal("DeleteOwned() = true while another finalizer remains, want false")
	}

	current := &v1alpha1.NetworkAttachment{}
	if err := c.Get(context.Background(), key, current); err != nil {
		t.Fatalf("get attachment: %v", err)
	}
	if len(current.Finalizers) != 2 || current.Finalizers[0] != testOwnerFinalizer {
		t.Fatalf("finalizers = %v, want both original finalizers retained", current.Finalizers)
	}
}

func TestDeleteOwnedReturnsReadError(t *testing.T) {
	wantErr := errors.New("read failed")
	reader := failingReader{err: wantErr}

	got, err := DeleteOwned(context.Background(), reader, nil, client.ObjectKey{Namespace: "net", Name: "na-1"}, testOwnerFinalizer)
	if got {
		t.Fatal("DeleteOwned() = true on a read error, want false")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("DeleteOwned() error = %v, want %v", err, wantErr)
	}
}

func TestDeleteOwnedReturnsDeleteError(t *testing.T) {
	key := client.ObjectKey{Namespace: "net", Name: "na-1"}
	na := &v1alpha1.NetworkAttachment{ObjectMeta: metav1.ObjectMeta{
		Namespace: key.Namespace,
		Name:      key.Name,
	}}
	c := newTestClient(t, na)
	wantErr := errors.New("delete failed")

	got, err := DeleteOwned(context.Background(), c, failingWriter{Writer: c, deleteErr: wantErr}, key, testOwnerFinalizer)
	if got {
		t.Fatal("DeleteOwned() = true on a delete error, want false")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("DeleteOwned() error = %v, want %v", err, wantErr)
	}
}

func TestDeleteOwnedReturnsFinalizerPatchError(t *testing.T) {
	key := client.ObjectKey{Namespace: "net", Name: "na-1"}
	deletingAt := metav1.NewTime(time.Now())
	na := &v1alpha1.NetworkAttachment{ObjectMeta: metav1.ObjectMeta{
		Namespace:         key.Namespace,
		Name:              key.Name,
		DeletionTimestamp: &deletingAt,
		Finalizers:        []string{testOwnerFinalizer},
	}}
	c := newTestClient(t, na)
	wantErr := errors.New("patch failed")

	got, err := DeleteOwned(context.Background(), c, failingWriter{Writer: c, patchErr: wantErr}, key, testOwnerFinalizer)
	if got {
		t.Fatal("DeleteOwned() = true on a patch error, want false")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("DeleteOwned() error = %v, want %v", err, wantErr)
	}
}

type failingReader struct {
	client.Reader
	err error
}

func (r failingReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return r.err
}

type failingWriter struct {
	client.Writer
	deleteErr error
	patchErr  error
}

func (w failingWriter) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if w.deleteErr != nil {
		return w.deleteErr
	}
	return w.Writer.Delete(ctx, obj, opts...)
}

func (w failingWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if w.patchErr != nil {
		return w.patchErr
	}
	return w.Writer.Patch(ctx, obj, patch, opts...)
}
