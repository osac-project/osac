/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package projection

import (
	"time"
)

type ResourceState struct {
	ResourceID         string
	ResourceType       string
	TenantID           string
	ProjectID          string
	CurrentState       string
	PreviousState      string
	IsBillable         bool
	BillableSince      *time.Time
	LastHeartbeatAt    *time.Time
	TransitionTime     time.Time
	FulfillmentVersion int32
	BillingDimensions  map[string]any
}
