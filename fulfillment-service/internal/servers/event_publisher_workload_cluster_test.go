/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"testing"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func TestDecorateWorkloadClusterEvents(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		status    string
		reason    string
		want      string
		severity  string
	}{
		{name: "registration", operation: eventPublisherOpInsert, want: "Registered", severity: "Normal"},
		{name: "available", operation: eventPublisherOpUpdate, status: "True", want: "Available", severity: "Normal"},
		{name: "unavailable", operation: eventPublisherOpUpdate, status: "False", want: "Unavailable", severity: "Warning"},
		{name: "identity mismatch", operation: eventPublisherOpUpdate, status: "False", reason: "ClusterIdentityMismatch", want: "IdentityMismatch", severity: "Warning"},
		{name: "reconnected", operation: eventPublisherOpSignal, want: "HubReconnected", severity: "Normal"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := privatev1.Event_builder{
				WorkloadCluster: privatev1.WorkloadCluster_builder{
					Status: privatev1.WorkloadClusterStatus_builder{
						Conditions: []*privatev1.WorkloadClusterCondition{
							privatev1.WorkloadClusterCondition_builder{Type: "Available", Status: test.status, Reason: test.reason}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build()

			(&EventPublisher{}).decorateWorkloadClusterEvent(event, test.operation)

			if event.GetReason() != test.want || event.GetSeverity() != test.severity {
				t.Fatalf("got reason=%q severity=%q, want reason=%q severity=%q", event.GetReason(), event.GetSeverity(), test.want, test.severity)
			}
		})
	}

	t.Run("preserves explicit metadata", func(t *testing.T) {
		event := privatev1.Event_builder{
			Reason:   "CustomReason",
			Severity: "Warning",
			WorkloadCluster: privatev1.WorkloadCluster_builder{
				Status: privatev1.WorkloadClusterStatus_builder{
					Conditions: []*privatev1.WorkloadClusterCondition{
						privatev1.WorkloadClusterCondition_builder{Type: "Available", Status: "True"}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build()

		(&EventPublisher{}).decorateWorkloadClusterEvent(event, eventPublisherOpUpdate)

		if event.GetReason() != "CustomReason" || event.GetSeverity() != "Warning" {
			t.Fatalf("got reason=%q severity=%q, want explicit metadata", event.GetReason(), event.GetSeverity())
		}
	})
}
