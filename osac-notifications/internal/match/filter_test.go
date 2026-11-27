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

	"github.com/osac-project/osac/osac-notifications/internal/cevent"
)

var _ = Describe("EvalFilter", func() {
	It("matches kind and severity", func() {
		ok, err := EvalFilter(`kind == "event" && severity in ["warning", "critical"]`, &cevent.Accepted{
			Kind: "event", Severity: "warning", Type: "osac.notification.event.quota",
			Tenant: "acme", Data: cevent.Data{Title: "Quota"},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
	})

	It("rejects non-matching filters", func() {
		ok, err := EvalFilter(`severity == "critical"`, &cevent.Accepted{
			Kind: "event", Severity: "info", Type: "osac.notification.event.x",
			Tenant: "acme", Data: cevent.Data{Title: "t"},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeFalse())
	})

	It("returns error for invalid CEL", func() {
		_, err := EvalFilter(`kind ===`, &cevent.Accepted{Kind: "event"})
		Expect(err).To(HaveOccurred())
	})
})
