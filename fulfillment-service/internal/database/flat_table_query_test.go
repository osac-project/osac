/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package database

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = Describe("Flat table query helpers", func() {
	DescribeTable("normalizeFlatTableLimit",
		func(limit, expected int32) {
			Expect(normalizeFlatTableLimit(limit)).To(Equal(expected))
		},
		Entry("an unset limit serves the default page", int32(0), int32(flatTableDefaultLimit)),
		Entry("an in-range limit passes through", int32(10), int32(10)),
		Entry("the maximum limit passes through", int32(flatTableMaxLimit), int32(flatTableMaxLimit)),
		Entry("an oversized limit is clamped to the maximum", int32(flatTableMaxLimit+1), int32(flatTableMaxLimit)),
		Entry("a math.MaxInt32-style limit is clamped to the maximum", int32(2147483647), int32(flatTableMaxLimit)),
	)
})
