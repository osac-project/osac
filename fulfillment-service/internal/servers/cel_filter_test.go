/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// cancelAfterChecksContext reports a cancelled request from a chosen ctx.Err check onwards. CEL compilation and
// program construction never observe the context, so no real cancellation can be timed against those windows;
// counting the checks pins each recheck after them deterministically instead of racing the compiler.
type cancelAfterChecksContext struct {
	context.Context
	cancelOn int
	checks   int
}

func (c *cancelAfterChecksContext) Err() error {
	c.checks++
	if c.checks >= c.cancelOn {
		return context.Canceled
	}
	return nil
}

var _ = Describe("CEL filter utilities", func() {
	Describe("filterItems", func() {
		env := newTypedFilterEnv[*privatev1.CatalogPricingInput]()
		items := []*privatev1.CatalogPricingInput{
			privatev1.CatalogPricingInput_builder{Service: "compute_instance", Dimension: "instance_type"}.Build(),
			privatev1.CatalogPricingInput_builder{Service: "cluster", Dimension: "node_count"}.Build(),
		}

		It("returns the input unchanged for an empty filter", func() {
			result, err := filterItems(context.Background(), env, items, "")
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(items))
		})

		It("keeps only the items the expression accepts", func() {
			result, err := filterItems(context.Background(), env, items, `this.service == "cluster"`)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(HaveLen(1))
			Expect(result[0].GetService()).To(Equal("cluster"))
		})

		It("rejects a syntactically invalid filter", func() {
			_, err := filterItems(context.Background(), env, items, `this.service ==`)
			Expect(err).To(HaveOccurred())
		})

		It("rejects a filter whose result is not a boolean", func() {
			_, err := filterItems(context.Background(), env, items, `this.service`)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("must be a boolean"))
		})

		It("rejects a non-boolean filter when there are no items", func() {
			_, err := filterItems(context.Background(), env, []*privatev1.CatalogPricingInput{}, `this.service`)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("must be a boolean"))
		})

		DescribeTable("returns the context error once the request is done",
			func(expired bool, filter string, empty bool, wantCode codes.Code) {
				ctx, cancel := context.WithCancel(context.Background())
				if expired {
					cancel()
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				}
				defer cancel()
				cancel()
				input := items
				if empty {
					input = nil
				}
				result, err := filterItems(ctx, env, input, filter)
				Expect(err).To(MatchError(ctx.Err()))
				Expect(result).To(BeNil())
				// filterError is the only place that maps a filter failure to a transport status; a done request
				// must keep its own status even when the filter would otherwise be reported as InvalidArgument.
				Expect(status.Code(filterError(err))).To(Equal(wantCode))
			},
			Entry("cancelled", false, `this.service == "cluster"`, false, codes.Canceled),
			Entry("expired", true, `this.service == "cluster"`, false, codes.DeadlineExceeded),
			Entry("cancelled with a malformed filter", false, `this.service ==`, false, codes.Canceled),
			Entry("expired with a malformed filter", true, `this.service ==`, false, codes.DeadlineExceeded),
			Entry("cancelled with a non-boolean filter", false, `this.service`, false, codes.Canceled),
			Entry("expired with a non-boolean filter", true, `this.service`, false, codes.DeadlineExceeded),
			Entry("empty filter", false, "", false, codes.Canceled),
			Entry("empty input", false, "true", true, codes.Canceled),
		)

		DescribeTable("reports a request that finishes while the filter is being prepared",
			func(filter string, cancelOn int) {
				ctx := &cancelAfterChecksContext{Context: context.Background(), cancelOn: cancelOn}
				// The empty input keeps every later cancellation point out of reach, so each entry can only
				// pass through the recheck it targets.
				result, err := filterItems(ctx, env, []*privatev1.CatalogPricingInput{}, filter)
				Expect(err).To(MatchError(context.Canceled))
				Expect(result).To(BeNil())
			},
			// The second check is the recheck after compilation, which must win over this malformed filter.
			Entry("cancelled during compilation", `this.service ==`, 2),
			// The third check follows program construction, where an empty input would otherwise return success.
			Entry("cancelled after program construction", "true", 3),
		)

		It("interrupts a CEL comprehension when the request is cancelled during evaluation", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			cancellingEnv, err := env.Extend(cel.Function("cancel_request",
				cel.Overload("cancel_request", []*cel.Type{}, cel.BoolType,
					cel.FunctionBinding(func(...ref.Val) ref.Val {
						calls++
						cancel()
						return types.True
					}))))
			Expect(err).ToNot(HaveOccurred())
			const iterations = 500
			filter := "[" + strings.Repeat("1,", iterations-1) + "1].all(x, cancel_request())"
			result, err := filterItems(ctx, cancellingEnv, items, filter)
			Expect(err).To(MatchError(context.Canceled))
			Expect(result).To(BeNil())
			// Checking ctx only after Eval would still run every iteration; cancellation must interrupt CEL itself.
			Expect(calls).To(BeNumerically(">", 0))
			Expect(calls).To(BeNumerically("<", iterations))
		})
	})

	Describe("validateCELSyntax", func() {
		DescribeTable("validates syntax",
			func(input string, shouldPass bool) {
				err := validateCELSyntax(input)
				if shouldPass {
					Expect(err).ToNot(HaveOccurred())
				} else {
					Expect(err).To(HaveOccurred())
				}
			},
			Entry("valid simple expression", "true", true),
			Entry("valid field reference", "this.published", true),
			Entry("valid comparison", "this.id == '123'", true),
			Entry("valid compound", "this.a && this.b || this.c", true),
			Entry("unbalanced closing paren", "true)", false),
			Entry("unbalanced opening paren", "(true", false),
			Entry("injection attempt", `true) || (true`, false),
			Entry("empty string is not valid CEL", "", false),
		)
	})

	Describe("filterReferencedFields", func() {
		It("returns all dotted field paths from a compound expression", func() {
			fields, err := filterReferencedFields(
				"this.metadata.name == 'x' && this.spec.state == 1 || this.spec.enabled")
			Expect(err).ToNot(HaveOccurred())
			// Intermediate selects (this.metadata, this.spec) are also returned because the
			// AST walker visits every select node in the chain, not just leaves.
			Expect(fields).To(Equal(map[string]bool{
				"this.metadata.name": true,
				"this.metadata":      true,
				"this.spec.state":    true,
				"this.spec.enabled":  true,
				"this.spec":          true,
			}))
		})

		It("returns empty map for expression with no field selects", func() {
			fields, err := filterReferencedFields("true")
			Expect(err).ToNot(HaveOccurred())
			Expect(fields).To(BeEmpty())
		})

		It("returns error for invalid syntax", func() {
			_, err := filterReferencedFields("true) || (true")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("composeFilterDefaults", func() {
		defaults := []filterDefault{
			{field: "this.spec.state", predicate: "(this.spec.state == 1 || this.spec.state == 2)"},
			{field: "this.spec.enabled", predicate: "this.spec.enabled == true"},
		}

		It("applies all defaults when filter is empty", func() {
			result, err := composeFilterDefaults("", defaults)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(
				"(this.spec.state == 1 || this.spec.state == 2) && this.spec.enabled == true"))
		})

		It("applies all defaults when filter references unrelated field", func() {
			result, err := composeFilterDefaults("this.metadata.name == 'test'", defaults)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(
				"(this.metadata.name == 'test') && " +
					"(this.spec.state == 1 || this.spec.state == 2) && " +
					"this.spec.enabled == true"))
		})

		It("skips state default when filter references state", func() {
			result, err := composeFilterDefaults("this.spec.state == 3", defaults)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(
				"(this.spec.state == 3) && this.spec.enabled == true"))
		})

		It("skips enabled default when filter references enabled", func() {
			result, err := composeFilterDefaults("this.spec.enabled == false", defaults)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(
				"(this.spec.enabled == false) && (this.spec.state == 1 || this.spec.state == 2)"))
		})

		It("skips all defaults when filter references all fields", func() {
			result, err := composeFilterDefaults(
				"this.spec.state == 3 && this.spec.enabled == false", defaults)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("this.spec.state == 3 && this.spec.enabled == false"))
		})

		It("returns error for invalid filter", func() {
			_, err := composeFilterDefaults("true) || (true", defaults)
			Expect(err).To(HaveOccurred())
		})

		It("skips default when child field is referenced", func() {
			childDefaults := []filterDefault{
				{field: "this.spec.upgrades", predicate: "this.spec.upgrades.enabled == true"},
			}
			result, err := composeFilterDefaults(
				"this.spec.upgrades.count > 0", childDefaults)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("this.spec.upgrades.count > 0"))
		})
	})

})
