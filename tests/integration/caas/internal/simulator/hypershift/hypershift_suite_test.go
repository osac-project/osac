// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package hypershift

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestHyperShift runs the HyperShift unit specs with local fixtures.
func TestHyperShift(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HyperShift Unit Suite")
}
