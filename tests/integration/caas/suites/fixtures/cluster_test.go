// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package fixtures

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
)

var _ = Describe("Cluster request capacity", func() {
	DescribeTable("keeps requested workers independent of available inventory",
		func(capacity int, workers int32, wantError bool) {
			group := InventoryGroup{
				Name: "group", InstanceTypeName: "group-instance-type",
				Inventory: harness.Inventory{Hosts: make([]harness.Host, capacity)},
			}

			spec, err := ClusterSpec(group, workers)
			if wantError {
				Expect(err).To(MatchError(ContainSubstring("requested workers")))
				Expect(spec).To(BeNil())
				return
			}

			Expect(err).NotTo(HaveOccurred())
			Expect(spec.GetTemplate().GetId()).To(Equal(clusterTemplateID))
			Expect(spec.GetVersion().GetName()).To(Equal("caas-it-4-17-1"))
			Expect(spec.GetVersion().GetTenant()).To(Equal("shared"))
			Expect(spec.GetVersion().GetShared()).To(BeFalse())
			Expect(spec.GetNodeSets()).To(HaveLen(1))
			compute := spec.GetNodeSets()["compute"]
			Expect(compute.GetSize()).To(Equal(workers))
			Expect(compute.GetBaremetalInstanceType().GetName()).To(Equal(group.InstanceTypeName))
			Expect(compute.GetBaremetalInstanceType().GetTenant()).To(Equal("shared"))
			Expect(compute.GetBaremetalInstanceType().GetShared()).To(BeFalse())
			Expect(group.Inventory.Hosts).To(HaveLen(capacity))
		},
		Entry("one worker from a larger pool", 2, int32(1), false),
		Entry("two workers", 2, int32(2), false),
		Entry("three workers", 3, int32(3), false),
		Entry("insufficient inventory", 2, int32(3), true),
		Entry("empty inventory", 0, int32(1), true),
		Entry("zero workers", 2, int32(0), true),
		Entry("negative workers", 2, int32(-1), true),
	)
})
