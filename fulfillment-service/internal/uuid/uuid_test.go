/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package uuid

import (
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("UUID generation", func() {
	It("Generates unique identifiers", func() {
		const count = 1000
		seen := make(map[string]bool)
		for range count {
			id := New()
			Expect(seen[id]).To(BeFalse())
			seen[id] = true
		}
	})

	It("Generates identifiers sorted by generation time", func() {
		const count = 100
		ids := make([]string, count)
		for i := range count {
			ids[i] = New()
			time.Sleep(time.Microsecond)
		}
		sorted := make([]string, count)
		copy(sorted, ids)
		sort.Strings(sorted)
		Expect(ids).To(Equal(sorted))
	})
})
