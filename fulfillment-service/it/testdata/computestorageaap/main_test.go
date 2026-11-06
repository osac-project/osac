/*
Copyright (c) 2026 Red Hat Inc.

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

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func request(t *testing.T, server *httptest.Server, method, path, body string, wantStatus int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: status %d, want %d; response %s", method, path, resp.StatusCode, wantStatus, data)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestJobLifecycle(t *testing.T) {
	server := httptest.NewServer(newRecorder())
	t.Cleanup(server.Close)
	for _, template := range []string{"1", "2", "osac-create-compute-instance", "osac-delete-compute-instance"} {
		t.Run(template, func(t *testing.T) {
			payload := `{"extra_vars":{"osac_job_vars":{"resource":{"spec":{"diskVolumeIDs":{"boot":"volume-1"}}},"tenant_storage_classes":[{"tier":"gold","name":"gold-sc"}]}},"labels":[]}`
			launch := request(t, server, http.MethodPost, "/api/v2/job_templates/"+template+"/launch/", payload, http.StatusCreated)
			id := launch["id"]
			jobPath := fmt.Sprintf("/api/v2/jobs/%.0f/", id)
			job := request(t, server, http.MethodGet, jobPath, "", http.StatusOK)
			if job["status"] != "running" {
				t.Fatalf("new job status: %v", job)
			}
			var variables map[string]any
			if err := json.Unmarshal([]byte(job["extra_vars"].(string)), &variables); err != nil {
				t.Fatal(err)
			}
			if variables["osac_job_vars"] == nil || !reflect.DeepEqual(job["artifacts"], map[string]any{}) {
				t.Fatalf("job response lost variables or artifacts: %v", job)
			}
			captures := request(t, server, http.MethodGet, "/captures", "", http.StatusOK)
			jobs := captures["jobs"].([]any)
			captured := jobs[len(jobs)-1].(map[string]any)
			var expected map[string]any
			if err := json.Unmarshal([]byte(payload), &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(captured["payload"], expected) {
				t.Fatalf("captured payload changed: %v", captured)
			}
			name := "osac-create-compute-instance"
			if template == "2" || template == "osac-delete-compute-instance" {
				name = "osac-delete-compute-instance"
			}
			if captured["template"] != name {
				t.Fatalf("captured template: got %v, want %s", captured["template"], name)
			}
			for _, state := range []string{"successful", "failed", "canceled"} {
				request(t, server, http.MethodPost, fmt.Sprintf("/complete/%.0f", id), `{"status":"`+state+`"}`, http.StatusOK)
				job = request(t, server, http.MethodGet, jobPath, "", http.StatusOK)
				if job["status"] != state {
					t.Fatalf("completion status: got %v, want %s", job["status"], state)
				}
			}
		})
	}
}

func TestDiscovery(t *testing.T) {
	server := httptest.NewServer(newRecorder())
	t.Cleanup(server.Close)
	for _, tc := range []struct {
		name string
		id   float64
	}{
		{"osac-create-compute-instance", 1},
		{"osac-delete-compute-instance", 2},
	} {
		result := request(t, server, http.MethodGet, "/api/v2/job_templates/?name="+tc.name, "", http.StatusOK)
		want := []any{map[string]any{"id": tc.id, "name": tc.name}}
		if result["count"] != float64(1) || !reflect.DeepEqual(result["results"], want) {
			t.Fatalf("template lookup: %v", result)
		}
	}
	for _, path := range []string{"/api/v2/job_templates/?name=osac-create-storage", "/api/v2/workflow_job_templates/"} {
		result := request(t, server, http.MethodGet, path, "", http.StatusOK)
		if result["count"] != float64(0) || len(result["results"].([]any)) != 0 {
			t.Fatalf("unsupported template discovered: %v", result)
		}
	}
	if request(t, server, http.MethodGet, "/healthz", "", http.StatusOK)["ready"] != true {
		t.Fatal("recorder is not ready")
	}
	organizations := request(t, server, http.MethodGet, "/api/v2/organizations/", "", http.StatusOK)
	if !reflect.DeepEqual(organizations["results"], []any{map[string]any{"id": float64(1), "name": "osac"}}) {
		t.Fatalf("organization lookup: %v", organizations)
	}
}

func TestLabelsCancellationAndReset(t *testing.T) {
	server := httptest.NewServer(newRecorder())
	t.Cleanup(server.Close)
	label := request(t, server, http.MethodPost, "/api/v2/labels/", `{"name":"attempt-1"}`, http.StatusCreated)
	labels := request(t, server, http.MethodGet, "/api/v2/labels/?name=attempt-1", "", http.StatusOK)
	if !reflect.DeepEqual(labels["results"], []any{label}) {
		t.Fatalf("label lookup: %v", labels)
	}
	launch := request(t, server, http.MethodPost, "/api/v2/job_templates/1/launch/", `{"extra_vars":{},"labels":[1]}`, http.StatusCreated)
	request(t, server, http.MethodPost, "/api/v2/labels/", `{"name":"attempt-1"}`, http.StatusCreated)
	duplicateLabels := request(t, server, http.MethodPost, "/api/v2/job_templates/1/launch/", `{"extra_vars":{},"labels":[1,2]}`, http.StatusCreated)
	request(t, server, http.MethodPost, "/api/v2/job_templates/2/launch/", `{"extra_vars":{},"labels":[]}`, http.StatusCreated)
	jobs := request(t, server, http.MethodGet, "/api/v2/jobs/?labels__name=attempt-1", "", http.StatusOK)
	results := jobs["results"].([]any)
	if len(results) != 2 || results[0].(map[string]any)["id"] != launch["id"] ||
		results[1].(map[string]any)["id"] != duplicateLabels["id"] || jobs["next"] != nil {
		t.Fatalf("attempt lookup: %v", jobs)
	}
	if got := request(t, server, http.MethodGet, "/api/v2/jobs/?labels__name=absent", "", http.StatusOK); len(got["results"].([]any)) != 0 {
		t.Fatalf("unknown attempt matched jobs: %v", got)
	}
	id := launch["id"]
	request(t, server, http.MethodPost, fmt.Sprintf("/api/v2/jobs/%.0f/cancel/", id), "", http.StatusAccepted)
	if job := request(t, server, http.MethodGet, fmt.Sprintf("/api/v2/jobs/%.0f/", id), "", http.StatusOK); job["status"] != "canceled" {
		t.Fatalf("job was not canceled: %v", job)
	}
	request(t, server, http.MethodGet, "/unsupported", "", http.StatusNotFound)
	request(t, server, http.MethodPost, "/reset", "", http.StatusOK)
	captures := request(t, server, http.MethodGet, "/captures", "", http.StatusOK)
	if len(captures["jobs"].([]any)) != 0 || len(captures["unexpected"].([]any)) != 0 {
		t.Fatalf("reset retained captures: %v", captures)
	}
	labels = request(t, server, http.MethodGet, "/api/v2/labels/?name=attempt-1", "", http.StatusOK)
	if len(labels["results"].([]any)) != 0 {
		t.Fatalf("reset retained labels: %v", labels)
	}
	request(t, server, http.MethodGet, fmt.Sprintf("/api/v2/jobs/%.0f/", id), "", http.StatusNotFound)
	next := request(t, server, http.MethodPost, "/api/v2/job_templates/1/launch/", `{"extra_vars":{}}`, http.StatusCreated)
	if next["id"].(float64) <= id.(float64) {
		t.Fatal("reset reused a previous job ID")
	}
}

func TestInvalidRequests(t *testing.T) {
	server := httptest.NewServer(newRecorder())
	t.Cleanup(server.Close)
	for _, tc := range []struct {
		path, body string
	}{
		{"/reset", `[]`},
		{"/reset", `null`},
		{"/reset", `{"broken":`},
		{"/reset", `{}` + `{}`},
		{"/reset", strings.Repeat(" ", 2*1024*1024+1)},
		{"/api/v2/labels/", `{}`},
		{"/api/v2/job_templates/1/launch/", `{}`},
		{"/api/v2/job_templates/1/launch/", `{"extra_vars":"secret-marker"}`},
		{"/api/v2/job_templates/1/launch/", `{"extra_vars":null}`},
		{"/complete/999", `{"status":"successful"}`},
	} {
		result := request(t, server, http.MethodPost, tc.path, tc.body, http.StatusBadRequest)
		if strings.Contains(fmt.Sprint(result), "secret-marker") {
			t.Fatal("invalid request was echoed in the error")
		}
	}
	launch := request(t, server, http.MethodPost, "/api/v2/job_templates/1/launch/", `{"extra_vars":{}}`, http.StatusCreated)
	request(t, server, http.MethodPost, fmt.Sprintf("/complete/%.0f", launch["id"]), `{"status":"running"}`, http.StatusBadRequest)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v2/job_templates/99/launch/"},
		{http.MethodGet, "/api/v2/jobs/999/"},
		{http.MethodPost, "/api/v2/jobs/999/cancel/"},
		{http.MethodDelete, "/api/v2/jobs/101/"},
	} {
		request(t, server, tc.method, tc.path, `{}`, http.StatusNotFound)
	}
	captures := request(t, server, http.MethodGet, "/captures", "", http.StatusOK)
	if len(captures["jobs"].([]any)) != 1 || len(captures["unexpected"].([]any)) != 4 {
		t.Fatalf("unsupported calls were not recorded or created jobs: %v", captures)
	}
}

func TestConcurrentLaunches(t *testing.T) {
	recorder := newRecorder()
	errors := make(chan error, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			response := httptest.NewRecorder()
			recorder.ServeHTTP(response, httptest.NewRequest(http.MethodPost,
				"/api/v2/job_templates/1/launch/", strings.NewReader(`{"extra_vars":{}}`)))
			if response.Code != http.StatusCreated {
				errors <- fmt.Errorf("launch status %d", response.Code)
			}
		})
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	ids := map[int]bool{}
	for _, job := range recorder.jobs {
		ids[job.ID] = true
	}
	if len(recorder.jobs) != 20 || len(ids) != 20 {
		t.Fatalf("concurrent launches lost jobs or reused IDs: %d jobs, %d IDs", len(recorder.jobs), len(ids))
	}
}
