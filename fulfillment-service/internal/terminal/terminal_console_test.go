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
	"os"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/fulfillment-service/internal/text"
)

var _ = Describe("Console", func() {
	Describe("Creation", func() {
		It("Can be created with all the default parameters", func() {
			console, err := NewConsole().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(console).ToNot(BeNil())
		})

		It("Can't be created without a logger", func() {
			console, err := NewConsole().
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(console).To(BeNil())
		})
	})

	Describe("Render YAML", func() {
		It("Can render a simple map as YAML", func() {
			// Ceate a temporary file to write the YAML to:
			file, err := os.CreateTemp("", "*.test")
			Expect(err).ToNot(HaveOccurred())
			defer func() {
				err := file.Close()
				Expect(err).ToNot(HaveOccurred())
			}()

			// Create the console:
			console, err := NewConsole().
				SetLogger(logger).
				SetStdout(file).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Render the YAML and verify the result:
			data := map[string]any{
				"name":  "test",
				"value": 123,
			}
			console.RenderYaml(ctx, data)

			// Verify the content of the file:
			content, err := os.ReadFile(file.Name())
			Expect(err).ToNot(HaveOccurred())
			Expect(string(content)).To(MatchYAML(text.Dedent(`
				name: test
				value: 123
			`)))
		})

		It("Can render a slice as YAML", func() {
			// Ceate a temporary file to write the YAML to:
			file, err := os.CreateTemp("", "*.test")
			Expect(err).ToNot(HaveOccurred())
			defer func() {
				err := file.Close()
				Expect(err).ToNot(HaveOccurred())
			}()

			// Create the console:
			console, err := NewConsole().
				SetLogger(logger).
				SetStdout(file).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Render the YAML and verify the result:
			data := []map[string]any{
				{
					"id":   "1",
					"name": "first",
				},
				{
					"id":   "2",
					"name": "second",
				},
			}
			console.RenderYaml(ctx, data)

			// Verify the content of the file:
			content, err := os.ReadFile(file.Name())
			Expect(err).ToNot(HaveOccurred())
			Expect(string(content)).To(MatchYAML(text.Dedent(`
				- id: "1"
				  name: first
				- id: "2"
				  name: second
			`)))
		})
	})

	Describe("Render JSON", func() {
		It("Can render a simple map as JSON", func() {
			// Ceate a temporary file to write the JSON to:
			file, err := os.CreateTemp("", "*.test")
			Expect(err).ToNot(HaveOccurred())
			defer func() {
				err := file.Close()
				Expect(err).ToNot(HaveOccurred())
			}()

			// Create the console:
			console, err := NewConsole().
				SetLogger(logger).
				SetStdout(file).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Render the JSON and verify the result:
			data := map[string]any{
				"name":  "test",
				"value": 123,
			}
			console.RenderJson(ctx, data)

			// Verify the content of the file:
			content, err := os.ReadFile(file.Name())
			Expect(err).ToNot(HaveOccurred())
			Expect(string(content)).To(MatchJSON(`{
				"name": "test",
				"value": 123
			}`))
		})

		It("Can render a slice as JSON", func() {
			// Ceate a temporary file to write the JSON to:
			file, err := os.CreateTemp("", "*.test")
			Expect(err).ToNot(HaveOccurred())
			defer func() {
				err := file.Close()
				Expect(err).ToNot(HaveOccurred())
			}()

			// Create the console:
			console, err := NewConsole().
				SetLogger(logger).
				SetStdout(file).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Render the JSON and verify the result:
			data := []map[string]any{
				{
					"id":   "1",
					"name": "first",
				},
				{
					"id":   "2",
					"name": "second",
				},
			}
			console.RenderJson(ctx, data)

			// Verify the content of the file:
			content, err := os.ReadFile(file.Name())
			Expect(err).ToNot(HaveOccurred())
			Expect(string(content)).To(MatchJSON(`[
				{
					"id": "1",
					"name": "first"
				},
				{
					"id": "2",
					"name": "second"
				}
			]`))
		})
	})
})
