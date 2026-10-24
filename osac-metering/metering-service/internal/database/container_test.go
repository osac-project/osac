/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package database

import (
	"os"
	"path/filepath"
	"testing"

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
