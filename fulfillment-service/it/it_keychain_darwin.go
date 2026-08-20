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

// securityBinPath is mutable only so tests in this package can substitute a stub binary to
// exercise provisionTestKeychain's failure branches (see
// TestProvisionTestKeychain_SubprocessFailures). It is package-global state — do not add
// t.Parallel() to any test in this file while relying on the substitution; this file already
// assumes sequential execution elsewhere (see provisionTestKeychain's doc comment).
var securityBinPath = "/usr/bin/security" // SIP-protected, stable across macOS versions

const keychainProvisionTimeout = 30 * time.Second

// keychainEnv returns a minimal env slice scoped to the given homeDir, suitable for
// subprocess calls that must resolve macOS keychain state from that directory.
//
// All three subprocess calls in provisionTestKeychain (create-keychain,
// set-keychain-settings, and default-keychain) use this fresh minimal env slice with
// HOME=homeDir instead of appending to os.Environ(). This avoids the duplicate-HOME-entry
// risk: os.Environ() already contains the real ambient HOME, and which entry the security
// subprocess honors is platform/libc-dependent. Mirroring the cliEnv() pattern
// (it_tool.go) ensures deterministic scoping.
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

// provisionTestKeychain provisions a real macOS keychain scoped to the given homeDir.
//
// macOS resolves both the default-keychain setting and the keychain search list through
// ~/Library/Preferences/com.apple.security.plist, which is scoped per $HOME. Overriding
// HOME is therefore sufficient to fully sandbox both settings — the throwaway keychain
// created here is invisible to the real user keychain and vice versa.
//
// The fulfillment-service CLI's credential write path (osac login -> go-keyring ->
// /usr/bin/security) triggers errSecAuthFailed (-60006) and a blocking macOS Keychain
// Access dialog when no valid keychain exists at the expected location. The read/probe
// path, by contrast, returns a clean ErrNotFound (exit 44) with no dialog. This function
// creates a throwaway keychain so the write path succeeds silently during IT runs.
//
// The real osac login path needs no extra HOME scoping here because go-keyring's darwin
// backend inherits env from the parent osac process, which cliEnv() already scopes —
// this file only needs to scope its OWN three subprocess calls. See keychainEnv for the
// rationale behind that scoping.
//
// After creating the keychain, auto-lock is disabled (no lock-on-sleep, no idle timeout)
// to prevent the keychain from locking mid-run if a developer's laptop sleeps during a
// long-running IT suite, which would otherwise trigger the same auth failures this fix
// prevents.
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
