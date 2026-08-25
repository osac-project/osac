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

// keychainProbePassword is the unlock-keychain probe's password; empty in production (mutable for tests -- see fix
// commit). Known limitation: the empty-password no-op-when-unlocked behavior was verified on a standard,
// non-MDM-managed Mac; MDM-enforced keychain policies are untested and could cause a false "unavailable" (safe --
// just an unnecessary file-store fallback, not a dialog).
var keychainProbePassword = ""

// keychainProbeTimeout bounds the default-keychain and lock-state checks; real invocations finish in well under a second.
const keychainProbeTimeout = 5 * time.Second

// keychainAvailable reports whether a default keychain is configured and unlocked (see fix commit for why keyring.Get alone can't tell).
func keychainAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), keychainProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, securityBinPath, "default-keychain")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	path := strings.Trim(strings.TrimSpace(string(out)), `"`)

	// A locked default keychain must count as unavailable too -- unlock-keychain -p fails fast with no dialog on one, unlike show-keychain-info (see fix commit).
	//nolint:gosec // G204: securityBinPath, keychainProbePassword, and path are hardcoded/test-stubbable/system-derived, not externally controlled
	cmd = exec.CommandContext(ctx, securityBinPath, "unlock-keychain", "-p", keychainProbePassword, path)
	cmd.WaitDelay = time.Second
	return cmd.Run() == nil
}
