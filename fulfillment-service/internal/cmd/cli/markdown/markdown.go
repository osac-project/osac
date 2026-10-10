/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

// Package markdown provides the CLI's readable terminal Markdown style.
package markdown

import (
	"io"
	"os"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"golang.org/x/term"
)

const maxReadableWidth = 100

// NewRenderer creates a Markdown renderer for CLI output and reports whether its style is neutral.
func NewRenderer(out io.Writer, colored bool) (*glamour.TermRenderer, bool, error) {
	width := 0
	if file, ok := out.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		var err error
		width, _, err = term.GetSize(int(file.Fd()))
		if err != nil {
			return nil, false, err
		}
	}
	width = min(width, maxReadableWidth)

	style, neutral := styleForOutput(out, colored)
	if neutral {
		style.Strong.BlockPrefix, style.Strong.BlockSuffix = "", ""
		style.Emph.BlockPrefix, style.Emph.BlockSuffix = "", ""
		style.Strikethrough.BlockPrefix, style.Strikethrough.BlockSuffix = "", ""
	}

	zero := new(uint)
	style.Document.Margin = zero
	style.Document.BlockPrefix = ""
	style.H1.Prefix = ""
	style.H2.Prefix = ""
	style.H3.Prefix = ""
	style.H4.Prefix = ""
	style.H5.Prefix = ""
	style.H6.Prefix = ""
	style.Code.BackgroundColor = nil
	style.Code.Prefix = ""
	style.Code.Suffix = ""

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
	)
	return renderer, neutral, err
}

// styleForOutput chooses a Glamour palette; the boolean reports whether the neutral
// ASCII style needs its visible emphasis markers removed.
func styleForOutput(out io.Writer, colored bool) (ansi.StyleConfig, bool) {
	if !colored {
		return styles.ASCIIStyleConfig, true
	}
	file, ok := out.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		// Forced color to a file or buffer keeps the existing dark palette;
		// this is a fallback, not a detected background.
		return styles.DarkStyleConfig, false
	}
	background, err := lipgloss.BackgroundColor(os.Stdin, file)
	if err != nil || background == nil {
		return styles.ASCIIStyleConfig, true
	}
	if (uv.BackgroundColorEvent{Color: background}).IsDark() {
		return styles.DarkStyleConfig, false
	}
	return styles.LightStyleConfig, false
}
