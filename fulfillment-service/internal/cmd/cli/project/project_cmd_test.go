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
			ctx := context.Background()
			settings := mustSettings()
			settings.SetProject("team-a")
			name := runner.resolveProjectName(ctx, settings, "frontend")
			Expect(name).To(Equal("team-a.frontend"))
		})

		It("Navigates up with '..' from a top-level project", func() {
			ctx := context.Background()
			settings := mustSettings()
			settings.SetProject("team-a")
			name := runner.resolveProjectName(ctx, settings, "..")
			Expect(name).To(BeEmpty())
		})

		It("Navigates up with '..' from a nested project", func() {
			ctx := context.Background()
			settings := mustSettings()
			settings.SetProject("team-a.frontend")
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
			ctx := context.Background()
			settings := mustSettings()
			settings.SetProject("team-a.frontend")
			name := runner.resolveProjectName(ctx, settings, "staging")
			Expect(name).To(Equal("team-a.frontend.staging"))
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
