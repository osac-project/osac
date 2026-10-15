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

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
)

// Regression tests for OSAC-6381: global references with project==""
// must resolve to the tenant's default/root project, not the owner's
// project.
var _ = Describe("selectedReferenceScope", func() {
	ownerInNamedProject := referenceScope{
		tenant:  "acme",
		project: "apps",
	}

	ownerInDefaultProject := referenceScope{
		tenant:  "acme",
		project: "",
	}

	DescribeTable("selects the correct scope",
		func(
			owner referenceScope,
			shared bool,
			project string,
			expectedTenant string,
			expectedProject string,
		) {
			result := selectedReferenceScope(
				owner, shared, project,
			)
			Expect(result.tenant).To(
				Equal(expectedTenant),
				"tenant mismatch",
			)
			Expect(result.project).To(
				Equal(expectedProject),
				"project mismatch",
			)
		},

		// OSAC-6381 regression: empty project on a global reference
		// from a named-project owner must select the default project,
		// not inherit the owner's project.
		Entry(
			"empty project from named-project owner "+
				"selects default project",
			ownerInNamedProject, false, "",
			"acme", "",
		),

		// Shared reference with empty project targets the shared
		// tenant's default project.
		Entry(
			"shared with empty project selects "+
				"shared default project",
			ownerInNamedProject, true, "",
			auth.SharedTenant, "",
		),

		// Explicit named project overrides the owner's project.
		Entry(
			"explicit project overrides owner project",
			ownerInNamedProject, false, "infra",
			"acme", "infra",
		),

		// Shared with explicit project selects that project in
		// the shared tenant.
		Entry(
			"shared with explicit project",
			ownerInNamedProject, true, "templates",
			auth.SharedTenant, "templates",
		),

		// Owner already in the default project — empty project
		// remains at the default project (no change).
		Entry(
			"empty project from default-project owner "+
				"stays at default",
			ownerInDefaultProject, false, "",
			"acme", "",
		),

		// Non-shared, owner's tenant is preserved.
		Entry(
			"non-shared keeps owner tenant",
			ownerInNamedProject, false, "other",
			"acme", "other",
		),
	)
})
