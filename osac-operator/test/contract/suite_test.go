// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package contract

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Operator Contract Suite")
}
