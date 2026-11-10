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

package directclient

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNewRejectsNilConfig(t *testing.T) {
	if _, err := New(nil, client.Options{}); err == nil {
		t.Fatal("New(nil, ...) should return an error")
	}
}

func TestNewBuildsClient(t *testing.T) {
	c, err := New(&rest.Config{Host: "https://127.0.0.1:1"}, client.Options{})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	if c == nil {
		t.Fatal("New returned a nil client")
	}
}

func TestWrapRejectsNilClient(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Wrap(nil) should panic")
		}
	}()

	Wrap(nil)
}

func TestWrapForwardsToUnderlyingClient(t *testing.T) {
	underlying := fake.NewClientBuilder().WithObjects(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "present", Namespace: "default"},
	}).Build()

	direct := Wrap(underlying)

	got := &corev1.ConfigMap{}
	err := direct.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "present"}, got)
	if err != nil {
		t.Fatalf("Get through wrapped client: %v", err)
	}
}
