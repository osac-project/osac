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
	"fmt"
	"log/slog"
	"strconv"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
)

// lookupClusterVersionByName returns the ClusterVersion with the given metadata.name.
// Returns InvalidArgument if not found, Internal on lookup failure.
func lookupClusterVersionByName(
	ctx context.Context,
	logger *slog.Logger,
	clusterVersionsDao *dao.GenericDAO[*privatev1.ClusterVersion],
	versionName string,
) (*privatev1.ClusterVersion, error) {
	response, err := clusterVersionsDao.List().
		SetFilter(fmt.Sprintf("this.metadata.name == %s", strconv.Quote(versionName))).
		SetLimit(1).
		Do(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to retrieve cluster version",
			slog.String("version_name", versionName),
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal,
			"failed to retrieve cluster version '%s'", versionName)
	}
	if len(response.GetItems()) == 0 {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cluster version '%s' not found", versionName)
	}
	return response.GetItems()[0], nil
}

// validateClusterVersionUsability validates that a ClusterVersion is usable for cluster creation.
// Returns an error if the version is disabled, deleted, or in OBSOLETE state.
func validateClusterVersionUsability(cv *privatev1.ClusterVersion, versionName string) error {
	if cv.GetMetadata().HasDeletionTimestamp() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cluster version '%s' has been deleted", versionName)
	}
	if cv.GetSpec().HasEnabled() && !cv.GetSpec().GetEnabled() {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cluster version '%s' is disabled", versionName)
	}
	if cv.GetSpec().GetState() == privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cluster version '%s' is obsolete and cannot be used", versionName)
	}
	return nil
}

// lookupAndValidateClusterVersion looks up a ClusterVersion by metadata.name and validates it is usable.
func lookupAndValidateClusterVersion(
	ctx context.Context,
	logger *slog.Logger,
	clusterVersionsDao *dao.GenericDAO[*privatev1.ClusterVersion],
	versionName string,
) error {
	cv, err := lookupClusterVersionByName(ctx, logger, clusterVersionsDao, versionName)
	if err != nil {
		return err
	}
	return validateClusterVersionUsability(cv, versionName)
}

// buildClusterVersionReference creates a ClusterVersionReference from a ClusterVersion.
func buildClusterVersionReference(cv *privatev1.ClusterVersion) *privatev1.ClusterVersionReference {
	ref := &privatev1.ClusterVersionReference{}
	ref.SetId(cv.GetId())
	ref.SetName(cv.GetMetadata().GetName())
	return ref
}

// resolveDefaultClusterVersion looks up the system default ClusterVersion (spec.is_default == true),
// validates it is usable, and returns a ClusterVersionReference.
func resolveDefaultClusterVersion(
	ctx context.Context,
	logger *slog.Logger,
	clusterVersionsDao *dao.GenericDAO[*privatev1.ClusterVersion],
) (*privatev1.ClusterVersionReference, error) {
	response, err := clusterVersionsDao.List().
		SetFilter("this.spec.is_default == true && !has(this.metadata.deletion_timestamp)").
		SetLimit(1).
		Do(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to look up default cluster version",
			slog.Any("error", err),
		)
		return nil, grpcstatus.Errorf(grpccodes.Internal,
			"failed to look up default cluster version")
	}
	if len(response.GetItems()) == 0 {
		return nil, grpcstatus.Errorf(grpccodes.InvalidArgument,
			"no version specified and no system default version is configured")
	}
	if response.GetTotal() > 1 {
		logger.WarnContext(ctx, "multiple default ClusterVersions found, using first",
			slog.Int("count", int(response.GetTotal())),
		)
	}
	cv := response.GetItems()[0]
	versionName := cv.GetMetadata().GetName()
	if err := validateClusterVersionUsability(cv, versionName); err != nil {
		return nil, err
	}
	return buildClusterVersionReference(cv), nil
}
