/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package envutil_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac-metering/adapters/envutil"
)

func TestEnvutil(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Envutil Suite")
}

var _ = Describe("RequireEnv", func() {
	It("returns the value when set", func() {
		GinkgoT().Setenv("TEST_REQUIRE_ENV_KEY", "hello")
		Expect(envutil.RequireEnv("TEST_REQUIRE_ENV_KEY")).To(Equal("hello"))
	})
})

var _ = Describe("EnvOrDefault", func() {
	It("returns the env value when set", func() {
		GinkgoT().Setenv("TEST_ENV_OR_DEFAULT_KEY", "custom")
		Expect(envutil.EnvOrDefault("TEST_ENV_OR_DEFAULT_KEY", "fallback")).To(Equal("custom"))
	})

	It("returns the fallback when unset", func() {
		Expect(envutil.EnvOrDefault("TEST_ENV_OR_DEFAULT_UNSET", "fallback")).To(Equal("fallback"))
	})

	It("returns the fallback when empty", func() {
		GinkgoT().Setenv("TEST_ENV_OR_DEFAULT_KEY", "")
		Expect(envutil.EnvOrDefault("TEST_ENV_OR_DEFAULT_KEY", "fallback")).To(Equal("fallback"))
	})
})

var _ = Describe("ReadFileOrFatal", func() {
	It("reads and trims file content", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "secret")
		Expect(os.WriteFile(path, []byte("  my-secret\n"), 0o600)).To(Succeed())
		Expect(envutil.ReadFileOrFatal(path)).To(Equal("my-secret"))
	})
})

var _ = Describe("SplitAndTrim", func() {
	It("splits and trims a comma-separated string", func() {
		Expect(envutil.SplitAndTrim(" a , b , c ", ",")).To(Equal([]string{"a", "b", "c"}))
	})

	It("drops empty segments", func() {
		Expect(envutil.SplitAndTrim("a,,b,", ",")).To(Equal([]string{"a", "b"}))
	})

	It("returns empty slice for whitespace-only input", func() {
		Expect(envutil.SplitAndTrim("  ,  , ", ",")).To(BeEmpty())
	})

	It("handles a single value", func() {
		Expect(envutil.SplitAndTrim("solo", ",")).To(Equal([]string{"solo"}))
	})
})
