/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dao

import (
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Repeated enum filter translation", func() {
	for _, desc := range []protoreflect.MessageDescriptor{
		(*publicv1.DiskImage)(nil).ProtoReflect().Descriptor(),
		(*privatev1.DiskImage)(nil).ProtoReflect().Descriptor(),
	} {
		Describe(string(desc.FullName()), func() {
			var translator *FilterTranslator

			BeforeEach(func() {
				var err error
				translator, err = NewFilterTranslator().SetLogger(logger).SetDescriptor(desc).Build()
				Expect(err).NotTo(HaveOccurred())
			})

			DescribeTable("binds enum names", func(ctx context.Context, filter, name string) {
				sql, args, err := translator.Translate(ctx, filter, 2)
				Expect(err).NotTo(HaveOccurred())
				Expect(sql).To(Equal(`data->'spec'->'architecture' @> jsonb_build_array($3::text)`))
				Expect(args).To(Equal([]any{name}))
			},
				Entry("amd64", `1 in this.spec.architecture`, "ARCHITECTURE_AMD64"),
				Entry("arm64", `2 in this.spec.architecture`, "ARCHITECTURE_ARM64"),
				Entry("s390x", `3 in this.spec.architecture`, "ARCHITECTURE_S390X"),
			)

			It("preserves boolean comparisons around membership", func(ctx context.Context) {
				sql, args, err := translator.Translate(ctx, `(1 in this.spec.architecture) == true`, 0)
				Expect(err).NotTo(HaveOccurred())
				Expect(sql).To(Equal(`data->'spec'->'architecture' @> jsonb_build_array($1::text) = true`))
				Expect(args).To(Equal([]any{"ARCHITECTURE_AMD64"}))
			})

			It("preserves empty-list folding without binding unused enum values", func(ctx context.Context) {
				sql, args, err := translator.Translate(ctx, `(1 in this.spec.architecture) in [] || 2 in this.spec.architecture`, 0)
				Expect(err).NotTo(HaveOccurred())
				Expect(sql).To(Equal(`false or data->'spec'->'architecture' @> jsonb_build_array($1::text)`))
				Expect(args).To(Equal([]any{"ARCHITECTURE_ARM64"}))

				sql, args, err = translator.Translate(ctx, `(0 in this.spec.architecture) in []`, 0)
				Expect(err).NotTo(HaveOccurred())
				Expect(sql).To(Equal("false"))
				Expect(args).To(BeEmpty())
			})

			It("numbers multiple predicates in traversal order", func(ctx context.Context) {
				sql, args, err := translator.Translate(ctx, `1 in this.spec.architecture || 2 in this.spec.architecture`, 2)
				Expect(err).NotTo(HaveOccurred())
				Expect(sql).To(ContainSubstring(`jsonb_build_array($3::text)`))
				Expect(sql).To(ContainSubstring(`jsonb_build_array($4::text)`))
				Expect(args).To(Equal([]any{"ARCHITECTURE_AMD64", "ARCHITECTURE_ARM64"}))
			})

			DescribeTable("rejects invalid enum operands without partial arguments", func(ctx context.Context, filter, message string) {
				sql, args, err := translator.Translate(ctx, filter, 0)
				Expect(err).To(MatchError(ContainSubstring(message)))
				Expect(sql).To(BeEmpty())
				Expect(args).To(BeEmpty())
			},
				Entry("zero", `0 in this.spec.architecture`, "does not support zero values stored as null"),
				Entry("unknown", `99 in this.spec.architecture`, "unknown enum value 99"),
				Entry("out of int32 range", `2147483648 in this.spec.architecture`, "unknown enum value 2147483648"),
				Entry("wrong type", `'ARCHITECTURE_AMD64' in this.spec.architecture`, "no matching overload"),
				Entry("nonliteral", `(1 + 1) in this.spec.architecture`, "requires a literal integer value"),
				Entry("error after a bound argument", `1 in this.spec.architecture && this.spec.source_type == 99`, "unknown enum value 99"),
			)

			It("isolates arguments across concurrent translations", func(ctx context.Context) {
				results := make(chan error, 32)
				for i := range 32 {
					go func() {
						value := i%3 + 1
						sql, args, err := translator.Translate(ctx, fmt.Sprintf(`%d in this.spec.architecture`, value), i)
						if err != nil {
							results <- err
							return
						}
						names := []string{"ARCHITECTURE_AMD64", "ARCHITECTURE_ARM64", "ARCHITECTURE_S390X"}
						if !strings.Contains(sql, fmt.Sprintf("$%d::text", i+1)) || len(args) != 1 || args[0] != names[value-1] {
							results <- fmt.Errorf("unexpected translation: %s %v", sql, args)
							return
						}
						results <- nil
					}()
				}
				for range 32 {
					Expect(<-results).NotTo(HaveOccurred())
				}
			})
		})
	}
})
