package events

import (
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/osac-project/osac-metering/schema"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const VolumeStatePrefix = "VOLUME_STATE_"

const (
	VolumeStateUnspecified = "UNSPECIFIED"
	VolumeStateCreating    = "CREATING"
	VolumeStateAvailable   = "AVAILABLE"
	VolumeStateFailed      = "FAILED"
	VolumeStateDeleting    = "DELETING"
	VolumeStateDeleted     = "DELETED"
)

var volumeStates = map[string]struct{}{
	VolumeStateUnspecified: {},
	VolumeStateCreating:    {},
	VolumeStateAvailable:   {},
	VolumeStateFailed:      {},
	VolumeStateDeleting:    {},
	VolumeStateDeleted:     {},
}

var volumeCommittedCapacityStates = map[string]struct{}{
	VolumeStateAvailable: {},
	VolumeStateDeleting:  {},
	VolumeStateDeleted:   {},
}

// Volume transitions are intentionally exhaustive for every state that may be
// observed after creation. Missing entries are invalid transitions.
var volumeTransitions = TransitionTable{
	{StateEmpty, VolumeStateUnspecified}: {Skip: true},
	{StateEmpty, VolumeStateCreating}:    {Skip: true},
	{StateEmpty, VolumeStateAvailable}:   {EventType: eventBillableStart},
	{StateEmpty, VolumeStateFailed}:      {Skip: true},
	{StateEmpty, VolumeStateDeleting}:    {Skip: true},
	{StateEmpty, VolumeStateDeleted}:     {Skip: true},

	{VolumeStateUnspecified, VolumeStateUnspecified}: {Skip: true},
	{VolumeStateUnspecified, VolumeStateCreating}:    {Skip: true},
	{VolumeStateUnspecified, VolumeStateAvailable}:   {EventType: eventBillableStart},
	{VolumeStateUnspecified, VolumeStateFailed}:      {Skip: true},
	{VolumeStateUnspecified, VolumeStateDeleting}:    {Skip: true},
	{VolumeStateUnspecified, VolumeStateDeleted}:     {Skip: true},

	{VolumeStateCreating, VolumeStateCreating}:  {Skip: true},
	{VolumeStateCreating, VolumeStateAvailable}: {EventType: eventBillableStart},
	{VolumeStateCreating, VolumeStateFailed}:    {Skip: true},
	{VolumeStateCreating, VolumeStateDeleting}:  {Skip: true},
	{VolumeStateCreating, VolumeStateDeleted}:   {Skip: true},

	{VolumeStateAvailable, VolumeStateAvailable}: {Skip: true},
	{VolumeStateAvailable, VolumeStateFailed}:    {EventType: EventSuspended},
	{VolumeStateAvailable, VolumeStateDeleting}:  {EventType: EventSuspended},
	{VolumeStateAvailable, VolumeStateDeleted}:   {EventType: EventSuspended},

	{VolumeStateFailed, VolumeStateFailed}:   {Skip: true},
	{VolumeStateFailed, VolumeStateDeleting}: {Skip: true},
	{VolumeStateFailed, VolumeStateDeleted}:  {Skip: true},

	{VolumeStateDeleting, VolumeStateDeleting}: {Skip: true},
	{VolumeStateDeleting, VolumeStateDeleted}:  {Skip: true},

	{VolumeStateDeleted, VolumeStateDeleted}: {Skip: true},
}

type volumeMapper struct {
	volume *privatev1.Volume
}

func (m *volumeMapper) ResourceType() string { return schema.ResourceTypeVolume }
func (m *volumeMapper) ResourceID() string   { return m.volume.GetId() }

func (m *volumeMapper) TenantID() string {
	return m.volume.GetMetadata().GetTenant()
}

func (m *volumeMapper) ProjectID() *string {
	return NilIfEmpty(m.volume.GetMetadata().GetProject())
}

func (m *volumeMapper) CatalogItemID() *string { return nil }
func (m *volumeMapper) TemplateID() *string    { return nil }

func (m *volumeMapper) CurrentState() string {
	return VolumeCurrentState(m.volume)
}

func VolumeCurrentState(volume *privatev1.Volume) string {
	state := volume.GetStatus().GetState()
	if state == privatev1.VolumeState_VOLUME_STATE_DELETED {
		return VolumeStateDeleted
	}
	if volume.GetMetadata().GetDeletionTimestamp() != nil {
		return VolumeStateDeleting
	}
	return strings.TrimPrefix(state.String(), VolumeStatePrefix)
}

func IsVolumeResourceType(resourceType string) bool {
	return resourceType == schema.ResourceTypeVolume
}

func (m *volumeMapper) FulfillmentVersion() int32 {
	return m.volume.GetMetadata().GetVersion()
}

func (m *volumeMapper) IsBillable() bool {
	return IsVolumeBillableState(m.CurrentState()) && volumeHasVendorIdentity(m.volume)
}

func volumeHasVendorIdentity(volume *privatev1.Volume) bool {
	return volumeUsesBlockProtocol(volume) && volume.GetStatus().GetVendorVolumeId() != ""
}

func volumeUsesBlockProtocol(volume *privatev1.Volume) bool {
	return volume.GetStatus().GetProtocol() == privatev1.StorageProtocol_STORAGE_PROTOCOL_BLOCK
}

func (m *volumeMapper) BillingDimensionsMap() (map[string]any, error) {
	return VolumeBillingDimensions(m.volume)
}

func (m *volumeMapper) Usage(eventType string, billableSince *time.Time, transitionTime time.Time, dimensions map[string]any) (*schema.Usage, error) {
	sizeGiB, err := VolumeSizeGiB(dimensions)
	if err != nil {
		return nil, err
	}
	return VolumeLifecycleUsage(m.volume, eventType, billableSince, transitionTime, sizeGiB)
}

func VolumeBillingDimensions(volume *privatev1.Volume) (map[string]any, error) {
	state := VolumeCurrentState(volume)
	if _, ok := volumeStates[state]; !ok {
		return nil, fmt.Errorf("%w: volume %s has unknown state %q", ErrDataQuality, volume.GetId(), state)
	}
	sizeGiB := volume.GetSpec().GetSizeGib()
	if _, committed := volumeCommittedCapacityStates[state]; committed && volumeHasVendorIdentity(volume) {
		sizeGiB = volume.GetStatus().GetProvisionedSizeGib()
	}
	dimensions := map[string]any{
		"volume_id":    volume.GetId(),
		"tenant_id":    volume.GetMetadata().GetTenant(),
		"project_id":   volume.GetMetadata().GetProject(),
		"storage_tier": volume.GetSpec().GetStorageTier(),
		"size_gib":     sizeGiB,
	}
	return dimensions, validateVolumeBillingDimensions(dimensions)
}

func validateVolumeBillingDimensions(dimensions map[string]any) error {
	for _, key := range []string{"volume_id", "storage_tier", "size_gib", "tenant_id", "project_id"} {
		value, ok := dimensions[key]
		if !ok {
			return fmt.Errorf("%w: resource type %s is missing billing dimension %q", ErrDataQuality, schema.ResourceTypeVolume, key)
		}
		if key == "size_gib" {
			size, ok := toFloat64(value)
			if !ok || size <= 0 {
				return fmt.Errorf("%w: resource type %s has invalid billing dimension %q", ErrDataQuality, schema.ResourceTypeVolume, key)
			}
			continue
		}
		valueString, ok := value.(string)
		if !ok || (valueString == "" && key != "project_id") {
			return fmt.Errorf("%w: resource type %s has invalid billing dimension %q", ErrDataQuality, schema.ResourceTypeVolume, key)
		}
	}
	return nil
}

func IsVolumeBillableState(state string) bool { return state == VolumeStateAvailable }

func IsVolumeBillable(volume *privatev1.Volume) bool {
	return IsVolumeBillableState(VolumeCurrentState(volume)) && volumeHasVendorIdentity(volume)
}

func IsVolumeTransientState(string) bool { return false }

func (m *volumeMapper) CloudEventType(eventType privatev1.EventType, previousState string) (string, error) {
	if eventType == privatev1.EventType_EVENT_TYPE_OBJECT_CREATED && m.CurrentState() != VolumeStateCreating {
		return "", fmt.Errorf("volume OBJECT_CREATED must be CREATING, got %s", m.CurrentState())
	}
	result, err := ResolveCloudEventType(volumeTransitions, eventType, previousState, m.CurrentState())
	if err != nil {
		return "", err
	}
	if eventType == privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED &&
		(result == eventBillableStart || result == EventSuspended) &&
		!volumeHasVendorIdentity(m.volume) {
		if !volumeUsesBlockProtocol(m.volume) {
			return "", ErrSkipTransition
		}
		return "", fmt.Errorf("%w: volume %s has no vendor volume ID at billing boundary", ErrDataQuality, m.ResourceID())
	}
	return result, nil
}

func (m *volumeMapper) TransitionTime(event *privatev1.Event, previousState string) (time.Time, error) {
	if event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED &&
		previousState == VolumeStateAvailable && m.CurrentState() == VolumeStateAvailable {
		return VolumeCapacityTransitionTime(event)
	}
	return VolumeTransitionTime(m.volume, event)
}

func VolumeCapacityTransitionTime(event *privatev1.Event) (time.Time, error) {
	if event.GetTimestamp() == nil {
		return time.Time{}, fmt.Errorf("%w: volume event %s has no capacity transition timestamp", ErrDataQuality, event.GetId())
	}
	return event.GetTimestamp().AsTime(), nil
}

// VolumeLifecycleUsage builds the exact usage record for a billing boundary.
// A start has a zero interval; a close requires the projection's authoritative
// BillableSince timestamp and never substitutes the current time.
func VolumeLifecycleUsage(volume *privatev1.Volume, eventType string, billableSince *time.Time, transitionTime time.Time, sizeGiB int64) (*schema.Usage, error) {
	transitionTime = transitionTime.UTC().Truncate(time.Microsecond)
	if eventType == EventStarted || eventType == EventResumed {
		return &schema.Usage{
			Semantics: schema.UsageSemanticsInterval,
			From:      transitionTime.Format(time.RFC3339Nano),
			To:        transitionTime.Format(time.RFC3339Nano),
			Quantity:  "0.000000",
			Unit:      schema.UsageUnitGiByteSecond,
			Precision: schema.UsagePrecisionMicrosecond,
		}, nil
	}
	if eventType != EventSuspended && eventType != EventUpdated {
		return nil, nil
	}
	if billableSince == nil {
		return nil, fmt.Errorf("%w: volume %s has no billable start for %s", ErrDataQuality, volume.GetId(), eventType)
	}
	from := billableSince.UTC().Truncate(time.Microsecond)
	if transitionTime.Before(from) {
		return nil, fmt.Errorf("%w: volume %s usage boundary %s precedes billable start %s", ErrDataQuality, volume.GetId(), transitionTime, from)
	}
	return &schema.Usage{
		Semantics: schema.UsageSemanticsInterval,
		From:      from.Format(time.RFC3339Nano),
		To:        transitionTime.Format(time.RFC3339Nano),
		Quantity:  fixedGiByteSeconds(sizeGiB, transitionTime.Sub(from)),
		Unit:      schema.UsageUnitGiByteSecond,
		Precision: schema.UsagePrecisionMicrosecond,
	}, nil
}

// VolumeHeartbeatUsage reports the complete current interval, not an
// incremental slice. Providers replace a prior cumulative heartbeat.
func VolumeHeartbeatUsage(volumeID string, sizeGiB int64, billableSince *time.Time, now time.Time) (*schema.Usage, error) {
	if billableSince == nil {
		return nil, fmt.Errorf("%w: volume %s has no billable start for heartbeat", ErrDataQuality, volumeID)
	}
	from := billableSince.UTC().Truncate(time.Microsecond)
	to := now.UTC().Truncate(time.Microsecond)
	if to.Before(from) {
		return nil, fmt.Errorf("%w: volume %s heartbeat boundary %s precedes billable start %s", ErrDataQuality, volumeID, to, from)
	}
	return &schema.Usage{
		Semantics: schema.UsageSemanticsCumulative,
		From:      from.Format(time.RFC3339Nano),
		To:        to.Format(time.RFC3339Nano),
		Quantity:  fixedGiByteSeconds(sizeGiB, to.Sub(from)),
		Unit:      schema.UsageUnitGiByteSecond,
		Precision: schema.UsagePrecisionMicrosecond,
	}, nil
}

func VolumeSizeGiB(dimensions map[string]any) (int64, error) {
	value, ok := dimensions["size_gib"]
	if !ok {
		return 0, fmt.Errorf("%w: volume is missing size_gib", ErrDataQuality)
	}
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case float64:
		if typed != float64(int64(typed)) {
			return 0, fmt.Errorf("%w: volume size_gib is fractional", ErrDataQuality)
		}
		return int64(typed), nil
	default:
		return 0, fmt.Errorf("%w: volume size_gib has unsupported type %T", ErrDataQuality, value)
	}
}

func fixedGiByteSeconds(sizeGiB int64, duration time.Duration) string {
	quantity := new(big.Int).Mul(big.NewInt(sizeGiB), big.NewInt(duration.Microseconds()))
	whole := new(big.Int).Quo(quantity, big.NewInt(1_000_000))
	fraction := new(big.Int).Mod(quantity, big.NewInt(1_000_000))
	return fmt.Sprintf("%s.%06s", whole.String(), fraction.String())
}

func VolumeTransitionTime(volume *privatev1.Volume, event *privatev1.Event) (time.Time, error) {
	if event.GetType() == privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED {
		deletionTimestamp := volume.GetMetadata().GetDeletionTimestamp()
		if deletionTimestamp != nil {
			return deletionTimestamp.AsTime(), nil
		}
		state := VolumeCurrentState(volume)
		if state == VolumeStateDeleting || state == VolumeStateDeleted {
			return time.Time{}, fmt.Errorf("%w: volume %s is %s without deletion_timestamp", ErrDataQuality, volume.GetId(), state)
		}
	}
	return ResolveTransitionTime(
		event.GetType(),
		event.GetTimestamp(),
		volume.GetMetadata().GetCreationTimestamp(),
		volume.GetStatus().GetStateTransitionTime(),
		volume.GetId(),
	)
}
