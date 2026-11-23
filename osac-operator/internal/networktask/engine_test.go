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

package networktask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
)

// fakeAAP is an in-memory AAP.
type fakeAAP struct {
	mu        sync.Mutex
	nextID    int
	jobs      map[int]*aap.Job
	jobLabel  map[int]string
	labels    map[string]int
	launches  []aap.LaunchJobTemplateRequest
	launchErr error
	cancelled []string
}

func newFakeAAP() *fakeAAP {
	return &fakeAAP{nextID: 100, jobs: map[int]*aap.Job{}, jobLabel: map[int]string{}, labels: map[string]int{}}
}

func (f *fakeAAP) EnsureLabel(_ context.Context, _ string, name string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.labels[name]; ok {
		return id, nil
	}
	id := len(f.labels) + 1
	f.labels[name] = id
	return id, nil
}

func (f *fakeAAP) labelName(id int) string {
	for name, labelID := range f.labels {
		if labelID == id {
			return name
		}
	}
	return ""
}

func (f *fakeAAP) LaunchJobTemplate(_ context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launches = append(f.launches, req)
	if f.launchErr != nil {
		return nil, f.launchErr
	}
	f.nextID++
	f.jobs[f.nextID] = &aap.Job{ID: f.nextID, Status: "pending"}
	if len(req.Labels) > 0 {
		f.jobLabel[f.nextID] = f.labelName(req.Labels[0])
	}
	return &aap.LaunchJobTemplateResponse{JobID: f.nextID}, nil
}

// addJob creates a job with a label as AAP would after a failed launch call.
func (f *fakeAAP) addJob(label, status string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.jobs[f.nextID] = &aap.Job{ID: f.nextID, Status: status}
	f.jobLabel[f.nextID] = label
	return f.nextID
}

func (f *fakeAAP) GetJob(_ context.Context, id string) (*aap.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, _ := strconv.Atoi(id)
	job, ok := f.jobs[n]
	if !ok {
		return nil, &aap.NotFoundError{Resource: "job " + id}
	}
	copied := *job
	return &copied, nil
}

func (f *fakeAAP) CancelJob(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, id)
	return nil
}

func (f *fakeAAP) ListJobsByLabel(_ context.Context, name string) ([]aap.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []aap.Job
	for id, label := range f.jobLabel {
		if label == name {
			out = append(out, *f.jobs[id])
		}
	}
	return out, nil
}

func (f *fakeAAP) finish(id string, status string, artifacts string, explanation string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, _ := strconv.Atoi(id)
	job := f.jobs[n]
	job.Status = status
	job.JobExplanation = explanation
	if artifacts != "" {
		job.Artifacts = json.RawMessage(artifacts)
	}
}

func (f *fakeAAP) last() aap.LaunchJobTemplateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.launches[len(f.launches)-1]
}

type harness struct {
	t      *testing.T
	ctx    context.Context
	client client.Client
	aap    *fakeAAP
	clock  *clocktesting.FakeClock
	engine *Engine
	// lastGet is the object passed to the latest runKeep.
	lastGet *v1alpha1.Subnet
}

func testScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func newHarness(t *testing.T, objs ...client.Object) *harness {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.Subnet{}, &v1alpha1.VirtualNetwork{}, &v1alpha1.NetworkAttachment{}, &v1alpha1.ComputeInstance{}).
		Build()
	f := newFakeAAP()
	clk := clocktesting.NewFakeClock(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	e := New(c, c, f, Config{TemplatePrefix: "osac", Organization: "osac", ProvisioningEnabled: true}).WithClock(clk)
	return &harness{t: t, ctx: context.Background(), client: c, aap: f, clock: clk, engine: e}
}

var fabricRef = &v1alpha1.NetworkManagerRef{Name: "acme_fabric", AnsibleRole: "acme.network.fabric"}

func subnetObj(name, id, vn string) *v1alpha1.Subnet {
	return &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "osac-networking", UID: types.UID("uid-" + name),
			Labels:      map[string]string{"osac.openshift.io/subnet-uuid": id, VirtualNetworkIDLabel: vn},
			Annotations: map[string]string{TenantAnnotation: "tenant-a"},
		},
		Spec: v1alpha1.SubnetSpec{VirtualNetwork: vn, IPv4CIDR: "10.0.1.0/24",
			AddressPlan: v1alpha1.SubnetAddressPlan{Overlay: 8, Fabric: 4, LBVIP: 4}},
	}
}

func (h *harness) get(name string) *v1alpha1.Subnet {
	s := &v1alpha1.Subnet{}
	if err := h.client.Get(h.ctx, client.ObjectKey{Namespace: "osac-networking", Name: name}, s); err != nil {
		h.t.Fatal(err)
	}
	return s
}

func (h *harness) run(name string, req Request) Outcome {
	h.t.Helper()
	out, err := h.engine.Run(h.ctx, h.get(name), req)
	if err != nil {
		h.t.Fatalf("Run: %v", err)
	}
	return out
}

// runKeep runs on a fresh copy and keeps it in lastGet for Accept callbacks.
func (h *harness) runKeep(name string, req Request) Outcome {
	h.t.Helper()
	h.lastGet = h.get(name)
	out, err := h.engine.Run(h.ctx, h.lastGet, req)
	if err != nil {
		h.t.Fatalf("Run: %v", err)
	}
	return out
}

func fabricReq(task string) Request {
	return Request{Task: task, Manager: v1alpha1.NetworkJobManagerFabric, ManagerRef: fabricRef, Kind: "Subnet",
		Vars: map[string]any{"resource": map[string]any{"id": "s1"}}}
}

func entry(t *testing.T, s *v1alpha1.Subnet, task string) v1alpha1.NetworkJobStatus {
	t.Helper()
	e := FindJob(s.Status.Jobs, task, v1alpha1.NetworkJobManagerFabric)
	if e == nil {
		t.Fatalf("no %s entry", task)
	}
	return *e
}

func TestLaunchWritesRunningThenJobID(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	stored := false
	req := fabricReq(CreateSubnet)
	req.BeforeLaunch = func() error { stored = true; return nil }
	out := h.run("s1", req)
	if !stored {
		t.Fatal("BeforeLaunch not called")
	}
	if out.Entry.State != v1alpha1.NetworkJobStateRunning || out.Entry.Attempt != 1 || out.Entry.JobID != "101" {
		t.Fatalf("unexpected entry %+v", out.Entry)
	}
	launch := h.aap.last()
	if launch.TemplateName != "osac-create-subnet" {
		t.Fatalf("template %q", launch.TemplateName)
	}
	if h.aap.labelName(launch.Labels[0]) != "osac-create_subnet-fabric-s1-1" {
		t.Fatalf("label %q", h.aap.labelName(launch.Labels[0]))
	}
	vars := launch.ExtraVars["osac_job_vars"].(map[string]any)
	if vars["task"] != CreateSubnet {
		t.Fatalf("task var %v", vars["task"])
	}
	manager := vars["manager"].(map[string]any)
	if manager["type"] != "fabric" || manager["ansible_role"] != "acme.network.fabric" || manager["name"] != "acme_fabric" {
		t.Fatalf("manager var %v", manager)
	}
	if got := entry(t, h.get("s1"), CreateSubnet); got.JobID != "101" {
		t.Fatalf("stored entry %+v", got)
	}
}

func TestRejectsTaskOutsideFixedSet(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	req := fabricReq(QueryDHCPLease)
	req.Manager = v1alpha1.NetworkJobManagerKubernetes
	if _, err := h.engine.Run(h.ctx, h.get("s1"), req); err == nil {
		t.Fatal("expected rejection of a task outside the Kubernetes set")
	}
	if len(h.aap.launches) != 0 {
		t.Fatal("launched")
	}
}

func TestConflictLaunchesNothing(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	stale := h.get("s1")
	fresh := h.get("s1")
	fresh.Annotations["x"] = "y"
	if err := h.client.Update(h.ctx, fresh); err != nil {
		t.Fatal(err)
	}
	_, err := h.engine.Run(h.ctx, stale, fabricReq(CreateSubnet))
	if !IsConflict(err) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if len(h.aap.launches) != 0 {
		t.Fatal("launched despite conflict")
	}
}

func TestBlockedWhileDeleting(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	req := fabricReq(CreateSubnet)
	req.TargetDeleting = true
	out := h.run("s1", req)
	if !out.Blocked || len(h.aap.launches) != 0 {
		t.Fatalf("expected blocked launch, got %+v", out)
	}
}

func TestStatusMapping(t *testing.T) {
	cases := []struct {
		name        string
		task        string
		status      string
		artifacts   string
		explanation string
		wantState   v1alpha1.NetworkJobState
		wantMessage string
	}{
		{"success without output", CreateSubnet, "successful", "", "", v1alpha1.NetworkJobStateSucceeded, ""},
		{"undefined output key", DeleteVirtualNetwork, "successful", `{"osac_result":{"x":1}}`, "", v1alpha1.NetworkJobStateRetrying, "delete_virtual_network (fabric) attempt 1 failed: OSAC rejected the delete_virtual_network result: undefined output \"x\"; next retry at 2026-10-01T12:02:00Z"},
		{"rejected result is final", CreateNATGateway, "successful", `{"osac_result":{"x":1}}`, "", v1alpha1.NetworkJobStateFailed, "OSAC rejected the create_nat_gateway result: undefined output \"x\""},
		{"retryable osac_error", CreateSubnet, "failed", `{"osac_error":{"message":"backend busy","retryable":true}}`, "", v1alpha1.NetworkJobStateRetrying, "create_subnet (fabric) attempt 1 failed: backend busy; next retry at 2026-10-01T12:02:00Z"},
		{"final osac_error", CreateSubnet, "failed", `{"osac_error":{"message":"no free VLAN","retryable":false}}`, "", v1alpha1.NetworkJobStateFailed, "no free VLAN"},
		{"result ignored on a failed play", CreateSubnet, "failed", `{"osac_result":{"x":1},"osac_error":{"message":"no free VLAN","retryable":false}}`, "", v1alpha1.NetworkJobStateFailed, "no free VLAN"},
		{"malformed osac_error", CreateSubnet, "failed", `{"osac_error":{"message":"m","retryable":"yes"}}`, "", v1alpha1.NetworkJobStateFailed, "AAP job 101 failed; malformed osac_error"},
		{"extra osac_error key", CreateSubnet, "failed", `{"osac_error":{"message":"m","retryable":true,"x":1}}`, "", v1alpha1.NetworkJobStateFailed, "AAP job 101 failed; malformed osac_error"},
		{"no osac_error", CreateSubnet, "failed", "", "Playbook failed", v1alpha1.NetworkJobStateFailed, "AAP job 101 failed: Playbook failed"},
		{"timeout", CreateSubnet, "failed", "", "Job terminated due to timeout", v1alpha1.NetworkJobStateRetrying, "create_subnet (fabric) attempt 1 failed: job timed out after 60 minutes; next retry at 2026-10-01T12:02:00Z"},
		{"AAP error", CreateSubnet, "error", "", "pod evicted", v1alpha1.NetworkJobStateRetrying, "create_subnet (fabric) attempt 1 failed: AAP job 101 ended with an error: pod evicted; next retry at 2026-10-01T12:02:00Z"},
		{"cleanup final failure is retried", DeleteSubnet, "failed", `{"osac_error":{"message":"nope","retryable":false}}`, "", v1alpha1.NetworkJobStateRetrying, "delete_subnet (fabric) attempt 1 failed: nope; next retry at 2026-10-01T12:02:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, subnetObj("s1", "s1", "vn1"))
			out := h.run("s1", fabricReq(tc.task))
			h.aap.finish(out.Entry.JobID, tc.status, tc.artifacts, tc.explanation)
			out = h.run("s1", fabricReq(tc.task))
			if out.Entry.State != tc.wantState || out.Entry.Message != tc.wantMessage {
				t.Fatalf("got %s %q, want %s %q", out.Entry.State, out.Entry.Message, tc.wantState, tc.wantMessage)
			}
		})
	}
}

func TestBackoffSequence(t *testing.T) {
	want := []time.Duration{2, 4, 8, 16, 30, 30, 30}
	for i, w := range want {
		if got := Backoff(CreateSubnet, i+1, int32(i+1)); got != w*time.Minute {
			t.Fatalf("failure %d: got %s, want %s", i+1, got, w*time.Minute)
		}
	}
	if Backoff(QueryDHCPLease, 5, 19) != 30*time.Second || Backoff(QueryDHCPLease, 5, 20) != 5*time.Minute {
		t.Fatal("query_dhcp_lease cadence")
	}
}

func TestRetryWaitsAndLaunchesNextAttempt(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	waits := []time.Duration{2, 4, 8, 16, 30, 30}
	for i, w := range waits {
		out := h.run("s1", fabricReq(CreateSubnet))
		if out.Entry.State != v1alpha1.NetworkJobStateRunning || out.Entry.Attempt != int32(i+1) {
			t.Fatalf("attempt %d: %+v", i+1, out.Entry)
		}
		h.aap.finish(out.Entry.JobID, "failed", `{"osac_error":{"message":"busy","retryable":true}}`, "")
		out = h.run("s1", fabricReq(CreateSubnet))
		if out.RequeueAfter != w*time.Minute {
			t.Fatalf("attempt %d wait %s", i+1, out.RequeueAfter)
		}
		h.clock.Step(w*time.Minute - time.Second)
		if out = h.run("s1", fabricReq(CreateSubnet)); out.Entry.State != v1alpha1.NetworkJobStateRetrying {
			t.Fatalf("retried early: %+v", out.Entry)
		}
		h.clock.Step(time.Second)
	}
}

func TestRestartRestartsTheWait(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	out := h.run("s1", fabricReq(CreateSubnet))
	h.aap.finish(out.Entry.JobID, "failed", `{"osac_error":{"message":"busy","retryable":true}}`, "")
	h.run("s1", fabricReq(CreateSubnet))
	h.clock.Step(time.Minute)
	// A new engine is a restarted operator.
	h.engine = New(h.client, h.client, h.aap, Config{TemplatePrefix: "osac", Organization: "osac", ProvisioningEnabled: true}).WithClock(h.clock)
	out = h.run("s1", fabricReq(CreateSubnet))
	if out.Entry.State != v1alpha1.NetworkJobStateRetrying || out.RequeueAfter != 2*time.Minute {
		t.Fatalf("got %+v", out)
	}
	if !strings.HasSuffix(out.Entry.Message, "next retry at 2026-10-01T12:03:00Z") {
		t.Fatalf("stale retry time %q", out.Entry.Message)
	}
}

func TestCanceledByHandRelaunches(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	out := h.run("s1", fabricReq(CreateSubnet))
	h.aap.finish(out.Entry.JobID, "canceled", "", "")
	out = h.run("s1", fabricReq(CreateSubnet))
	if out.Entry.State != v1alpha1.NetworkJobStateRunning || out.Entry.Attempt != 2 || len(h.aap.launches) != 2 {
		t.Fatalf("got %+v", out.Entry)
	}
}

func TestLaunchDue(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	due := func() bool {
		return h.engine.LaunchDue(h.get("s1"), CreateSubnet, v1alpha1.NetworkJobManagerFabric)
	}
	if !due() {
		t.Fatal("no entry yet: a launch is due")
	}
	out := h.run("s1", fabricReq(CreateSubnet))
	if due() {
		t.Fatal("Running: no launch is due")
	}
	h.aap.finish(out.Entry.JobID, "error", "", "")
	h.run("s1", fabricReq(CreateSubnet))
	if due() {
		t.Fatal("Retrying in backoff: no launch is due")
	}
	h.clock.Step(Backoff(CreateSubnet, 1, 1) + time.Second)
	if !due() {
		t.Fatal("Retrying after its backoff: a launch is due")
	}
	s := h.get("s1")
	Cancel(s, CreateSubnet, v1alpha1.NetworkJobManagerFabric)
	if err := h.client.Status().Update(h.ctx, s); err != nil {
		t.Fatal(err)
	}
	if !due() {
		t.Fatal("Cancelled: a launch is due")
	}
}

func TestHoldLaunchStartsNoJob(t *testing.T) {
	held := func() Request {
		req := fabricReq(CreateSubnet)
		req.HoldLaunch = true
		return req
	}

	t.Run("no entry", func(t *testing.T) {
		h := newHarness(t, subnetObj("s1", "s1", "vn1"))
		out := h.run("s1", held())
		if len(h.aap.launches) != 0 || len(h.get("s1").Status.Jobs) != 0 {
			t.Fatalf("launched %d, jobs %+v", len(h.aap.launches), h.get("s1").Status.Jobs)
		}
		if out.RequeueAfter <= 0 {
			t.Fatal("a held launch must requeue")
		}
	})

	t.Run("retry whose wait ended", func(t *testing.T) {
		h := newHarness(t, subnetObj("s1", "s1", "vn1"))
		out := h.run("s1", fabricReq(CreateSubnet))
		h.aap.finish(out.Entry.JobID, "error", "", "")
		h.run("s1", fabricReq(CreateSubnet))
		h.clock.Step(Backoff(CreateSubnet, 1, 1) + time.Second)
		out = h.run("s1", held())
		if len(h.aap.launches) != 1 {
			t.Fatalf("launches %d", len(h.aap.launches))
		}
		if e := entry(t, h.get("s1"), CreateSubnet); e.State != v1alpha1.NetworkJobStateRetrying || e.Attempt != 1 {
			t.Fatalf("unexpected entry %+v", e)
		}
		if out.RequeueAfter <= 0 {
			t.Fatal("a held launch must requeue")
		}
	})

	t.Run("job cancelled by hand", func(t *testing.T) {
		h := newHarness(t, subnetObj("s1", "s1", "vn1"))
		out := h.run("s1", fabricReq(CreateSubnet))
		h.aap.finish(out.Entry.JobID, "canceled", "", "")
		out = h.run("s1", held())
		if len(h.aap.launches) != 1 {
			t.Fatalf("launches %d", len(h.aap.launches))
		}
		if out.Entry.State != v1alpha1.NetworkJobStateCancelled || out.RequeueAfter <= 0 {
			t.Fatalf("unexpected outcome %+v", out)
		}
		if !h.engine.LaunchDue(h.get("s1"), CreateSubnet, v1alpha1.NetworkJobManagerFabric) {
			t.Fatal("the next run must be able to launch")
		}
		out = h.run("s1", fabricReq(CreateSubnet))
		if out.Entry.Attempt != 2 || out.Entry.State != v1alpha1.NetworkJobStateRunning {
			t.Fatalf("unexpected entry %+v", out.Entry)
		}
	})

	t.Run("lost launch", func(t *testing.T) {
		h := newHarness(t, subnetObj("s1", "s1", "vn1"))
		h.aap.launchErr = errors.New("connection refused")
		h.run("s1", fabricReq(CreateSubnet))
		h.aap.launchErr = nil
		launches := len(h.aap.launches)
		h.clock.Step(LostLaunchTimeout)
		out := h.run("s1", held())
		if len(h.aap.launches) != launches {
			t.Fatalf("launches %d, want %d", len(h.aap.launches), launches)
		}
		if out.Entry.State != v1alpha1.NetworkJobStateCancelled {
			t.Fatalf("unexpected outcome %+v", out)
		}
	})

	t.Run("tracks a running job", func(t *testing.T) {
		h := newHarness(t, subnetObj("s1", "s1", "vn1"))
		out := h.run("s1", fabricReq(CreateSubnet))
		h.aap.finish(out.Entry.JobID, "failed", "", "")
		out = h.run("s1", held())
		if out.Entry.State != v1alpha1.NetworkJobStateFailed {
			t.Fatalf("unexpected outcome %+v", out)
		}
	})
}

func TestNotStartedIsCancelledAndRetried(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	out := h.run("s1", fabricReq(CreateSubnet))
	h.clock.Step(29 * time.Minute)
	h.run("s1", fabricReq(CreateSubnet))
	if len(h.aap.cancelled) != 0 {
		t.Fatal("cancelled too early")
	}
	h.clock.Step(time.Minute)
	h.run("s1", fabricReq(CreateSubnet))
	if len(h.aap.cancelled) != 1 {
		t.Fatal("not cancelled after 30 minutes")
	}
	h.aap.finish(out.Entry.JobID, "canceled", "", "")
	out = h.run("s1", fabricReq(CreateSubnet))
	if out.Entry.State != v1alpha1.NetworkJobStateRetrying || !strings.Contains(out.Entry.Message, MessageNotStarted) {
		t.Fatalf("got %+v", out.Entry)
	}
}

func TestLostJobIsRetryable(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	h.run("s1", fabricReq(CreateSubnet))
	delete(h.aap.jobs, 101)
	out := h.run("s1", fabricReq(CreateSubnet))
	if out.Entry.State != v1alpha1.NetworkJobStateRetrying || !strings.Contains(out.Entry.Message, "AAP has no record of job 101") {
		t.Fatalf("got %+v", out.Entry)
	}
}

func TestEmptyJobIDResolution(t *testing.T) {
	t.Run("absent: launched once after two minutes", func(t *testing.T) {
		h := newHarness(t, subnetObj("s1", "s1", "vn1"))
		h.aap.launchErr = fmt.Errorf("timeout")
		out := h.run("s1", fabricReq(CreateSubnet))
		if out.Entry.State != v1alpha1.NetworkJobStateRunning || out.Entry.JobID != "" {
			t.Fatalf("got %+v", out.Entry)
		}
		h.aap.launchErr = nil
		h.clock.Step(time.Minute)
		h.run("s1", fabricReq(CreateSubnet))
		if len(h.aap.launches) != 1 {
			t.Fatal("relaunched before two minutes")
		}
		h.clock.Step(time.Minute)
		out = h.run("s1", fabricReq(CreateSubnet))
		if len(h.aap.launches) != 2 || out.Entry.Attempt != 1 || out.Entry.JobID == "" {
			t.Fatalf("got %+v after %d launches", out.Entry, len(h.aap.launches))
		}
	})
	t.Run("created later by AAP: tracked, lowest ID, others cancelled", func(t *testing.T) {
		h := newHarness(t, subnetObj("s1", "s1", "vn1"))
		h.aap.launchErr = fmt.Errorf("timeout")
		h.run("s1", fabricReq(CreateSubnet))
		h.aap.launchErr = nil
		low := h.aap.addJob("osac-create_subnet-fabric-s1-1", "running")
		high := h.aap.addJob("osac-create_subnet-fabric-s1-1", "running")
		out := h.run("s1", fabricReq(CreateSubnet))
		if out.Entry.JobID != strconv.Itoa(low) || len(h.aap.launches) != 1 {
			t.Fatalf("got %+v", out.Entry)
		}
		if len(h.aap.cancelled) != 1 || h.aap.cancelled[0] != strconv.Itoa(high) {
			t.Fatalf("cancelled %v", h.aap.cancelled)
		}
	})
	t.Run("absent while deleting: Cancelled", func(t *testing.T) {
		h := newHarness(t, subnetObj("s1", "s1", "vn1"))
		h.aap.launchErr = fmt.Errorf("timeout")
		h.run("s1", fabricReq(CreateSubnet))
		h.clock.Step(LostLaunchTimeout)
		req := fabricReq(CreateSubnet)
		req.TargetDeleting = true
		out := h.run("s1", req)
		if out.Entry.State != v1alpha1.NetworkJobStateCancelled || len(h.aap.launches) != 1 {
			t.Fatalf("got %+v", out.Entry)
		}
	})
}

func TestCancelForDelete(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	out := h.run("s1", fabricReq(CreateSubnet))
	done, _, err := h.engine.CancelForDelete(h.ctx, h.get("s1"), "Subnet")
	if err != nil || done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if len(h.aap.cancelled) != 1 {
		t.Fatal("job not cancelled")
	}
	// The job succeeded before the cancel took effect: its result is ignored.
	h.aap.finish(out.Entry.JobID, "successful", `{"osac_result":{"network_segment":{}}}`, "")
	done, _, err = h.engine.CancelForDelete(h.ctx, h.get("s1"), "Subnet")
	if err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if got := entry(t, h.get("s1"), CreateSubnet); got.State != v1alpha1.NetworkJobStateCancelled {
		t.Fatalf("got %+v", got)
	}
}

func TestCancelForDeleteRetrying(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	out := h.run("s1", fabricReq(CreateSubnet))
	h.aap.finish(out.Entry.JobID, "failed", `{"osac_error":{"message":"busy","retryable":true}}`, "")
	h.run("s1", fabricReq(CreateSubnet))
	done, _, err := h.engine.CancelForDelete(h.ctx, h.get("s1"), "Subnet")
	if err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if got := entry(t, h.get("s1"), CreateSubnet); got.State != v1alpha1.NetworkJobStateCancelled {
		t.Fatalf("got %+v", got)
	}
}

func TestMarkForRerun(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	out := h.run("s1", fabricReq(CreateSubnet))
	h.aap.finish(out.Entry.JobID, "successful", "", "")
	h.run("s1", fabricReq(CreateSubnet))
	s := h.get("s1")
	h.engine.MarkForRerun(s, CreateSubnet, v1alpha1.NetworkJobManagerFabric)
	if err := h.client.Status().Update(h.ctx, s); err != nil {
		t.Fatal(err)
	}
	out = h.run("s1", fabricReq(CreateSubnet))
	if out.Entry.State != v1alpha1.NetworkJobStateRunning || out.Entry.Attempt != 2 {
		t.Fatalf("got %+v", out.Entry)
	}
}

func TestSubnetSlot(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"), subnetObj("s2", "s2", "vn1"), subnetObj("s3", "s3", "vn2"))
	req := func(id string) Request {
		r := fabricReq(CreateSubnet)
		r.Slot = &SubnetSlot{VirtualNetworkID: map[string]string{"s1": "vn1", "s2": "vn1", "s3": "vn2"}[id]}
		return r
	}
	first := h.run("s1", req("s1"))
	if out := h.run("s2", req("s2")); !out.WaitingForSlot {
		t.Fatalf("second Subnet of the VirtualNetwork launched: %+v", out)
	}
	if out := h.run("s3", req("s3")); out.WaitingForSlot {
		t.Fatal("Subnet of another VirtualNetwork waits")
	}
	h.aap.finish(first.Entry.JobID, "successful", "", "")
	h.run("s1", req("s1"))
	if out := h.run("s2", req("s2")); out.WaitingForSlot || out.Entry.State != v1alpha1.NetworkJobStateRunning {
		t.Fatalf("second Subnet did not launch after the first ended: %+v", out)
	}
}

func TestSubnetSlotSelectorAndLockCleanup(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	var options client.ListOptions
	h.engine.apiReader = interceptor.NewClient(h.client.(client.WithWatch), interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			options = client.ListOptions{}
			options.ApplyOptions(opts)
			return c.List(ctx, list, opts...)
		},
	})
	unlock, held, err := h.engine.takeSlot(h.ctx, h.get("s1"), &SubnetSlot{VirtualNetworkID: "vn1"})
	if err != nil || held {
		t.Fatalf("slot = %t, %v", held, err)
	}
	unlock()
	if options.Namespace != "osac-networking" || options.LabelSelector == nil || options.LabelSelector.String() != "osac.openshift.io/virtual-network-id=vn1" {
		t.Fatalf("slot list options = %+v", options)
	}
	if len(h.engine.vnLocks.locks) != 0 {
		t.Fatalf("idle locks retained: %d", len(h.engine.vnLocks.locks))
	}
}

func TestSubnetSlotErrorsReleaseLocks(t *testing.T) {
	for _, phase := range []string{"list", "before launch", "running status"} {
		t.Run(phase, func(t *testing.T) {
			h := newHarness(t, subnetObj("s1", "s1", "vn1"))
			failure := errors.New("API unavailable")
			req := fabricReq(CreateSubnet)
			req.Slot = &SubnetSlot{VirtualNetworkID: "vn1"}
			switch phase {
			case "list":
				h.engine.apiReader = interceptor.NewClient(h.client.(client.WithWatch), interceptor.Funcs{List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error { return failure }})
			case "before launch":
				req.BeforeLaunch = func() error { return failure }
			case "running status":
				h.engine.client = interceptor.NewClient(h.client.(client.WithWatch), interceptor.Funcs{SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
					return failure
				}})
			}
			if _, err := h.engine.Run(h.ctx, h.get("s1"), req); !errors.Is(err, failure) {
				t.Fatalf("got %v", err)
			}
			if len(h.engine.vnLocks.locks) != 0 {
				t.Fatalf("failed path retained locks: %d", len(h.engine.vnLocks.locks))
			}
		})
	}
}

func TestKeyedMutexRetainsWaiters(t *testing.T) {
	var locks keyedMutex
	first := locks.acquire("vn1")
	waiterAcquired, releaseWaiter, waiterDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		unlock := locks.acquire("vn1")
		close(waiterAcquired)
		<-releaseWaiter
		unlock()
		close(waiterDone)
	}()
	deadline := time.After(3 * time.Second)
	for {
		locks.mu.Lock()
		refs := locks.locks["vn1"].refs
		locks.mu.Unlock()
		if refs == 2 {
			break
		}
		select {
		case <-deadline:
			first()
			close(releaseWaiter)
			t.Fatal("waiter did not retain the key")
		case <-time.After(time.Millisecond):
		}
	}
	other := locks.acquire("vn2")
	other()
	first()
	select {
	case <-waiterAcquired:
	case <-time.After(3 * time.Second):
		close(releaseWaiter)
		t.Fatal("waiter did not acquire released key")
	}
	locks.mu.Lock()
	entries, refs := len(locks.locks), locks.locks["vn1"].refs
	locks.mu.Unlock()
	if entries != 1 || refs != 1 {
		t.Errorf("waiting holder lost its key: entries=%d refs=%d", entries, refs)
	}
	close(releaseWaiter)
	<-waiterDone
	if len(locks.locks) != 0 {
		t.Fatalf("final holder retained keys: %d", len(locks.locks))
	}
}

type blockingEnsureLabelAAP struct {
	AAPClient
	entered chan struct{}
	release chan struct{}
}

func (a *blockingEnsureLabelAAP) EnsureLabel(ctx context.Context, organization, name string) (int, error) {
	if name == "osac-create_subnet-fabric-s1-1" {
		close(a.entered)
		select {
		case <-a.release:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return a.AAPClient.EnsureLabel(ctx, organization, name)
}

type engineRunResult struct {
	out Outcome
	err error
}

func asyncEngineRun(e *Engine, ctx context.Context, subnet *v1alpha1.Subnet, req Request) <-chan engineRunResult {
	done := make(chan engineRunResult, 1)
	go func() { out, err := e.Run(ctx, subnet, req); done <- engineRunResult{out, err} }()
	return done
}

func awaitEngineRun(t *testing.T, done <-chan engineRunResult) Outcome {
	t.Helper()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		return result.out
	case <-time.After(3 * time.Second):
		t.Fatal("engine run did not finish")
		return Outcome{}
	}
}

func TestSubnetSlotReleasesBeforeAAP(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"), subnetObj("s2", "s2", "vn1"), subnetObj("s3", "s3", "vn2"))
	aap := &blockingEnsureLabelAAP{AAPClient: h.aap, entered: make(chan struct{}), release: make(chan struct{})}
	h.engine.aap = aap
	var once sync.Once
	release := func() { once.Do(func() { close(aap.release) }) }
	defer release()
	req := fabricReq(CreateSubnet)
	req.Slot = &SubnetSlot{VirtualNetworkID: "vn1"}
	first := asyncEngineRun(h.engine, h.ctx, h.get("s1"), req)
	select {
	case <-aap.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first AAP call did not start")
	}
	if got := entry(t, h.get("s1"), CreateSubnet); got.State != v1alpha1.NetworkJobStateRunning || got.JobID != "" {
		t.Fatalf("reservation not persisted: %+v", got)
	}
	second := asyncEngineRun(h.engine, h.ctx, h.get("s2"), req)
	select {
	case result := <-second:
		if result.err != nil || !result.out.WaitingForSlot {
			t.Fatalf("second run = %+v", result)
		}
	case <-time.After(time.Second):
		t.Error("slot check waited for the first AAP call")
		release()
		awaitEngineRun(t, second)
	}
	otherReq := fabricReq(CreateSubnet)
	otherReq.Slot = &SubnetSlot{VirtualNetworkID: "vn2"}
	if out := awaitEngineRun(t, asyncEngineRun(h.engine, h.ctx, h.get("s3"), otherReq)); out.WaitingForSlot || out.Entry.JobID == "" {
		t.Fatalf("independent network blocked: %+v", out)
	}
	release()
	awaitEngineRun(t, first)
	if len(h.engine.vnLocks.locks) != 0 {
		t.Fatalf("idle locks retained: %d", len(h.engine.vnLocks.locks))
	}
}

func TestSiblingAcceptanceSerializesThroughStatus(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"), subnetObj("s2", "s2", "vn1"), subnetObj("s3", "s3", "vn2"))
	reqFor := func(s *v1alpha1.Subnet, entered chan struct{}) Request {
		req := fabricReq(CreateSubnet)
		req.Slot = &SubnetSlot{VirtualNetworkID: s.Spec.VirtualNetwork}
		req.Accept = func(ctx context.Context, result Result) error {
			if entered != nil {
				close(entered)
			}
			return AcceptSegment(ctx, h.engine.APIReader(), s, "evpn", result)
		}
		return req
	}
	// Seed completed AAP jobs so both sibling acceptances can race for the
	// shared VirtualNetwork lock.
	for i, name := range []string{"s1", "s2", "s3"} {
		s := h.get(name)
		out, err := h.engine.Run(h.ctx, s, fabricReq(CreateSubnet))
		if err != nil {
			t.Fatal(err)
		}
		l2 := 100 + i
		artifacts := fmt.Sprintf(`{"osac_result":{"network_segment":{"type":"evpn","l2_vni":%d}}}`, l2)
		h.aap.finish(out.Entry.JobID, "successful", artifacts, "")
	}
	firstAtStatus, releaseStatus := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(releaseStatus) }) }
	defer release()
	h.engine.client = interceptor.NewClient(h.client.(client.WithWatch), interceptor.Funcs{SubResourceUpdate: func(ctx context.Context, c client.Client, name string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
		if s := obj.(*v1alpha1.Subnet); s.Name == "s1" && s.Status.NetworkSegment != nil {
			close(firstAtStatus)
			<-releaseStatus
		}
		return c.SubResource(name).Update(ctx, obj, opts...)
	}})
	firstSubnet := h.get("s1")
	first := asyncEngineRun(h.engine, h.ctx, firstSubnet, reqFor(firstSubnet, nil))
	select {
	case <-firstAtStatus:
	case <-time.After(3 * time.Second):
		t.Fatal("first acceptance did not reach status write")
	}
	secondEntered := make(chan struct{})
	secondSubnet := h.get("s2")
	second := asyncEngineRun(h.engine, h.ctx, secondSubnet, reqFor(secondSubnet, secondEntered))
	select {
	case <-secondEntered:
		t.Error("sibling acceptance ran before the first status transition persisted")
	case <-time.After(100 * time.Millisecond):
	}
	thirdSubnet := h.get("s3")
	if out := awaitEngineRun(t, asyncEngineRun(h.engine, h.ctx, thirdSubnet, reqFor(thirdSubnet, nil))); !out.Succeeded() {
		t.Fatalf("different network acceptance blocked: %+v", out)
	}
	release()
	if out := awaitEngineRun(t, first); !out.Succeeded() {
		t.Fatalf("first acceptance = %+v", out)
	}
	if out := awaitEngineRun(t, second); !out.Succeeded() {
		t.Fatalf("second acceptance = %+v", out)
	}
	if segment := h.get("s2").Status.NetworkSegment; segment == nil || segment.L2VNI != 101 {
		t.Fatalf("second fabric segment was not stored: %+v", segment)
	}
	if len(h.engine.vnLocks.locks) != 0 {
		t.Fatalf("acceptance locks retained: %d", len(h.engine.vnLocks.locks))
	}
}

func TestProvisioningDisabledRefusesManagerTasks(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	h.engine.cfg.ProvisioningEnabled = false
	if _, err := h.engine.Run(h.ctx, h.get("s1"), fabricReq(CreateSubnet)); err == nil {
		t.Fatal("expected refusal")
	}
}

func TestAcceptFuncRetryErrorLeavesEntryRetrying(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	req := fabricReq(CreateSubnet)
	req.Accept = func(context.Context, Result) error { return &RetryError{Reason: "x"} }
	out := h.run("s1", req)
	h.aap.finish(out.Entry.JobID, "successful", `{"osac_result":{}}`, "")
	out = h.run("s1", req)
	if out.Entry.State != v1alpha1.NetworkJobStateRetrying || !strings.Contains(out.Entry.Message, "x") {
		t.Fatalf("got %+v", out.Entry)
	}
}

func TestFabricSegmentVNIConflictIsRejected(t *testing.T) {
	other := subnetObj("old", "old", "vn2")
	other.Status.NetworkSegment = &v1alpha1.NetworkSegment{Type: "evpn", L2VNI: 100}
	h := newHarness(t, subnetObj("s1", "s1", "vn1"), other)
	req := func() Request {
		r := fabricReq(CreateSubnet)
		r.SerializeVNI = true
		r.Accept = func(ctx context.Context, res Result) error {
			return AcceptSegment(ctx, h.engine.APIReader(), h.lastGet, "evpn", res)
		}
		return r
	}
	out := h.runKeep("s1", req())
	h.aap.finish(out.Entry.JobID, "successful", `{"osac_result":{"network_segment":{"type":"evpn","l2_vni":100}}}`, "")
	out = h.runKeep("s1", req())
	if out.Entry.State != v1alpha1.NetworkJobStateFailed || !strings.Contains(out.Entry.Message, "VNI already in use") {
		t.Fatalf("got %+v", out.Entry)
	}
	if h.get("s1").Status.NetworkSegment != nil {
		t.Fatal("rejected segment was stored")
	}
}

func TestTargetDeletingCancelsRunningJob(t *testing.T) {
	for _, status := range []string{"pending", "running"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t, subnetObj("s1", "s1", "vn1"))
			out := h.run("s1", fabricReq(CreateSubnet))
			h.aap.finish(out.Entry.JobID, status, "", "")
			req := fabricReq(CreateSubnet)
			req.TargetDeleting = true
			out = h.run("s1", req)
			if out.Entry.State != v1alpha1.NetworkJobStateRunning || len(h.aap.cancelled) != 1 ||
				h.aap.cancelled[0] != out.Entry.JobID {
				t.Fatalf("got %+v, cancelled %v", out.Entry, h.aap.cancelled)
			}
			// The job succeeded before the cancel took effect: its result is ignored.
			h.aap.finish(out.Entry.JobID, "successful", "", "")
			out = h.run("s1", req)
			if out.Entry.State != v1alpha1.NetworkJobStateCancelled || len(h.aap.launches) != 1 {
				t.Fatalf("got %+v", out.Entry)
			}
		})
	}
}

func TestNotStartedCancelSurvivesRestart(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	h.run("s1", fabricReq(CreateSubnet))
	h.clock.Step(NotStartedTimeout)
	out := h.run("s1", fabricReq(CreateSubnet))
	if len(h.aap.cancelled) != 1 || out.Entry.Message != MessageNotStarted {
		t.Fatalf("got %+v, cancelled %v", out.Entry, h.aap.cancelled)
	}
	if got := entry(t, h.get("s1"), CreateSubnet); got.Message != MessageNotStarted {
		t.Fatalf("the cancel is not recorded in status: %+v", got)
	}
	// A restart loses the in-memory state; AAP then reports canceled.
	h.engine = New(h.client, h.client, h.aap, Config{TemplatePrefix: "osac", Organization: "osac", ProvisioningEnabled: true}).WithClock(h.clock)
	h.aap.finish(out.Entry.JobID, "canceled", "", "")
	out = h.run("s1", fabricReq(CreateSubnet))
	if out.Entry.State != v1alpha1.NetworkJobStateRetrying || !strings.Contains(out.Entry.Message, MessageNotStarted) ||
		len(h.aap.launches) != 1 {
		t.Fatalf("got %+v after %d launches", out.Entry, len(h.aap.launches))
	}
}

func TestCancelForDeleteCancelsLateJob(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	h.aap.launchErr = fmt.Errorf("timeout")
	h.run("s1", fabricReq(CreateSubnet))
	done, _, err := h.engine.CancelForDelete(h.ctx, h.get("s1"), "Subnet")
	if err != nil || done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	h.clock.Step(LostLaunchTimeout)
	done, _, err = h.engine.CancelForDelete(h.ctx, h.get("s1"), "Subnet")
	if err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if got := entry(t, h.get("s1"), CreateSubnet); got.State != v1alpha1.NetworkJobStateCancelled || got.JobID != "" {
		t.Fatalf("got %+v", got)
	}
	// AAP creates the job after all: it is tracked and cancelled.
	late := h.aap.addJob("osac-create_subnet-fabric-s1-1", "pending")
	done, _, err = h.engine.CancelForDelete(h.ctx, h.get("s1"), "Subnet")
	if err != nil || done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if len(h.aap.cancelled) != 1 || h.aap.cancelled[0] != strconv.Itoa(late) {
		t.Fatalf("cancelled %v", h.aap.cancelled)
	}
	if got := entry(t, h.get("s1"), CreateSubnet); got.JobID != strconv.Itoa(late) {
		t.Fatalf("got %+v", got)
	}
	h.aap.finish(strconv.Itoa(late), "canceled", "", "")
	done, _, err = h.engine.CancelForDelete(h.ctx, h.get("s1"), "Subnet")
	if err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if got := entry(t, h.get("s1"), CreateSubnet); got.State != v1alpha1.NetworkJobStateCancelled {
		t.Fatalf("got %+v", got)
	}
}

func TestForgetDropsTheObjectState(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"), subnetObj("s2", "s2", "vn1"))
	h.run("s1", fabricReq(CreateSubnet))
	h.run("s2", fabricReq(CreateSubnet))
	h.engine.Forget("uid-s1")
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	for key := range h.engine.tracked {
		if key.uid == "uid-s1" {
			t.Fatalf("state of s1 kept: %+v", key)
		}
	}
	if len(h.engine.tracked) != 1 {
		t.Fatalf("state of s2 dropped: %d entries", len(h.engine.tracked))
	}
}

func computeInstanceObj(name string) *v1alpha1.ComputeInstance {
	return &v1alpha1.ComputeInstance{
		TypeMeta:   metav1.TypeMeta{APIVersion: v1alpha1.GroupVersion.String(), Kind: "ComputeInstance"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "osac-vmaas", UID: types.UID("uid-" + name)},
		Spec:       v1alpha1.ComputeInstanceSpec{TemplateID: "test"},
	}
}

func computeInstanceReq() Request {
	return Request{Task: "provision", Manager: v1alpha1.NetworkJobManagerNone, Kind: "ComputeInstance", Template: "provision", Accept: AcceptNoOutput}
}

func (h *harness) getComputeInstance(name string) *v1alpha1.ComputeInstance {
	h.t.Helper()
	ci := &v1alpha1.ComputeInstance{}
	if err := h.client.Get(h.ctx, client.ObjectKey{Namespace: "osac-vmaas", Name: name}, ci); err != nil {
		h.t.Fatal(err)
	}
	return ci
}

func (h *harness) runComputeInstance(name string, req Request) Outcome {
	h.t.Helper()
	out, err := h.engine.Run(h.ctx, h.getComputeInstance(name), req)
	if err != nil {
		h.t.Fatalf("Run: %v", err)
	}
	return out
}

func sameProvisionFailure(got, want *v1alpha1.NetworkJobFailure) bool {
	if got == nil || want == nil {
		return got == want
	}
	return got.Reason == want.Reason && got.Message == want.Message && got.Attempt == want.Attempt && got.ObservedAt.Time.Equal(want.ObservedAt.Time)
}

func TestProvisionFailureSourcesPersistSafeCheckpoint(t *testing.T) {
	cases := []struct {
		name, status, artifacts, explanation string
		reason, message                      string
		state                                v1alpha1.NetworkJobState
		notStarted, lost                     bool
	}{
		{"retryable provider result", "failed", `{"osac_error":{"message":"sentinel-token job 101 task provision manager none traceback retry-after 2030-01-01T00:00:00Z","retryable":true}}`, "", "ProviderReported", "Provisioning failed.", v1alpha1.NetworkJobStateRetrying, false, false},
		{"terminal provider result", "failed", `{"osac_error":{"message":"sentinel-token","retryable":false}}`, "", "ProviderReported", "Provisioning failed.", v1alpha1.NetworkJobStateFailed, false, false},
		{"missing error artifact", "failed", `{}`, "failed", "InvalidResult", "Internal error", v1alpha1.NetworkJobStateFailed, false, false},
		{"malformed error artifact", "failed", `{"osac_error":{"message":"sentinel-token","retryable":"true"}}`, "failed", "InvalidResult", "Internal error", v1alpha1.NetworkJobStateFailed, false, false},
		{"unexpected successful result", "successful", `{"osac_result":{"unexpected":"sentinel-token"}}`, "", "ResultRejected", "Internal error", v1alpha1.NetworkJobStateFailed, false, false},
		{"rejected successful result", "successful", `{"osac_result":[]}`, "", "ResultRejected", "Internal error", v1alpha1.NetworkJobStateFailed, false, false},
		{"timeout takes precedence", "failed", `{"osac_error":{"message":"sentinel-token","retryable":false}}`, "Job terminated due to timeout", "TimedOut", "The provisioning attempt timed out.", v1alpha1.NetworkJobStateRetrying, false, false},
		{"lost AAP job", "", "", "", "JobLost", "Internal error", v1alpha1.NetworkJobStateRetrying, false, true},
		{"AAP infrastructure error", "error", `{}`, "infrastructure sentinel", "ProviderError", "Internal error", v1alpha1.NetworkJobStateRetrying, false, false},
		{"not started", "canceled", `{}`, "", "JobNotStarted", "Internal error", v1alpha1.NetworkJobStateRetrying, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, computeInstanceObj("ci"))
			req := computeInstanceReq()
			if out := h.runComputeInstance("ci", req); out.Entry.State != v1alpha1.NetworkJobStateRunning {
				t.Fatalf("launch outcome %+v", out)
			}
			h.clock.Step(123 * time.Millisecond)
			if tc.notStarted {
				h.clock.Step(NotStartedTimeout)
				if out := h.runComputeInstance("ci", req); out.Entry.Message != MessageNotStarted {
					t.Fatalf("not-started marker outcome %+v", out)
				}
				h.aap.finish("101", tc.status, tc.artifacts, tc.explanation)
			} else if tc.lost {
				h.aap.mu.Lock()
				delete(h.aap.jobs, 101)
				h.aap.mu.Unlock()
			} else {
				h.aap.finish("101", tc.status, tc.artifacts, tc.explanation)
			}
			observed := metav1.NewTime(h.clock.Now().UTC().Truncate(time.Second))
			out := h.runComputeInstance("ci", req)
			if out.Entry.State != tc.state {
				t.Fatalf("state %s, want %s", out.Entry.State, tc.state)
			}
			if out.Retrying() != (tc.state == v1alpha1.NetworkJobStateRetrying) {
				t.Fatalf("Retrying()=%t for state %s", out.Retrying(), tc.state)
			}
			want := &v1alpha1.NetworkJobFailure{Reason: tc.reason, Message: tc.message, Attempt: 1, ObservedAt: observed}
			if !sameProvisionFailure(out.LastFailure, want) || !sameProvisionFailure(out.Entry.LastFailure, want) {
				t.Fatalf("outcome checkpoint %+v and entry checkpoint %+v, want %+v", out.LastFailure, out.Entry.LastFailure, want)
			}
			stored := h.getComputeInstance("ci")
			if !sameProvisionFailure(stored.Status.Jobs[0].LastFailure, want) {
				t.Fatalf("stored checkpoint %+v, want %+v", stored.Status.Jobs[0].LastFailure, want)
			}
			if strings.Contains(fmt.Sprint(out.LastFailure), "sentinel-token") || strings.Contains(fmt.Sprint(out.LastFailure), "infrastructure sentinel") {
				t.Fatalf("provider detail leaked into checkpoint: %+v", out.LastFailure)
			}
		})
	}
}

func TestProvisionFailureCheckpointSurvivesRetryAndClearsOnSuccess(t *testing.T) {
	h := newHarness(t, computeInstanceObj("ci"))
	req := computeInstanceReq()
	h.runComputeInstance("ci", req)
	h.aap.finish("101", "failed", `{"osac_error":{"message":"first secret","retryable":true}}`, "")
	first := h.runComputeInstance("ci", req)
	firstFailure := first.LastFailure
	if firstFailure == nil || firstFailure.Attempt != 1 || firstFailure.Message != "Provisioning failed." {
		t.Fatalf("first failure %+v", firstFailure)
	}

	// A new engine reconstructs retry timing and replays the persisted checkpoint.
	h.engine = New(h.client, h.client, h.aap, Config{TemplatePrefix: "osac", Organization: "osac", ProvisioningEnabled: true}).WithClock(h.clock)
	replayed := h.runComputeInstance("ci", req)
	if !replayed.Retrying() || !sameProvisionFailure(replayed.LastFailure, firstFailure) {
		t.Fatalf("restart outcome %+v", replayed)
	}
	h.clock.Step(replayed.RequeueAfter + time.Second)
	req.HoldLaunch = true
	held := h.runComputeInstance("ci", req)
	if !held.Retrying() || !sameProvisionFailure(held.LastFailure, firstFailure) {
		t.Fatalf("held retry lost checkpoint: %+v", held)
	}
	req.HoldLaunch = false
	launched := h.runComputeInstance("ci", req)
	if launched.Entry.Attempt != 2 || !sameProvisionFailure(launched.LastFailure, firstFailure) {
		t.Fatalf("retry launch outcome %+v", launched)
	}
	if got := h.aap.last(); got.ExtraVars["osac_job_vars"] == nil {
		t.Fatal("retry launch was not sent to AAP")
	}

	h.aap.finish(launched.Entry.JobID, "failed", `{"osac_error":{"message":"second secret","retryable":true}}`, "")
	second := h.runComputeInstance("ci", req)
	if second.LastFailure == nil || second.LastFailure.Attempt != 2 || second.LastFailure.ObservedAt.Time.Equal(firstFailure.ObservedAt.Time) {
		t.Fatalf("second failure did not replace checkpoint: first=%+v second=%+v", firstFailure, second.LastFailure)
	}

	instance := h.getComputeInstance("ci")
	h.engine.MarkForRerun(instance, "provision", v1alpha1.NetworkJobManagerNone)
	if err := h.client.Status().Update(h.ctx, instance); err != nil {
		t.Fatal(err)
	}
	cleared := h.getComputeInstance("ci")
	if cleared.Status.Jobs[0].LastFailure != nil {
		t.Fatalf("configuration rerun retained checkpoint: %+v", cleared.Status.Jobs[0].LastFailure)
	}
	third := h.runComputeInstance("ci", req)
	h.aap.finish(third.Entry.JobID, "successful", `{}`, "")
	succeeded := h.runComputeInstance("ci", req)
	if succeeded.Entry.State != v1alpha1.NetworkJobStateSucceeded || succeeded.LastFailure != nil || succeeded.Entry.LastFailure != nil {
		t.Fatalf("success did not clear checkpoint: %+v", succeeded)
	}
}

func TestProvisionDeletionClearsFailureCheckpoint(t *testing.T) {
	h := newHarness(t, computeInstanceObj("ci"))
	req := computeInstanceReq()
	h.runComputeInstance("ci", req)
	h.aap.finish("101", "failed", `{"osac_error":{"message":"secret","retryable":true}}`, "")
	h.runComputeInstance("ci", req)
	ci := h.getComputeInstance("ci")
	done, _, err := h.engine.CancelForDelete(h.ctx, ci, "ComputeInstance")
	if err != nil || !done {
		t.Fatalf("CancelForDelete done=%t err=%v", done, err)
	}
	deleted := h.getComputeInstance("ci")
	if deleted.Status.Jobs[0].State != v1alpha1.NetworkJobStateCancelled || deleted.Status.Jobs[0].LastFailure != nil {
		t.Fatalf("deletion did not clear retry checkpoint: %+v", deleted.Status.Jobs[0])
	}
}

func TestUnrelatedTaskFailureHasNoProvisionCheckpoint(t *testing.T) {
	h := newHarness(t, subnetObj("s1", "s1", "vn1"))
	req := fabricReq(CreateSubnet)
	h.run("s1", req)
	h.aap.finish("101", "failed", `{"osac_error":{"message":"provider detail","retryable":false}}`, "")
	out := h.run("s1", req)
	if out.LastFailure != nil || out.Entry.LastFailure != nil {
		t.Fatalf("unrelated networking task got provision checkpoint: %+v", out)
	}
}

func TestLegacyProvisionEntriesDoNotInventFailureHistory(t *testing.T) {
	t.Run("retrying entry stays retrying without a checkpoint", func(t *testing.T) {
		ci := computeInstanceObj("ci")
		ci.Status.Jobs = []v1alpha1.NetworkJobStatus{{Task: "provision", Manager: v1alpha1.NetworkJobManagerNone, Attempt: 1, State: v1alpha1.NetworkJobStateRetrying, Message: MessageRerun}}
		h := newHarness(t, ci)
		req := computeInstanceReq()
		req.HoldLaunch = true
		out := h.runComputeInstance("ci", req)
		if !out.Retrying() || out.LastFailure != nil || out.Entry.LastFailure != nil {
			t.Fatalf("legacy retry gained failure history: %+v", out)
		}
	})

	t.Run("running attempt does not imply retry lineage", func(t *testing.T) {
		ci := computeInstanceObj("ci")
		ci.Status.Jobs = []v1alpha1.NetworkJobStatus{{Task: "provision", Manager: v1alpha1.NetworkJobManagerNone, Attempt: 3, State: v1alpha1.NetworkJobStateRunning, JobID: "999"}}
		h := newHarness(t, ci)
		h.aap.jobs[999] = &aap.Job{ID: 999, Status: "running"}
		out := h.runComputeInstance("ci", computeInstanceReq())
		if out.Entry.State != v1alpha1.NetworkJobStateRunning || out.LastFailure != nil || out.Entry.LastFailure != nil {
			t.Fatalf("legacy running entry gained failure history: %+v", out)
		}
	})

	t.Run("cancelled relaunch has no synthesized checkpoint", func(t *testing.T) {
		ci := computeInstanceObj("ci")
		ci.Status.Jobs = []v1alpha1.NetworkJobStatus{{Task: "provision", Manager: v1alpha1.NetworkJobManagerNone, Attempt: 3, State: v1alpha1.NetworkJobStateCancelled}}
		h := newHarness(t, ci)
		out := h.runComputeInstance("ci", computeInstanceReq())
		if out.Entry.Attempt != 4 || out.LastFailure != nil || out.Entry.LastFailure != nil {
			t.Fatalf("legacy cancelled relaunch gained failure history: %+v", out)
		}
	})
}

func TestProvisionDeletionClearsRunningAndTerminalFailureCheckpoint(t *testing.T) {
	t.Run("running retry clears after AAP acknowledges deletion cancel", func(t *testing.T) {
		h := newHarness(t, computeInstanceObj("ci"))
		req := computeInstanceReq()
		h.runComputeInstance("ci", req)
		h.aap.finish("101", "failed", `{"osac_error":{"message":"first","retryable":true}}`, "")
		first := h.runComputeInstance("ci", req)
		h.clock.Step(first.RequeueAfter + time.Second)
		h.runComputeInstance("ci", req)
		if out := h.runComputeInstance("ci", req); out.Entry.Attempt != 2 || out.LastFailure == nil {
			t.Fatalf("retry did not retain checkpoint: %+v", out)
		}
		ci := h.getComputeInstance("ci")
		done, _, err := h.engine.CancelForDelete(h.ctx, ci, "ComputeInstance")
		if err != nil || done {
			t.Fatalf("first delete cancel done=%t err=%v", done, err)
		}
		h.aap.finish("102", "canceled", `{}`, "")
		ci = h.getComputeInstance("ci")
		done, _, err = h.engine.CancelForDelete(h.ctx, ci, "ComputeInstance")
		if err != nil || !done {
			t.Fatalf("second delete cancel done=%t err=%v", done, err)
		}
		stored := h.getComputeInstance("ci")
		if stored.Status.Jobs[0].State != v1alpha1.NetworkJobStateCancelled || stored.Status.Jobs[0].LastFailure != nil {
			t.Fatalf("running deletion retained checkpoint: %+v", stored.Status.Jobs[0])
		}
	})

	t.Run("terminal failed entry clears without changing its state", func(t *testing.T) {
		h := newHarness(t, computeInstanceObj("ci"))
		req := computeInstanceReq()
		h.runComputeInstance("ci", req)
		h.aap.finish("101", "failed", `{"osac_error":{"message":"private","retryable":false}}`, "")
		failed := h.runComputeInstance("ci", req)
		if failed.Entry.State != v1alpha1.NetworkJobStateFailed || failed.LastFailure == nil {
			t.Fatalf("terminal failure missing checkpoint: %+v", failed)
		}
		ci := h.getComputeInstance("ci")
		done, _, err := h.engine.CancelForDelete(h.ctx, ci, "ComputeInstance")
		if err != nil || !done {
			t.Fatalf("terminal delete cancel done=%t err=%v", done, err)
		}
		stored := h.getComputeInstance("ci")
		if stored.Status.Jobs[0].State != v1alpha1.NetworkJobStateFailed || stored.Status.Jobs[0].LastFailure != nil {
			t.Fatalf("terminal deletion changed state or retained checkpoint: %+v", stored.Status.Jobs[0])
		}
	})
}

func TestProvisionSuccessClearsRetainedFailure(t *testing.T) {
	h := newHarness(t, computeInstanceObj("ci"))
	req := computeInstanceReq()
	h.runComputeInstance("ci", req)
	h.aap.finish("101", "failed", `{"osac_error":{"message":"private","retryable":true}}`, "")
	failed := h.runComputeInstance("ci", req)
	h.clock.Step(failed.RequeueAfter)
	running := h.runComputeInstance("ci", req)
	if running.LastFailure == nil {
		t.Fatal("retry lost failure before success")
	}
	h.aap.finish(running.Entry.JobID, "successful", `{}`, "")
	succeeded := h.runComputeInstance("ci", req)
	if !succeeded.Succeeded() || succeeded.LastFailure != nil || h.getComputeInstance("ci").Status.Jobs[0].LastFailure != nil {
		t.Fatalf("success retained failure: %+v", succeeded)
	}
}

func TestProvisionFailureReplaysWithoutRefetchAndSurvivesLostLaunch(t *testing.T) {
	h := newHarness(t, computeInstanceObj("ci"))
	req := computeInstanceReq()
	h.runComputeInstance("ci", req)
	h.aap.finish("101", "failed", `{"osac_error":{"message":"private","retryable":false}}`, "")
	failed := h.runComputeInstance("ci", req)
	delete(h.aap.jobs, 101)
	h.engine = New(h.client, h.client, h.aap, Config{Organization: "osac"}).WithClock(h.clock)
	replay := h.runComputeInstance("ci", req)
	if !replay.Failed() || !sameProvisionFailure(replay.LastFailure, failed.LastFailure) {
		t.Fatalf("terminal replay refetched or changed failure: %+v", replay)
	}
	// A manual cancellation can relaunch without erasing the last observation.
	ci := h.getComputeInstance("ci")
	ci.Status.Jobs[0].State = v1alpha1.NetworkJobStateCancelled
	if err := h.client.Status().Update(h.ctx, ci); err != nil {
		t.Fatal(err)
	}

	req.HoldLaunch = true
	held := h.runComputeInstance("ci", req)
	if held.Entry.State != v1alpha1.NetworkJobStateCancelled || !sameProvisionFailure(held.LastFailure, failed.LastFailure) {
		t.Fatalf("held cancelled relaunch lost checkpoint: %+v", held)
	}
	req.HoldLaunch = false
	h.aap.launchErr = errors.New("lost launch response")
	lost := h.runComputeInstance("ci", req)
	if lost.Entry.Attempt != 2 || lost.Entry.JobID != "" || !sameProvisionFailure(lost.LastFailure, failed.LastFailure) {
		t.Fatalf("reservation lost checkpoint: %+v", lost)
	}
	label := JobLabel(req.Task, req.Manager, APIID(ci, req.Kind), 2)
	id := h.aap.addJob(label, "running")
	h.engine = New(h.client, h.client, h.aap, Config{Organization: "osac"}).WithClock(h.clock)
	recovered := h.runComputeInstance("ci", req)
	if recovered.Entry.JobID != strconv.Itoa(id) || !sameProvisionFailure(recovered.LastFailure, failed.LastFailure) {
		t.Fatalf("label recovery lost checkpoint: %+v", recovered)
	}
}

func TestProvisionManualCancellationRetainsCheckpointWhileHeld(t *testing.T) {
	h := newHarness(t, computeInstanceObj("ci"))
	req := computeInstanceReq()
	h.runComputeInstance("ci", req)
	h.aap.finish("101", "failed", `{"osac_error":{"message":"private","retryable":true}}`, "")
	failed := h.runComputeInstance("ci", req)
	h.clock.Step(failed.RequeueAfter)
	running := h.runComputeInstance("ci", req)
	h.aap.finish(running.Entry.JobID, "canceled", `{}`, "")
	req.HoldLaunch = true
	for range 3 {
		held := h.runComputeInstance("ci", req)
		if held.Entry.State != v1alpha1.NetworkJobStateCancelled || !sameProvisionFailure(held.LastFailure, failed.LastFailure) {
			t.Fatalf("manual cancellation hid checkpoint: %+v", held)
		}
	}
	req.HoldLaunch = false
	relaunched := h.runComputeInstance("ci", req)
	if relaunched.Entry.Attempt != 3 || !sameProvisionFailure(relaunched.LastFailure, failed.LastFailure) {
		t.Fatalf("manual relaunch lost checkpoint: %+v", relaunched)
	}
}
