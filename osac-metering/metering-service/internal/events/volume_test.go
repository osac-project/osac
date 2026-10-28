package events_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac-metering/internal/events"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestVolumeWatchLifecycle(t *testing.T) {
	creation := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	available := creation.Add(time.Minute)
	deletion := creation.Add(time.Hour)
	volume := &privatev1.Volume{
		Id: "volume-1",
		Metadata: &privatev1.Metadata{
			Tenant:            "tenant-1",
			Project:           "project-1",
			CreationTimestamp: timestamppb.New(creation),
		},
		Spec: &privatev1.VolumeSpec{StorageTier: "gold", SizeGib: 100},
		Status: &privatev1.VolumeStatus{
			State:               privatev1.VolumeState_VOLUME_STATE_CREATING,
			Protocol:            privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
			StateTransitionTime: timestamppb.New(creation),
		},
	}

	created := mapVolumeEvent(t, &privatev1.Event{
		Id:      "volume-created",
		Type:    privatev1.EventType_EVENT_TYPE_OBJECT_CREATED,
		Payload: &privatev1.Event_Volume{Volume: volume},
	})
	if created.Type() != events.EventCreated {
		t.Fatalf("created event type = %q", created.Type())
	}

	volume.Status.State = privatev1.VolumeState_VOLUME_STATE_AVAILABLE
	volume.Status.VendorVolumeId = "vendor-1"
	volume.Status.ProvisionedSizeGib = 100
	volume.Status.StateTransitionTime = timestamppb.New(available)
	started := mapVolumeEvent(t, &privatev1.Event{
		Id:      "volume-available",
		Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
		Payload: &privatev1.Event_Volume{Volume: volume},
	})
	if started.Type() != events.EventStarted || !started.Time().Equal(available) {
		t.Fatalf("started event = type %q time %s", started.Type(), started.Time())
	}

	volume.Metadata.DeletionTimestamp = timestamppb.New(deletion)
	volume.Status.State = privatev1.VolumeState_VOLUME_STATE_DELETING
	suspended := mapVolumeEvent(t, &privatev1.Event{
		Id:      "volume-deleting",
		Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
		Payload: &privatev1.Event_Volume{Volume: volume},
	})
	if suspended.Type() != events.EventSuspended || !suspended.Time().Equal(deletion) {
		t.Fatalf("suspended event = type %q time %s", suspended.Type(), suspended.Time())
	}
}

func TestVolumeDoesNotBillWithoutVendorIdentity(t *testing.T) {
	volume := &privatev1.Volume{
		Id:       "volume-no-id",
		Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
		Spec:     &privatev1.VolumeSpec{StorageTier: "gold", SizeGib: 1},
		Status: &privatev1.VolumeStatus{
			State:               privatev1.VolumeState_VOLUME_STATE_AVAILABLE,
			Protocol:            privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
			ProvisionedSizeGib:  1,
			StateTransitionTime: timestamppb.Now(),
		},
	}
	mapper, err := events.MapperForEvent(&privatev1.Event{Payload: &privatev1.Event_Volume{Volume: volume}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mapper.CloudEventType(privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, events.VolumeStateCreating)
	if !errors.Is(err, events.ErrDataQuality) {
		t.Fatalf("expected missing vendor identity to fail, got %v", err)
	}
}

func TestNFSVolumeDoesNotBill(t *testing.T) {
	volume := &privatev1.Volume{
		Id:       "volume-nfs",
		Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
		Spec:     &privatev1.VolumeSpec{StorageTier: "gold", SizeGib: 1},
		Status: &privatev1.VolumeStatus{
			State:              privatev1.VolumeState_VOLUME_STATE_AVAILABLE,
			VendorVolumeId:     "vendor-nfs",
			Protocol:           privatev1.StorageProtocol_STORAGE_PROTOCOL_NFS,
			ProvisionedSizeGib: 1,
		},
	}
	mapper, err := events.MapperForEvent(&privatev1.Event{Payload: &privatev1.Event_Volume{Volume: volume}})
	if err != nil {
		t.Fatal(err)
	}
	if mapper.IsBillable() {
		t.Fatal("NFS volume is billable")
	}
	_, err = mapper.CloudEventType(privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, events.VolumeStateCreating)
	if !errors.Is(err, events.ErrSkipTransition) {
		t.Fatalf("expected NFS volume to skip, got %v", err)
	}
}

func TestVolumeBillingDimensionsValidatesMapperOutput(t *testing.T) {
	volume := &privatev1.Volume{
		Id:       "volume-invalid-dimensions",
		Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
		Spec:     &privatev1.VolumeSpec{StorageTier: "gold", SizeGib: 1},
		Status: &privatev1.VolumeStatus{
			State:               privatev1.VolumeState_VOLUME_STATE_AVAILABLE,
			VendorVolumeId:      "vendor-1",
			Protocol:            privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
			ProvisionedSizeGib:  1,
			StateTransitionTime: timestamppb.Now(),
		},
	}
	volume.Metadata.Tenant = ""

	mapper, err := events.MapperForEvent(&privatev1.Event{Payload: &privatev1.Event_Volume{Volume: volume}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mapper.BillingDimensionsMap()
	if !errors.Is(err, events.ErrDataQuality) {
		t.Fatalf("expected data quality error from Volume mapper, got %v", err)
	}
}

func TestVolumeDeletedStateWinsOverDeletionTimestamp(t *testing.T) {
	volume := &privatev1.Volume{
		Id:       "volume-deleted",
		Metadata: &privatev1.Metadata{DeletionTimestamp: timestamppb.Now()},
		Status:   &privatev1.VolumeStatus{State: privatev1.VolumeState_VOLUME_STATE_DELETED},
	}
	if got := events.VolumeCurrentState(volume); got != events.VolumeStateDeleted {
		t.Fatalf("current state = %q, want %q", got, events.VolumeStateDeleted)
	}
}

func TestVolumeDeletedUpdateClosesAtDeletionTimestamp(t *testing.T) {
	deletionTime := time.Date(2026, 9, 16, 11, 0, 0, 0, time.UTC)
	stateTime := deletionTime.Add(time.Minute)
	volume := &privatev1.Volume{
		Id:       "volume-deleted-boundary",
		Metadata: &privatev1.Metadata{Tenant: "tenant-1", DeletionTimestamp: timestamppb.New(deletionTime)},
		Spec:     &privatev1.VolumeSpec{StorageTier: "gold", SizeGib: 1},
		Status: &privatev1.VolumeStatus{
			State:               privatev1.VolumeState_VOLUME_STATE_DELETED,
			VendorVolumeId:      "vendor-1",
			Protocol:            privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
			ProvisionedSizeGib:  1,
			StateTransitionTime: timestamppb.New(stateTime),
		},
	}
	event := &privatev1.Event{
		Id:      "volume-deleted-boundary",
		Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
		Payload: &privatev1.Event_Volume{Volume: volume},
	}
	mapper, err := events.MapperForEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	transitionTime, err := mapper.TransitionTime(event, events.VolumeStateAvailable)
	if err != nil {
		t.Fatal(err)
	}
	if !transitionTime.Equal(deletionTime) {
		t.Fatalf("transition time = %s, want deletion time %s", transitionTime, deletionTime)
	}
}

func TestVolumeAvailableToAvailableIsNotALifecycleBoundary(t *testing.T) {
	volume := &privatev1.Volume{
		Id:       "volume-same-state",
		Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
		Spec:     &privatev1.VolumeSpec{StorageTier: "gold", SizeGib: 1},
		Status: &privatev1.VolumeStatus{
			State:              privatev1.VolumeState_VOLUME_STATE_AVAILABLE,
			VendorVolumeId:     "vendor-1",
			Protocol:           privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
			ProvisionedSizeGib: 1,
		},
	}
	mapper, err := events.MapperForEvent(&privatev1.Event{Payload: &privatev1.Event_Volume{Volume: volume}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mapper.CloudEventType(privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED, events.VolumeStateAvailable)
	if !errors.Is(err, events.ErrSkipTransition) {
		t.Fatalf("expected same-state update to skip, got %v", err)
	}
}

func TestVolumeDeletingRequiresDeletionTimestamp(t *testing.T) {
	volume := &privatev1.Volume{
		Id:       "volume-deleting-without-timestamp",
		Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
		Status: &privatev1.VolumeStatus{
			State:               privatev1.VolumeState_VOLUME_STATE_DELETING,
			StateTransitionTime: timestamppb.Now(),
		},
	}
	event := &privatev1.Event{
		Id:      "volume-deleting-without-timestamp",
		Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
		Payload: &privatev1.Event_Volume{Volume: volume},
	}
	mapper, err := events.MapperForEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mapper.TransitionTime(event, events.VolumeStateAvailable)
	if !errors.Is(err, events.ErrDataQuality) {
		t.Fatalf("expected data quality error, got %v", err)
	}
}

func TestVolumeDeletedRequiresDeletionTimestamp(t *testing.T) {
	volume := &privatev1.Volume{
		Id:       "volume-deleted-without-timestamp",
		Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
		Status: &privatev1.VolumeStatus{
			State:               privatev1.VolumeState_VOLUME_STATE_DELETED,
			StateTransitionTime: timestamppb.Now(),
		},
	}
	event := &privatev1.Event{
		Id:      "volume-deleted-without-timestamp",
		Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
		Payload: &privatev1.Event_Volume{Volume: volume},
	}
	mapper, err := events.MapperForEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mapper.TransitionTime(event, events.VolumeStateAvailable)
	if !errors.Is(err, events.ErrDataQuality) {
		t.Fatalf("expected data quality error, got %v", err)
	}
}

func TestVolumeUsageContract(t *testing.T) {
	start := time.Date(2026, 9, 16, 10, 0, 0, 123456000, time.UTC)
	end := start.Add(90 * time.Second)
	volume := &privatev1.Volume{
		Id:       "volume-usage",
		Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
		Spec:     &privatev1.VolumeSpec{StorageTier: "gold", SizeGib: 12},
		Status: &privatev1.VolumeStatus{
			State:               privatev1.VolumeState_VOLUME_STATE_DELETING,
			Protocol:            privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
			VendorVolumeId:      "vendor-usage",
			ProvisionedSizeGib:  12,
			StateTransitionTime: timestamppb.New(end),
		},
	}
	volume.Metadata.DeletionTimestamp = timestamppb.New(end)
	event := &privatev1.Event{
		Id:      "volume-usage-close",
		Type:    privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
		Payload: &privatev1.Event_Volume{Volume: volume},
	}
	mapper, err := events.MapperForEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	dims, err := mapper.BillingDimensionsMap()
	if err != nil {
		t.Fatal(err)
	}
	ce, err := events.MapWatchEvent(event, mapper, &events.StateContext{
		PreviousState: events.VolumeStateAvailable,
		BillableSince: &start,
	}, dims)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(ce.Data(), &data); err != nil {
		t.Fatal(err)
	}
	usage := data["usage"].(map[string]any)
	if usage["semantics"] != "interval" || usage["unit"] != "gibibyte_second" || usage["precision"] != "microsecond" {
		t.Fatalf("usage metadata = %#v", usage)
	}
	if usage["from"] != "2026-09-16T10:00:00.123456Z" || usage["to"] != "2026-09-16T10:01:30.123456Z" {
		t.Fatalf("usage boundaries = %#v", usage)
	}
	if usage["quantity"] != "1080.000000" {
		t.Fatalf("usage quantity = %v", usage["quantity"])
	}
}

func TestVolumeResizeUsageUsesClosingSliceCapacity(t *testing.T) {
	start := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	volume := &privatev1.Volume{
		Id:       "volume-resize",
		Metadata: &privatev1.Metadata{Tenant: "tenant-1"},
		Spec:     &privatev1.VolumeSpec{StorageTier: "gold", SizeGib: 200},
		Status: &privatev1.VolumeStatus{
			State:              privatev1.VolumeState_VOLUME_STATE_AVAILABLE,
			Protocol:           privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK,
			VendorVolumeId:     "vendor-resize",
			ProvisionedSizeGib: 200,
		},
	}
	mapper, err := events.MapperForEvent(&privatev1.Event{Payload: &privatev1.Event_Volume{Volume: volume}})
	if err != nil {
		t.Fatal(err)
	}
	usage, err := mapper.Usage(events.EventUpdated, &start, end, map[string]any{
		"volume_id": "volume-resize", "tenant_id": "tenant-1", "project_id": "", "storage_tier": "gold", "size_gib": int64(100),
	})
	if err != nil {
		t.Fatal(err)
	}
	if usage.Quantity != "6000.000000" {
		t.Fatalf("resize close quantity = %q, want 6000.000000", usage.Quantity)
	}
}

func mapVolumeEvent(t *testing.T, event *privatev1.Event) *cloudevents.Event {
	t.Helper()
	mapper, err := events.MapperForEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	dims, err := mapper.BillingDimensionsMap()
	if err != nil {
		t.Fatal(err)
	}
	previous := ""
	billableSince := time.Time{}
	if event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED {
		previous = events.VolumeStateCreating
		if event.GetId() == "volume-deleting" {
			previous = events.VolumeStateAvailable
			billableSince = event.GetVolume().GetStatus().GetStateTransitionTime().AsTime().Add(-time.Minute)
		}
	}
	stateCtx := &events.StateContext{PreviousState: previous}
	if !billableSince.IsZero() {
		stateCtx.BillableSince = &billableSince
	}
	ce, err := events.MapWatchEvent(event, mapper, stateCtx, dims)
	if err != nil {
		t.Fatal(err)
	}
	return ce
}
