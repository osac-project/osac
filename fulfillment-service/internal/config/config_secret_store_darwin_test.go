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

// WARNING: This file contains plain (non-Ginkgo) Go tests, since it must be //go:build darwin-gated and the
// existing Ginkgo suite in this package is unconditional. Do not add t.Parallel() to these tests while they rely
// on mutating the package-level securityBinPath var.

package config

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// setupDefaultKeychain creates a throwaway keychain under a sandboxed HOME and makes it the default, returning its path.
func setupDefaultKeychain(t *testing.T, password string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	keychainDir := filepath.Join(dir, "Library", "Keychains")
	if err := os.MkdirAll(keychainDir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	keychainPath := filepath.Join(keychainDir, "login.keychain-db")
	if out, err := exec.Command(securityBinPath, "create-keychain", "-p", password, keychainPath).CombinedOutput(); err != nil {
		t.Fatalf("create-keychain: %v: %s", err, out)
	}
	if out, err := exec.Command(securityBinPath, "default-keychain", "-s", keychainPath).CombinedOutput(); err != nil {
		t.Fatalf("default-keychain -s: %v: %s", err, out)
	}
	return keychainPath
}

// stubSecurityBinary points securityBinPath at a throwaway shell script for the duration of the test.
func stubSecurityBinary(t *testing.T, script string) {
	t.Helper()
	stubDir := t.TempDir()
	stubPath := filepath.Join(stubDir, "security")
	if err := os.WriteFile(stubPath, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	original := securityBinPath
	securityBinPath = stubPath
	t.Cleanup(func() { securityBinPath = original })
}

func TestKeychainAvailable_NoDefaultKeychain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if keychainAvailable() {
		t.Error("keychainAvailable() = true, want false with no default keychain configured")
	}
}

func TestKeychainAvailable_RealKeychainPresent(t *testing.T) {
	setupDefaultKeychain(t, "test-only-password")

	// This verifies the full keychainAvailable() plumbing (stage-1 default-keychain detection under sandboxed
	// HOME, stage-2 unlock-keychain subprocess execution and exit-code interpretation) using the keychain's real
	// password, since securityd's session-trust no-op shortcut doesn't transfer through HOME overrides in
	// sandboxed test keychains. The production empty-string probe's no-op behavior is verified manually in Task 4
	// against a real, unsandboxed login session.
	originalKeychainProbePassword := keychainProbePassword
	keychainProbePassword = "test-only-password"
	t.Cleanup(func() { keychainProbePassword = originalKeychainProbePassword })

	if !keychainAvailable() {
		t.Error("keychainAvailable() = false, want true with a default keychain configured")
	}
}

func TestKeychainAvailable_SecurityBinaryFails(t *testing.T) {
	stubSecurityBinary(t, "#!/bin/sh\nexit 1\n")

	if keychainAvailable() {
		t.Error("keychainAvailable() = true, want false when the security binary fails")
	}
}

func TestKeychainAvailable_SecurityBinaryHangs(t *testing.T) {
	stubSecurityBinary(t, "#!/bin/sh\nexec sleep 10\n")

	start := time.Now()
	available := keychainAvailable()
	elapsed := time.Since(start)

	if available {
		t.Error("keychainAvailable() = true, want false when the security binary hangs")
	}
	if elapsed >= 7*time.Second {
		t.Errorf("keychainAvailable() took %s, want well under the 10s sleep -- keychainProbeTimeout doesn't seem to be cancelling the subprocess", elapsed)
	}
}

func TestKeychainAvailable_DefaultKeychainLocked(t *testing.T) {
	keychainPath := setupDefaultKeychain(t, "test-only-password")
	// Locking doesn't require the keychain's password -- only unlocking does.
	if out, err := exec.Command(securityBinPath, "lock-keychain", keychainPath).CombinedOutput(); err != nil {
		t.Fatalf("lock-keychain: %v: %s", err, out)
	}

	if keychainAvailable() {
		t.Error("keychainAvailable() = true, want false with a locked default keychain")
	}
}

// TestSettingsSave_FallsBackToFileWithNoKeychain exercises the exact code path the original bug lives in -- a
// real credential save, as login_cmd.go performs post-auth -- without needing a live server or the full IT
// harness. Uses a throwaway discard logger rather than the Ginkgo suite's package-level logger fixture, since
// that fixture only initializes inside TestConfig's BeforeSuite, which this file's own -run-filtered invocations
// never trigger.
func TestSettingsSave_FallsBackToFileWithNoKeychain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	logger := slog.New(slog.DiscardHandler)

	settings, err := NewSettings().
		SetLogger(logger).
		SetDir(dir).
		Build()
	if err != nil {
		t.Fatalf("NewSettings().Build(): %v", err)
	}
	settings.SetAccessToken("test-access-token")
	settings.SetRefreshToken("test-refresh-token")

	ctx := context.Background()
	done := make(chan error, 1)
	go func() { done <- settings.Save(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Settings.Save(): %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Settings.Save() did not return within 10s -- likely blocked on an interactive keychain prompt")
	}

	secretsFile := filepath.Join(dir, "secrets.json")
	if _, err := os.Stat(secretsFile); err != nil {
		t.Fatalf("expected secrets.json to exist via file fallback: %v", err)
	}
}
