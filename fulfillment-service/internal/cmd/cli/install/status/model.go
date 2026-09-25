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
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/osac-project/osac/osac-installer/pkg/install"
)

// checkResultsMsg carries a fresh check run's results into Update.
type checkResultsMsg struct {
	results []install.Result
}

// tickMsg triggers the next check run in watch mode.
type tickMsg time.Time

// watchModel is the tea.Model driving `osac install status --watch`: it
// re-runs the same checks discover/validate use on an interval and
// redraws in place, fitted to the terminal's current size (tracked via
// tea.WindowSizeMsg).
type watchModel struct {
	ctx      context.Context //nolint:containedctx // bubbletea's Update/Init have no context parameter to thread this through otherwise.
	clients  *install.Clients
	checks   []install.Check
	interval time.Duration

	results []install.Result
	width   int
}

func newWatchModel(ctx context.Context, clients *install.Clients, checks []install.Check, interval time.Duration) watchModel {
	return watchModel{
		ctx:      ctx,
		clients:  clients,
		checks:   checks,
		interval: interval,
	}
}

func (m watchModel) Init() tea.Cmd {
	return runChecks(m.ctx, m.clients, m.checks)
}

func runChecks(ctx context.Context, clients *install.Clients, checks []install.Check) tea.Cmd {
	return func() tea.Msg {
		return checkResultsMsg{results: install.RunAll(ctx, clients, checks)}
	}
}

func tick(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m watchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
		return m, nil
	case checkResultsMsg:
		m.results = msg.results
		return m, tick(m.interval)
	case tickMsg:
		return m, runChecks(m.ctx, m.clients, m.checks)
	}
	return m, nil
}

func (m watchModel) View() tea.View {
	v := tea.NewView(renderStatus(m.results, m.width) + "\n(press q to quit)")
	v.AltScreen = true
	return v
}
