//go:build darwin

/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// WARNING: This file contains plain (non-Ginkgo) Go tests — the first in the it package.
// Run ONLY with:
//
//	go test -run 'TestProvisionTestKeychain|TestNewCLIHomeDir' ./it/...
//
// A bare `go test ./it/...` or `ginkgo run it` will also sweep in the full TestIntegration
// Ginkgo suite, which brings up a kind cluster and takes ~15-17 minutes.
// Do NOT add Ginkgo Describe/It blocks here — they register globally at init time and would
// run under the full suite regardless of the -run filter, defeating the isolation.

package it

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// currentDefaultKeychainPath runs `security default-keychain` scoped to dir's HOME and
// returns the unquoted path it reports.
func currentDefaultKeychainPath(t *testing.T, dir string) string {
	t.Helper()

	cmd := exec.Command(securityBinPath, "default-keychain")
	cmd.Env = keychainEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("security default-keychain: %v: %s", err, out)
	}
	return strings.Trim(strings.TrimSpace(string(out)), "\"")
}

// expectedKeychainPath returns the path provisionTestKeychain is expected to have set as
// dir's default keychain.
func expectedKeychainPath(t *testing.T, dir string) string {
	t.Helper()

	// macOS resolves /var -> /private/var via symlink; normalize both sides.
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", dir, err)
	}
	return filepath.Join(resolvedDir, "Library", "Keychains", "login.keychain-db")
}

func TestProvisionTestKeychain_CreatesResolvableDefaultKeychain(t *testing.T) {
	dir := t.TempDir()

	if err := provisionTestKeychain(dir); err != nil {
		t.Fatalf("provisionTestKeychain: %v", err)
	}

	got, want := currentDefaultKeychainPath(t, dir), expectedKeychainPath(t, dir)
	if got != want {
		t.Errorf("default keychain = %q, want %q", got, want)
	}

	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("os.Stat(%q): %v", want, err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("keychain file mode = %o, want %o", perm, 0600)
	}

	// show-keychain-info's text output is undocumented and could shift across macOS versions.
	infoOut, err := exec.Command(securityBinPath, "show-keychain-info", want).CombinedOutput()
	if err != nil {
		t.Fatalf("security show-keychain-info: %v: %s", err, infoOut)
	}
	if strings.Contains(string(infoOut), "lock-on-sleep") {
		t.Errorf("keychain settings report lock-on-sleep enabled: %s", infoOut)
	}
	if strings.Contains(string(infoOut), "timeout=") {
		t.Errorf("keychain settings report a lock timeout: %s", infoOut)
	}
}

func TestProvisionTestKeychain_DoesNotAffectRealDefaultKeychain(t *testing.T) {
	beforeDefault, err := exec.Command(securityBinPath, "default-keychain").CombinedOutput()
	if err != nil {
		t.Fatalf("pre-check security default-keychain: %v: %s", err, beforeDefault)
	}

	beforeList, err := exec.Command(securityBinPath, "list-keychains", "-d", "user").CombinedOutput()
	if err != nil {
		t.Fatalf("pre-check security list-keychains: %v: %s", err, beforeList)
	}

	dir := t.TempDir()
	if err := provisionTestKeychain(dir); err != nil {
		t.Fatalf("provisionTestKeychain: %v", err)
	}

	afterDefault, err := exec.Command(securityBinPath, "default-keychain").CombinedOutput()
	if err != nil {
		t.Fatalf("post-check security default-keychain: %v: %s", err, afterDefault)
	}

	afterList, err := exec.Command(securityBinPath, "list-keychains", "-d", "user").CombinedOutput()
	if err != nil {
		t.Fatalf("post-check security list-keychains: %v: %s", err, afterList)
	}

	if string(afterDefault) != string(beforeDefault) {
		t.Errorf("default keychain changed:\n  before: %s  after:  %s", beforeDefault, afterDefault)
	}
	if string(afterList) != string(beforeList) {
		t.Errorf("keychain search list changed:\n  before: %s  after:  %s", beforeList, afterList)
	}
}

func TestProvisionTestKeychain_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	if err := provisionTestKeychain(dir); err != nil {
		t.Fatalf("provisionTestKeychain: %v", err)
	}

	env := keychainEnv(dir)

	// Matches go-keyring's Set() flags (-U, no keychain path) though not its stdin-piped
	// dispatch — both reach the same underlying write API this test exercises.
	addCmd := exec.Command(securityBinPath, "add-generic-password",
		"-U",
		"-a", "osac-test-account",
		"-s", "osac-test-service",
		"-w", "test-secret-value",
	)
	addCmd.Env = env
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("security add-generic-password: %v: %s", err, out)
	}

	findCmd := exec.Command(securityBinPath, "find-generic-password",
		"-a", "osac-test-account",
		"-s", "osac-test-service",
		"-w",
	)
	findCmd.Env = env
	out, err := findCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("security find-generic-password: %v: %s", err, out)
	}

	got := strings.TrimSpace(string(out))
	if got != "test-secret-value" {
		t.Errorf("round-trip value = %q, want %q", got, "test-secret-value")
	}
}

func TestProvisionTestKeychain_SubprocessFailures(t *testing.T) {
	tests := []struct {
		name       string
		failOn     string
		wantErrMsg string
	}{
		{"CreateKeychain", "create-keychain", "failed to create test keychain"},
		{"DisableAutoLock", "set-keychain-settings", "failed to disable keychain auto-lock"},
		{"SetDefaultKeychain", "default-keychain", "failed to set default keychain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubDir := t.TempDir()
			stubPath := filepath.Join(stubDir, "security")
			// Fails only on the targeted subcommand; touches the keychain file on a
			// non-failing create-keychain so os.Chmod doesn't fail on a missing file.
			script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = %q ]; then
	exit 1
fi
if [ "$1" = "create-keychain" ]; then
	for arg in "$@"; do :; done
	touch "$arg"
fi
exit 0
`, tt.failOn)
			if err := os.WriteFile(stubPath, []byte(script), 0755); err != nil {
				t.Fatalf("os.WriteFile(%q): %v", stubPath, err)
			}

			original := securityBinPath
			securityBinPath = stubPath
			t.Cleanup(func() { securityBinPath = original })

			dir := t.TempDir()
			err := provisionTestKeychain(dir)
			if err == nil {
				t.Fatal("provisionTestKeychain: expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErrMsg) {
				t.Errorf("provisionTestKeychain error = %q, want substring %q", err.Error(), tt.wantErrMsg)
			}
		})
	}
}

func TestNewCLIHomeDir_ProvisionsResolvableKeychain(t *testing.T) {
	tool := &Tool{}

	dir, err := tool.NewCLIHomeDir()
	if err != nil {
		t.Fatalf("NewCLIHomeDir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("returned dir does not exist: %v", err)
	}

	got, want := currentDefaultKeychainPath(t, dir), expectedKeychainPath(t, dir)
	if got != want {
		t.Errorf("default keychain = %q, want %q", got, want)
	}
}

func TestNewCLIHomeDir_ProvisioningFailureCleansUp(t *testing.T) {
	stubDir := t.TempDir()
	stubPath := filepath.Join(stubDir, "security")
	if err := os.WriteFile(stubPath, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatalf("os.WriteFile(%q): %v", stubPath, err)
	}

	original := securityBinPath
	securityBinPath = stubPath
	t.Cleanup(func() { securityBinPath = original })

	before, err := filepath.Glob(filepath.Join(os.TempDir(), "*.cli-home"))
	if err != nil {
		t.Fatalf("filepath.Glob (before): %v", err)
	}

	tool := &Tool{}
	dir, err := tool.NewCLIHomeDir()
	if err == nil {
		t.Fatalf("NewCLIHomeDir: expected error, got nil (dir=%q)", dir)
	}
	if !strings.Contains(err.Error(), "failed to provision test keychain") {
		t.Errorf("NewCLIHomeDir error = %q, want substring %q", err.Error(), "failed to provision test keychain")
	}
	if dir != "" {
		t.Errorf("NewCLIHomeDir dir = %q, want empty string on error", dir)
	}

	after, err := filepath.Glob(filepath.Join(os.TempDir(), "*.cli-home"))
	if err != nil {
		t.Fatalf("filepath.Glob (after): %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("leaked *.cli-home temp dir(s): before=%d after=%d", len(before), len(after))
	}
}
