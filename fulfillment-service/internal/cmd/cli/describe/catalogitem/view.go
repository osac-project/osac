/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package catalogitem

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/cmd/cli/markdown"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type row struct {
	label   string
	state   string
	value   string
	details []string
}

type view struct {
	name        string
	id          string
	title       string
	description string
	metadata    *publicv1.Metadata
	published   bool
	template    string
	kind        string
	fields      []row
	parameters  []row
}

// render shows stored Catalog Item policies without incorporating Template or creation defaults.
func render(w io.Writer, v view, colored bool) error {
	renderer, neutral, err := markdown.NewRenderer(w, colored)
	if err != nil {
		return err
	}
	palette := newPalette(!neutral)
	title := v.title
	if title == "" {
		title = v.name
	}
	if _, err := fmt.Fprintln(w, palette.title.Render(clean(title))); err != nil {
		return err
	}
	if v.description != "" {
		lines := strings.Split(strings.TrimRight(v.description, "\n"), "\n")
		for i, line := range lines {
			lines[i] = clean(line)
		}
		text, err := renderer.Render(strings.Join(lines, "\n"))
		if err != nil {
			return err
		}
		text = strings.Trim(text, "\n") + "\n"
		if !neutral {
			_, err = io.WriteString(w, text)
		} else {
			_, err = lipgloss.Fprint(w, text)
		}
		if err != nil {
			return err
		}
	}
	published := "No"
	if v.published {
		published = "Yes"
	}
	if _, err := fmt.Fprintf(w, "\nName:       %s\nID:         %s\nScope:      %s\nPublished:  %s\nTemplate:   %s\n",
		clean(v.name), clean(v.id), clean(scope(v.metadata)), palette.published(v.published, published), clean(v.template)); err != nil {
		return err
	}
	if err := renderRows(w, "RESOURCE FIELDS", v.fields, palette); err != nil {
		return err
	}
	if err := renderRows(w, "TEMPLATE PARAMETERS", v.parameters, palette); err != nil {
		return err
	}
	getCommand := fmt.Sprintf("osac get %s %s", v.kind, clean(v.name))
	if v.metadata.GetTenant() == auth.SharedTenant {
		getCommand += " --tenant shared"
	}
	_, err = fmt.Fprintf(w, "\n%s\n",
		palette.note.Render(fmt.Sprintf("Full catalog item definition: %s -o yaml", getCommand)))
	return err
}

func renderRows(w io.Writer, heading string, rows []row, palette palette) error {
	if _, err := fmt.Fprintf(w, "\n%s\n", palette.heading.Render(heading)); err != nil {
		return err
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "  (none governed)")
		return err
	}
	width := 27
	for _, r := range rows {
		width = max(width, min(34, lipgloss.Width(clean(r.label))))
	}
	for _, r := range rows {
		label := clean(r.label)
		padding := strings.Repeat(" ", max(0, width-lipgloss.Width(label)))
		state := palette.state(r.state)
		if _, err := fmt.Fprintf(w, "  %s%s  %s", label, padding, state); err != nil {
			return err
		}
		if r.value != "" {
			if _, err := fmt.Fprintf(w, "  %s", clean(r.value)); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, detail := range r.details {
			if _, err := fmt.Fprintf(w, "    %s\n", summarizeLongDetail(clean(detail))); err != nil {
				return err
			}
		}
	}
	return nil
}

// summarizeLongDetail points to the full definition when a structured detail is too wide to show inline.
func summarizeLongDetail(value string) string {
	if lipgloss.Width(value) > 100 {
		return "(long value; see get -o yaml)"
	}
	return value
}

func scope(metadata *publicv1.Metadata) string {
	result := "Tenant"
	if metadata == nil || metadata.GetTenant() == "" {
		result = "-"
	} else if metadata.GetTenant() == auth.SharedTenant {
		result = "Shared"
	}
	if project := metadata.GetProject(); project != "" {
		result += " (project: " + project + ")"
	}
	return result
}

// clean replaces terminal and invisible formatting controls in server-provided text with spaces.
func clean(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || (r >= 0x80 && r <= 0x9f) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, value)
}
