// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package fixtures

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/osac-operator/pkg/aap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/tests/integration/caas/internal/awx"
	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
)

var _ = Describe("Scoped AWX start observation", func() {
	const playbook = "osac-aap/playbook_osac_create_hosted_cluster.yml"

	var (
		resource *metav1.ObjectMeta
		job      aap.Job
	)

	BeforeEach(func() {
		created := time.Unix(1_700_000_000, 0).UTC()
		resource = &metav1.ObjectMeta{
			Name: "cluster", Namespace: "osac", UID: "original",
			CreationTimestamp: metav1.NewTime(created),
			Annotations:       map[string]string{harness.TenantAnnotation: "tenant"},
		}
		inputs, err := json.Marshal(map[string]any{
			"osac_job_vars": map[string]any{"resource": map[string]any{"metadata": resource}},
			"private_input": "never-report-inputs",
		})
		Expect(err).NotTo(HaveOccurred())

		job = aap.Job{
			ID: 7, Status: "running", Playbook: playbook, ExtraVars: string(inputs),
			Created: created.Add(time.Second), Started: created.Add(2 * time.Second),
		}
	})

	DescribeTable("requires a started native job",
		func(ctx SpecContext, status string, started, wantFailure bool) {
			job.Status = status
			if !started {
				job.Started = time.Time{}
			}

			installation := startObservationInstallation(func() []aap.Job { return []aap.Job{job} })

			budget := time.Second
			if wantFailure {
				budget = 100 * time.Millisecond
			}

			bounded, cancel := context.WithTimeout(ctx, budget)
			defer cancel()

			failure := InterceptGomegaFailure(func() {
				ExpectStartedJob(bounded, installation, resource, playbook, nil)
			})
			Expect(fmt.Sprint(failure)).NotTo(ContainSubstring("never-report-inputs"))

			if wantFailure {
				Expect(failure).To(MatchError(ContainSubstring("scoped AWX automation must start")))
			} else {
				Expect(failure).NotTo(HaveOccurred())
			}

			entries := CurrentSpecReport().ReportEntries
			Expect(entries).To(HaveLen(1))
			Expect(entries[0].Name).To(Equal(playbook + " start attempts"))
			Expect(entries[0].GetRawValue()).To(Equal([]map[string]any{{
				"job": job.ID, "status": job.Status, "created": job.Created,
				"started": job.Started, "finished": job.Finished,
			}}))
		},
		Entry("accepts running automation before provisioning completes", "running", true, false),
		Entry("accepts already successful automation", "successful", true, false),
		Entry("waits for pending automation", "pending", false, true),
		Entry("waits for waiting automation", "waiting", false, true),
		Entry("requires a native start timestamp for running automation", "running", false, true),
		Entry("requires a native start timestamp for successful automation", "successful", false, true),
		Entry("rejects a pending job with a start timestamp", "pending", true, true),
		Entry("rejects a waiting job with a start timestamp", "waiting", true, true),
		Entry("rejects an unknown status with a start timestamp", "unknown", true, true),
	)

	It("waits through pending submission until the job starts", func(ctx SpecContext) {
		var requests atomic.Int32

		installation := startObservationInstallation(func() []aap.Job {
			observed := job
			if requests.Add(1) == 1 {
				observed.Status = "pending"
				observed.Started = time.Time{}
			}

			return []aap.Job{observed}
		})

		bounded, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		ExpectStartedJob(bounded, installation, resource, playbook, nil)
		Expect(requests.Load()).To(Equal(int32(2)))
	})

	It("begins progress while a duplicate submission is still pending", func(ctx SpecContext) {
		pending := job
		pending.ID = 8
		pending.Status = "pending"
		pending.Started = time.Time{}
		installation := startObservationInstallation(func() []aap.Job { return []aap.Job{job, pending} })

		bounded, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()

		ExpectStartedJob(bounded, installation, resource, playbook, nil)
	})

	DescribeTable("delegates terminal failures before considering a running attempt",
		func(ctx SpecContext, status string) {
			failed := job
			failed.Status = status
			failed.Finished = failed.Started.Add(time.Second)
			running := job
			running.ID = 8
			installation := startObservationInstallation(func() []aap.Job { return []aap.Job{failed, running} })

			bounded, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()

			returned := false
			failure := InterceptGomegaFailure(func() {
				ExpectStartedJob(bounded, installation, resource, playbook, nil)

				returned = true
			})
			Expect(failure).To(MatchError(ContainSubstring("job 7 ended " + status)))
			Expect(returned).To(BeFalse(), "terminal qualification must abort the caller")
		},
		Entry("failed", "failed"),
		Entry("error", "error"),
		Entry("canceled", "canceled"),
	)

	It("waits when no retained job identifies the resource", func(ctx SpecContext) {
		installation := startObservationInstallation(func() []aap.Job { return nil })

		bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()

		failure := InterceptGomegaFailure(func() {
			ExpectStartedJob(bounded, installation, resource, playbook, nil)
		})
		Expect(failure).To(MatchError(ContainSubstring("scoped AWX automation must start")))
	})

	It("excludes a started job for another resource incarnation", func(ctx SpecContext) {
		foreign := resource.DeepCopy()
		foreign.UID = "replacement"
		inputs, err := json.Marshal(map[string]any{
			"osac_job_vars": map[string]any{"resource": map[string]any{"metadata": foreign}},
		})
		Expect(err).NotTo(HaveOccurred())

		job.ExtraVars = string(inputs)
		installation := startObservationInstallation(func() []aap.Job { return []aap.Job{job} })

		bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()

		failure := InterceptGomegaFailure(func() {
			ExpectStartedJob(bounded, installation, resource, playbook, nil)
		})
		Expect(failure).To(MatchError(ContainSubstring("scoped AWX automation must start")))
	})

	It("honors caller cancellation", func(ctx SpecContext) {
		installation := startObservationInstallation(func() []aap.Job { return []aap.Job{job} })
		canceled, cancel := context.WithCancel(ctx)
		cancel()

		failure := InterceptGomegaFailure(func() {
			ExpectStartedJob(canceled, installation, resource, playbook, nil)
		})
		Expect(failure).To(MatchError(ContainSubstring("context canceled")))
	})
})

// startObservationInstallation serves retained attempts through the real AAP client.
func startObservationInstallation(jobs func() []aap.Job) *awx.Installation {
	GinkgoHelper()

	// Keep server assertions independent of intercepted caller failures.
	g := NewGomega(Fail)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()

		g.Expect(r.URL.Path).To(Equal("/api/v2/jobs/"))
		g.Expect(json.NewEncoder(w).Encode(map[string]any{"results": jobs(), "next": nil})).To(Succeed())
	}))
	DeferCleanup(server.Close)

	return &awx.Installation{Client: aap.NewClient(server.URL+"/api", "fixture-token", false)}
}
