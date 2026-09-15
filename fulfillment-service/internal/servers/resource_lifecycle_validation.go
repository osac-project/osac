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
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// validateResourceNotDeleted rejects a resolved dependency whose lifecycle deletion has started.
func validateResourceNotDeleted(kind, identifier, source string, metadata *privatev1.Metadata) error {
	if metadata != nil && metadata.HasDeletionTimestamp() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s '%s'%s has been deleted", kind, identifier, source)
	}
	return nil
}

// validateResolvedStorageTier checks deletion and readiness of a resolved tier without choosing a backend.
func validateResolvedStorageTier(tier *privatev1.StorageTier, source string) error {
	if err := validateResourceNotDeleted("storage tier", tier.GetId(), source, tier.GetMetadata()); err != nil {
		return err
	}
	if tier.GetStatus().GetState() != privatev1.StorageTierState_STORAGE_TIER_STATE_ACTIVE {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "storage tier '%s'%s is not active", tier.GetId(), source)
	}
	return nil
}

// validateResolvedSubnetReady checks readiness after reference resolution.
func validateResolvedSubnetReady(subnet *privatev1.Subnet, identifier, source string) error {
	if subnet.GetStatus().GetState() != privatev1.SubnetState_SUBNET_STATE_READY {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "subnet '%s'%s is not in READY state", identifier, source)
	}
	return nil
}

// validateResolvedSecurityGroup checks readiness and membership in the attachment's virtual network.
func validateResolvedSecurityGroup(group *privatev1.SecurityGroup, identifier, source, virtualNetworkID string) error {
	if group.GetStatus().GetState() != privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "security group '%s'%s is not in READY state", identifier, source)
	}
	if virtualNetworkID != "" && virtualNetworkID != refKey(group.GetSpec().GetVirtualNetwork()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "security group '%s'%s belongs to a different virtual network", identifier, source)
	}
	return nil
}
