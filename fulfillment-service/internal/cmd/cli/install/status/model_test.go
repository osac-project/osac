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
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/osac-installer/pkg/install"
)

var _ = Describe("watchModel", func() {
	var m watchModel

	BeforeEach(func() {
		m = newWatchModel(context.Background(), nil, nil, time.Second)
	})

	It("kicks off a check run from Init", func() {
		cmd := m.Init()
		Expect(cmd).NotTo(BeNil())

		msg := cmd()
		results, ok := msg.(checkResultsMsg)
		Expect(ok).To(BeTrue())
		Expect(results.results).To(BeEmpty()) // nil checks -> RunAll returns no results
	})

	It("stores results and schedules the next tick on checkResultsMsg", func() {
		results := []install.Result{{Check: passingCheck, Status: install.Pass}}

		next, cmd := m.Update(checkResultsMsg{results: results})

		updated := next.(watchModel)
		Expect(updated.results).To(Equal(results))
		Expect(cmd).NotTo(BeNil()) // schedules the next tick
	})

	It("re-runs checks on tickMsg", func() {
		_, cmd := m.Update(tickMsg(time.Now()))

		Expect(cmd).NotTo(BeNil())
		msg := cmd()
		_, ok := msg.(checkResultsMsg)
		Expect(ok).To(BeTrue())
	})

	It("tracks the terminal width from WindowSizeMsg", func() {
		next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

		Expect(next.(watchModel).width).To(Equal(120))
	})

	It("quits on q", func() {
		_, cmd := m.Update(tea.KeyPressMsg{Text: "q", Code: 'q'})
		Expect(cmd).NotTo(BeNil())
		Expect(cmd()).To(Equal(tea.QuitMsg{}))
	})

	It("ignores other keys", func() {
		_, cmd := m.Update(tea.KeyPressMsg{Text: "x", Code: 'x'})
		Expect(cmd).To(BeNil())
	})

	It("renders a bordered-free status view with AltScreen enabled", func() {
		m.results = []install.Result{{Check: passingCheck, Status: install.Pass, Message: "ok"}}

		v := m.View()

		Expect(v.AltScreen).To(BeTrue())
		Expect(v.Content).To(ContainSubstring("cert-manager-crds"))
		Expect(v.Content).To(ContainSubstring("press q to quit"))
	})
})
