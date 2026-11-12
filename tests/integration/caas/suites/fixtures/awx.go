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

// ExpectSuccessfulJobs checks every retained attempt and its actual execution image.
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
				map[string]any{"job": job.ID, "status": job.Status, "started": job.Started, "finished": job.Finished},
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

		// Terminal failures stop the wait; pending evidence can still arrive.
		if failure != "" {
			StopTrying(failure).Now()
		}

		for _, job := range jobs {
			g.Expect(job.Status).To(Equal("successful"), "every retained attempt must succeed")
		}

		for _, err := range observationErrors {
			g.Expect(err).NotTo(HaveOccurred(), "every successful attempt must retain its selected execution and runtime image")
		}
	}).WithContext(ctx).Should(Succeed())
	slog.InfoContext(ctx, "all retained automation attempts verified successful",
		"resource", resource.GetName(),
		"uid", resource.GetUID(),
		"playbook", playbook,
		"attempts", len(attempts),
	)
}
