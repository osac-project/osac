/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package project

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/config"
)

var _ = Describe("Project command", func() {
	It("Has the expected use string", func() {
		cmd := Cmd()
		Expect(cmd.Use).To(Equal("project [FLAG...] [NAME]"))
	})

	It("Has a --clear flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("clear")
		Expect(flag).ToNot(BeNil())
		Expect(flag.DefValue).To(Equal("false"))
	})

	It("Has an --output flag", func() {
		cmd := Cmd()
		flag := cmd.Flags().Lookup("output")
		Expect(flag).ToNot(BeNil())
		Expect(flag.Shorthand).To(Equal("o"))
	})

	It("Has no_matches and multiple_matches templates", func() {
		// The FindObject contract requires these template names.
		// Verify they can be read from the embedded filesystem.
		data, err := templatesFS.ReadFile("templates/no_matches.txt")
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("{{ .Ref }}"))

		data, err = templatesFS.ReadFile("templates/multiple_matches.txt")
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("{{ .Ref }}"))
		Expect(string(data)).To(ContainSubstring(".Matches"))
	})

	Describe("resolveProjectName", func() {
		var runner *runnerContext

		BeforeEach(func() {
			runner = &runnerContext{}
		})

		It("Returns absolute name when current project is empty", func() {
			ctx := context.Background()
			settings := mustSettings()
			name := runner.resolveProjectName(ctx, settings, "my-project")
			Expect(name).To(Equal("my-project"))
		})

		It("Appends relative name to current project", func() {
			ctx := config.ProjectIntoContext(context.Background(), "team-a")
			settings := mustSettings()
			name := runner.resolveProjectName(ctx, settings, "frontend")
			Expect(name).To(Equal("team-a.frontend"))
		})

		It("Navigates up with '..' from a top-level project", func() {
			ctx := config.ProjectIntoContext(context.Background(), "team-a")
			settings := mustSettings()
			name := runner.resolveProjectName(ctx, settings, "..")
			Expect(name).To(BeEmpty())
		})

		It("Navigates up with '..' from a nested project", func() {
			ctx := config.ProjectIntoContext(context.Background(), "team-a.frontend")
			settings := mustSettings()
			name := runner.resolveProjectName(ctx, settings, "..")
			Expect(name).To(Equal("team-a"))
		})

		It("Returns empty when '..' from no project", func() {
			ctx := context.Background()
			settings := mustSettings()
			name := runner.resolveProjectName(ctx, settings, "..")
			Expect(name).To(BeEmpty())
		})

		It("Builds a deeply nested name", func() {
			ctx := config.ProjectIntoContext(context.Background(), "team-a.frontend")
			settings := mustSettings()
			name := runner.resolveProjectName(ctx, settings, "staging")
			Expect(name).To(Equal("team-a.frontend.staging"))
		})

		DescribeTable("Navigation edge cases",
			func(current string, input string, expected string) {
				ctx := context.Background()
				if current != "" {
					ctx = config.ProjectIntoContext(ctx, current)
				}
				settings := mustSettings()
				name := runner.resolveProjectName(ctx, settings, input)
				Expect(name).To(Equal(expected))
			},
			Entry("Absolute name with no current project", "", "prod", "prod"),
			Entry("Relative name with single-level current", "org", "team", "org.team"),
			Entry("Relative name with multi-level current", "org.team", "env", "org.team.env"),
			Entry("'..' from deeply nested project", "a.b.c.d", "..", "a.b.c"),
			Entry("'..' from two-level project", "a.b", "..", "a"),
			Entry("'..' from single-level project", "a", "..", ""),
			Entry("'..' from empty project", "", "..", ""),
			Entry("Name containing dots is treated as single segment", "team", "a.b", "team.a.b"),
		)

		// Regression: when '..' resolves to "" from a top-level project, setProject
		// should clear the project rather than trying to find a project named "".
		It("Returns empty for '..' from single-level (setProject clears)", func() {
			ctx := config.ProjectIntoContext(context.Background(), "team-a")
			settings := mustSettings()
			name := runner.resolveProjectName(ctx, settings, "..")
			Expect(name).To(BeEmpty(),
				"'..' from a single-level project should resolve to empty, "+
					"signalling setProject to clear the saved project")
		})

		// Regression: when a project is saved, a UUID argument should not be
		// made relative. resolveProjectName does prepend the current project,
		// but setProject tries the resolved (child) name first, then falls back
		// to the raw input so that explicit IDs and absolute names still work.
		It("Prepends current project to a UUID-like name (setProject falls back to raw)", func() {
			ctx := config.ProjectIntoContext(context.Background(), "team-a")
			settings := mustSettings()
			name := runner.resolveProjectName(ctx, settings, "abc-123-def")
			// resolveProjectName always relativizes — setProject tries this
			// resolved name first, then falls back to the raw argument for
			// ID/absolute selection.
			Expect(name).To(Equal("team-a.abc-123-def"))
		})

		// Regression: bare name "dev" under effective project "team" should
		// resolve to "team.dev" (child) before a root project named "dev".
		// setProject uses this resolved name first, falling back to raw "dev"
		// only if the child does not exist — preventing root/child collisions.
		It("Resolves child before root when a project is active", func() {
			ctx := config.ProjectIntoContext(context.Background(), "team")
			settings := mustSettings()
			name := runner.resolveProjectName(ctx, settings, "dev")
			Expect(name).To(Equal("team.dev"),
				"bare 'dev' under effective project 'team' should resolve to 'team.dev'")
		})

		// Regression: when the effective project is "org" and the user types
		// "dev", the resolved name is "org.dev". If the server has both a root
		// "dev" and a child "org.dev", the child must win. This test verifies
		// resolveProjectName produces the right candidate for the child-first
		// lookup that setProject performs.
		It("Resolves relative name for child-first lookup precedence", func() {
			ctx := config.ProjectIntoContext(context.Background(), "org")
			settings := mustSettings()
			name := runner.resolveProjectName(ctx, settings, "dev")
			Expect(name).To(Equal("org.dev"),
				"setProject should try 'org.dev' first, then fall back to 'dev'")
		})

		// Regression: resolveProjectName must use the effective project from the
		// context (which accounts for --project flag override), not the saved
		// settings value. When --project org.team is passed but settings has
		// "team-a", navigation must resolve from "org.team".
		It("Uses effective project from context, not saved settings", func() {
			// Simulate --project org.team overriding saved "team-a":
			// root command puts the effective project into context.
			ctx := config.ProjectIntoContext(context.Background(), "org.team")
			settings := mustSettings()
			settings.SetProject("team-a") // saved value differs from effective

			name := runner.resolveProjectName(ctx, settings, "..")
			Expect(name).To(Equal("org"),
				"'..' should navigate from effective context project 'org.team', not saved 'team-a'")
		})

		It("Appends relative name using effective project from context", func() {
			ctx := config.ProjectIntoContext(context.Background(), "org.team")
			settings := mustSettings()
			settings.SetProject("team-a") // saved value differs

			name := runner.resolveProjectName(ctx, settings, "env")
			Expect(name).To(Equal("org.team.env"),
				"relative name should be appended to the effective context project")
		})
	})

	Describe("noOpRenderer", func() {
		It("Implements the reflection.Renderer interface", func() {
			var r noOpRenderer
			// Should not panic; discards all output silently.
			r.Render(context.Background(), "no_matches.txt", nil)
		})
	})
})

// mustSettings creates a minimal Settings object for testing.
func mustSettings() *config.Settings {
	tmp := GinkgoT().TempDir()
	settings, err := config.NewSettings().
		SetLogger(logger).
		SetDir(tmp).
		Build()
	Expect(err).ToNot(HaveOccurred())
	return settings
}
