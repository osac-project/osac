package events

import (
	"errors"
	"fmt"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
)

var ErrDataQuality = errors.New("data quality")

// ResourceMapper extracts metering data from a resource-specific Event payload.
// Each OSAC resource type (ComputeInstance, ClusterOrder, etc.) implements this.
type ResourceMapper interface {
	ResourceType() string
	ResourceID() string
	TenantID() string
	ProjectID() *string
	CatalogItemID() *string
	TemplateID() *string
	CurrentState() string
	BillingDimensions() any
	TransitionTime(eventType privatev1.EventType) (time.Time, error)
	CloudEventType(eventType privatev1.EventType) (string, error)
}

// MapWatchEvent converts a fulfillment-service Watch Event into a CloudEvents 1.0
// event using the appropriate ResourceMapper for the payload type.
func MapWatchEvent(event *privatev1.Event) (*cloudevents.Event, error) {
	mapper, err := mapperForEvent(event)
	if err != nil {
		return nil, err
	}

	ceType, err := mapper.CloudEventType(event.GetType())
	if err != nil {
		return nil, err
	}

	if mapper.ResourceID() == "" {
		return nil, fmt.Errorf("%w: event %s has no resource_id", ErrDataQuality, event.GetId())
	}

	if mapper.TenantID() == "" {
		return nil, fmt.Errorf("%w: resource %s has no tenant_id", ErrDataQuality, mapper.ResourceID())
	}

	transitionTime, err := mapper.TransitionTime(event.GetType())
	if err != nil {
		return nil, err
	}

	ce := cloudevents.NewEvent()
	ce.SetID(event.GetId())
	ce.SetSource("osac-metering")
	ce.SetType(ceType)
	ce.SetTime(transitionTime)

	ce.SetExtension("osacresourceid", mapper.ResourceID())
	ce.SetExtension("osacresourcetype", mapper.ResourceType())
	ce.SetExtension("osactenant", mapper.TenantID())
	if p := mapper.ProjectID(); p != nil && *p != "" {
		ce.SetExtension("osacproject", *p)
	}

	data := meteringData{
		ResourceID:        mapper.ResourceID(),
		ResourceType:      mapper.ResourceType(),
		TenantID:          mapper.TenantID(),
		ProjectID:         mapper.ProjectID(),
		CatalogItemID:     mapper.CatalogItemID(),
		TemplateID:        mapper.TemplateID(),
		PreviousState:     nil, // Phase 1: no state tracking
		CurrentState:      mapper.CurrentState(),
		TransitionTime:    transitionTime.Format(time.RFC3339Nano),
		DurationSeconds:   nil, // Phase 1: no duration tracking
		BillingDimensions: mapper.BillingDimensions(),
		SchemaVersion:     "v1",
	}
	if err := ce.SetData(cloudevents.ApplicationJSON, data); err != nil {
		return nil, fmt.Errorf("setting CloudEvent data: %w", err)
	}

	return &ce, nil
}

// mapperForEvent returns the ResourceMapper for the event's payload type.
// Adding a new resource type = one case here + one mapper file.
func mapperForEvent(event *privatev1.Event) (ResourceMapper, error) {
	if ci := event.GetComputeInstance(); ci != nil {
		return &computeInstanceMapper{ci: ci}, nil
	}
	return nil, fmt.Errorf("unsupported event payload type for event %s", event.GetId())
}

// meteringData is the shared JSON payload for all resource types.
type meteringData struct {
	ResourceID        string   `json:"resource_id"`
	ResourceType      string   `json:"resource_type"`
	TenantID          string   `json:"tenant_id"`
	ProjectID         *string  `json:"project_id"`
	CatalogItemID     *string  `json:"catalog_item_id"`
	TemplateID        *string  `json:"template_id"`
	PreviousState     *string  `json:"previous_state"`
	CurrentState      string   `json:"current_state"`
	TransitionTime    string   `json:"transition_time"`
	DurationSeconds   *float64 `json:"duration_seconds"`
	BillingDimensions any      `json:"billing_dimensions"`
	SchemaVersion     string   `json:"schema_version"`
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
