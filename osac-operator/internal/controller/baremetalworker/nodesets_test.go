/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type nodeSetClient struct {
	FulfillmentClient
	kube               client.Client
	order              client.ObjectKey
	bmis               []*privatev1.BareMetalInstance
	failCreate         bool
	names              []string
	reservationMissing bool
}

func (f *nodeSetClient) ListBareMetalInstances(context.Context, string) ([]*privatev1.BareMetalInstance, error) {
	return f.bmis, nil
}
func (f *nodeSetClient) GetBareMetalInstanceType(_ context.Context, name string) (*privatev1.BareMetalInstanceType, error) {
	return privatev1.BareMetalInstanceType_builder{Metadata: privatev1.Metadata_builder{Name: name}.Build()}.Build(), nil
}
func (f *nodeSetClient) CreateBareMetalInstance(ctx context.Context, bmi *privatev1.BareMetalInstance) (*privatev1.BareMetalInstance, error) {
	name := bmi.GetMetadata().GetName()
	f.names = append(f.names, name)
	co := &v1alpha1.ClusterOrder{}
	if err := f.kube.Get(ctx, f.order, co); err != nil {
		return nil, err
	}
	recorded := false
	for _, w := range co.Status.Workers {
		if w.BareMetalInstance.Name == name {
			recorded = true
		}
	}
	if !recorded {
		f.reservationMissing = true
	}
	if f.failCreate {
		return nil, fmt.Errorf("interrupted create")
	}
	bmi.SetId(fmt.Sprintf("id-%d", len(f.bmis)))
	f.bmis = append(f.bmis, bmi)
	return bmi, nil
}

func nodeSetHarness(t *testing.T, name string, requests ...v1alpha1.NodeRequest) (*Reconciler, *nodeSetClient, *v1alpha1.ClusterOrder) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	co := &v1alpha1.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test"}, Spec: v1alpha1.ClusterOrderSpec{NodeRequests: requests}}
	kube := clientfake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(co).WithObjects(co).Build()
	fc := &nodeSetClient{kube: kube, order: client.ObjectKeyFromObject(co)}
	r := &Reconciler{Client: kube, apiReader: kube, scheme: scheme, fulfillment: fc, recorder: events.NewFakeRecorder(100)}
	return r, fc, co
}
func nodeRequest(instanceType string, count int) v1alpha1.NodeRequest {
	return v1alpha1.NodeRequest{NodeSet: instanceType, NumberOfNodes: count, BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: instanceType}}
}

// capacityObservation lists the provider's current BMIs into the invocation-local
// observation that production lifecycle/creation stages receive explicitly.
func capacityObservation(t *testing.T, fc FulfillmentClient) *workerObservation {
	t.Helper()
	bmis, err := fc.ListBareMetalInstances(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	return indexWorkerBMIs(bmis)
}

// runWorkerCapacityStage runs the production lifecycle stage and then the
// reservation/create stage with unit-test inputs (no resolved image or
// ignition). Prerequisite resolution and its deferral are covered by the public
// Reconcile specs; unit fixtures keep asserting durable checkpoints.
func runWorkerCapacityStage(t *testing.T, r *Reconciler, co *v1alpha1.ClusterOrder) (ctrl.Result, error) {
	t.Helper()
	observed := capacityObservation(t, r.fulfillment)
	res, err := r.reconcileWorkerLifecycle(context.Background(), co, "tenant")
	if err != nil || !res.IsZero() {
		return res, err
	}
	_, res, err = r.reconcileDueWorkerCapacity(context.Background(), co, "tenant", workerCreationInputs{}, observed)
	return res, err
}

func reconcileNodeSetTest(t *testing.T, r *Reconciler, co *v1alpha1.ClusterOrder) {
	t.Helper()
	n := len(co.Status.Workers)
	for _, nr := range co.Spec.NodeRequests {
		n += nr.NumberOfNodes
	}
	for range 16 + 8*n {
		before := co.DeepCopy()
		res, err := runWorkerCapacityStage(t, r, co)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
			t.Fatal(err)
		}
		if res.RequeueAfter != time.Second {
			return
		}
		if reflect.DeepEqual(before.Status.Workers, co.Status.Workers) {
			t.Fatal("boundary requeue without persisted progress")
		}
	}
	t.Fatal("capacity fixture exceeded finite reconciliation bound")
}

func TestValidateBareMetalNodeSets(t *testing.T) {
	tests := []struct {
		name     string
		requests []v1alpha1.NodeRequest
		wantErr  string
	}{
		{name: "empty order"},
		{name: "non-BM requests are ignored", requests: []v1alpha1.NodeRequest{{}, {}}},
		{name: "valid BM request", requests: []v1alpha1.NodeRequest{nodeRequest("standard", 1)}},
		{
			name: "mixed requests validate only BM nodesets",
			requests: []v1alpha1.NodeRequest{
				{}, nodeRequest("standard", 1), {NodeSet: "standard"},
			},
		},
		{
			name: "missing BM instance type retains original index",
			requests: []v1alpha1.NodeRequest{
				{}, {NodeSet: "compute", BareMetal: &v1alpha1.BareMetalNodeSpec{}},
			},
			wantErr: "spec.nodeRequests[1].bareMetal.instanceType is required",
		},
		{
			name:     "missing BM nodeset",
			requests: []v1alpha1.NodeRequest{{BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "standard"}}},
			wantErr:  "spec.nodeRequests[0].nodeSet must be nonempty and unique",
		},
		{
			name:     "duplicate BM nodesets",
			requests: []v1alpha1.NodeRequest{nodeRequest("standard", 1), {}, nodeRequest("standard", 2)},
			wantErr:  "spec.nodeRequests[2].nodeSet must be nonempty and unique",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			co := &v1alpha1.ClusterOrder{Spec: v1alpha1.ClusterOrderSpec{NodeRequests: tt.requests}}
			err := validateBareMetalNodeSets(co)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected validation error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("validation error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestReconcileIgnoresNonBareMetalOrder(t *testing.T) {
	r, fc, co := nodeSetHarness(t, "non-bm", v1alpha1.NodeRequest{NodeSet: "other"})
	before := co.DeepCopy()
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
	if err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}
	if !result.IsZero() {
		t.Fatalf("result = %+v, want no requeue", result)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(co, before) {
		t.Error("reconciliation mutated a non-BM ClusterOrder")
	}
	if len(fc.names) != 0 {
		t.Errorf("created BMI names = %v, want none", fc.names)
	}
}

func TestNewWorkerStatusUsesRequestedPhase(t *testing.T) {
	for _, phase := range []string{workerPhaseProvisioning, workerPhaseWaitingForAgent} {
		t.Run(phase, func(t *testing.T) {
			w := newWorkerStatus("compute", "standard", "bmi-name", "bmi-id", phase)
			if w.Phase != phase {
				t.Fatalf("phase = %q, want %q", w.Phase, phase)
			}
			if w.NodeSet != "compute" || w.InstanceType != "standard" || w.Name != "bmi-name" || w.Kind != workerKindBMI ||
				w.BareMetalInstance != (v1alpha1.BareMetalInstanceReference{Name: "bmi-name", ID: "bmi-id"}) || w.CreationTimestamp.IsZero() {
				t.Fatalf("incorrect worker identity or creation timestamp: %+v", w)
			}
		})
	}
}

func TestNodeSetNamesAreBoundedAndReserved(t *testing.T) {
	r, fc, co := nodeSetHarness(t, strings.Repeat("a", 63), nodeRequest("standard", 2))
	reconcileNodeSetTest(t, r, co)
	if len(fc.names) != 2 {
		t.Fatalf("created %d BMIs, want 2", len(fc.names))
	}
	for _, name := range fc.names {
		if len(name) > 63 || strings.Contains(name, co.Name) {
			t.Errorf("BMI name depends on order name or exceeds label limit: %q", name)
		}
	}
	if fc.reservationMissing {
		t.Error("BMI created before its name was persisted in status")
	}
	for _, w := range co.Status.Workers {
		if w.BareMetalInstance.Name != w.Name || w.BareMetalInstance.ID == "" {
			t.Fatalf("missing explicit BMI identity: %+v", w)
		}
		raw, err := json.Marshal(w)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "resourceID") || !strings.Contains(string(raw), "bareMetalInstance") {
			t.Fatalf("incorrect reference JSON: %s", raw)
		}
	}
	first := append([]string(nil), fc.names...)
	reconcileNodeSetTest(t, r, co)
	if len(fc.names) != len(first) {
		t.Error("repeated reconciliation created new BMIs")
	}
}

func TestNodeSetRejectsMissingRecordedBMIName(t *testing.T) {
	for _, id := range []string{"", "known-bmi-id"} {
		t.Run("id="+id, func(t *testing.T) {
			r, fc, co := nodeSetHarness(t, "missing-reference", nodeRequest("standard", 2))
			co.Status.Workers = []v1alpha1.WorkerStatus{{
				Name: "worker-slot", Kind: workerKindBMI, NodeSet: "standard", InstanceType: "standard",
				Phase: workerPhaseProvisioning, BareMetalInstance: v1alpha1.BareMetalInstanceReference{ID: id},
			}}
			if err := r.Status().Update(context.Background(), co); err != nil {
				t.Fatal(err)
			}
			_, err := runWorkerCapacityStage(t, r, co)
			if err == nil || !strings.Contains(err.Error(), "bareMetalInstance.name") {
				t.Fatalf("expected a missing-reference error, got %v", err)
			}
			if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
				t.Fatal(err)
			}
			if len(fc.names) != 0 || len(co.Status.Workers) != 1 || co.Status.Workers[0].BareMetalInstance.Name != "" || co.Status.Workers[0].BareMetalInstance.ID != id {
				t.Fatalf("incomplete reference was guessed or provisioning continued: creates=%v workers=%+v", fc.names, co.Status.Workers)
			}
		})
	}
}

func TestPendingBMIRecoveryRejectsMissingRecordedName(t *testing.T) {
	r, fc, co := nodeSetHarness(t, "missing-reference", nodeRequest("standard", 1))
	co.Status.Workers = []v1alpha1.WorkerStatus{{
		Name: "worker-slot", Kind: workerKindBMI, NodeSet: "standard", InstanceType: "standard",
		Phase: workerPhaseProvisioning,
	}}
	if err := r.Status().Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	// Ownership by the cluster does not prove that this BMI belongs to this slot.
	fc.bmis = []*privatev1.BareMetalInstance{privatev1.BareMetalInstance_builder{
		Id: "unrelated-bmi", Metadata: privatev1.Metadata_builder{
			Name: "worker-slot", Tenant: "tenant",
			Labels:      map[string]string{clusterOrderLabel: co.Name},
			Annotations: map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/" + co.Name},
		}.Build(),
	}.Build()}
	if err := runDeletionBMIStage(context.Background(), r, co); err == nil || !strings.Contains(err.Error(), "bareMetalInstance.name") {
		t.Fatalf("expected a missing-reference error, got %v", err)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if co.Status.Workers[0].BareMetalInstance != (v1alpha1.BareMetalInstanceReference{}) {
		t.Fatalf("guessed a BMI reference from the slot name: %+v", co.Status.Workers[0])
	}
}

func TestPendingBMIRecoveryUsesRecordedNameNotWorkerName(t *testing.T) {
	r, fc, co := nodeSetHarness(t, "recorded-reference", nodeRequest("standard", 1))
	co.Status.Workers = []v1alpha1.WorkerStatus{{
		Name: "worker-slot", Kind: workerKindBMI, NodeSet: "standard", InstanceType: "standard",
		Phase: workerPhaseProvisioning, BareMetalInstance: v1alpha1.BareMetalInstanceReference{Name: "actual-bmi"},
	}}
	if err := r.Status().Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	fc.bmis = []*privatev1.BareMetalInstance{privatev1.BareMetalInstance_builder{
		Id: "recorded-id", Metadata: privatev1.Metadata_builder{
			Name: "actual-bmi", Tenant: "tenant",
			Labels:      map[string]string{clusterOrderLabel: co.Name},
			Annotations: map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/" + co.Name},
		}.Build(),
	}.Build()}
	if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	if err := runDeletionBMIStage(context.Background(), r, co); !errors.Is(err, errWorkerObservationChanged) {
		t.Fatalf("first deletion observation error=%v, want boundary", err)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	w := co.Status.Workers[0]
	if len(co.Status.Workers) != 1 || w.Name != "worker-slot" || w.BareMetalInstance.Name != "actual-bmi" || w.BareMetalInstance.ID != "recorded-id" {
		t.Fatalf("did not preserve the recorded BMI identity: %+v", co.Status.Workers)
	}
}

func TestNodeSetStaleSnapshotConflictsInsteadOfRebasing(t *testing.T) {
	r, _, co := nodeSetHarness(t, "stale-order", nodeRequest("standard", 2))
	stale := co.DeepCopy()
	if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	latest := &v1alpha1.ClusterOrder{}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), latest); err != nil {
		t.Fatal(err)
	}
	if _, err := r.reserveWorkerSlots(context.Background(), stale); !errors.Is(err, errWorkerObservationChanged) {
		t.Fatalf("error=%v, want stale-observation interruption", err)
	}
	if len(latest.Status.Workers) != 2 || len(stale.Status.Workers) != 0 {
		t.Fatalf("unexpected reservations after stale conflict: latest=%+v stale=%+v", latest.Status.Workers, stale.Status.Workers)
	}
}

func TestIsExcessWorkerSlot(t *testing.T) {
	tests := []struct {
		name      string
		kind      string
		phase     string
		remaining int
		want      bool
	}{
		{name: "ready worker", kind: workerKindBMI, phase: workerPhaseReady, remaining: 1},
		{name: "pending reservation", kind: workerKindBMI, phase: workerPhaseProvisioning, remaining: 1},
		{name: "failed slot awaiting retry", kind: workerKindBMI, phase: workerPhaseFailed, remaining: 1},
		{name: "capacity satisfied", kind: workerKindBMI, phase: workerPhaseReady, want: true},
		{name: "unbinding worker", kind: workerKindBMI, phase: workerPhaseUnbinding, remaining: 1, want: true},
		{name: "deleting worker", kind: workerKindBMI, phase: workerPhaseDeleting, remaining: 1, want: true},
		{name: "other resource kind", kind: "Other", phase: workerPhaseReady, remaining: 1, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := v1alpha1.WorkerStatus{Kind: tt.kind, Phase: tt.phase}
			if got := isExcessWorkerSlot(w, tt.remaining); got != tt.want {
				t.Errorf("isExcessWorkerSlot() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlanWorkerSlots(t *testing.T) {
	compute := nodeRequest("standard", 2)
	compute.NodeSet = "compute"
	batch := nodeRequest("standard", 1)
	batch.NodeSet = "batch"
	old := metav1.NewTime(time.Unix(100, 0))
	newer := metav1.NewTime(time.Unix(200, 0))
	worker := func(name, nodeSet, phase string, created metav1.Time) v1alpha1.WorkerStatus {
		return v1alpha1.WorkerStatus{
			Name: name, NodeSet: nodeSet, InstanceType: "standard", Kind: workerKindBMI,
			Phase: phase, CreationTimestamp: created,
			BareMetalInstance: v1alpha1.BareMetalInstanceReference{Name: name},
		}
	}
	ready := worker("ready", "compute", workerPhaseReady, old)
	pending := worker("pending", "compute", workerPhaseProvisioning, newer)
	failed := worker("failed", "compute", workerPhaseFailed, old)
	batchWorker := worker("batch-worker", "batch", workerPhaseReady, old)
	otherKind := ready
	otherKind.Name, otherKind.Kind = "other-kind", "Other"

	tests := []struct {
		name     string
		requests []v1alpha1.NodeRequest
		workers  []v1alpha1.WorkerStatus
		selected []string
		excess   []string
		missing  map[string]int
	}{
		{
			name: "empty order", missing: map[string]int{},
		},
		{
			name: "scale up from no workers", requests: []v1alpha1.NodeRequest{compute},
			missing: map[string]int{"compute": 2},
		},
		{
			name: "scale up preserves a pending reservation", requests: []v1alpha1.NodeRequest{compute},
			workers: []v1alpha1.WorkerStatus{pending}, selected: []string{"pending"},
			missing: map[string]int{"compute": 1},
		},
		{
			name: "at capacity retains failed slots for retry", requests: []v1alpha1.NodeRequest{compute},
			workers: []v1alpha1.WorkerStatus{failed, ready}, selected: []string{"failed", "ready"},
			missing: map[string]int{"compute": 0},
		},
		{
			name: "scale down prefers healthy then pending workers", requests: []v1alpha1.NodeRequest{compute},
			workers:  []v1alpha1.WorkerStatus{failed, pending, ready},
			selected: []string{"pending", "ready"}, excess: []string{"failed"},
			missing: map[string]int{"compute": 0},
		},
		{
			name: "scale down prefers older healthy workers", requests: []v1alpha1.NodeRequest{compute},
			workers: []v1alpha1.WorkerStatus{
				worker("newest", "compute", workerPhaseReady, metav1.NewTime(time.Unix(300, 0))),
				worker("new", "compute", workerPhaseReady, newer), ready,
			},
			selected: []string{"new", "ready"}, excess: []string{"newest"},
			missing: map[string]int{"compute": 0},
		},
		{
			name: "equal priorities and ages preserve input order", requests: []v1alpha1.NodeRequest{batch},
			workers:  []v1alpha1.WorkerStatus{batchWorker, worker("batch-peer", "batch", workerPhaseReady, old)},
			selected: []string{"batch-worker"}, excess: []string{"batch-peer"},
			missing: map[string]int{"batch": 0},
		},
		{
			name: "teardown workers do not satisfy capacity", requests: []v1alpha1.NodeRequest{compute},
			workers: []v1alpha1.WorkerStatus{
				worker("unbinding", "compute", workerPhaseUnbinding, old),
				worker("deleting", "compute", workerPhaseDeleting, old), ready,
			},
			selected: []string{"ready"}, excess: []string{"unbinding", "deleting"},
			missing: map[string]int{"compute": 1},
		},
		{
			name: "scale up and down independently with shared hardware", requests: []v1alpha1.NodeRequest{batch, compute},
			workers:  []v1alpha1.WorkerStatus{batchWorker, ready, worker("batch-new", "batch", workerPhaseReady, newer)},
			selected: []string{"batch-worker", "ready"}, excess: []string{"batch-new"},
			missing: map[string]int{"compute": 1, "batch": 0},
		},
		{
			name: "removed nodeset workers are excess", requests: []v1alpha1.NodeRequest{compute},
			workers: []v1alpha1.WorkerStatus{batchWorker}, excess: []string{"batch-worker"},
			missing: map[string]int{"compute": 2},
		},
		{
			name: "other resource kinds do not satisfy capacity", requests: []v1alpha1.NodeRequest{compute},
			workers: []v1alpha1.WorkerStatus{otherKind}, excess: []string{"other-kind"},
			missing: map[string]int{"compute": 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			co := &v1alpha1.ClusterOrder{
				Spec:   v1alpha1.ClusterOrderSpec{NodeRequests: tt.requests},
				Status: v1alpha1.ClusterOrderStatus{Workers: tt.workers},
			}
			before := co.DeepCopy()
			plan := planWorkerSlots(co)
			assertWorkerSlotNames(t, "selected", plan.selected, tt.selected)
			assertWorkerSlotNames(t, "excess", plan.excess, tt.excess)
			if !reflect.DeepEqual(plan.missingByNodeSet, tt.missing) {
				t.Errorf("missing = %v, want %v", plan.missingByNodeSet, tt.missing)
			}
			if !reflect.DeepEqual(co, before) {
				t.Error("planning mutated the ClusterOrder")
			}
		})
	}
}

func assertWorkerSlotNames(t *testing.T, partition string, workers []v1alpha1.WorkerStatus, want []string) {
	t.Helper()
	names := make([]string, 0, len(workers))
	for _, w := range workers {
		names = append(names, w.Name)
	}
	want = slices.Clone(want)
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("%s = %v, want %v", partition, names, want)
	}
}

func TestAllocateMissingWorkerSlotsUsesPlan(t *testing.T) {
	compute, batch := nodeRequest("standard", 2), nodeRequest("standard", 1)
	compute.NodeSet, batch.NodeSet = "compute", "batch"
	co := &v1alpha1.ClusterOrder{
		Spec: v1alpha1.ClusterOrderSpec{NodeRequests: []v1alpha1.NodeRequest{compute, batch}},
		Status: v1alpha1.ClusterOrderStatus{Workers: []v1alpha1.WorkerStatus{
			newWorkerStatus("compute", "standard", "compute-ready", "", workerPhaseReady),
			newWorkerStatus("compute", "standard", "compute-deleting", "", workerPhaseDeleting),
			newWorkerStatus("batch", "standard", "batch-ready", "", workerPhaseReady),
			newWorkerStatus("batch", "standard", "batch-failed", "", workerPhaseFailed),
		}},
	}
	before := co.DeepCopy()
	if err := allocateMissingWorkerSlots(co); err != nil {
		t.Fatal(err)
	}
	if len(co.Status.Workers) != len(before.Status.Workers)+1 {
		t.Fatalf("allocated %d slots, want 1", len(co.Status.Workers)-len(before.Status.Workers))
	}
	if !reflect.DeepEqual(co.Status.Workers[:len(before.Status.Workers)], before.Status.Workers) {
		t.Fatal("allocation changed existing worker identities or lifecycle state")
	}
	reserved := co.Status.Workers[len(before.Status.Workers)]
	if reserved.NodeSet != "compute" || reserved.InstanceType != "standard" || reserved.Phase != workerPhaseProvisioning ||
		reserved.Name == "" || reserved.BareMetalInstance.Name != reserved.Name {
		t.Fatalf("incorrect scale-up reservation: %+v", reserved)
	}
	plan := planWorkerSlots(co)
	if plan.missingByNodeSet["compute"] != 0 || plan.missingByNodeSet["batch"] != 0 {
		t.Fatalf("incorrect post-allocation plan: %+v", plan)
	}
	assertWorkerSlotNames(t, "excess", plan.excess, []string{"compute-deleting", "batch-failed"})
	after := co.DeepCopy()
	if err := allocateMissingWorkerSlots(co); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(co, after) {
		t.Fatal("repeated allocation changed reserved slots")
	}
}

func TestNodeSetScaleDownPrefersHealthyOlderWorkers(t *testing.T) {
	old := metav1.NewTime(metav1.Now().Add(-time.Hour))
	co := &v1alpha1.ClusterOrder{Spec: v1alpha1.ClusterOrderSpec{NodeRequests: []v1alpha1.NodeRequest{nodeRequest("standard", 1)}}, Status: v1alpha1.ClusterOrderStatus{Workers: []v1alpha1.WorkerStatus{
		{NodeSet: "standard", Name: "failed", Kind: workerKindBMI, InstanceType: "standard", Phase: workerPhaseFailed, CreationTimestamp: old},
		{NodeSet: "standard", Name: "new-ready", Kind: workerKindBMI, InstanceType: "standard", Phase: workerPhaseReady, CreationTimestamp: metav1.Now()},
		{NodeSet: "standard", Name: "old-ready", Kind: workerKindBMI, InstanceType: "standard", Phase: workerPhaseReady, CreationTimestamp: old},
		{NodeSet: "standard", Name: "deleting", Kind: workerKindBMI, InstanceType: "standard", Phase: workerPhaseDeleting, CreationTimestamp: old},
	}}}
	plan := planWorkerSlots(co)
	if len(plan.selected) != 1 || plan.selected[0].Name != "old-ready" || len(plan.excess) != 3 {
		t.Fatalf("incorrect scale-down selection: selected=%+v excess=%+v", plan.selected, plan.excess)
	}
}

func TestNodeSetInterruptedCreateReusesReservation(t *testing.T) {
	r, fc, co := nodeSetHarness(t, "order", nodeRequest("standard", 1))
	fc.failCreate = true
	if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	if err := r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if _, err := runWorkerCapacityStage(t, r, co); err == nil {
		t.Fatal("expected interrupted create")
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(co), co); err != nil {
		t.Fatal(err)
	}
	if len(co.Status.Workers) != 1 {
		t.Fatalf("lost pending worker reservation: %+v", co.Status.Workers)
	}
	reserved := co.Status.Workers[0].BareMetalInstance.Name
	fc.failCreate = false
	reconcileNodeSetTest(t, r, co)
	if len(fc.names) != 2 || fc.names[0] != reserved || fc.names[1] != reserved {
		t.Fatalf("create retries changed identity: %v", fc.names)
	}
}

func TestNodeSetBackoffDoesNotReadoptDeletedBMI(t *testing.T) {
	r, _, co := nodeSetHarness(t, "backoff-order", nodeRequest("standard", 1))
	reconcileNodeSetTest(t, r, co)
	next := metav1.NewTime(time.Now().Add(time.Hour))
	co.Status.Workers[0].Phase = workerPhaseFailed
	co.Status.Workers[0].BareMetalInstance.ID = ""
	co.Status.Workers[0].NextRetryTime = &next
	if err := r.Status().Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	// A deletion can be asynchronous: the old BMI still appears in List. It must
	// not be re-adopted into the failed slot while its retry backoff is pending.
	reconcileNodeSetTest(t, r, co)
	if co.Status.Workers[0].BareMetalInstance.ID != "" {
		t.Fatal("re-adopted BMI during retry backoff")
	}
}

func TestNodeSetsSharingHardwareScaleIndependently(t *testing.T) {
	compute, batch := nodeRequest("standard", 1), nodeRequest("standard", 2)
	compute.NodeSet, batch.NodeSet = "compute", "batch"
	r, fc, co := nodeSetHarness(t, "same-hardware", compute, batch)
	reconcileNodeSetTest(t, r, co)
	batchWorkers := make(map[string]bool)
	for _, w := range co.Status.Workers {
		if w.NodeSet == "batch" {
			batchWorkers[w.Name] = true
		}
	}
	if len(batchWorkers) != 2 {
		t.Fatalf("lost batch membership: %+v", co.Status.Workers)
	}
	co.Spec.NodeRequests[0].NumberOfNodes = 3
	if err := r.Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	reconcileNodeSetTest(t, r, co)
	counts := map[string]int{}
	for _, w := range co.Status.Workers {
		counts[w.NodeSet]++
		if batchWorkers[w.Name] && w.NodeSet != "batch" {
			t.Fatal("reassigned a batch worker to compute")
		}
	}
	if counts["compute"] != 3 || counts["batch"] != 2 || len(fc.bmis) != 5 {
		t.Fatalf("incorrect independent capacities: %v, %d BMIs", counts, len(fc.bmis))
	}
}

func TestNodeSetScalingPreservesOtherMembership(t *testing.T) {
	r, fc, co := nodeSetHarness(t, "order", nodeRequest("standard", 1), nodeRequest("gpu", 1))
	reconcileNodeSetTest(t, r, co)
	gpu := co.Status.Workers[1]
	co.Spec.NodeRequests[0].NumberOfNodes = 2
	if err := r.Update(context.Background(), co); err != nil {
		t.Fatal(err)
	}
	reconcileNodeSetTest(t, r, co)
	var retained bool
	counts := map[string]int{}
	for _, w := range co.Status.Workers {
		counts[w.InstanceType]++
		if w.Name == gpu.Name && w.InstanceType == "gpu" {
			retained = true
		}
	}
	if !retained || counts["standard"] != 2 || counts["gpu"] != 1 {
		t.Fatalf("scaling changed worker membership: %+v", co.Status.Workers)
	}
	if len(fc.bmis) != 3 {
		t.Fatalf("got %d BMIs, want 3", len(fc.bmis))
	}
}
