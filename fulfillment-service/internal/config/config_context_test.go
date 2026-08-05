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
})
