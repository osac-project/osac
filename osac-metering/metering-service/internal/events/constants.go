/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package events

import "github.com/osac-project/osac-metering/schema"

// CloudEvent type constants — lifecycle events for stateful resources.
// Event type strings are local to this package; resource types and schema
// version live in the shared schema module.
const (
	EventCreated    = "osac.resource.created.v1"
	EventStarted    = "osac.resource.started.v1"
	EventResumed    = "osac.resource.resumed.v1"
	EventSuspended  = "osac.resource.suspended.v1"
	EventDeleted    = "osac.resource.deleted.v1"
	EventUpdated    = "osac.resource.updated.v1"
	EventHeartbeat  = "osac.resource.heartbeat.v1"
	EventCorrection = "osac.resource.correction.v1"
)

// CloudEvent type constants — MaaS inference (stateless, no lifecycle).
const (
	EventInferenceUsage = "osac.inference.usage.v1"
)

const ResourceTypeMaaSInference = schema.ResourceTypeMaaSInference

const SchemaVersion = schema.SchemaVersion
