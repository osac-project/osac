/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package logging

import (
	"bytes"
	"context"
	"io"
	"log/slog"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Writer", func() {
	It("Rejects missing logger", func() {
		writer, err := NewWriter().
			Build()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("logger"))
		Expect(writer).To(BeNil())
	})

	It("Writes to the logger", func() {
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetLevel("debug").
			Build()
		Expect(err).ToNot(HaveOccurred())

		writer, err := NewWriter().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		n, err := writer.Write([]byte("my message"))
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(Equal(len("my message")))

		messages := Parse(buffer)
		Expect(messages).To(HaveLen(1))
		Expect(messages[0]["data"]).To(Equal("my message"))
	})

	It("Uses info level by default", func() {
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetLevel("debug").
			Build()
		Expect(err).ToNot(HaveOccurred())

		writer, err := NewWriter().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = writer.Write([]byte("my message"))
		Expect(err).ToNot(HaveOccurred())

		messages := Parse(buffer)
		Expect(messages).To(HaveLen(1))
		Expect(messages[0]["level"]).To(Equal("INFO"))
	})

	It("Honors custom level", func() {
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			SetLevel("debug").
			Build()
		Expect(err).ToNot(HaveOccurred())

		writer, err := NewWriter().
			SetLogger(logger).
			SetLevel(slog.LevelDebug).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = writer.Write([]byte("my message"))
		Expect(err).ToNot(HaveOccurred())

		messages := Parse(buffer)
		Expect(messages).To(HaveLen(1))
		Expect(messages[0]["level"]).To(Equal("DEBUG"))
	})

	It("Passes the context to the logger", func() {
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		ctx := context.Background()
		writer, err := NewWriter().
			SetLogger(logger).
			SetContext(ctx).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = writer.Write([]byte("my message"))
		Expect(err).ToNot(HaveOccurred())

		messages := Parse(buffer)
		Expect(messages).To(HaveLen(1))
		Expect(messages[0]["data"]).To(Equal("my message"))
	})

	It("Redacts a single secret", func() {
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		writer, err := NewWriter().
			SetLogger(logger).
			AddSecret("secret").
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = writer.Write([]byte("my secret value"))
		Expect(err).ToNot(HaveOccurred())

		messages := Parse(buffer)
		Expect(messages).To(HaveLen(1))
		Expect(messages[0]["data"]).To(Equal("my *** value"))
		Expect(messages[0]["data"]).ToNot(ContainSubstring("secret"))
	})

	It("Redacts multiple secrets", func() {
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		writer, err := NewWriter().
			SetLogger(logger).
			AddSecrets("secret", "password").
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = writer.Write([]byte("my secret and password here"))
		Expect(err).ToNot(HaveOccurred())

		messages := Parse(buffer)
		Expect(messages).To(HaveLen(1))
		Expect(messages[0]["data"]).To(Equal("my *** and *** here"))
		Expect(messages[0]["data"]).ToNot(ContainSubstring("secret"))
		Expect(messages[0]["data"]).ToNot(ContainSubstring("password"))
	})

	It("Redacts all occurrences of the same text", func() {
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		writer, err := NewWriter().
			SetLogger(logger).
			AddSecret("secret").
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = writer.Write([]byte("first secret and second secret"))
		Expect(err).ToNot(HaveOccurred())

		messages := Parse(buffer)
		Expect(messages).To(HaveLen(1))
		Expect(messages[0]["data"]).To(Equal("first *** and second ***"))
		Expect(messages[0]["data"]).ToNot(ContainSubstring("secret"))
	})

	It("Doesn't modify message when no redactions are configured", func() {
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		writer, err := NewWriter().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = writer.Write([]byte("my secret value"))
		Expect(err).ToNot(HaveOccurred())

		messages := Parse(buffer)
		Expect(messages).To(HaveLen(1))
		Expect(messages[0]["data"]).To(Equal("my secret value"))
	})

	It("Reports the original byte count even after redaction", func() {
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		writer, err := NewWriter().
			SetLogger(logger).
			AddSecret("secret").
			Build()
		Expect(err).ToNot(HaveOccurred())

		original := []byte("my secret value")
		n, err := writer.Write(original)
		Expect(err).ToNot(HaveOccurred())

		messages := Parse(buffer)
		Expect(messages).To(HaveLen(1))
		Expect(n).To(Equal(len(original)))
	})

	It("Doesn't corrupt message when an empty string is added as a secret", func() {
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		writer, err := NewWriter().
			SetLogger(logger).
			AddSecret("").
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = writer.Write([]byte("my message"))
		Expect(err).ToNot(HaveOccurred())

		messages := Parse(buffer)
		Expect(messages).To(HaveLen(1))
		Expect(messages[0]["data"]).To(Equal("my message"))
	})

	It("Includes size attribute in the log entry", func() {
		buffer := &bytes.Buffer{}
		logger, err := NewLogger().
			SetWriter(io.MultiWriter(buffer, GinkgoWriter)).
			Build()
		Expect(err).ToNot(HaveOccurred())

		writer, err := NewWriter().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		data := []byte("hello")
		n, err := writer.Write(data)
		Expect(err).ToNot(HaveOccurred())

		messages := Parse(buffer)
		Expect(messages).To(HaveLen(1))
		Expect(messages[0]["size"]).To(BeNumerically("==", n))
	})
})
