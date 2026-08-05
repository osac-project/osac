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
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Normalize nil", func() {
	type fake struct{}

	It("Returns nil for a nil interface", func() {
		var n any
		Expect(NormalizeNil(n)).To(BeNil())
	})

	It("Returns nil for a typed-nil pointer", func() {
		var f *fake                                                  //nolint:staticcheck // SA4023: intentional typed-nil test
		var n any = f                                                //nolint:staticcheck // SA4023: intentional typed-nil test
		Expect(n != nil).To(BeTrue(), "interface itself is not nil") //nolint:ginkgolinter,staticcheck // intentional typed-nil test
		Expect(NormalizeNil(n)).To(BeNil())
	})

	It("Returns the value when it holds a valid pointer", func() {
		f := &fake{}
		var n any = f
		Expect(NormalizeNil(n)).To(Equal(f))
	})
})
