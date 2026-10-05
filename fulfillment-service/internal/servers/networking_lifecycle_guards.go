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

package servers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type lifecycleResource interface {
	dao.Object
	GetMetadata() *privatev1.Metadata
}

func referenceMatches[T resourceRef](reference T, id string, metadata *privatev1.Metadata) bool {
	key := refKey(reference)
	if key == id {
		return true
	}
	return metadata != nil && metadata.GetName() != "" && key == metadata.GetName()
}

// lockLifecycleResource serializes a delete check with creates that lock their referenced parent before persisting.
func lockLifecycleResource[O lifecycleResource](
	ctx context.Context,
	resourceDao *dao.GenericDAO[O],
	id string,
	kind string,
) (O, error) {
	var zero O
	response, err := resourceDao.Get().SetId(id).SetLock(true).Do(ctx)
	if err != nil {
		if _, ok := errors.AsType[*dao.ErrNotFound](err); ok {
			return zero, grpcstatus.Errorf(grpccodes.NotFound, "%s '%s' not found", kind, id)
		}
		if denied, ok := errors.AsType[*dao.ErrDenied](err); ok {
			return zero, grpcstatus.Error(grpccodes.PermissionDenied, denied.Reason)
		}
		if _, ok := errors.AsType[*dao.ErrDeadlock](err); ok {
			return zero, grpcstatus.Error(grpccodes.Aborted, "concurrent modification detected, please retry")
		}
		return zero, grpcstatus.Errorf(grpccodes.Internal, "failed to lock %s '%s' for deletion", kind, id)
	}
	return response.GetObject(), nil
}

// rejectDeleteIfReferenced looks for unarchived dependents. A deletion timestamp does not make a
// dependent inactive: it may still be waiting for cleanup and must keep its references protected.
func rejectDeleteIfReferenced[O lifecycleResource](
	ctx context.Context,
	logger *slog.Logger,
	dependentDao *dao.GenericDAO[O],
	dependentKind string,
	parentKind string,
	parentID string,
	parentMetadata *privatev1.Metadata,
	references func(O) bool,
) error {
	const pageSize int32 = 1000
	var dependentCount int
	firstDependentID := ""
	firstDependentName := ""
	for offset := int32(0); ; offset += pageSize {
		page, err := dependentDao.List().SetLimit(pageSize).SetOffset(offset).Do(ctx)
		if err != nil {
			if logger != nil {
				logger.ErrorContext(ctx, "Failed to check networking dependents",
					slog.String("parent_kind", parentKind),
					slog.String("parent_id", parentID),
					slog.String("dependent_kind", dependentKind),
					slog.Any("error", err))
			}
			return grpcstatus.Errorf(grpccodes.Internal, "failed to check %s references", parentKind)
		}
		for _, dependent := range page.GetItems() {
			if !references(dependent) {
				continue
			}
			dependentCount++
			if firstDependentID == "" {
				firstDependentID = dependent.GetId()
				firstDependentName = dependent.GetId()
				if metadata := dependent.GetMetadata(); metadata != nil && metadata.GetName() != "" {
					firstDependentName = metadata.GetName()
				}
			}
		}
		if len(page.GetItems()) < int(pageSize) {
			break
		}
		if offset > int32(^uint32(0)>>1)-pageSize {
			return grpcstatus.Errorf(grpccodes.Internal,
				"too many %s records to check %s references", dependentKind, parentKind)
		}
	}
	if dependentCount == 0 {
		return nil
	}
	parentName := fmt.Sprintf("'%s'", parentID)
	if parentMetadata != nil && parentMetadata.GetName() != "" {
		if name := parentMetadata.GetName(); name != parentID {
			parentName = fmt.Sprintf("'%s' (id '%s')", name, parentID)
		}
	}
	dependentName := fmt.Sprintf("'%s'", firstDependentID)
	if firstDependentName != firstDependentID {
		dependentName = fmt.Sprintf("'%s' (id '%s')", firstDependentName, firstDependentID)
	}
	return grpcstatus.Errorf(grpccodes.FailedPrecondition,
		"cannot delete %s %s: %d active %s resource(s) still reference it (including %s)",
		parentKind, parentName, dependentCount, dependentKind, dependentName)
}
