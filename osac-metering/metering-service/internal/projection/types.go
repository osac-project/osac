/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package projection

import "time"

// MeterState tracks the current interval and the first interval for one meter.
// ActiveSince is cleared when the meter is suspended; FirstStartedAt is never
// cleared and selects started.v1 versus resumed.v1.
type MeterState struct {
	ActiveSince    *time.Time
	FirstStartedAt *time.Time
}

// BMaaSMeterState keeps allocation and consumption lifecycle state explicit.
type BMaaSMeterState struct {
	Allocation  MeterState
	Consumption MeterState
}

type ResourceState struct {
	ResourceID    string
	ResourceType  string
	TenantID      string
	ProjectID     string
	Deleted       bool
	CurrentState  string
	PreviousState string
	IsBillable    bool
	// EverBillable is true once this resource has been billable at least once,
	// ever, and stays true from then on -- distinguishes first-ever activation
	// (started.v1) from every later resume (resumed.v1). See StateContext.EverBillable.
	EverBillable  bool
	BillableSince *time.Time
	// ComponentBillableSince tracks, per node_set, when that component's
	// billing dimensions last changed. N+1 decomposed resources (ClusterOrder)
	// scale different node sets at different times — BillableSince alone
	// would understate duration_seconds for a component that didn't cause
	// the most recent reset. Keyed by ComponentRecord.NodeSet; absent for
	// resource types that don't decompose into components.
	ComponentBillableSince map[string]time.Time
	BMaaSMeterState        BMaaSMeterState
	LastHeartbeatAt        *time.Time
	TransitionTime         time.Time
	FulfillmentVersion     int32
	BillingDimensions      map[string]any
}
