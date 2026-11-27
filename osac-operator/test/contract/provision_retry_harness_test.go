// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	clocktesting "k8s.io/utils/clock/testing"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
)

func TestProvisionBaseURL(t *testing.T) {
	for _, base := range []string{"https://example.test", "https://example.test/api", "https://example.test/api%3Fpart"} {
		if !validProvisionBaseURL(base) {
			t.Errorf("rejected valid base %q", base)
		}
	}
	for _, base := range []string{
		"https://example.test/api?", "https://example.test/api#", "https://example.test/api?x=1",
		"https://example.test/api#part", "https://user@example.test/api", "http://example.test/api", "://bad",
	} {
		if validProvisionBaseURL(base) {
			t.Errorf("accepted invalid base %q", base)
		}
	}
}

type provisionRoundTripFunc func(*http.Request) (*http.Response, error)

func (f provisionRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type provisionCloseErrorBody struct {
	io.Reader
	closed bool
}

func (b *provisionCloseErrorBody) Close() error {
	b.closed = true
	return errors.New("synthetic-private-close-detail")
}

func TestProvisionResponseCloseError(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       string
	}{
		{"success", `{}`, http.StatusOK, "close AAP contract response body failed"},
		{"decode", `{`, http.StatusOK, "decode AAP contract metadata"},
		{"status", `{}`, http.StatusInternalServerError, "HTTP 500"},
		{"not found", `{}`, http.StatusNotFound, "close AAP contract response body failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &provisionCloseErrorBody{Reader: strings.NewReader(tc.body)}
			transport := provisionRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: body, Header: make(http.Header)}, nil
			})
			h := provisionAAPHTTP{cfg: provisionAAPConfig{base: "https://example.test/api"},
				http: &http.Client{Transport: transport}}
			var metadata map[string]any
			status, err := h.request(context.Background(), http.MethodGet, "jobs/1/", &metadata)
			if status != tc.status || err == nil || !strings.Contains(err.Error(), tc.want) ||
				strings.Contains(err.Error(), "synthetic-private") || !body.closed {
				t.Fatalf("unexpected close/error outcome: status=%d err=%v closed=%t", status, err, body.closed)
			}
		})
	}
}

type provisionCleanupFixture struct {
	t                          *testing.T
	fault                      string
	deletedJobs, deletedLabels map[int]bool
	labelsVisited              bool
}

func (f *provisionCleanupFixture) encode(w http.ResponseWriter, value any) {
	if err := json.NewEncoder(w).Encode(value); err != nil {
		f.t.Errorf("encode fixture response: %v", err)
	}
}

func (f *provisionCleanupFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v2/jobs/":
		if (f.fault == "list" || f.fault == "multiple") && r.URL.Query().Get("labels__name") == "attempt2" {
			http.Error(w, "synthetic-private", http.StatusInternalServerError)
			return
		}
		f.encode(w, map[string]any{"count": 2, "results": []map[string]int{{"id": 101}, {"id": 102}}})
	case strings.HasPrefix(r.URL.Path, "/api/v2/jobs/"):
		f.job(w, r)
	case r.URL.Path == "/api/v2/labels/":
		f.labelsVisited = true
		label := r.URL.Query().Get("name")
		id := 201
		if label == "attempt2" {
			id = 202
		}
		results := []map[string]any{}
		if !f.deletedLabels[id] {
			results = append(results, map[string]any{"id": id, "name": label, "organization": 9})
		}
		f.encode(w, map[string]any{"count": len(results), "results": results})
	case strings.HasPrefix(r.URL.Path, "/api/v2/labels/") && r.Method == http.MethodDelete:
		id, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v2/labels/"), "/"))
		if err != nil {
			f.t.Errorf("invalid label path")
			http.NotFound(w, r)
			return
		}
		if f.fault == "label-delete" && id == 201 {
			http.Error(w, "synthetic-private", http.StatusInternalServerError)
			return
		}
		f.deletedLabels[id] = true
		w.WriteHeader(http.StatusNoContent)
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}
}

func (f *provisionCleanupFixture) job(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v2/jobs/"), "/"))
	if err != nil {
		f.t.Errorf("invalid job path")
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodDelete {
		if (f.fault == "delete" || f.fault == "multiple") && id == 101 {
			http.Error(w, "synthetic-private", http.StatusInternalServerError)
			return
		}
		f.deletedJobs[id] = true
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if f.deletedJobs[id] {
		http.NotFound(w, r)
		return
	}
	if f.fault == "read" && id == 101 {
		http.Error(w, "synthetic-private", http.StatusInternalServerError)
		return
	}
	owner := "contract-test"
	if f.fault == "ownership" && id == 101 {
		owner = "someone-else"
	}
	vars, err := json.Marshal(map[string]any{"osac_job_vars": map[string]any{"fixture_run": owner}})
	if err != nil {
		f.t.Errorf("marshal fixture: %v", err)
		http.Error(w, "fixture", 500)
		return
	}
	f.encode(w, aap.Job{ID: id, Status: "failed", ProjectID: 7,
		Playbook: provisionFixturePlaybook, ExtraVars: string(vars)})
}

func TestProvisionCleanupContinuesAndPreservesRecoveryLabels(t *testing.T) {
	for _, tc := range []struct {
		fault                                              string
		wantErr, job101, labelsVisited, label201, label202 bool
	}{
		{"list", true, true, false, false, false},
		{"read", true, false, false, false, false},
		{"ownership", true, false, false, false, false},
		{"delete", true, false, false, false, false},
		{"label-delete", true, true, true, false, true},
		{"multiple", true, false, false, false, false},
		{"none", false, true, true, true, true},
	} {
		t.Run(tc.fault, func(t *testing.T) {
			fixture := &provisionCleanupFixture{t: t, fault: tc.fault,
				deletedJobs: map[int]bool{}, deletedLabels: map[int]bool{}}
			server := httptest.NewServer(fixture)
			defer server.Close()
			h := provisionAAPHTTP{cfg: provisionAAPConfig{base: server.URL + "/api", project: 7, org: 9}, http: server.Client()}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := h.cleanup(ctx, aap.NewClient(h.cfg.base, "", false), "contract-test",
				[]string{"attempt1", "attempt2"}, map[int]bool{101: true, 102: true})
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected cleanup result: %v", err)
			}
			if tc.fault == "multiple" && (!strings.Contains(err.Error(), "list run-owned jobs failed") ||
				!strings.Contains(err.Error(), "HTTP 500")) {
				t.Fatalf("independent cleanup errors were not collected: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "synthetic-private") {
				t.Fatal("cleanup exposed response detail")
			}
			if !fixture.deletedJobs[102] {
				t.Fatal("independent owned job was not deleted")
			}
			if fixture.deletedJobs[101] != tc.job101 {
				t.Fatal("unexpected deletion of failed or unverified job")
			}
			if fixture.labelsVisited != tc.labelsVisited {
				t.Fatal("unexpected recovery label cleanup")
			}
			if fixture.deletedLabels[201] != tc.label201 || fixture.deletedLabels[202] != tc.label202 {
				t.Fatal("unexpected label deletion outcome")
			}
		})
	}
}

func TestProvisionLaunchRejectsLateResult(t *testing.T) {
	for _, expireDuringCall := range []bool{false, true} {
		t.Run(strconv.FormatBool(expireDuringCall), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			clock := clocktesting.NewFakeClock(time.Now())
			calls := 0
			run := func(callCtx context.Context) networktask.Outcome {
				calls++
				if !expireDuringCall && calls == 1 {
					return networktask.Outcome{}
				}
				cancel()
				if callCtx.Err() == nil {
					t.Error("launch context did not inherit cancellation")
				}
				return networktask.Outcome{Entry: v1alpha1.NetworkJobStatus{
					JobID: "101", State: v1alpha1.NetworkJobStateRunning, Attempt: 1,
				}}
			}
			out, err := launchProvisionAttempt(ctx, clock, run, 1)
			if !errors.Is(err, context.Canceled) || out.Entry.JobID != "" {
				t.Fatalf("accepted late launch: %+v %v", out, err)
			}
		})
	}
}

func TestProvisionLaunchDeadlineBoundsRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	clock := clocktesting.NewFakeClock(time.Now())
	run := func(callCtx context.Context) networktask.Outcome {
		<-callCtx.Done()
		return networktask.Outcome{Entry: v1alpha1.NetworkJobStatus{
			JobID: "101", State: v1alpha1.NetworkJobStateRunning, Attempt: 1,
		}}
	}
	out, err := launchProvisionAttempt(ctx, clock, run, 1)
	if !errors.Is(err, context.DeadlineExceeded) || out.Entry.JobID != "" {
		t.Fatalf("accepted a result returned after the request deadline: %+v %v", out, err)
	}
}
