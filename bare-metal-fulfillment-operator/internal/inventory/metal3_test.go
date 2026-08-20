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
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testNamespace = "test-bmaas"
	testHostClass = "metal3"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = metal3api.AddToScheme(s)
	return s
}

func newMetal3ClientForTest(objects ...client.Object) *Metal3Client {
	scheme := newTestScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&metal3api.BareMetalHost{}).
		Build()
	return &Metal3Client{
		client:    fakeClient,
		namespace: testNamespace,
		hostClass: testHostClass,
	}
}

func newBMH(name string, labels map[string]string, opStatus metal3api.OperationalStatus, provState metal3api.ProvisioningState) *metal3api.BareMetalHost {
	if labels == nil {
		labels = map[string]string{}
	}
	return &metal3api.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels:    labels,
		},
		Status: metal3api.BareMetalHostStatus{
			OperationalStatus: opStatus,
			Provisioning: metal3api.ProvisionStatus{
				State: provState,
			},
		},
	}
}

func defaultLabels() map[string]string {
	return map[string]string{
		Metal3HostTypeLabel:  "gpu-node",
		Metal3ManagedByLabel: "baremetal",
	}
}

func withHardwareDetails(bmh *metal3api.BareMetalHost, nics ...metal3api.NIC) *metal3api.BareMetalHost {
	bmh.Status.HardwareDetails = &metal3api.HardwareDetails{NIC: nics}
	return bmh
}

func testNIC(mac string) metal3api.NIC {
	return metal3api.NIC{MAC: mac}
}

// --- ParseHostID ---

func TestParseHostID(t *testing.T) {
	tests := []struct {
		name      string
		hostID    string
		wantNS    string
		wantName  string
		wantError bool
	}{
		{
			name:     "valid namespace/name",
			hostID:   "my-namespace/my-host",
			wantNS:   "my-namespace",
			wantName: "my-host",
		},
		{
			name:      "missing namespace",
			hostID:    "/my-host",
			wantError: true,
		},
		{
			name:      "missing name",
			hostID:    "my-namespace/",
			wantError: true,
		},
		{
			name:      "no separator",
			hostID:    "just-a-name",
			wantError: true,
		},
		{
			name:      "empty string",
			hostID:    "",
			wantError: true,
		},
		{
			name:     "name with extra slashes",
			hostID:   "ns/name/extra",
			wantNS:   "ns",
			wantName: "name/extra",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns, name, err := ParseHostID(tt.hostID)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ns != tt.wantNS {
				t.Errorf("namespace = %q, want %q", ns, tt.wantNS)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
		})
	}
}

// --- FindFreeHost ---

//nolint:gocyclo
func TestFindFreeHost(t *testing.T) {
	ctx := context.Background()

	t.Run("returns matching unassigned host", func(t *testing.T) {
		bmh := withHardwareDetails(newBMH("host-1", defaultLabels(), metal3api.OperationalStatusOK, metal3api.StateAvailable), testNIC("AA:BB:CC:DD:EE:01"))

		m := newMetal3ClientForTest(bmh)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host == nil {
			t.Fatal("expected a host, got nil")
		}
		if host.InventoryHostID != testNamespace+"/host-1" {
			t.Errorf("InventoryHostID = %q, want %q", host.InventoryHostID, testNamespace+"/host-1")
		}
		if host.Name != "host-1" {
			t.Errorf("Name = %q, want %q", host.Name, "host-1")
		}
		if host.HostType != "gpu-node" {
			t.Errorf("HostType = %q, want %q", host.HostType, "gpu-node")
		}
		if host.HostClass != testHostClass {
			t.Errorf("HostClass = %q, want %q", host.HostClass, testHostClass)
		}
		if host.ProvisionState != "available" {
			t.Errorf("ProvisionState = %q, want %q", host.ProvisionState, "available")
		}
		if host.ManagedBy != "baremetal" {
			t.Errorf("ManagedBy = %q, want %q", host.ManagedBy, "baremetal")
		}
	})

	t.Run("excludes hosts with consumerRef set", func(t *testing.T) {
		bmh := newBMH("host-consumed", defaultLabels(), metal3api.OperationalStatusOK, metal3api.StateAvailable)
		bmh.Spec.ConsumerRef = &corev1.ObjectReference{Name: "some-consumer"}

		m := newMetal3ClientForTest(bmh)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (host has consumerRef), got %+v", host)
		}
	})

	t.Run("excludes hosts with non-ok operational status", func(t *testing.T) {
		bmh := newBMH("host-error", defaultLabels(), metal3api.OperationalStatusError, metal3api.StateAvailable)

		m := newMetal3ClientForTest(bmh)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (host has error status), got %+v", host)
		}
	})

	t.Run("excludes hosts with unacceptable provisioning state", func(t *testing.T) {
		bmh := newBMH("host-provisioning", defaultLabels(), metal3api.OperationalStatusOK, metal3api.StateProvisioning)

		m := newMetal3ClientForTest(bmh)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (host is provisioning), got %+v", host)
		}
	})

	t.Run("filters by host type label", func(t *testing.T) {
		gpuLabels := map[string]string{Metal3HostTypeLabel: "gpu-node", Metal3ManagedByLabel: "baremetal"}
		cpuLabels := map[string]string{Metal3HostTypeLabel: "cpu-node", Metal3ManagedByLabel: "baremetal"}
		gpuHost := withHardwareDetails(newBMH("host-gpu", gpuLabels, metal3api.OperationalStatusOK, metal3api.StateAvailable), testNIC("AA:BB:CC:DD:EE:01"))
		cpuHost := withHardwareDetails(newBMH("host-cpu", cpuLabels, metal3api.OperationalStatusOK, metal3api.StateAvailable), testNIC("AA:BB:CC:DD:EE:02"))

		m := newMetal3ClientForTest(gpuHost, cpuHost)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host == nil {
			t.Fatal("expected gpu host, got nil")
		}
		if host.HostType != "gpu-node" {
			t.Errorf("HostType = %q, want %q", host.HostType, "gpu-node")
		}
	})

	t.Run("filters by managed-by label mismatch", func(t *testing.T) {
		labels := map[string]string{Metal3HostTypeLabel: "gpu-node", Metal3ManagedByLabel: "agent"}
		bmh := newBMH("host-agent", labels, metal3api.OperationalStatusOK, metal3api.StateAvailable)

		m := newMetal3ClientForTest(bmh)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (managedBy mismatch), got %+v", host)
		}
	})

	t.Run("filters by explicit managed-by match expression", func(t *testing.T) {
		labels := map[string]string{Metal3HostTypeLabel: "gpu-node", Metal3ManagedByLabel: "agent"}
		bmh := withHardwareDetails(newBMH("host-agent", labels, metal3api.OperationalStatusOK, metal3api.StateAvailable), testNIC("AA:BB:CC:DD:EE:01"))

		m := newMetal3ClientForTest(bmh)
		host, err := m.FindFreeHost(ctx, map[string]string{
			"hostType":  "gpu-node",
			"managedBy": "agent",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host == nil {
			t.Fatal("expected host (managedBy matches), got nil")
		}
	})

	t.Run("returns nil when no matching hosts exist", func(t *testing.T) {
		m := newMetal3ClientForTest()
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (no hosts), got %+v", host)
		}
	})

	t.Run("matches hosts without hostType filter", func(t *testing.T) {
		labels := map[string]string{Metal3ManagedByLabel: "baremetal"}
		bmh := withHardwareDetails(newBMH("host-any", labels, metal3api.OperationalStatusOK, metal3api.StateAvailable), testNIC("AA:BB:CC:DD:EE:01"))

		m := newMetal3ClientForTest(bmh)
		host, err := m.FindFreeHost(ctx, map[string]string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host == nil {
			t.Fatal("expected host (no hostType filter), got nil")
		}
	})

	t.Run("defaults managed-by to baremetal when label is absent", func(t *testing.T) {
		labels := map[string]string{Metal3HostTypeLabel: "gpu-node"}
		bmh := withHardwareDetails(newBMH("host-no-managed-by", labels, metal3api.OperationalStatusOK, metal3api.StateAvailable), testNIC("AA:BB:CC:DD:EE:01"))

		m := newMetal3ClientForTest(bmh)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host == nil {
			t.Fatal("expected host (managed-by defaults to baremetal), got nil")
		}
		if host.ManagedBy != "baremetal" {
			t.Errorf("ManagedBy = %q, want %q", host.ManagedBy, "baremetal")
		}
	})

	t.Run("excludes hosts with no managed-by label when explicit managedBy filter differs", func(t *testing.T) {
		labels := map[string]string{Metal3HostTypeLabel: "gpu-node"}
		bmh := newBMH("host-no-managed-by", labels, metal3api.OperationalStatusOK, metal3api.StateAvailable)

		m := newMetal3ClientForTest(bmh)
		host, err := m.FindFreeHost(ctx, map[string]string{
			"hostType":  "gpu-node",
			"managedBy": "agent",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (managed-by defaults to baremetal, not agent), got %+v", host)
		}
	})

	t.Run("skips host with inspect.metal3.io disabled annotation", func(t *testing.T) {
		bmh := withHardwareDetails(newBMH("host-inspect-disabled", defaultLabels(), metal3api.OperationalStatusOK, metal3api.StateAvailable), testNIC("AA:BB:CC:DD:EE:01"))
		if bmh.Annotations == nil {
			bmh.Annotations = map[string]string{}
		}
		bmh.Annotations["inspect.metal3.io"] = "disabled"

		m := newMetal3ClientForTest(bmh)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (inspection disabled), got %+v", host)
		}
	})

	t.Run("includes host when inspect.metal3.io annotation has non-disabled value", func(t *testing.T) {
		bmh := withHardwareDetails(newBMH("host-inspect-other", defaultLabels(), metal3api.OperationalStatusOK, metal3api.StateAvailable), testNIC("AA:BB:CC:DD:EE:01"))
		if bmh.Annotations == nil {
			bmh.Annotations = map[string]string{}
		}
		bmh.Annotations["inspect.metal3.io"] = "metadata"

		m := newMetal3ClientForTest(bmh)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host == nil {
			t.Fatal("expected host (inspect annotation is not disabled), got nil")
		}
	})

	t.Run("skips host with nil HardwareDetails", func(t *testing.T) {
		bmh := newBMH("host-no-hardware", defaultLabels(), metal3api.OperationalStatusOK, metal3api.StateAvailable)

		m := newMetal3ClientForTest(bmh)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (HardwareDetails is nil), got %+v", host)
		}
	})

	t.Run("skips host with empty NIC list", func(t *testing.T) {
		bmh := withHardwareDetails(newBMH("host-empty-nics", defaultLabels(), metal3api.OperationalStatusOK, metal3api.StateAvailable))

		m := newMetal3ClientForTest(bmh)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (HardwareDetails has no NICs), got %+v", host)
		}
	})

	t.Run("selects host with HardwareDetails when multiple candidates exist", func(t *testing.T) {
		noHardware := newBMH("host-no-hw", defaultLabels(), metal3api.OperationalStatusOK, metal3api.StateAvailable)
		withHW := withHardwareDetails(newBMH("host-with-hw", defaultLabels(), metal3api.OperationalStatusOK, metal3api.StateAvailable), testNIC("AA:BB:CC:DD:EE:01"))

		m := newMetal3ClientForTest(noHardware, withHW)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host == nil {
			t.Fatal("expected host (one candidate has HardwareDetails), got nil")
		}
		if host.Name != "host-with-hw" {
			t.Errorf("expected host-with-hw, got %q", host.Name)
		}
	})
}

// --- AssignHost ---

func TestAssignHost(t *testing.T) {
	ctx := context.Background()

	t.Run("assigns host with labels and consumerRef", func(t *testing.T) {
		bmh := newBMH("host-1", defaultLabels(), metal3api.OperationalStatusOK, metal3api.StateAvailable)

		m := newMetal3ClientForTest(bmh)
		host, err := m.AssignHost(ctx, testNamespace+"/host-1", "instance-123", map[string]string{
			"profileName": "myProfile",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host == nil {
			t.Fatal("expected assigned host, got nil")
		}
		if host.BareMetalInstanceID != "instance-123" {
			t.Errorf("BareMetalInstanceID = %q, want %q", host.BareMetalInstanceID, "instance-123")
		}

		updatedBMH := &metal3api.BareMetalHost{}
		if err := m.client.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "host-1"}, updatedBMH); err != nil {
			t.Fatalf("failed to get updated BMH: %v", err)
		}
		if updatedBMH.Labels[metal3LabelPrefix+"profileName"] != "myProfile" {
			t.Errorf("profileName label = %q, want %q", updatedBMH.Labels[metal3LabelPrefix+"profileName"], "myProfile")
		}
		if updatedBMH.Spec.ConsumerRef == nil {
			t.Fatal("consumerRef should be set")
		}
		if updatedBMH.Spec.ConsumerRef.Name != "instance-123" {
			t.Errorf("consumerRef.Name = %q, want %q", updatedBMH.Spec.ConsumerRef.Name, "instance-123")
		}
	})

	t.Run("returns nil if host has consumerRef for a different consumer", func(t *testing.T) {
		bmh := newBMH("host-taken", defaultLabels(), metal3api.OperationalStatusOK, metal3api.StateAvailable)
		bmh.Spec.ConsumerRef = &corev1.ObjectReference{Name: "other-instance"}

		m := newMetal3ClientForTest(bmh)
		host, err := m.AssignHost(ctx, testNamespace+"/host-taken", "my-instance", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (host taken by other), got %+v", host)
		}
	})

	t.Run("succeeds if host is already assigned to the same instance", func(t *testing.T) {
		bmh := newBMH("host-mine", defaultLabels(), metal3api.OperationalStatusOK, metal3api.StateAvailable)
		bmh.Spec.ConsumerRef = &corev1.ObjectReference{Name: "my-instance"}

		m := newMetal3ClientForTest(bmh)
		host, err := m.AssignHost(ctx, testNamespace+"/host-mine", "my-instance", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host == nil {
			t.Fatal("expected host (idempotent assign), got nil")
		}
	})

	t.Run("returns error for empty inventoryHostID", func(t *testing.T) {
		m := newMetal3ClientForTest()
		_, err := m.AssignHost(ctx, "", "instance-123", nil)
		if err == nil {
			t.Fatal("expected error for empty hostID, got nil")
		}
	})

	t.Run("returns error for empty bareMetalInstanceID", func(t *testing.T) {
		m := newMetal3ClientForTest()
		_, err := m.AssignHost(ctx, testNamespace+"/host-1", "", nil)
		if err == nil {
			t.Fatal("expected error for empty instanceID, got nil")
		}
	})

	t.Run("returns error for invalid host ID format", func(t *testing.T) {
		m := newMetal3ClientForTest()
		_, err := m.AssignHost(ctx, "no-slash", "instance-123", nil)
		if err == nil {
			t.Fatal("expected error for invalid hostID, got nil")
		}
	})
}

// --- UnassignHost ---

func TestUnassignHost(t *testing.T) {
	ctx := context.Background()

	t.Run("removes labels and clears consumerRef", func(t *testing.T) {
		bmh := newBMH("host-1", map[string]string{
			Metal3HostTypeLabel:               "gpu-node",
			Metal3ManagedByLabel:              "baremetal",
			metal3LabelPrefix + "profileName": "myProfile",
		}, metal3api.OperationalStatusOK, metal3api.StateAvailable)
		bmh.Spec.ConsumerRef = &corev1.ObjectReference{Name: "instance-123"}

		m := newMetal3ClientForTest(bmh)
		err := m.UnassignHost(ctx, testNamespace+"/host-1", []string{"profileName"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		updatedBMH := &metal3api.BareMetalHost{}
		if err := m.client.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "host-1"}, updatedBMH); err != nil {
			t.Fatalf("failed to get updated BMH: %v", err)
		}
		if _, ok := updatedBMH.Labels[metal3LabelPrefix+"profileName"]; ok {
			t.Error("profileName label should have been removed")
		}
		if updatedBMH.Labels[Metal3ManagedByLabel] != "baremetal" {
			t.Error("managedBy label should not have been removed")
		}
		if updatedBMH.Spec.ConsumerRef != nil {
			t.Error("consumerRef should have been cleared")
		}
	})

	t.Run("handles no additional labels to remove", func(t *testing.T) {
		bmh := newBMH("host-2", map[string]string{
			Metal3ManagedByLabel: "baremetal",
		}, metal3api.OperationalStatusOK, metal3api.StateAvailable)
		bmh.Spec.ConsumerRef = &corev1.ObjectReference{Name: "instance-456"}

		m := newMetal3ClientForTest(bmh)
		err := m.UnassignHost(ctx, testNamespace+"/host-2", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		updatedBMH := &metal3api.BareMetalHost{}
		if err := m.client.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "host-2"}, updatedBMH); err != nil {
			t.Fatalf("failed to get updated BMH: %v", err)
		}
		if updatedBMH.Spec.ConsumerRef != nil {
			t.Error("consumerRef should have been cleared")
		}
	})

	t.Run("returns error for invalid host ID", func(t *testing.T) {
		m := newMetal3ClientForTest()
		err := m.UnassignHost(ctx, "invalid-id", nil)
		if err == nil {
			t.Fatal("expected error for invalid hostID, got nil")
		}
	})
}

// --- GetHostNICs ---

func TestGetHostNICs(t *testing.T) {
	ctx := context.Background()

	t.Run("returns lowercased MACs from HardwareDetails NIC list", func(t *testing.T) {
		bmh := withHardwareDetails(
			newBMH("host-1", defaultLabels(), metal3api.OperationalStatusOK, metal3api.StateAvailable),
			testNIC("AA:BB:CC:DD:EE:01"),
			testNIC("aa:bb:cc:dd:ee:02"),
			testNIC("FF:00:11:22:33:44"),
		)

		m := newMetal3ClientForTest(bmh)
		nics, err := m.GetHostNICs(ctx, testNamespace+"/host-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(nics) != 3 {
			t.Fatalf("expected 3 NICs, got %d", len(nics))
		}
		wantMACs := []string{"aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:02", "ff:00:11:22:33:44"}
		for i, want := range wantMACs {
			if nics[i].MAC != want {
				t.Errorf("NIC[%d].MAC = %q, want %q", i, nics[i].MAC, want)
			}
		}
	})

	t.Run("returns empty slice when HardwareDetails is nil", func(t *testing.T) {
		bmh := newBMH("host-no-hw", defaultLabels(), metal3api.OperationalStatusOK, metal3api.StateAvailable)

		m := newMetal3ClientForTest(bmh)
		nics, err := m.GetHostNICs(ctx, testNamespace+"/host-no-hw")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(nics) != 0 {
			t.Errorf("expected empty NIC list, got %v", nics)
		}
	})

	t.Run("returns error when NIC list is empty despite completed inspection", func(t *testing.T) {
		bmh := withHardwareDetails(newBMH("host-empty-nics", defaultLabels(), metal3api.OperationalStatusOK, metal3api.StateAvailable))

		m := newMetal3ClientForTest(bmh)
		_, err := m.GetHostNICs(ctx, testNamespace+"/host-empty-nics")
		if err == nil {
			t.Fatal("expected error for empty NIC list, got nil")
		}
	})

	t.Run("returns error for invalid inventoryHostID format", func(t *testing.T) {
		m := newMetal3ClientForTest()
		_, err := m.GetHostNICs(ctx, "invalid-no-slash")
		if err == nil {
			t.Fatal("expected error for invalid hostID, got nil")
		}
	})

	t.Run("returns error when BareMetalHost not found", func(t *testing.T) {
		m := newMetal3ClientForTest()
		_, err := m.GetHostNICs(ctx, testNamespace+"/nonexistent-host")
		if err == nil {
			t.Fatal("expected error for missing BMH, got nil")
		}
	})
}

// --- parseMetal3Namespace ---

func TestParseMetal3Namespace(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *Config
		want      string
		wantError bool
	}{
		{
			name: "valid config",
			cfg: &Config{
				Options: map[string]any{
					"metal3": map[string]any{
						"namespace": "openshift-machine-api",
					},
				},
			},
			want: "openshift-machine-api",
		},
		{
			name: "missing metal3 key",
			cfg: &Config{
				Options: map[string]any{},
			},
			wantError: true,
		},
		{
			name: "empty namespace",
			cfg: &Config{
				Options: map[string]any{
					"metal3": map[string]any{
						"namespace": "",
					},
				},
			},
			wantError: true,
		},
		{
			name: "missing namespace key",
			cfg: &Config{
				Options: map[string]any{
					"metal3": map[string]any{},
				},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMetal3Namespace(tt.cfg)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("namespace = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- init registration ---

func TestMetal3BackendRegistration(t *testing.T) {
	t.Run("metal3 backend is registered in newClientFuncs", func(t *testing.T) {
		if _, ok := newClientFuncs["metal3"]; !ok {
			t.Fatal("metal3 backend not registered in newClientFuncs")
		}
	})
}
