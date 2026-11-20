/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package testing

import (
	"regexp"
	"strings"
)

// TableRows splits rendered CLI table output into rows of trimmed cells, so that assertions compare content without
// depending on the padding.
//
// Two or more consecutive whitespace characters or a single tab separate columns, so the same helper handles both
// space aligned tables (for example those written by text/tabwriter) and tab separated tables. Cells are trimmed, empty
// trailing cells and blank lines are dropped, and rows are returned in the order they appear in the output.
func TableRows(output string) [][]string {
	separator := regexp.MustCompile(`\s{2,}|\t`)
	var rows [][]string
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		row := separator.Split(line, -1)
		for i := range row {
			row[i] = strings.TrimSpace(row[i])
		}
		for len(row) > 0 && row[len(row)-1] == "" {
			row = row[:len(row)-1]
		}
		rows = append(rows, row)
	}
	return rows
}
