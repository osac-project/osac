// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package host

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestHost runs the Host unit specs with local fixtures.
func TestHost(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Host Unit Suite")
}
