/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package install

import "fmt"

// FormatResults renders results as human-readable report lines, one per
// check plus a trailing summary line. It contains no terminal control codes
// or dependency on any particular output mechanism, so both
// `osac install discover` and `osac install validate` can print these lines
// through whatever console abstraction they use, and this package stays
// testable without one.
func FormatResults(results []Result) []string {
	lines := make([]string, 0, len(results)+1)
	var passed, failed int
	for _, result := range results {
		status := "PASS"
		if result.Status == Failed {
			status = "FAIL"
			failed++
		} else {
			passed++
		}
		lines = append(lines, fmt.Sprintf(
			"[%s] (%s) %-45s %s",
			status, result.Check.Severity, result.Check.Name, result.Message,
		))
	}
	lines = append(lines, fmt.Sprintf("%d passed, %d failed", passed, failed))
	return lines
}
