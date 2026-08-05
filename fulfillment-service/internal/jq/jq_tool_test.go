/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package jq

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Tool", func() {
	It("Can't be created without a logger", func() {
		// Create the template:
		tool, err := NewTool().Build()
		Expect(err).To(HaveOccurred())
		msg := err.Error()
		Expect(msg).To(ContainSubstring("logger"))
		Expect(msg).To(ContainSubstring("mandatory"))
		Expect(tool).To(BeNil())
	})

	It("Accepts primitive input", func() {
		// Create the instance:
		tool, err := NewTool().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Check that it accepts the input:
		var x int
		err = tool.Evaluate(`.`, 42, &x)
		Expect(err).ToNot(HaveOccurred())
		Expect(x).To(Equal(42))
	})

	It("Accepts struct input", func() {
		// Create the instance:
		tool, err := NewTool().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Check that it accepts the input:
		type Point struct {
			X int `json:"x"`
			Y int `json:"y"`
		}
		p := &Point{
			X: 42,
			Y: 24,
		}
		var x int
		err = tool.Evaluate(`.x`, p, &x)
		Expect(err).ToNot(HaveOccurred())
		Expect(x).To(Equal(42))
	})

	It("Accepts map input", func() {
		// Create the instance:
		tool, err := NewTool().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Check that it accepts the input:
		m := map[string]int{
			"x": 42,
			"y": 24,
		}
		var x int
		err = tool.Evaluate(`.x`, m, &x)
		Expect(err).ToNot(HaveOccurred())
		Expect(x).To(Equal(42))
	})

	It("Accepts slice input", func() {
		// Create the instance:
		tool, err := NewTool().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Check that it accepts the input:
		s := []int{42, 24}
		var x int
		err = tool.Evaluate(`.[0]`, s, &x)
		Expect(err).ToNot(HaveOccurred())
		Expect(x).To(Equal(42))
	})

	It("Gets all values if output is slice", func() {
		// Create the instance:
		tool, err := NewTool().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Check that it accepts the input:
		s := []int{42, 24}
		var t []int
		err = tool.Evaluate(`.[]`, s, &t)
		Expect(err).ToNot(HaveOccurred())
		Expect(t).To(ConsistOf(42, 24))
	})

	It("Gets first value if there is only one and output is not slice", func() {
		// Create the instance:
		tool, err := NewTool().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Check that it accepts the input:
		s := []int{42}
		var x int
		err = tool.Evaluate(`.[]`, s, &x)
		Expect(err).ToNot(HaveOccurred())
		Expect(x).To(Equal(42))
	})

	It("Gets first value if there is only one and output is slice", func() {
		// Create the instance:
		tool, err := NewTool().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Check that it accepts the input:
		s := []int{42}
		var t []int
		err = tool.Evaluate(`.[]`, s, &t)
		Expect(err).ToNot(HaveOccurred())
		Expect(t).To(ConsistOf(42))
	})

	It("Returns first result if there are multiple results and output isn't slice", func() {
		// Create the instance:
		tool, err := NewTool().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Check that it fails:
		s := []int{42, 24}
		var x int
		err = tool.Evaluate(`.[]`, s, &x)
		Expect(err).ToNot(HaveOccurred())
		Expect(x).To(Equal(42))
	})

	It("Fails if output is not compatible with input", func() {
		// Create the instance:
		tool, err := NewTool().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Check that it fails:
		var x int
		err = tool.Evaluate(`.`, "mytext", &x)
		Expect(err).To(HaveOccurred())
	})

	It("Rejects output that isn't a pointer", func() {
		// Create the instance:
		tool, err := NewTool().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Check that it rejects the ouptut:
		var x int
		err = tool.Evaluate(`.`, 42, x)
		Expect(err).To(HaveOccurred())
		msg := err.Error()
		Expect(msg).To(ContainSubstring("pointer"))
		Expect(msg).To(ContainSubstring("int"))
	})

	It("Can read from a string", func() {
		// Create the instance:
		tool, err := NewTool().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Check that it can read from a string:
		var x int
		err = tool.EvaluateString(
			`.x`,
			`{
				"x": 42,
				"y": 24
			}`,
			&x,
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(x).To(Equal(42))
	})

	It("Can read from an array of bytes", func() {
		// Create the instance:
		tool, err := NewTool().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Check that it can read from an array of bytes:
		var x int
		err = tool.EvaluateBytes(
			`.x`,
			[]byte(`{
				"x": 42,
				"y": 24
			}`),
			&x,
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(x).To(Equal(42))
	})

	It("Accepts struct output", func() {
		// Create the instance:
		tool, err := NewTool().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Check that it writes into a struct option:
		type Point struct {
			X int `json:"x"`
			Y int `json:"y"`
		}
		var p Point
		err = tool.EvaluateString(
			`{
				"x": .x,
				"y": .y
			}`,
			`{
				"x": 42,
				"y": 24
			}`,
			&p,
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(p.X).To(Equal(42))
		Expect(p.Y).To(Equal(24))
	})

	It("Accepts variables", func() {
		// Create the instance:
		tool, err := NewTool().
			SetLogger(logger).
			Build()
		Expect(err).ToNot(HaveOccurred())

		// Check that it writes into a struct option:
		type Pair struct {
			A string `json:"a"`
			B int    `json:"b"`
		}
		var p Pair
		err = tool.EvaluateString(
			`{
				"a": $var_a,
				"b": $var_b
			}`,
			`{}`,
			&p,
			String("$var_a", "hello"),
			Int("$var_b", 123),
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(p.A).To(Equal("hello"))
		Expect(p.B).To(Equal(123))
	})
})
