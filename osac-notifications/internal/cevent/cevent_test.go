/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cevent_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/osac-notifications/internal/cevent"
	"github.com/osac-project/osac/osac-notifications/internal/grant"
)

func validBody(overrides map[string]any) []byte {
	base := map[string]any{
		"specversion":            "1.0",
		"id":                     "fulfillment-ci-1",
		"source":                 "osac-fulfillment",
		"type":                   "osac.notification.event.resource_created",
		"time":                   "2026-11-07T18:05:00Z",
		"datacontenttype":        "application/json",
		"osactenant":             "acme",
		"kind":                   "event",
		"severity":               "info",
		"osacmatchsubscriptions": false,
		"data": map[string]any{
			"title": "Compute instance ready",
			"recipients": []map[string]any{
				{"type": "user", "id": "user-123"},
				{"type": "group", "id": "g1"},
				{"type": "user", "id": "user-123"},
				{"type": "USER", "id": "user-456"},
			},
		},
	}
	for k, v := range overrides {
		base[k] = v
	}
	body, err := json.Marshal(base)
	Expect(err).ToNot(HaveOccurred())
	return body
}

var _ = Describe("CloudEvent validation", func() {
	It("accepts a valid fulfillment event", func() {
		accepted, err := cevent.Validate("osac.notifications.acme", validBody(nil), grant.DefaultMap)
		Expect(err).ToNot(HaveOccurred())
		Expect(accepted.Tenant).To(Equal("acme"))
		Expect(accepted.Kind).To(Equal("event"))
		Expect(accepted.MatchSubs).To(BeFalse())
		Expect(cevent.UserRecipientIDs(accepted.Data.Recipients)).To(Equal([]string{"user-123", "user-456"}))
	})

	It("rejects announcement kind", func() {
		_, err := cevent.Validate("osac.notifications.acme", validBody(map[string]any{
			"kind": "announcement",
			"type": "osac.notification.announcement.maintenance",
		}), grant.DefaultMap)
		Expect(err).To(MatchError(ContainSubstring("announcement")))
	})

	It("rejects osactenant / topic mismatch", func() {
		_, err := cevent.Validate("osac.notifications.acme", validBody(map[string]any{
			"osactenant": "other",
		}), grant.DefaultMap)
		Expect(err).To(MatchError(ContainSubstring("does not match")))
	})

	It("rejects disallowed source/kind", func() {
		_, err := cevent.Validate("osac.notifications.acme", validBody(map[string]any{
			"source": "osac-metering",
			"kind":   "meter",
			"type":   "osac.notification.meter.usage",
		}), grant.DefaultMap)
		Expect(err).To(HaveOccurred())
	})

	It("rejects DLQ topics", func() {
		Expect(cevent.TopicMatches("osac.notifications.dlq.acme")).To(BeFalse())
		Expect(cevent.TopicMatches("osac.notifications.acme")).To(BeTrue())
	})

	It("rejects events with no USER recipients", func() {
		_, err := cevent.Validate("osac.notifications.acme", validBody(map[string]any{
			"data": map[string]any{
				"title": "Ready",
				"recipients": []map[string]any{
					{"type": "group", "id": "g1"},
				},
			},
		}), grant.DefaultMap)
		Expect(err).To(MatchError(ContainSubstring("at least one recipient")))
	})
})
