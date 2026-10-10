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
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

func nodeSetHarness(name string, requests ...v1alpha1.NodeRequest) (*Reconciler, *nodeSetClient, *v1alpha1.ClusterOrder) {
	GinkgoHelper()
	scheme := runtime.NewScheme()
	Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
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
func capacityObservation(fc FulfillmentClient) *workerObservation {
	GinkgoHelper()
	bmis, err := fc.ListBareMetalInstances(context.Background(), "")
	Expect(err).NotTo(HaveOccurred())
	return indexWorkerBMIs(bmis)
}

// runWorkerCapacityStage runs the production lifecycle stage and then the
// reservation/create stage with unit-test inputs (no resolved image or
// ignition). Prerequisite resolution and its deferral are covered by the public
// Reconcile specs; unit fixtures keep asserting durable checkpoints.
func runWorkerCapacityStage(r *Reconciler, co *v1alpha1.ClusterOrder) (ctrl.Result, error) {
	GinkgoHelper()
	observed := capacityObservation(r.fulfillment)
	res, err := r.reconcileWorkerLifecycle(context.Background(), co, "tenant")
	if err != nil || !res.IsZero() {
		return res, err
	}
	_, res, err = r.reconcileDueWorkerCapacity(context.Background(), co, "tenant", workerCreationInputs{}, observed)
	return res, err
}

func reconcileNodeSetTest(r *Reconciler, co *v1alpha1.ClusterOrder) {
	GinkgoHelper()
	n := len(co.Status.Workers)
	for _, nr := range co.Spec.NodeRequests {
		n += nr.NumberOfNodes
	}
	for range 16 + 8*n {
		before := co.DeepCopy()
		res, err := runWorkerCapacityStage(r, co)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
		if res.RequeueAfter != time.Second {
			return
		}
		Expect(before.Status.Workers).NotTo(Equal(co.Status.Workers), "boundary requeue without persisted progress")
	}
	Fail("capacity fixture exceeded finite reconciliation bound")
}

var _ = Describe("Bare-metal NodeSet validation", func() {
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
		It(tt.name, func() {
			co := &v1alpha1.ClusterOrder{Spec: v1alpha1.ClusterOrderSpec{NodeRequests: tt.requests}}
			err := validateBareMetalNodeSets(co)
			if tt.wantErr == "" {
				Expect(err).ToNot(HaveOccurred(), "unexpected validation error: %v", err)
				return
			}
			Expect(err).To(HaveOccurred(), "validation error = %v, want %q", err, tt.wantErr)
			Expect(err.Error()).To(Equal(tt.wantErr), "validation error = %v, want %q", err, tt.wantErr)
		})
	}
})

var _ = It("ignores ClusterOrders without bare-metal requests", func() {
	r, fc, co := nodeSetHarness("non-bm", v1alpha1.NodeRequest{NodeSet: "other"})
	before := co.DeepCopy()
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(co)})
	Expect(err).ToNot(HaveOccurred(), "unexpected reconcile error: %v", err)
	Expect(result.IsZero()).To(BeTrue(), "result = %+v, want no requeue", result)
	Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co).To(Equal(before), "reconciliation mutated a non-BM ClusterOrder")
	Expect(fc.names).To(BeEmpty(), "created BMI names = %v, want none", fc.names)
})

var _ = Describe("New worker status uses the requested phase", func() {
	for _, phase := range []string{workerPhaseProvisioning, workerPhaseWaitingForAgent} {
		It(phase, func() {
			w := newWorkerStatus("compute", "standard", "bmi-name", "bmi-id", phase)
			Expect(w.Phase).To(Equal(phase), "phase = %q, want %q", w.Phase, phase)
			Expect(w.NodeSet).To(Equal("compute"), "incorrect worker identity or creation timestamp: %+v", w)
			Expect(w.InstanceType).To(Equal("standard"), "incorrect worker identity or creation timestamp: %+v", w)
			Expect(w.Name).To(Equal("bmi-name"), "incorrect worker identity or creation timestamp: %+v", w)
			Expect(w.Kind).To(Equal(workerKindBMI), "incorrect worker identity or creation timestamp: %+v", w)
			Expect(w.BareMetalInstance).To(Equal((v1alpha1.BareMetalInstanceReference{Name: "bmi-name", ID: "bmi-id"})), "incorrect worker identity or creation timestamp: %+v", w)
			Expect(w.CreationTimestamp.IsZero()).To(BeFalse(), "incorrect worker identity or creation timestamp: %+v", w)
		})
	}
})

var _ = It("persists bounded worker names before allocating NodeSet capacity", func() {
	r, fc, co := nodeSetHarness(strings.Repeat("a", 63), nodeRequest("standard", 2))
	reconcileNodeSetTest(r, co)
	Expect(fc.names).To(HaveLen(2), "created %d BMIs, want 2", len(fc.names))
	for _, name := range fc.names {
		Expect(len(name)).ToNot(BeNumerically(">", 63), "BMI name depends on order name or exceeds label limit: %q", name)
		Expect(strings.Contains(name, co.Name)).To(BeFalse(), "BMI name depends on order name or exceeds label limit: %q", name)
	}
	Expect(fc.reservationMissing).To(BeFalse(), "BMI created before its name was persisted in status")
	for _, w := range co.Status.Workers {
		Expect(w.BareMetalInstance.Name).To(Equal(w.Name), "missing explicit BMI identity: %+v", w)
		Expect(w.BareMetalInstance.ID).NotTo(Equal(""), "missing explicit BMI identity: %+v", w)
		raw, err := json.Marshal(w)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Contains(string(raw), "resourceID")).To(BeFalse(), "incorrect reference JSON: %s", raw)
		Expect(strings.Contains(string(raw), "bareMetalInstance")).To(BeTrue(), "incorrect reference JSON: %s", raw)
	}
	first := append([]string(nil), fc.names...)
	reconcileNodeSetTest(r, co)
	Expect(fc.names).To(HaveLen(len(first)), "repeated reconciliation created new BMIs")
})

var _ = Describe("NodeSet validation rejects missing recorded BMI names", func() {
	for _, id := range []string{"", "known-bmi-id"} {
		It("id="+id, func() {
			r, fc, co := nodeSetHarness("missing-reference", nodeRequest("standard", 2))
			co.Status.Workers = []v1alpha1.WorkerStatus{{
				Name: "worker-slot", Kind: workerKindBMI, NodeSet: "standard", InstanceType: "standard",
				Phase: workerPhaseProvisioning, BareMetalInstance: v1alpha1.BareMetalInstanceReference{ID: id},
			}}
			Expect(r.Status().Update(context.Background(), co)).To(Succeed())
			_, err := runWorkerCapacityStage(r, co)
			Expect(err).To(HaveOccurred(), "expected a missing-reference error, got %v", err)
			Expect(strings.Contains(err.Error(), "bareMetalInstance.name")).To(BeTrue(), "expected a missing-reference error, got %v", err)
			Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
			Expect(fc.names).To(BeEmpty(), "incomplete reference was guessed or provisioning continued: creates=%v workers=%+v", fc.names, co.Status.Workers)
			Expect(co.Status.Workers).To(HaveLen(1), "incomplete reference was guessed or provisioning continued: creates=%v workers=%+v", fc.names, co.Status.Workers)
			Expect(co.Status.Workers[0].BareMetalInstance.Name).To(Equal(""), "incomplete reference was guessed or provisioning continued: creates=%v workers=%+v", fc.names, co.Status.Workers)
			Expect(co.Status.Workers[0].BareMetalInstance.ID).To(Equal(id), "incomplete reference was guessed or provisioning continued: creates=%v workers=%+v", fc.names, co.Status.Workers)
		})
	}
})

var _ = It("rejects pending BMI recovery without a recorded name", func() {
	r, fc, co := nodeSetHarness("missing-reference", nodeRequest("standard", 1))
	co.Status.Workers = []v1alpha1.WorkerStatus{{
		Name: "worker-slot", Kind: workerKindBMI, NodeSet: "standard", InstanceType: "standard",
		Phase: workerPhaseProvisioning,
	}}
	Expect(r.Status().Update(context.Background(), co)).To(Succeed())
	// Ownership by the cluster does not prove that this BMI belongs to this slot.
	fc.bmis = []*privatev1.BareMetalInstance{privatev1.BareMetalInstance_builder{
		Id: "unrelated-bmi", Metadata: privatev1.Metadata_builder{
			Name: "worker-slot", Tenant: "tenant",
			Labels:      map[string]string{clusterOrderLabel: co.Name},
			Annotations: map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/" + co.Name},
		}.Build(),
	}.Build()}
	if err := runDeletionBMIStage(context.Background(), r, co); err == nil || !strings.Contains(err.Error(), "bareMetalInstance.name") {
		Fail(fmt.Sprintf("expected a missing-reference error, got %v", err))
	}
	Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Status.Workers[0].BareMetalInstance).To(Equal((v1alpha1.BareMetalInstanceReference{})), "guessed a BMI reference from the slot name: %+v", co.Status.Workers[0])
})

var _ = It("uses the recorded BMI name rather than the worker name for recovery", func() {
	r, fc, co := nodeSetHarness("recorded-reference", nodeRequest("standard", 1))
	co.Status.Workers = []v1alpha1.WorkerStatus{{
		Name: "worker-slot", Kind: workerKindBMI, NodeSet: "standard", InstanceType: "standard",
		Phase: workerPhaseProvisioning, BareMetalInstance: v1alpha1.BareMetalInstanceReference{Name: "actual-bmi"},
	}}
	Expect(r.Status().Update(context.Background(), co)).To(Succeed())
	fc.bmis = []*privatev1.BareMetalInstance{privatev1.BareMetalInstance_builder{
		Id: "recorded-id", Metadata: privatev1.Metadata_builder{
			Name: "actual-bmi", Tenant: "tenant",
			Labels:      map[string]string{clusterOrderLabel: co.Name},
			Annotations: map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/" + co.Name},
		}.Build(),
	}.Build()}
	if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	if err := runDeletionBMIStage(context.Background(), r, co); !errors.Is(err, errWorkerObservationChanged) {
		Fail(fmt.Sprintf("first deletion observation error=%v, want boundary", err))
	}
	Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	w := co.Status.Workers[0]
	Expect(co.Status.Workers).To(HaveLen(1), "did not preserve the recorded BMI identity: %+v", co.Status.Workers)
	Expect(w.Name).To(Equal("worker-slot"), "did not preserve the recorded BMI identity: %+v", co.Status.Workers)
	Expect(w.BareMetalInstance.Name).To(Equal("actual-bmi"), "did not preserve the recorded BMI identity: %+v", co.Status.Workers)
	Expect(w.BareMetalInstance.ID).To(Equal("recorded-id"), "did not preserve the recorded BMI identity: %+v", co.Status.Workers)
})

var _ = It("rejects a stale NodeSet snapshot rather than rebasing it", func() {
	r, _, co := nodeSetHarness("stale-order", nodeRequest("standard", 2))
	stale := co.DeepCopy()
	if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	latest := &v1alpha1.ClusterOrder{}
	Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), latest)).To(Succeed())
	if _, err := r.reserveWorkerSlots(context.Background(), stale); !errors.Is(err, errWorkerObservationChanged) {
		Fail(fmt.Sprintf("error=%v, want stale-observation interruption", err))
	}
	Expect(latest.Status.Workers).To(HaveLen(2), "unexpected reservations after stale conflict: latest=%+v stale=%+v", latest.Status.Workers, stale.Status.Workers)
	Expect(stale.Status.Workers).To(BeEmpty(), "unexpected reservations after stale conflict: latest=%+v stale=%+v", latest.Status.Workers, stale.Status.Workers)
})

var _ = Describe("Excess worker slot classification", func() {
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
		It(tt.name, func() {
			w := v1alpha1.WorkerStatus{Kind: tt.kind, Phase: tt.phase}
			if got := isExcessWorkerSlot(w, tt.remaining); got != tt.want {
				Fail(fmt.Sprintf("isExcessWorkerSlot() = %v, want %v", got, tt.want))
			}
		})
	}
})

var _ = It("plans worker slots independently for each NodeSet", func() {
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
		By(tt.name)
		func() {
			co := &v1alpha1.ClusterOrder{
				Spec:   v1alpha1.ClusterOrderSpec{NodeRequests: tt.requests},
				Status: v1alpha1.ClusterOrderStatus{Workers: tt.workers},
			}
			before := co.DeepCopy()
			plan := planWorkerSlots(co)
			assertWorkerSlotNames("selected", plan.selected, tt.selected)
			assertWorkerSlotNames("excess", plan.excess, tt.excess)
			Expect(plan.missingByNodeSet).To(Equal(tt.missing), "missing = %v, want %v", plan.missingByNodeSet, tt.missing)
			Expect(co).To(Equal(before), "planning mutated the ClusterOrder")
		}()
	}
})

func assertWorkerSlotNames(partition string, workers []v1alpha1.WorkerStatus, want []string) {
	GinkgoHelper()
	names := make([]string, 0, len(workers))
	for _, w := range workers {
		names = append(names, w.Name)
	}
	want = slices.Clone(want)
	slices.Sort(names)
	slices.Sort(want)
	Expect(slices.Equal(names, want)).To(BeTrue(), "%s = %v, want %v", partition, names, want)
}

var _ = It("allocates missing worker slots from the capacity plan", func() {
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
	Expect(allocateMissingWorkerSlots(co)).To(Succeed())
	Expect(co.Status.Workers).To(HaveLen(len(before.Status.Workers)+1), "allocated %d slots, want 1", len(co.Status.Workers)-len(before.Status.Workers))
	Expect(co.Status.Workers[:len(before.Status.Workers)]).To(Equal(before.Status.Workers), "allocation changed existing worker identities or lifecycle state")
	reserved := co.Status.Workers[len(before.Status.Workers)]
	Expect(reserved.NodeSet).To(Equal("compute"), "incorrect scale-up reservation: %+v", reserved)
	Expect(reserved.InstanceType).To(Equal("standard"), "incorrect scale-up reservation: %+v", reserved)
	Expect(reserved.Phase).To(Equal(workerPhaseProvisioning), "incorrect scale-up reservation: %+v", reserved)
	Expect(reserved.Name).NotTo(Equal(""), "incorrect scale-up reservation: %+v", reserved)
	Expect(reserved.BareMetalInstance.Name).To(Equal(reserved.Name), "incorrect scale-up reservation: %+v", reserved)
	plan := planWorkerSlots(co)
	Expect(plan.missingByNodeSet["compute"]).To(Equal(0), "incorrect post-allocation plan: %+v", plan)
	Expect(plan.missingByNodeSet["batch"]).To(Equal(0), "incorrect post-allocation plan: %+v", plan)
	assertWorkerSlotNames("excess", plan.excess, []string{"compute-deleting", "batch-failed"})
	after := co.DeepCopy()
	Expect(allocateMissingWorkerSlots(co)).To(Succeed())
	Expect(co).To(Equal(after), "repeated allocation changed reserved slots")
})

var _ = It("retains healthy older workers during NodeSet scale-down", func() {
	old := metav1.NewTime(metav1.Now().Add(-time.Hour))
	co := &v1alpha1.ClusterOrder{Spec: v1alpha1.ClusterOrderSpec{NodeRequests: []v1alpha1.NodeRequest{nodeRequest("standard", 1)}}, Status: v1alpha1.ClusterOrderStatus{Workers: []v1alpha1.WorkerStatus{
		{NodeSet: "standard", Name: "failed", Kind: workerKindBMI, InstanceType: "standard", Phase: workerPhaseFailed, CreationTimestamp: old},
		{NodeSet: "standard", Name: "new-ready", Kind: workerKindBMI, InstanceType: "standard", Phase: workerPhaseReady, CreationTimestamp: metav1.Now()},
		{NodeSet: "standard", Name: "old-ready", Kind: workerKindBMI, InstanceType: "standard", Phase: workerPhaseReady, CreationTimestamp: old},
		{NodeSet: "standard", Name: "deleting", Kind: workerKindBMI, InstanceType: "standard", Phase: workerPhaseDeleting, CreationTimestamp: old},
	}}}
	plan := planWorkerSlots(co)
	Expect(plan.selected).To(HaveLen(1), "incorrect scale-down selection: selected=%+v excess=%+v", plan.selected, plan.excess)
	Expect(plan.selected[0].Name).To(Equal("old-ready"), "incorrect scale-down selection: selected=%+v excess=%+v", plan.selected, plan.excess)
	Expect(plan.excess).To(HaveLen(3), "incorrect scale-down selection: selected=%+v excess=%+v", plan.selected, plan.excess)
})

var _ = It("reuses the NodeSet reservation after an interrupted Create", func() {
	r, fc, co := nodeSetHarness("order", nodeRequest("standard", 1))
	fc.failCreate = true
	if _, err := r.reserveWorkerSlots(context.Background(), co); err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(r.apiReader.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	if _, err := runWorkerCapacityStage(r, co); err == nil {
		Fail("expected interrupted create")
	}
	Expect(r.Get(context.Background(), client.ObjectKeyFromObject(co), co)).To(Succeed())
	Expect(co.Status.Workers).To(HaveLen(1), "lost pending worker reservation: %+v", co.Status.Workers)
	reserved := co.Status.Workers[0].BareMetalInstance.Name
	fc.failCreate = false
	reconcileNodeSetTest(r, co)
	Expect(fc.names).To(HaveLen(2), "create retries changed identity: %v", fc.names)
	Expect(fc.names[0]).To(Equal(reserved), "create retries changed identity: %v", fc.names)
	Expect(fc.names[1]).To(Equal(reserved), "create retries changed identity: %v", fc.names)
})

var _ = It("does not readopt a deleted BMI during NodeSet retry backoff", func() {
	r, _, co := nodeSetHarness("backoff-order", nodeRequest("standard", 1))
	reconcileNodeSetTest(r, co)
	next := metav1.NewTime(time.Now().Add(time.Hour))
	co.Status.Workers[0].Phase = workerPhaseFailed
	co.Status.Workers[0].BareMetalInstance.ID = ""
	co.Status.Workers[0].NextRetryTime = &next
	Expect(r.Status().Update(context.Background(), co)).To(Succeed())
	// A deletion can be asynchronous: the old BMI still appears in List. It must
	// not be re-adopted into the failed slot while its retry backoff is pending.
	reconcileNodeSetTest(r, co)
	Expect(co.Status.Workers[0].BareMetalInstance.ID).To(Equal(""), "re-adopted BMI during retry backoff")
})

var _ = It("scales NodeSets independently when they share an instance type", func() {
	compute, batch := nodeRequest("standard", 1), nodeRequest("standard", 2)
	compute.NodeSet, batch.NodeSet = "compute", "batch"
	r, fc, co := nodeSetHarness("same-hardware", compute, batch)
	reconcileNodeSetTest(r, co)
	batchWorkers := make(map[string]bool)
	for _, w := range co.Status.Workers {
		if w.NodeSet == "batch" {
			batchWorkers[w.Name] = true
		}
	}
	Expect(batchWorkers).To(HaveLen(2), "lost batch membership: %+v", co.Status.Workers)
	co.Spec.NodeRequests[0].NumberOfNodes = 3
	Expect(r.Update(context.Background(), co)).To(Succeed())
	reconcileNodeSetTest(r, co)
	counts := map[string]int{}
	for _, w := range co.Status.Workers {
		counts[w.NodeSet]++
		Expect(batchWorkers[w.Name] && w.NodeSet != "batch").To(BeFalse(), "reassigned a batch worker to compute")
	}
	Expect(counts["compute"]).To(Equal(3), "incorrect independent capacities: %v, %d BMIs", counts, len(fc.bmis))
	Expect(counts["batch"]).To(Equal(2), "incorrect independent capacities: %v, %d BMIs", counts, len(fc.bmis))
	Expect(fc.bmis).To(HaveLen(5), "incorrect independent capacities: %v, %d BMIs", counts, len(fc.bmis))
})

var _ = It("preserves other NodeSet memberships during scaling", func() {
	r, fc, co := nodeSetHarness("order", nodeRequest("standard", 1), nodeRequest("gpu", 1))
	reconcileNodeSetTest(r, co)
	gpu := co.Status.Workers[1]
	co.Spec.NodeRequests[0].NumberOfNodes = 2
	Expect(r.Update(context.Background(), co)).To(Succeed())
	reconcileNodeSetTest(r, co)
	var retained bool
	counts := map[string]int{}
	for _, w := range co.Status.Workers {
		counts[w.InstanceType]++
		if w.Name == gpu.Name && w.InstanceType == "gpu" {
			retained = true
		}
	}
	Expect(retained).To(BeTrue(), "scaling changed worker membership: %+v", co.Status.Workers)
	Expect(counts["standard"]).To(Equal(2), "scaling changed worker membership: %+v", co.Status.Workers)
	Expect(counts["gpu"]).To(Equal(1), "scaling changed worker membership: %+v", co.Status.Workers)
	Expect(fc.bmis).To(HaveLen(3), "got %d BMIs, want 3", len(fc.bmis))
})
