// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cpo

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestCPO runs the CPO unit specs with local fixtures.
func TestCPO(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CPO Unit Suite")
}
