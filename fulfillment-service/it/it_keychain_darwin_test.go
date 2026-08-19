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
//	go test -run TestProvisionTestKeychain ./it/...
//
// A bare `go test ./it/...` or `ginkgo run it` will also sweep in the full TestIntegration
// Ginkgo suite, which brings up a kind cluster and takes ~15-17 minutes.
// Do NOT add Ginkgo Describe/It blocks here — they register globally at init time and would
// run under the full suite regardless of the -run filter, defeating the isolation.

package it

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvisionTestKeychain_CreatesResolvableDefaultKeychain(t *testing.T) {
	dir := t.TempDir()

	if err := provisionTestKeychain(dir); err != nil {
		t.Fatalf("provisionTestKeychain: %v", err)
	}

	cmd := exec.Command(securityBinPath, "default-keychain")
	cmd.Env = keychainEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("security default-keychain: %v: %s", err, out)
	}

	got := strings.TrimSpace(string(out))
	got = strings.Trim(got, "\"")

	// macOS resolves /var -> /private/var via symlink; normalize both sides.
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", dir, err)
	}
	want := resolvedDir + "/Library/Keychains/login.keychain-db"
	if got != want {
		t.Errorf("default keychain = %q, want %q", got, want)
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
	keychainPath := dir + "/Library/Keychains/login.keychain-db"

	addCmd := exec.Command(securityBinPath, "add-generic-password",
		"-a", "osac-test-account",
		"-s", "osac-test-service",
		"-w", "test-secret-value",
		keychainPath,
	)
	addCmd.Env = env
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("security add-generic-password: %v: %s", err, out)
	}

	findCmd := exec.Command(securityBinPath, "find-generic-password",
		"-a", "osac-test-account",
		"-s", "osac-test-service",
		"-w",
		keychainPath,
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
