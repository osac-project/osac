// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package ovn

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestOVN runs unit checks for the Kind OVN status fixture.
func TestOVN(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kind OVN fixture")
}
