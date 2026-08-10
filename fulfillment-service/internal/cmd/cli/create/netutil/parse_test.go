/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package netutil

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ExtractSecurityGroupListSuffix", func() {
	DescribeTable("when input contains security groups it should extract them",
		func(input, wantPrefix string, wantGroups []string) {
			prefix, groups, ok := ExtractSecurityGroupListSuffix(input)
			Expect(ok).To(BeTrue())
			Expect(prefix).To(Equal(wantPrefix))
			Expect(groups).To(Equal(wantGroups))
		},
		Entry("security-groups with hyphen",
			"subnet=b,security-groups=x", "subnet=b", []string{"x"}),
		Entry("security_groups with underscore",
			"subnet=a,security_groups=g1,g2", "subnet=a", []string{"g1", "g2"}),
		Entry("preserves case in prefix and groups",
			"subnet=Sub-1,interface=Eth0,security-groups=SG-A,SG-B",
			"subnet=Sub-1,interface=Eth0", []string{"SG-A", "SG-B"}),
	)

	It("should return the original string unchanged when no security groups are present", func() {
		prefix, groups, ok := ExtractSecurityGroupListSuffix("subnet=s1,interface=data-0")
		Expect(ok).To(BeFalse())
		Expect(prefix).To(Equal("subnet=s1,interface=data-0"))
		Expect(groups).To(BeNil())
	})
})
