// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestSimulator runs the Simulator unit specs with local fixtures.
func TestSimulator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Simulator Unit Suite")
}
