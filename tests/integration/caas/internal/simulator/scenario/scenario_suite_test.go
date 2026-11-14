// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package scenario

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestScenario runs the Scenario unit specs with local fixtures.
func TestScenario(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Scenario Unit Suite")
}
