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
	"reflect"
	"strings"
	"testing"

	metal3api "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const (
	testNamespace = "test-bmaas"
	testHostClass = "metal3"
)

func TestGetHostLogicalPortMACs_Metal3(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		hostID      string
		missingHost bool
		want        map[string]string
		wantErr     bool
	}{
		{
			name: "logical port names and MACs are preserved",
			annotations: map[string]string{
				HostInterfaceMACsAnnotation: `{"data-0":"52:54:00:16:04:83","data-1":"52:54:00:AA:BB:CC"}`,
			},
			want: map[string]string{"data-0": "52:54:00:16:04:83", "data-1": "52:54:00:AA:BB:CC"},
		},
		{name: "absent annotation", want: map[string]string{}},
		{name: "empty annotation", annotations: map[string]string{HostInterfaceMACsAnnotation: ""}, want: map[string]string{}},
		{name: "empty mapping", annotations: map[string]string{HostInterfaceMACsAnnotation: `{}`}, want: map[string]string{}},
		{name: "null mapping", annotations: map[string]string{HostInterfaceMACsAnnotation: `null`}},
		{name: "malformed JSON", annotations: map[string]string{HostInterfaceMACsAnnotation: "not-json"}, wantErr: true},
		{name: "wrong shape", annotations: map[string]string{HostInterfaceMACsAnnotation: `[]`}, wantErr: true},
		{name: "non-string MAC", annotations: map[string]string{HostInterfaceMACsAnnotation: `{"data-0":42}`}, wantErr: true},
		{name: "missing host", missingHost: true, wantErr: true},
		{name: "invalid host ID", hostID: "bad-id", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			if !tt.missingHost {
				objects = append(objects, newBMHBuilder("host-macs").WithAnnotations(tt.annotations).Build())
			}
			m := newMetal3ClientForTest(objects...)
			hostID := tt.hostID
			if hostID == "" {
				hostID = testNamespace + "/host-macs"
			}
			got, err := m.GetHostLogicalPortMACs(context.Background(), hostID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetHostLogicalPortMACs error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetHostLogicalPortMACs = %v, want %v", got, tt.want)
			}
		})
	}
}

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

// newMetal3ClientNoHardwareDataCRD builds a client that behaves as if the
// HardwareData CRD is not installed (older Metal3): any Get/List of a
// HardwareData object returns a NoMatchError, while BareMetalHost operations
// pass through to the underlying fake client. This exercises the fallback to
// BareMetalHost.Status.HardwareDetails.
func newMetal3ClientNoHardwareDataCRD(objects ...client.Object) *Metal3Client {
	noMatch := &meta.NoKindMatchError{
		GroupKind: schema.GroupKind{Group: "metal3.io", Kind: "HardwareData"},
	}
	isHardwareData := func(obj runtime.Object) bool {
		switch obj.(type) {
		case *metal3api.HardwareData, *metal3api.HardwareDataList:
			return true
		default:
			return false
		}
	}
	scheme := newTestScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&metal3api.BareMetalHost{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if isHardwareData(obj) {
					return noMatch
				}
				return c.Get(ctx, key, obj, opts...)
			},
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if isHardwareData(list) {
					return noMatch
				}
				return c.List(ctx, list, opts...)
			},
		}).
		Build()
	return &Metal3Client{
		client:    fakeClient,
		namespace: testNamespace,
		hostClass: testHostClass,
	}
}

func defaultLabels() map[string]string {
	return map[string]string{
		Metal3HostTypeLabel:  "gpu-node",
		Metal3ManagedByLabel: "baremetal",
	}
}

func testNIC(mac string) metal3api.NIC {
	return metal3api.NIC{MAC: mac}
}

type bmhBuilder struct {
	name             string
	labels           map[string]string
	annotations      map[string]string
	opStatus         metal3api.OperationalStatus
	provState        metal3api.ProvisioningState
	consumerRef      *corev1.ObjectReference
	inspectionMode   metal3api.InspectionMode
	statusNICs       []metal3api.NIC
	hasStatusNICs    bool
	hardwareDataNICs []metal3api.NIC
	hasHardwareData  bool
}

func newBMHBuilder(name string) *bmhBuilder {
	return &bmhBuilder{
		name:      name,
		labels:    defaultLabels(),
		opStatus:  metal3api.OperationalStatusOK,
		provState: metal3api.StateAvailable,
	}
}

func (b *bmhBuilder) WithLabels(labels map[string]string) *bmhBuilder {
	b.labels = labels
	return b
}

func (b *bmhBuilder) WithAnnotations(annotations map[string]string) *bmhBuilder {
	b.annotations = annotations
	return b
}

func (b *bmhBuilder) WithOpStatus(opStatus metal3api.OperationalStatus) *bmhBuilder {
	b.opStatus = opStatus
	return b
}

func (b *bmhBuilder) WithProvState(provState metal3api.ProvisioningState) *bmhBuilder {
	b.provState = provState
	return b
}

func (b *bmhBuilder) WithConsumerRef(ref *corev1.ObjectReference) *bmhBuilder {
	b.consumerRef = ref
	return b
}

func (b *bmhBuilder) WithInspectionMode(mode metal3api.InspectionMode) *bmhBuilder {
	b.inspectionMode = mode
	return b
}

// WithHardwareDataNICs seeds the companion HardwareData resource (the preferred
// NIC source). Calling it with no NICs seeds a present-but-empty HardwareData.
func (b *bmhBuilder) WithHardwareDataNICs(nics ...metal3api.NIC) *bmhBuilder {
	b.hasHardwareData = true
	b.hardwareDataNICs = nics
	return b
}

// WithStatusNICs seeds the deprecated bmh.Status.HardwareDetails (the fallback
// NIC source).
func (b *bmhBuilder) WithStatusNICs(nics ...metal3api.NIC) *bmhBuilder {
	b.hasStatusNICs = true
	b.statusNICs = nics
	return b
}

// WithNICs seeds the companion HardwareData source — the new default NIC
// source — so existing call sites exercise the primary read path.
func (b *bmhBuilder) WithNICs(nics ...metal3api.NIC) *bmhBuilder {
	return b.WithHardwareDataNICs(nics...)
}

func (b *bmhBuilder) Build() *metal3api.BareMetalHost {
	labels := b.labels
	if labels == nil {
		labels = map[string]string{}
	}
	bmh := &metal3api.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{
			Name:        b.name,
			Namespace:   testNamespace,
			Labels:      labels,
			Annotations: b.annotations,
		},
		Spec: metal3api.BareMetalHostSpec{
			ConsumerRef:    b.consumerRef,
			InspectionMode: b.inspectionMode,
		},
		Status: metal3api.BareMetalHostStatus{
			OperationalStatus: b.opStatus,
			Provisioning: metal3api.ProvisionStatus{
				State: b.provState,
			},
		},
	}
	if b.hasStatusNICs {
		bmh.Status.HardwareDetails = &metal3api.HardwareDetails{NIC: b.statusNICs}
	}
	return bmh
}

// BuildObjects returns the BMH plus a companion HardwareData object when the
// HardwareData source was seeded (including the present-but-empty case). When
// only the status source is seeded, no HardwareData is emitted — modelling an
// absent HardwareData object and exercising the fallback path.
func (b *bmhBuilder) BuildObjects() []client.Object {
	objs := []client.Object{b.Build()}
	if b.hasHardwareData {
		objs = append(objs, hardwareDataFor(b.name, b.hardwareDataNICs))
	}
	return objs
}

func hardwareDataFor(name string, nics []metal3api.NIC) *metal3api.HardwareData {
	return &metal3api.HardwareData{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: metal3api.HardwareDataSpec{
			HardwareDetails: &metal3api.HardwareDetails{NIC: nics},
		},
	}
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

// --- validateMetal3MatchExpressions ---

func TestValidateMetal3MatchExpressions(t *testing.T) {
	tests := []struct {
		name             string
		matchExpressions map[string]string
		wantError        bool
		wantErrorMsg     string
	}{
		{
			name:             "valid single label",
			matchExpressions: map[string]string{"hardware-profile": "gpu-large"},
			wantError:        false,
		},
		{
			name: "valid multiple labels",
			matchExpressions: map[string]string{
				"hardware-profile": "gpu-large",
				"rack":             "rack-01",
				"datacenter":       "dc-west",
			},
			wantError: false,
		},
		{
			name:             "empty map is valid",
			matchExpressions: map[string]string{},
			wantError:        false,
		},
		{
			name:             "nil map is valid",
			matchExpressions: nil,
			wantError:        false,
		},
		{
			name:             "empty string key is invalid",
			matchExpressions: map[string]string{"": "value"},
			wantError:        true,
			wantErrorMsg:     "not a valid label key",
		},
		{
			name:             "key with spaces is invalid",
			matchExpressions: map[string]string{"key with spaces": "value"},
			wantError:        true,
			wantErrorMsg:     "not a valid label key",
		},
		{
			name:             "invalid label value is rejected",
			matchExpressions: map[string]string{"datacenter": "bad value"},
			wantError:        true,
			wantErrorMsg:     "not a valid label value",
		},
		{
			name:             "empty value is rejected",
			matchExpressions: map[string]string{"datacenter": ""},
			wantError:        true,
			wantErrorMsg:     "empty value not allowed",
		},
		{
			name:             "managedBy key is allowed (specially handled)",
			matchExpressions: map[string]string{"managedBy": "baremetal"},
			wantError:        false,
		},
		{
			name:             "provisionState key is allowed",
			matchExpressions: map[string]string{"provisionState": "available"},
			wantError:        false,
		},
		{
			name: "mix of regular and special keys is valid",
			matchExpressions: map[string]string{
				"hardware-profile": "gpu-large",
				"managedBy":        "baremetal",
			},
			wantError: false,
		},
		{
			name:             "hostType key is allowed (legacy compatibility)",
			matchExpressions: map[string]string{"hostType": "gpu-node"},
			wantError:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMetal3MatchExpressions(tt.matchExpressions)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrorMsg)
				}
				if !strings.Contains(err.Error(), tt.wantErrorMsg) {
					t.Errorf("expected error containing %q, got %q", tt.wantErrorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

// --- FindFreeHost ---

//nolint:gocyclo
func TestFindFreeHost(t *testing.T) {
	ctx := context.Background()

	t.Run("returns matching unassigned host", func(t *testing.T) {
		objs := newBMHBuilder("host-1").WithNICs(testNIC("AA:BB:CC:DD:EE:01")).BuildObjects()

		m := newMetal3ClientForTest(objs...)
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
		bmh := newBMHBuilder("host-consumed").WithConsumerRef(&corev1.ObjectReference{Name: "some-consumer"}).Build()

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
		bmh := newBMHBuilder("host-error").WithOpStatus(metal3api.OperationalStatusError).Build()

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
		bmh := newBMHBuilder("host-provisioning").WithProvState(metal3api.StateProvisioning).Build()

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
		gpuObjs := newBMHBuilder("host-gpu").WithLabels(gpuLabels).WithNICs(testNIC("AA:BB:CC:DD:EE:01")).BuildObjects()
		cpuObjs := newBMHBuilder("host-cpu").WithLabels(cpuLabels).WithNICs(testNIC("AA:BB:CC:DD:EE:02")).BuildObjects()

		m := newMetal3ClientForTest(append(gpuObjs, cpuObjs...)...)
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
		bmh := newBMHBuilder("host-agent").WithLabels(labels).Build()

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
		objs := newBMHBuilder("host-agent").WithLabels(labels).WithNICs(testNIC("AA:BB:CC:DD:EE:01")).BuildObjects()

		m := newMetal3ClientForTest(objs...)
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
		objs := newBMHBuilder("host-any").WithLabels(labels).WithNICs(testNIC("AA:BB:CC:DD:EE:01")).BuildObjects()

		m := newMetal3ClientForTest(objs...)
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
		objs := newBMHBuilder("host-no-managed-by").WithLabels(labels).WithNICs(testNIC("AA:BB:CC:DD:EE:01")).BuildObjects()

		m := newMetal3ClientForTest(objs...)
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
		bmh := newBMHBuilder("host-no-managed-by").WithLabels(labels).Build()

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

	t.Run("selects host when only Status.HardwareDetails has NICs", func(t *testing.T) {
		objs := newBMHBuilder("host-status-nics").WithStatusNICs(testNIC("AA:BB:CC:DD:EE:01")).BuildObjects()

		m := newMetal3ClientForTest(objs...)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host == nil {
			t.Fatal("expected host (Status.HardwareDetails fallback), got nil")
		}
		if host.Name != "host-status-nics" {
			t.Errorf("expected host-status-nics, got %q", host.Name)
		}
	})

	t.Run("falls back to Status.HardwareDetails when HardwareData CRD is not installed", func(t *testing.T) {
		bmh := newBMHBuilder("host-no-hd-crd").
			WithStatusNICs(testNIC("AA:BB:CC:DD:EE:01")).
			Build()

		m := newMetal3ClientNoHardwareDataCRD(bmh)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error (missing HardwareData CRD should not fail host selection): %v", err)
		}
		if host == nil {
			t.Fatal("expected host from Status.HardwareDetails fallback, got nil")
		}
	})

	t.Run("skips host with inspect.metal3.io disabled annotation", func(t *testing.T) {
		objs := newBMHBuilder("host-inspect-disabled").
			WithAnnotations(map[string]string{"inspect.metal3.io": "disabled"}).
			WithNICs(testNIC("AA:BB:CC:DD:EE:01")).
			BuildObjects()

		m := newMetal3ClientForTest(objs...)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (inspection disabled), got %+v", host)
		}
	})

	t.Run("skips host with InspectionMode disabled field", func(t *testing.T) {
		objs := newBMHBuilder("host-inspect-mode-disabled").
			WithInspectionMode(metal3api.InspectionModeDisabled).
			WithNICs(testNIC("AA:BB:CC:DD:EE:01")).
			BuildObjects()

		m := newMetal3ClientForTest(objs...)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (InspectionMode disabled), got %+v", host)
		}
	})

	t.Run("includes host when inspect.metal3.io annotation has non-disabled value", func(t *testing.T) {
		objs := newBMHBuilder("host-inspect-other").
			WithAnnotations(map[string]string{"inspect.metal3.io": "metadata"}).
			WithNICs(testNIC("AA:BB:CC:DD:EE:01")).
			BuildObjects()

		m := newMetal3ClientForTest(objs...)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host == nil {
			t.Fatal("expected host (inspect annotation is not disabled), got nil")
		}
	})

	t.Run("skips host when neither source has NICs", func(t *testing.T) {
		bmh := newBMHBuilder("host-no-nics").Build()

		m := newMetal3ClientForTest(bmh)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (no NIC data in either source), got %+v", host)
		}
	})

	t.Run("skips host when HardwareData is present but empty and status is empty", func(t *testing.T) {
		objs := newBMHBuilder("host-empty-hw").WithHardwareDataNICs().BuildObjects()

		m := newMetal3ClientForTest(objs...)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host != nil {
			t.Errorf("expected nil (empty HardwareData, empty status), got %+v", host)
		}
	})

	t.Run("selects the candidate that has NIC data when others do not", func(t *testing.T) {
		noNICs := newBMHBuilder("host-no-nics").BuildObjects()
		withNICs := newBMHBuilder("host-with-nics").WithNICs(testNIC("AA:BB:CC:DD:EE:01")).BuildObjects()

		m := newMetal3ClientForTest(append(noNICs, withNICs...)...)
		host, err := m.FindFreeHost(ctx, map[string]string{"hostType": "gpu-node"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host == nil {
			t.Fatal("expected host (one candidate has NIC data), got nil")
		}
		if host.Name != "host-with-nics" {
			t.Errorf("expected host-with-nics, got %q", host.Name)
		}
	})

	t.Run("empty matchExpressions returns any available host", func(t *testing.T) {
		labels := map[string]string{Metal3ManagedByLabel: "baremetal"}
		objs := newBMHBuilder("host-any").WithLabels(labels).WithOpStatus(metal3api.OperationalStatusOK).WithProvState(metal3api.StateAvailable).WithNICs(testNIC("AA:BB:CC:DD:EE:FF")).BuildObjects()

		m := newMetal3ClientForTest(objs...)

		// No label filters - should return any available host
		result, err := m.FindFreeHost(ctx, map[string]string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected host (no filters), got nil")
		}
		if result.Name != "host-any" {
			t.Errorf("got host %q, want %q", result.Name, "host-any")
		}
	})

	t.Run("returns error for invalid match expressions", func(t *testing.T) {
		m := newMetal3ClientForTest()

		// Empty key should be rejected
		_, err := m.FindFreeHost(ctx, map[string]string{"": "value"})
		if err == nil {
			t.Fatal("expected error for empty key, got nil")
		}
		if !strings.Contains(err.Error(), "not a valid label key") {
			t.Errorf("expected error about invalid label key, got %q", err.Error())
		}
	})

	t.Run("filters by arbitrary label key-value pairs", func(t *testing.T) {
		// Create hosts with different label combinations
		gpuLabels := map[string]string{
			"osac.openshift.io/hardware-profile": "gpu-large",
			"datacenter":                         "dc-west",
			Metal3ManagedByLabel:                 "baremetal",
		}
		cpuLabels := map[string]string{
			"osac.openshift.io/hardware-profile": "cpu-standard",
			"datacenter":                         "dc-east",
			Metal3ManagedByLabel:                 "baremetal",
		}
		gpuObjs := newBMHBuilder("host-gpu").WithLabels(gpuLabels).WithOpStatus(metal3api.OperationalStatusOK).WithProvState(metal3api.StateAvailable).WithNICs(testNIC("AA:BB:CC:DD:EE:01")).BuildObjects()
		cpuObjs := newBMHBuilder("host-cpu").WithLabels(cpuLabels).WithOpStatus(metal3api.OperationalStatusOK).WithProvState(metal3api.StateAvailable).WithNICs(testNIC("AA:BB:CC:DD:EE:02")).BuildObjects()

		m := newMetal3ClientForTest(append(gpuObjs, cpuObjs...)...)

		// Filter by hardware-profile label
		host, err := m.FindFreeHost(ctx, map[string]string{
			"osac.openshift.io/hardware-profile": "gpu-large",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host == nil {
			t.Fatal("expected host (gpu-large profile), got nil")
		}
		if host.Name != "host-gpu" {
			t.Errorf("expected host-gpu, got %q", host.Name)
		}
	})

	t.Run("filters by multiple label requirements (AND semantics)", func(t *testing.T) {
		// Create hosts with different label combinations
		matchingLabels := map[string]string{
			"osac.openshift.io/hardware-profile": "gpu-large",
			"datacenter":                         "dc-west",
			"rack":                               "rack-01",
			Metal3ManagedByLabel:                 "baremetal",
		}
		partialLabels := map[string]string{
			"osac.openshift.io/hardware-profile": "gpu-large",
			"datacenter":                         "dc-east", // Different datacenter
			"rack":                               "rack-01",
			Metal3ManagedByLabel:                 "baremetal",
		}
		matchingObjs := newBMHBuilder("host-matching").WithLabels(matchingLabels).WithOpStatus(metal3api.OperationalStatusOK).WithProvState(metal3api.StateAvailable).WithNICs(testNIC("AA:BB:CC:DD:EE:03")).BuildObjects()
		partialObjs := newBMHBuilder("host-partial").WithLabels(partialLabels).WithOpStatus(metal3api.OperationalStatusOK).WithProvState(metal3api.StateAvailable).WithNICs(testNIC("AA:BB:CC:DD:EE:04")).BuildObjects()

		m := newMetal3ClientForTest(append(matchingObjs, partialObjs...)...)

		// Require both hardware-profile AND datacenter to match
		host, err := m.FindFreeHost(ctx, map[string]string{
			"osac.openshift.io/hardware-profile": "gpu-large",
			"datacenter":                         "dc-west",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if host == nil {
			t.Fatal("expected host (matching both labels), got nil")
		}
		if host.Name != "host-matching" {
			t.Errorf("expected host-matching, got %q", host.Name)
		}
	})
}

// --- AssignHost ---

func TestAssignHost(t *testing.T) {
	ctx := context.Background()

	t.Run("assigns host with labels and consumerRef", func(t *testing.T) {
		bmh := newBMHBuilder("host-1").Build()

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
		bmh := newBMHBuilder("host-taken").WithConsumerRef(&corev1.ObjectReference{Name: "other-instance"}).Build()

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
		bmh := newBMHBuilder("host-mine").WithConsumerRef(&corev1.ObjectReference{Name: "my-instance"}).Build()

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
	t.Run("assigns label-filtered host and sets consumerRef correctly", func(t *testing.T) {
		// Create hosts with different labels - only one should match the filter
		matchingLabels := map[string]string{
			"osac.openshift.io/hardware-profile": "gpu-large",
			"datacenter":                         "dc-west",
			Metal3ManagedByLabel:                 "baremetal",
		}
		nonMatchingLabels := map[string]string{
			"osac.openshift.io/hardware-profile": "cpu-standard",
			"datacenter":                         "dc-east",
			Metal3ManagedByLabel:                 "baremetal",
		}
		matchingObjs := newBMHBuilder("host-gpu").WithLabels(matchingLabels).WithOpStatus(metal3api.OperationalStatusOK).WithProvState(metal3api.StateAvailable).WithNICs(testNIC("AA:BB:CC:DD:EE:05")).BuildObjects()
		nonMatchingObjs := newBMHBuilder("host-cpu").WithLabels(nonMatchingLabels).WithOpStatus(metal3api.OperationalStatusOK).WithProvState(metal3api.StateAvailable).WithNICs(testNIC("AA:BB:CC:DD:EE:06")).BuildObjects()

		m := newMetal3ClientForTest(append(matchingObjs, nonMatchingObjs...)...)

		// First, find the host using label filtering
		foundHost, err := m.FindFreeHost(ctx, map[string]string{
			"osac.openshift.io/hardware-profile": "gpu-large",
			"datacenter":                         "dc-west",
		})
		if err != nil {
			t.Fatalf("unexpected error during FindFreeHost: %v", err)
		}
		if foundHost == nil {
			t.Fatal("expected to find matching host, got nil")
		}
		if foundHost.Name != "host-gpu" {
			t.Errorf("found host %q, want %q", foundHost.Name, "host-gpu")
		}

		// Now assign that specific host
		assignedHost, err := m.AssignHost(ctx, foundHost.InventoryHostID, "instance-456", map[string]string{
			"profileName": "gpu-profile",
		})
		if err != nil {
			t.Fatalf("unexpected error during AssignHost: %v", err)
		}
		if assignedHost == nil {
			t.Fatal("expected assigned host, got nil")
		}
		if assignedHost.BareMetalInstanceID != "instance-456" {
			t.Errorf("BareMetalInstanceID = %q, want %q", assignedHost.BareMetalInstanceID, "instance-456")
		}

		// Verify the BareMetalHost object has the correct ConsumerRef
		updatedBMH := &metal3api.BareMetalHost{}
		if err := m.client.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "host-gpu"}, updatedBMH); err != nil {
			t.Fatalf("failed to get updated BMH: %v", err)
		}

		if updatedBMH.Spec.ConsumerRef == nil {
			t.Fatal("expected ConsumerRef to be set, got nil")
		}
		if updatedBMH.Spec.ConsumerRef.Name != "instance-456" {
			t.Errorf("ConsumerRef.Name = %q, want %q", updatedBMH.Spec.ConsumerRef.Name, "instance-456")
		}
		if updatedBMH.Spec.ConsumerRef.Kind != "BareMetalInstance" {
			t.Errorf("ConsumerRef.Kind = %q, want %q", updatedBMH.Spec.ConsumerRef.Kind, "BareMetalInstance")
		}

		// Verify the profile label was added with the correct prefix
		expectedProfileLabel := metal3LabelPrefix + "profileName"
		if updatedBMH.Labels[expectedProfileLabel] != "gpu-profile" {
			t.Errorf("profile label %q = %q, want %q", expectedProfileLabel, updatedBMH.Labels[expectedProfileLabel], "gpu-profile")
		}
	})
}

// --- UnassignHost ---

func TestUnassignHost(t *testing.T) {
	ctx := context.Background()

	t.Run("removes labels and clears consumerRef", func(t *testing.T) {
		bmh := newBMHBuilder("host-1").
			WithLabels(map[string]string{
				Metal3HostTypeLabel:               "gpu-node",
				Metal3ManagedByLabel:              "baremetal",
				metal3LabelPrefix + "profileName": "myProfile",
			}).
			WithConsumerRef(&corev1.ObjectReference{Name: "instance-123"}).
			Build()

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
		bmh := newBMHBuilder("host-2").
			WithLabels(map[string]string{Metal3ManagedByLabel: "baremetal"}).
			WithConsumerRef(&corev1.ObjectReference{Name: "instance-456"}).
			Build()

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

	t.Run("returns lowercased MACs from HardwareData when present", func(t *testing.T) {
		objs := newBMHBuilder("host-1").WithHardwareDataNICs(
			testNIC("AA:BB:CC:DD:EE:01"),
			testNIC("aa:bb:cc:dd:ee:02"),
			testNIC("FF:00:11:22:33:44"),
		).BuildObjects()

		m := newMetal3ClientForTest(objs...)
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

	t.Run("returns lowercased MACs from Status.HardwareDetails when HardwareData is absent", func(t *testing.T) {
		bmh := newBMHBuilder("host-status-only").WithStatusNICs(
			testNIC("AA:BB:CC:DD:EE:01"),
			testNIC("FF:00:11:22:33:44"),
		).Build()

		m := newMetal3ClientForTest(bmh)
		nics, err := m.GetHostNICs(ctx, testNamespace+"/host-status-only")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantMACs := []string{"aa:bb:cc:dd:ee:01", "ff:00:11:22:33:44"}
		if len(nics) != len(wantMACs) {
			t.Fatalf("expected %d NICs, got %d", len(wantMACs), len(nics))
		}
		for i, want := range wantMACs {
			if nics[i].MAC != want {
				t.Errorf("NIC[%d].MAC = %q, want %q", i, nics[i].MAC, want)
			}
		}
	})

	t.Run("falls back to Status.HardwareDetails when HardwareData CRD is not installed", func(t *testing.T) {
		bmh := newBMHBuilder("host-no-hd-crd").WithStatusNICs(
			testNIC("AA:BB:CC:DD:EE:01"),
			testNIC("FF:00:11:22:33:44"),
		).Build()

		m := newMetal3ClientNoHardwareDataCRD(bmh)
		nics, err := m.GetHostNICs(ctx, testNamespace+"/host-no-hd-crd")
		if err != nil {
			t.Fatalf("unexpected error (missing HardwareData CRD should not fail NIC lookup): %v", err)
		}
		wantMACs := []string{"aa:bb:cc:dd:ee:01", "ff:00:11:22:33:44"}
		if len(nics) != len(wantMACs) {
			t.Fatalf("expected %d NICs, got %d", len(wantMACs), len(nics))
		}
		for i, want := range wantMACs {
			if nics[i].MAC != want {
				t.Errorf("NIC[%d].MAC = %q, want %q", i, nics[i].MAC, want)
			}
		}
	})

	t.Run("falls back to Status.HardwareDetails when HardwareData NICs all have empty MACs", func(t *testing.T) {
		objs := newBMHBuilder("host-empty-mac-hd").
			WithHardwareDataNICs(testNIC(""), testNIC("")).
			WithStatusNICs(testNIC("AA:BB:CC:DD:EE:01")).
			BuildObjects()

		m := newMetal3ClientForTest(objs...)
		nics, err := m.GetHostNICs(ctx, testNamespace+"/host-empty-mac-hd")
		if err != nil {
			t.Fatalf("unexpected error (all-empty-MAC HardwareData should fall back to status): %v", err)
		}
		if len(nics) != 1 || nics[0].MAC != "aa:bb:cc:dd:ee:01" {
			t.Fatalf("expected status NIC aa:bb:cc:dd:ee:01, got %+v", nics)
		}
	})

	t.Run("returns HardwareData NICs when both sources are populated", func(t *testing.T) {
		objs := newBMHBuilder("host-both").
			WithHardwareDataNICs(testNIC("AA:BB:CC:DD:EE:01")).
			WithStatusNICs(testNIC("11:22:33:44:55:66")).
			BuildObjects()

		m := newMetal3ClientForTest(objs...)
		nics, err := m.GetHostNICs(ctx, testNamespace+"/host-both")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(nics) != 1 {
			t.Fatalf("expected 1 NIC, got %d", len(nics))
		}
		if nics[0].MAC != "aa:bb:cc:dd:ee:01" {
			t.Errorf("NIC[0].MAC = %q, want HardwareData source %q", nics[0].MAC, "aa:bb:cc:dd:ee:01")
		}
	})

	t.Run("skips NIC entries with empty MACs", func(t *testing.T) {
		objs := newBMHBuilder("host-empty-macs").WithHardwareDataNICs(
			testNIC("AA:BB:CC:DD:EE:01"),
			testNIC(""),
			testNIC("FF:00:11:22:33:44"),
		).BuildObjects()

		m := newMetal3ClientForTest(objs...)
		nics, err := m.GetHostNICs(ctx, testNamespace+"/host-empty-macs")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantMACs := []string{"aa:bb:cc:dd:ee:01", "ff:00:11:22:33:44"}
		if len(nics) != len(wantMACs) {
			t.Fatalf("expected %d NICs (empty MAC skipped), got %d", len(wantMACs), len(nics))
		}
		for i, want := range wantMACs {
			if nics[i].MAC != want {
				t.Errorf("NIC[%d].MAC = %q, want %q", i, nics[i].MAC, want)
			}
		}
	})

	t.Run("returns error when neither source has NICs", func(t *testing.T) {
		bmh := newBMHBuilder("host-no-hw").Build()

		m := newMetal3ClientForTest(bmh)
		_, err := m.GetHostNICs(ctx, testNamespace+"/host-no-hw")
		if err == nil {
			t.Fatal("expected error when no NIC data in either source, got nil")
		}
	})

	t.Run("returns error when HardwareData is present but empty and status is empty", func(t *testing.T) {
		objs := newBMHBuilder("host-empty-hw").WithHardwareDataNICs().BuildObjects()

		m := newMetal3ClientForTest(objs...)
		_, err := m.GetHostNICs(ctx, testNamespace+"/host-empty-hw")
		if err == nil {
			t.Fatal("expected error for empty HardwareData and empty status, got nil")
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
