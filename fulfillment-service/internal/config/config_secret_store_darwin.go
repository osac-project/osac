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

// keychainProbeTimeout bounds the default-keychain and lock-state checks; real invocations complete in well
// under a second.
const keychainProbeTimeout = 5 * time.Second

// keychainAvailable reports whether a default keychain is configured and unlocked. See the fix commit for why
// this check exists: keyring.Get's own probe can't make either distinction on macOS.
func keychainAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), keychainProbeTimeout)
	defer cancel()

	//nolint:gosec // G204: securityBinPath is a hardcoded, test-stubbable literal, not externally controlled
	out, err := exec.CommandContext(ctx, securityBinPath, "default-keychain").Output()
	if err != nil {
		return false
	}
	path := strings.Trim(strings.TrimSpace(string(out)), `"`)

	// A configured default keychain isn't enough -- if it's locked, keyring.Get/Set risk hanging on an
	// interactive unlock dialog instead of failing cleanly (see the fix commit for the full mechanism).
	// show-keychain-info requires the target keychain to be unlocked to read its settings and fails fast
	// (exit 152, no dialog) when it isn't -- empirically verified against a real locked/unlocked test
	// keychain. A bare `security show-keychain-info` with no argument does NOT resolve to the default
	// keychain (it returns a null placeholder), so the path from the first call is passed explicitly.
	//nolint:gosec // G204: securityBinPath is a hardcoded, test-stubbable literal, not externally controlled
	return exec.CommandContext(ctx, securityBinPath, "show-keychain-info", path).Run() == nil
}
