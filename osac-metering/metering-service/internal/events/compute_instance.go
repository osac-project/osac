package events

import (
	"strings"
	"time"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
)

const ComputeInstanceStatePrefix = "COMPUTE_INSTANCE_STATE_"

// VMaaS compute instance state constants.
const (
	ComputeInstanceStateRunning     = "RUNNING"
	ComputeInstanceStateStopped     = "STOPPED"
	ComputeInstanceStatePaused      = "PAUSED"
	ComputeInstanceStateFailed      = "FAILED"
	ComputeInstanceStateStopping    = "STOPPING"
	ComputeInstanceStateStarting    = "STARTING"
	ComputeInstanceStateDeleting    = "DELETING"
	ComputeInstanceStateUnspecified = "UNSPECIFIED"
)

// Compute instance state machine. Every (from, to) pair is enumerated
// explicitly — no wildcards. Missing entry = error (fail fast).
//
// Billable: RUNNING
// Non-billable: STOPPED, PAUSED, FAILED, DELETING, UNSPECIFIED
// Transient: STOPPING, STARTING
var computeInstanceTransitions = TransitionTable{
	// --- From "" (initial observation) ---
	{StateEmpty, ComputeInstanceStateRunning}:     {EventType: EventStarted},
	{StateEmpty, ComputeInstanceStateStopped}:     {Skip: true},
	{StateEmpty, ComputeInstanceStatePaused}:      {Skip: true},
	{StateEmpty, ComputeInstanceStateFailed}:      {Skip: true},
	{StateEmpty, ComputeInstanceStateStopping}:    {Transient: true},
	{StateEmpty, ComputeInstanceStateStarting}:    {Transient: true},
	{StateEmpty, ComputeInstanceStateDeleting}:    {Skip: true},
	{StateEmpty, ComputeInstanceStateUnspecified}: {Skip: true},

	// --- From RUNNING ---
	{ComputeInstanceStateRunning, ComputeInstanceStateRunning}:     {Skip: true},
	{ComputeInstanceStateRunning, ComputeInstanceStateStopped}:     {EventType: EventSuspended},
	{ComputeInstanceStateRunning, ComputeInstanceStatePaused}:      {EventType: EventSuspended},
	{ComputeInstanceStateRunning, ComputeInstanceStateFailed}:      {EventType: EventSuspended},
	{ComputeInstanceStateRunning, ComputeInstanceStateStopping}:    {Transient: true},
	{ComputeInstanceStateRunning, ComputeInstanceStateStarting}:    {Transient: true},
	{ComputeInstanceStateRunning, ComputeInstanceStateDeleting}:    {EventType: EventSuspended},
	{ComputeInstanceStateRunning, ComputeInstanceStateUnspecified}: {EventType: EventSuspended},

	// --- From STOPPED ---
	{ComputeInstanceStateStopped, ComputeInstanceStateRunning}:     {EventType: EventResumed},
	{ComputeInstanceStateStopped, ComputeInstanceStateStopped}:     {Skip: true},
	{ComputeInstanceStateStopped, ComputeInstanceStatePaused}:      {Skip: true},
	{ComputeInstanceStateStopped, ComputeInstanceStateFailed}:      {Skip: true},
	{ComputeInstanceStateStopped, ComputeInstanceStateStopping}:    {Transient: true},
	{ComputeInstanceStateStopped, ComputeInstanceStateStarting}:    {Transient: true},
	{ComputeInstanceStateStopped, ComputeInstanceStateDeleting}:    {Skip: true},
	{ComputeInstanceStateStopped, ComputeInstanceStateUnspecified}: {Skip: true},

	// --- From PAUSED ---
	{ComputeInstanceStatePaused, ComputeInstanceStateRunning}:     {EventType: EventResumed},
	{ComputeInstanceStatePaused, ComputeInstanceStateStopped}:     {Skip: true},
	{ComputeInstanceStatePaused, ComputeInstanceStatePaused}:      {Skip: true},
	{ComputeInstanceStatePaused, ComputeInstanceStateFailed}:      {Skip: true},
	{ComputeInstanceStatePaused, ComputeInstanceStateStopping}:    {Transient: true},
	{ComputeInstanceStatePaused, ComputeInstanceStateStarting}:    {Transient: true},
	{ComputeInstanceStatePaused, ComputeInstanceStateDeleting}:    {Skip: true},
	{ComputeInstanceStatePaused, ComputeInstanceStateUnspecified}: {Skip: true},

	// --- From FAILED ---
	{ComputeInstanceStateFailed, ComputeInstanceStateRunning}:     {EventType: EventStarted},
	{ComputeInstanceStateFailed, ComputeInstanceStateStopped}:     {Skip: true},
	{ComputeInstanceStateFailed, ComputeInstanceStatePaused}:      {Skip: true},
	{ComputeInstanceStateFailed, ComputeInstanceStateFailed}:      {Skip: true},
	{ComputeInstanceStateFailed, ComputeInstanceStateStopping}:    {Transient: true},
	{ComputeInstanceStateFailed, ComputeInstanceStateStarting}:    {Transient: true},
	{ComputeInstanceStateFailed, ComputeInstanceStateDeleting}:    {Skip: true},
	{ComputeInstanceStateFailed, ComputeInstanceStateUnspecified}: {Skip: true},

	// --- From STOPPING ---
	{ComputeInstanceStateStopping, ComputeInstanceStateRunning}:     {EventType: EventStarted},
	{ComputeInstanceStateStopping, ComputeInstanceStateStopped}:     {Skip: true},
	{ComputeInstanceStateStopping, ComputeInstanceStatePaused}:      {Skip: true},
	{ComputeInstanceStateStopping, ComputeInstanceStateFailed}:      {Skip: true},
	{ComputeInstanceStateStopping, ComputeInstanceStateStopping}:    {Skip: true},
	{ComputeInstanceStateStopping, ComputeInstanceStateStarting}:    {Transient: true},
	{ComputeInstanceStateStopping, ComputeInstanceStateDeleting}:    {Skip: true},
	{ComputeInstanceStateStopping, ComputeInstanceStateUnspecified}: {Skip: true},

	// --- From STARTING ---
	{ComputeInstanceStateStarting, ComputeInstanceStateRunning}:     {EventType: EventStarted},
	{ComputeInstanceStateStarting, ComputeInstanceStateStopped}:     {Skip: true},
	{ComputeInstanceStateStarting, ComputeInstanceStatePaused}:      {Skip: true},
	{ComputeInstanceStateStarting, ComputeInstanceStateFailed}:      {Skip: true},
	{ComputeInstanceStateStarting, ComputeInstanceStateStopping}:    {Transient: true},
	{ComputeInstanceStateStarting, ComputeInstanceStateStarting}:    {Skip: true},
	{ComputeInstanceStateStarting, ComputeInstanceStateDeleting}:    {Skip: true},
	{ComputeInstanceStateStarting, ComputeInstanceStateUnspecified}: {Skip: true},

	// --- From DELETING ---
	{ComputeInstanceStateDeleting, ComputeInstanceStateRunning}:     {EventType: EventStarted},
	{ComputeInstanceStateDeleting, ComputeInstanceStateStopped}:     {Skip: true},
	{ComputeInstanceStateDeleting, ComputeInstanceStatePaused}:      {Skip: true},
	{ComputeInstanceStateDeleting, ComputeInstanceStateFailed}:      {Skip: true},
	{ComputeInstanceStateDeleting, ComputeInstanceStateStopping}:    {Transient: true},
	{ComputeInstanceStateDeleting, ComputeInstanceStateStarting}:    {Transient: true},
	{ComputeInstanceStateDeleting, ComputeInstanceStateDeleting}:    {Skip: true},
	{ComputeInstanceStateDeleting, ComputeInstanceStateUnspecified}: {Skip: true},

	// --- From UNSPECIFIED ---
	{ComputeInstanceStateUnspecified, ComputeInstanceStateRunning}:     {EventType: EventStarted},
	{ComputeInstanceStateUnspecified, ComputeInstanceStateStopped}:     {Skip: true},
	{ComputeInstanceStateUnspecified, ComputeInstanceStatePaused}:      {Skip: true},
	{ComputeInstanceStateUnspecified, ComputeInstanceStateFailed}:      {Skip: true},
	{ComputeInstanceStateUnspecified, ComputeInstanceStateStopping}:    {Transient: true},
	{ComputeInstanceStateUnspecified, ComputeInstanceStateStarting}:    {Transient: true},
	{ComputeInstanceStateUnspecified, ComputeInstanceStateDeleting}:    {Skip: true},
	{ComputeInstanceStateUnspecified, ComputeInstanceStateUnspecified}: {Skip: true},
}

type computeInstanceMapper struct {
	ci *privatev1.ComputeInstance
}

func (m *computeInstanceMapper) ResourceType() string { return ResourceTypeComputeInstance }
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
	return state == ComputeInstanceStateRunning
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
	return ResolveCloudEventType(computeInstanceTransitions, eventType, previousState, m.CurrentState())
}

func (m *computeInstanceMapper) TransitionTime(eventType privatev1.EventType) (time.Time, error) {
	return ResolveTransitionTime(eventType,
		m.ci.GetMetadata().GetCreationTimestamp(),
		m.ci.GetMetadata().GetDeletionTimestamp(),
		m.ci.GetStatus().GetStateTransitionTime(),
		m.ci.GetId())
}
