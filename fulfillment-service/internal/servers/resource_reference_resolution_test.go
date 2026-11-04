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
			tenant string,
			shared bool,
			project string,
			expectedTenant string,
			expectedProject string,
			expectConflict bool,
		) {
			result, err := selectedReferenceScope(owner, tenant, shared, project)
			if expectConflict {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).ToNot(HaveOccurred())
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
			ownerInNamedProject, "", false, "",
			"acme", "", false,
		),

		// Shared reference with empty project targets the shared
		// tenant's default project.
		Entry(
			"shared with empty project selects "+
				"shared default project",
			ownerInNamedProject, "", true, "",
			auth.SharedTenant, "", false,
		),

		// Explicit named project overrides the owner's project.
		Entry(
			"explicit project overrides owner project",
			ownerInNamedProject, "", false, "infra",
			"acme", "infra", false,
		),

		// Shared with explicit project selects that project in
		// the shared tenant.
		Entry(
			"shared with explicit project",
			ownerInNamedProject, "", true, "templates",
			auth.SharedTenant, "templates", false,
		),

		// Owner already in the default project — empty project
		// remains at the default project (no change).
		Entry(
			"empty project from default-project owner "+
				"stays at default",
			ownerInDefaultProject, "", false, "",
			"acme", "", false,
		),

		// Non-shared, owner's tenant is preserved.
		Entry(
			"non-shared keeps owner tenant",
			ownerInNamedProject, "", false, "other",
			"acme", "other", false,
		),
		Entry(
			"explicit tenant selects a visible tenant",
			ownerInNamedProject, "tenant-b", false, "catalog",
			"tenant-b", "catalog", false,
		),
		Entry(
			"explicit shared tenant selects shared",
			ownerInNamedProject, auth.SharedTenant, false, "catalog",
			auth.SharedTenant, "catalog", false,
		),
		Entry(
			"matching explicit shared selectors are accepted",
			ownerInNamedProject, auth.SharedTenant, true, "catalog",
			auth.SharedTenant, "catalog", false,
		),
		Entry(
			"conflicting explicit tenant and shared selectors are rejected",
			ownerInNamedProject, "tenant-b", true, "catalog",
			"", "", true,
		),
	)
})
