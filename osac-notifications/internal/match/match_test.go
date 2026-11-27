/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package match

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/osac-notifications/internal/fanout"
)

var _ = Describe("deliveryName", func() {
	It("builds a stable DNS-safe label for UUID-length ids", func() {
		subID := "019a1b2c-3d4e-7f80-9abc-def012345678"
		eventID := "quota-warn-acme-compute-2026-11-07T18:00:00Z"
		name, err := deliveryName(subID, eventID)
		Expect(err).ToNot(HaveOccurred())
		Expect(name).To(MatchRegexp(`^del-[0-9a-f]{32}$`))
		Expect(len(name)).To(BeNumerically("<=", 63))
		again, err := deliveryName(subID, eventID)
		Expect(err).ToNot(HaveOccurred())
		Expect(again).To(Equal(name))
	})

	It("returns non-retryable when ids are missing", func() {
		_, err := deliveryName("", "evt-1")
		Expect(err).To(HaveOccurred())
		Expect(fanout.Permanent(err)).To(BeTrue())
	})
})
