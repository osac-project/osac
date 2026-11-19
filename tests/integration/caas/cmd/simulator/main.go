// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"

	testlogging "github.com/osac-project/osac/tests/integration/caas/internal/logging"
	"github.com/osac-project/osac/tests/integration/caas/internal/simulator"
)

// main reads process settings and starts the simulator until a termination signal.
func main() {
	if err := run(); err != nil {
		slog.Error("simulator stopped", "error", err)
		os.Exit(1)
	}
}

// run checks the existing health endpoint or starts the in-cluster simulator process.
func run() error {
	healthcheck := flag.Bool("healthcheck", false, "Check the existing local process health endpoint and exit")

	flag.Parse()

	if *healthcheck {
		return simulator.CheckHealth(context.Background(), "http://127.0.0.1:8081/healthz")
	}

	logger, err := testlogging.NewLogger(os.Stderr)
	if err != nil {
		return err
	}

	testlogging.Configure(logger.With("component", "simulator"))

	connection, err := rest.InClusterConfig()
	if err != nil {
		return err
	}

	// Rely on API Priority and Fairness, matching controller-runtime's client configuration.
	connection.QPS = -1

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	ctx = ctrl.LoggerInto(ctx, ctrl.Log.WithName("simulator"))

	return simulator.Run(ctx, simulator.Config{
		RESTConfig:           connection,
		AgentNamespaces:      strings.Split(os.Getenv("SIMULATED_NAMESPACES"), ","),
		SettingsNamespace:    os.Getenv("POD_NAMESPACE"),
		Metal3Namespace:      os.Getenv("METAL3_NAMESPACE"),
		DefaultIngressDomain: os.Getenv("DEFAULT_INGRESS_DOMAIN"),
	})
}
