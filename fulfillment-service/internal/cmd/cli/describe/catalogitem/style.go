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

import "charm.land/lipgloss/v2"

type palette struct {
	title    lipgloss.Style
	heading  lipgloss.Style
	locked   lipgloss.Style
	editable lipgloss.Style
	note     lipgloss.Style
}

func newPalette(colored bool) palette {
	if !colored {
		plain := lipgloss.NewStyle()
		return palette{title: plain, heading: plain, locked: plain, editable: plain, note: plain}
	}
	return palette{
		title:    lipgloss.NewStyle().Bold(true),
		heading:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")),
		locked:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")),
		editable: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2")),
		note:     lipgloss.NewStyle().Faint(true),
	}
}

func (p palette) state(value string) string {
	if value == "LOCKED" {
		return p.locked.Render("LOCKED  ")
	}
	return p.editable.Render("EDITABLE")
}

func (p palette) published(value bool, label string) string {
	if value {
		return p.editable.Render(label)
	}
	return label
}
