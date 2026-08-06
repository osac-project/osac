/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package events

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	privatev1 "github.com/osac-project/osac-metering/internal/api/osac/private/v1"
)

var ErrSkipNonBillingTransition = errors.New("non-billing transition")

const ClusterStatePrefix = "CLUSTER_STATE_"

type clusterMapper struct {
	cl *privatev1.Cluster
}

func (m *clusterMapper) ResourceType() string { return "cluster_order" }
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

func (m *clusterMapper) CloudEventType(eventType privatev1.EventType, previousState string) (string, error) {
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

// CaaS billing model: both PROGRESSING and READY are billable. Transitions
// between them have no billing boundary — the interval continues. Dimension
// changes (scaling) during such transitions are detected by the Watch Consumer
// after receiving ErrSkipNonBillingTransition; it checks DimensionsEqual and
// emits updated.v1 for changed components. If the consumer misses a dimension
// change during PROGRESSING<->READY (unlikely — scaling during provisioning),
// the hourly reconciler detects billing_dimensions_drift and emits a
// correction event, bounding the gap to one reconciliation cycle.
func (m *clusterMapper) resolveUpdatedEventType(previousState string) (string, error) {
	currentState := m.CurrentState()
	currentBillable := IsClusterBillableState(currentState)
	previousBillable := IsClusterBillableState(previousState)

	switch {
	case currentBillable && previousState == "":
		return "osac.resource.started.v1", nil
	case currentBillable && !previousBillable:
		return "osac.resource.resumed.v1", nil
	case !currentBillable && previousBillable:
		return "osac.resource.suspended.v1", nil
	case currentBillable && previousBillable:
		return "", ErrSkipNonBillingTransition
	case !currentBillable && !previousBillable:
		return "", ErrSkipNonBillingTransition
	default:
		return "", fmt.Errorf("unexpected cluster state transition: %s -> %s", previousState, currentState)
	}
}

func (m *clusterMapper) TransitionTime(eventType privatev1.EventType) (time.Time, error) {
	switch eventType {
	case privatev1.EventType_EVENT_TYPE_OBJECT_CREATED:
		if md := m.cl.GetMetadata(); md != nil {
			if ct := md.GetCreationTimestamp(); ct != nil {
				return ct.AsTime(), nil
			}
		}
		return time.Time{}, fmt.Errorf("%w: cluster %s has no creation_timestamp", ErrDataQuality, m.cl.GetId())

	case privatev1.EventType_EVENT_TYPE_OBJECT_DELETED:
		if md := m.cl.GetMetadata(); md != nil {
			if dt := md.GetDeletionTimestamp(); dt != nil {
				return dt.AsTime(), nil
			}
		}
		return time.Time{}, fmt.Errorf("%w: cluster %s has no deletion_timestamp", ErrDataQuality, m.cl.GetId())

	default:
		if s := m.cl.GetStatus(); s != nil {
			if t := s.GetStateTransitionTime(); t != nil {
				return t.AsTime(), nil
			}
		}
		return time.Time{}, fmt.Errorf("%w: cluster %s has no state_transition_time", ErrDataQuality, m.cl.GetId())
	}
}

// IsClusterBillableState returns whether a ClusterOrder state string represents
// a billable state. Single source of truth — used by Watch Consumer, Heartbeat
// Generator, and Reconciler.
func IsClusterBillableState(state string) bool {
	return state == "PROGRESSING" || state == "READY"
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
	if vn := spec.GetVersionName(); vn != "" {
		dims["version_name"] = vn
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
				"host_type":  ns.GetHostType(),
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
		dims["release_image"] = cr.ReleaseImage
	}
	return dims
}

// DecomposeClusterComponents extracts N+1 component records from stored
// billing dimensions. Used by Watch Consumer, Heartbeat Generator, and
// Reconciler to fan out one cluster into per-component events.
func DecomposeClusterComponents(billingDims map[string]any) []ComponentRecord {
	clusterTemplate, _ := billingDims["cluster_template"].(string)
	releaseImage, _ := billingDims["release_image"].(string)

	componentsRaw, ok := billingDims["components"]
	if !ok {
		return nil
	}

	components, ok := componentsRaw.([]any)
	if !ok {
		return nil
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

		var nodeCount int32
		if nc, ok := toFloat64(cm["node_count"]); ok {
			nodeCount = int32(nc)
		}

		records = append(records, ComponentRecord{
			NodeSet:         nodeSet,
			Component:       component,
			HostType:        hostType,
			NodeCount:       nodeCount,
			ClusterTemplate: clusterTemplate,
			ReleaseImage:    releaseImage,
		})
	}

	return records
}

// ComponentEventID derives a deterministic CloudEvent ID for a decomposed
// component event. Uses NodeSet as the unique key per cluster.
func ComponentEventID(baseEventID string, comp ComponentRecord) string {
	return fmt.Sprintf("%s/%s", baseEventID, comp.NodeSet)
}

// ChangedComponents compares old and new billing dimensions and returns
// component records that changed: node_count differs, newly added, or removed.
// Removed components are returned with NodeCount=0.
func ChangedComponents(oldDims, newDims map[string]any) []ComponentRecord {
	oldRecords := DecomposeClusterComponents(oldDims)
	newRecords := DecomposeClusterComponents(newDims)

	oldByKey := make(map[string]ComponentRecord, len(oldRecords))
	for _, r := range oldRecords {
		oldByKey[r.NodeSet] = r
	}

	newByKey := make(map[string]bool, len(newRecords))
	var changed []ComponentRecord
	for _, r := range newRecords {
		newByKey[r.NodeSet] = true
		old, exists := oldByKey[r.NodeSet]
		if !exists || old.NodeCount != r.NodeCount {
			changed = append(changed, r)
		}
	}

	for _, r := range oldRecords {
		if !newByKey[r.NodeSet] {
			changed = append(changed, ComponentRecord{
				Component:       r.Component,
				HostType:        r.HostType,
				NodeCount:       0,
				ClusterTemplate: r.ClusterTemplate,
				ReleaseImage:    r.ReleaseImage,
			})
		}
	}

	return changed
}
