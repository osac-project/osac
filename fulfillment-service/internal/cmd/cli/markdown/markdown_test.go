/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package markdown

import (
	"io"
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = Describe("Markdown rendering", func() {
	DescribeTable("style for non-terminal output",
		func(colored, wantNeutral bool, wantColor string) {
			style, neutral := styleForOutput(io.Discard, colored)
			Expect(neutral).To(Equal(wantNeutral))
			_, rendererNeutral, err := NewRenderer(io.Discard, colored)
			Expect(err).NotTo(HaveOccurred())
			Expect(rendererNeutral).To(Equal(wantNeutral))
			if wantNeutral {
				Expect(style.Document.Color).To(BeNil())
			} else {
				Expect(style.Document.Color).NotTo(BeNil())
				Expect(*style.Document.Color).To(Equal(wantColor))
			}
		},
		Entry("disabled color uses the neutral style", false, true, ""),
		Entry("forced color keeps the dark fallback", true, false, "252"),
	)

	It("omits emphasis markers in neutral output", func() {
		renderer, neutral, err := NewRenderer(io.Discard, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(neutral).To(BeTrue())
		text, err := renderer.Render("**strong** and *emphasis* and ~~strikethrough~~")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(text)).To(Equal("strong and emphasis and strikethrough"))
	})
})
