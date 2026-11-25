// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package prepare seeds persistent application fixtures without running cluster scenarios.
package prepare

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/tests/integration/caas/internal/awx"
	"github.com/osac-project/osac/tests/integration/caas/suites/fixtures"
)

// TestPrepare runs persistent application setup against the deployed installation.
func TestPrepare(t *testing.T) {
	RegisterFailHandler(Fail)
	fixtures.ConfigureLogging("prepare")
	SetDefaultEventuallyPollingInterval(time.Second)
	RunSpecs(t, "CaaS preparation")
}

var _ = Describe("Persistent application fixtures", func() {
	It("prepares identities, tenants and hub", func(ctx SpecContext) {
		environment := fixtures.OpenKubernetes()
		pods, err := awx.ObserveJobPods(ctx, environment)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(pods.Close)

		executions := fixtures.OpenAWX(ctx, environment)
		fixtures.ExpectServiceControllers(ctx, environment, executions)
		class := fixtures.ExpectNetworkClassReady(ctx, environment, executions)

		tool := fixtures.NewFulfillmentTool(environment)
		DeferCleanup(tool.Close, NodeTimeout(2*time.Minute))

		By("preparing shared identities and application prerequisites")
		Expect(tool.Setup(ctx)).To(Succeed())
		fixtures.EnsureProfileExternalIPPool(ctx, environment, tool, executions, pods)

		By("requiring the user tenant and its default networking to be ready")

		tenantID := fixtures.Tenant(ctx, tool)
		fixtures.ExpectTenantNetworkJobs(ctx, environment, executions, pods, tenantID, class)

		if executions.NetworkingEnabled && executions.ExpectedK8s == "cudn_evpn" {
			fixtures.ExpectCUDNManagerReady(ctx, environment)
		}
	}, SpecTimeout(20*time.Minute))
})
