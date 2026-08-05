/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package serial

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Escape Detector", func() {
	var detector *escapeDetector

	BeforeEach(func() {
		detector = newEscapeDetector()
	})

	DescribeTable("should detect escape in single feed",
		func(input []byte) {
			Expect(detector.feed(input)).To(BeTrue())
		},
		Entry("CR ~ . in single chunk", []byte("\r~.")),
		Entry("multiple CRs before ~ .", []byte("\r\r\r~.")),
		Entry("embedded in larger input", []byte("hello\r~.")),
		Entry("Ctrl+]", []byte{0x1D}),
		Entry("Ctrl+] embedded in input", []byte("typing something\x1D")),
	)

	DescribeTable("should not detect escape",
		func(input []byte) {
			Expect(detector.feed(input)).To(BeFalse())
		},
		Entry("~ . without preceding Enter", []byte("~.")),
		Entry("CR ~ without .", []byte("\r~x")),
		Entry("normal text", []byte("hello world")),
	)

	It("should detect retried escape after failed sequence with CR", func() {
		Expect(detector.feed([]byte("\r~\r~."))).To(BeTrue())
	})

	It("should detect retried escape across feeds after CR reset", func() {
		Expect(detector.feed([]byte("\r~\r"))).To(BeFalse())
		Expect(detector.feed([]byte("~."))).To(BeTrue())
	})

	It("should detect CR ~ . across separate feeds", func() {
		Expect(detector.feed([]byte("\r"))).To(BeFalse())
		Expect(detector.feed([]byte("~"))).To(BeFalse())
		Expect(detector.feed([]byte("."))).To(BeTrue())
	})

	It("should detect LF ~ . across separate feeds", func() {
		Expect(detector.feed([]byte("\n"))).To(BeFalse())
		Expect(detector.feed([]byte("~"))).To(BeFalse())
		Expect(detector.feed([]byte("."))).To(BeTrue())
	})

	It("should reset after failed sequence", func() {
		Expect(detector.feed([]byte("\r~x"))).To(BeFalse())
		Expect(detector.feed([]byte("\r~."))).To(BeTrue())
	})
})
