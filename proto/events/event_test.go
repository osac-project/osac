package events_test

import (
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// TestPublicEventPayloads verifies public payload identities and private resource exclusions.
func TestPublicEventPayloads(t *testing.T) {
	publicPayload := (&publicv1.Event{}).ProtoReflect().Descriptor().Oneofs().ByName("payload")
	privatePayload := (&privatev1.Event{}).ProtoReflect().Descriptor().Oneofs().ByName("payload")
	privateResources := map[protoreflect.Name]bool{
		"hub": true, "network_class": true, "storage_backend": true, "volume": true,
	}
	if publicPayload.Fields().Len() != privatePayload.Fields().Len()-len(privateResources) {
		t.Fatalf("public payload count = %d, want %d", publicPayload.Fields().Len(), privatePayload.Fields().Len()-len(privateResources))
	}
	for index := range privatePayload.Fields().Len() {
		privateField := privatePayload.Fields().Get(index)
		t.Run(string(privateField.Name()), func(t *testing.T) {
			publicField := publicPayload.Fields().ByName(privateField.Name())
			if privateResources[privateField.Name()] {
				if publicField != nil {
					t.Fatal("private resource is exposed in the public event")
				}
				return
			}
			if publicField == nil {
				t.Fatal("public resource is missing from the public event")
			}
			if publicField.Number() != privateField.Number() || publicField.Message().Name() != privateField.Message().Name() {
				t.Fatal("public payload number or resource type differs from the private event")
			}
			if publicField.Message().ParentFile().Package() != "osac.public.v1" {
				t.Fatal("public payload references a non-public resource type")
			}
		})
	}
}

// TestPublicExternalIPEventRoundTrip verifies public ExternalIP event JSON serialization.
func TestPublicExternalIPEventRoundTrip(t *testing.T) {
	event := &publicv1.Event{}
	err := protojson.Unmarshal([]byte(`{
		"id": "event-id",
		"type": "EVENT_TYPE_OBJECT_UPDATED",
		"externalIp": {
			"id": "external-ip-id",
			"status": {"state": "EXTERNAL_IP_STATE_ALLOCATED", "address": "192.0.2.10"}
		}
	}`), event)
	if err != nil {
		t.Fatalf("unmarshal public ExternalIP event: %v", err)
	}
	if event.GetExternalIp().GetId() != "external-ip-id" || event.GetExternalIp().GetStatus().GetAddress() != "192.0.2.10" {
		t.Fatal("ExternalIP payload was not retained")
	}
	data, err := protojson.Marshal(event)
	if err != nil {
		t.Fatalf("marshal public ExternalIP event: %v", err)
	}
	roundTrip := &publicv1.Event{}
	if err := protojson.Unmarshal(data, roundTrip); err != nil {
		t.Fatalf("unmarshal serialized public ExternalIP event: %v", err)
	}
	if !proto.Equal(event, roundTrip) {
		t.Fatal("public ExternalIP event changed during serialization")
	}
}

// TestPublicExternalIPEventKeepsPrivateStatusFieldsHidden checks nested status privacy.
func TestPublicExternalIPEventKeepsPrivateStatusFieldsHidden(t *testing.T) {
	publicFields := (&publicv1.ExternalIPStatus{}).ProtoReflect().Descriptor().Fields()
	privateFields := (&privatev1.ExternalIPStatus{}).ProtoReflect().Descriptor().Fields()
	for _, name := range []protoreflect.Name{"hub", "attribution", "attachment_transition_time", "state_transition_time"} {
		t.Run(string(name), func(t *testing.T) {
			if privateFields.ByName(name) == nil {
				t.Fatal("expected private status field does not exist")
			}
			if publicFields.ByName(name) != nil {
				t.Fatal("private status field is exposed")
			}
		})
	}
	if err := protojson.Unmarshal([]byte(`{"externalIp":{"status":{"hub":"private-hub"}}}`), &publicv1.Event{}); err == nil {
		t.Fatal("public event JSON accepts a private nested status field")
	}
}

// TestPublicEventKeepsSignalPrivate verifies the signal name stays private in descriptors and JSON.
func TestPublicEventKeepsSignalPrivate(t *testing.T) {
	name := protoreflect.Name("EVENT_TYPE_OBJECT_SIGNALED")
	if privatev1.EventType(0).Descriptor().Values().ByName(name) == nil {
		t.Fatal("private signal event type does not exist")
	}
	if publicv1.EventType(0).Descriptor().Values().ByName(name) != nil {
		t.Fatal("signal event type is exposed")
	}
	if err := protojson.Unmarshal([]byte(`{"type":"EVENT_TYPE_OBJECT_SIGNALED"}`), &publicv1.Event{}); err == nil {
		t.Fatal("public event JSON accepts the private signal event name")
	}
}
