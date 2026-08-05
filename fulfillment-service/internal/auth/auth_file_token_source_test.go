/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("File token source", func() {
	var (
		ctx    context.Context
		tmpDir string
	)

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Create a temporary directory:
		tmpDir, err = os.MkdirTemp("", "*.test")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			err := os.RemoveAll(tmpDir)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	makeFile := func(content string) string {
		file := filepath.Join(tmpDir, "token.txt")
		err := os.WriteFile(file, []byte(content), 0600)
		Expect(err).ToNot(HaveOccurred())
		return file
	}

	Describe("Build", func() {
		It("Fails when logger is not set", func() {
			file := makeFile("my-token")
			source, err := NewFileTokenSource().
				SetFile(file).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(source).To(BeNil())
		})

		It("Fails when file path is not set", func() {
			source, err := NewFileTokenSource().
				SetLogger(logger).
				Build()
			Expect(err).To(MatchError("file is mandatory"))
			Expect(source).To(BeNil())
		})

		It("Fails when file does not exist", func() {
			file := filepath.Join(tmpDir, "does-not-exist.txt")
			source, err := NewFileTokenSource().
				SetLogger(logger).
				SetFile(file).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(source).To(BeNil())
		})

		It("Succeeds when all required parameters are set", func() {
			file := makeFile("my-token")
			source, err := NewFileTokenSource().
				SetLogger(logger).
				SetFile(file).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(source).ToNot(BeNil())
		})
	})

	Describe("Behaviour", func() {

		It("Fails when token file is empty", func() {
			file := makeFile("")
			source, err := NewFileTokenSource().
				SetLogger(logger).
				SetFile(file).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(source).ToNot(BeNil())
			token, err := source.Token(ctx)
			Expect(err).To(HaveOccurred())
			Expect(token).To(BeNil())
		})

		It("Successfully reads token from file", func() {
			// Write the token:
			file := makeFile("my-token")

			// Create the source:
			source, err := NewFileTokenSource().
				SetLogger(logger).
				SetFile(file).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Verify that the token is read from the file:
			token, err := source.Token(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(token).ToNot(BeNil())
			Expect(token.Access).To(Equal("my-token"))
		})

		It("Returns cached token when file has not changed", func() {
			// Create the file:
			file := makeFile("my-token")

			// Create the source:
			source, err := NewFileTokenSource().
				SetLogger(logger).
				SetFile(file).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// First call should read from file:
			token1, err := source.Token(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(token1).ToNot(BeNil())
			Expect(token1.Access).To(Equal("my-token"))

			// Second call should return cached token:
			token2, err := source.Token(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(token2).To(BeIdenticalTo(token1))
		})

		It("Reloads token when file has changed", func() {
			// Write initial token:
			file := makeFile("my-token")

			// Create the source:
			source, err := NewFileTokenSource().
				SetLogger(logger).
				SetFile(file).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Write a new token to the file:
			err = os.WriteFile(file, []byte("new-token"), 0600)
			Expect(err).ToNot(HaveOccurred())

			// Get the token again, and verify that it is the new one:
			token, err := source.Token(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(token.Access).To(Equal("new-token"))
		})

		It("Handles file permission errors gracefully", func() {
			// Write the token:
			file := makeFile("my-token")

			// Create the source:
			source, err := NewFileTokenSource().
				SetLogger(logger).
				SetFile(file).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Remove read permissions:
			err = os.Chmod(file, 0000)
			Expect(err).ToNot(HaveOccurred())

			// Verify that reading the token fails:
			token, err := source.Token(ctx)
			Expect(err).To(HaveOccurred())
			message := err.Error()
			Expect(message).To(ContainSubstring("permission denied"))
			Expect(token).To(BeNil())
		})
	})
})
