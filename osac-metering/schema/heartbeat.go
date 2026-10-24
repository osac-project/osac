/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package schema

// HeartbeatType is the CloudEvent type of a metering heartbeat.
const HeartbeatType = "osac.resource.heartbeat.v1"

// HeartbeatData is the payload of a metering heartbeat CloudEvent. It is the difference the
// round sends: a quantity over an interval, positive or negative. Consumers add heartbeats up;
// they never replace one.
type HeartbeatData struct {
	// TenantID is the tenant the usage belongs to.
	TenantID string `json:"tenant_id"`

	// ResourceType and ResourceID name the resource the usage is about.
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`

	// Meter and Component name the meter the usage is about.
	Meter     string `json:"meter"`
	Component string `json:"component"`

	// BillingDimensions carry the meter's dimension values.
	BillingDimensions map[string]string `json:"billing_dimensions"`

	// Usage is the quantity over the interval. From is inclusive and to exclusive, in UTC.
	Usage Usage `json:"usage"`

	// HeartbeatTime is the round time that produced the heartbeat.
	HeartbeatTime string `json:"heartbeat_time"`

	// LastAppliedOffset is the event offset the round had folded.
	LastAppliedOffset int64 `json:"last_applied_offset"`

	// RulesRevision is the revision whose rules produced the heartbeat.
	RulesRevision string `json:"rules_revision"`
}

// ProtoResourceTypeToSchema maps the proto message name of a billable resource to its schema
// resource type constant.
func ProtoResourceTypeToSchema(protoName string) string {
	switch protoName {
	case "ComputeInstance":
		return ResourceTypeComputeInstance
	case "Cluster":
		return ResourceTypeClusterOrder
	case "BareMetalInstance":
		return ResourceTypeBareMetalInstance
	case "ExternalIP":
		return ResourceTypeExternalIP
	case "NATGateway":
		return ResourceTypeNATGateway
	case "Volume":
		return ResourceTypeVolume
	default:
		return protoName
	}
}

// SchemaResourceTypeToProto maps a schema resource type constant back to the proto message
// name of the resource.
func SchemaResourceTypeToProto(schemaName string) string {
	switch schemaName {
	case ResourceTypeComputeInstance:
		return "ComputeInstance"
	case ResourceTypeClusterOrder:
		return "Cluster"
	case ResourceTypeBareMetalInstance:
		return "BareMetalInstance"
	case ResourceTypeExternalIP:
		return "ExternalIP"
	case ResourceTypeNATGateway:
		return "NATGateway"
	case ResourceTypeVolume:
		return "Volume"
	default:
		return schemaName
	}
}
