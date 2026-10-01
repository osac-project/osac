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
	"golang.org/x/term"
)

const maxReadableWidth = 100

// NewHelpRenderer creates a Markdown renderer for CLI help, adapting to the output terminal.
func NewHelpRenderer(out io.Writer, colored bool) (*glamour.TermRenderer, error) {
	return newRenderer(out, colored, false)
}

// NewDescriptionRenderer creates a renderer that omits Markdown emphasis markers in plain descriptions.
func NewDescriptionRenderer(out io.Writer, colored bool) (*glamour.TermRenderer, error) {
	return newRenderer(out, colored, true)
}

func newRenderer(out io.Writer, colored, stripPlainEmphasis bool) (*glamour.TermRenderer, error) {
	width := 0
	if file, ok := out.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		var err error
		width, _, err = term.GetSize(int(file.Fd()))
		if err != nil {
			return nil, err
		}
	}
	width = min(width, maxReadableWidth)

	var style ansi.StyleConfig
	if colored {
		if lipgloss.HasDarkBackground(os.Stdin, os.Stdout) {
			style = styles.DarkStyleConfig
		} else {
			style = styles.LightStyleConfig
		}
	} else {
		style = styles.ASCIIStyleConfig
		if stripPlainEmphasis {
			style.Strong.BlockPrefix, style.Strong.BlockSuffix = "", ""
			style.Emph.BlockPrefix, style.Emph.BlockSuffix = "", ""
			style.Strikethrough.BlockPrefix, style.Strikethrough.BlockSuffix = "", ""
		}
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

	return glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
	)
}
