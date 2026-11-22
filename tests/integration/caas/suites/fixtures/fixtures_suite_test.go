// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package fixtures

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestFixtures runs shared fixture specs without deployed services.
func TestFixtures(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyPollingInterval(5 * time.Millisecond)
	RunSpecs(t, "CaaS Fixture Unit Suite")
}
