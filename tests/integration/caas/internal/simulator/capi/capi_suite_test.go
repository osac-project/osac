// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capi

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestCAPI runs the CAPI unit specs with local fixtures.
func TestCAPI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CAPI Unit Suite")
}
