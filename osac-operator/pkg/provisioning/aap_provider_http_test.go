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

package provisioning_test

// These specs exercise AAPProvider through a real aap.Client against a
// stateful HTTP double, so launch, poll, and cancel share one fake AAP —
// the seam where launch/poll/cancel asymmetries (e.g. workflow job IDs
// living under workflow_jobs) hide from interface mocks.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// fakeAAPJob is one job record in the fake AAP server. kind records which
// endpoint segment serves it ("jobs" or "workflow_jobs"), mirroring AAP's
// split API.
type fakeAAPJob struct {
	id        int
	kind      string
	status    string
	extraVars string
	artifacts json.RawMessage
}

func (j *fakeAAPJob) terminal() bool {
	switch j.status {
	case "successful", "failed", "error", "canceled":
		return true
	}
	return false
}

// fakeAAP is a stateful AAP API double. Launches create job records,
// GetJob reads them from the endpoint matching their kind (404 from the
// other), cancels transition non-terminal jobs and 405 terminal ones.
type fakeAAP struct {
	mu     sync.Mutex
	server *httptest.Server

	templates    map[string]aap.Template // by name
	templateIDs  map[string]int
	jobs         map[int]*fakeAAPJob
	nextJobID    int
	nextTemplate int

	// rateLimit, when non-empty, makes launch endpoints respond 429 with
	// this Retry-After value.
	rateLimit string
}

func newFakeAAP() *fakeAAP {
	f := &fakeAAP{
		templates:    map[string]aap.Template{},
		templateIDs:  map[string]int{},
		jobs:         map[int]*fakeAAPJob{},
		nextJobID:    100,
		nextTemplate: 1,
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeAAP) close() { f.server.Close() }

func (f *fakeAAP) client() *aap.Client {
	return aap.NewClient(f.server.URL, "token", false)
}

func (f *fakeAAP) addTemplate(name string, templateType aap.TemplateType) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextTemplate
	f.nextTemplate++
	f.templates[name] = aap.Template{ID: id, Name: name, Type: templateType}
	f.templateIDs[name] = id
}

func (f *fakeAAP) setStatus(jobID int, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if job, ok := f.jobs[jobID]; ok {
		job.status = status
	}
}

func (f *fakeAAP) setArtifacts(jobID int, artifacts json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if job, ok := f.jobs[jobID]; ok {
		job.artifacts = artifacts
	}
}

func (f *fakeAAP) purge(jobID int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.jobs, jobID)
}

func (f *fakeAAP) job(jobID int) *fakeAAPJob {
	f.mu.Lock()
	defer f.mu.Unlock()
	if job, ok := f.jobs[jobID]; ok {
		return &fakeAAPJob{
			id: job.id, kind: job.kind, status: job.status,
			extraVars: job.extraVars, artifacts: job.artifacts,
		}
	}
	return nil
}

func (f *fakeAAP) handle(w http.ResponseWriter, r *http.Request) {
	defer GinkgoRecover()
	f.mu.Lock()
	defer f.mu.Unlock()

	path := r.URL.Path

	// Template lookups: GET /v2/{job_templates,workflow_job_templates}/?name=X
	if r.Method == http.MethodGet && strings.HasSuffix(path, "/") &&
		(strings.HasPrefix(path, fmt.Sprintf("/%s/%s/", aap.APIVersion, aap.JobTemplatesEndpoint)) ||
			strings.HasPrefix(path, fmt.Sprintf("/%s/%s/", aap.APIVersion, aap.WorkflowJobTemplatesEndpoint))) {
		wantType := aap.TemplateTypeJob
		if strings.Contains(path, aap.WorkflowJobTemplatesEndpoint) {
			wantType = aap.TemplateTypeWorkflow
		}
		name := r.URL.Query().Get("name")
		if tmpl, ok := f.templates[name]; ok && tmpl.Type == wantType {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count":   1,
				"results": []any{map[string]any{"id": f.templateIDs[name], "name": name}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"count": 0, "results": []any{}})
		return
	}

	// Launches: POST /v2/{job_templates,workflow_job_templates}/<id>/launch/
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/launch/") {
		if f.rateLimit != "" {
			w.Header().Set("Retry-After", f.rateLimit)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		kind := "jobs"
		templateType := aap.TemplateTypeJob
		if strings.Contains(path, aap.WorkflowJobTemplatesEndpoint) {
			kind = "workflow_jobs"
			templateType = aap.TemplateTypeWorkflow
		}
		var templateName string
		for name, tmpl := range f.templates {
			if tmpl.Type == templateType && f.templateIDs[name] > 0 {
				idStr := fmt.Sprintf("%d", f.templateIDs[name])
				if strings.Contains(path, "/"+idStr+"/launch/") {
					templateName = name
					break
				}
			}
		}
		if templateName == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var payload struct {
			ExtraVars map[string]any `json:"extra_vars"`
		}
		Expect(json.NewDecoder(r.Body).Decode(&payload)).To(Succeed())
		extraVars, err := json.Marshal(payload.ExtraVars)
		Expect(err).NotTo(HaveOccurred())
		job := &fakeAAPJob{
			id:        f.nextJobID,
			kind:      kind,
			status:    "pending",
			extraVars: string(extraVars),
		}
		f.jobs[job.id] = job
		f.nextJobID++
		_ = json.NewEncoder(w).Encode(map[string]any{"id": job.id})
		return
	}

	// Job status: GET /v2/{jobs,workflow_jobs}/<id>/
	if r.Method == http.MethodGet {
		if jobID, kind, ok := f.parseJobPath(path); ok {
			job, exists := f.jobs[jobID]
			if !exists || job.kind != kind {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         job.id,
				"status":     job.status,
				"extra_vars": job.extraVars,
				"artifacts":  job.artifacts,
			})
			return
		}
	}

	// Cancel: POST /v2/{jobs,workflow_jobs}/<id>/cancel/
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/cancel/") {
		if jobID, kind, ok := f.parseCancelPath(path); ok {
			job, exists := f.jobs[jobID]
			if !exists || job.kind != kind {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if job.terminal() {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			job.status = "canceled"
			w.WriteHeader(http.StatusAccepted)
			return
		}
	}

	w.WriteHeader(http.StatusNotFound)
}

// parseJobPath extracts the job ID and endpoint kind from
// /v2/{jobs,workflow_jobs}/<id>/.
func (f *fakeAAP) parseJobPath(path string) (int, string, bool) {
	for _, seg := range []string{aap.WorkflowJobsEndpoint, aap.JobsEndpoint} {
		prefix := fmt.Sprintf("/%s/%s/", aap.APIVersion, seg)
		if strings.HasPrefix(path, prefix) && strings.HasSuffix(path, "/") {
			idStr := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/")
			if id, err := strconv.Atoi(idStr); err == nil {
				return id, seg, true
			}
		}
	}
	return 0, "", false
}

// parseCancelPath extracts the job ID and endpoint kind from
// /v2/{jobs,workflow_jobs}/<id>/cancel/.
func (f *fakeAAP) parseCancelPath(path string) (int, string, bool) {
	for _, seg := range []string{aap.WorkflowJobsEndpoint, aap.JobsEndpoint} {
		prefix := fmt.Sprintf("/%s/%s/", aap.APIVersion, seg)
		if strings.HasPrefix(path, prefix) && strings.HasSuffix(path, "/cancel/") {
			idStr := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/cancel/")
			if id, err := strconv.Atoi(idStr); err == nil {
				return id, seg, true
			}
		}
	}
	return 0, "", false
}

var _ = Describe("AAPProvider HTTP flows", func() {
	var (
		fake     *fakeAAP
		ctx      context.Context
		instance *v1alpha1.ComputeInstance
	)

	BeforeEach(func() {
		fake = newFakeAAP()
		ctx = context.Background()
		instance = &v1alpha1.ComputeInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "flow-instance", Namespace: "default"},
		}
	})

	AfterEach(func() { fake.close() })

	It("runs a job-template provision round trip: launch, poll to success, read outputs", func() {
		fake.addTemplate("prov", aap.TemplateTypeJob)
		fake.addTemplate("deprov", aap.TemplateTypeJob)
		provider := provisioning.NewAAPProvider(fake.client(), "prov", "deprov")

		result, err := provider.TriggerProvision(ctx, instance)
		Expect(err).NotTo(HaveOccurred())
		jobID, err := strconv.Atoi(result.JobID)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.job(jobID)).NotTo(BeNil(), "launch must create a job in the fake AAP")
		Expect(fake.job(jobID).kind).To(Equal("jobs"))

		status, err := provider.GetProvisionStatus(ctx, instance, result.JobID)
		Expect(err).NotTo(HaveOccurred())
		Expect(status.State).To(Equal(v1alpha1.JobStatePending))

		fake.setStatus(jobID, "running")
		status, err = provider.GetProvisionStatus(ctx, instance, result.JobID)
		Expect(err).NotTo(HaveOccurred())
		Expect(status.State).To(Equal(v1alpha1.JobStateRunning))

		fake.setStatus(jobID, "successful")
		fake.setArtifacts(jobID, json.RawMessage(`{"l2_vni": 14}`))
		status, err = provider.GetProvisionStatus(ctx, instance, result.JobID)
		Expect(err).NotTo(HaveOccurred())
		Expect(status.State).To(Equal(v1alpha1.JobStateSucceeded))

		withVars, err := provider.GetProvisionStatusWithExtraVars(ctx, instance, result.JobID)
		Expect(err).NotTo(HaveOccurred())
		Expect(withVars.ExtraVars).To(HaveKeyWithValue("l2_vni", float64(14)))
		Expect(withVars.InputExtraVars).To(HaveKey("osac_job_vars"), "launch inputs must round-trip into poll outputs")
	})

	It("runs a workflow-template provision round trip", func() {
		fake.addTemplate("wf-prov", aap.TemplateTypeWorkflow)
		fake.addTemplate("deprov", aap.TemplateTypeJob)
		provider := provisioning.NewAAPProvider(fake.client(), "wf-prov", "deprov")

		result, err := provider.TriggerProvision(ctx, instance)
		Expect(err).NotTo(HaveOccurred())
		jobID, err := strconv.Atoi(result.JobID)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.job(jobID).kind).To(Equal("workflow_jobs"))

		fake.setStatus(jobID, "running")
		status, err := provider.GetProvisionStatus(ctx, instance, result.JobID)
		Expect(err).NotTo(HaveOccurred(), "the workflow job ID a launch returns must be pollable")
		Expect(status.State).To(Equal(v1alpha1.JobStateRunning))
	})

	It("cancels a running workflow provision job before deprovisioning", func() {
		fake.addTemplate("prov", aap.TemplateTypeWorkflow)
		fake.addTemplate("deprov", aap.TemplateTypeJob)
		provider := provisioning.NewAAPProvider(fake.client(), "prov", "deprov")

		launched, err := provider.TriggerProvision(ctx, instance)
		Expect(err).NotTo(HaveOccurred())
		jobID, _ := strconv.Atoi(launched.JobID)
		fake.setStatus(jobID, "running")
		instance.Status.ProvisioningJobs = []v1alpha1.JobStatus{{
			JobID:     launched.JobID,
			Type:      v1alpha1.JobTypeProvision,
			State:     v1alpha1.JobStateRunning,
			Timestamp: metav1.NewTime(time.Now().UTC()),
		}}

		result, err := provider.TriggerDeprovision(ctx, instance, instance.Status.ProvisioningJobs)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Action).To(Equal(provisioning.DeprovisionWaiting))
		Expect(fake.job(jobID).status).To(Equal("canceled"), "the workflow job must be cancelable through its own endpoint")
	})

	It("cancels a running job-template provision job before deprovisioning", func() {
		fake.addTemplate("prov", aap.TemplateTypeJob)
		fake.addTemplate("deprov", aap.TemplateTypeJob)
		provider := provisioning.NewAAPProvider(fake.client(), "prov", "deprov")

		launched, err := provider.TriggerProvision(ctx, instance)
		Expect(err).NotTo(HaveOccurred())
		jobID, _ := strconv.Atoi(launched.JobID)
		fake.setStatus(jobID, "running")
		instance.Status.ProvisioningJobs = []v1alpha1.JobStatus{{
			JobID:     launched.JobID,
			Type:      v1alpha1.JobTypeProvision,
			State:     v1alpha1.JobStateRunning,
			Timestamp: metav1.NewTime(time.Now().UTC()),
		}}

		result, err := provider.TriggerDeprovision(ctx, instance, instance.Status.ProvisioningJobs)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Action).To(Equal(provisioning.DeprovisionWaiting))
		Expect(fake.job(jobID).status).To(Equal("canceled"))
	})

	It("triggers the deprovision job when the provision job is already terminal", func() {
		fake.addTemplate("prov", aap.TemplateTypeJob)
		fake.addTemplate("deprov", aap.TemplateTypeJob)
		provider := provisioning.NewAAPProvider(fake.client(), "prov", "deprov")

		launched, err := provider.TriggerProvision(ctx, instance)
		Expect(err).NotTo(HaveOccurred())
		jobID, _ := strconv.Atoi(launched.JobID)
		fake.setStatus(jobID, "successful")
		instance.Status.ProvisioningJobs = []v1alpha1.JobStatus{{
			JobID:     launched.JobID,
			Type:      v1alpha1.JobTypeProvision,
			State:     v1alpha1.JobStateSucceeded,
			Timestamp: metav1.NewTime(time.Now().UTC()),
		}}

		result, err := provider.TriggerDeprovision(ctx, instance, instance.Status.ProvisioningJobs)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Action).To(Equal(provisioning.DeprovisionTriggered))
		deleteID, err := strconv.Atoi(result.JobID)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.job(deleteID)).NotTo(BeNil(), "deprovision must launch the delete template")
	})

	It("triggers the deprovision job when the provision job was purged", func() {
		fake.addTemplate("prov", aap.TemplateTypeJob)
		fake.addTemplate("deprov", aap.TemplateTypeJob)
		provider := provisioning.NewAAPProvider(fake.client(), "prov", "deprov")

		launched, err := provider.TriggerProvision(ctx, instance)
		Expect(err).NotTo(HaveOccurred())
		jobID, _ := strconv.Atoi(launched.JobID)
		fake.setStatus(jobID, "running")
		fake.purge(jobID)
		instance.Status.ProvisioningJobs = []v1alpha1.JobStatus{{
			JobID:     launched.JobID,
			Type:      v1alpha1.JobTypeProvision,
			State:     v1alpha1.JobStateRunning,
			Timestamp: metav1.NewTime(time.Now().UTC()),
		}}

		result, err := provider.TriggerDeprovision(ctx, instance, instance.Status.ProvisioningJobs)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Action).To(Equal(provisioning.DeprovisionTriggered), "a job missing from both endpoints is treated as terminal")
	})

	It("surfaces API rate limiting as a RateLimitError with the advertised delay", func() {
		fake.addTemplate("prov", aap.TemplateTypeJob)
		fake.addTemplate("deprov", aap.TemplateTypeJob)
		provider := provisioning.NewAAPProvider(fake.client(), "prov", "deprov")
		fake.rateLimit = "17"

		_, err := provider.TriggerProvision(ctx, instance)
		Expect(err).To(HaveOccurred())
		rateLimitErr, ok := provisioning.AsRateLimitError(err)
		Expect(ok).To(BeTrue(), "a rate-limited launch must surface as RateLimitError so the lifecycle can requeue")
		Expect(rateLimitErr.RetryAfter).To(Equal(17 * time.Second))
	})
})
