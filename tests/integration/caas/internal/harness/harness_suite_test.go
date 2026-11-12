// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package harness

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestHarness runs the Harness unit specs with local fixtures.
func TestHarness(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Harness Unit Suite")
}
