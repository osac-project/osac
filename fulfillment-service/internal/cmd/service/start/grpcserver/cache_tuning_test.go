/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package grpcserver

import (
	"bytes"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("resolveCatalogCacheTTLs", func() {
	var env map[string]string
	getenv := func(name string) string { return env[name] }
	quiet := slog.New(slog.DiscardHandler)

	BeforeEach(func() {
		env = map[string]string{}
	})

	It("uses the compiled-in shared default for both caches when nothing is set", func() {
		cacheTTLs := resolveCatalogCacheTTLs(getenv, quiet)
		Expect(cacheTTLs.inventory).To(Equal(300 * time.Second))
		Expect(cacheTTLs.rateCard).To(Equal(300 * time.Second))
	})

	It("applies the shared default to a cache without an override", func() {
		env[catalogCacheDefaultSecondsEnv] = "60"
		cacheTTLs := resolveCatalogCacheTTLs(getenv, quiet)
		Expect(cacheTTLs.inventory).To(Equal(time.Minute))
		Expect(cacheTTLs.rateCard).To(Equal(time.Minute))
	})

	It("prefers each cache's own override over the shared default", func() {
		env[catalogCacheDefaultSecondsEnv] = "600"
		env[catalogCacheInventorySecondsEnv] = "30"
		env[catalogCacheRateCardSecondsEnv] = "300"
		cacheTTLs := resolveCatalogCacheTTLs(getenv, quiet)
		Expect(cacheTTLs.inventory).To(Equal(30 * time.Second))
		Expect(cacheTTLs.rateCard).To(Equal(300 * time.Second))
	})

	DescribeTable("Falls back to the compiled-in default and warns when the shared default is unusable",
		func(value string) {
			var logged bytes.Buffer
			env[catalogCacheDefaultSecondsEnv] = value
			cacheTTLs := resolveCatalogCacheTTLs(getenv, slog.New(slog.NewTextHandler(&logged, nil)))
			Expect(cacheTTLs.inventory).To(Equal(300 * time.Second))
			Expect(cacheTTLs.rateCard).To(Equal(300 * time.Second))
			Expect(logged.String()).To(ContainSubstring(catalogCacheDefaultSecondsEnv))
			Expect(logged.String()).To(ContainSubstring("level=WARN"))
		},
		Entry("malformed", "five minutes"),
		Entry("zero", "0"),
		Entry("negative", "-1"),
		Entry("overflows the cache interval", "9223372037"),
	)

	DescribeTable("Treats an unusable override as unset and warns",
		func(variable string, value string) {
			var logged bytes.Buffer
			env[catalogCacheDefaultSecondsEnv] = "120"
			env[variable] = value
			cacheTTLs := resolveCatalogCacheTTLs(getenv, slog.New(slog.NewTextHandler(&logged, nil)))
			Expect(cacheTTLs.inventory).To(Equal(2 * time.Minute))
			Expect(cacheTTLs.rateCard).To(Equal(2 * time.Minute))
			Expect(logged.String()).To(ContainSubstring(variable))
			Expect(logged.String()).To(ContainSubstring("level=WARN"))
		},
		Entry("malformed inventory override", catalogCacheInventorySecondsEnv, "30s"),
		Entry("zero inventory override", catalogCacheInventorySecondsEnv, "0"),
		Entry("negative inventory override", catalogCacheInventorySecondsEnv, "-30"),
		Entry("malformed rate-card override", catalogCacheRateCardSecondsEnv, "300.5"),
		Entry("zero rate-card override", catalogCacheRateCardSecondsEnv, "0"),
		Entry("negative rate-card override", catalogCacheRateCardSecondsEnv, "-300"),
		Entry("overflowing rate-card override", catalogCacheRateCardSecondsEnv, "9223372037"),
	)
})
