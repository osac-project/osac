/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package events

import (
	"fmt"
	"sort"
	"strings"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
)

const ClusterStatePrefix = "CLUSTER_STATE_"

// DimensionReleaseImage is the billing dimension key for the cluster release image.
const DimensionReleaseImage = "release_image"

// CaaS cluster state constants.
const (
	ClusterStateProgressing  = "PROGRESSING"
	ClusterStateReady        = "READY"
	ClusterStateFailed       = "FAILED"
	ClusterStateDeleting     = "DELETING"
	ClusterStateDeleteFailed = "DELETE_FAILED"
	ClusterStateUnspecified  = "UNSPECIFIED"
)

type clusterMapper struct {
	cl *privatev1.Cluster
}

func (m *clusterMapper) ResourceType() string { return ResourceTypeClusterOrder }
func (m *clusterMapper) ResourceID() string   { return m.cl.GetId() }

func (m *clusterMapper) FulfillmentVersion() int32 {
	if md := m.cl.GetMetadata(); md != nil {
		return md.GetVersion()
	}
	return 0
}

func (m *clusterMapper) TenantID() string {
	if md := m.cl.GetMetadata(); md != nil {
		return md.GetTenant()
	}
	return ""
}

func (m *clusterMapper) ProjectID() *string {
	if md := m.cl.GetMetadata(); md != nil {
		return NilIfEmpty(md.GetProject())
	}
	return nil
}

func (m *clusterMapper) CatalogItemID() *string {
	if s := m.cl.GetSpec(); s != nil {
		if ci := s.GetCatalogItem(); ci != nil {
			return NilIfEmpty(ci.GetId())
		}
	}
	return nil
}

func (m *clusterMapper) TemplateID() *string {
	if s := m.cl.GetSpec(); s != nil {
		if t := s.GetTemplate(); t != nil {
			return NilIfEmpty(t.GetId())
		}
	}
	return nil
}

func (m *clusterMapper) CurrentState() string {
	state := privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED
	if s := m.cl.GetStatus(); s != nil {
		state = s.GetState()
	}
	return strings.TrimPrefix(state.String(), ClusterStatePrefix)
}

func (m *clusterMapper) IsBillable() bool {
	return IsClusterBillableState(m.CurrentState())
}

func (m *clusterMapper) BillingDimensionsMap() map[string]any {
	return ClusterBillingDimensions(m.cl)
}

// CaaS cluster state machine. Both PROGRESSING and READY are billable.
// Transitions between billable states have no billing boundary (Skip).
// Dimension changes (scaling) during skipped transitions are detected by
// the Watch Consumer via DimensionsEqual; the hourly reconciler catches
// any missed dimension drift.
var clusterTransitions = TransitionTable{
	// Started: first billable state (no previous)
	{StateEmpty, ClusterStateProgressing}: {EventType: EventStarted},
	{StateEmpty, ClusterStateReady}:       {EventType: EventStarted},

	// Skip: first observed in non-billable state (bootstrap, reconnect after failure)
	{StateEmpty, ClusterStateFailed}:       {Skip: true},
	{StateEmpty, ClusterStateDeleting}:     {Skip: true},
	{StateEmpty, ClusterStateDeleteFailed}: {Skip: true},
	{StateEmpty, ClusterStateUnspecified}:  {Skip: true},

	// Resumed: non-billable to billable
	{ClusterStateFailed, ClusterStateProgressing}:       {EventType: EventResumed},
	{ClusterStateFailed, ClusterStateReady}:             {EventType: EventResumed},
	{ClusterStateDeleting, ClusterStateProgressing}:     {EventType: EventResumed},
	{ClusterStateDeleting, ClusterStateReady}:           {EventType: EventResumed},
	{ClusterStateDeleteFailed, ClusterStateProgressing}: {EventType: EventResumed},
	{ClusterStateDeleteFailed, ClusterStateReady}:       {EventType: EventResumed},
	{ClusterStateUnspecified, ClusterStateProgressing}:  {EventType: EventResumed},
	{ClusterStateUnspecified, ClusterStateReady}:        {EventType: EventResumed},

	// Suspended: billable to non-billable
	{ClusterStateProgressing, ClusterStateFailed}:       {EventType: EventSuspended},
	{ClusterStateProgressing, ClusterStateDeleting}:     {EventType: EventSuspended},
	{ClusterStateReady, ClusterStateFailed}:             {EventType: EventSuspended},
	{ClusterStateReady, ClusterStateDeleting}:           {EventType: EventSuspended},
	{ClusterStateProgressing, ClusterStateUnspecified}:  {EventType: EventSuspended},
	{ClusterStateReady, ClusterStateUnspecified}:        {EventType: EventSuspended},
	{ClusterStateReady, ClusterStateDeleteFailed}:       {EventType: EventSuspended},
	{ClusterStateProgressing, ClusterStateDeleteFailed}: {EventType: EventSuspended},

	// Skip: billable to billable (no billing boundary, includes same-state for scaling)
	{ClusterStateProgressing, ClusterStateReady}:       {Skip: true},
	{ClusterStateReady, ClusterStateProgressing}:       {Skip: true},
	{ClusterStateProgressing, ClusterStateProgressing}: {Skip: true},
	{ClusterStateReady, ClusterStateReady}:             {Skip: true},

	// Skip: non-billable to non-billable (no billing effect, includes same-state)
	{ClusterStateFailed, ClusterStateFailed}:             {Skip: true},
	{ClusterStateDeleting, ClusterStateDeleting}:         {Skip: true},
	{ClusterStateDeleteFailed, ClusterStateDeleteFailed}: {Skip: true},
	{ClusterStateUnspecified, ClusterStateUnspecified}:   {Skip: true},
	{ClusterStateFailed, ClusterStateDeleting}:           {Skip: true},
	{ClusterStateFailed, ClusterStateDeleteFailed}:       {Skip: true},
	{ClusterStateDeleting, ClusterStateDeleteFailed}:     {Skip: true},
	{ClusterStateDeleteFailed, ClusterStateDeleting}:     {Skip: true},
	{ClusterStateDeleting, ClusterStateFailed}:           {Skip: true},
	{ClusterStateDeleteFailed, ClusterStateFailed}:       {Skip: true},
	{ClusterStateUnspecified, ClusterStateFailed}:        {Skip: true},
	{ClusterStateUnspecified, ClusterStateDeleting}:      {Skip: true},
	{ClusterStateUnspecified, ClusterStateDeleteFailed}:  {Skip: true},
	{ClusterStateFailed, ClusterStateUnspecified}:        {Skip: true},
	{ClusterStateDeleting, ClusterStateUnspecified}:      {Skip: true},
	{ClusterStateDeleteFailed, ClusterStateUnspecified}:  {Skip: true},
}

func (m *clusterMapper) CloudEventType(eventType privatev1.EventType, previousState string) (string, error) {
	return ResolveCloudEventType(clusterTransitions, eventType, previousState, m.CurrentState())
}

func (m *clusterMapper) TransitionTime(eventType privatev1.EventType) (time.Time, error) {
	return ResolveTransitionTime(eventType,
		m.cl.GetMetadata().GetCreationTimestamp(),
		m.cl.GetMetadata().GetDeletionTimestamp(),
		m.cl.GetStatus().GetStateTransitionTime(),
		m.cl.GetId())
}

// IsClusterBillableState returns whether a ClusterOrder state string represents
// a billable state. Single source of truth — used by Watch Consumer, Heartbeat
// Generator, and Reconciler.
func IsClusterBillableState(state string) bool {
	return state == ClusterStateProgressing || state == ClusterStateReady
}

// ClusterBillingDimensions extracts billing dimensions from a Cluster proto,
// including the full component breakdown needed for N+1 decomposition.
// Node sets come from spec (desired state the tenant is billed for), not status.
func ClusterBillingDimensions(cl *privatev1.Cluster) map[string]any {
	dims := map[string]any{}
	spec := cl.GetSpec()
	if spec == nil {
		return dims
	}
	if t := spec.GetTemplate(); t != nil {
		dims["cluster_template"] = t.GetName()
	}
	if vn := spec.GetVersion().GetName(); vn != "" {
		dims[DimensionReleaseImage] = vn
	}

	// Use []any (not []map[string]any) so DecomposeClusterComponents' type
	// assertion works for both fresh dims and JSONB-round-tripped dims.
	components := []any{
		map[string]any{
			"node_set":   "_control_plane",
			"component":  "control_plane",
			"host_type":  "_control_plane",
			"node_count": int32(1),
		},
	}

	if nodeSets := spec.GetNodeSets(); nodeSets != nil {
		keys := make([]string, 0, len(nodeSets))
		for k := range nodeSets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ns := nodeSets[k]
			components = append(components, map[string]any{
				"node_set":   k,
				"component":  "worker",
				"host_type":  ns.GetHostType().GetName(),
				"node_count": ns.GetSize(),
			})
		}
	}

	dims["components"] = components
	return dims
}

// ComponentRecord represents one billing record in the N+1 decomposition.
type ComponentRecord struct {
	NodeSet         string
	Component       string
	HostType        string
	NodeCount       int32
	ClusterTemplate string
	ReleaseImage    string
	IsNew           bool
}

// FlatBillingDimensions returns per-component billing dimensions for a single
// CloudEvent record.
func (cr ComponentRecord) FlatBillingDimensions() map[string]any {
	dims := map[string]any{
		"cluster_template": cr.ClusterTemplate,
		"node_set":         cr.NodeSet,
		"component":        cr.Component,
		"host_type":        cr.HostType,
		"node_count":       cr.NodeCount,
	}
	if cr.ReleaseImage != "" {
		dims[DimensionReleaseImage] = cr.ReleaseImage
	}
	return dims
}

// DecomposeClusterComponents extracts N+1 component records from stored
// billing dimensions. Used by Watch Consumer, Heartbeat Generator, and
// Reconciler to fan out one cluster into per-component events. Returns
// ErrDataQuality if the components list itself is missing or malformed —
// per-component node_count is trusted as-is: fulfillment-service's API
// rejects non-positive node set sizes at write time (see
// PrivateClustersServer's node-set-size check), and ClusterBillingDimensions
// always populates node_count from that same validated field, so every
// caller-supplied value is already known good.
func DecomposeClusterComponents(billingDims map[string]any) ([]ComponentRecord, error) {
	clusterTemplate, _ := billingDims["cluster_template"].(string)
	releaseImage, _ := billingDims[DimensionReleaseImage].(string)

	componentsRaw, ok := billingDims["components"]
	if !ok {
		return nil, fmt.Errorf("%w: billing dimensions have no components", ErrDataQuality)
	}

	components, ok := componentsRaw.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: billing dimensions components is not a list (got %T)", ErrDataQuality, componentsRaw)
	}

	records := make([]ComponentRecord, 0, len(components))
	for _, c := range components {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		nodeSet, _ := cm["node_set"].(string)
		component, _ := cm["component"].(string)
		hostType, _ := cm["host_type"].(string)

		nc, _ := toFloat64(cm["node_count"])
		nodeCount := int32(nc)

		records = append(records, ComponentRecord{
			NodeSet:         nodeSet,
			Component:       component,
			HostType:        hostType,
			NodeCount:       nodeCount,
			ClusterTemplate: clusterTemplate,
			ReleaseImage:    releaseImage,
		})
	}

	return records, nil
}

// ComponentEventID derives a deterministic CloudEvent ID for a decomposed
// component event. Uses NodeSet as the unique key per cluster.
func ComponentEventID(baseEventID string, comp ComponentRecord) string {
	return fmt.Sprintf("%s/%s", baseEventID, comp.NodeSet)
}

// DecomposeClusterEvents fans out a single event into N+1 per-component events.
// Returns error if billing dimensions have no components (data quality issue).
// buildFn receives (per-component billing dimensions, deterministic event ID).
func DecomposeClusterEvents(billingDims map[string]any, baseID string, buildFn EventBuilder) ([]cloudevents.Event, error) {
	components, err := DecomposeClusterComponents(billingDims)
	if err != nil {
		return nil, err
	}
	if len(components) == 0 {
		return nil, fmt.Errorf("%w: cluster has no components in billing dimensions", ErrDataQuality)
	}

	result := make([]cloudevents.Event, 0, len(components))
	for _, comp := range components {
		ce, err := buildFn(comp.FlatBillingDimensions(), ComponentEventID(baseID, comp))
		if err != nil {
			return nil, err
		}
		result = append(result, ce)
	}
	return result, nil
}

// ChangedComponents compares old and new billing dimensions and returns
// component records that changed — including newly added or removed node
// sets. "Changed" is defined as "the component's billing-relevant wire
// representation differs" (via FlatBillingDimensions + DimensionsEqual),
// the same equality used to gate entry into this comparison in the first
// place — so this can never miss a field DimensionsEqual would catch.
// Removed components are returned with NodeCount=0.
func ChangedComponents(oldDims, newDims map[string]any) ([]ComponentRecord, error) {
	oldRecords, err := DecomposeClusterComponents(oldDims)
	if err != nil {
		return nil, err
	}
	newRecords, err := DecomposeClusterComponents(newDims)
	if err != nil {
		return nil, err
	}

	oldByKey := make(map[string]ComponentRecord, len(oldRecords))
	for _, r := range oldRecords {
		oldByKey[r.NodeSet] = r
	}

	newByKey := make(map[string]bool, len(newRecords))
	var changed []ComponentRecord
	for _, r := range newRecords {
		newByKey[r.NodeSet] = true
		old, exists := oldByKey[r.NodeSet]
		if !exists || !DimensionsEqual(old.FlatBillingDimensions(), r.FlatBillingDimensions()) {
			r.IsNew = !exists
			changed = append(changed, r)
		}
	}

	for _, r := range oldRecords {
		if !newByKey[r.NodeSet] {
			changed = append(changed, ComponentRecord{
				NodeSet:         r.NodeSet,
				Component:       r.Component,
				HostType:        r.HostType,
				NodeCount:       0,
				ClusterTemplate: r.ClusterTemplate,
				ReleaseImage:    r.ReleaseImage,
			})
		}
	}

	return changed, nil
}

// NextComponentBillableSince returns the per-component "billable since"
// timestamps for newDims: a component unchanged from oldDims carries forward
// its entry from oldSince, a new or changed component resets to now. Callers
// pass oldDims/oldSince as nil to force every current component to reset
// (e.g. a resource newly becoming billable, where any prior per-component
// history is no longer relevant). Resource types that don't decompose into
// components (no "components" key in newDims) return nil.
//
// Reuses ChangedComponents rather than re-implementing the old-vs-new diff,
// so this can never disagree with the changed-component set a caller
// actually publishes for the same transition.
func NextComponentBillableSince(oldDims map[string]any, oldSince map[string]time.Time, newDims map[string]any, now time.Time) map[string]time.Time {
	newRecords, err := DecomposeClusterComponents(newDims)
	if err != nil {
		return nil
	}

	// oldDims decomposition failing (e.g. first-ever creation, no prior
	// components) just means every current component is treated as changed
	// below — correct, since there is no prior state to carry forward.
	changed, _ := ChangedComponents(oldDims, newDims)
	changedSet := make(map[string]bool, len(changed))
	for _, c := range changed {
		changedSet[c.NodeSet] = true
	}

	since := make(map[string]time.Time, len(newRecords))
	for _, r := range newRecords {
		if !changedSet[r.NodeSet] {
			if t, ok := oldSince[r.NodeSet]; ok {
				since[r.NodeSet] = t
				continue
			}
		}
		since[r.NodeSet] = now
	}
	return since
}
