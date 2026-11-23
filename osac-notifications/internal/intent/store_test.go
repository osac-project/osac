/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package intent_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/osac-notifications/internal/cevent"
	"github.com/osac-project/osac/osac-notifications/internal/intent"
)

func sampleAccepted() *cevent.Accepted {
	return &cevent.Accepted{
		Tenant:   "acme",
		Source:   "osac-fulfillment",
		ID:       "evt-1",
		Kind:     "event",
		Type:     "osac.notification.event.resource_created",
		Severity: "info",
		Topic:    "osac.notifications.acme",
		Payload:  []byte(`{"id":"evt-1"}`),
	}
}

var _ = Describe("TestStore", func() {
	It("tracks created, pending, completed, and failed states", func() {
		store := intent.NewTestStore()
		ctx := context.Background()
		accepted := sampleAccepted()

		result, err := store.Accept(ctx, accepted)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(intent.Created))

		result, err = store.Accept(ctx, accepted)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(intent.Pending))

		Expect(store.MarkStatus(ctx, "acme", "osac-fulfillment", "evt-1", intent.StatusCompleted)).To(Succeed())
		Expect(store.StatusOf("acme", "osac-fulfillment", "evt-1")).To(Equal(intent.StatusCompleted))

		result, err = store.Accept(ctx, accepted)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(intent.Completed))
	})

	It("lists only pending rows and supports failed status", func() {
		store := intent.NewTestStore()
		ctx := context.Background()
		a := sampleAccepted()
		_, err := store.Accept(ctx, a)
		Expect(err).ToNot(HaveOccurred())

		b := sampleAccepted()
		b.ID = "evt-2"
		_, err = store.Accept(ctx, b)
		Expect(err).ToNot(HaveOccurred())
		Expect(store.MarkStatus(ctx, "acme", "osac-fulfillment", "evt-2", intent.StatusFailed)).To(Succeed())

		pending, err := store.ListPending(ctx, 10)
		Expect(err).ToNot(HaveOccurred())
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].ID).To(Equal("evt-1"))
		Expect(store.StatusOf("acme", "osac-fulfillment", "evt-2")).To(Equal(intent.StatusFailed))
	})
})
