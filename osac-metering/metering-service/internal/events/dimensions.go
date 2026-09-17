/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package events

import (
	"fmt"

	"github.com/osac-project/osac-metering/schema"
)

// ValidateBillingDimensions rejects incomplete billing dimensions before an
// event reaches Kafka. An empty project_id identifies the tenant default
// project; all other string dimensions must be non-empty.
func ValidateBillingDimensions(resourceType string, dimensions map[string]any) error {
	if resourceType == schema.ResourceTypeVolume {
		return validateVolumeBillingDimensions(dimensions)
	}
	return validateNetworkingBillingDimensions(resourceType, dimensions)
}

// DimensionsEqual compares two billing dimension maps for equality.
// Numeric values are compared as float64 to handle type differences
// from JSON round-tripping (int32 from proto, float64 from JSONB).
func DimensionsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !valueEqual(v, b[k]) {
			return false
		}
	}
	return true
}

func valueEqual(a, b any) bool {
	fa, aOK := toFloat64(a)
	fb, bOK := toFloat64(b)
	if aOK && bOK {
		return fa == fb
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
