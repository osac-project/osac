/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is
distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
implied. See the License for the specific language governing permissions and limitations under the
License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// The refusal decision is plain arithmetic over plain data, so these tables run in
// milliseconds with no database: the project chain, the subtree sums, the limit
// resolution order, the scope walk of the comparison, the update-mask coverage and the
// parent-id parser.

var _ = Describe("Quota enforcement decisions", func() {
	DescribeTable("returns the project chain",
		func(project string, chain []string) {
			Expect(quotaProjectChain(project)).To(Equal(chain))
		},
		Entry("a nested project and each named ancestor", "team.web", []string{"team.web", "team"}),
		Entry("a top-level project alone", "team", []string{"team"}),
		Entry("a three-level chain", "a.b.c", []string{"a.b.c", "a.b", "a"}),
		Entry("the default project has no chain of its own", "", nil),
	)

	Describe("sums the usage of a scope's subtree", func() {
		usage := map[string]map[string]int64{
			"":         {"storage.gib": 1},
			"team":     {"storage.gib": 2},
			"team.web": {"storage.gib": 4, "compute.vcpus": 3},
			"teamx":    {"storage.gib": 8},
			"other":    {"storage.gib": 16},
		}

		DescribeTable("returns the subtree's sums",
			func(scope string, want map[string]int64) {
				Expect(quotaSubtreeUsage(usage, scope)).To(Equal(want))
			},
			Entry("the project itself", "team.web",
				map[string]int64{"storage.gib": 4, "compute.vcpus": 3}),
			Entry("the project and the projects inside it", "team",
				map[string]int64{"storage.gib": 6, "compute.vcpus": 3}),
			Entry("a same-prefix project sits outside the subtree", "teamx",
				map[string]int64{"storage.gib": 8}),
			Entry("the empty scope sums the whole tenant", "",
				map[string]int64{"storage.gib": 31, "compute.vcpus": 3}),
			Entry("a scope nothing reports", "missing", map[string]int64{}),
		)
	})

	Describe("resolves a scope's limit", func() {
		limits := &quotaLimits{
			defaults:  map[string]int64{"with.default": 3, "with.both": 3},
			overrides: map[string]int64{"with.override": 10, "with.both": 10},
			projects: map[string]map[string]int64{
				"team": {"with.override": 4},
			},
		}

		DescribeTable("returns the limit that binds the scope",
			func(scope, dimension string, want int64) {
				Expect(limits.limitOf(scope, dimension)).To(Equal(want))
			},
			Entry("the project's own limit wins", "team", "with.override", int64(4)),
			Entry("the tenant's override replaces the default", "", "with.both", int64(10)),
			Entry("the override of a project that sets none", "team", "with.both", int64(10)),
			Entry("the default when the tenant sets none", "", "with.default", int64(3)),
			Entry("zero when nothing sets it", "team", "unset", int64(0)),
		)
	})

	DescribeTable("returns the first refusal of a request",
		func(added map[string]int64, usage map[string]map[string]int64, limits *quotaLimits,
			project string, want *quotaRefusal,
		) {
			Expect(firstQuotaRefusal(added, usage, limits, project)).To(Equal(want))
		},
		Entry("a request that fits every scope",
			map[string]int64{"storage.gib": 1},
			map[string]map[string]int64{"team": {"storage.gib": 1}},
			&quotaLimits{
				defaults: map[string]int64{"storage.gib": 10},
				projects: map[string]map[string]int64{"team": {"storage.gib": 5}},
			},
			"team", nil),
		Entry("the request's project refuses first",
			map[string]int64{"storage.gib": 2},
			map[string]map[string]int64{"team.web": {"storage.gib": 1}},
			&quotaLimits{
				defaults: map[string]int64{"storage.gib": 1},
				projects: map[string]map[string]int64{"team.web": {"storage.gib": 2}},
			},
			"team.web",
			&quotaRefusal{
				scope: publicv1.QuotaScope_QUOTA_SCOPE_PROJECT, project: "team.web", dimension: "storage.gib",
				limit: 2, usage: 1, added: 2,
			}),
		Entry("an ancestor project refuses when the project fits",
			map[string]int64{"storage.gib": 2},
			map[string]map[string]int64{"team.web": {"storage.gib": 1}},
			&quotaLimits{
				projects: map[string]map[string]int64{
					"team.web": {"storage.gib": 3},
					"team":     {"storage.gib": 2},
				},
			},
			"team.web",
			&quotaRefusal{
				scope: publicv1.QuotaScope_QUOTA_SCOPE_ANCESTOR_PROJECT, project: "team", dimension: "storage.gib",
				limit: 2, usage: 1, added: 2,
			}),
		Entry("the tenant refuses a request in the default project",
			map[string]int64{"storage.gib": 2},
			map[string]map[string]int64{"": {"storage.gib": 1}},
			&quotaLimits{defaults: map[string]int64{"storage.gib": 2}},
			"",
			&quotaRefusal{
				scope: publicv1.QuotaScope_QUOTA_SCOPE_TENANT, dimension: "storage.gib", limit: 2, usage: 1, added: 2,
			}),
		Entry("the first scope in the walk wins over a crossing tenant",
			map[string]int64{"storage.gib": 2},
			map[string]map[string]int64{"team": {"storage.gib": 1}},
			&quotaLimits{
				defaults: map[string]int64{"storage.gib": 1},
				projects: map[string]map[string]int64{"team": {"storage.gib": 2}},
			},
			"team",
			&quotaRefusal{
				scope: publicv1.QuotaScope_QUOTA_SCOPE_PROJECT, project: "team", dimension: "storage.gib",
				limit: 2, usage: 1, added: 2,
			}),
		Entry("a project without a limit of its own is refused by the tenant's",
			map[string]int64{"storage.gib": 2},
			map[string]map[string]int64{"team": {"storage.gib": 4}, "": {"storage.gib": 3}},
			&quotaLimits{overrides: map[string]int64{"storage.gib": 5}},
			"team",
			&quotaRefusal{
				scope: publicv1.QuotaScope_QUOTA_SCOPE_TENANT, dimension: "storage.gib", limit: 5,
				usage: 7, added: 2,
			}),
		Entry("a project without a limit of its own is refused by the default",
			map[string]int64{"storage.gib": 2},
			map[string]map[string]int64{"team": {"storage.gib": 4}, "": {"storage.gib": 3}},
			&quotaLimits{defaults: map[string]int64{"storage.gib": 5}},
			"team",
			&quotaRefusal{
				scope: publicv1.QuotaScope_QUOTA_SCOPE_TENANT, dimension: "storage.gib", limit: 5,
				usage: 7, added: 2,
			}),
		Entry("two crossed dimensions report the alphabetically first",
			map[string]int64{"compute.vcpus": 1, "storage.gib": 1},
			map[string]map[string]int64{"": {"compute.vcpus": 0, "storage.gib": 0}},
			&quotaLimits{
				defaults: map[string]int64{"compute.vcpus": 0, "storage.gib": 0},
			},
			"",
			&quotaRefusal{
				scope: publicv1.QuotaScope_QUOTA_SCOPE_TENANT, dimension: "compute.vcpus", limit: 0, usage: 0, added: 1,
			}),
	)

	DescribeTable("tells whether a mask path covers an annotated field",
		func(maskPath, annotated string, covered bool) {
			mask := &fieldmaskpb.FieldMask{Paths: []string{maskPath}}
			Expect(updateIncludesField(mask, annotated)).To(Equal(covered))
		},
		Entry("a mask of spec covers every spec field", "spec", "spec.instance_type", true),
		Entry("a mask of spec covers a deep spec field", "spec",
			"spec.node_sets.workers.baremetal_instance_type.name", true),
		Entry("the same field", "spec.instance_type", "spec.instance_type", true),
		Entry("a deeper mask path covers the annotated field", "spec.instance_type.name",
			"spec.instance_type", true),
		Entry("an annotated path deeper than the mask", "spec.node_sets",
			"spec.node_sets.workers.size", true),
		Entry("a sibling spec field", "spec.run_strategy", "spec.instance_type", false),
		Entry("a status path", "status.instance_type", "spec.instance_type", false),
		Entry("a metadata path", "metadata.name", "spec", false),
	)

	Describe("parses a parent id out of a created child's name", func() {
		vmID := uuid.New()

		DescribeTable("returns the id",
			func(volumeName, wantID string, wantOK bool) {
				id, ok := quotaParentIDFromName(volumeName, "ci-{parent}-")
				Expect(ok).To(Equal(wantOK))
				Expect(id).To(Equal(wantID))
			},
			Entry("a uuidv7 after the ci- prefix", "ci-"+vmID+"-boot", vmID, true),
			Entry("a short caller-chosen id", "ci-shortvm-boot", "", false),
			Entry("a foreign pvc- prefix", "pvc-"+uuid.New()+"-pvc", "", false),
			Entry("a foreign prefix as long as the link's own", "vd-"+vmID+"-boot", "", false),
			Entry("a name too short to carry the id", "ci-boot", "", false),
		)
	})
})
