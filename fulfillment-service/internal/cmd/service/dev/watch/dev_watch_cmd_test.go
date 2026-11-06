/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/logging"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func TestWatch(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Developer event watch")
}

var _ = Describe("Event activity logging", func() {
	DescribeTable("Logs only event metadata with the default logger",
		func(event *publicv1.Event, payloadKind string) {
			var output bytes.Buffer
			logger, err := logging.NewLogger().SetOut(&output).Build()
			Expect(err).ToNot(HaveOccurred())
			runner := &runnerContext{logger: logger}
			runner.logEvent(context.Background(), event)
			var record map[string]any
			Expect(json.Unmarshal(output.Bytes(), &record)).To(Succeed())
			Expect(record).To(HaveLen(6))
			Expect(record).To(HaveKeyWithValue("msg", "Received event"))
			Expect(record).To(HaveKeyWithValue("level", "INFO"))
			Expect(record).To(HaveKey("time"))
			Expect(record).To(HaveKeyWithValue("event_id", event.GetId()))
			Expect(record).To(HaveKeyWithValue("event_type", event.GetType().String()))
			Expect(record).To(HaveKeyWithValue("payload_kind", payloadKind))
			for _, marker := range []string{
				"synthetic-user@example.invalid", "synthetic-customer",
				"synthetic-host.example.invalid", "synthetic-customer-status",
				"synthetic-resource", "synthetic-label-value",
			} {
				Expect(output.String()).ToNot(ContainSubstring(marker))
			}
		},
		Entry("User email and username", &publicv1.Event{
			Id: "event-user", Type: publicv1.EventType_EVENT_TYPE_OBJECT_CREATED,
			Payload: &publicv1.Event_User{User: &publicv1.User{
				Spec: &publicv1.UserSpec{Email: "synthetic-user@example.invalid", Username: "synthetic-customer"},
			}},
		}, "user"),
		Entry("FabricDomain hostnames and member status", &publicv1.Event{
			Id: "event-fabric", Type: publicv1.EventType_EVENT_TYPE_OBJECT_UPDATED,
			Payload: &publicv1.Event_FabricDomain{FabricDomain: &publicv1.FabricDomain{
				Spec: &publicv1.FabricDomainSpec{Servers: []string{"synthetic-host.example.invalid"}},
				Status: &publicv1.FabricDomainStatus{Members: []*publicv1.FabricDomainMemberStatus{{
					Server: "synthetic-host.example.invalid", Message: "synthetic-customer-status",
				}}},
			}},
		}, "fabric_domain"),
		Entry("Resource metadata", &publicv1.Event{
			Id: "event-project", Type: publicv1.EventType_EVENT_TYPE_OBJECT_DELETED,
			Payload: &publicv1.Event_Project{Project: &publicv1.Project{
				Id: "synthetic-resource", Metadata: &publicv1.Metadata{
					Name: "synthetic-customer", Labels: map[string]string{"customer": "synthetic-label-value"},
				},
			}},
		}, "project"),
		Entry("Nil event", nil, "none"),
		Entry("Empty event", &publicv1.Event{}, "none"),
		Entry("Absent payload", &publicv1.Event{Id: "event-empty"}, "none"),
		Entry("Nil nested payload", &publicv1.Event{Payload: &publicv1.Event_User{}}, "user"),
		Entry("Typed nil oneof", &publicv1.Event{Payload: (*publicv1.Event_User)(nil)}, "none"),
	)
})
