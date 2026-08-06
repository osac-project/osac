/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package reconciliation

import (
	"testing"
)

func TestCorrectionDescription(t *testing.T) {
	tests := []struct {
		reason   CorrectionReason
		expected string
	}{
		{MissedCreation, "Resource found in fulfillment-service but missing from metering projection"},
		{StateDrift, "Resource state in fulfillment-service differs from metering projection"},
		{BillingDimensionsDrift, "Billing dimensions in fulfillment-service differ from metering projection"},
		{MissedDeletion, "Resource found in metering projection but missing from fulfillment-service"},
	}

	for _, tc := range tests {
		t.Run(string(tc.reason), func(t *testing.T) {
			desc, err := correctionDescription(tc.reason)
			if err != nil {
				t.Fatalf("unexpected error for reason %s: %v", tc.reason, err)
			}
			if desc != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, desc)
			}
		})
	}
}

func TestCorrectionDescriptionUnknownReason(t *testing.T) {
	_, err := correctionDescription("unknown_reason")
	if err == nil {
		t.Fatal("expected error for unknown correction reason, got nil")
	}
	expected := "unknown correction reason: unknown_reason"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}
