/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package templating

import (
	"bytes"
	"os"
	"strings"
	tmpl "text/template"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	. "github.com/osac-project/fulfillment-service/internal/testing"
	"github.com/osac-project/fulfillment-service/internal/text"
)

var _ = Describe("Engine", func() {
	It("Can't be created without a logger", func() {
		// Create the filesystem:
		tmp, fsys := TmpFS()
		defer os.RemoveAll(tmp)

		// Create the engine:
		engine, err := NewEngine().
			AddFS(fsys).
			Build()
		Expect(err).To(HaveOccurred())
		msg := err.Error()
		Expect(msg).To(ContainSubstring("logger"))
		Expect(msg).To(ContainSubstring("mandatory"))
		Expect(engine).To(BeNil())
	})

	It("Can be created without a filesystem", func() {
		engine, err := NewEngine().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(engine).ToNot(BeNil())
		Expect(engine.Names()).To(BeEmpty())
	})

	It("Can add filesystem after creation", func() {
		// Create the file system:
		tmp, fsys := TmpFS(
			"a.txt", "a",
		)
		defer os.RemoveAll(tmp)

		// Create the engine without a filesystem:
		engine, err := NewEngine().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(engine.Names()).To(BeEmpty())

		// Add filesystem after creation:
		err = engine.AddFS(fsys)
		Expect(err).ToNot(HaveOccurred())

		// Verify that it has loaded the file:
		names := engine.Names()
		Expect(names).To(ConsistOf("a.txt"))
	})

	It("Can add multiple filesystems at once", func() {
		// Create the file systems:
		tmp1, fsys1 := TmpFS(
			"a.txt", "a",
		)
		defer os.RemoveAll(tmp1)

		tmp2, fsys2 := TmpFS(
			"b.txt", "b",
		)
		defer os.RemoveAll(tmp2)

		// Create the engine and add multiple filesystems:
		engine, err := NewEngine().
			SetLogger(logger).
			AddFS(fsys1, fsys2).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Verify that it has loaded files from both filesystems:
		names := engine.Names()
		Expect(names).To(ConsistOf("a.txt", "b.txt"))
	})

	It("Can add multiple filesystems incrementally", func() {
		// Create the file systems:
		tmp1, fsys1 := TmpFS(
			"a.txt", "a",
		)
		defer os.RemoveAll(tmp1)

		tmp2, fsys2 := TmpFS(
			"b.txt", "b",
		)
		defer os.RemoveAll(tmp2)

		tmp3, fsys3 := TmpFS(
			"c.txt", "c",
		)
		defer os.RemoveAll(tmp3)

		// Create the engine with one filesystem:
		engine, err := NewEngine().
			SetLogger(logger).
			AddFS(fsys1).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Add more filesystems after creation:
		err = engine.AddFS(fsys2, fsys3)
		Expect(err).ToNot(HaveOccurred())

		// Verify that it has loaded files from all filesystems:
		names := engine.Names()
		Expect(names).To(ConsistOf("a.txt", "b.txt", "c.txt"))
	})

	It("Loads single template file", func() {
		// Create the file system:
		tmp, fsys := TmpFS(
			"a.txt", "a",
		)
		defer os.RemoveAll(tmp)

		// Create the engine:
		engine, err := NewEngine().
			SetLogger(logger).
			AddFS(fsys).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Verify that it has loaded the file:
		names := engine.Names()
		Expect(names).To(ConsistOf("a.txt"))
	})

	It("Loads multiple template files", func() {
		// Create the file system:
		tmp, fsys := TmpFS(
			"a.txt", "a",
			"b.txt", "b",
			"c.txt", "c",
		)
		defer os.RemoveAll(tmp)

		// Create the engine:
		engine, err := NewEngine().
			SetLogger(logger).
			AddFS(fsys).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Verify that it has loaded the file:
		names := engine.Names()
		Expect(names).To(ConsistOf("a.txt", "b.txt", "c.txt"))
	})

	It("Executes simple template", func() {
		// Create the file system:
		tmp, fsys := TmpFS(
			"a.txt", "a",
		)
		defer os.RemoveAll(tmp)

		// Create the engine:
		engine, err := NewEngine().
			SetLogger(logger).
			AddFS(fsys).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Execute the template:
		buffer := &bytes.Buffer{}
		err = engine.Execute(buffer, "a.txt", nil)
		Expect(err).ToNot(HaveOccurred())
		text := buffer.String()
		Expect(text).To(Equal("a"))
	})

	It("Executes multiple templates", func() {
		// Create the file system:
		tmp, fsys := TmpFS(
			"a.txt", "a",
			"b.txt", "b",
			"c.txt", "c",
		)
		defer os.RemoveAll(tmp)

		// Create the engine:
		engine, err := NewEngine().
			SetLogger(logger).
			AddFS(fsys).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Execute the first template:
		buffer := &bytes.Buffer{}
		err = engine.Execute(buffer, "a.txt", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(buffer.String()).To(Equal("a"))

		// Executes the second template:
		buffer.Reset()
		err = engine.Execute(buffer, "b.txt", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(buffer.String()).To(Equal("b"))

		// Executes the third template:
		buffer.Reset()
		err = engine.Execute(buffer, "c.txt", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(buffer.String()).To(Equal("c"))
	})

	It("Executes template inside directory", func() {
		// Create the file system:
		tmp, fsys := TmpFS(
			"first/second/myfile.txt", "mytext",
		)
		defer os.RemoveAll(tmp)

		// Create the engine:
		engine, err := NewEngine().
			SetLogger(logger).
			AddFS(fsys).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Execute the template:
		buffer := &bytes.Buffer{}
		err = engine.Execute(buffer, "first/second/myfile.txt", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(buffer.String()).To(Equal("mytext"))
	})

	It("Reports template that doesn't exist", func() {
		// Create the file system:
		tmp, fsys := TmpFS(
			"good.txt", "mycontent",
		)
		defer os.RemoveAll(tmp)

		// Create the engine:
		engine, err := NewEngine().
			SetLogger(logger).
			AddFS(fsys).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Execute the template:
		buffer := &bytes.Buffer{}
		err = engine.Execute(buffer, "bad.txt", nil)
		Expect(err).To(HaveOccurred())
		msg := err.Error()
		Expect(msg).To(ContainSubstring("no template"))
		Expect(msg).To(ContainSubstring("bad.txt"))
	})

	It("Passes data to the template", func() {
		// Create the file system:
		tmp, fsys := TmpFS(
			"f.txt", "x={{ .X }} y={{ .Y }}",
		)
		defer os.RemoveAll(tmp)

		// Create the engine:
		engine, err := NewEngine().
			SetLogger(logger).
			AddFS(fsys).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Execute the template:
		buffer := &bytes.Buffer{}
		err = engine.Execute(buffer, "f.txt", map[string]any{
			"X": 42,
			"Y": 24,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(buffer.String()).To(Equal("x=42 y=24"))
	})

	It("Honours the optional directory", func() {
		// Create the file system:
		tmp, fsys := TmpFS(
			"mydir/myfile.txt", "mytext",
		)
		defer os.RemoveAll(tmp)

		// Create the engine:
		engine, err := NewEngine().
			SetLogger(logger).
			AddFS(fsys).
			SetDir("mydir").
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Execute the template:
		buffer := &bytes.Buffer{}
		err = engine.Execute(buffer, "myfile.txt", nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(buffer.String()).To(Equal("mytext"))
	})

	Context("Template function 'execute'", func() {
		It("Executes the target template", func() {
			// Create the file system:
			tmp, fsys := TmpFS(
				"caller.txt", `{{ execute "called.txt" . }}`,
				"called.txt", `mytext`,
			)
			defer os.RemoveAll(tmp)

			// Create the engine:
			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Execute the template:
			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "caller.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal("mytext"))
		})

		It("Executes multiple chained templates", func() {
			// Create the file system:
			tmp, fsys := TmpFS(
				"first.txt", `{{ execute "second.txt" . }}`,
				"second.txt", `{{ execute "third.txt" . }}`,
				"third.txt", `mytext`,
			)
			defer os.RemoveAll(tmp)

			// Create the engine:
			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Execute the template:
			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "first.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal("mytext"))
		})

		It("Accepts input", func() {
			// Create the file system:
			tmp, fsys := TmpFS(
				"caller.txt", `{{ execute "called.txt" 42 }}`,
				"called.txt", `{{ . }}`,
			)
			defer os.RemoveAll(tmp)

			// Create the engine:
			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Execute the template:
			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "caller.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal("42"))
		})

		It("Fails if executed template doesn't exist", func() {
			// Create the file system. Note the typo in the name of the called template.
			tmp, fsys := TmpFS(
				"caller.txt", `{{ execute "caled.txt" 42 }}`,
				"called.txt", `{{ . }}`,
			)
			defer os.RemoveAll(tmp)

			// Create the engine:
			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Execute the template:
			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "caller.txt", nil)
			Expect(err).To(HaveOccurred())
			msg := err.Error()
			Expect(msg).To(ContainSubstring("find"))
			Expect(msg).To(ContainSubstring("caled.txt"))
		})
	})

	Context("Template function 'base64'", func() {
		It("Encodes text", func() {
			// Create the file system:
			tmp, fsys := TmpFS(
				"myfile.txt", `{{ "mytext" | base64 }}`,
			)
			defer os.RemoveAll(tmp)

			// Create the engine:
			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Execute the template:
			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal("bXl0ZXh0"))
		})
	})

	DescribeTable(
		"Template function 'json'",
		func(input any, expected string) {
			// Create the file system:
			tmp, fsys := TmpFS(
				"myfile.txt", `{{ . | json }}`,
			)
			defer os.RemoveAll(tmp)

			// Create the engine:
			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Execute the template:
			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", input)
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal(expected))
		},
		Entry(
			"String that doesn't need quotes",
			`mytext`,
			`"mytext"`,
		),
		Entry(
			"String that needs quotes",
			`my"text"`,
			`"my\"text\""`,
		),
		Entry(
			"Integer",
			42,
			`42`,
		),
		Entry(
			"Boolean",
			true,
			`true`,
		),
		Entry(
			"Struct without tags",
			struct {
				X int
				Y int
			}{
				X: 42,
				Y: 24,
			},
			`{"X":42,"Y":24}`,
		),
		Entry(
			"Struct with tags",
			struct {
				X int `json:"my_x"`
				Y int `json:"my_y"`
			}{
				X: 42,
				Y: 24,
			},
			`{"my_x":42,"my_y":24}`,
		),
		Entry(
			"Slice",
			[]int{42, 24},
			`[42,24]`,
		),
		Entry(
			"Map",
			map[string]int{"x": 42, "y": 24},
			`{"x":42,"y":24}`,
		),
	)

	Context("Template function 'uuid'", func() {
		It("Generates valid UUIDs", func() {
			// Create the file system:
			tmp, fsys := TmpFS(
				"myuuid.txt", `{{ uuid }}`,
			)
			defer os.RemoveAll(tmp)

			// Create the engine:
			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Execute the template:
			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myuuid.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			_, err = uuid.Parse(buffer.String())
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("Template function 'data'", func() {
		It("Generates expected map", func() {
			// Create the file system:
			tmp, fsys := TmpFS(
				"myfile.txt",
				text.Dedent(`
					{{ range $name, $age := (data "Joe" 52 "Mary" 53) -}}
					{{ $name }}: {{ $age }}
					{{ end -}}
				`),
			)
			defer os.RemoveAll(tmp)

			// Create the engine:
			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Execute the template:
			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal(text.Dedent(`
				Joe: 52
				Mary: 53
			`)))
		})

		It("Fails if number of arguments isn't even", func() {
			// Create the file system:
			tmp, fsys := TmpFS(
				"myfile.txt",
				`{{ data "X" 123 "Y" }}`,
			)
			defer os.RemoveAll(tmp)

			// Create the engine:
			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Execute the template:
			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", nil)
			Expect(err).To(HaveOccurred())
			msg := err.Error()
			Expect(msg).To(ContainSubstring("should be even"))
			Expect(msg).To(ContainSubstring("3"))
		})

		It("Fails if name isn't a string", func() {
			// Create the file system:
			tmp, fsys := TmpFS(
				"myfile.txt",
				`{{ data 42 123 "Y" 456 }}`,
			)
			defer os.RemoveAll(tmp)

			// Create the engine:
			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Execute the template:
			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", nil)
			Expect(err).To(HaveOccurred())
			msg := err.Error()
			Expect(msg).To(ContainSubstring("argument 0 should be a string"))
			Expect(msg).To(ContainSubstring("it is of type int"))
		})
	})

	Context("Template function 'backtick'", func() {
		It("Returns a single backtick by default", func() {
			tmp, fsys := TmpFS(
				"myfile.txt", `{{ backtick }}`,
			)
			defer os.RemoveAll(tmp)

			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal("`"))
		})

		It("Returns the requested number of backticks", func() {
			tmp, fsys := TmpFS(
				"myfile.txt", `{{ backtick 3 }}`,
			)
			defer os.RemoveAll(tmp)

			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal("```"))
		})

		It("Returns a single backtick for zero or negative count", func() {
			tmp, fsys := TmpFS(
				"myfile.txt", `{{ backtick 0 }}`,
			)
			defer os.RemoveAll(tmp)

			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal("`"))
		})
	})

	Context("Template function 'evaluate'", func() {
		It("Evaluates a plain string without template syntax", func() {
			tmp, fsys := TmpFS(
				"myfile.txt", `{{ evaluate "hello world" . }}`,
			)
			defer os.RemoveAll(tmp)

			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal("hello world"))
		})

		It("Evaluates template syntax in the string", func() {
			tmp, fsys := TmpFS(
				"myfile.txt", `{{ evaluate .Text .Data }}`,
			)
			defer os.RemoveAll(tmp)

			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", map[string]any{
				"Text": "name={{ .Name }}",
				"Data": map[string]any{"Name": "Joe"},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal("name=Joe"))
		})

		It("Has access to built-in functions", func() {
			tmp, fsys := TmpFS(
				"myfile.txt", `{{ evaluate .Text . }}`,
			)
			defer os.RemoveAll(tmp)

			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				Build()
			Expect(err).ToNot(HaveOccurred())

			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", map[string]any{
				"Text": "{{ backtick 3 }}shell{{ backtick 3 }}",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal("```shell```"))
		})

		It("Has access to custom functions", func() {
			tmp, fsys := TmpFS(
				"myfile.txt", `{{ evaluate .Text . }}`,
			)
			defer os.RemoveAll(tmp)

			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				AddFunction("greet", func(name string) string {
					return "hello " + name
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", map[string]any{
				"Text": `{{ greet "world" }}`,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal("hello world"))
		})
	})

	Context("Custom functions", func() {
		It("Supports adding a single custom function", func() {
			// Create the file system:
			tmp, fsys := TmpFS(
				"myfile.txt", `{{ "hello world" | upper }}`,
			)
			defer os.RemoveAll(tmp)

			// Create the engine with a custom function:
			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				AddFunction("upper", strings.ToUpper).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Execute the template:
			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal("HELLO WORLD"))
		})

		It("Supports adding multiple custom functions at once", func() {
			// Create the file system:
			tmp, fsys := TmpFS(
				"myfile.txt", `{{ upper "hello" }} {{ lower "WORLD" }}`,
			)
			defer os.RemoveAll(tmp)

			// Create the engine with custom functions:
			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				AddFunctions(tmpl.FuncMap{
					"upper": strings.ToUpper,
					"lower": strings.ToLower,
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Execute the template:
			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal("HELLO world"))
		})

		It("Supports adding functions incrementally", func() {
			// Create the file system:
			tmp, fsys := TmpFS(
				"myfile.txt", `{{ "hello world" | upper | lower }}`,
			)
			defer os.RemoveAll(tmp)

			// Create the engine with custom functions:
			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				AddFunction("upper", strings.ToUpper).
				AddFunction("lower", strings.ToLower).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Execute the template:
			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal("hello world"))
		})

		It("Custom functions can accept parameters", func() {
			// Create the file system:
			tmp, fsys := TmpFS(
				"myfile.txt", `{{ repeat "abc" 3 }}`,
			)
			defer os.RemoveAll(tmp)

			// Create the engine with a custom function:
			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				AddFunction("repeat", strings.Repeat).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Execute the template:
			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(buffer.String()).To(Equal("abcabcabc"))
		})

		It("Built-in functions are still available", func() {
			// Create the file system:
			tmp, fsys := TmpFS(
				"myfile.txt", `{{ "hello" | upper }} {{ uuid | len }}`,
			)
			defer os.RemoveAll(tmp)

			// Create the engine with a custom function:
			engine, err := NewEngine().
				SetLogger(logger).
				AddFS(fsys).
				AddFunction("upper", strings.ToUpper).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Execute the template:
			buffer := &bytes.Buffer{}
			err = engine.Execute(buffer, "myfile.txt", nil)
			Expect(err).ToNot(HaveOccurred())
			result := buffer.String()
			Expect(result).To(HavePrefix("HELLO "))
			Expect(result).To(HaveSuffix(" 36"))
		})
	})
})
