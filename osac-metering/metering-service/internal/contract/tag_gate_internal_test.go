/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package contract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTagGateAllowsANewVersionAndRejectsAnEditedOne proves the release gate does not deadlock:
// after a tag ships v1 and the manifest, a pull request may add v2 next to its manifest entry
// (the manifest lives outside the tagged directory), but an edit to v1 still fails.
func TestTagGateAllowsANewVersionAndRejectsAnEditedOne(t *testing.T) {
	for _, tool := range []string{"git", "bash"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	root := repositoryRoot(t)
	checkout := t.TempDir()

	copyFile(t,
		filepath.Join(root, "osac-metering/scripts/test-contract-schemas.sh"),
		filepath.Join(checkout, "osac-metering/scripts/test-contract-schemas.sh"),
	)
	versionsDirectory := filepath.Join(checkout, "osac-metering/schema/versions")
	v1Path := filepath.Join(versionsDirectory, "v1.json")
	manifestPath := filepath.Join(checkout, "osac-metering/schema/published.sha256")
	copyFile(t, filepath.Join(root, "osac-metering/schema/versions/v1.json"), v1Path)
	copyFile(t, filepath.Join(root, "osac-metering/schema/published.sha256"), manifestPath)

	runCommand(t, checkout, "git", "init", "--quiet")
	runCommand(t, checkout, "git", "config", "user.email", "test@example.com")
	runCommand(t, checkout, "git", "config", "user.name", "Test")
	runCommand(t, checkout, "git", "add", "-A")
	runCommand(t, checkout, "git", "commit", "--quiet", "-m", "release")
	runCommand(t, checkout, "git", "tag", "osac-metering/v1.0.0")

	script := filepath.Join(checkout, "osac-metering/scripts/test-contract-schemas.sh")
	if output, err := runScript(script); err != nil {
		t.Fatalf("the released tree must pass the gate: %v\n%s", err, output)
	}

	// A new version file and its manifest entry are the only way a field changes. Before the
	// manifest moved out of the tagged directory, this step failed on the manifest change.
	if err := os.WriteFile(filepath.Join(versionsDirectory, "v2.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("writing v2.json: %v", err)
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("reading the manifest: %v", err)
	}
	manifest = append(manifest, []byte("0000000000000000000000000000000000000000000000000000000000000000  v2.json\n")...)
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatalf("writing the manifest: %v", err)
	}
	if output, err := runScript(script); err != nil {
		t.Fatalf("a new version with its manifest entry must pass: %v\n%s", err, output)
	}

	// Editing the released file must still fail, whatever the manifest says.
	v1, err := os.ReadFile(v1Path)
	if err != nil {
		t.Fatalf("reading v1.json: %v", err)
	}
	if err := os.WriteFile(v1Path, append(v1, '\n'), 0o644); err != nil {
		t.Fatalf("editing v1.json: %v", err)
	}
	output, err := runScript(script)
	if err == nil {
		t.Fatalf("editing a released schema file must fail:\n%s", output)
	}
	if !strings.Contains(output, "changed, was renamed, or was deleted") {
		t.Fatalf("the gate did not name the edit:\n%s", output)
	}
}

// copyFile copies one file, creating its parent directory.
func copyFile(t *testing.T, from, to string) {
	t.Helper()
	content, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("reading %s: %v", from, err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(to), err)
	}
	if err := os.WriteFile(to, content, 0o644); err != nil {
		t.Fatalf("writing %s: %v", to, err)
	}
}

// runCommand runs a command in a directory and fails the test when it fails.
func runCommand(t *testing.T, directory string, name string, args ...string) string {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, output)
	}
	return string(output)
}

// runScript runs the release gate and returns its combined output.
func runScript(script string) (string, error) {
	command := exec.Command("bash", script)
	output, err := command.CombinedOutput()
	return string(output), err
}
