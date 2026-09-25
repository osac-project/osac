/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package render

import (
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/osac-installer/pkg/install"
)

const ansiEscape = "\x1b["

var ansiEscapePattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

// visualWidth returns a line's rendered width, i.e. its rune count with
// ANSI escape sequences (which are invisible, zero-width on screen) removed.
// Plain len() would count both raw bytes of multi-byte box-drawing runes and
// the invisible escape-sequence runes, wildly overstating what a terminal
// actually displays.
func visualWidth(line string) int {
	return len([]rune(ansiEscapePattern.ReplaceAllString(line, "")))
}

var (
	passingCheck  = install.Check{Name: "cert-manager-crds", Severity: install.Required}
	warningCheck  = install.Check{Name: "default-storageclass", Severity: install.Warning}
	requiredCheck = install.Check{Name: "metal3-baremetalhost-crd", Severity: install.Required}
)

var _ = Describe("Report", func() {
	It("falls back to plain FormatResults output when color is disabled, byte for byte", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass, Message: "CRD found"},
			{Check: warningCheck, Status: install.Failed, Message: "no default StorageClass found"},
		}

		got := Report(results, false, 0)

		want := strings.Join(install.FormatResults(results), "\n") + "\n"
		Expect(got).To(Equal(want))
		Expect(got).NotTo(ContainSubstring(ansiEscape))
		Expect(got).NotTo(ContainSubstring("╭"))
	})

	It("renders a bordered, colored table when color is enabled", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass, Message: "CRD found"},
			{Check: warningCheck, Status: install.Failed, Message: "no default StorageClass found"},
		}

		got := Report(results, true, 0)

		Expect(got).To(ContainSubstring(ansiEscape), "expected ANSI color codes when color is enabled")
		Expect(got).To(ContainSubstring("╭"), "expected a bordered table when color is enabled")
		Expect(got).To(ContainSubstring("cert-manager-crds"))
		Expect(got).To(ContainSubstring("default-storageclass"))
	})

	It("includes a summary line with the pass/fail counts", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass},
			{Check: warningCheck, Status: install.Failed},
			{Check: requiredCheck, Status: install.Failed},
		}

		got := Report(results, true, 0)

		Expect(got).To(ContainSubstring("1 passed, 2 failed"))
	})

})

var _ = Describe("summaryLine", func() {
	It("is plain text when nothing failed", func() {
		line := summaryLine([]install.Result{{Check: passingCheck, Status: install.Pass}})

		Expect(line).To(Equal("1 passed, 0 failed"))
		Expect(line).NotTo(ContainSubstring(ansiEscape))
	})

	It("is colored when at least one check failed", func() {
		line := summaryLine([]install.Result{{Check: passingCheck, Status: install.Failed}})

		Expect(line).To(ContainSubstring("0 passed, 1 failed"))
		Expect(line).To(ContainSubstring(ansiEscape))
	})
})

var _ = Describe("Table", func() {
	It("includes a header row and one row per result, in order", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass, Message: "CRD found"},
			{Check: warningCheck, Status: install.Failed, Message: "no default StorageClass"},
			{Check: requiredCheck, Status: install.Failed, Message: "CRD not found"},
		}

		got := Table(results, 0)

		Expect(got).To(ContainSubstring("STATUS"))
		Expect(got).To(ContainSubstring("SEVERITY"))
		Expect(got).To(ContainSubstring("CHECK"))
		Expect(got).To(ContainSubstring("MESSAGE"))

		firstIdx := strings.Index(got, passingCheck.Name)
		secondIdx := strings.Index(got, warningCheck.Name)
		thirdIdx := strings.Index(got, requiredCheck.Name)
		Expect(firstIdx).To(BeNumerically(">", 0))
		Expect(secondIdx).To(BeNumerically(">", firstIdx))
		Expect(thirdIdx).To(BeNumerically(">", secondIdx))
	})

	It("shows PASS for a passing check and FAIL for a failing check", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass, Message: "ok"},
			{Check: warningCheck, Status: install.Failed, Message: "not ok"},
		}

		got := Table(results, 0)

		Expect(got).To(ContainSubstring("PASS"))
		Expect(got).To(ContainSubstring("FAIL"))
	})

	It("wraps long messages onto multiple lines when a width is given", func() {
		longMessage := "this is a deliberately long failure message that should not fit on one line " +
			"at a narrow table width and must wrap onto at least one additional line"
		results := []install.Result{
			{Check: requiredCheck, Status: install.Failed, Message: longMessage},
		}

		got := Table(results, 60)

		// The full message can't appear on a single line of a 60-wide table,
		// but every word must still appear somewhere in the wrapped output.
		for _, word := range strings.Fields(longMessage) {
			Expect(got).To(ContainSubstring(word))
		}
		lines := strings.Split(got, "\n")
		Expect(len(lines)).To(BeNumerically(">", 1), "expected the long message to wrap onto multiple lines")
		for _, line := range lines {
			Expect(visualWidth(line)).To(Equal(60),
				"line's rendered width should exactly match the requested table width: %q", line)
		}
	})

	It("renders without constraint when width is 0", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass, Message: "a short message"},
		}

		unconstrained := Table(results, 0)

		Expect(unconstrained).To(ContainSubstring("a short message"))
	})

	It("returns a well-formed (non-empty, bordered) table for zero results", func() {
		got := Table(nil, 0)

		Expect(got).NotTo(BeEmpty())
		Expect(got).To(ContainSubstring("STATUS"))
	})
})

var _ = Describe("Width", func() {
	It("returns 0 for a non-*os.File writer", func() {
		Expect(Width(&strings.Builder{})).To(Equal(0))
	})
})
