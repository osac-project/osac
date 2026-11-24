/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package utils

import (
	"strings"
)

// SanitizeDNS1123Name converts input into a DNS-1123 name base: lowercased, every rune
// outside [a-z0-9-] replaced with a dash, and dashes trimmed from both ends. A positive
// maxBase truncates a longer base to that many bytes, trimming the dashes the cut can
// leave behind. A non-nil suffix receives the original input, and its result is appended
// to the base, so two inputs that sanitize to the same base still get different names;
// a nil suffix returns the base alone.
func SanitizeDNS1123Name(input string, maxBase int, suffix func(input string) string) string {
	var sanitized strings.Builder
	for _, r := range strings.ToLower(input) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sanitized.WriteRune(r)
		} else {
			sanitized.WriteRune('-')
		}
	}
	base := strings.Trim(sanitized.String(), "-")
	if maxBase > 0 && len(base) > maxBase {
		base = strings.TrimRight(base[:maxBase], "-")
	}
	if suffix == nil {
		return base
	}
	return base + suffix(input)
}
