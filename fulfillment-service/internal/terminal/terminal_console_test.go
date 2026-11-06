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
	"errors"
	"io"
	"os"
	"strings"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/text"
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

	Describe("Structured output color", func() {
		It("can force color to a redirected file", func() {
			file, err := os.CreateTemp(GinkgoT().TempDir(), "output")
			Expect(err).NotTo(HaveOccurred())
			console, err := NewConsole().SetLogger(logger).SetStdout(file).SetColorEnabled(true).Build()
			Expect(err).NotTo(HaveOccurred())
			console.RenderJson(ctx, map[string]string{"name": "test"})
			Expect(file.Close()).To(Succeed())
			content, err := os.ReadFile(file.Name())
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(ContainSubstring("\x1b["))
		})

		DescribeTable("respects the color decision",
			func(format string, override *bool, colored bool) {
				var output bytes.Buffer
				builder := NewConsole().SetLogger(logger).SetStdout(&output)
				if override != nil {
					builder.SetColorEnabled(*override)
				}
				console, err := builder.Build()
				Expect(err).NotTo(HaveOccurred())
				if format == "json" {
					console.RenderJson(ctx, map[string]string{"name": "test"})
				} else {
					console.RenderYaml(ctx, map[string]string{"name": "test"})
				}
				if colored {
					Expect(output.String()).To(ContainSubstring("\x1b["))
				} else {
					Expect(output.String()).NotTo(ContainSubstring("\x1b["))
				}
				Expect(output.String()).To(ContainSubstring("test"))
			},
			Entry("JSON without override", "json", nil, false),
			Entry("JSON with color enabled", "json", new(true), true),
			Entry("JSON with color disabled", "json", new(false), false),
			Entry("YAML without override", "yaml", nil, false),
			Entry("YAML with color enabled", "yaml", new(true), true),
			Entry("YAML with color disabled", "yaml", new(false), false),
		)
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

// failingWriter is an io.Writer that accepts at most a prefix of the data written to it and then fails with a
// persistent error, to simulate a broken standard output stream. When it accepts less than the full write it
// reports the error together with the partial write, as an io.Writer is required to do.
type failingWriter struct {
	err    error
	accept int
	wrote  strings.Builder
}

// Write is the implementation of the io.Writer interface.
func (w *failingWriter) Write(p []byte) (int, error) {
	remaining := w.accept - w.wrote.Len()
	if remaining <= 0 {
		return 0, w.err
	}
	n := min(remaining, len(p))
	w.wrote.Write(p[:n])
	if n < len(p) {
		return n, w.err
	}
	return n, nil
}

// makeTemplates creates a console with a small set of templates, to test the Render method.
func makeTemplates() (*Console, error) {
	templates := fstest.MapFS{
		"templates/lines.txt": &fstest.MapFile{Data: []byte("first\nsecond\nthird\n")},
		"templates/broken.txt": &fstest.MapFile{
			// Field access on a string fails when the template is executed, not when it is parsed:
			Data: []byte(`{{ .Value.Field }}`),
		},
	}
	console, err := NewConsole().
		SetLogger(logger).
		Build()
	if err != nil {
		return nil, err
	}
	return console, console.AddTemplates(templates, "templates")
}

var _ = Describe("Console output errors", func() {
	It("Infof returns nil when the write succeeds", func() {
		var output bytes.Buffer
		console, err := NewConsole().SetLogger(logger).SetStdout(&output).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(console.Infof(ctx, "hello\n")).To(Succeed())
		Expect(output.String()).To(Equal("hello\n"))
	})

	It("Infof returns the error when the write fails", func() {
		sentinel := errors.New("broken stdout")
		writer := &failingWriter{err: sentinel}
		console, err := NewConsole().SetLogger(logger).SetStdout(writer).Build()
		Expect(err).ToNot(HaveOccurred())
		err = console.Infof(ctx, "hello\n")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, sentinel)).To(BeTrue())
	})

	It("Infof returns the error after a partial write", func() {
		sentinel := errors.New("broken stdout")
		writer := &failingWriter{err: sentinel, accept: 2}
		console, err := NewConsole().SetLogger(logger).SetStdout(writer).Build()
		Expect(err).ToNot(HaveOccurred())
		err = console.Infof(ctx, "hello\n")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, sentinel)).To(BeTrue())
		Expect(writer.wrote.String()).To(Equal("he"))
	})

	It("Infof returns the closed pipe error when the reader of the pipe is closed", func() {
		reader, writer := io.Pipe()
		defer func() {
			_ = reader.Close()
		}()
		Expect(reader.Close()).To(Succeed())
		console, err := NewConsole().SetLogger(logger).SetStdout(writer).Build()
		Expect(err).ToNot(HaveOccurred())
		err = console.Infof(ctx, "hello\n")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, io.ErrClosedPipe)).To(BeTrue())
	})

	It("Render returns the error when the template doesn't exist", func() {
		console, err := makeTemplates()
		Expect(err).ToNot(HaveOccurred())
		err = console.Render(ctx, "missing.txt", nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`failed to execute template "missing.txt"`))
	})

	It("Render returns the error when the template fails to execute", func() {
		console, err := makeTemplates()
		Expect(err).ToNot(HaveOccurred())
		err = console.Render(ctx, "broken.txt", map[string]any{"Value": "text"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`failed to execute template "broken.txt"`))
	})

	It("Render returns the write error and stops at the first failed line", func() {
		sentinel := errors.New("broken stdout")
		writer := &failingWriter{err: sentinel, accept: 2}
		console, err := NewConsole().SetLogger(logger).SetStdout(writer).Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(console.AddTemplates(fstest.MapFS{
			"templates/lines.txt": &fstest.MapFile{Data: []byte("first\nsecond\nthird\n")},
		}, "templates")).To(Succeed())
		err = console.Render(ctx, "lines.txt", nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, sentinel)).To(BeTrue())
		// Only the prefix of the first line should have been written, the remaining lines must be skipped:
		Expect(writer.wrote.String()).To(Equal("fi"))
	})

	DescribeTable("structured output returns the write error",
		func(format string, color *bool) {
			sentinel := errors.New("broken stdout")
			writer := &failingWriter{err: sentinel}
			builder := NewConsole().SetLogger(logger).SetStdout(writer)
			if color != nil {
				builder.SetColorEnabled(*color)
			}
			console, err := builder.Build()
			Expect(err).ToNot(HaveOccurred())
			var renderErr error
			if format == "json" {
				renderErr = console.RenderJson(ctx, map[string]string{"name": "test"})
			} else {
				renderErr = console.RenderYaml(ctx, map[string]string{"name": "test"})
			}
			Expect(renderErr).To(HaveOccurred())
			Expect(errors.Is(renderErr, sentinel)).To(BeTrue())
		},
		Entry("JSON without color override", "json", nil),
		Entry("JSON with color disabled", "json", new(false)),
		Entry("JSON with color enabled", "json", new(true)),
		Entry("YAML without color override", "yaml", nil),
		Entry("YAML with color enabled", "yaml", new(true)),
	)

	It("RenderJson returns the error after a partial write", func() {
		sentinel := errors.New("broken stdout")
		writer := &failingWriter{err: sentinel, accept: 2}
		console, err := NewConsole().SetLogger(logger).SetStdout(writer).Build()
		Expect(err).ToNot(HaveOccurred())
		err = console.RenderJson(ctx, map[string]string{"name": "test"})
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, sentinel)).To(BeTrue())
		Expect(writer.wrote.String()).To(Equal("{\n"))
	})

	It("RenderJson returns the closed pipe error when color is enabled and the reader of the pipe is closed",
		func() {
			reader, writer := io.Pipe()
			defer func() {
				_ = reader.Close()
			}()
			Expect(reader.Close()).To(Succeed())
			console, err := NewConsole().SetLogger(logger).SetStdout(writer).SetColorEnabled(true).Build()
			Expect(err).ToNot(HaveOccurred())
			err = console.RenderJson(ctx, map[string]string{"name": "test"})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, io.ErrClosedPipe)).To(BeTrue())
		})
})
