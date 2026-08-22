/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Visibility", func() {
	Describe("Nil receiver", func() {
		It("Is not total", func() {
			var v *Visibility
			Expect(v.IsTotal()).To(BeFalse())
		})

		It("No project is visible", func() {
			var v *Visibility
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeFalse())
			Expect(v.IsProjectVisible("", "")).To(BeFalse())
		})
	})

	Describe("Pre-built sentinels", func() {
		It("ZeroVisibility is zero and not total", func() {
			v := ZeroVisibility()
			Expect(v.IsZero()).To(BeTrue())
			Expect(v.IsTotal()).To(BeFalse())
			Expect(v.IsTenantVisible("tenant-a")).To(BeFalse())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeFalse())
		})

		It("TotalVisibility is total and not zero", func() {
			v := TotalVisibility()
			Expect(v.IsTotal()).To(BeTrue())
			Expect(v.IsZero()).To(BeFalse())
			Expect(v.IsTenantVisible("tenant-a")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeTrue())
		})
	})

	Describe("Zero check", func() {
		It("Is zero if nil", func() {
			var v *Visibility
			Expect(v.IsZero()).To(BeTrue())
		})

		It("Is zero when newly created with no rules", func() {
			v, err := NewVisibility().Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsZero()).To(BeTrue())
		})

		It("Is not zero after adding a project", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsZero()).To(BeFalse())
		})

		It("Is not zero when total", func() {
			v, err := NewVisibility().
				SetTotal(true).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsZero()).To(BeFalse())
		})
	})

	Describe("Total check", func() {
		It("Is not total when newly created", func() {
			v, err := NewVisibility().Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsTotal()).To(BeFalse())
		})

		It("Is total after setting total", func() {
			v, err := NewVisibility().
				SetTotal(true).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsTotal()).To(BeTrue())
		})

		It("Previously added projects are no longer relevant after setting total", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				SetTotal(true).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsTotal()).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-b", "project-y")).To(BeTrue())
		})
	})

	Describe("Tenant visibility check", func() {
		It("Returns false for a nil visibility", func() {
			var v *Visibility
			Expect(v.IsTenantVisible("tenant-a")).To(BeFalse())
		})

		It("Returns false for a visibility with no access", func() {
			v, err := NewVisibility().Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsTenantVisible("tenant-a")).To(BeFalse())
		})

		It("Returns true for any tenant when total", func() {
			v, err := NewVisibility().
				SetTotal(true).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsTenantVisible("tenant-a")).To(BeTrue())
			Expect(v.IsTenantVisible("tenant-b")).To(BeTrue())
		})

		It("Returns true for a tenant that has been added", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsTenantVisible("tenant-a")).To(BeTrue())
		})

		It("Returns false for a tenant that has not been added", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsTenantVisible("tenant-b")).To(BeFalse())
		})
	})

	Describe("Project visibility check", func() {
		It("Returns false for a visibility with no access", func() {
			v, err := NewVisibility().Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeFalse())
		})

		It("Returns true for any project when total", func() {
			v, err := NewVisibility().
				SetTotal(true).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-b", "project-y")).To(BeTrue())
		})

		It("Returns true for a visible project", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeTrue())
		})

		It("Returns false for an unknown tenant", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-b", "project-x")).To(BeFalse())
		})

		It("Returns false for a non-visible project in a known tenant", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "project-y")).To(BeFalse())
		})

		It("Returns true for a descendant of a visible project", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "project-x.child")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "project-x.child.grandchild")).To(BeTrue())
		})

		It("Returns false for a project that is a prefix but not an ancestor", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "project-xyz")).To(BeFalse())
		})

		It("Returns false for the parent of a visible project", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x.child").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeFalse())
		})

		It("Does not grant visibility of other projects when the default project is visible", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeFalse())
			Expect(v.IsProjectVisible("tenant-a", "project-y")).To(BeFalse())
			Expect(v.IsProjectVisible("tenant-a", "project-x.child")).To(BeFalse())
		})

		It("Returns true for the root project itself when it is visible", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "")).To(BeTrue())
		})

		It("Does not grant visibility across tenants when root is added to one tenant", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-b", "project-x")).To(BeFalse())
		})

		It("Returns true for the default project when the tenant has only non-default projects", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "")).To(BeTrue())
		})

		It("Returns true for the default project when the tenant was added without projects", func() {
			v, err := NewVisibility().
				AddVisibleTenant("tenant-a").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "")).To(BeTrue())
		})

		It("Returns false for the default project when the tenant is not visible", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-b", "")).To(BeFalse())
		})
	})

	Describe("Get visible tenants", func() {
		It("Returns nil for a nil visibility", func() {
			var v *Visibility
			Expect(v.VisibleTenants()).To(BeNil())
		})

		It("Returns nil for a total visibility", func() {
			v, err := NewVisibility().
				SetTotal(true).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleTenants()).To(BeNil())
		})

		It("Returns empty slice for a visibility with no access", func() {
			v, err := NewVisibility().Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleTenants()).To(BeEmpty())
		})

		It("Returns the tenants that have been added", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				AddVisibleProject("tenant-b", "project-y").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleTenants()).To(ConsistOf("tenant-a", "tenant-b"))
		})

		It("Returns a single tenant when only one has been added", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				AddVisibleProject("tenant-a", "project-y").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleTenants()).To(ConsistOf("tenant-a"))
		})

		It("Returns tenants sorted alphabetically", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-c", "project-x").
				AddVisibleProject("tenant-a", "project-x").
				AddVisibleProject("tenant-b", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleTenants()).To(Equal([]string{"tenant-a", "tenant-b", "tenant-c"}))
		})
	})

	Describe("Get visible projects", func() {
		It("Returns nil for a nil visibility", func() {
			var v *Visibility
			Expect(v.VisibleProjects("tenant-a")).To(BeNil())
		})

		It("Returns nil for a total visibility", func() {
			v, err := NewVisibility().
				SetTotal(true).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleProjects("tenant-a")).To(BeNil())
		})

		It("Returns nil for a visibility with no access", func() {
			v, err := NewVisibility().Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleProjects("tenant-a")).To(BeNil())
		})

		It("Returns nil for an unknown tenant", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleProjects("tenant-b")).To(BeNil())
		})

		It("Returns the projects for a known tenant", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				AddVisibleProject("tenant-a", "project-y").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleProjects("tenant-a")).To(ConsistOf("", "project-x", "project-y"))
		})

		It("Returns projects sorted alphabetically", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-z").
				AddVisibleProject("tenant-a", "project-a").
				AddVisibleProject("tenant-a", "project-m").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleProjects("tenant-a")).To(Equal([]string{
				"", "project-a", "project-m", "project-z",
			}))
		})

		It("Always includes the default project even when not explicitly added", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleProjects("tenant-a")).To(ContainElement(""))
		})

		It("Does not duplicate the default project when it was explicitly added", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "").
				AddVisibleProject("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleProjects("tenant-a")).To(Equal([]string{"", "project-x"}))
		})

		It("Includes the default project when the tenant was added without projects", func() {
			v, err := NewVisibility().
				AddVisibleTenant("tenant-a").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleProjects("tenant-a")).To(Equal([]string{""}))
		})
	})

	Describe("Add visible tenants", func() {
		It("Makes the tenant visible", func() {
			v, err := NewVisibility().
				AddVisibleTenant("tenant-a").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsTenantVisible("tenant-a")).To(BeTrue())
		})

		It("Does not grant project visibility", func() {
			v, err := NewVisibility().
				AddVisibleTenant("tenant-a").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeFalse())
		})

		It("Does not duplicate a tenant already present", func() {
			v, err := NewVisibility().
				AddVisibleTenant("tenant-a").
				AddVisibleTenant("tenant-a").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleTenants()).To(Equal([]string{"tenant-a"}))
		})

		It("Adds multiple tenants at once", func() {
			v, err := NewVisibility().
				AddVisibleTenants("tenant-a", "tenant-b", "tenant-c").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleTenants()).To(Equal([]string{"tenant-a", "tenant-b", "tenant-c"}))
		})

		It("Returns an error when the tenant is empty", func() {
			v, err := NewVisibility().
				AddVisibleTenant("").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(v).To(BeNil())
		})
	})

	Describe("Add visible projects", func() {
		It("Adds multiple projects for the same tenant", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				AddVisibleProject("tenant-a", "project-y").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "project-y")).To(BeTrue())
		})

		It("Adds projects for multiple tenants", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				AddVisibleProject("tenant-b", "project-y").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-b", "project-y")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "project-y")).To(BeFalse())
			Expect(v.IsProjectVisible("tenant-b", "project-x")).To(BeFalse())
		})

		It("Removes duplicate projects during build", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				AddVisibleProject("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeTrue())
			Expect(v.VisibleProjects("tenant-a")).To(Equal([]string{"", "project-x"}))
		})

		It("Removes a descendant if ancestor is also present", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				AddVisibleProject("tenant-a", "project-x.child").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "project-x.child")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "project-x.other")).To(BeTrue())
			Expect(v.VisibleProjects("tenant-a")).To(Equal([]string{"", "project-x"}))
		})

		It("Removes sibling descendants of a retained ancestor", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "a").
				AddVisibleProject("tenant-a", "a.b").
				AddVisibleProject("tenant-a", "a.c").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "a")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "a.b")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "a.c")).To(BeTrue())
			Expect(v.VisibleProjects("tenant-a")).To(Equal([]string{"", "a"}))
		})

		It("Removes a descendant even when a lexicographically intervening name is present", func() {
			// After sort, "a-0" sits between "a" and "a.b" because '-' precedes '.'. Adjacent-only compact would
			// keep "a.b" even though "a" already covers it.
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "a").
				AddVisibleProject("tenant-a", "a-0").
				AddVisibleProject("tenant-a", "a.b").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "a")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "a-0")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "a.b")).To(BeTrue())
			Expect(v.VisibleProjects("tenant-a")).To(Equal([]string{"", "a", "a-0"}))
		})

		It("Adds multiple projects for a tenant in one call", func() {
			v, err := NewVisibility().
				AddVisibleProjects("tenant-a", "project-x", "project-y", "project-z").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "project-y")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "project-z")).To(BeTrue())
		})

		It("Adds a single project", func() {
			v, err := NewVisibility().
				AddVisibleProjects("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeTrue())
		})

		It("Creates the tenant if it does not exist", func() {
			v, err := NewVisibility().
				AddVisibleProjects("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsTenantVisible("tenant-a")).To(BeTrue())
		})

		It("Does not duplicate projects when called multiple times", func() {
			v, err := NewVisibility().
				AddVisibleProjects("tenant-a", "project-x").
				AddVisibleProjects("tenant-a", "project-x").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleProjects("tenant-a")).To(Equal([]string{"", "project-x"}))
		})

		It("Can be combined with adding a single project", func() {
			v, err := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				AddVisibleProjects("tenant-a", "project-y", "project-z").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.VisibleProjects("tenant-a")).To(Equal([]string{
				"", "project-x", "project-y", "project-z",
			}))
		})

		It("Adds projects for different tenants", func() {
			v, err := NewVisibility().
				AddVisibleProjects("tenant-a", "project-x", "project-y").
				AddVisibleProjects("tenant-b", "project-z").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.IsProjectVisible("tenant-a", "project-x")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "project-y")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-b", "project-z")).To(BeTrue())
			Expect(v.IsProjectVisible("tenant-a", "project-z")).To(BeFalse())
		})
	})

	Describe("Builder reuse", func() {
		It("Does not mutate a previous result when more projects are added after Build", func() {
			builder := NewVisibility().
				AddVisibleProject("tenant-a", "project-x").
				AddVisibleProject("tenant-a", "project-y")
			first, err := builder.Build()
			Expect(err).ToNot(HaveOccurred())

			builder.AddVisibleProject("tenant-a", "project-z")
			second, err := builder.Build()
			Expect(err).ToNot(HaveOccurred())

			Expect(first.VisibleProjects("tenant-a")).To(Equal([]string{"", "project-x", "project-y"}))
			Expect(first.IsProjectVisible("tenant-a", "project-z")).To(BeFalse())
			Expect(second.VisibleProjects("tenant-a")).To(Equal([]string{
				"", "project-x", "project-y", "project-z",
			}))
		})
	})
})
