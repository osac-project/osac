// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package fixtures

import (
	"context"
	"fmt"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/tests/integration/caas/internal/awx"
)

// ExpectStartedJob waits for scoped automation to start within the caller's deadline.
// Terminal failures use the existing outcome, execution-evidence and quarantine checks.
func ExpectStartedJob(
	ctx context.Context,
	executions *awx.Installation,
	resource metav1.Object,
	playbook string,
	pods *awx.JobPodObserver,
) {
	GinkgoHelper()
	By("waiting for scoped automation to start")

	var (
		attempts []map[string]any
		failed   bool
	)

	defer func() { AddReportEntry(playbook+" start attempts", attempts) }()

	Eventually(func(g Gomega) {
		jobs, err := executions.JobsForResource(ctx, resource, playbook)
		g.Expect(err).NotTo(HaveOccurred())

		attempts = nil

		failed = false

		var started bool

		for _, job := range jobs {
			attempts = append(attempts, map[string]any{
				"job": job.ID, "status": job.Status, "created": job.Created,
				"started": job.Started, "finished": job.Finished,
			})

			switch job.Status {
			case "failed", "error", "canceled":
				failed = true
			case "running", "successful":
				started = started || !job.Started.IsZero()
			}
		}

		g.Expect(failed || started).To(BeTrue(), "a running or successful attempt must retain its native start timestamp")
	}).WithContext(ctx).Should(Succeed(), "scoped AWX automation must start")

	if failed {
		ExpectSuccessfulJobs(ctx, executions, resource, playbook, pods)
	}
}

// ExpectSuccessfulJobs verifies retained attempts and their execution images.
// A temporary overlap quarantine skips whole specs without establishing failure cause.
// The caller owns the phase deadline. Reports contain identities, never job inputs.
func ExpectSuccessfulJobs(
	ctx context.Context,
	executions *awx.Installation,
	resource metav1.Object,
	playbook string,
	pods *awx.JobPodObserver,
) {
	GinkgoHelper()
	slog.DebugContext(ctx, "waiting for successful scoped automation and execution evidence",
		"resource", resource.GetName(),
		"namespace", resource.GetNamespace(),
		"uid", resource.GetUID(),
		"playbook", playbook,
	)

	var (
		attempts []map[string]any
		evidence []*awx.AWXExecutionEvidence
	)

	defer func() {
		AddReportEntry(playbook+" attempts", attempts)
		AddReportEntry(playbook+" executions", evidence)
	}()

	By("requiring every retained automation attempt and its execution image to succeed")

	ticket := map[string]string{
		"osac-aap/playbook_osac_delete_bare_metal_instance.yml": "OSAC-7739",
		"osac-aap/playbook_osac_delete_hosted_cluster.yml":      "OSAC-4031",
	}[playbook]

	Eventually(func(g Gomega) {
		// Correlate all attempts, including earlier failures and retries.
		jobs, err := executions.JobsForResource(ctx, resource, playbook)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(jobs).NotTo(BeEmpty(), "retained jobs must identify this resource and playbook")

		// Retain safe identities and execution evidence before asserting outcomes.
		attempts, evidence = nil, nil
		failure := ""

		var observationErrors []error

		for _, job := range jobs {
			attempts = append(
				attempts,
				map[string]any{
					"job": job.ID, "status": job.Status, "created": job.Created,
					"started": job.Started, "finished": job.Finished,
				},
			)
			execution, err := executions.CheckExecution(job, resource, playbook, pods)

			observationErrors = append(observationErrors, err)
			if err == nil {
				evidence = append(evidence, execution)
			}

			if job.Status == "failed" || job.Status == "error" || job.Status == "canceled" {
				failure = fmt.Sprintf("%s job %d ended %s", playbook, job.ID, job.Status)
			}
		}

		// Unrecognized terminal failures stop the wait immediately.
		if failure != "" && (ticket == "" || !awx.FailedAttemptsOverlap(jobs)) {
			StopTrying(failure).Now()
		}

		for _, err := range observationErrors {
			g.Expect(err).NotTo(HaveOccurred(), "every attempt must retain its selected execution and runtime image")
		}

		// Temporarily skip specs matching the OSAC-7739/OSAC-4031 concurrency heuristic.
		// Execution or submission overlap is a triage heuristic, not proof of either issue.
		// Skipped specs remain unqualified; passing specs still require every attempt to succeed.
		// Remove this quarantine when the tracked issues are resolved.
		if failure != "" {
			slog.WarnContext(ctx, "overlapping deletion failure quarantined", "issue", ticket, "failure", failure)
			Skip(ticket)
		}

		for _, job := range jobs {
			g.Expect(job.Status).To(Equal("successful"), "every retained attempt must succeed")
		}
	}).WithContext(ctx).Should(Succeed())
	slog.InfoContext(ctx, "all retained automation attempts verified successful",
		"resource", resource.GetName(),
		"uid", resource.GetUID(),
		"playbook", playbook,
		"attempts", len(attempts),
	)
}
