// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

// Package logging configures process logging without application or test-framework dependencies.
package logging

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

var levelName = flag.String("log-level", defaultLevel(), "Log level: debug, info, warn or error")

// defaultLevel reads the Make setting, using info for direct binary invocations.
func defaultLevel() string {
	if value := os.Getenv("LOG_LEVEL"); value != "" {
		return value
	}

	return "info"
}

// Level validates the selected level before clients or controllers start.
func Level() (slog.Level, error) {
	switch *levelName {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q: use debug, info, warn or error", *levelName)
	}
}

// NewLogger creates the native JSON logger at the selected process level.
func NewLogger(writer io.Writer) (*slog.Logger, error) {
	level, err := Level()
	if err != nil {
		return nil, err
	}

	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level})), nil
}

// Configure connects standard Go, controller-runtime and Kubernetes logs once at process startup.
func Configure(logger *slog.Logger) {
	slog.SetDefault(logger)
	adapter := logr.FromSlogHandler(logger.Handler())
	crlog.SetLogger(adapter)
	// klog filters verbosity before forwarding to the structured handler.
	verbosity := "0"
	if logger.Enabled(context.Background(), slog.LevelDebug) {
		verbosity = "4"
	}

	var level klog.Level

	if err := level.Set(verbosity); err != nil {
		logger.Error("configure Kubernetes log verbosity", "error", err)
	}

	klog.SetLoggerWithOptions(adapter, klog.ContextualLogger(true))
	slog.Info("logging configured", "logLevel", *levelName)
}
