/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package database

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testContainerTool(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "container-tool")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func testContainer(t *testing.T, tool string) *Container {
	t.Helper()
	container, err := NewContainer().
		SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil))).
		SetTool(tool).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	return container
}

func TestContainerPortDiscoveryDeadline(t *testing.T) {
	tool := testContainerTool(t, "printf x >> \"$2\"\nexec sleep 5\n")
	container := testContainer(t, tool)
	marker := filepath.Join(t.TempDir(), "attempts")
	container.id = marker
	start := time.Now()
	err := container.waitForPort(context.Background(), 350*time.Millisecond, 50*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected port discovery deadline, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("port discovery exceeded its deadline by too much: %s", elapsed)
	}
	attempts, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) < 2 {
		t.Fatalf("expected retry after a blocked port command, got %d attempts", len(attempts))
	}
}

func TestContainerLaunchFailureCleanup(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("FAKE_CONTAINER_STATE", stateDir)
	tool := testContainerTool(t, `case "$1" in
run)
  touch "$FAKE_CONTAINER_STATE/container"
  exit 23
  ;;
port) exit 1 ;;
kill)
  touch "$FAKE_CONTAINER_STATE/killed"
  rm "$FAKE_CONTAINER_STATE/container"
  ;;
esac
`)
	container := testContainer(t, tool)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var configFile string
	t.Run("startup", func(t *testing.T) {
		// Cleanup must already be registered when startup reports an error.
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
			defer cleanupCancel()
			if err := container.Stop(cleanupCtx); err != nil {
				t.Errorf("failed to clean up container: %v", err)
			}
		})
		err := container.Start(ctx)
		if err == nil || !strings.Contains(err.Error(), "database container exited during startup: exit status 23") {
			t.Fatalf("expected asynchronous launch failure, got %v", err)
		}
		configFile = container.configFile
	})
	if _, err := os.Stat(configFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("configuration file should be removed after failed startup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "killed")); err != nil {
		t.Fatalf("cleanup did not kill the container after the run command failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "container")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("container remains after failed startup cleanup: %v", err)
	}
}

func TestContainerPortFailureCleanup(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("FAKE_CONTAINER_STATE", stateDir)
	tool := testContainerTool(t, `case "$1" in
run)
  printf '%s' "$$" > "$FAKE_CONTAINER_STATE/pid"
  exec sleep 5
  ;;
port)
  while [ ! -s "$FAKE_CONTAINER_STATE/pid" ]; do sleep 0.01; done
  exec sleep 5
  ;;
kill)
  printf killed > "$FAKE_CONTAINER_STATE/killed"
  kill "$(cat "$FAKE_CONTAINER_STATE/pid")"
  sleep 3 &
  ;;
esac
`)
	container := testContainer(t, tool)
	var configFile string
	t.Run("startup", func(t *testing.T) {
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cleanupCancel()
			start := time.Now()
			if err := container.Stop(cleanupCtx); err != nil {
				t.Errorf("failed to stop container after port failure: %v", err)
			}
			if elapsed := time.Since(start); elapsed > 2500*time.Millisecond {
				t.Errorf("cleanup exceeded the bounded kill wait: %s", elapsed)
			}
		})
		startCtx, startCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer startCancel()
		if err := container.Start(startCtx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected port discovery deadline, got %v", err)
		}
		configFile = container.configFile
	})
	if _, err := os.Stat(filepath.Join(stateDir, "killed")); err != nil {
		t.Fatalf("cleanup did not kill the running container: %v", err)
	}
	select {
	case <-container.runDone:
	default:
		t.Fatal("foreground run command was not reaped")
	}
	if _, err := os.Stat(configFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("configuration file should be removed after failed startup: %v", err)
	}
}
