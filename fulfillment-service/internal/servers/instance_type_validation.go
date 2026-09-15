/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"fmt"
	"time"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// validateInstanceTypeState looks up an instance type by name and validates its state.
// Returns warnings for DEPRECATED types, error for OBSOLETE or not-found types.
// The source parameter provides context for error messages (e.g., " in spec_defaults", " in fields.instance_type").
// Pass an empty string for source when validating directly on a ComputeInstance.
func validateInstanceTypeState(
	ctx context.Context,
	instanceTypesDao *dao.GenericDAO[*privatev1.InstanceType],
	instanceTypeName string,
	source string,
) ([]string, error) {
	getResponse, err := instanceTypesDao.Get().
		SetId(instanceTypeName).
		Do(ctx)
	if err != nil {
		var notFoundErr *dao.ErrNotFound
		if errors.As(err, &notFoundErr) {
			return nil, grpcstatus.Errorf(grpccodes.NotFound,
				"instance type '%s'%s not found", instanceTypeName, source)
		}
		return nil, grpcstatus.Errorf(grpccodes.Internal,
			"failed to retrieve instance type '%s'", instanceTypeName)
	}

	return validateResolvedInstanceType(getResponse.GetObject(), instanceTypeName, source)
}

// validateResolvedInstanceType validates lifecycle state and returns any deprecation warning for an existing type.
func validateResolvedInstanceType(it *privatev1.InstanceType, instanceTypeName, source string) ([]string, error) {
	if err := validateResourceNotDeleted("instance type", instanceTypeName, source, it.GetMetadata()); err != nil {
		return nil, err
	}
	state := it.GetSpec().GetState()
	var warnings []string

	switch state {
	case privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE:
		return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"instance type '%s'%s is obsolete and cannot be used",
			instanceTypeName, source)
	case privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED:
		warning := fmt.Sprintf("Instance type '%s'%s is deprecated", instanceTypeName, source)
		dep := it.GetSpec().GetDeprecation()
		if dep != nil {
			if dep.GetObsolescenceTimestamp() != nil {
				warning += fmt.Sprintf(" and will become obsolete on %s",
					dep.GetObsolescenceTimestamp().AsTime().Format(time.RFC3339))
			}
			if dep.GetReplacement() != nil {
				warning += fmt.Sprintf(". Consider using '%s' instead", refKey(dep.GetReplacement()))
			}
		}
		warnings = append(warnings, warning)
	}

	return warnings, nil
}
