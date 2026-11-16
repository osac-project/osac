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
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	return s
}

func TestNewRejectsMissingConfiguration(t *testing.T) {
	scheme := testScheme(t)

	if _, err := New(nil, scheme, nil); err == nil {
		t.Fatal("New(nil, ...) should return an error")
	}
	if _, err := New(&rest.Config{Host: "https://127.0.0.1:1"}, nil, nil); err == nil {
		t.Fatal("New with a nil scheme should return an error")
	}
}

// TestNewReadsThroughToAPIServer is the executable proof behind the package's
// promise: every read reaches the API server. A cache-backed client would
// serve the second read of the same object locally and would establish a
// watch; a direct client does neither.
func TestNewReadsThroughToAPIServer(t *testing.T) {
	var requests, watches int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		if r.URL.Query().Get("watch") == "true" {
			atomic.AddInt32(&watches, 1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"ConfigMap",` +
			`"metadata":{"name":"present","namespace":"default"}}`))
	}))
	defer server.Close()

	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{corev1.SchemeGroupVersion})
	mapper.Add(corev1.SchemeGroupVersion.WithKind("ConfigMap"), meta.RESTScopeNamespace)

	direct, err := New(&rest.Config{Host: server.URL}, testScheme(t), mapper)
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	key := client.ObjectKey{Namespace: "default", Name: "present"}
	for i := 0; i < 2; i++ {
		if err := direct.Get(context.Background(), key, &corev1.ConfigMap{}); err != nil {
			t.Fatalf("Get %d through direct client: %v", i+1, err)
		}
	}

	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Fatalf("expected 2 API server requests for 2 reads, got %d: "+
			"the client served a read from somewhere other than the API server", got)
	}
	if got := atomic.LoadInt32(&watches); got != 0 {
		t.Fatalf("expected no watch requests, got %d", got)
	}
}

func TestWrappingRejectsNilClient(t *testing.T) {
	if _, err := WrapForTest(nil); err == nil {
		t.Fatal("WrapForTest(nil) should return an error")
	}
}

func TestWrappingForwardsToUnderlyingClient(t *testing.T) {
	underlying := fake.NewClientBuilder().WithObjects(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "present", Namespace: "default"},
	}).Build()

	direct, err := WrapForTest(underlying)
	if err != nil {
		t.Fatalf("WrapForTest returned an error: %v", err)
	}

	got := &corev1.ConfigMap{}
	err = direct.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "present"}, got)
	if err != nil {
		t.Fatalf("Get through wrapped client: %v", err)
	}
}
