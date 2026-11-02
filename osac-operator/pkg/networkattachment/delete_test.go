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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const ownerFinalizer = "osac.openshift.io/test-owner"

func TestDeleteOwned_NotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = opv1alpha1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	gone, err := DeleteOwned(context.Background(), c, c, client.ObjectKey{Namespace: "ns", Name: "missing"}, ownerFinalizer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !gone {
		t.Fatal("expected gone=true for missing NetworkAttachment")
	}
}

func TestDeleteOwned_IssuesDelete(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = opv1alpha1.AddToScheme(scheme)
	na := &opv1alpha1.NetworkAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "na-1",
			Namespace:  "ns",
			Finalizers: []string{ownerFinalizer, "other"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(na).Build()

	gone, err := DeleteOwned(context.Background(), c, c, client.ObjectKey{Namespace: "ns", Name: "na-1"}, ownerFinalizer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gone {
		t.Fatal("expected gone=false after issuing delete")
	}
	updated := &opv1alpha1.NetworkAttachment{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "na-1"}, updated); err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if updated.DeletionTimestamp.IsZero() {
		t.Fatal("expected DeletionTimestamp to be set")
	}
}

func TestDeleteOwned_StripsSoleOwnerFinalizer(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = opv1alpha1.AddToScheme(scheme)
	now := metav1.Now()
	na := &opv1alpha1.NetworkAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "na-1",
			Namespace:         "ns",
			Finalizers:        []string{ownerFinalizer},
			DeletionTimestamp: &now,
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(na).Build()

	gone, err := DeleteOwned(context.Background(), c, c, client.ObjectKey{Namespace: "ns", Name: "na-1"}, ownerFinalizer)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gone {
		t.Fatal("expected gone=false until object is fully removed")
	}
	updated := &opv1alpha1.NetworkAttachment{}
	err = c.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "na-1"}, updated)
	if err == nil && len(updated.Finalizers) != 0 {
		t.Fatalf("expected owner finalizer stripped, got %v", updated.Finalizers)
	}
}
