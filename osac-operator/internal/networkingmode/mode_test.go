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

package networkingmode

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func testClient(t *testing.T, classes ...*v1alpha1.NetworkClass) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	objects := make([]client.Object, 0, len(classes))
	for _, class := range classes {
		objects = append(objects, class)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func networkClass(name string, enabled *bool) *v1alpha1.NetworkClass {
	return &v1alpha1.NetworkClass{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "osac"},
		Spec:       v1alpha1.NetworkClassSpec{ProvisioningEnabled: enabled},
	}
}

func TestResolveEnabled(t *testing.T) {
	kube := testClient(t, networkClass("class", ptr.To(true)))
	enabled, err := Resolve(context.Background(), kube, "osac")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !enabled {
		t.Fatalf("enabled = false, want true")
	}
}

func TestResolveDisabled(t *testing.T) {
	kube := testClient(t, networkClass("class", ptr.To(false)))
	enabled, err := Resolve(context.Background(), kube, "osac")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if enabled {
		t.Fatalf("enabled = true, want false")
	}
}

func TestResolveWithoutClassIsUnresolved(t *testing.T) {
	_, err := Resolve(context.Background(), testClient(t), "osac")
	if !errors.Is(err, ErrUnresolved) {
		t.Fatalf("err = %v, want ErrUnresolved", err)
	}
}

func TestResolveWithoutModeIsUnresolved(t *testing.T) {
	_, err := Resolve(context.Background(), testClient(t, networkClass("class", nil)), "osac")
	if !errors.Is(err, ErrUnresolved) {
		t.Fatalf("err = %v, want ErrUnresolved", err)
	}
}

func TestResolveIgnoresDeletingClasses(t *testing.T) {
	deleting := networkClass("deleting", ptr.To(false))
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	deleting.Finalizers = []string{"test"}
	kube := testClient(t, deleting, networkClass("active", ptr.To(true)))
	enabled, err := Resolve(context.Background(), kube, "osac")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !enabled {
		t.Fatalf("enabled = false, want true")
	}
}

func TestResolveMultipleClassesIsUnresolved(t *testing.T) {
	kube := testClient(t, networkClass("a", ptr.To(true)), networkClass("b", ptr.To(false)))
	_, err := Resolve(context.Background(), kube, "osac")
	if !errors.Is(err, ErrUnresolved) {
		t.Fatalf("err = %v, want ErrUnresolved", err)
	}
}

func TestMismatchError(t *testing.T) {
	err := &MismatchError{Configured: true, Accepted: false}
	if !strings.Contains(err.Error(), "cannot change after installation") {
		t.Fatalf("message %q must explain the reinstall requirement", err.Error())
	}
}
