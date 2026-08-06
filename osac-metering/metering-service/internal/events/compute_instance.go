package events

import (
	"fmt"
	"strings"
	"time"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
)

const ComputeInstanceStatePrefix = "COMPUTE_INSTANCE_STATE_"

type computeInstanceMapper struct {
	ci *privatev1.ComputeInstance
}

func (m *computeInstanceMapper) ResourceType() string { return "compute_instance" }
func (m *computeInstanceMapper) ResourceID() string   { return m.ci.GetId() }

func (m *computeInstanceMapper) FulfillmentVersion() int32 {
	if md := m.ci.GetMetadata(); md != nil {
		return md.GetVersion()
	}
	return 0
}

func (m *computeInstanceMapper) IsBillable() bool {
	if s := m.ci.GetStatus(); s != nil {
		return s.GetState() == privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING
	}
	return false
}

func (m *computeInstanceMapper) BillingDimensionsMap() map[string]any {
	return ComputeInstanceBillingDimensions(m.ci)
}

func ComputeInstanceBillingDimensions(ci *privatev1.ComputeInstance) map[string]any {
	dims := map[string]any{}
	spec := ci.GetSpec()
	if spec == nil {
		return dims
	}
	if it := spec.GetInstanceType(); it != nil {
		dims["instance_type"] = it.GetName()
	}
	if img := spec.GetImage(); img != nil {
		dims["image_ref"] = img.GetSourceRef()
	}
	if disk := spec.GetBootDisk(); disk != nil {
		dims["boot_disk_size_gib"] = disk.GetSizeGib()
	}
	return dims
}

// IsBillableState returns whether a ComputeInstance state string represents
// a billable state. Single source of truth for billability — used by both
// the Watch Consumer (via IsBillable) and the Reconciler.
func IsBillableState(state string) bool {
	return state == "RUNNING"
}

func (m *computeInstanceMapper) TenantID() string {
	if md := m.ci.GetMetadata(); md != nil {
		return md.GetTenant()
	}
	return ""
}

func (m *computeInstanceMapper) ProjectID() *string {
	if md := m.ci.GetMetadata(); md != nil {
		return NilIfEmpty(md.GetProject())
	}
	return nil
}

func (m *computeInstanceMapper) CatalogItemID() *string {
	if s := m.ci.GetSpec(); s != nil {
		if ci := s.GetCatalogItem(); ci != nil {
			return NilIfEmpty(ci.GetName())
		}
	}
	return nil
}

func (m *computeInstanceMapper) TemplateID() *string {
	if s := m.ci.GetSpec(); s != nil {
		if t := s.GetTemplate(); t != nil {
			return NilIfEmpty(t.GetName())
		}
	}
	return nil
}

func (m *computeInstanceMapper) CurrentState() string {
	state := privatev1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED
	if s := m.ci.GetStatus(); s != nil {
		state = s.GetState()
	}
	return strings.TrimPrefix(state.String(), ComputeInstanceStatePrefix)
}

func (m *computeInstanceMapper) CloudEventType(eventType privatev1.EventType, previousState string) (string, error) {
	switch eventType {
	case privatev1.EventType_EVENT_TYPE_OBJECT_CREATED:
		return "osac.resource.created.v1", nil
	case privatev1.EventType_EVENT_TYPE_OBJECT_DELETED:
		return "osac.resource.deleted.v1", nil
	case privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED:
		return m.resolveUpdatedEventType(previousState)
	default:
		return "", fmt.Errorf("unsupported event type: %v", eventType)
	}
}

func (m *computeInstanceMapper) resolveUpdatedEventType(previousState string) (string, error) {
	currentState := m.CurrentState()

	switch {
	case currentState == "RUNNING" && (previousState == "STOPPED" || previousState == "PAUSED"):
		return "osac.resource.resumed.v1", nil
	case currentState == "RUNNING":
		return "osac.resource.started.v1", nil
	case currentState == "STOPPED" || currentState == "PAUSED" || currentState == "FAILED" || currentState == "DELETING":
		return "osac.resource.suspended.v1", nil
	case currentState == "STOPPING" || currentState == "STARTING":
		return "", ErrTransientState
	case currentState == "UNSPECIFIED":
		return "osac.resource.updated.v1", nil
	default:
		return "", fmt.Errorf("unexpected compute instance state transition: %s -> %s", previousState, currentState)
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
