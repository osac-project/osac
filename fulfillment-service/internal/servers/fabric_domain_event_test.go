/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"encoding/json"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// These boundary checks need neither a database nor Kafka: committed outbox rows
// must map to a typed private event before the reconciler can receive them.
func TestFabricDomainEventDispatch(t *testing.T) {
	g := NewWithT(t)
	publicStatusFields := (&publicv1.FabricDomainStatus{}).ProtoReflect().Descriptor().Fields()
	g.Expect(publicStatusFields.ByName("backend_id")).To(BeNil(), "Netris identifiers are private implementation details")
	g.Expect(publicStatusFields.ByName("vpc_id")).To(BeNil(), "Netris identifiers are private implementation details")
	privateStatusFields := (&privatev1.FabricDomainStatus{}).ProtoReflect().Descriptor().Fields()
	g.Expect(privateStatusFields.ByName("backend_id")).NotTo(BeNil())
	g.Expect(privateStatusFields.ByName("vpc_id")).NotTo(BeNil())
	builder := NewEventPublisher()
	oneof, err := builder.calculatePayloadOneof((&privatev1.Event{}).ProtoReflect().Descriptor())
	g.Expect(err).NotTo(HaveOccurred())
	fields, err := builder.calculatePayloadFields(oneof)
	g.Expect(err).NotTo(HaveOccurred())
	field := fields["fabric_domains"]
	g.Expect(field).NotTo(BeNil(), "FabricDomain changes must be publishable from the transactional outbox")
	g.Expect(string(field.Name())).To(Equal("fabric_domain"))
	g.Expect(int(field.Number())).To(Equal(38))
	g.Expect((&publicv1.Event{}).ProtoReflect().Descriptor().Fields().ByName("fabric_domain")).To(BeNil())
	publisher := &EventPublisher{payloadOneof: oneof, payloadFields: fields}
	timestamp := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

	for _, test := range []struct {
		op   string
		kind privatev1.EventType
	}{
		{eventPublisherOpInsert, privatev1.EventType_EVENT_TYPE_OBJECT_CREATED},
		{eventPublisherOpUpdate, privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED},
		{eventPublisherOpDelete, privatev1.EventType_EVENT_TYPE_OBJECT_DELETED},
		{eventPublisherOpSignal, privatev1.EventType_EVENT_TYPE_OBJECT_SIGNALED},
	} {
		t.Run(test.op, func(t *testing.T) {
			g := NewWithT(t)
			domain := privatev1.FabricDomain_builder{
				Spec: privatev1.FabricDomainSpec_builder{
					Type:    privatev1.FabricDomainType_FABRIC_DOMAIN_TYPE_ETHERNET_EW,
					Servers: []string{"server-a", "server-b"}, VirtualNetwork: "vn-id",
				}.Build(),
				Status: privatev1.FabricDomainStatus_builder{Hub: "hub-id"}.Build(),
			}.Build()
			data, err := protojson.Marshal(domain)
			g.Expect(err).NotTo(HaveOccurred())
			row := eventPublisherObjectJson{
				ID: "domain-id", Name: "fabric-a", Tenant: "tenant-a", Creator: "admin",
				Version: 3, CreationTimestamp: timestamp.Add(-time.Hour),
				Finalizers:  []string{"fulfillment-controller"},
				Annotations: map[string]string{"osac.openshift.io/owner-reference": "supplied-owner"},
				Data:        data,
			}
			if test.op == eventPublisherOpDelete {
				row.DeletionTimestamp = timestamp.Add(-time.Minute)
				row.Finalizers = nil
			}
			raw, err := json.Marshal(row)
			g.Expect(err).NotTo(HaveOccurred())
			event, err := publisher.convertChange(&eventPublisherChange{
				id: "change-id", table: "fabric_domains", op: test.op, data: raw, timestamp: timestamp,
			})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(event.GetId()).To(Equal("change-id"))
			g.Expect(event.GetType()).To(Equal(test.kind))
			g.Expect(event.GetTimestamp().AsTime()).To(Equal(timestamp))
			g.Expect(event.ProtoReflect().WhichOneof(oneof)).To(Equal(field))
			domain.SetId(row.ID)
			domain.SetMetadata(privatev1.Metadata_builder{
				Name: row.Name, Tenant: row.Tenant, Creator: row.Creator, Version: row.Version,
				CreationTimestamp: timestamppb.New(row.CreationTimestamp),
				Finalizers:        row.Finalizers, Annotations: row.Annotations,
			}.Build())
			if !row.DeletionTimestamp.IsZero() {
				domain.GetMetadata().SetDeletionTimestamp(timestamppb.New(row.DeletionTimestamp))
			}
			payload := event.ProtoReflect().Get(field).Message().Interface()
			g.Expect(proto.Equal(payload, domain)).To(BeTrue(), "event must retain spec, placement, and ownership metadata")
			tenant, id, err := publisher.payloadIdentity(event)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(tenant).To(Equal(row.Tenant))
			g.Expect(id).To(Equal(row.ID))
			wire, err := proto.Marshal(event)
			g.Expect(err).NotTo(HaveOccurred())
			received := &privatev1.Event{}
			g.Expect(proto.Unmarshal(wire, received)).To(Succeed())
			g.Expect(proto.Equal(received, event)).To(BeTrue())
		})
	}
}
