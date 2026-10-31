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

type tenantMetadataObject interface {
	GetMetadata() *privatev1.Metadata
}

func validateTenantMatch(parentTenant string, referenced tenantMetadataObject,
	referencedKind, referencedID string) error {
	if referenced == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s '%s' does not exist", referencedKind, referencedID)
	}
	referencedMetadata := referenced.GetMetadata()
	if referencedMetadata == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s '%s' has no tenant", referencedKind, referencedID)
	}
	referencedTenant := referencedMetadata.GetTenant()
	if parentTenant != referencedTenant {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s '%s' belongs to tenant '%s', expected tenant '%s'",
			referencedKind, referencedID, referencedTenant, parentTenant)
	}
	return nil
}

func validateTenantOrShared(parentTenant string, referenced tenantMetadataObject,
	referencedKind, referencedID, sharedTenant string) error {
	if referenced == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s '%s' does not exist", referencedKind, referencedID)
	}
	referencedMetadata := referenced.GetMetadata()
	if referencedMetadata == nil {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s '%s' has no tenant", referencedKind, referencedID)
	}
	referencedTenant := referencedMetadata.GetTenant()
	if referencedTenant != sharedTenant && referencedTenant != parentTenant {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"%s '%s' belongs to tenant '%s', expected tenant '%s' or shared tenant '%s'",
			referencedKind, referencedID, referencedTenant, parentTenant, sharedTenant)
	}
	return nil
}
