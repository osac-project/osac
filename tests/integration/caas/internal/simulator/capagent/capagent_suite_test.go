// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package capagent

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestCAPAgent runs the CAP-Agent unit specs with local fixtures.
func TestCAPAgent(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CAP-Agent Unit Suite")
}
