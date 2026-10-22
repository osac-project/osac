/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestFabricDomainEventFilterCompiles(t *testing.T) {
	env, err := cel.NewEnv(
		cel.Types(&privatev1.Event{}),
		cel.Variable("event", cel.ObjectType("osac.private.v1.Event")),
		cel.Constant("EVENT_TYPE_OBJECT_UPDATED", cel.IntType, types.Int(privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED)),
	)
	if err != nil {
		t.Fatalf("creating private-event CEL environment: %v", err)
	}
	ast, issues := env.Compile(fabricDomainEventFilter)
	if issues != nil && issues.Err() != nil {
		t.Fatalf("compiling FabricDomain event filter: %v", issues.Err())
	}
	program, err := env.Program(ast)
	if err != nil {
		t.Fatalf("building FabricDomain event filter program: %v", err)
	}

	tests := []struct {
		name  string
		event *privatev1.Event
		want  bool
	}{
		{
			name: "unrelated virtual network update",
			event: privatev1.Event_builder{
				Type:           privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				VirtualNetwork: privatev1.VirtualNetwork_builder{}.Build(),
			}.Build(),
		},
		{
			name: "virtual network hub assignment",
			want: true,
			event: privatev1.Event_builder{
				Type: privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				VirtualNetwork: privatev1.VirtualNetwork_builder{
					Status: privatev1.VirtualNetworkStatus_builder{Hub: "hub-a"}.Build(),
				}.Build(),
			}.Build(),
		},
		{
			name: "virtual network deletion request",
			want: true,
			event: privatev1.Event_builder{
				Type: privatev1.EventType_EVENT_TYPE_OBJECT_UPDATED,
				VirtualNetwork: privatev1.VirtualNetwork_builder{
					Metadata: privatev1.Metadata_builder{DeletionTimestamp: timestamppb.Now()}.Build(),
				}.Build(),
			}.Build(),
		},
		{
			name:  "unrelated hub creation",
			event: privatev1.Event_builder{Hub: privatev1.Hub_builder{}.Build()}.Build(),
		},
		{
			name:  "fabric domain event",
			want:  true,
			event: privatev1.Event_builder{FabricDomain: privatev1.FabricDomain_builder{}.Build()}.Build(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, _, err := program.Eval(map[string]any{"event": tt.event})
			if err != nil {
				t.Fatalf("evaluating FabricDomain event filter: %v", err)
			}
			got, ok := value.(types.Bool)
			if !ok {
				t.Fatalf("event filter returned %T, want bool", value)
			}
			if bool(got) != tt.want {
				t.Fatalf("event filter returned %t, want %t", got, tt.want)
			}
		})
	}
}
