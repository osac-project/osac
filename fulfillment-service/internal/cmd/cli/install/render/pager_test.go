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
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Page", func() {
	It("writes content directly, unpaged, when w isn't a terminal", func() {
		var buf strings.Builder

		err := Page(&buf, "some content\n")

		Expect(err).NotTo(HaveOccurred())
		Expect(buf.String()).To(Equal("some content\n"))
	})
})

var _ = Describe("pagerCommand", func() {
	It("defaults to less with -F -R -X, preserving color through -R", func() {
		GinkgoT().Setenv("PAGER", "")

		name, args := pagerCommand()

		Expect(name).To(Equal("less"))
		Expect(args).To(ContainElement("-R"))
	})

	It("uses $PAGER verbatim, with no flags appended, when set", func() {
		GinkgoT().Setenv("PAGER", "more -c")

		name, args := pagerCommand()

		Expect(name).To(Equal("more"))
		Expect(args).To(Equal([]string{"-c"}))
	})
})
