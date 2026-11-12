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

// run loads the in-cluster connection and passes settings to the simulator process.
func run() error {
	flag.Parse()

	logger, err := testlogging.NewLogger(os.Stderr)
	if err != nil {
		return err
	}

	testlogging.Configure(logger.With("component", "simulator"))

	connection, err := rest.InClusterConfig()
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	ctx = ctrl.LoggerInto(ctx, ctrl.Log.WithName("simulator"))

	return simulator.Run(ctx, simulator.Config{
		RESTConfig:        connection,
		AgentNamespaces:   strings.Split(os.Getenv("SIMULATED_NAMESPACES"), ","),
		SettingsNamespace: os.Getenv("POD_NAMESPACE"),
		Metal3Namespace:   os.Getenv("METAL3_NAMESPACE"),
	})
}
