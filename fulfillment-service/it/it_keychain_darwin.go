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

package it

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const testKeychainPassword = "osac-it-test-keychain" // not a secret — keychain is deleted with the per-test $HOME at cleanup

// Mutable so tests can stub it (see TestProvisionTestKeychain_SubprocessFailures); do not
// add t.Parallel() to tests in this file while relying on that.
var securityBinPath = "/usr/bin/security" // SIP-protected, stable across macOS versions

// Real invocations complete in ~100-250ms combined; this just bounds a hang.
const keychainProvisionTimeout = 5 * time.Second

// keychainEnv returns a minimal env slice scoped to homeDir. A fresh slice (not
// append(os.Environ(), ...)) avoids a duplicate HOME= entry, whose precedence is
// libc-dependent.
func keychainEnv(homeDir string) []string {
	return []string{
		"HOME=" + homeDir,
		"PATH=" + os.Getenv("PATH"),
	}
}

// runSecurity runs securityBinPath with the given args and env, wrapping any failure with
// failMsg and the command's combined output.
func runSecurity(ctx context.Context, env []string, failMsg string, args ...string) error {
	cmd := exec.CommandContext(ctx, securityBinPath, args...)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", failMsg, err, bytes.TrimSpace(out))
	}
	return nil
}

// provisionTestKeychain creates a real, throwaway keychain scoped to homeDir and sets it as
// homeDir's default. Without one, go-keyring's write path (osac login) hits errSecAuthFailed
// and pops a blocking Keychain Access dialog; the read/probe path is unaffected. Auto-lock is
// disabled so a sleeping laptop doesn't relock the keychain mid-suite.
//
// Assumes sequential test execution (ginkgo run defaults to --ginkgo.parallel.total=1);
// enabling --procs requires giving each process its own homeDir.
func provisionTestKeychain(homeDir string) error {
	keychainDir := filepath.Join(homeDir, "Library", "Keychains")
	if err := os.MkdirAll(keychainDir, 0700); err != nil {
		return fmt.Errorf("failed to create keychain directory: %w", err)
	}

	keychainPath := filepath.Join(keychainDir, "login.keychain-db")

	ctx, cancel := context.WithTimeout(context.Background(), keychainProvisionTimeout)
	defer cancel()

	env := keychainEnv(homeDir)

	if err := runSecurity(ctx, env, "failed to create test keychain", "create-keychain", "-p", testKeychainPassword, keychainPath); err != nil {
		return err
	}

	if err := os.Chmod(keychainPath, 0600); err != nil {
		return fmt.Errorf("failed to restrict keychain file permissions: %w", err)
	}

	if err := runSecurity(ctx, env, "failed to disable keychain auto-lock", "set-keychain-settings", keychainPath); err != nil {
		return err
	}

	if err := runSecurity(ctx, env, "failed to set default keychain", "default-keychain", "-s", keychainPath); err != nil {
		return err
	}

	return nil
}
