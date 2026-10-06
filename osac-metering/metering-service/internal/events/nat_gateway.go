package events

import (
	"strings"
	"time"

	"github.com/osac-project/osac-metering/schema"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const NATGatewayStatePrefix = "NAT_GATEWAY_STATE_"

const (
	NATGatewayStatePending     = "PENDING"
	NATGatewayStateReady       = "READY"
	NATGatewayStateFailed      = "FAILED"
	NATGatewayStateDeleting    = "DELETING"
	NATGatewayStateUnspecified = "UNSPECIFIED"
)

var natGatewayTransitions = TransitionTable{
	{StateEmpty, NATGatewayStatePending}:     {Skip: true},
	{StateEmpty, NATGatewayStateReady}:       {EventType: eventBillableStart},
	{StateEmpty, NATGatewayStateFailed}:      {Skip: true},
	{StateEmpty, NATGatewayStateDeleting}:    {Skip: true},
	{StateEmpty, NATGatewayStateUnspecified}: {Skip: true},

	{NATGatewayStatePending, NATGatewayStatePending}:     {Skip: true},
	{NATGatewayStatePending, NATGatewayStateReady}:       {EventType: eventBillableStart},
	{NATGatewayStatePending, NATGatewayStateFailed}:      {Skip: true},
	{NATGatewayStatePending, NATGatewayStateDeleting}:    {Skip: true},
	{NATGatewayStatePending, NATGatewayStateUnspecified}: {Skip: true},

	{NATGatewayStateReady, NATGatewayStatePending}:     {EventType: EventSuspended},
	{NATGatewayStateReady, NATGatewayStateReady}:       {Skip: true},
	{NATGatewayStateReady, NATGatewayStateFailed}:      {EventType: EventSuspended},
	{NATGatewayStateReady, NATGatewayStateDeleting}:    {EventType: EventSuspended},
	{NATGatewayStateReady, NATGatewayStateUnspecified}: {Skip: true},

	{NATGatewayStateFailed, NATGatewayStatePending}:     {Skip: true},
	{NATGatewayStateFailed, NATGatewayStateReady}:       {EventType: eventBillableStart},
	{NATGatewayStateFailed, NATGatewayStateFailed}:      {Skip: true},
	{NATGatewayStateFailed, NATGatewayStateDeleting}:    {Skip: true},
	{NATGatewayStateFailed, NATGatewayStateUnspecified}: {Skip: true},

	{NATGatewayStateDeleting, NATGatewayStatePending}:     {Skip: true},
	{NATGatewayStateDeleting, NATGatewayStateReady}:       {EventType: eventBillableStart},
	{NATGatewayStateDeleting, NATGatewayStateFailed}:      {Skip: true},
	{NATGatewayStateDeleting, NATGatewayStateDeleting}:    {Skip: true},
	{NATGatewayStateDeleting, NATGatewayStateUnspecified}: {Skip: true},

	{NATGatewayStateUnspecified, NATGatewayStatePending}:     {Skip: true},
	{NATGatewayStateUnspecified, NATGatewayStateReady}:       {EventType: eventBillableStart},
	{NATGatewayStateUnspecified, NATGatewayStateFailed}:      {Skip: true},
	{NATGatewayStateUnspecified, NATGatewayStateDeleting}:    {Skip: true},
	{NATGatewayStateUnspecified, NATGatewayStateUnspecified}: {Skip: true},
}

type natGatewayMapper struct {
	gateway    *privatev1.NATGateway
	deployment string
}

func NewNATGatewayMapper(gateway *privatev1.NATGateway, deploymentID string) ResourceMapper {
	return &natGatewayMapper{gateway: gateway, deployment: deploymentID}
}

func (m *natGatewayMapper) ResourceType() string { return schema.ResourceTypeNATGateway }
func (m *natGatewayMapper) ResourceID() string   { return m.gateway.GetId() }

func (m *natGatewayMapper) FulfillmentVersion() int32 {
	return m.gateway.GetMetadata().GetVersion()
}

func (m *natGatewayMapper) TenantID() string {
	return m.gateway.GetMetadata().GetTenant()
}

func (m *natGatewayMapper) ProjectID() *string {
	return NilIfEmpty(m.gateway.GetMetadata().GetProject())
}

func (m *natGatewayMapper) CatalogItemID() *string { return nil }
func (m *natGatewayMapper) TemplateID() *string    { return nil }

func (m *natGatewayMapper) CurrentState() string {
	return NATGatewayCurrentState(m.gateway)
}

func NATGatewayCurrentState(gateway *privatev1.NATGateway) string {
	if gateway.GetMetadata().GetDeletionTimestamp() != nil {
		return NATGatewayStateDeleting
	}
	state := gateway.GetStatus().GetState()
	return strings.TrimPrefix(state.String(), NATGatewayStatePrefix)
}

func (m *natGatewayMapper) IsBillable() bool {
	return m.CurrentState() == NATGatewayStateReady
}

func (m *natGatewayMapper) BillingDimensionsMap() (map[string]any, error) {
	dimensions := NATGatewayBillingDimensions(m.gateway, m.deployment)
	return dimensions, validateNetworkingBillingDimensions(schema.ResourceTypeNATGateway, dimensions)
}

func (m *natGatewayMapper) Usage(string, *time.Time, time.Time, map[string]any) (*schema.Usage, error) {
	return nil, nil
}

func NATGatewayBillingDimensions(gateway *privatev1.NATGateway, deploymentID string) map[string]any {
	return map[string]any{
		"deployment":      deploymentID,
		"virtual_network": gateway.GetSpec().GetVirtualNetwork().GetId(),
		"external_ip":     gateway.GetSpec().GetExternalIp().GetId(),
		"tenant_id":       gateway.GetMetadata().GetTenant(),
		"project_id":      gateway.GetMetadata().GetProject(),
	}
}

func IsNATGatewayBillableState(state string) bool {
	return state == NATGatewayStateReady
}

func IsNATGatewayTransientState(string) bool { return false }

func (m *natGatewayMapper) CloudEventType(eventType privatev1.EventType, previousState string) (string, error) {
	return ResolveCloudEventType(natGatewayTransitions, eventType, previousState, m.CurrentState())
}

func (m *natGatewayMapper) TransitionTime(event *privatev1.Event, _ string) (time.Time, error) {
	eventType := event.GetType()
	if eventType == privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED && m.gateway.GetMetadata().GetDeletionTimestamp() != nil {
		return m.gateway.GetMetadata().GetDeletionTimestamp().AsTime(), nil
	}
	return ResolveTransitionTime(eventType,
		event.GetTimestamp(),
		m.gateway.GetMetadata().GetCreationTimestamp(),
		m.gateway.GetStatus().GetStateTransitionTime(),
		m.gateway.GetId())
}
