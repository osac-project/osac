// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package assisted

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestAssisted runs the Assisted unit specs with local fixtures.
func TestAssisted(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Assisted Unit Suite")
}
