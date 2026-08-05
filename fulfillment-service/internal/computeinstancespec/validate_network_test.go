/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package computeinstancespec

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("ValidateNetworkAttachments", func() {
	DescribeTable("validates network attachments",
		func(attachments []*privatev1.NetworkAttachment, shouldError bool) {
			err := ValidateNetworkAttachments(attachments)
			if shouldError {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).ToNot(HaveOccurred())
			}
		},
		Entry("nil attachments (pod network)",
			nil,
			false,
		),
		Entry("empty attachments array (pod network)",
			[]*privatev1.NetworkAttachment{},
			false,
		),
		Entry("valid attachments with subnets",
			[]*privatev1.NetworkAttachment{
				privatev1.NetworkAttachment_builder{Subnet: "subnet-a"}.Build(),
				privatev1.NetworkAttachment_builder{Subnet: "subnet-b"}.Build(),
			},
			false,
		),
		Entry("valid attachment with subnet and security groups",
			[]*privatev1.NetworkAttachment{
				privatev1.NetworkAttachment_builder{
					Subnet:         "subnet-a",
					SecurityGroups: []string{"sg-1", "sg-2"},
				}.Build(),
			},
			false,
		),
		Entry("invalid attachment with empty subnet",
			[]*privatev1.NetworkAttachment{
				privatev1.NetworkAttachment_builder{Subnet: ""}.Build(),
			},
			true,
		),
		Entry("invalid second attachment with empty subnet",
			[]*privatev1.NetworkAttachment{
				privatev1.NetworkAttachment_builder{Subnet: "subnet-a"}.Build(),
				privatev1.NetworkAttachment_builder{Subnet: ""}.Build(),
			},
			true,
		),
	)
})
