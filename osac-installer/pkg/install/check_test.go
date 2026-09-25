/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package install

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("RunAll", func() {
	It("runs every check and preserves order", func() {
		var ran []string
		checks := []Check{
			{Name: "a", Run: func(_ context.Context, _ *Clients) (Status, string) {
				ran = append(ran, "a")
				return Pass, "ok"
			}},
			{Name: "b", Run: func(_ context.Context, _ *Clients) (Status, string) {
				ran = append(ran, "b")
				return Failed, "not ok"
			}},
		}

		results := RunAll(context.Background(), newFakeClients(nil, nil), checks)

		Expect(ran).To(Equal([]string{"a", "b"}))
		Expect(results).To(HaveLen(2))
		Expect(results[0].Check.Name).To(Equal("a"))
		Expect(results[0].Status).To(Equal(Pass))
		Expect(results[0].Message).To(Equal("ok"))
		Expect(results[1].Check.Name).To(Equal("b"))
		Expect(results[1].Status).To(Equal(Failed))
		Expect(results[1].Message).To(Equal("not ok"))
	})

	It("returns an empty, non-nil slice for zero checks", func() {
		results := RunAll(context.Background(), newFakeClients(nil, nil), nil)
		Expect(results).NotTo(BeNil())
		Expect(results).To(BeEmpty())
	})
})

var _ = Describe("AnyRequiredFailed", func() {
	It("returns false when there are no results", func() {
		Expect(AnyRequiredFailed(nil)).To(BeFalse())
	})

	It("returns false when every check passed", func() {
		results := []Result{
			{Check: Check{Severity: Required}, Status: Pass},
			{Check: Check{Severity: Warning}, Status: Pass},
		}
		Expect(AnyRequiredFailed(results)).To(BeFalse())
	})

	It("returns false when only a Warning check failed", func() {
		results := []Result{
			{Check: Check{Severity: Required}, Status: Pass},
			{Check: Check{Severity: Warning}, Status: Failed},
		}
		Expect(AnyRequiredFailed(results)).To(BeFalse())
	})

	It("returns true when a Required check failed", func() {
		results := []Result{
			{Check: Check{Severity: Warning}, Status: Pass},
			{Check: Check{Severity: Required}, Status: Failed},
		}
		Expect(AnyRequiredFailed(results)).To(BeTrue())
	})
})

var _ = Describe("Severity", func() {
	It("stringifies known values", func() {
		Expect(Required.String()).To(Equal("required"))
		Expect(Warning.String()).To(Equal("warning"))
	})

	It("stringifies an unknown value without panicking", func() {
		Expect(Severity(99).String()).To(Equal("unknown"))
	})
})

var _ = Describe("Status", func() {
	It("stringifies known values", func() {
		Expect(Pass.String()).To(Equal("pass"))
		Expect(Failed.String()).To(Equal("fail"))
	})

	It("stringifies an unknown value without panicking", func() {
		Expect(Status(99).String()).To(Equal("unknown"))
	})
})
