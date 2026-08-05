/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package terminal

import (
	"bytes"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Spinner", func() {
	Describe("Non-interactive mode", func() {
		It("should print the same message again after Stop resets state", func() {
			var buf bytes.Buffer
			s := NewSpinner(&buf)

			s.Start("Connecting...")
			s.Stop()
			s.Start("Connecting...")
			s.Stop()

			lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
			Expect(lines).To(Equal([]string{"Connecting...", "Connecting..."}))
		})

		It("should be safe to Stop when never started", func() {
			var buf bytes.Buffer
			s := NewSpinner(&buf)

			Expect(func() { s.Stop() }).NotTo(Panic())
			Expect(buf.String()).To(BeEmpty())
		})

		It("should not emit escape codes", func() {
			var buf bytes.Buffer
			s := NewSpinner(&buf)

			s.Start("msg")
			s.Stop()

			Expect(buf.String()).To(Equal("msg\n"))
			Expect(buf.String()).NotTo(ContainSubstring("\r"))
			Expect(buf.String()).NotTo(ContainSubstring("\033"))
		})

		It("should deduplicate repeated messages within a cycle", func() {
			var buf bytes.Buffer
			s := NewSpinner(&buf)

			s.Start("A")
			s.Start("A")
			s.Stop()

			Expect(buf.String()).To(Equal("A\n"))
		})

		It("should not deduplicate different messages", func() {
			var buf bytes.Buffer
			s := NewSpinner(&buf)

			s.Start("A")
			s.Start("B")
			s.Stop()

			Expect(buf.String()).To(Equal("A\nB\n"))
		})
	})

	Describe("Interactive mode", func() {
		It("should clear the line on Stop", func() {
			var buf bytes.Buffer
			s := NewSpinner(&buf)
			s.interactive = true

			s.Start("msg")
			// Let the animation goroutine render at least one frame.
			time.Sleep(150 * time.Millisecond)
			s.Stop()

			Expect(buf.String()).To(HaveSuffix("\r\033[K"))
		})

		It("should not panic on double Stop", func() {
			var buf bytes.Buffer
			s := NewSpinner(&buf)
			s.interactive = true

			s.Start("msg")
			time.Sleep(150 * time.Millisecond)

			Expect(func() {
				s.Stop()
				s.Stop()
			}).NotTo(Panic())
		})

		It("should restart animation after Stop", func() {
			var buf bytes.Buffer
			s := NewSpinner(&buf)
			s.interactive = true

			s.Start("first")
			time.Sleep(150 * time.Millisecond)
			s.Stop()

			buf.Reset()

			s.Start("second")
			time.Sleep(150 * time.Millisecond)
			s.Stop()

			Expect(buf.String()).To(ContainSubstring("second"))
		})
	})

	Describe("Writer accessor", func() {
		It("should return the underlying writer", func() {
			var buf bytes.Buffer
			s := NewSpinner(&buf)
			Expect(s.Writer()).To(BeIdenticalTo(&buf))
		})
	})
})
