/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package version

import (
	"runtime/debug"
	"strings"
)

// Unknown is the constant value used when version information is not available.
const Unknown = "unknown"

// This will be injected into the binary during the build.
var id = Unknown

func init() {
	if id != Unknown {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				id = setting.Value
			}
		}
	}
}

// Get returns the version identifier.
func Get() string {
	return strings.TrimPrefix(id, "v")
}

// Set sets the version identifier. This is intended for unit tests and there is no reason to call it in other contexts.
func Set(value string) {
	id = value
}
