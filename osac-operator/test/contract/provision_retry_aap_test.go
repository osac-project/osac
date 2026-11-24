// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
)

const provisionFixturePlaybook = "osac-operator/test/contract/testdata/provision_retry/playbook.yml"
const provisionContractPrefix = "OSAC_PROVISION_RETRY_AAP_"

type provisionAAPConfig struct {
	base, token, revision, digest string
	org, project, template        int
}

func provisionConfig(t *testing.T) provisionAAPConfig {
	t.Helper()
	read := func(name string) string {
		value := os.Getenv(provisionContractPrefix + name)
		if value == "" {
			t.Fatalf("missing %s%s", provisionContractPrefix, name)
		}
		return value
	}
	id := func(name string) int {
		value, err := strconv.Atoi(read(name))
		if err != nil || value <= 0 {
			t.Fatalf("%s%s must be a positive ID", provisionContractPrefix, name)
		}
		return value
	}
	cfg := provisionAAPConfig{base: strings.TrimRight(read("URL"), "/"), token: read("TOKEN"),
		org: id("ORG_ID"), project: id("PROJECT_ID"), template: id("TEMPLATE_ID"),
		revision: read("SOURCE_REVISION"), digest: read("EE_DIGEST")}
	if !validProvisionBaseURL(cfg.base) {
		t.Fatal("AAP URL must be an HTTPS API base without userinfo, query or fragment")
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`).MatchString(cfg.revision) {
		t.Fatal("SOURCE_REVISION must be a full Git object ID")
	}
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(cfg.digest) {
		t.Fatal("EE_DIGEST must be sha256 followed by 64 lowercase hex digits")
	}
	if read("CLEANUP") != "1" {
		t.Fatal("CLEANUP=1 is required to authorize removal of this run's jobs and labels")
	}
	return cfg
}

func validProvisionBaseURL(base string) bool {
	u, err := url.Parse(base)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && !strings.ContainsAny(base, "?#")
}

// provisionAAPHTTP uses trusted TLS and never includes response bodies or URLs in errors.
type provisionAAPHTTP struct {
	cfg  provisionAAPConfig
	http *http.Client
}

func (h provisionAAPHTTP) request(ctx context.Context, method, path string, into any) (status int, err error) {
	req, err := http.NewRequestWithContext(ctx, method, h.cfg.base+"/v2/"+path, nil)
	if err != nil {
		return 0, errors.New("construct AAP contract request")
	}
	req.Header.Set("Authorization", "Bearer "+h.cfg.token)
	resp, err := h.http.Do(req)
	if err != nil {
		return 0, errors.New("AAP contract transport failed (details withheld)")
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil && err == nil {
			err = errors.New("close AAP contract response body failed (details withheld)")
		}
	}()
	if resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("AAP contract request returned HTTP %d", resp.StatusCode)
	}
	if into != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(into); err != nil {
			return resp.StatusCode, errors.New("decode AAP contract metadata")
		}
	}
	return resp.StatusCode, nil
}

func (h provisionAAPHTTP) get(ctx context.Context, path string, into any) error {
	status, err := h.request(ctx, http.MethodGet, path, into)
	if err == nil && status == http.StatusNotFound {
		return errors.New("required AAP contract resource missing")
	}
	return err
}

func (h provisionAAPHTTP) preflight(ctx context.Context) error {
	var template struct {
		Project      int
		Playbook     string
		AskVariables bool `json:"ask_variables_on_launch"`
		AskLabels    bool `json:"ask_labels_on_launch"`
		EE           int  `json:"execution_environment"`
	}
	if err := h.get(ctx, fmt.Sprintf("job_templates/%d/", h.cfg.template), &template); err != nil {
		return err
	}
	if template.Project != h.cfg.project || template.Playbook != provisionFixturePlaybook ||
		template.EE <= 0 || !template.AskVariables || !template.AskLabels {
		return errors.New("template must select the fixture project/playbook/EE and accept variables and labels")
	}
	var credentials struct{ Count int }
	if err := h.get(ctx, fmt.Sprintf("job_templates/%d/credentials/", h.cfg.template), &credentials); err != nil {
		return err
	}
	if credentials.Count != 0 {
		return errors.New("fixture template must not have credentials")
	}
	var project struct {
		Revision     string `json:"scm_revision"`
		Organization int
		Branch       string `json:"scm_branch"`
	}
	if err := h.get(ctx, fmt.Sprintf("projects/%d/", h.cfg.project), &project); err != nil {
		return err
	}
	if project.Revision != h.cfg.revision || project.Branch != h.cfg.revision || project.Organization != h.cfg.org {
		return errors.New("fixture project organization or pinned/synced SOURCE_REVISION differs")
	}
	return h.checkEE(ctx, template.EE)
}

func (h provisionAAPHTTP) checkEE(ctx context.Context, id int) error {
	var ee struct{ Image string }
	if err := h.get(ctx, fmt.Sprintf("execution_environments/%d/", id), &ee); err != nil {
		return err
	}
	if !strings.HasSuffix(ee.Image, "@"+h.cfg.digest) {
		return errors.New("fixture EE is not pinned to EE_DIGEST")
	}
	return nil
}

func fixtureVars(job *aap.Job) (map[string]any, error) {
	var envelope struct {
		Vars map[string]any `json:"osac_job_vars"`
	}
	if err := json.Unmarshal([]byte(job.ExtraVars), &envelope); err != nil || envelope.Vars == nil {
		return nil, errors.New("AAP job did not preserve the fixture variable envelope")
	}
	return envelope.Vars, nil
}

func (h provisionAAPHTTP) checkJob(ctx context.Context, job *aap.Job, runID, scenario string, attempt int) error {
	vars, err := fixtureVars(job)
	if err != nil {
		return err
	}
	if vars["fixture_run"] != runID || vars["fixture_case"] != scenario ||
		vars["fixture_attempt"] != float64(attempt) || vars["task"] != "provision" {
		return errors.New("AAP job fixture inputs differ from the engine request")
	}
	if job.ProjectID != h.cfg.project || job.SCMRevision != h.cfg.revision ||
		job.Playbook != provisionFixturePlaybook || job.ExecutionEnvironmentID <= 0 {
		return errors.New("AAP job did not execute the pinned fixture project/playbook/EE")
	}

	var labels struct{ Count int }
	label := networktask.JobLabel("provision", v1alpha1.NetworkJobManagerNone, runID, int32(attempt))
	path := fmt.Sprintf("jobs/%d/labels/?%s", job.ID, url.Values{"name": {label}}.Encode())
	if err := h.get(ctx, path, &labels); err != nil {
		return err
	}
	if labels.Count != 1 {
		return errors.New("AAP ignored the engine attempt label")
	}
	return h.checkEE(ctx, job.ExecutionEnvironmentID)
}

func waitProvisionJob(ctx context.Context, api *aap.Client, id string) (*aap.Job, error) {
	for {
		job, err := api.GetJob(ctx, id)
		if err != nil {
			return nil, errors.New("read real AAP job failed (details withheld)")
		}
		switch job.Status {
		case "successful", "failed", "error", "canceled":
			return job, nil
		}
		select {
		case <-ctx.Done():
			return nil, errors.New("real AAP job exceeded its deadline")
		case <-time.After(time.Second):
		}
	}
}

func sameContractFailure(a, b *v1alpha1.NetworkJobFailure) bool {
	return a != nil && b != nil && a.Reason == b.Reason && a.Message == b.Message &&
		a.Attempt == b.Attempt && a.ObservedAt.Time.Equal(b.ObservedAt.Time)
}

func TestProvisionRetryAAPBoundary(t *testing.T) {
	if os.Getenv(provisionContractPrefix+"CONTRACT") != "1" {
		t.Skip("real AAP contract is opt-in; no live boundary evidence")
	}
	cfg := provisionConfig(t)
	h := provisionAAPHTTP{cfg: cfg, http: &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	ctx, cancel := context.WithTimeout(ctrllog.IntoContext(context.Background(), logr.Discard()), time.Minute)
	defer cancel()
	if err := h.preflight(ctx); err != nil {
		t.Fatal(err)
	}
	scenarios := []string{
		"fail_once", "nonretryable", "malformed_error", "missing_error", "malformed_result", "unexpected_result",
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) { runProvisionScenario(t, h, scenario) })
	}
	t.Logf("real AAP contract completed; fixture revision=%s EE=%s", cfg.revision, cfg.digest)
	t.Log("Kubernetes persistence=fake; Fulfillment/VM=absent")
}

func runProvisionScenario(t *testing.T, h provisionAAPHTTP, scenario string) {
	t.Helper()
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal("generate fixture run ID")
	}
	runID := "contract-" + hex.EncodeToString(random[:])
	api := aap.NewClient(h.cfg.base, h.cfg.token, false)
	knownJobs := map[int]bool{}
	labels := []string{
		networktask.JobLabel("provision", v1alpha1.NetworkJobManagerNone, runID, 1),
		networktask.JobLabel("provision", v1alpha1.NetworkJobManagerNone, runID, 2),
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(ctrllog.IntoContext(context.Background(), logr.Discard()), 2*time.Minute)
		defer cancel()
		if err := h.cleanup(cleanupCtx, api, runID, labels, knownJobs); err != nil {
			t.Errorf("fixture cleanup: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(ctrllog.IntoContext(context.Background(), logr.Discard()), 11*time.Minute)
	defer cancel()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal("register ComputeInstance scheme")
	}
	instance := &v1alpha1.ComputeInstance{
		ObjectMeta: metav1.ObjectMeta{Name: runID, Namespace: "contract", UID: types.UID(runID)},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance).WithStatusSubresource(instance).Build()
	clock := clocktesting.NewFakeClock(time.Now().UTC().Truncate(time.Second))
	engine := networktask.New(kube, kube, api, networktask.Config{Organization: strconv.Itoa(h.cfg.org)}).WithClock(clock)
	req := networktask.Request{
		Task: "provision", Manager: v1alpha1.NetworkJobManagerNone, Kind: "ComputeInstance", APIID: runID,
		Template: strconv.Itoa(h.cfg.template), Accept: networktask.AcceptNoOutput,
		Vars: map[string]any{"fixture_run": runID, "fixture_case": scenario, "fixture_attempt": 1}}
	run := func(runCtx context.Context) networktask.Outcome {
		t.Helper()
		current := &v1alpha1.ComputeInstance{}
		if err := kube.Get(runCtx, client.ObjectKeyFromObject(instance), current); err != nil {
			t.Fatal("read fixture status")
		}
		out, err := engine.Run(runCtx, current, req)
		if id, parseErr := strconv.Atoi(out.Entry.JobID); parseErr == nil && id > 0 {
			knownJobs[id] = true
		}
		if err != nil {
			t.Fatal("real engine Run failed (details withheld)")
		}
		return out
	}
	first, err := launchProvisionAttempt(ctx, clock, run, 1)
	if err != nil {
		t.Fatal(err)
	}
	job := waitProvisionAttempt(t, ctx, h, api, first, runID, scenario, 1)
	wantStatus := "failed"
	wantReason, wantMessage := "InvalidResult", "Internal error"
	if scenario == "fail_once" || scenario == "nonretryable" {
		wantReason, wantMessage = "ProviderReported", "Provisioning failed."
	}
	if scenario == "malformed_result" || scenario == "unexpected_result" {
		wantStatus, wantReason = "successful", "ResultRejected"
	}
	if job.Status != wantStatus {
		t.Fatal("fixture returned an unexpected AAP terminal state")
	}
	failed := run(ctx)
	wantFailure := &v1alpha1.NetworkJobFailure{
		Reason: wantReason, Message: wantMessage, Attempt: 1,
		ObservedAt: metav1.NewTime(clock.Now().UTC().Truncate(time.Second)),
	}
	if !sameContractFailure(failed.LastFailure, wantFailure) {
		t.Fatal("real AAP artifact did not produce the expected safe checkpoint")
	}
	if scenario != "fail_once" {
		if !failed.Failed() || failed.RequeueAfter != 0 {
			t.Fatal("terminal fixture unexpectedly scheduled a retry")
		}
		return
	}
	if !failed.Retrying() || failed.RequeueAfter != 2*time.Minute {
		t.Fatal("retryable fixture changed retry classification/backoff")
	}
	checkpoint := failed.LastFailure.DeepCopy()
	clock.Step(failed.RequeueAfter)
	req.Vars["fixture_attempt"] = 2
	second, err := launchProvisionAttempt(ctx, clock, run, 2)
	if err != nil {
		t.Fatal(err)
	}
	stored := &v1alpha1.ComputeInstance{}
	if err := kube.Get(ctx, client.ObjectKeyFromObject(instance), stored); err != nil {
		t.Fatal("read retry reservation")
	}
	if second.Entry.JobID == first.Entry.JobID || !sameContractFailure(second.LastFailure, checkpoint) ||
		!sameContractFailure(stored.Status.Jobs[0].LastFailure, checkpoint) {
		t.Fatal("running retry did not retain the original failed attempt/time")
	}
	if job := waitProvisionAttempt(t, ctx, h, api, second, runID, scenario, 2); job.Status != "successful" {
		t.Fatal("second fixture attempt did not succeed")
	}
	succeeded := run(ctx)
	if err := kube.Get(ctx, client.ObjectKeyFromObject(instance), stored); err != nil {
		t.Fatal("read successful status")
	}
	if !succeeded.Succeeded() || succeeded.LastFailure != nil || stored.Status.Jobs[0].LastFailure != nil {
		t.Fatal("real successful retry did not clear checkpoint")
	}
}

func (h provisionAAPHTTP) cleanup(
	ctx context.Context, api *aap.Client, runID string, labels []string, knownJobs map[int]bool,
) error {
	// Preserve all recovery labels if any job cannot be discovered or removed.
	// Known job IDs still receive independent ownership checks and cleanup.
	var cleanupErrors []error
	for _, label := range labels {
		jobs, err := api.ListJobsByLabel(ctx, label)
		if err != nil {
			cleanupErrors = append(cleanupErrors, errors.New("list run-owned jobs failed (details withheld)"))
			continue
		}
		for _, job := range jobs {
			knownJobs[job.ID] = true
		}
	}
	for id := range knownJobs {
		if err := h.cleanupJob(ctx, api, runID, id); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if len(cleanupErrors) != 0 {
		return errors.Join(cleanupErrors...)
	}
	for _, label := range labels {
		if err := h.cleanupLabel(ctx, label); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func (h provisionAAPHTTP) cleanupJob(ctx context.Context, api *aap.Client, runID string, id int) error {
	job, err := api.GetJob(ctx, strconv.Itoa(id))
	if err != nil {
		var notFound *aap.NotFoundError
		if errors.As(err, &notFound) {
			return nil
		}
		return errors.New("read cleanup job failed (details withheld)")
	}
	vars, err := fixtureVars(job)
	if err != nil || vars["fixture_run"] != runID ||
		job.ProjectID != h.cfg.project || job.Playbook != provisionFixturePlaybook {
		return errors.New("refusing to clean up a job without matching run ownership")
	}
	switch job.Status {
	case "successful", "failed", "error", "canceled":
	default:
		if err := api.CancelJob(ctx, strconv.Itoa(id)); err != nil {
			var notAllowed *aap.MethodNotAllowedError
			if !errors.As(err, &notAllowed) {
				return errors.New("cancel run-owned job failed (details withheld)")
			}
		}
		if _, err := waitProvisionJob(ctx, api, strconv.Itoa(id)); err != nil {
			return err
		}
	}
	path := fmt.Sprintf("jobs/%d/", id)
	if _, err := h.request(ctx, http.MethodDelete, path, nil); err != nil {
		return err
	}
	for {
		status, err := h.request(ctx, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		if status == http.StatusNotFound {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("job deletion deadline exceeded")
		case <-time.After(time.Second):
		}
	}
}

func (h provisionAAPHTTP) cleanupLabel(ctx context.Context, label string) error {
	var page struct {
		Count   int
		Results []struct {
			ID           int
			Name         string
			Organization int
		}
	}
	query := url.Values{"name": {label}, "organization": {strconv.Itoa(h.cfg.org)}}
	path := "labels/?" + query.Encode()
	if err := h.get(ctx, path, &page); err != nil {
		return err
	}
	if page.Count > 1 {
		return errors.New("refusing ambiguous label cleanup")
	}
	for _, item := range page.Results {
		if item.Name != label || item.Organization != h.cfg.org {
			return errors.New("refusing label cleanup without matching ownership")
		}
		if _, err := h.request(ctx, http.MethodDelete, fmt.Sprintf("labels/%d/", item.ID), nil); err != nil {
			return err
		}
	}
	if err := h.get(ctx, path, &page); err != nil {
		return err
	}
	if page.Count != 0 {
		return errors.New("run label remains after cleanup")
	}
	return nil
}

func launchProvisionAttempt(
	ctx context.Context, clock *clocktesting.FakeClock, run func(context.Context) networktask.Outcome, attempt int,
) (networktask.Outcome, error) {
	deadline, stop := context.WithTimeout(ctx, 5*time.Minute)
	defer stop()
	lastPoll := time.Now()
	out := run(deadline)
	for out.Entry.JobID == "" {
		select {
		case <-deadline.Done():
			return networktask.Outcome{}, fmt.Errorf("AAP launch/label recovery exceeded deadline: %w", deadline.Err())
		case <-time.After(time.Second):
		}
		now := time.Now()
		clock.Step(now.Sub(lastPoll))
		lastPoll = now
		out = run(deadline)
	}
	if deadline.Err() != nil {
		return networktask.Outcome{}, fmt.Errorf("AAP launch/label recovery exceeded deadline: %w", deadline.Err())
	}
	if out.Entry.State != v1alpha1.NetworkJobStateRunning || out.Entry.Attempt != int32(attempt) {
		return networktask.Outcome{}, errors.New("expected persisted Running launch")
	}
	return out, nil
}

func waitProvisionAttempt(
	t *testing.T, ctx context.Context, h provisionAAPHTTP, api *aap.Client,
	out networktask.Outcome, runID, scenario string, attempt int,
) *aap.Job {
	t.Helper()
	deadline, stop := context.WithTimeout(ctx, 5*time.Minute)
	defer stop()
	job, err := waitProvisionJob(deadline, api, out.Entry.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.checkJob(deadline, job, runID, scenario, attempt); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestLaunchProvisionAttemptAdvancesRecoveryClock(t *testing.T) {
	for _, recoverLaunch := range []bool{false, true} {
		t.Run(fmt.Sprintf("recovery=%t", recoverLaunch), func(t *testing.T) {
			// Preserve an explicit retry-backoff advance before launch polling.
			start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).Add(2 * time.Minute)
			clock := clocktesting.NewFakeClock(start)
			calls := 0
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			run := func(runCtx context.Context) networktask.Outcome {
				calls++
				launchDeadline, ok := runCtx.Deadline()
				if !ok || time.Until(launchDeadline) > 5*time.Minute {
					t.Fatal("launch callback did not receive a bounded context")
				}
				entry := v1alpha1.NetworkJobStatus{State: v1alpha1.NetworkJobStateRunning, Attempt: 2}
				if recoverLaunch && calls == 1 {
					return networktask.Outcome{Entry: entry}
				}
				if recoverLaunch && clock.Since(start) < time.Second {
					t.Fatal("launch recovery did not advance the engine clock by elapsed real time")
				}
				entry.JobID = "101"
				return networktask.Outcome{Entry: entry}
			}
			out, err := launchProvisionAttempt(ctx, clock, run, 2)
			if err != nil {
				t.Fatal(err)
			}
			if out.Entry.JobID != "101" {
				t.Fatal("launch recovery lost the job")
			}
			if !recoverLaunch && (!clock.Now().Equal(start) || calls != 1) {
				t.Fatal("immediate launch changed the controlled clock or polled again")
			}
		})
	}
}
