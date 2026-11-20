/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Keyset DAO record traversal", func() {
	It("visits all matching real DAO records across pages", func() {
		previous := forEachRecordPageSize
		forEachRecordPageSize = 2
		DeferCleanup(func() { forEachRecordPageSize = previous })
		vn := seedVirtualNetwork(testTenant, "10.10.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		var expected []string
		for _, name := range []string{"a", "b", "c", "d", "e"} {
			sg := seedSecurityGroup(vn, name, privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
			expected = append(expected, sg.GetId())
		}
		var ids []string
		Expect(forEachRecord(ctx, fixtureDAO[*privatev1.SecurityGroup](), func(sg *privatev1.SecurityGroup) {
			ids = append(ids, sg.GetId())
		})).To(Succeed())
		Expect(ids).To(ConsistOf(expected))
	})

	It("continues after records on an earlier page are archived", func() {
		previous := forEachRecordPageSize
		forEachRecordPageSize = 2
		DeferCleanup(func() { forEachRecordPageSize = previous })
		vn := seedVirtualNetwork(testTenant, "10.10.0.0/16", privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil)
		securityGroups := fixtureDAO[*privatev1.SecurityGroup]()
		var expected []string
		for _, name := range []string{"a", "b", "c", "d", "e"} {
			sg := seedSecurityGroup(vn, name, privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, nil)
			expected = append(expected, sg.GetId())
		}

		var visited []string
		Expect(forEachRecord(ctx, securityGroups, func(sg *privatev1.SecurityGroup) {
			visited = append(visited, sg.GetId())
			if len(visited) <= 2 {
				_, err := securityGroups.Delete().SetId(sg.GetId()).Do(ctx)
				Expect(err).NotTo(HaveOccurred())
			}
		})).To(Succeed())
		Expect(visited).To(ConsistOf(expected))
	})
})
