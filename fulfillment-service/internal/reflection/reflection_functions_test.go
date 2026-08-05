/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package reflection

import (
	"context"
	"errors"
	"io"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Normalize function", func() {
	It("Accepts function without context or error", func() {
		fn, err := NormalizeFunc[string](
			func() string {
				return "hello"
			},
		)
		Expect(err).ToNot(HaveOccurred())
		result, err := fn(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal("hello"))
	})

	It("Accepts function with context but without error", func() {
		fn, err := NormalizeFunc[string](
			func(_ context.Context) string {
				return "ctx"
			},
		)
		Expect(err).ToNot(HaveOccurred())
		result, err := fn(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal("ctx"))
	})

	It("Accepts function without context but with error", func() {
		fn, err := NormalizeFunc[string](
			func() (result string, err error) {
				result = "ok"
				return
			},
		)
		Expect(err).ToNot(HaveOccurred())
		result, err := fn(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal("ok"))
	})

	It("Propagates error from function without context but with error", func() {
		fn, err := NormalizeFunc[string](
			func() (result string, err error) {
				err = errors.New("boom")
				return
			},
		)
		Expect(err).ToNot(HaveOccurred())
		_, err = fn(context.Background())
		Expect(err).To(MatchError("boom"))
	})

	It("Accepts function with context and with error", func() {
		fn, err := NormalizeFunc[string](
			func(_ context.Context) (result string, err error) {
				result = "full"
				return
			},
		)
		Expect(err).ToNot(HaveOccurred())
		result, err := fn(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal("full"))
	})

	It("Propagates error from function with context and with error", func() {
		fn, err := NormalizeFunc[string](func(_ context.Context) (string, error) {
			return "", errors.New("ctx-boom")
		})
		Expect(err).ToNot(HaveOccurred())
		_, err = fn(context.Background())
		Expect(err).To(MatchError("ctx-boom"))
	})

	It("Works with non-string types", func() {
		fn, err := NormalizeFunc[int](
			func() int {
				return 42
			},
		)
		Expect(err).ToNot(HaveOccurred())
		result, err := fn(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(42))
	})

	It("Handles nil return for interface type", func() {
		fn, err := NormalizeFunc[io.Reader](
			func() io.Reader {
				return nil
			},
		)
		Expect(err).ToNot(HaveOccurred())
		result, err := fn(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeNil())
	})

	It("Rejects nil", func() {
		_, err := NormalizeFunc[string](nil)
		Expect(err).To(MatchError("expected a function, got <nil>"))
	})

	It("Rejects typed-nil function", func() {
		var fn func() string
		_, err := NormalizeFunc[string](fn)
		Expect(err).To(MatchError("expected a non-nil function, got nil func() string"))
	})

	It("Rejects non-function values", func() {
		_, err := NormalizeFunc[string]("not-a-function")
		Expect(err).To(MatchError("expected a function, got string"))
	})

	It("Rejects functions with wrong input type", func() {
		_, err := NormalizeFunc[string](
			func(int) string {
				return ""
			},
		)
		Expect(err).To(MatchError("function must have a context.Context parameter, but has int"))
	})

	It("Rejects functions with wrong return type", func() {
		_, err := NormalizeFunc[string](
			func() int {
				return 0
			},
		)
		Expect(err).To(MatchError("function must return string, but returns int"))
	})

	It("Rejects functions with too many inputs", func() {
		_, err := NormalizeFunc[string](
			func(context.Context, int) string {
				return ""
			},
		)
		Expect(err).To(MatchError("function must have at most one input parameter, but has 2"))
	})

	It("Rejects functions with concrete context type", func() {
		type myCtx struct {
			context.Context
		}
		_, err := NormalizeFunc[string](
			func(myCtx) string {
				return ""
			},
		)
		Expect(err).To(HaveOccurred())
	})

	It("Rejects functions with concrete error type", func() {
		type myErr struct {
			error
		}
		_, err := NormalizeFunc[string](
			func() (string, *myErr) {
				return "", nil
			},
		)
		Expect(err).To(HaveOccurred())
	})

	It("Rejects functions with too many outputs", func() {
		_, err := NormalizeFunc[string](
			func() (string, int, error) {
				return "", 0, nil
			},
		)
		Expect(err).To(MatchError("function must return at most two values, but returns 3"))
	})
})
