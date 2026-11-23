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
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
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
			project string,
			expectedTenant string,
			expectedProject string,
		) {
			result := selectedReferenceScope(owner, tenant, project)
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
			ownerInNamedProject, "", "",
			"acme", "",
		),

		// An explicit shared tenant targets the shared tenant's default project.
		Entry(
			"shared with empty project selects "+
				"shared default project",
			ownerInNamedProject, auth.SharedTenant, "",
			auth.SharedTenant, "",
		),

		// Explicit named project overrides the owner's project.
		Entry(
			"explicit project overrides owner project",
			ownerInNamedProject, "", "infra",
			"acme", "infra",
		),

		// An explicit shared tenant with a project selects that project.
		Entry(
			"shared with explicit project",
			ownerInNamedProject, auth.SharedTenant, "templates",
			auth.SharedTenant, "templates",
		),

		// Owner already in the default project — empty project
		// remains at the default project (no change).
		Entry(
			"empty project from default-project owner "+
				"stays at default",
			ownerInDefaultProject, "", "",
			"acme", "",
		),

		// An omitted tenant keeps the owner tenant.
		Entry(
			"non-shared keeps owner tenant",
			ownerInNamedProject, "", "other",
			"acme", "other",
		),
		Entry(
			"explicit tenant selects a visible tenant",
			ownerInNamedProject, "tenant-b", "catalog",
			"tenant-b", "catalog",
		),
		Entry(
			"explicit shared tenant selects shared",
			ownerInNamedProject, auth.SharedTenant, "catalog",
			auth.SharedTenant, "catalog",
		),
	)
})

var _ = Describe("inheritReferenceScope", func() {
	owner := privatev1.Metadata_builder{
		Tenant:  "acme",
		Project: "apps",
	}.Build()

	It("inherits the owner tenant and project for a name-based default", func() {
		reference := privatev1.DiskImageReference_builder{Name: "default-image"}.Build()

		inheritReferenceScope(reference, owner)

		Expect(reference.GetTenant()).To(Equal("acme"))
		Expect(reference.GetProject()).To(Equal("apps"))
		Expect(reference.GetTenant()).ToNot(Equal("shared"))
	})

	It("keeps an ID-based default tenantless and rejects a target outside the owner tenant", func() {
		reference := privatev1.DiskImageReference_builder{Id: "shared-image-id"}.Build()

		inheritReferenceScope(reference, owner)

		Expect(reference.GetTenant()).To(BeEmpty())
		Expect(reference.GetTenant()).ToNot(Equal("shared"))
		Expect(reference.GetProject()).To(BeEmpty())
		Expect(validateDependencyOwnerScope(
			referenceScope{tenant: owner.GetTenant(), project: owner.GetProject()},
			privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
			reference.GetTenant(), "disk image", "",
		)).To(MatchError(ContainSubstring("must belong to the owning tenant")))
	})

	It("preserves an explicit reference scope", func() {
		reference := privatev1.DiskImageReference_builder{
			Id: "shared-image-id", Tenant: auth.SharedTenant, Project: "templates",
		}.Build()

		inheritReferenceScope(reference, owner)

		Expect(reference.GetTenant()).To(Equal(auth.SharedTenant))
		Expect(reference.GetProject()).To(Equal("templates"))
	})
})
