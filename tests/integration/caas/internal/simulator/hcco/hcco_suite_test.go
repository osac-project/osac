// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package hcco

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestHCCO runs the HCCO unit specs with local fixtures.
func TestHCCO(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HCCO Unit Suite")
}
