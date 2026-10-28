/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package schema

// SchemaVersion is the current version of the metering event schema.
const SchemaVersion = "v1"

const (
	UsageSemanticsInterval   = "interval"
	UsageSemanticsCumulative = "cumulative"
	UsageUnitGiByteSecond    = "gibibyte_second"
	UsagePrecisionSecond     = "second"
)

// Usage is the canonical usage record of a heartbeat. Timestamps are UTC and carry whole
// seconds: no source clock in the pipeline is finer (Kubernetes CR timestamps truncate to
// seconds), so finer digits would be false precision. Quantity is fixed-point text so JSON
// decoding cannot turn billing values into binary floating-point numbers.
type Usage struct {
	Semantics string `json:"semantics"`
	From      string `json:"from"`
	To        string `json:"to"`
	Quantity  string `json:"quantity"`
	Unit      string `json:"unit"`
	Precision string `json:"precision"`
}

// heartbeatDataFields holds the canonical field list of a heartbeat payload. Unexported to
// prevent mutation; callers use HeartbeatDataFields().
var heartbeatDataFields = [...]string{
	"tenant_id",
	"resource_type",
	"resource_id",
	"meter",
	"component",
	"heartbeat_time",
	"last_applied_offset",
	"rules_revision",
}

// HeartbeatDataFields returns the JSON field names of HeartbeatData in struct field order,
// excluding usage and billing_dimensions (the adapter flattens those separately). Returns a
// fresh copy on each call so callers cannot mutate the canonical list.
func HeartbeatDataFields() []string {
	out := make([]string, len(heartbeatDataFields))
	copy(out, heartbeatDataFields[:])
	return out
}
