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
	FulfillmentVersion() int32
	IsBillable() bool
	BillingDimensionsMap() map[string]any
	TransitionTime(eventType privatev1.EventType) (time.Time, error)
	CloudEventType(eventType privatev1.EventType, previousState string) (string, error)
}

// StateContext carries previous state from the State Projection for enriching
// lifecycle events with duration and state-derived type resolution.
type StateContext struct {
	PreviousState   string
	WasBillable     bool
	BillableSince   *time.Time
	DurationSeconds *float64
}

// MapWatchEvent converts a fulfillment-service Watch Event into a CloudEvents 1.0
// event. billingDims is the billing dimensions to embed in the event payload —
// callers pass per-component flat dims (from decomposition) or top-level-only
// dims (for audit events), never the nested stored form directly.
func MapWatchEvent(event *privatev1.Event, mapper ResourceMapper, stateCtx *StateContext, billingDims map[string]any) (*cloudevents.Event, error) {
	previousState := stateCtx.PreviousState

	ceType, err := mapper.CloudEventType(event.GetType(), previousState)
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

	projectID := ""
	if p := mapper.ProjectID(); p != nil {
		projectID = *p
	}
	SetOSACExtensions(&ce, mapper.ResourceID(), mapper.ResourceType(), mapper.TenantID(), projectID)

	data := BuildLifecycleData(mapper, billingDims, stateCtx.PreviousState, stateCtx.DurationSeconds, transitionTime)
	if err := ce.SetData(cloudevents.ApplicationJSON, data); err != nil {
		return nil, fmt.Errorf("setting CloudEvent data: %w", err)
	}

	return &ce, nil
}

// MapperForEvent returns the ResourceMapper for the event's payload type.
// Exported for use by the Watch Consumer to inspect resource state before mapping.
func MapperForEvent(event *privatev1.Event) (ResourceMapper, error) {
	return mapperForEvent(event)
}

// mapperForEvent returns the ResourceMapper for the event's payload type.
// Adding a new resource type = one case here + one mapper file.
func mapperForEvent(event *privatev1.Event) (ResourceMapper, error) {
	if ci := event.GetComputeInstance(); ci != nil {
		return &computeInstanceMapper{ci: ci}, nil
	}
	if cl := event.GetCluster(); cl != nil {
		return &clusterMapper{cl: cl}, nil
	}
	return nil, fmt.Errorf("unsupported event payload type for event %s", event.GetId())
}

// LifecycleData is the shared JSON payload for lifecycle and scaling events
// across all resource types. Exported and built exclusively through
// BuildLifecycleData so every producer (MapWatchEvent, and the Watch
// Consumer's scaling-event builder) emits the identical shape.
type LifecycleData struct {
	ResourceID        string         `json:"resource_id"`
	ResourceType      string         `json:"resource_type"`
	TenantID          string         `json:"tenant_id"`
	ProjectID         *string        `json:"project_id"`
	CatalogItemID     *string        `json:"catalog_item_id"`
	TemplateID        *string        `json:"template_id"`
	PreviousState     *string        `json:"previous_state"`
	CurrentState      string         `json:"current_state"`
	TransitionTime    string         `json:"transition_time"`
	DurationSeconds   *float64       `json:"duration_seconds"`
	BillingDimensions map[string]any `json:"billing_dimensions"`
	SchemaVersion     string         `json:"schema_version"`
}

// BuildLifecycleData constructs the shared lifecycle/scaling event payload
// from a resource mapper.
func BuildLifecycleData(mapper ResourceMapper, billingDims map[string]any, previousState string, durationSeconds *float64, transitionTime time.Time) LifecycleData {
	var prevStatePtr *string
	if previousState != "" {
		prevStatePtr = &previousState
	}
	return LifecycleData{
		ResourceID:        mapper.ResourceID(),
		ResourceType:      mapper.ResourceType(),
		TenantID:          mapper.TenantID(),
		ProjectID:         mapper.ProjectID(),
		CatalogItemID:     mapper.CatalogItemID(),
		TemplateID:        mapper.TemplateID(),
		PreviousState:     prevStatePtr,
		CurrentState:      mapper.CurrentState(),
		TransitionTime:    transitionTime.Format(time.RFC3339Nano),
		DurationSeconds:   durationSeconds,
		BillingDimensions: billingDims,
		SchemaVersion:     "v1",
	}
}

func NilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
