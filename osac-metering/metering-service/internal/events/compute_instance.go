package events

import (
	"fmt"
	"strings"
	"time"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
)

const computeInstanceStatePrefix = "COMPUTE_INSTANCE_STATE_"

type computeInstanceMapper struct {
	ci *privatev1.ComputeInstance
}

func (m *computeInstanceMapper) ResourceType() string { return "compute_instance" }
func (m *computeInstanceMapper) ResourceID() string   { return m.ci.GetId() }

func (m *computeInstanceMapper) TenantID() string {
	if md := m.ci.GetMetadata(); md != nil {
		return md.GetTenant()
	}
	return ""
}

func (m *computeInstanceMapper) ProjectID() *string {
	if md := m.ci.GetMetadata(); md != nil {
		return nilIfEmpty(md.GetProject())
	}
	return nil
}

func (m *computeInstanceMapper) CatalogItemID() *string {
	if s := m.ci.GetSpec(); s != nil {
		return nilIfEmpty(s.GetCatalogItem())
	}
	return nil
}

func (m *computeInstanceMapper) TemplateID() *string {
	if s := m.ci.GetSpec(); s != nil {
		return nilIfEmpty(s.GetTemplate())
	}
	return nil
}

func (m *computeInstanceMapper) CurrentState() string {
	state := privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED
	if s := m.ci.GetStatus(); s != nil {
		state = s.GetState()
	}
	return strings.TrimPrefix(state.String(), computeInstanceStatePrefix)
}

type vmaasBillingDimensions struct {
	InstanceType    *string `json:"instance_type"`
	ImageRef        *string `json:"image_ref"`
	BootDiskSizeGib *int32  `json:"boot_disk_size_gib"`
}

func (m *computeInstanceMapper) BillingDimensions() any {
	var bd vmaasBillingDimensions
	spec := m.ci.GetSpec()
	if spec == nil {
		return bd
	}
	if spec.InstanceType != nil {
		it := spec.GetInstanceType()
		bd.InstanceType = &it
	}
	if img := spec.GetImage(); img != nil {
		ref := img.GetSourceRef()
		bd.ImageRef = &ref
	}
	if disk := spec.GetBootDisk(); disk != nil {
		size := disk.GetSizeGib()
		bd.BootDiskSizeGib = &size
	}
	return bd
}

func (m *computeInstanceMapper) CloudEventType(eventType privatev1.EventType) (string, error) {
	switch eventType {
	case privatev1.EventType_EVENT_TYPE_OBJECT_CREATED:
		return "osac.resource.created.v1", nil
	case privatev1.EventType_EVENT_TYPE_OBJECT_DELETED:
		return "osac.resource.deleted.v1", nil
	case privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED:
		return m.mapUpdatedEventType(), nil
	default:
		return "", fmt.Errorf("unsupported event type: %v", eventType)
	}
}

func (m *computeInstanceMapper) mapUpdatedEventType() string {
	state := privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED
	if s := m.ci.GetStatus(); s != nil {
		state = s.GetState()
	}
	switch state {
	case privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING:
		return "osac.resource.started.v1"
	case privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED,
		privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED,
		privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED:
		return "osac.resource.suspended.v1"
	default:
		return "osac.resource.updated.v1"
	}
}

func (m *computeInstanceMapper) TransitionTime(eventType privatev1.EventType) (time.Time, error) {
	switch eventType {
	case privatev1.EventType_EVENT_TYPE_OBJECT_CREATED:
		if md := m.ci.GetMetadata(); md != nil {
			if ct := md.GetCreationTimestamp(); ct != nil {
				return ct.AsTime(), nil
			}
		}
		return time.Time{}, fmt.Errorf("%w: event %s has no creation_timestamp", ErrDataQuality, m.ci.GetId())

	case privatev1.EventType_EVENT_TYPE_OBJECT_DELETED:
		if md := m.ci.GetMetadata(); md != nil {
			if dt := md.GetDeletionTimestamp(); dt != nil {
				return dt.AsTime(), nil
			}
		}
		return time.Time{}, fmt.Errorf("%w: event %s has no deletion_timestamp", ErrDataQuality, m.ci.GetId())

	default:
		if s := m.ci.GetStatus(); s != nil {
			if t := s.GetStateTransitionTime(); t != nil {
				return t.AsTime(), nil
			}
		}
		return time.Time{}, fmt.Errorf("%w: event %s has no state_transition_time", ErrDataQuality, m.ci.GetId())
	}
}
