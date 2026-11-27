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
	"fmt"

	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

func typedResourceOutcome[
	Request any,
	Response resourceGetResponse[Object],
	Object proto.Message,
](
	label string,
	get func(context.Context, *Request) (Response, error),
	buildRequest func(string) *Request,
	outcome func(Object) GetResourceOutcomeOutput,
) func(context.Context, string) (GetResourceOutcomeOutput, error) {
	return func(ctx context.Context, id string) (GetResourceOutcomeOutput, error) {
		response, err := get(ctx, buildRequest(id))
		if err != nil {
			return GetResourceOutcomeOutput{}, fmt.Errorf("failed to get %s %q: %w", label, id, err)
		}
		return outcome(response.GetObject()), nil
	}
}

func computeInstanceOutcome(item *publicv1.ComputeInstance) GetResourceOutcomeOutput {
	outcome := newResourceOutcome(ResourceTypeComputeInstance, item.GetId(), item.GetMetadata().GetVersion())
	status := item.GetStatus()
	outcome.LifecycleState = "unknown"
	outcome.Timing.StateTransitionTime = formatTimestamp(status.GetStateTransitionTime())
	outcome.Timing.LastRestartedAt = formatTimestamp(status.GetLastRestartedAt())
	appendStringFact(&outcome, "internal_ip_address", status.GetInternalIpAddress())
	appendStringFact(&outcome, "external_ip_address", status.GetExternalIpAddress())
	if status.GetInternalIpAddress() == "" {
		outcome.Unknowns = append(outcome.Unknowns, "internal_ip_address")
	}
	if status.GetExternalIpAddress() == "" {
		outcome.Unknowns = append(outcome.Unknowns, "external_ip_address")
	}
	if status.GetInstanceType() != nil {
		appendStringFact(&outcome, "instance_type_id", status.GetInstanceType().GetId())
	}
	for _, attachment := range status.GetComputeNetworkAttachmentStatuses() {
		appendStringFact(&outcome, "network_attachment_subnet_id", attachment.GetSubnetRef())
		appendStringFact(&outcome, "network_attachment_ip_address", attachment.GetIpAddress())
	}
	outcome.Unknowns = append(outcome.Unknowns, "readiness_freshness")
	if outcome.Timing.StateTransitionTime == "" {
		outcome.Unknowns = append(outcome.Unknowns, "state_transition_time")
	}
	return outcome
}

func virtualNetworkOutcome(item *publicv1.VirtualNetwork) GetResourceOutcomeOutput {
	outcome := newResourceOutcome(ResourceTypeVirtualNetwork, item.GetId(), item.GetMetadata().GetVersion())
	status := item.GetStatus()
	outcome.LifecycleState = normalizeLifecycleState(status.GetState().String())
	appendStringFact(&outcome, "ipv4_cidr", item.GetSpec().GetIpv4Cidr())
	if status.GetState() == 0 {
		outcome.Unknowns = append(outcome.Unknowns, "lifecycle_state")
	}
	return outcome
}

func subnetOutcome(item *publicv1.Subnet) GetResourceOutcomeOutput {
	outcome := newResourceOutcome(ResourceTypeSubnet, item.GetId(), item.GetMetadata().GetVersion())
	status := item.GetStatus()
	outcome.LifecycleState = normalizeLifecycleState(status.GetState().String())
	appendStringFact(&outcome, "ipv4_cidr", item.GetSpec().GetIpv4Cidr())
	if item.GetSpec().GetVirtualNetwork() != nil {
		appendStringFact(&outcome, "virtual_network_id", item.GetSpec().GetVirtualNetwork().GetId())
	}
	if status.GetState() == 0 {
		outcome.Unknowns = append(outcome.Unknowns, "lifecycle_state")
	}
	return outcome
}

func securityGroupOutcome(item *publicv1.SecurityGroup) GetResourceOutcomeOutput {
	outcome := newResourceOutcome(ResourceTypeSecurityGroup, item.GetId(), item.GetMetadata().GetVersion())
	status := item.GetStatus()
	outcome.LifecycleState = normalizeLifecycleState(status.GetState().String())
	if item.GetSpec().GetVirtualNetwork() != nil {
		appendStringFact(&outcome, "virtual_network_id", item.GetSpec().GetVirtualNetwork().GetId())
	}
	appendStringFact(&outcome, "ingress_rule_count", fmt.Sprintf("%d", len(item.GetSpec().GetIngress())))
	appendStringFact(&outcome, "egress_rule_count", fmt.Sprintf("%d", len(item.GetSpec().GetEgress())))
	if status.GetState() == 0 {
		outcome.Unknowns = append(outcome.Unknowns, "lifecycle_state")
	}
	return outcome
}

func appendStringFact(outcome *GetResourceOutcomeOutput, name, value string) {
	if value == "" {
		return
	}
	outcome.Facts = append(outcome.Facts, ResourceOutcomeFact{Name: name, Value: value})
}
