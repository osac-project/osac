//go:build !darwin

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

// provisionTestKeychain is a no-op on non-darwin platforms. macOS-specific keychain
// provisioning is only needed on darwin; on Linux (CI's ubuntu-latest runners) the
// existing keyring probe falls back to file-based storage cleanly.
func provisionTestKeychain(_ string) error {
	return nil
}
