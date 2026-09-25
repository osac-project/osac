/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package status

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/progress"
	"charm.land/lipgloss/v2"

	"github.com/osac-project/osac/osac-installer/pkg/install"
)

// Colors match render.Table's STATUS column, for a consistent look across
// `osac install discover/validate/status`.
const (
	colorGreen  = "10" // Pass
	colorRed    = "9"  // Fail, Required
	colorYellow = "11" // Fail, Warning
	colorBanner = "99" // Purple -- matches the progress bar's default blend
	colorBorder = "99"
	colorHeader = "14" // Cyan section headers (RESOURCES/OPERATORS)
)

const (
	defaultWidth    = 80
	frameMaxWidth   = 100
	frameMinWidth   = 24
	frameOverhead   = 4 // border (2 cols) + horizontal padding (2 cols)
	maxBarWidth     = 60
	minBarWidth     = 10
	nameColWidth    = 36
	barWidthTrim    = 4 // margin subtracted from the content width for the bar
	layoutOverhead  = 3 // icon + the two spaces separating icon/name/message
	minMessageWidth = 10
)

// renderStatus renders results as a framed dashboard: a small "OSAC" banner,
// a progress bar (passed/total), and one Resources/Operators section per
// install.Category, fitted to width. Pure and stateless -- given the same
// results and width, it always renders the same string -- so it's testable
// without a real terminal or tea.Program, and reusable by both the one-shot
// render path and the watch-mode tea.Model's View.
func renderStatus(results []install.Result, width int) string {
	if width <= 0 {
		width = defaultWidth
	}
	frameWidth := width
	if frameWidth > frameMaxWidth {
		frameWidth = frameMaxWidth
	}
	if frameWidth < frameMinWidth {
		frameWidth = frameMinWidth
	}
	contentWidth := frameWidth - frameOverhead

	var b strings.Builder
	b.WriteString(banner())
	b.WriteString(progressLine(results, contentWidth))
	b.WriteString("\n\n")
	b.WriteString(sections(results, contentWidth))

	// Every line written above (banner rows, the progress bar, each
	// statusLine) is already sized to fit within contentWidth, so the frame
	// renders them as-is rather than re-truncating already-ANSI-styled
	// multi-line content here too -- safer than trusting a second
	// width-clamping pass to handle escape codes correctly across lines.
	content := strings.TrimRight(b.String(), "\n")
	frame := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(colorBorder)).
		Padding(0, 1).
		Width(frameWidth)
	return frame.Render(content)
}

// sections groups results into Resources/Operators (install.Category),
// rendering only the sections that actually have entries -- a --services
// selection that never includes any Operator-category check (e.g. no
// services requested at all) shouldn't print an empty "OPERATORS" heading.
func sections(results []install.Result, width int) string {
	if len(results) == 0 {
		return "No checks to run.\n"
	}
	var b strings.Builder
	writeSection(&b, "RESOURCES", filterCategory(results, install.ResourceCategory), width)
	writeSection(&b, "OPERATORS", filterCategory(results, install.OperatorCategory), width)
	return b.String()
}

func writeSection(b *strings.Builder, title string, results []install.Result, width int) {
	if len(results) == 0 {
		return
	}
	b.WriteString(sectionHeader(title))
	b.WriteString("\n")
	for _, result := range results {
		b.WriteString(statusLine(result, width))
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func filterCategory(results []install.Result, category install.Category) []install.Result {
	var out []install.Result
	for _, result := range results {
		if result.Check.Category == category {
			out = append(out, result)
		}
	}
	return out
}

func sectionHeader(title string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorHeader)).Render(title)
}

func progressLine(results []install.Result, width int) string {
	barWidth := width - barWidthTrim
	if barWidth > maxBarWidth {
		barWidth = maxBarWidth
	}
	if barWidth < minBarWidth {
		barWidth = minBarWidth
	}
	bar := progress.New(progress.WithDefaultBlend(), progress.WithWidth(barWidth))

	passed := 0
	for _, result := range results {
		if result.Status == install.Pass {
			passed++
		}
	}
	total := len(results)
	var percent float64
	if total > 0 {
		percent = float64(passed) / float64(total)
	}

	return fmt.Sprintf("%s  %d/%d ready", bar.ViewAs(percent), passed, total)
}

// statusLine renders one check as "<icon> <name, padded/truncated> <message,
// truncated>". Padding and truncation happen on the plain text *before* any
// ANSI styling is applied (lipgloss.Style.Width wraps rather than
// truncating, and padding an already-styled string miscounts its escape
// codes as visible characters) -- both would silently break the single-line
// layout otherwise: a long check name would wrap onto a second line, and an
// overly long message would render with no indication it was cut off.
func statusLine(result install.Result, width int) string {
	icon, style := statusIcon(result)
	name := lipgloss.NewStyle().Bold(true).Render(padName(result.Check.Name, nameColWidth))

	messageWidth := width - nameColWidth - layoutOverhead
	if messageWidth < minMessageWidth {
		messageWidth = minMessageWidth
	}
	message := truncateEllipsis(result.Message, messageWidth)

	return fmt.Sprintf("%s %s %s", style.Render(icon), name, message)
}

// padName truncates s to width (adding an ellipsis if it was cut) or
// right-pads it with spaces to reach width, so every row's message starts
// in the same column regardless of check name length.
func padName(s string, width int) string {
	s = truncateEllipsis(s, width)
	if n := width - len([]rune(s)); n > 0 {
		s += strings.Repeat(" ", n)
	}
	return s
}

// truncateEllipsis returns s unchanged if it's within width runes,
// otherwise cuts it to width-1 runes and appends "…" so a truncated value
// is visibly distinguishable from a short one that happened to fit exactly.
func truncateEllipsis(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width <= 1 {
		return string(r[:width])
	}
	return string(r[:width-1]) + "…"
}

func statusIcon(result install.Result) (string, lipgloss.Style) {
	bold := lipgloss.NewStyle().Bold(true)
	if result.Status == install.Pass {
		return "✓", bold.Foreground(lipgloss.Color(colorGreen))
	}
	if result.Check.Severity == install.Warning {
		return "✗", bold.Foreground(lipgloss.Color(colorYellow))
	}
	return "✗", bold.Foreground(lipgloss.Color(colorRed))
}

// osacFont is a small 6-row block font, just the letters OSAC needs. Every
// glyph's rows are the same rune-length (verified by TestOSACFontGlyphsAlign
// in render_test.go), so banner() can concatenate them column-wise without
// hand-aligning a giant multi-line string by eye.
var osacFont = map[rune][]string{
	'O': {
		" ████ ",
		"██  ██",
		"██  ██",
		"██  ██",
		"██  ██",
		" ████ ",
	},
	'S': {
		" █████",
		"██    ",
		" ████ ",
		"    ██",
		"    ██",
		"█████ ",
	},
	'A': {
		" ████ ",
		"██  ██",
		"██████",
		"██  ██",
		"██  ██",
		"██  ██",
	},
	'C': {
		" █████",
		"██    ",
		"██    ",
		"██    ",
		"██    ",
		" █████",
	},
}

// osacFontHeight is the row count every osacFont glyph has.
const osacFontHeight = 6

// banner renders "OSAC" as block letters, colored, with a trailing blank
// line separating it from the rest of the view.
func banner() string {
	rows := make([]string, osacFontHeight)
	for _, r := range "OSAC" {
		glyph := osacFont[r]
		for i := range rows {
			rows[i] += glyph[i] + " "
		}
	}
	style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorBanner))
	var b strings.Builder
	for _, row := range rows {
		b.WriteString(style.Render(row))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}
