/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package usage_test

import (
	"testing"
	"time"

	"github.com/osac-project/osac-metering/internal/database"
)

// These tests pin the Cluster control-plane billing treatment of the deletion
// states (OSAC-4077): DELETING and DELETE_FAILED are never billable, so a
// teardown that stalls or fails must not keep billing the cluster, and the
// DELETING/DELETE_FAILED flapping produced by deprovision retries bills
// nothing either.

// TestClusterNeverBillsDuringTeardown checks that a READY cluster stops
// billing the moment deletion starts, through a failed deprovision and its
// retry, until the cluster is gone.
func TestClusterNeverBillsDuringTeardown(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("Cluster", "c-1", at(0)),
		state("Cluster", "c-1", at(0), "PROGRESSING"),
		state("Cluster", "c-1", at(time.Minute), "READY"),
		// The customer deletes the cluster; the deprovision then fails and is
		// retried, flapping the state between DELETING and DELETE_FAILED.
		state("Cluster", "c-1", at(3*time.Minute), "DELETING"),
		state("Cluster", "c-1", at(4*time.Minute), "DELETE_FAILED"),
		state("Cluster", "c-1", at(5*time.Minute), "DELETING"),
	}
	expectPieces(t, calculator, facts, at(10*time.Minute), []string{
		"Cluster control_plane 10:00:00.000000..10:03:00.000000 rate=1000000 micro=180000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\"}",
	})
}

// TestClusterDeleteFailedBeforeReadyBillsNothing checks the guardrail for a
// cluster deleted while it was still provisioning: no state on its timeline
// ever confirmed billability, so nothing bills even though DELETE_FAILED
// holds resources on the provider side.
func TestClusterDeleteFailedBeforeReadyBillsNothing(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("Cluster", "c-1", at(0)),
		state("Cluster", "c-1", at(0), "PROGRESSING"),
		state("Cluster", "c-1", at(time.Minute), "DELETING"),
		state("Cluster", "c-1", at(2*time.Minute), "DELETE_FAILED"),
	}
	expectPieces(t, calculator, facts, at(10*time.Minute), nil)
}

// TestClusterUnknownStateStopsBilling checks the version-skew guardrail: a
// state the shipped rules do not recognize (a newer producer reporting a new
// granular state, for example DEGRADED) ends billing instead of billing
// blindly, and billing resumes unchanged once a recognized state returns.
func TestClusterUnknownStateStopsBilling(t *testing.T) {
	calculator := defaultCalculator(t)
	facts := []database.Fact{
		created("Cluster", "c-1", at(0)),
		state("Cluster", "c-1", at(0), "PROGRESSING"),
		state("Cluster", "c-1", at(time.Minute), "READY"),
		// A state no rule in the current revision bills in.
		state("Cluster", "c-1", at(2*time.Minute), "DEGRADED"),
		state("Cluster", "c-1", at(3*time.Minute), "READY"),
	}
	expectPieces(t, calculator, facts, at(4*time.Minute), []string{
		"Cluster control_plane 10:00:00.000000..10:02:00.000000 rate=1000000 micro=120000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\"}",
		"Cluster control_plane 10:03:00.000000..10:04:00.000000 rate=1000000 micro=60000000 " +
			"pending=false dims={\"deployment\":\"us-east\",\"meter_type\":\"second\"," +
			"\"tenant\":\"acme\"}",
	})
}
