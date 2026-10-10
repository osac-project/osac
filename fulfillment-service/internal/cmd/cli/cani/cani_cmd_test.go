/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cani

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Method mapping", func() {
	DescribeTable("Maps methods correctly",
		func(method string, expectedMethod string) {
			result, err := toMethod(method)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(expectedMethod))
		},
		Entry("create", "create", "Create"),
		Entry("Create", "Create", "Create"),
		Entry("get", "get", "Get"),
		Entry("list", "list", "List"),
		Entry("update", "update", "Update"),
		Entry("delete", "delete", "Delete"),
		Entry("watch", "watch", "Watch"),
	)

	It("Returns error for unknown method", func() {
		_, err := toMethod("invalid")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unknown method"))
	})
})
