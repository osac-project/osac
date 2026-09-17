package events

import (
	"fmt"
	"strings"
	"time"

	"github.com/osac-project/osac-metering/schema"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const ExternalIPStatePrefix = "EXTERNAL_IP_STATE_"

const (
	ExternalIPStatePending     = "PENDING"
	ExternalIPStateAllocated   = "ALLOCATED"
	ExternalIPStateFailed      = "FAILED"
	ExternalIPStateDeleting    = "DELETING"
	ExternalIPStateUnspecified = "UNSPECIFIED"
)

var externalIPTransitions = TransitionTable{
	{StateEmpty, ExternalIPStatePending}:     {Skip: true},
	{StateEmpty, ExternalIPStateAllocated}:   {EventType: eventBillableStart},
	{StateEmpty, ExternalIPStateFailed}:      {Skip: true},
	{StateEmpty, ExternalIPStateDeleting}:    {Skip: true},
	{StateEmpty, ExternalIPStateUnspecified}: {Skip: true},

	{ExternalIPStatePending, ExternalIPStatePending}:     {Skip: true},
	{ExternalIPStatePending, ExternalIPStateAllocated}:   {EventType: eventBillableStart},
	{ExternalIPStatePending, ExternalIPStateFailed}:      {Skip: true},
	{ExternalIPStatePending, ExternalIPStateDeleting}:    {Skip: true},
	{ExternalIPStatePending, ExternalIPStateUnspecified}: {Skip: true},

	{ExternalIPStateAllocated, ExternalIPStatePending}:     {Skip: true},
	{ExternalIPStateAllocated, ExternalIPStateAllocated}:   {Skip: true},
	{ExternalIPStateAllocated, ExternalIPStateFailed}:      {EventType: EventSuspended},
	{ExternalIPStateAllocated, ExternalIPStateDeleting}:    {EventType: EventSuspended},
	{ExternalIPStateAllocated, ExternalIPStateUnspecified}: {Skip: true},

	{ExternalIPStateFailed, ExternalIPStatePending}:     {Skip: true},
	{ExternalIPStateFailed, ExternalIPStateAllocated}:   {EventType: eventBillableStart},
	{ExternalIPStateFailed, ExternalIPStateFailed}:      {Skip: true},
	{ExternalIPStateFailed, ExternalIPStateDeleting}:    {Skip: true},
	{ExternalIPStateFailed, ExternalIPStateUnspecified}: {Skip: true},

	{ExternalIPStateDeleting, ExternalIPStatePending}:     {Skip: true},
	{ExternalIPStateDeleting, ExternalIPStateAllocated}:   {EventType: eventBillableStart},
	{ExternalIPStateDeleting, ExternalIPStateFailed}:      {Skip: true},
	{ExternalIPStateDeleting, ExternalIPStateDeleting}:    {Skip: true},
	{ExternalIPStateDeleting, ExternalIPStateUnspecified}: {Skip: true},

	{ExternalIPStateUnspecified, ExternalIPStatePending}:     {Skip: true},
	{ExternalIPStateUnspecified, ExternalIPStateAllocated}:   {EventType: eventBillableStart},
	{ExternalIPStateUnspecified, ExternalIPStateFailed}:      {Skip: true},
	{ExternalIPStateUnspecified, ExternalIPStateDeleting}:    {Skip: true},
	{ExternalIPStateUnspecified, ExternalIPStateUnspecified}: {Skip: true},
}

type externalIPMapper struct {
	ip           *privatev1.ExternalIP
	deploymentID string
	poolFamilies map[string]string
}

func NewExternalIPMapper(ip *privatev1.ExternalIP, deploymentID string, poolFamilies map[string]string) ResourceMapper {
	return &externalIPMapper{ip: ip, deploymentID: deploymentID, poolFamilies: poolFamilies}
}

var ipFamilyDimensions = map[privatev1.IPFamily]string{
	privatev1.IPFamily_IP_FAMILY_IPV4: "ipv4",
	privatev1.IPFamily_IP_FAMILY_IPV6: "ipv6",
}

func (m *externalIPMapper) ResourceType() string { return schema.ResourceTypeExternalIP }
func (m *externalIPMapper) ResourceID() string   { return m.ip.GetId() }

func (m *externalIPMapper) FulfillmentVersion() int32 {
	return m.ip.GetMetadata().GetVersion()
}

func (m *externalIPMapper) TenantID() string {
	return m.ip.GetMetadata().GetTenant()
}

func (m *externalIPMapper) ProjectID() *string {
	return NilIfEmpty(m.ip.GetMetadata().GetProject())
}

func (m *externalIPMapper) CatalogItemID() *string { return nil }
func (m *externalIPMapper) TemplateID() *string    { return nil }

func (m *externalIPMapper) CurrentState() string {
	return ExternalIPCurrentState(m.ip)
}

func ExternalIPCurrentState(ip *privatev1.ExternalIP) string {
	if ip.GetMetadata().GetDeletionTimestamp() != nil {
		return ExternalIPStateDeleting
	}
	state := ip.GetStatus().GetState()
	return strings.TrimPrefix(state.String(), ExternalIPStatePrefix)
}

func ExternalIPPoolFamily(pool *privatev1.ExternalIPPool) (string, error) {
	if pool.GetId() == "" {
		return "", fmt.Errorf("%w: ExternalIPPool has no ID", ErrDataQuality)
	}
	family, ok := ipFamilyDimensions[pool.GetSpec().GetIpFamily()]
	if !ok {
		return "", fmt.Errorf("%w: ExternalIPPool %s has an unsupported IP family", ErrDataQuality, pool.GetId())
	}
	return family, nil
}

func (m *externalIPMapper) IsBillable() bool {
	return m.CurrentState() == ExternalIPStateAllocated
}

func (m *externalIPMapper) BillingDimensionsMap() (map[string]any, error) {
	return ExternalIPBillingDimensions(m.ip, m.deploymentID, m.poolFamilies)
}

func (m *externalIPMapper) Usage(string, *time.Time, time.Time, map[string]any) (*schema.Usage, error) {
	return nil, nil
}

func ExternalIPBillingDimensions(ip *privatev1.ExternalIP, deploymentID string, pools map[string]string) (map[string]any, error) {
	dimensions := map[string]any{
		"deployment": deploymentID,
		"attached":   ip.GetStatus().GetAttached(),
		"tenant_id":  ip.GetMetadata().GetTenant(),
		"project_id": ip.GetMetadata().GetProject(),
	}

	poolID := ip.GetSpec().GetPool().GetId()
	family, ok := pools[poolID]
	if !ok {
		return dimensions, fmt.Errorf("%w: external IP pool %s was not found", ErrDataQuality, poolID)
	}
	dimensions["pool"] = poolID
	dimensions["ip_family"] = family

	if ip.GetStatus().GetAttached() {
		attribution, err := attributionDimensions(ip.GetStatus().GetAttribution())
		if err != nil {
			return dimensions, err
		}
		for key, value := range attribution {
			dimensions[key] = value
		}
	}
	return dimensions, validateNetworkingBillingDimensions(schema.ResourceTypeExternalIP, dimensions)
}

func IsExternalIPBillableState(state string) bool {
	return state == ExternalIPStateAllocated
}

func IsExternalIPTransientState(string) bool { return false }

func (m *externalIPMapper) CloudEventType(eventType privatev1.EventType, previousState string) (string, error) {
	return ResolveCloudEventType(externalIPTransitions, eventType, previousState, m.CurrentState())
}

func (m *externalIPMapper) TransitionTime(event *privatev1.Event, previousState string) (time.Time, error) {
	eventType := event.GetType()
	if eventType == privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED {
		return ExternalIPWatchTransitionTime(m.ip, previousState)
	}
	return ResolveTransitionTime(eventType,
		event.GetTimestamp(),
		m.ip.GetMetadata().GetCreationTimestamp(),
		m.ip.GetStatus().GetStateTransitionTime(),
		m.ip.GetId())
}

func ExternalIPWatchTransitionTime(ip *privatev1.ExternalIP, previousState string) (time.Time, error) {
	if timestamp := ip.GetMetadata().GetDeletionTimestamp(); timestamp != nil {
		return timestamp.AsTime(), nil
	}
	stateTime := ip.GetStatus().GetStateTransitionTime()
	if ip.GetStatus().GetState() == privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED &&
		previousState == ExternalIPStateAllocated {
		attachmentTime := ip.GetStatus().GetAttachmentTransitionTime()
		if ip.GetStatus().GetAttached() && attachmentTime == nil {
			return time.Time{}, fmt.Errorf("%w: resource %s has no attachment transition time", ErrDataQuality, ip.GetId())
		}
		if attachmentTime != nil && (stateTime == nil || attachmentTime.AsTime().After(stateTime.AsTime())) {
			return attachmentTime.AsTime(), nil
		}
	}
	if stateTime == nil {
		return time.Time{}, fmt.Errorf("%w: resource %s has no authoritative transition time", ErrDataQuality, ip.GetId())
	}
	return stateTime.AsTime(), nil
}

func ExternalIPTransitionTime(ip *privatev1.ExternalIP) (time.Time, error) {
	if timestamp := ip.GetMetadata().GetDeletionTimestamp(); timestamp != nil {
		return timestamp.AsTime(), nil
	}
	stateTime := ip.GetStatus().GetStateTransitionTime()
	if ip.GetStatus().GetState() == privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED {
		attachmentTime := ip.GetStatus().GetAttachmentTransitionTime()
		if ip.GetStatus().GetAttached() && attachmentTime == nil {
			return time.Time{}, fmt.Errorf("%w: resource %s has no attachment transition time", ErrDataQuality, ip.GetId())
		}
		if attachmentTime != nil && (stateTime == nil || attachmentTime.AsTime().After(stateTime.AsTime())) {
			return attachmentTime.AsTime(), nil
		}
	}
	if stateTime == nil {
		return time.Time{}, fmt.Errorf("%w: resource %s has no authoritative transition time", ErrDataQuality, ip.GetId())
	}
	return stateTime.AsTime(), nil
}
