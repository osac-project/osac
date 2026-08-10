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

package inventory

import (
	"context"
	"testing"

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newBMHTestManager(t *testing.T, objects ...client.Object) *BMHLifecycleManager {
	t.Helper()
	k8sClient := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(objects...).
		WithStatusSubresource(&metal3api.BareMetalHost{}).
		Build()
	return NewBMHLifecycleManager(k8sClient, testNamespace)
}

func TestCreateBMH(t *testing.T) {
	t.Run("creates new BMH", func(t *testing.T) {
		mgr := newBMHTestManager(t)

		err := mgr.CreateBMH(context.Background(), BMHCreateParams{
			Name:              "node001",
			BMCAddress:        "redfish-virtualmedia+https://10.0.0.1/redfish/v1/Systems/1",
			CredentialsSecret: "bmc-creds",
			BootMACAddress:    "aa:bb:cc:dd:ee:01",
			ConsumerRef: &corev1.ObjectReference{
				APIVersion: "osac.openshift.io/v1alpha1",
				Kind:       "BareMetalInstance",
				Name:       "test-instance-uid",
			},
			Labels: map[string]string{"osac.openshift.io/pool-id": "pool1"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		bmh := &metal3api.BareMetalHost{}
		if err := mgr.client.Get(context.Background(), client.ObjectKey{
			Namespace: testNamespace, Name: "node001",
		}, bmh); err != nil {
			t.Fatalf("failed to get created BMH: %v", err)
		}

		if bmh.Spec.BMC.Address != "redfish-virtualmedia+https://10.0.0.1/redfish/v1/Systems/1" {
			t.Errorf("expected BMC address, got %s", bmh.Spec.BMC.Address)
		}
		if bmh.Spec.BMC.CredentialsName != "bmc-creds" {
			t.Errorf("expected credentials name bmc-creds, got %s", bmh.Spec.BMC.CredentialsName)
		}
		if bmh.Spec.BootMACAddress != "aa:bb:cc:dd:ee:01" {
			t.Errorf("expected boot MAC aa:bb:cc:dd:ee:01, got %s", bmh.Spec.BootMACAddress)
		}
		if bmh.Spec.Online {
			t.Error("expected online=false")
		}
		if bmh.Spec.ConsumerRef == nil || bmh.Spec.ConsumerRef.Name != "test-instance-uid" {
			t.Error("expected consumerRef with test-instance-uid")
		}
		if bmh.Labels["osac.openshift.io/pool-id"] != "pool1" {
			t.Errorf("expected pool-id label, got %v", bmh.Labels)
		}
	})

	t.Run("idempotent with matching consumerRef", func(t *testing.T) {
		existing := &metal3api.BareMetalHost{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "node001",
				Namespace: testNamespace,
			},
			Spec: metal3api.BareMetalHostSpec{
				ConsumerRef: &corev1.ObjectReference{
					Name: "test-instance-uid",
				},
			},
		}
		mgr := newBMHTestManager(t, existing)

		err := mgr.CreateBMH(context.Background(), BMHCreateParams{
			Name:              "node001",
			BMCAddress:        "redfish+https://10.0.0.1",
			CredentialsSecret: "creds",
			BootMACAddress:    "aa:bb:cc:dd:ee:01",
			ConsumerRef: &corev1.ObjectReference{
				Name: "test-instance-uid",
			},
		})
		if err != nil {
			t.Fatalf("expected idempotent success, got: %v", err)
		}
	})

	t.Run("error with different consumerRef", func(t *testing.T) {
		existing := &metal3api.BareMetalHost{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "node001",
				Namespace: testNamespace,
			},
			Spec: metal3api.BareMetalHostSpec{
				ConsumerRef: &corev1.ObjectReference{
					Name: "other-instance-uid",
				},
			},
		}
		mgr := newBMHTestManager(t, existing)

		err := mgr.CreateBMH(context.Background(), BMHCreateParams{
			Name:              "node001",
			BMCAddress:        "redfish+https://10.0.0.1",
			CredentialsSecret: "creds",
			BootMACAddress:    "aa:bb:cc:dd:ee:01",
			ConsumerRef: &corev1.ObjectReference{
				Name: "my-instance-uid",
			},
		})
		if err == nil {
			t.Fatal("expected error for different consumerRef, got nil")
		}
	})
}

func TestDeleteBMH(t *testing.T) {
	t.Run("deletes existing BMH", func(t *testing.T) {
		existing := &metal3api.BareMetalHost{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "node001",
				Namespace: testNamespace,
			},
		}
		mgr := newBMHTestManager(t, existing)

		if err := mgr.DeleteBMH(context.Background(), "node001"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		bmh := &metal3api.BareMetalHost{}
		err := mgr.client.Get(context.Background(), client.ObjectKey{
			Namespace: testNamespace, Name: "node001",
		}, bmh)
		if err == nil {
			t.Error("expected BMH to be deleted")
		}
	})

	t.Run("idempotent for not found", func(t *testing.T) {
		mgr := newBMHTestManager(t)

		if err := mgr.DeleteBMH(context.Background(), "nonexistent"); err != nil {
			t.Fatalf("expected idempotent success for not found, got: %v", err)
		}
	})
}

func TestIsBMHReady(t *testing.T) {
	t.Run("ready when available and OK", func(t *testing.T) {
		bmh := &metal3api.BareMetalHost{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "node001",
				Namespace: testNamespace,
			},
			Status: metal3api.BareMetalHostStatus{
				Provisioning: metal3api.ProvisionStatus{
					State: metal3api.StateAvailable,
				},
				OperationalStatus: metal3api.OperationalStatusOK,
			},
		}
		mgr := newBMHTestManager(t, bmh)

		ready, err := mgr.IsBMHReady(context.Background(), "node001")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ready {
			t.Error("expected ready=true")
		}
	})

	t.Run("not ready when registering", func(t *testing.T) {
		bmh := &metal3api.BareMetalHost{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "node001",
				Namespace: testNamespace,
			},
			Status: metal3api.BareMetalHostStatus{
				Provisioning: metal3api.ProvisionStatus{
					State: metal3api.StateRegistering,
				},
				OperationalStatus: metal3api.OperationalStatusOK,
			},
		}
		mgr := newBMHTestManager(t, bmh)

		ready, err := mgr.IsBMHReady(context.Background(), "node001")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ready {
			t.Error("expected ready=false for registering state")
		}
	})

	t.Run("error when operational status is error", func(t *testing.T) {
		bmh := &metal3api.BareMetalHost{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "node001",
				Namespace: testNamespace,
			},
			Status: metal3api.BareMetalHostStatus{
				Provisioning: metal3api.ProvisionStatus{
					State: metal3api.StateAvailable,
				},
				OperationalStatus: metal3api.OperationalStatusError,
				ErrorMessage:      "BMC connection failed",
			},
		}
		mgr := newBMHTestManager(t, bmh)

		_, err := mgr.IsBMHReady(context.Background(), "node001")
		if err == nil {
			t.Fatal("expected error for error operational status, got nil")
		}
	})

	t.Run("error when BMH not found", func(t *testing.T) {
		mgr := newBMHTestManager(t)

		_, err := mgr.IsBMHReady(context.Background(), "nonexistent")
		if err == nil {
			t.Fatal("expected error for not found, got nil")
		}
	})
}
