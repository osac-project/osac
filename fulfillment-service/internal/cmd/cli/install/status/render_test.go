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
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/osac-installer/pkg/install"
)

var (
	passingCheck  = install.Check{Name: "cert-manager-crds", Severity: install.Required, Category: install.ResourceCategory}
	warningCheck  = install.Check{Name: "default-storageclass", Severity: install.Warning, Category: install.ResourceCategory}
	requiredCheck = install.Check{Name: "aap-operator", Severity: install.Required, Category: install.OperatorCategory}
)

var _ = Describe("renderStatus", func() {
	It("includes the passed/total count", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass, Message: "CRD found"},
			{Check: requiredCheck, Status: install.Failed, Message: "not installed"},
		}

		got := renderStatus(results, 80)

		Expect(got).To(ContainSubstring("1/2 ready"))
	})

	It("includes every check's name and message", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass, Message: "CRD found"},
			{Check: warningCheck, Status: install.Failed, Message: "no default StorageClass found"},
		}

		got := renderStatus(results, 80)

		Expect(got).To(ContainSubstring("cert-manager-crds"))
		Expect(got).To(ContainSubstring("CRD found"))
		Expect(got).To(ContainSubstring("default-storageclass"))
		Expect(got).To(ContainSubstring("no default StorageClass found"))
	})

	It("handles zero results without panicking", func() {
		got := renderStatus(nil, 80)

		Expect(got).To(ContainSubstring("0/0 ready"))
		Expect(got).To(ContainSubstring("No checks to run"))
	})

	It("falls back to a default width when given 0 or a negative width", func() {
		Expect(func() { renderStatus([]install.Result{{Check: passingCheck, Status: install.Pass}}, 0) }).NotTo(Panic())
		Expect(func() { renderStatus([]install.Result{{Check: passingCheck, Status: install.Pass}}, -5) }).NotTo(Panic())
	})
})

var _ = Describe("statusLine", func() {
	It("stays on one line and ellipsizes a check name longer than the name column", func() {
		longName := install.Check{Name: "metal3-provisioning-watch-all-namespaces", Severity: install.Required}
		result := install.Result{Check: longName, Status: install.Failed, Message: "short"}

		got := statusLine(result, 80)

		Expect(got).NotTo(ContainSubstring("\n"))
		Expect(got).To(ContainSubstring("…"))
		Expect(got).To(ContainSubstring("short"))
	})

	It("ellipsizes an overly long message instead of cutting it off mid-word with no indicator", func() {
		longMessage := "this message is deliberately much longer than any reasonable terminal-width budget for the message column and must be shortened"
		result := install.Result{Check: passingCheck, Status: install.Pass, Message: longMessage}

		got := statusLine(result, 80)

		Expect(got).NotTo(ContainSubstring("\n"))
		Expect(got).To(HaveSuffix("…"))
	})

	It("does not truncate a message that fits", func() {
		result := install.Result{Check: passingCheck, Status: install.Pass, Message: "short message"}

		got := statusLine(result, 80)

		Expect(got).To(ContainSubstring("short message"))
		Expect(got).NotTo(ContainSubstring("…"))
	})
})

var _ = Describe("padName", func() {
	It("right-pads a short name to the target width", func() {
		Expect(padName("abc", 6)).To(Equal("abc   "))
	})

	It("truncates a long name with an ellipsis instead of exceeding the width", func() {
		got := padName("abcdefghij", 6)
		Expect([]rune(got)).To(HaveLen(6))
		Expect(got).To(HaveSuffix("…"))
	})
})

var _ = Describe("sections", func() {
	It("groups results under RESOURCES and OPERATORS headers, resources first", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass},
			{Check: requiredCheck, Status: install.Failed},
		}

		got := sections(results, 76)

		Expect(got).To(ContainSubstring("RESOURCES"))
		Expect(got).To(ContainSubstring("OPERATORS"))
		Expect(strings.Index(got, "RESOURCES")).To(BeNumerically("<", strings.Index(got, "OPERATORS")))
	})

	It("omits a section header when no result belongs to that category", func() {
		results := []install.Result{{Check: passingCheck, Status: install.Pass}}

		got := sections(results, 76)

		Expect(got).To(ContainSubstring("RESOURCES"))
		Expect(got).NotTo(ContainSubstring("OPERATORS"))
	})
})

var _ = Describe("osacFont", func() {
	It("has every glyph at a consistent height and per-glyph row width", func() {
		for letter, glyph := range osacFont {
			Expect(glyph).To(HaveLen(osacFontHeight), "letter %q", string(letter))
			width := len([]rune(glyph[0]))
			for i, row := range glyph {
				Expect([]rune(row)).To(HaveLen(width), "letter %q row %d", string(letter), i)
			}
		}
	})
})

var _ = Describe("banner", func() {
	It("renders osacFontHeight lines plus a trailing blank line", func() {
		got := banner()

		lines := strings.Split(got, "\n")
		// osacFontHeight content lines + 1 blank line from the trailing
		// "\n\n" + the empty string split produces after the final "\n".
		Expect(lines).To(HaveLen(osacFontHeight + 2))
		Expect(lines[osacFontHeight]).To(BeEmpty())
	})
})

var _ = Describe("statusIcon", func() {
	It("shows a check mark for a passing check regardless of severity", func() {
		icon, _ := statusIcon(install.Result{Check: requiredCheck, Status: install.Pass})
		Expect(icon).To(Equal("✓"))
	})

	It("shows an X for a failed Required check", func() {
		icon, _ := statusIcon(install.Result{Check: requiredCheck, Status: install.Failed})
		Expect(icon).To(Equal("✗"))
	})

	It("shows an X for a failed Warning check too, just styled differently", func() {
		icon, _ := statusIcon(install.Result{Check: warningCheck, Status: install.Failed})
		Expect(icon).To(Equal("✗"))
	})
})
