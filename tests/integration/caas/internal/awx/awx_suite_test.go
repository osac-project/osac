// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package awx

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestAWX runs the AWX unit specs with local fixtures.
func TestAWX(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AWX Unit Suite")
}
