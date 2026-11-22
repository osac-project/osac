// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package kube

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestKube runs the Kubernetes helpers unit specs with local fixtures.
func TestKube(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kubernetes helpers Unit Suite")
}
