package events

import (
	"fmt"
	"time"

	"github.com/osac-project/osac-metering/schema"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const BareMetalInstanceStatePrefix = "BARE_METAL_INSTANCE_STATE_"

const BareMetalInstanceStateRunning = "BARE_METAL_INSTANCE_STATE_RUNNING"

type bareMetalInstanceMapper struct {
	instance *privatev1.BareMetalInstance
}

func (m *bareMetalInstanceMapper) ResourceType() string { return ResourceTypeBareMetalInstance }
func (m *bareMetalInstanceMapper) ResourceID() string   { return m.instance.GetId() }

func (m *bareMetalInstanceMapper) FulfillmentVersion() int32 {
	if md := m.instance.GetMetadata(); md != nil {
		return md.GetVersion()
	}
	return 0
}

func (m *bareMetalInstanceMapper) TenantID() string {
	if md := m.instance.GetMetadata(); md != nil {
		return md.GetTenant()
	}
	return ""
}

func (m *bareMetalInstanceMapper) ProjectID() *string {
	if md := m.instance.GetMetadata(); md != nil {
		return NilIfEmpty(md.GetProject())
	}
	return nil
}

func (m *bareMetalInstanceMapper) CatalogItemID() *string {
	if spec := m.instance.GetSpec(); spec != nil {
		if catalogItem := spec.GetCatalogItem(); catalogItem != nil {
			return NilIfEmpty(catalogItem.GetName())
		}
	}
	return nil
}

func (m *bareMetalInstanceMapper) TemplateID() *string {
	if spec := m.instance.GetSpec(); spec != nil {
		if template := spec.GetTemplate(); template != nil {
			return NilIfEmpty(template.GetName())
		}
	}
	return nil
}

func (m *bareMetalInstanceMapper) CurrentState() string {
	state := privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_UNSPECIFIED
	if status := m.instance.GetStatus(); status != nil {
		state = status.GetState()
	}
	return state.String()
}

func (m *bareMetalInstanceMapper) IsBillable() bool {
	return IsAllocationBillableState(m.CurrentState())
}

func (m *bareMetalInstanceMapper) BillingDimensionsMap() (map[string]any, error) {
	return BareMetalInstanceBillingDimensions(m.instance)
}

func (m *bareMetalInstanceMapper) Usage(string, *time.Time, time.Time, map[string]any) (*schema.Usage, error) {
	return nil, nil
}

func BareMetalInstanceBillingDimensions(bmi *privatev1.BareMetalInstance) (map[string]any, error) {
	spec := bmi.GetSpec()
	if spec == nil || spec.GetInstanceType() == nil || spec.GetInstanceType().GetId() == "" {
		return nil, fmt.Errorf("%w: missing spec.instance_type.id", ErrDataQuality)
	}

	dims := map[string]any{
		"bm_instance_type": spec.GetInstanceType().GetId(),
	}
	if catalogItem := spec.GetCatalogItem(); catalogItem != nil {
		dims["catalog_item"] = catalogItem.GetName()
	}
	return dims, nil
}

func (m *bareMetalInstanceMapper) CloudEventType(eventType privatev1.EventType, _ string) (string, error) {
	switch eventType {
	case privatev1.EventType_EVENT_TYPE_OBJECT_CREATED:
		return EventCreated, nil
	case privatev1.EventType_EVENT_TYPE_OBJECT_DELETED:
		return EventDeleted, nil
	case privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED:
		return EventUpdated, nil
	default:
		return "", fmt.Errorf("%w: %v", ErrUnsupportedEvent, eventType)
	}
}

func (m *bareMetalInstanceMapper) TransitionTime(event *privatev1.Event, _ string) (time.Time, error) {
	return ResolveTransitionTime(
		event.GetType(),
		event.GetTimestamp(),
		m.instance.GetMetadata().GetCreationTimestamp(),
		m.instance.GetStatus().GetStateTransitionTime(),
		m.instance.GetId(),
	)
}
