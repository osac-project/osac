/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package mcpserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ResourceOutcomeResource struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Version int32  `json:"version"`
}

type ResourceOutcomeCondition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	LastTransitionTime string `json:"last_transition_time,omitempty"`
}

type ResourceOutcomeTiming struct {
	ObservedAt          string `json:"observed_at"`
	StateTransitionTime string `json:"state_transition_time,omitempty"`
	LastRestartedAt     string `json:"last_restarted_at,omitempty"`
}

type ResourceOutcomeFact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type GetResourceOutcomeInput struct {
	ResourceType ResourceType `json:"resource_type" jsonschema:"supported OSAC deployment resource type"`
	ResourceID   string       `json:"resource_id" jsonschema:"resource ID"`
}

type GetResourceOutcomeOutput struct {
	Resource       ResourceOutcomeResource    `json:"resource"`
	LifecycleState string                     `json:"lifecycle_state"`
	Conditions     []ResourceOutcomeCondition `json:"conditions"`
	Timing         ResourceOutcomeTiming      `json:"timing"`
	Facts          []ResourceOutcomeFact      `json:"facts"`
	Unknowns       []string                   `json:"unknowns"`
	NextActions    []string                   `json:"next_actions"`
}

func handleGetResourceOutcome(resources resourceRegistry) mcp.ToolHandlerFor[GetResourceOutcomeInput, GetResourceOutcomeOutput] {
	return func(
		ctx context.Context, req *mcp.CallToolRequest, input GetResourceOutcomeInput,
	) (*mcp.CallToolResult, GetResourceOutcomeOutput, error) {
		operations, ok := resources[input.ResourceType]
		if !ok {
			return nil, GetResourceOutcomeOutput{}, safeInvalidRequestError(unsupportedResourceTypeError(input.ResourceType))
		}
		if strings.TrimSpace(input.ResourceID) == "" {
			return nil, GetResourceOutcomeOutput{}, safeInvalidRequestError(
				errors.New("resource_id must not be empty"),
			)
		}
		if operations.outcome == nil {
			return nil, unknownResourceOutcome(input), nil
		}
		ctx, cancel := toolCallContext(ctx)
		defer cancel()
		ctx, err := forwardAPIToken(ctx, req)
		if err != nil {
			return nil, GetResourceOutcomeOutput{}, err
		}
		outcome, err := operations.outcome(ctx, input.ResourceID)
		if err != nil {
			return nil, GetResourceOutcomeOutput{}, safePublicAPIError(err)
		}
		return nil, outcome, nil
	}
}

func unknownResourceOutcome(input GetResourceOutcomeInput) GetResourceOutcomeOutput {
	outcome := newResourceOutcome(input.ResourceType, input.ResourceID, 0)
	outcome.LifecycleState = "unknown"
	outcome.Unknowns = []string{
		"resource_version",
		"lifecycle_state",
		"conditions",
		"timing",
		"facts",
	}
	outcome.NextActions = []string{"Use list_resources to inspect this resource type."}
	return outcome
}

func newResourceOutcome(resourceType ResourceType, id string, version int32) GetResourceOutcomeOutput {
	return GetResourceOutcomeOutput{
		Resource: ResourceOutcomeResource{
			Type:    string(resourceType),
			ID:      id,
			Version: version,
		},
		Conditions:  []ResourceOutcomeCondition{},
		Facts:       []ResourceOutcomeFact{},
		Unknowns:    []string{},
		NextActions: []string{},
		Timing: ResourceOutcomeTiming{
			ObservedAt: time.Now().UTC().Format(time.RFC3339),
		},
	}
}

func outcomeFromGet(
	resourceType ResourceType, get func(context.Context, string) (ResourceSummary, error),
) func(context.Context, string) (GetResourceOutcomeOutput, error) {
	return func(ctx context.Context, id string) (GetResourceOutcomeOutput, error) {
		summary, err := get(ctx, id)
		if err != nil {
			return GetResourceOutcomeOutput{}, err
		}
		return summaryOutcome(resourceType, summary), nil
	}
}

func summaryOutcome(resourceType ResourceType, summary ResourceSummary) GetResourceOutcomeOutput {
	outcome := newResourceOutcome(resourceType, summary.ID, summary.Version)
	outcome.LifecycleState = normalizeLifecycleState(summary.State)
	if summary.Type != "" {
		outcome.Facts = append(outcome.Facts, ResourceOutcomeFact{Name: "type", Value: summary.Type})
	}
	if outcome.LifecycleState == "unknown" {
		outcome.Unknowns = append(outcome.Unknowns, "lifecycle_state")
		outcome.NextActions = append(outcome.NextActions, "Call get_resource_outcome again after public state is reported.")
	}
	return outcome
}

func normalizeLifecycleState(state string) string {
	if state == "" || strings.HasSuffix(state, "_UNSPECIFIED") {
		return "unknown"
	}
	return state
}

func formatTimestamp(timestamp *timestamppb.Timestamp) string {
	if timestamp == nil || !timestamp.IsValid() {
		return ""
	}
	return timestamp.AsTime().UTC().Format(time.RFC3339)
}
