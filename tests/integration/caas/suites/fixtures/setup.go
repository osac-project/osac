// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package fixtures supplies application prerequisites and cross-component assertions.
package fixtures

import (
	"context"
	"log/slog"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	fulfillmentit "github.com/osac-project/osac/fulfillment-service/it"
	"google.golang.org/grpc"

	"github.com/osac-project/osac/tests/integration/caas/internal/awx"
	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
	testlogging "github.com/osac-project/osac/tests/integration/caas/internal/logging"
)

// ConfigureLogging initializes the redacting suite logger before opening clients.
func ConfigureLogging(suite string) {
	GinkgoHelper()

	level, err := testlogging.Level()
	Expect(err).NotTo(HaveOccurred())
	logger, err := fulfillmentit.NewLogger(GinkgoWriter, level)
	Expect(err).NotTo(HaveOccurred())
	testlogging.Configure(logger.With("suite", suite))
}

// OpenKubernetes opens the dedicated installation for this worker.
func OpenKubernetes() *harness.Environment {
	GinkgoHelper()

	environment, err := harness.Open(os.Getenv("NS"))
	Expect(err).NotTo(HaveOccurred())

	return environment
}

// NewFulfillmentTool validates the dedicated installation inputs and builds its shared API tool.
// Callers register cleanup before preparing or connecting the tool.
func NewFulfillmentTool(environment *harness.Environment) *fulfillmentit.Tool {
	GinkgoHelper()

	Expect(environment.Namespace).To(Equal("osac"), "Fulfillment IT uses the osac namespace")
	Expect(os.Getenv("KUBECONFIG")).NotTo(BeEmpty())
	projectDir := os.Getenv("FULFILLMENT_PROJECT_DIR")
	Expect(projectDir).NotTo(BeEmpty(), "run through the CaaS Makefile to select the Fulfillment project")

	tool, err := fulfillmentit.NewTool().
		SetLogger(slog.Default().With("component", "fulfillment-tool")).
		SetProjectDir(projectDir).
		Build()
	Expect(err).NotTo(HaveOccurred())

	return tool
}

// OpenFulfillment connects to prepared APIs and keeps clients open through suite cleanup.
// Process 1 exports shared cluster logs after the final audit.
func OpenFulfillment(ctx context.Context, environment *harness.Environment) *fulfillmentit.Tool {
	GinkgoHelper()

	tool := NewFulfillmentTool(environment)
	if GinkgoParallelProcess() == 1 {
		DeferCleanup(tool.Cleanup, NodeTimeout(2*time.Minute))
	} else {
		DeferCleanup(tool.Close, NodeTimeout(2*time.Minute))
	}

	started := time.Now()

	slog.InfoContext(ctx, "connecting Fulfillment clients")

	err := tool.Connect(ctx)

	slog.InfoContext(ctx, "Fulfillment clients connected", "duration", time.Since(started), "failed", err != nil)
	Expect(err).NotTo(HaveOccurred())

	return tool
}

// OpenAWX reports the verified automation selections required by the current suite.
func OpenAWX(ctx context.Context, environment *harness.Environment) *awx.Installation {
	GinkgoHelper()

	executions, err := awx.Open(ctx, environment)
	Expect(err).NotTo(HaveOccurred())
	AddReportEntry("Selected AWX inputs", executions)

	return executions
}

// RequireClean stops application reuse when resources or active automation remain.
func RequireClean(
	ctx context.Context,
	environment *harness.Environment,
	inventory harness.Inventory,
	executions *awx.Installation,
	connection grpc.ClientConnInterface,
	tenantID string,
) {
	GinkgoHelper()
	slog.DebugContext(ctx, "checking installation before reuse", "namespace", environment.Namespace)

	if err := CheckCleanEnvironment(ctx, environment, inventory, executions, connection, tenantID); err != nil {
		AbortSuite("test environment is not clean; stopping reuse: " + err.Error())
	}
}
