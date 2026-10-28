/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package database

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestNewContainerUsesLocalPostgresInOpenShell(t *testing.T) {
	binDir := t.TempDir()
	for _, name := range []string{"initdb", "postgres", "podman"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir)
	t.Setenv("OPENSHELL_SANDBOX", "test")

	container, err := NewContainer(logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(binDir, "postgres"); container.tool != want {
		t.Errorf("OpenShell database tool = %q, want %q", container.tool, want)
	}
}

// The sandbox-local server must not allocate a TCP port: closing a probe
// listener and letting PostgreSQL bind afterwards races with every other test
// package that asks the kernel for an ephemeral port.
func TestStartLocalUsesUnixSocketOnly(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	initdb := filepath.Join(dir, "initdb")
	if err := os.WriteFile(initdb, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	postgres := filepath.Join(dir, "postgres")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\n"
	if err := os.WriteFile(postgres, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	testAdminPassword := "fixture-password"
	container := &Container{
		logger:        logr.Discard(),
		tool:          postgres,
		initdbTool:    initdb,
		local:         true,
		adminPassword: testAdminPassword,
	}
	if err := container.startLocal(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if container.runCmd != nil && container.runCmd.Process != nil {
			_ = container.runCmd.Process.Kill()
			_, _ = container.runCmd.Process.Wait()
		}
		_ = os.RemoveAll(container.dataDir)
	})

	if container.port != "" {
		t.Errorf("local server allocated TCP port %q; it must listen on its socket only", container.port)
	}
	// startLocal returns as soon as the process is started; give the fake
	// server a moment to record its arguments.
	var (
		raw     []byte
		readErr error
	)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		raw, readErr = os.ReadFile(argsFile)
		if readErr == nil {
			break
		}
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	args := string(raw)
	socketDir := filepath.Join(container.dataDir, "data")
	for _, want := range []string{"listen_addresses=", "unix_socket_directories=" + socketDir} {
		if !strings.Contains(args, want) {
			t.Errorf("postgres arguments missing %q:\n%s", want, args)
		}
	}
	for _, unwanted := range []string{"-h\n", "-p\n"} {
		if strings.Contains(args, unwanted) {
			t.Errorf("postgres arguments still configure TCP (%q):\n%s", unwanted, args)
		}
	}
}

func TestStopLocalReportsAnUnexpectedExit(t *testing.T) {
	container := &Container{
		logger:  logr.Discard(),
		tool:    "/bin/sh",
		local:   true,
		dataDir: t.TempDir(),
		runCmd:  exec.Command("/bin/sh", "-c", "trap 'exit 3' INT; while :; do sleep 0.05; done"),
	}
	if err := container.runCmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond) // let the trap install before signalling

	err := container.stopLocal(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("stopLocal() = %v, want an unexpected-exit error", err)
	}
}

func TestLocalDSNUsesUnixSocket(t *testing.T) {
	container := &Container{local: true, dataDir: "/tmp/osac-db"}
	parsed, err := url.Parse(container.dsn("postgres", "secret", "postgres", "&connect_timeout=1"))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "" {
		t.Errorf("local DSN has TCP host %q, want a socket path", parsed.Host)
	}
	if got := parsed.Query().Get("host"); got != "/tmp/osac-db/data" {
		t.Errorf("local DSN socket host = %q, want %q", got, "/tmp/osac-db/data")
	}
	if parsed.Path != "/postgres" || parsed.Query().Get("sslmode") != "disable" {
		t.Errorf("unexpected local DSN %q", parsed)
	}
}

func TestContainerDSNUsesTCP(t *testing.T) {
	container := &Container{host: "127.0.0.1", port: "5433"}
	parsed, err := url.Parse(container.dsn("user", "pw", "db", ""))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "127.0.0.1:5433" {
		t.Errorf("container DSN host = %q, want 127.0.0.1:5433", parsed.Host)
	}
}
