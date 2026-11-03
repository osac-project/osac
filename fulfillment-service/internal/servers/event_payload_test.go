/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Public event payloads", func() {
	publicPayload := (&publicv1.Event{}).ProtoReflect().Descriptor().Oneofs().ByName("payload")
	privatePayload := (&privatev1.Event{}).ProtoReflect().Descriptor().Oneofs().ByName("payload")
	privateResources := map[protoreflect.Name]bool{
		"hub": true, "network_class": true, "storage_backend": true, "volume": true,
	}
	It("Includes all public resources and excludes the four private resources", func() {
		Expect(publicPayload.Fields().Len()).To(Equal(privatePayload.Fields().Len() - len(privateResources)))
	})

	for index := range privatePayload.Fields().Len() {
		privateField := privatePayload.Fields().Get(index)
		It("Preserves the visibility and identity of "+string(privateField.Name()), func() {
			publicField := publicPayload.Fields().ByName(privateField.Name())
			if privateResources[privateField.Name()] {
				Expect(publicField).To(BeNil())
				return
			}
			Expect(publicField).NotTo(BeNil())
			Expect(publicField.Number()).To(Equal(privateField.Number()))
			Expect(publicField.Message().Name()).To(Equal(privateField.Message().Name()))
			Expect(publicField.Message().ParentFile().Package()).To(Equal(protoreflect.FullName("osac.public.v1")))
		})
	}
})

var _ = Describe("Public ExternalIP events", func() {
	It("Retains the payload through JSON serialization", func() {
		event := &publicv1.Event{}
		err := protojson.Unmarshal([]byte(`{
		"id": "event-id",
		"type": "EVENT_TYPE_OBJECT_UPDATED",
		"externalIp": {
			"id": "external-ip-id",
			"status": {"state": "EXTERNAL_IP_STATE_ALLOCATED", "address": "192.0.2.10"}
		}
	}`), event)
		Expect(err).NotTo(HaveOccurred())
		Expect(event.GetExternalIp().GetId()).To(Equal("external-ip-id"))
		Expect(event.GetExternalIp().GetStatus().GetAddress()).To(Equal("192.0.2.10"))
		data, err := protojson.Marshal(event)
		Expect(err).NotTo(HaveOccurred())
		roundTrip := &publicv1.Event{}
		Expect(protojson.Unmarshal(data, roundTrip)).To(Succeed())
		Expect(proto.Equal(event, roundTrip)).To(BeTrue())
	})

	publicFields := (&publicv1.ExternalIPStatus{}).ProtoReflect().Descriptor().Fields()
	privateFields := (&privatev1.ExternalIPStatus{}).ProtoReflect().Descriptor().Fields()
	for _, name := range []protoreflect.Name{"hub", "attribution", "attachment_transition_time", "state_transition_time"} {
		It("Keeps the private status field "+string(name)+" hidden", func() {
			Expect(privateFields.ByName(name)).NotTo(BeNil())
			Expect(publicFields.ByName(name)).To(BeNil())
		})
	}
	It("Rejects private nested status fields in JSON", func() {
		Expect(protojson.Unmarshal([]byte(`{"externalIp":{"status":{"hub":"private-hub"}}}`), &publicv1.Event{})).NotTo(Succeed())
	})
})

var _ = Describe("Private signal events", func() {
	It("Keeps the signal name private in descriptors and rejects it in public JSON", func() {
		name := protoreflect.Name("EVENT_TYPE_OBJECT_SIGNALED")
		Expect(privatev1.EventType(0).Descriptor().Values().ByName(name)).NotTo(BeNil())
		Expect(publicv1.EventType(0).Descriptor().Values().ByName(name)).To(BeNil())
		Expect(protojson.Unmarshal([]byte(`{"type":"EVENT_TYPE_OBJECT_SIGNALED"}`), &publicv1.Event{})).NotTo(Succeed())
	})
})
