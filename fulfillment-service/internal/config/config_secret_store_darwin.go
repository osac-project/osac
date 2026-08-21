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

package config

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// securityBinPath is the path to the macOS security(1) CLI. Mutable so tests can stub it.
var securityBinPath = "/usr/bin/security" // SIP-protected, stable across macOS versions

// keychainProbePassword is the password used for the unlock-keychain probe. Empty in production, relying on
// securityd's already-unlocked no-op shortcut; mutable because that shortcut doesn't survive a sandboxed HOME
// override, so tests against a real unlocked keychain must supply its actual password (see the fix commit).
var keychainProbePassword = ""

// keychainProbeTimeout bounds the default-keychain and lock-state checks; real invocations complete in well
// under a second.
const keychainProbeTimeout = 5 * time.Second

// keychainAvailable reports whether a default keychain is configured and unlocked. See the fix commit for why
// this check exists: keyring.Get's own probe can't make either distinction on macOS.
func keychainAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), keychainProbeTimeout)
	defer cancel()

	//nolint:gosec // G204: securityBinPath is a hardcoded, test-stubbable literal, not externally controlled
	cmd := exec.CommandContext(ctx, securityBinPath, "default-keychain")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	path := strings.Trim(strings.TrimSpace(string(out)), `"`)

	// A locked default keychain must also count as unavailable: unlock-keychain -p is a no-op on an
	// already-unlocked keychain (exit 0) but fails fast with zero dialog on a locked one (exit 51) -- unlike
	// show-keychain-info, which has to consult SecurityAgent and pop a real dialog once run on an interactive
	// session, and was rejected for that reason (see the fix commit for the full empirical trail).
	//nolint:gosec // G204: securityBinPath, keychainProbePassword, and path are hardcoded/test-stubbable/system-derived, not externally controlled
	cmd = exec.CommandContext(ctx, securityBinPath, "unlock-keychain", "-p", keychainProbePassword, path)
	cmd.WaitDelay = time.Second
	return cmd.Run() == nil
}
