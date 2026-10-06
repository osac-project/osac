/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package config

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/packages"
)

var _ = Describe("Context", func() {
	It("Extracts settings from the context if previously added", func() {
		tmp, err := os.MkdirTemp("", "*.test")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(os.RemoveAll, tmp)
		settings, err := NewSettings().
			SetLogger(logger).
			SetDir(tmp).
			Build()
		Expect(err).ToNot(HaveOccurred())
		ctx := SettingsIntoContext(context.Background(), settings)
		extracted := SettingsFromContext(ctx)
		Expect(extracted).To(BeIdenticalTo(settings))
	})

	It("Panics if settings weren't added to the context", func() {
		ctx := context.Background()
		Expect(func() {
			SettingsFromContext(ctx)
		}).To(Panic())
	})

	It("Returns empty string when no tenant in context", func() {
		ctx := context.Background()
		Expect(TenantFromContext(ctx)).To(BeEmpty())
	})

	It("Returns the tenant stored by TenantIntoContext", func() {
		ctx := TenantIntoContext(context.Background(), "my-tenant")
		Expect(TenantFromContext(ctx)).To(Equal("my-tenant"))
	})

	Describe("PackageNames", func() {
		It("Returns only public package names when private is disabled", func() {
			tmp, err := os.MkdirTemp("", "*.test")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(os.RemoveAll, tmp)
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())

			names := settings.PackageNames()
			Expect(names).To(ConsistOf(packages.Public))
			Expect(names).To(ContainElement(packages.PublicV1))
			Expect(names).NotTo(ContainElement(packages.PrivateV1))
		})

		It("Returns both public and private package names when private is enabled", func() {
			tmp, err := os.MkdirTemp("", "*.test")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(os.RemoveAll, tmp)
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.SetPrivate(true)

			names := settings.PackageNames()
			Expect(names).To(ContainElement(packages.PublicV1))
			Expect(names).To(ContainElement(packages.PrivateV1))
			Expect(names).To(HaveLen(len(packages.Public) + len(packages.Private)))
		})
	})

	Describe("PackageNamesFromContext", func() {
		It("Returns public and private names when settings with private enabled are in context", func() {
			tmp, err := os.MkdirTemp("", "*.test")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(os.RemoveAll, tmp)
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())
			settings.SetPrivate(true)

			ctx := SettingsIntoContext(context.Background(), settings)
			names := PackageNamesFromContext(ctx)
			Expect(names).To(ContainElement(packages.PublicV1))
			Expect(names).To(ContainElement(packages.PrivateV1))
			Expect(names).To(HaveLen(len(packages.Public) + len(packages.Private)))
		})

		It("Returns only public names when settings with private disabled are in context", func() {
			tmp, err := os.MkdirTemp("", "*.test")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(os.RemoveAll, tmp)
			settings, err := NewSettings().
				SetLogger(logger).
				SetDir(tmp).
				Build()
			Expect(err).ToNot(HaveOccurred())

			ctx := SettingsIntoContext(context.Background(), settings)
			names := PackageNamesFromContext(ctx)
			Expect(names).To(ConsistOf(packages.Public))
			Expect(names).To(ContainElement(packages.PublicV1))
			Expect(names).NotTo(ContainElement(packages.PrivateV1))
		})

		It("Falls back to public packages when context has no settings", func() {
			ctx := context.Background()
			names := PackageNamesFromContext(ctx)
			Expect(names).To(ConsistOf(packages.Public))
			Expect(names).To(ContainElement(packages.PublicV1))
			Expect(names).NotTo(ContainElement(packages.PrivateV1))
		})

		It("Falls back to public packages when context is nil", func() {
			names := PackageNamesFromContext(nil) //nolint:staticcheck // intentionally testing nil context fallback
			Expect(names).To(ConsistOf(packages.Public))
			Expect(names).To(ContainElement(packages.PublicV1))
			Expect(names).NotTo(ContainElement(packages.PrivateV1))
		})
	})
})
