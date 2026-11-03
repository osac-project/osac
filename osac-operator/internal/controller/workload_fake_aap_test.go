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
	"sync"
	"time"

	. "github.com/onsi/gomega"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// Test doubles of the workload controller specs (ComputeInstance, ClusterOrder)
// that launch their jobs through the network task engine.

// wlFakeAAP is an in-memory AAP that records every launch.
type wlFakeAAP struct {
	mu        sync.Mutex
	nextID    int
	jobs      map[int]*aap.Job
	jobLabel  map[int]string
	labels    map[string]int
	launches  []wlLaunch
	cancelled []string
	// onLaunch, when set, sees each launch as AAP would receive it.
	onLaunch func(wlLaunch)
}

type wlLaunch struct {
	id       int
	template string
	label    string
	vars     map[string]any
}

func newWLFakeAAP() *wlFakeAAP {
	return &wlFakeAAP{nextID: 5000, jobs: map[int]*aap.Job{}, jobLabel: map[int]string{}, labels: map[string]int{}}
}

func (f *wlFakeAAP) EnsureLabel(_ context.Context, _ string, name string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.labels[name]; ok {
		return id, nil
	}
	id := len(f.labels) + 1
	f.labels[name] = id
	return id, nil
}

func (f *wlFakeAAP) LaunchJobTemplate(_ context.Context, req aap.LaunchJobTemplateRequest) (*aap.LaunchJobTemplateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	label := ""
	for name, id := range f.labels {
		if len(req.Labels) > 0 && id == req.Labels[0] {
			label = name
		}
	}
	f.jobs[f.nextID] = &aap.Job{ID: f.nextID, Status: "pending"}
	f.jobLabel[f.nextID] = label
	vars, _ := req.ExtraVars["osac_job_vars"].(map[string]any)
	// Round-trip through JSON so assertions see what AAP would receive.
	data, _ := json.Marshal(vars)
	decoded := map[string]any{}
	_ = json.Unmarshal(data, &decoded)
	launch := wlLaunch{id: f.nextID, template: req.TemplateName, label: label, vars: decoded}
	f.launches = append(f.launches, launch)
	if f.onLaunch != nil {
		f.onLaunch(launch)
	}
	return &aap.LaunchJobTemplateResponse{JobID: f.nextID}, nil
}

func (f *wlFakeAAP) GetJob(_ context.Context, id string) (*aap.Job, error) {
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

func (f *wlFakeAAP) CancelJob(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, id)
	n, _ := strconv.Atoi(id)
	if job, ok := f.jobs[n]; ok {
		job.Status = "canceled"
	}
	return nil
}

func (f *wlFakeAAP) ListJobsByLabel(_ context.Context, name string) ([]aap.Job, error) {
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

// tasks returns the launched tasks in order.
func (f *wlFakeAAP) tasks() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.launches))
	for _, launch := range f.launches {
		task, _ := launch.vars["task"].(string)
		out = append(out, task)
	}
	return out
}

// last returns the latest launch of a task, failing the spec when there is none.
func (f *wlFakeAAP) last(task string) wlLaunch {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.launches) - 1; i >= 0; i-- {
		if f.launches[i].vars["task"] == task {
			return f.launches[i]
		}
	}
	Expect(task).To(BeEmpty(), "no launch of task %s", task)
	return wlLaunch{}
}

// lastVars returns the osac_job_vars of the latest launch, or nil.
func (f *wlFakeAAP) lastVars() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.launches) == 0 {
		return nil
	}
	return f.launches[len(f.launches)-1].vars
}

// finish ends the latest job of a task with an AAP status and artifacts.
func (f *wlFakeAAP) finish(task, status, artifacts string) {
	launch := f.last(task)
	f.mu.Lock()
	defer f.mu.Unlock()
	job := f.jobs[launch.id]
	job.Status = status
	if artifacts != "" {
		job.Artifacts = json.RawMessage(artifacts)
	}
}

// newTestCIReconciler returns a ComputeInstance reconciler with provider
// networking disabled (the VM runs in the Tenant namespace, as before the
// networking designs) and a fake AAP.
func newTestCIReconciler(computeInstanceNamespace, tenantNamespace, networkingNamespace string, poll time.Duration) *ComputeInstanceReconciler {
	r, _ := newTestCIReconcilerWithAAP(false, computeInstanceNamespace, tenantNamespace, networkingNamespace, poll)
	return r
}

func newTestCIReconcilerWithAAP(enabled bool, computeInstanceNamespace, tenantNamespace, networkingNamespace string, poll time.Duration) (*ComputeInstanceReconciler, *wlFakeAAP) {
	fake := newWLFakeAAP()
	return newTestCIReconcilerOn(fake, enabled, computeInstanceNamespace, tenantNamespace, networkingNamespace, poll), fake
}

// newTestCIReconcilerWithFake returns a ComputeInstance reconciler with provider
// networking disabled that launches its jobs on the given fake AAP.
func newTestCIReconcilerWithFake(fake *wlFakeAAP, computeInstanceNamespace, tenantNamespace, networkingNamespace string, poll time.Duration) *ComputeInstanceReconciler {
	return newTestCIReconcilerOn(fake, false, computeInstanceNamespace, tenantNamespace, networkingNamespace, poll)
}

func newTestCIReconcilerOn(fake *wlFakeAAP, enabled bool, computeInstanceNamespace, tenantNamespace, networkingNamespace string, poll time.Duration) *ComputeInstanceReconciler {
	local := testMcManager.GetLocalManager()
	engine := networktask.New(local.GetClient(), local.GetAPIReader(), fake, networktask.Config{
		TemplatePrefix:      "osac",
		Organization:        "osac",
		ProvisioningEnabled: enabled,
		PollInterval:        100 * time.Millisecond,
	})
	return NewComputeInstanceReconciler(testMcManager, computeInstanceNamespace, tenantNamespace, networkingNamespace,
		engine, "osac-create-compute-instance", "osac-delete-compute-instance", poll, mcmanager.LocalCluster)
}

// triggerCapture records the launches of a fake AAP and the storage input of the latest one.
type triggerCapture struct {
	count int
	scs   []osacv1alpha1.ResolvedStorageClass
	tiers []provisioning.TierDefinition
	conns map[string]provisioning.BackendConnection
}

// capturingAAP makes the fake AAP record its next launches in c.
func capturingAAP(f *wlFakeAAP, c *triggerCapture) *wlFakeAAP {
	f.onLaunch = func(launch wlLaunch) {
		c.count++
		c.scs = tenantStorageClassesOf(launch.vars)
		c.tiers = tierDefinitionsOf(launch.vars)
		c.conns = backendConnectionsOf(launch.vars)
	}
	return f
}

// addJob records a job in the fake AAP with the given status, as a provision
// job of an earlier reconcile would be, and returns its ID.
func (f *wlFakeAAP) addJob(status string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.jobs[f.nextID] = &aap.Job{ID: f.nextID, Status: status}
	return strconv.Itoa(f.nextID)
}

// setJobStatus changes the AAP status of a job.
func (f *wlFakeAAP) setJobStatus(id, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, _ := strconv.Atoi(id)
	f.jobs[n].Status = status
}

// launchedTenantStorageClasses returns the tenant_storage_classes of the latest launch.
func launchedTenantStorageClasses(f *wlFakeAAP) []osacv1alpha1.ResolvedStorageClass {
	vars := f.lastVars()
	Expect(vars).NotTo(BeNil(), "no job was launched")
	return tenantStorageClassesOf(vars)
}

func tenantStorageClassesOf(vars map[string]any) []osacv1alpha1.ResolvedStorageClass {
	raw, ok := vars["tenant_storage_classes"].([]any)
	if !ok {
		return nil
	}
	out := make([]osacv1alpha1.ResolvedStorageClass, 0, len(raw))
	for _, item := range raw {
		entry := item.(map[string]any)
		out = append(out, osacv1alpha1.ResolvedStorageClass{Name: entry["name"].(string), Tier: entry["tier"].(string)})
	}
	return out
}

// launchedTierDefinitions returns the storage_tier_definitions of the latest launch.
func launchedTierDefinitions(f *wlFakeAAP) []provisioning.TierDefinition {
	vars := f.lastVars()
	Expect(vars).NotTo(BeNil(), "no job was launched")
	return tierDefinitionsOf(vars)
}

func tierDefinitionsOf(vars map[string]any) []provisioning.TierDefinition {
	raw, ok := vars["storage_tier_definitions"].([]any)
	if !ok {
		return nil
	}
	out := make([]provisioning.TierDefinition, 0, len(raw))
	for _, item := range raw {
		entry := item.(map[string]any)
		backendID, _ := entry["backend_id"].(string)
		out = append(out, provisioning.TierDefinition{Name: entry["name"].(string), BackendID: backendID})
	}
	return out
}

// launchedBackendConnections returns the storage_backend_connections of the latest launch.
func launchedBackendConnections(f *wlFakeAAP) map[string]provisioning.BackendConnection {
	vars := f.lastVars()
	Expect(vars).NotTo(BeNil(), "no job was launched")
	return backendConnectionsOf(vars)
}

func backendConnectionsOf(vars map[string]any) map[string]provisioning.BackendConnection {
	raw, ok := vars["storage_backend_connections"].(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]provisioning.BackendConnection{}
	for id, item := range raw {
		entry := item.(map[string]any)
		out[id] = provisioning.BackendConnection{
			Endpoint: entry["endpoint"].(string),
			Username: entry["username"].(string),
			Password: entry["password"].(string),
		}
	}
	return out
}
