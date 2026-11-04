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

// AAP recorder captures requests without executing provisioning.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var templates = []label{
	{ID: 1, Name: "osac-create-compute-instance"},
	{ID: 2, Name: "osac-delete-compute-instance"},
}

type label struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type job struct {
	ID       int                        `json:"id"`
	Template string                     `json:"template"`
	Status   string                     `json:"status"`
	Payload  map[string]json.RawMessage `json:"payload"`
}

func (j *job) response() map[string]any {
	return map[string]any{
		"id": j.ID, "status": j.Status,
		"extra_vars": string(j.Payload["extra_vars"]), "artifacts": map[string]any{},
	}
}

type recorder struct {
	mu         sync.Mutex
	jobs       []*job
	labels     []label
	unexpected []string
	nextID     int
}

func newRecorder() *recorder {
	return &recorder{jobs: []*job{}, labels: []label{}, unexpected: []string{}, nextID: 100}
}

func (s *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var payload map[string]json.RawMessage
	if r.Method == http.MethodPost {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2*1024*1024))
		payload = map[string]json.RawMessage{}
		if err != nil || (len(body) > 0 && (json.Unmarshal(body, &payload) != nil || payload == nil)) {
			reply(w, http.StatusBadRequest, map[string]string{"detail": "invalid fixture request"})
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	switch r.Method {
	case http.MethodGet:
		switch path {
		case "healthz":
			reply(w, http.StatusOK, map[string]bool{"ready": true})
			return
		case "captures":
			reply(w, http.StatusOK, map[string]any{"jobs": s.jobs, "unexpected": s.unexpected})
			return
		case "api/v2/job_templates":
			results := []label{}
			for _, template := range templates {
				if template.Name == r.URL.Query().Get("name") {
					results = append(results, template)
				}
			}
			reply(w, http.StatusOK, map[string]any{"count": len(results), "results": results})
			return
		case "api/v2/workflow_job_templates":
			reply(w, http.StatusOK, map[string]any{"count": 0, "results": []any{}})
			return
		case "api/v2/organizations":
			reply(w, http.StatusOK, map[string]any{"results": []label{{ID: 1, Name: "osac"}}})
			return
		case "api/v2/labels":
			results := []label{}
			for _, item := range s.labels {
				if item.Name == r.URL.Query().Get("name") {
					results = append(results, item)
				}
			}
			reply(w, http.StatusOK, map[string]any{"results": results})
			return
		case "api/v2/jobs":
			matching := map[int]bool{}
			for _, item := range s.labels {
				if item.Name == r.URL.Query().Get("labels__name") {
					matching[item.ID] = true
				}
			}
			results := []any{}
			for _, j := range s.jobs {
				var ids []int
				_ = json.Unmarshal(j.Payload["labels"], &ids)
				for _, id := range ids {
					if matching[id] {
						results = append(results, j.response())
						break
					}
				}
			}
			reply(w, http.StatusOK, map[string]any{"results": results, "next": nil})
			return
		}
		if len(parts) == 4 && strings.Join(parts[:3], "/") == "api/v2/jobs" {
			if j := s.findJob(parts[3]); j != nil {
				reply(w, http.StatusOK, j.response())
				return
			}
		}
	case http.MethodPost:
		switch path {
		case "reset":
			s.jobs, s.labels, s.unexpected = []*job{}, []label{}, []string{}
			reply(w, http.StatusOK, map[string]any{})
			return
		case "api/v2/labels":
			var name string
			if json.Unmarshal(payload["name"], &name) != nil || name == "" {
				reply(w, http.StatusBadRequest, map[string]string{"detail": "invalid fixture label"})
				return
			}
			item := label{ID: len(s.labels) + 1, Name: name}
			s.labels = append(s.labels, item)
			reply(w, http.StatusCreated, item)
			return
		}
		if len(parts) == 2 && parts[0] == "complete" {
			j := s.findJob(parts[1])
			var state string
			_ = json.Unmarshal(payload["status"], &state)
			if j == nil || (state != "successful" && state != "failed" && state != "canceled") {
				reply(w, http.StatusBadRequest, map[string]string{"detail": "invalid completion"})
				return
			}
			j.Status = state
			reply(w, http.StatusOK, map[string]any{})
			return
		}
		if len(parts) == 5 && strings.Join(parts[:3], "/") == "api/v2/job_templates" && parts[4] == "launch" {
			for _, template := range templates {
				if parts[3] != strconv.Itoa(template.ID) && parts[3] != template.Name {
					continue
				}
				var variables map[string]json.RawMessage
				if json.Unmarshal(payload["extra_vars"], &variables) != nil || variables == nil {
					reply(w, http.StatusBadRequest, map[string]string{"detail": "extra_vars must be an object"})
					return
				}
				s.nextID++
				s.jobs = append(s.jobs, &job{ID: s.nextID, Template: template.Name, Status: "running", Payload: payload})
				reply(w, http.StatusCreated, map[string]int{"id": s.nextID})
				return
			}
		}
		if len(parts) == 5 && strings.Join(parts[:3], "/") == "api/v2/jobs" && parts[4] == "cancel" {
			if j := s.findJob(parts[3]); j != nil {
				j.Status = "canceled"
				reply(w, http.StatusAccepted, map[string]any{})
				return
			}
		}
	}
	s.unexpected = append(s.unexpected, r.Method+" "+r.URL.Path)
	reply(w, http.StatusNotFound, map[string]string{"detail": "unsupported fixture operation"})
}

func (s *recorder) findJob(id string) *job {
	for _, j := range s.jobs {
		if strconv.Itoa(j.ID) == id {
			return j
		}
	}
	return nil
}

func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Response failures must not log AAP payloads, which can contain credentials.
	_ = json.NewEncoder(w).Encode(value)
}

func main() {
	server := &http.Server{Addr: ":8080", Handler: newRecorder(), ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
