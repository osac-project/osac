/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package render formats install.Result slices for terminal display, shared
// by `osac install discover` and `osac install validate`. It lives here,
// not in osac-installer/pkg/install, because that package is intentionally
// UI-agnostic (see its FormatResults doc comment) — terminal rendering is a
// CLI-layer concern.
package render

import (
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	xterm "golang.org/x/term"

	"github.com/osac-project/osac/osac-installer/pkg/install"
)

// Colors used for the STATUS column. Plain ANSI (not hex) for broad
// terminal-theme compatibility, matching how terminals conventionally color
// pass/fail/warn output.
const (
	colorGreen  = "10" // Pass
	colorRed    = "9"  // Fail, Required
	colorYellow = "11" // Fail, Warning
)

// maxTableWidth caps the table at a readable width even on a very wide
// terminal, matching internal/cmd/cli/help's maxReadableWidth precedent.
const maxTableWidth = 160

// Width returns the width to render the table at: the terminal's actual
// width (capped at maxTableWidth) if w is a terminal, or 0 (no constraint —
// the table sizes itself to its content) otherwise. Callers pass this
// straight through to Table/Report.
func Width(w io.Writer) int {
	file, ok := w.(*os.File)
	if !ok {
		return 0
	}
	fd := int(file.Fd())
	if !xterm.IsTerminal(fd) {
		return 0
	}
	width, _, err := xterm.GetSize(fd)
	if err != nil {
		return 0
	}
	return min(width, maxTableWidth)
}

// Report renders results for terminal display. When colorEnabled is false
// (piped output, no TTY — see terminal.Console.ColorEnabled), it falls back
// to install.FormatResults' plain line-per-check format instead of drawing
// a bordered table, so scripts that grep this output keep working exactly
// as before: box-drawing characters and ANSI color codes both interfere
// with that in ways a human at a terminal doesn't mind but a script does.
// width is as returned by Width; 0 means size the table to its content.
func Report(results []install.Result, colorEnabled bool, width int) string {
	if !colorEnabled {
		return strings.Join(install.FormatResults(results), "\n") + "\n"
	}
	return Table(results, width) + "\n" + summaryLine(results) + "\n"
}

// Table renders results as a bordered, colored table. Exported directly
// (not just through Report) so a caller that has already decided color is
// appropriate doesn't need to re-derive that decision. width of 0 sizes the
// table to its content instead of constraining/wrapping it.
func Table(results []install.Result, width int) string {
	headerStyle := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	cellStyle := lipgloss.NewStyle().Padding(0, 1)

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderRow(true).
		Headers("STATUS", "SEVERITY", "CHECK", "MESSAGE").
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle
			}
			style := cellStyle
			if col == 0 && row >= 0 && row < len(results) {
				style = style.Bold(true).Foreground(lipgloss.Color(statusColor(results[row])))
			}
			return style
		})
	if width > 0 {
		t = t.Width(width).Wrap(true)
	}

	for _, result := range results {
		t.Row(statusLabel(result.Status), result.Check.Severity.String(), result.Check.Name, result.Message)
	}

	return t.String()
}

func statusLabel(status install.Status) string {
	if status == install.Pass {
		return "PASS"
	}
	return "FAIL"
}

func statusColor(result install.Result) string {
	switch {
	case result.Status == install.Pass:
		return colorGreen
	case result.Check.Severity == install.Warning:
		return colorYellow
	default:
		return colorRed
	}
}

func summaryLine(results []install.Result) string {
	var passed, failed int
	for _, result := range results {
		if result.Status == install.Pass {
			passed++
		} else {
			failed++
		}
	}
	line := fmt.Sprintf("%d passed, %d failed", passed, failed)
	if failed == 0 {
		return line
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorRed)).Render(line)
}
