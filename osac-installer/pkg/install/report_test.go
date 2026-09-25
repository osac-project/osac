/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package install

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("FormatResults", func() {
	It("returns just the summary line for zero results", func() {
		lines := FormatResults(nil)
		Expect(lines).To(Equal([]string{"0 passed, 0 failed"}))
	})

	It("marks a passing check PASS and includes its name and message", func() {
		lines := FormatResults([]Result{
			{Check: Check{Name: "cert-manager-crds", Severity: Required}, Status: Pass, Message: "CRD found"},
		})

		Expect(lines).To(HaveLen(2))
		Expect(lines[0]).To(ContainSubstring("PASS"))
		Expect(lines[0]).To(ContainSubstring("cert-manager-crds"))
		Expect(lines[0]).To(ContainSubstring("CRD found"))
		Expect(lines[0]).NotTo(ContainSubstring("FAIL"))
		Expect(lines[1]).To(Equal("1 passed, 0 failed"))
	})

	It("marks a failing check FAIL and includes its severity", func() {
		lines := FormatResults([]Result{
			{Check: Check{Name: "default-storageclass", Severity: Warning}, Status: Failed, Message: "no default"},
		})

		Expect(lines[0]).To(ContainSubstring("FAIL"))
		Expect(lines[0]).To(ContainSubstring("warning"))
		Expect(lines[0]).To(ContainSubstring("default-storageclass"))
		Expect(lines[1]).To(Equal("0 passed, 1 failed"))
	})

	It("preserves result order and counts a mix of statuses correctly", func() {
		results := []Result{
			{Check: Check{Name: "a"}, Status: Pass},
			{Check: Check{Name: "b"}, Status: Failed},
			{Check: Check{Name: "c"}, Status: Pass},
		}

		lines := FormatResults(results)

		Expect(lines).To(HaveLen(4))
		Expect(lines[0]).To(ContainSubstring("a"))
		Expect(lines[1]).To(ContainSubstring("b"))
		Expect(lines[2]).To(ContainSubstring("c"))
		Expect(lines[3]).To(Equal("2 passed, 1 failed"))
	})
})
