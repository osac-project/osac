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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

// The refusal decision is plain arithmetic over plain data, so these tables run in
// milliseconds with no database: the project chain, the subtree sums, the limit
// resolution order, the scope walk of the comparison, the update-mask coverage and the
// parent-id parser.

func TestQuotaProjectChain(t *testing.T) {
	tests := []struct {
		name    string
		project string
		chain   []string
	}{
		{name: "a nested project and each named ancestor", project: "team.web",
			chain: []string{"team.web", "team"}},
		{name: "a top-level project alone", project: "team", chain: []string{"team"}},
		{name: "a three-level chain", project: "a.b.c", chain: []string{"a.b.c", "a.b", "a"}},
		{name: "the default project has no chain of its own", project: "", chain: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.chain, quotaProjectChain(test.project))
		})
	}
}

func TestQuotaSubtreeUsage(t *testing.T) {
	usage := map[string]map[string]int64{
		"":         {"storage.gib": 1},
		"team":     {"storage.gib": 2},
		"team.web": {"storage.gib": 4, "compute.vcpus": 3},
		"teamx":    {"storage.gib": 8},
		"other":    {"storage.gib": 16},
	}
	tests := []struct {
		name  string
		scope string
		want  map[string]int64
	}{
		{name: "the project itself", scope: "team.web",
			want: map[string]int64{"storage.gib": 4, "compute.vcpus": 3}},
		{name: "the project and the projects inside it", scope: "team",
			want: map[string]int64{"storage.gib": 6, "compute.vcpus": 3}},
		{name: "a same-prefix project sits outside the subtree", scope: "teamx",
			want: map[string]int64{"storage.gib": 8}},
		{name: "the empty scope sums the whole tenant", scope: "",
			want: map[string]int64{"storage.gib": 31, "compute.vcpus": 3}},
		{name: "a scope nothing reports", scope: "missing", want: map[string]int64{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, quotaSubtreeUsage(usage, test.scope))
		})
	}
}

func TestQuotaLimitResolutionOrder(t *testing.T) {
	limits := &quotaLimits{
		defaults:  map[string]int64{"with.default": 3, "with.both": 3},
		overrides: map[string]int64{"with.override": 10, "with.both": 10},
		projects: map[string]map[string]int64{
			"team": {"with.override": 4},
		},
	}
	tests := []struct {
		name      string
		scope     string
		dimension string
		want      int64
	}{
		{name: "the project's own limit wins", scope: "team", dimension: "with.override", want: 4},
		{name: "the tenant's override replaces the default", scope: "", dimension: "with.both", want: 10},
		{name: "the override of a project that sets none", scope: "team", dimension: "with.both", want: 10},
		{name: "the default when the tenant sets none", scope: "", dimension: "with.default", want: 3},
		{name: "zero when nothing sets it", scope: "team", dimension: "unset", want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, limits.limitOf(test.scope, test.dimension))
		})
	}
}

func TestFirstQuotaRefusal(t *testing.T) {
	tests := []struct {
		name    string
		added   map[string]int64
		usage   map[string]map[string]int64
		limits  *quotaLimits
		project string
		want    *quotaRefusal
	}{
		{
			name:  "a request that fits every scope",
			added: map[string]int64{"storage.gib": 1},
			usage: map[string]map[string]int64{"team": {"storage.gib": 1}},
			limits: &quotaLimits{
				defaults: map[string]int64{"storage.gib": 10},
				projects: map[string]map[string]int64{"team": {"storage.gib": 5}},
			},
			project: "team",
			want:    nil,
		},
		{
			name:  "the request's project refuses first",
			added: map[string]int64{"storage.gib": 2},
			usage: map[string]map[string]int64{"team.web": {"storage.gib": 1}},
			limits: &quotaLimits{
				defaults: map[string]int64{"storage.gib": 1},
				projects: map[string]map[string]int64{"team.web": {"storage.gib": 2}},
			},
			project: "team.web",
			want: &quotaRefusal{
				scope: "project", project: "team.web", dimension: "storage.gib",
				limit: 2, usage: 1, added: 2,
			},
		},
		{
			name:  "an ancestor project refuses when the project fits",
			added: map[string]int64{"storage.gib": 2},
			usage: map[string]map[string]int64{"team.web": {"storage.gib": 1}},
			limits: &quotaLimits{
				projects: map[string]map[string]int64{
					"team.web": {"storage.gib": 3},
					"team":     {"storage.gib": 2},
				},
			},
			project: "team.web",
			want: &quotaRefusal{
				scope: "ancestor_project", project: "team", dimension: "storage.gib",
				limit: 2, usage: 1, added: 2,
			},
		},
		{
			name:  "the tenant refuses a request in the default project",
			added: map[string]int64{"storage.gib": 2},
			usage: map[string]map[string]int64{"": {"storage.gib": 1}},
			limits: &quotaLimits{
				defaults: map[string]int64{"storage.gib": 2},
			},
			project: "",
			want: &quotaRefusal{
				scope: "tenant", dimension: "storage.gib", limit: 2, usage: 1, added: 2,
			},
		},
		{
			name:  "the first scope in the walk wins over a crossing tenant",
			added: map[string]int64{"storage.gib": 2},
			usage: map[string]map[string]int64{"team": {"storage.gib": 1}},
			limits: &quotaLimits{
				defaults: map[string]int64{"storage.gib": 1},
				projects: map[string]map[string]int64{"team": {"storage.gib": 2}},
			},
			project: "team",
			want: &quotaRefusal{
				scope: "project", project: "team", dimension: "storage.gib",
				limit: 2, usage: 1, added: 2,
			},
		},
		{
			name:  "two crossed dimensions report the alphabetically first",
			added: map[string]int64{"compute.vcpus": 1, "storage.gib": 1},
			usage: map[string]map[string]int64{"": {"compute.vcpus": 0, "storage.gib": 0}},
			limits: &quotaLimits{
				defaults: map[string]int64{"compute.vcpus": 0, "storage.gib": 0},
			},
			project: "",
			want: &quotaRefusal{
				scope: "tenant", dimension: "compute.vcpus", limit: 0, usage: 0, added: 1,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, firstQuotaRefusal(test.added, test.usage, test.limits, test.project))
		})
	}
}

func TestQuotaPathCovers(t *testing.T) {
	tests := []struct {
		name      string
		maskPath  string
		annotated string
		covered   bool
	}{
		{name: "a mask of spec covers every spec field", maskPath: "spec",
			annotated: "spec.instance_type", covered: true},
		{name: "a mask of spec covers a deep spec field", maskPath: "spec",
			annotated: "spec.node_sets.workers.baremetal_instance_type.name", covered: true},
		{name: "the same field", maskPath: "spec.instance_type",
			annotated: "spec.instance_type", covered: true},
		{name: "a deeper mask path covers the annotated field", maskPath: "spec.instance_type.name",
			annotated: "spec.instance_type", covered: true},
		{name: "an annotated path deeper than the mask", maskPath: "spec.node_sets",
			annotated: "spec.node_sets.workers.size", covered: true},
		{name: "a sibling spec field", maskPath: "spec.run_strategy",
			annotated: "spec.instance_type", covered: false},
		{name: "a status path", maskPath: "status.instance_type",
			annotated: "spec.instance_type", covered: false},
		{name: "a metadata path", maskPath: "metadata.name", annotated: "spec", covered: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.covered, quotaPathCovers(test.maskPath, test.annotated))
		})
	}
}

func TestQuotaParentIDFromName(t *testing.T) {
	vmID := uuid.New()
	tests := []struct {
		name       string
		volumeName string
		wantID     string
		wantOK     bool
	}{
		{name: "a uuidv7 after the ci- prefix", volumeName: "ci-" + vmID + "-boot",
			wantID: vmID, wantOK: true},
		{name: "a short caller-chosen id", volumeName: "ci-shortvm-boot", wantOK: false},
		{name: "a foreign pvc- prefix", volumeName: "pvc-" + uuid.New() + "-pvc", wantOK: false},
		{name: "a name too short to carry the id", volumeName: "ci-boot", wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			id, ok := quotaParentIDFromName(test.volumeName, "ci-{parent}-")
			require.Equal(t, test.wantOK, ok)
			require.Equal(t, test.wantID, id)
		})
	}
}
