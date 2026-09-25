/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package install

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

// This is a schema self-check for data/prerequisites.yaml: a malformed
// entry (unknown kind, missing kind-specific field, typo'd requiredFor
// value) must fail here, in CI, rather than silently produce zero checks
// or panic the first time someone runs `osac install discover` for real.
var _ = Describe("the embedded prerequisites matrix", func() {
	It("parses, and every entry builds a valid Check", func() {
		entries, err := loadMatrix()
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).NotTo(BeEmpty())

		seenIDs := map[string]bool{}
		for _, entry := range entries {
			Expect(entry.ID).NotTo(BeEmpty())
			Expect(seenIDs[entry.ID]).To(BeFalse(), "duplicate id %q", entry.ID)
			seenIDs[entry.ID] = true

			_, err := parseSeverity(entry.Severity)
			Expect(err).NotTo(HaveOccurred(), "entry %q has an invalid severity %q", entry.ID, entry.Severity)

			_, err = parseCategory(entry.Category)
			Expect(err).NotTo(HaveOccurred(), "entry %q has an invalid category %q", entry.ID, entry.Category)

			Expect(entry.RequiredFor).NotTo(BeEmpty(), "entry %q has no requiredFor", entry.ID)
			for _, service := range entry.RequiredFor {
				Expect(knownRequiredFor[service]).To(BeTrue(), "entry %q: unknown requiredFor value %q", entry.ID, service)
			}
			Expect(knownToggles[entry.Toggle]).To(BeTrue(), "entry %q: unknown toggle %q", entry.ID, entry.Toggle)

			build, ok := engines[entry.Kind]
			Expect(ok).To(BeTrue(), "entry %q: unknown kind %q", entry.ID, entry.Kind)
			_, err = build(entry)
			Expect(err).NotTo(HaveOccurred(), "entry %q failed to build", entry.ID)
		}
	})
})
