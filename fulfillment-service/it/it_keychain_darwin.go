//go:build darwin

/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const testKeychainPassword = "osac-it-test-keychain" // not a secret — keychain is deleted with the per-test $HOME at cleanup

// provisionTestKeychain provisions a real macOS keychain scoped to the given homeDir.
//
// The fulfillment-service CLI's credential write path (osac login -> go-keyring ->
// /usr/bin/security) triggers errSecAuthFailed (-60006) and a blocking macOS Keychain
// Access dialog when no valid keychain exists at the expected location. The read/probe
// path, by contrast, returns a clean ErrNotFound (exit 44) with no dialog. This function
// creates a throwaway keychain so the write path succeeds silently during IT runs.
//
// Both subprocess calls (create-keychain and default-keychain) use a fresh minimal env
// slice with HOME=homeDir instead of appending to os.Environ(). This avoids the
// duplicate-HOME-entry risk (SEC-D-010): os.Environ() already contains the real ambient
// HOME, and which entry the security subprocess honors is platform/libc-dependent.
// Mirroring the cliEnv() pattern (it_tool.go) ensures deterministic scoping.
//
// The real osac login path needs no extra HOME scoping here because go-keyring's darwin
// backend inherits env from the parent osac process, which cliEnv() already scopes —
// this file only needs to scope its OWN two subprocess calls.
//
// This function assumes sequential test execution (ginkgo run defaults to
// --ginkgo.parallel.total=1). If future contributors enable --procs, each parallel
// process must get its own homeDir to avoid keychain file contention.
func provisionTestKeychain(homeDir string) error {
	keychainDir := filepath.Join(homeDir, "Library", "Keychains")
	if err := os.MkdirAll(keychainDir, 0700); err != nil {
		return fmt.Errorf("failed to create keychain directory: %w", err)
	}

	keychainPath := filepath.Join(keychainDir, "login.keychain-db")

	env := []string{
		"HOME=" + homeDir,
		"PATH=" + os.Getenv("PATH"),
	}

	create := exec.Command("security", "create-keychain", "-p", testKeychainPassword, keychainPath)
	create.Env = env
	if out, err := create.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create test keychain: %w: %s", err, bytes.TrimSpace(out))
	}

	setDefault := exec.Command("security", "default-keychain", "-s", keychainPath)
	setDefault.Env = env
	if out, err := setDefault.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set default keychain: %w: %s", err, bytes.TrimSpace(out))
	}

	return nil
}
