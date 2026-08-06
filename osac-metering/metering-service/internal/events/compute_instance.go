package events

import (
	"fmt"
	"strings"
	"time"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
)

const ComputeInstanceStatePrefix = "COMPUTE_INSTANCE_STATE_"

// Compute instance state machine. The table IS the spec:
//   - Exact (from, to) match takes priority over wildcard
//   - Missing entry = error (fail fast)
//   - Transient: projection-only, no CloudEvent (billing context preserved)
var computeInstanceTransitions = TransitionTable{
	// Resumptions: specific previous states take priority over wildcard
	{"STOPPED", "RUNNING"}: {EventType: "osac.resource.resumed.v1"},
	{"PAUSED", "RUNNING"}:  {EventType: "osac.resource.resumed.v1"},

	// Started: any other previous state transitioning to RUNNING
	{"*", "RUNNING"}: {EventType: "osac.resource.started.v1"},

	// Suspended: billing interval closes
	{"*", "STOPPED"}:  {EventType: "osac.resource.suspended.v1"},
	{"*", "PAUSED"}:   {EventType: "osac.resource.suspended.v1"},
	{"*", "FAILED"}:   {EventType: "osac.resource.suspended.v1"},
	{"*", "DELETING"}: {EventType: "osac.resource.suspended.v1"},

	// Transient: intermediate states, preserve billing context
	{"*", "STOPPING"}: {Transient: true},
	{"*", "STARTING"}: {Transient: true},

	// Updated: no billing effect
	{"*", "UNSPECIFIED"}: {EventType: "osac.resource.updated.v1"},
}

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
		return resolveTransition(computeInstanceTransitions, previousState, m.CurrentState())
	default:
		return "", fmt.Errorf("unsupported event type: %v", eventType)
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
