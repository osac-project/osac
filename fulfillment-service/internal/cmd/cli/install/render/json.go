/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package render

import (
	"encoding/json"

	"github.com/osac-project/osac/osac-installer/pkg/install"
)

// JSONReport is the `--json` output shape for `osac install discover`/
// `validate`: a machine-readable equivalent of Report, for scripted
// consumption instead of a human at a terminal -- never colored,
// table-formatted, or paged.
type JSONReport struct {
	// Ready is true when no Required-severity check failed -- the same
	// condition install.AnyRequiredFailed (and so `osac install validate`'s
	// exit code) uses, exposed here so a script driving either command
	// doesn't need to re-derive it from Results.
	Ready   bool         `json:"ready"`
	Results []JSONResult `json:"results"`
}

// JSONResult is the JSON-serializable shape of one install.Result.
// install.Result itself isn't serialized directly: its Check field carries
// a Run function (not JSON-representable), and Severity/Status are integer
// enums that would otherwise serialize as bare numbers instead of
// "required"/"pass"/etc.
type JSONResult struct {
	Check    string `json:"check"`
	Severity string `json:"severity"`
	Status   string `json:"status"`
	Message  string `json:"message"`
}

// JSON renders results as indented JSON.
func JSON(results []install.Result) (string, error) {
	report := JSONReport{
		Ready:   !install.AnyRequiredFailed(results),
		Results: make([]JSONResult, len(results)),
	}
	for i, result := range results {
		report.Results[i] = JSONResult{
			Check:    result.Check.Name,
			Severity: result.Check.Severity.String(),
			Status:   result.Status.String(),
			Message:  result.Message,
		}
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}
