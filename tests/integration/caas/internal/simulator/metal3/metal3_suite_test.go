// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package metal3

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestMetal3 runs the Metal3 unit specs with local fixtures.
func TestMetal3(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Metal3 Unit Suite")
}
