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
	"context"
	"errors"
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Shared DAO page traversal", func() {
	DescribeTable("visits rows and stops after the first short page",
		func(rows []int, wantOffsets []int32) {
			var offsets []int32
			var visited []int
			list := func(gotCtx context.Context, offset, limit int32) ([]int, error) {
				Expect(gotCtx).To(Equal(ctx))
				Expect(limit).To(Equal(int32(2)))
				offsets = append(offsets, offset)
				end := min(int(offset+limit), len(rows))
				return rows[int(offset):end], nil
			}
			Expect(forEachDAOPage(ctx, 2, list, func(row int) { visited = append(visited, row) })).To(Succeed())
			Expect(visited).To(Equal(rows))
			Expect(offsets).To(Equal(wantOffsets))
		},
		Entry("several pages with a final partial page", []int{1, 2, 3, 4, 5}, []int32{0, 2, 4}),
		Entry("full final page requires an empty page", []int{1, 2, 3, 4}, []int32{0, 2, 4}),
		Entry("empty first page", []int(nil), []int32{0}),
	)

	It("returns the original list error without visiting later pages", func() {
		failure := errors.New("list unavailable")
		var visited []int
		calls := 0
		list := func(_ context.Context, offset, _ int32) ([]int, error) {
			calls++
			if offset == 0 {
				return []int{1, 2}, nil
			}
			return nil, failure
		}
		err := forEachDAOPage(ctx, 2, list, func(row int) { visited = append(visited, row) })
		Expect(err).To(BeIdenticalTo(failure))
		Expect(visited).To(Equal([]int{1, 2}))
		Expect(calls).To(Equal(2))
	})

	DescribeTable("checks the int32 offset boundary before incrementing",
		func(offset, size, want int32, overflow bool) {
			next, err := nextDAOOffset(offset, size)
			if overflow {
				Expect(err).To(BeIdenticalTo(ErrDAOOffsetOverflow))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(next).To(Equal(want))
		},
		Entry("initial offset", int32(0), int32(1000), int32(1000), false),
		Entry("exact last offset", int32(math.MaxInt32-1000), int32(1000), int32(math.MaxInt32), false),
		Entry("one past the boundary", int32(math.MaxInt32-999), int32(1000), int32(0), true),
		Entry("maximum offset", int32(math.MaxInt32), int32(1000), int32(0), true),
	)

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
})
