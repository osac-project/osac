/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package servers

import (
	"strings"
	"testing"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

func TestValidateNetworkingHubReferences(t *testing.T) {
	tests := []struct {
		name           string
		canonicalHub   string
		references     []networkingHubReference
		wantCode       grpccodes.Code
		wantMessage    []string
		notWantMessage []string
	}{
		{
			name:         "same hub references are accepted",
			canonicalHub: "hub-a",
			references: []networkingHubReference{
				{resourceType: "Subnet", id: "subnet-a", hubID: "hub-a"},
				{resourceType: "SecurityGroup", id: "sg-a", hubID: "hub-a"},
			},
		},
		{
			name:         "references on different hubs are rejected",
			canonicalHub: "hub-a",
			references: []networkingHubReference{
				{resourceType: "Subnet", id: "subnet-a", hubID: "hub-a"},
				{resourceType: "SecurityGroup", id: "sg-b", hubID: "hub-b"},
			},
			wantCode:       grpccodes.FailedPrecondition,
			wantMessage:    []string{"SecurityGroup", "sg-b", "different Hub than the canonical networking Hub"},
			notWantMessage: []string{"hub-a", "hub-b"},
		},
		{
			name:         "a single reference outside the canonical hub is rejected",
			canonicalHub: "hub-a",
			references: []networkingHubReference{
				{resourceType: "Subnet", id: "subnet-b", hubID: "hub-b"},
			},
			wantCode:       grpccodes.FailedPrecondition,
			wantMessage:    []string{"Subnet", "subnet-b", "different Hub than the canonical networking Hub"},
			notWantMessage: []string{"hub-a", "hub-b"},
		},
		{
			name:         "unassigned references are rejected",
			canonicalHub: "hub-a",
			references: []networkingHubReference{
				{resourceType: "Subnet", id: "subnet-a"},
			},
			wantCode:       grpccodes.FailedPrecondition,
			wantMessage:    []string{"Subnet", "subnet-a", "no Hub assignment", "canonical networking Hub"},
			notWantMessage: []string{"hub-a"},
		},
		{
			name:        "missing canonical hub is rejected",
			wantCode:    grpccodes.FailedPrecondition,
			wantMessage: []string{"canonical networking Hub", "not assigned"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateNetworkingHubReferences(test.canonicalHub, test.references...)
			if test.wantCode == grpccodes.OK {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if got := grpcstatus.Code(err); got != test.wantCode {
				t.Fatalf("expected gRPC code %s, got %s (%v)", test.wantCode, got, err)
			}
			for _, fragment := range test.wantMessage {
				if !strings.Contains(err.Error(), fragment) {
					t.Errorf("expected error %q to contain %q", err, fragment)
				}
			}
			for _, fragment := range test.notWantMessage {
				if strings.Contains(err.Error(), fragment) {
					t.Errorf("expected error %q not to contain %q", err, fragment)
				}
			}
		})
	}
}
