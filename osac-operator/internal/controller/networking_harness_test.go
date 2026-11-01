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

package controller

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
)

// Test doubles and fixtures of the networking resource controller specs.

// netFakeAAP is an in-memory AAP for the networking controller specs.
type netFakeAAP struct {
	mu       sync.Mutex
	nextID   int
	jobs     map[int]*aap.Job
	jobLabel map[int]string
	labels   map[string]int
	launches []netLaunch
	// cancelled are the job IDs CancelJob was called for, in order.
	cancelled []int
}

type netLaunch struct {
	id       int
	template string
	vars     map[string]any
	label    string
}

func newNetFakeAAP() *netFakeAAP {
	return &netFakeAAP{nextID: 1000, jobs: map[int]*aap.Job{}, jobLabel: map[int]string{}, labels: map[string]int{}}
}

func (f *netFakeAAP) EnsureLabel(_ context.Context, _ string, name string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.labels[name]; ok {
		return id, nil
	}
	id := len(f.labels) + 1
	f.labels[name] = id
	return id, nil
}

func (f *netFakeAAP) LaunchJobTemplate(_ context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	label := ""
	for name, id := range f.labels {
		if len(req.Labels) > 0 && id == req.Labels[0] {
			label = name
		}
	}
	f.jobs[f.nextID] = &aap.Job{ID: f.nextID, Status: "running"}
	f.jobLabel[f.nextID] = label
	// Round-trip the vars through JSON, as AAP would see them.
	data, _ := json.Marshal(req.ExtraVars["osac_job_vars"])
	vars := map[string]any{}
	_ = json.Unmarshal(data, &vars)
	f.launches = append(f.launches, netLaunch{id: f.nextID, template: req.TemplateName, vars: vars, label: label})
	return &aap.LaunchJobTemplateResponse{JobID: f.nextID}, nil
}

func (f *netFakeAAP) GetJob(_ context.Context, id string) (*aap.Job, error) {
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

func (f *netFakeAAP) CancelJob(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, _ := strconv.Atoi(id)
	f.cancelled = append(f.cancelled, n)
	if job, ok := f.jobs[n]; ok {
		job.Status = "canceled"
	}
	return nil
}

func (f *netFakeAAP) ListJobsByLabel(_ context.Context, name string) ([]aap.Job, error) {
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

// tasks lists the launched "<task>/<manager>" in order, optionally only of one resource ID.
func (f *netFakeAAP) tasks(resourceID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, l := range f.launches {
		if resourceID != "" {
			resource, _ := l.vars["resource"].(map[string]any)
			if resource["id"] != resourceID {
				continue
			}
		}
		out = append(out, l.vars["task"].(string)+"/"+managerType(l.vars))
	}
	return out
}

func managerType(vars map[string]any) string {
	manager, _ := vars["manager"].(map[string]any)
	if manager == nil {
		return "none"
	}
	return manager["type"].(string)
}

// latest returns the latest launch of a task on a manager type.
func (f *netFakeAAP) latest(task, manager string) netLaunch {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.launches) - 1; i >= 0; i-- {
		l := f.launches[i]
		if l.vars["task"] == task && managerType(l.vars) == manager {
			return l
		}
	}
	Expect(true).To(BeFalse(), "no launch of %s on %s", task, manager)
	return netLaunch{}
}

// finish ends the latest job of a task with an AAP status and artifacts.
func (f *netFakeAAP) finish(task, manager, status, artifacts string) {
	id := f.latest(task, manager).id
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs[id].Status = status
	if artifacts != "" {
		f.jobs[id].Artifacts = json.RawMessage(artifacts)
	}
}

// finishAll ends every launched job of a task on a manager type.
func (f *netFakeAAP) finishAll(task, manager, status, artifacts string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.launches {
		if l.vars["task"] == task && managerType(l.vars) == manager {
			f.jobs[l.id].Status = status
			f.jobs[l.id].Artifacts = json.RawMessage(artifacts)
		}
	}
}

func (f *netFakeAAP) succeed(task, manager string) { f.finish(task, manager, "successful", "") }

// cancelledJobs returns the job IDs CancelJob was called for.
func (f *netFakeAAP) cancelledJobs() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.cancelled...)
}

// netHarness is one networking namespace with an engine on the fake AAP.
type netHarness struct {
	ns     string
	aap    *netFakeAAP
	engine *networktask.Engine
}

func newNetHarness(enabled bool) *netHarness {
	h := &netHarness{ns: crdTestName("net"), aap: newNetFakeAAP()}
	Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: h.ns}})).To(Succeed())
	h.engine = networktask.New(k8sClient, k8sClient, h.aap, networktask.Config{
		TemplatePrefix: "osac", Organization: "osac", ProvisioningEnabled: enabled,
	})
	return h
}

var (
	testFabricRef = &v1alpha1.NetworkManagerRef{Name: "acme_fabric", AnsibleRole: "acme.network.fabric"}
	testK8sRef    = &v1alpha1.NetworkManagerRef{Name: "cudn_evpn", AnsibleRole: "osac.templates.cudn_evpn"}
)

func testManagers(k8s bool) *v1alpha1.NetworkManagers {
	m := &v1alpha1.NetworkManagers{Fabric: testFabricRef.DeepCopy()}
	if k8s {
		m.Kubernetes = testK8sRef.DeepCopy()
	}
	return m
}

// meta returns the metadata of a networking CR with its API ID label and tenant.
func (h *netHarness) meta(kind, id string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name: crdTestName(strings.ToLower(kind)) + "-" + id, Namespace: h.ns,
		Labels:      map[string]string{networktask.IDLabel(kind): id},
		Annotations: map[string]string{networktask.TenantAnnotation: "tenant-a"},
	}
}

// networkClass creates a Ready NetworkClass.
func (h *netHarness) networkClass(k8s, eastWest bool) {
	nc := &v1alpha1.NetworkClass{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: h.ns, Labels: map[string]string{osacNetworkClassIDLabel: "nc-1"}},
		Spec: v1alpha1.NetworkClassSpec{
			FabricManager: "acme_fabric",
			Defaults:      v1alpha1.NetworkClassDefaults{VirtualNetworkIPv4CIDR: "10.0.0.0/16", SubnetIPv4CIDR: "10.0.1.0/24"},
			AddressPlan:   v1alpha1.SubnetAddressPlan{Overlay: 8, Fabric: 4, LBVIP: 4},
		},
	}
	if k8s {
		nc.Spec.K8sManager = "cudn_evpn"
	}
	if eastWest {
		nc.Spec.EastWestCapabilities = &v1alpha1.NetworkClassEastWestCapabilities{SupportsEastWestEthernet: true}
	}
	Expect(k8sClient.Create(ctx, nc)).To(Succeed())
	nc.Status.NetworkManagers = testManagers(k8s)
	if k8s {
		nc.Status.NetworkType = v1alpha1.NetworkTypeEVPN
	}
	meta.SetStatusCondition(&nc.Status.Conditions, metav1.Condition{Type: v1alpha1.NetworkConditionReady,
		Status: metav1.ConditionTrue, Reason: v1alpha1.ReasonSucceeded})
	Expect(k8sClient.Status().Update(ctx, nc)).To(Succeed())
}

// markReady sets the status of a parent fixture: managers, Ready and extra changes.
func markReady[T networkResource](obj T, k8s bool, mutate func(T)) T {
	obj.SetNetworkManagers(testManagers(k8s))
	networktask.SetSucceeded(obj)
	if mutate != nil {
		mutate(obj)
	}
	Expect(k8sClient.Status().Update(ctx, obj)).To(Succeed())
	return obj
}

func (h *netHarness) virtualNetwork(id string) *v1alpha1.VirtualNetwork {
	vn := &v1alpha1.VirtualNetwork{ObjectMeta: h.meta(kindVirtualNetwork, id), Spec: v1alpha1.VirtualNetworkSpec{IPv4CIDR: "10.0.0.0/16"}}
	Expect(k8sClient.Create(ctx, vn)).To(Succeed())
	return vn
}

func (h *netHarness) readyVirtualNetwork(id string, k8s bool) *v1alpha1.VirtualNetwork {
	return markReady(h.virtualNetwork(id), k8s, func(vn *v1alpha1.VirtualNetwork) {
		if k8s {
			vn.Status.NetworkType = v1alpha1.NetworkTypeEVPN
			vn.Status.L3VNI = 20001
		}
	})
}

func (h *netHarness) subnet(id, vnID, cidr string, plan v1alpha1.SubnetAddressPlan) *v1alpha1.Subnet {
	s := &v1alpha1.Subnet{ObjectMeta: h.meta(kindSubnet, id),
		Spec: v1alpha1.SubnetSpec{VirtualNetwork: vnID, IPv4CIDR: cidr, AddressPlan: plan}}
	s.Labels[networktask.VirtualNetworkIDLabel] = vnID
	Expect(k8sClient.Create(ctx, s)).To(Succeed())
	return s
}

var testPlan = v1alpha1.SubnetAddressPlan{Overlay: 8, Fabric: 4, LBVIP: 4}

// readySubnet is a Ready Subnet 10.0.1.0/24 with its layout and segment.
func (h *netHarness) readySubnet(id, vnID string, k8s bool, plan v1alpha1.SubnetAddressPlan) *v1alpha1.Subnet {
	return markReady(h.subnet(id, vnID, "10.0.1.0/24", plan), k8s, func(s *v1alpha1.Subnet) {
		ipam, err := networktask.Layout(s.Spec.IPv4CIDR, plan)
		Expect(err).NotTo(HaveOccurred())
		s.Status.IPAM = ipam
		if k8s {
			s.Status.NetworkType = v1alpha1.NetworkTypeEVPN
			s.Status.NetworkSegment = &v1alpha1.NetworkSegment{Type: v1alpha1.NetworkTypeEVPN, L2VNI: 10100}
		}
	})
}

// taskInput is the stored task input of a networking CR.
func taskInput(obj v1alpha1.NetworkInputObject) map[string]any {
	input, err := networktask.LoadInput(obj)
	Expect(err).NotTo(HaveOccurred())
	return input
}

// reconcileNet runs one reconcile of r for obj and reads obj back; it
// returns false when the object is gone.
func reconcileNet(r reconcile.Reconciler, obj client.Object) (ctrl.Result, bool) {
	result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
	Expect(err).NotTo(HaveOccurred())
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
		return result, false
	}
	return result, true
}

func readyOf(obj networkResource) *metav1.Condition {
	return meta.FindStatusCondition(*obj.GetConditionsRef(), v1alpha1.NetworkConditionReady)
}

func expectReady(obj networkResource, status metav1.ConditionStatus, reason string) *metav1.Condition {
	ready := readyOf(obj)
	ExpectWithOffset(1, ready).NotTo(BeNil())
	ExpectWithOffset(1, ready.Status).To(Equal(status), ready.Message)
	ExpectWithOffset(1, ready.Reason).To(Equal(reason), ready.Message)
	return ready
}

func jobState(obj v1alpha1.NetworkTaskObject, task string, manager v1alpha1.NetworkJobManager) v1alpha1.NetworkJobState {
	entry := networktask.FindJob(obj.GetNetworkJobs(), task, manager)
	if entry == nil {
		return ""
	}
	return entry.State
}

// path reads a nested key of job vars or an input.
func path(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		next, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = next[k]
	}
	return cur
}

// deleteNet deletes a networking CR (it keeps finalizers, so it stays while deleting).
func deleteNet(obj client.Object) {
	Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
}
