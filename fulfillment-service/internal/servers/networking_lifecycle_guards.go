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
	"maps"
	"strings"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
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
	err := forEachDAOPage(ctx, pageSize, func(ctx context.Context, offset, limit int32) ([]O, error) {
		page, err := dependentDao.List().SetLimit(limit).SetOffset(offset).Do(ctx)
		if err != nil {
			return nil, err
		}
		return page.GetItems(), nil
	}, func(dependent O) {
		if !references(dependent) {
			return
		}
		dependentCount++
		if firstDependentID == "" {
			firstDependentID = dependent.GetId()
			firstDependentName = dependent.GetId()
			if metadata := dependent.GetMetadata(); metadata != nil && metadata.GetName() != "" {
				firstDependentName = metadata.GetName()
			}
		}
	})
	if err != nil {
		if errors.Is(err, ErrDAOOffsetOverflow) {
			return grpcstatus.Errorf(grpccodes.Internal,
				"too many %s records to check %s references", dependentKind, parentKind)
		}
		if logger != nil {
			logger.ErrorContext(ctx, "Failed to check networking dependents",
				slog.String("parent_kind", parentKind),
				slog.String("parent_id", parentID),
				slog.String("dependent_kind", dependentKind),
				slog.Any("error", err))
		}
		return grpcstatus.Errorf(grpccodes.Internal, "failed to check %s references", parentKind)
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

// Lock order (UN, fulfillment service concurrency). Every create, update and delete takes its row locks in this
// order, so concurrent requests can't deadlock:
//
//  1. Tenant (lockTenantForCreate).
//  2. NetworkClass (lockNetworkClass): shared for VirtualNetwork and Cluster creates, exclusive for ExternalIPPool
//     creates and address plan writes.
//  3. Parent VirtualNetwork, then the child record.
//  4. ExternalIPPool, then ExternalIP.
//  5. Target workload, then ExternalIP, then ExternalIPAttachment.
//  6. Records of one kind by ID ascending.
//
// A deadlock that the database still detects fails the request with ABORTED.

// deletingStateNumber is the number of the DELETING value of every networking and workload state enum.
const deletingStateNumber protoreflect.EnumNumber = 4

// totalVisibilityTenancyLogic gives the DAOs used by deletion guards visibility of every tenant and project: a guard
// counts every dependent, also in projects the caller can't see and in the system tenant (CaaS workers).
type totalVisibilityTenancyLogic struct{}

func (totalVisibilityTenancyLogic) DetermineAssignableTenants(context.Context) (collections.Set[string], error) {
	return auth.AllTenants, nil
}

func (totalVisibilityTenancyLogic) DetermineDefaultTenant(context.Context) (string, error) {
	return auth.SystemTenant, nil
}

func (totalVisibilityTenancyLogic) DetermineVisibility(context.Context) (*auth.Visibility, error) {
	return auth.TotalVisibility(), nil
}

// newGuardDAO creates a DAO with total visibility. Use it only to read dependents in guards and gates, never to return
// objects to the caller.
func newGuardDAO[O dao.Object](logger *slog.Logger) (*dao.GenericDAO[O], error) {
	return dao.NewGenericDAO[O]().
		SetLogger(logger).
		SetTenancyLogic(totalVisibilityTenancyLogic{}).
		Build()
}

// deleteAlreadyAccepted reports whether a locked record already has a deletion timestamp. A Delete of such a record
// returns OK and changes nothing: no guard, no new deletion time and no new task.
func deleteAlreadyAccepted(metadata *privatev1.Metadata) bool {
	return metadata != nil && metadata.HasDeletionTimestamp()
}

// markDeleting sets status.state to DELETING and clears status.message of a locked record, in the transaction of its
// delete, so the API state sticks to DELETING once the delete is accepted.
func markDeleting[O dao.Object](ctx context.Context, objectDao *dao.GenericDAO[O], object O) error {
	setDeletingStatus(object)
	if _, err := objectDao.Update().SetObject(object).Do(ctx); err != nil {
		return translateLifecycleError(err)
	}
	return nil
}

func setDeletingStatus(object proto.Message) {
	message := object.ProtoReflect()
	statusField := message.Descriptor().Fields().ByName("status")
	if statusField == nil {
		return
	}
	status := message.Mutable(statusField).Message()
	if stateField := status.Descriptor().Fields().ByName("state"); stateField != nil &&
		status.Get(stateField).Enum() != deletingStateNumber {
		status.Set(stateField, protoreflect.ValueOfEnum(deletingStateNumber))
		// Metered kinds record the time of every state change, and it must move forward.
		if timeField := status.Descriptor().Fields().ByName("state_transition_time"); timeField != nil {
			now := time.Now()
			if status.Has(timeField) {
				previous := status.Get(timeField).Message().Interface().(*timestamppb.Timestamp).AsTime()
				if !now.After(previous) {
					now = previous.Add(time.Microsecond)
				}
			}
			status.Set(timeField, protoreflect.ValueOfMessage(timestamppb.New(now).ProtoReflect()))
		}
	}
	if messageField := status.Descriptor().Fields().ByName("message"); messageField != nil {
		status.Clear(messageField)
	}
}

func statusStateNumber(object proto.Message) protoreflect.EnumNumber {
	message := object.ProtoReflect()
	statusField := message.Descriptor().Fields().ByName("status")
	if statusField == nil || !message.Has(statusField) {
		return 0
	}
	status := message.Get(statusField).Message()
	stateField := status.Descriptor().Fields().ByName("state")
	if stateField == nil {
		return 0
	}
	return status.Get(stateField).Enum()
}

// rejectStateChangeWhileDeleting rejects a status write that moves a record whose delete was accepted to a state other
// than DELETING. status.message stays writable.
func rejectStateChangeWhileDeleting(current, candidate proto.Message, metadata *privatev1.Metadata) error {
	if !deleteAlreadyAccepted(metadata) {
		return nil
	}
	state := statusStateNumber(candidate)
	if state == deletingStateNumber || state == statusStateNumber(current) {
		return nil
	}
	return grpcstatus.Error(grpccodes.InvalidArgument, "status.state cannot change after delete was accepted")
}

// validateNetworkingStatusOnlyUpdate checks a private networking Update after the update mask was applied: the
// controllers may write status, finalizers and annotations, but spec, the other top-level fields, metadata.name and
// metadata.labels are immutable. The allowed paths (for example "spec.address_plan") are excluded from the check.
func validateNetworkingStatusOnlyUpdate(kind string, current, candidate proto.Message, allowed ...string) error {
	currentCopy := proto.Clone(current)
	candidateCopy := proto.Clone(candidate)
	for _, path := range append([]string{"id", "metadata", "status"}, allowed...) {
		clearMessagePath(currentCopy.ProtoReflect(), path)
		clearMessagePath(candidateCopy.ProtoReflect(), path)
	}
	if !proto.Equal(currentCopy, candidateCopy) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "spec is immutable; delete and recreate the %s", kind)
	}
	currentMetadata := metadataOf(current)
	candidateMetadata := metadataOf(candidate)
	if currentMetadata.GetName() != candidateMetadata.GetName() ||
		!maps.Equal(currentMetadata.GetLabels(), candidateMetadata.GetLabels()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "spec is immutable; delete and recreate the %s", kind)
	}
	return nil
}

func metadataOf(object proto.Message) *privatev1.Metadata {
	type withMetadata interface {
		GetMetadata() *privatev1.Metadata
	}
	if value, ok := object.(withMetadata); ok {
		return value.GetMetadata()
	}
	return nil
}

func clearMessagePath(message protoreflect.Message, path string) {
	names := strings.Split(path, ".")
	for i, name := range names {
		field := message.Descriptor().Fields().ByName(protoreflect.Name(name))
		if field == nil {
			return
		}
		if i == len(names)-1 {
			message.Clear(field)
			return
		}
		if field.Message() == nil || !message.Has(field) {
			return
		}
		message = message.Mutable(field).Message()
	}
}

// lockTenantForCreate locks the Tenant record of a create, so a create and the Tenant delete never both succeed, and
// rejects creates in a Tenant that is being deleted. Tenants without a record (system, shared) aren't locked.
func lockTenantForCreate(ctx context.Context, tenant string) error {
	if tenant == "" || tenant == auth.SystemTenant || tenant == auth.SharedTenant {
		return nil
	}
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return err
	}
	var deleting bool
	err = tx.QueryRow(ctx,
		`select deletion_timestamp <> 'epoch' from tenants where id = $1 for update`, tenant,
	).Scan(&deleting)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgerrcode.DeadlockDetected {
			return grpcstatus.Error(grpccodes.Aborted, "concurrent modification detected, please retry")
		}
		return grpcstatus.Errorf(grpccodes.Internal, "failed to lock tenant '%s'", tenant)
	}
	if deleting {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "tenant '%s' is being deleted", tenant)
	}
	return nil
}

// lockNetworkClass finds the deployment NetworkClass, including one being deleted, and locks its record: exclusively
// when exclusive is true, shared otherwise. It returns nil when there is no NetworkClass.
func lockNetworkClass(ctx context.Context, networkClassDao *dao.GenericDAO[*privatev1.NetworkClass],
	exclusive bool) (*privatev1.NetworkClass, error) {
	response, err := networkClassDao.List().SetLimit(2).Do(ctx)
	if err != nil {
		return nil, grpcstatus.Error(grpccodes.Internal, "failed to find the NetworkClass")
	}
	if len(response.GetItems()) == 0 {
		return nil, nil
	}
	if len(response.GetItems()) > 1 {
		return nil, grpcstatus.Error(grpccodes.FailedPrecondition, errMultipleActiveNetworkClasses.Error())
	}
	request := networkClassDao.Get().SetId(response.GetItems()[0].GetId())
	if exclusive {
		request.SetLock(true)
	} else {
		request.SetShareLock(true)
	}
	locked, err := request.Do(ctx)
	if err != nil {
		return nil, translateLifecycleError(err)
	}
	return locked.GetObject(), nil
}

// requireReadyNetworkClass locks the NetworkClass and checks the gate that VirtualNetwork, ExternalIPPool and Cluster
// creates share: the NetworkClass exists, is READY and isn't being deleted.
func requireReadyNetworkClass(ctx context.Context, networkClassDao *dao.GenericDAO[*privatev1.NetworkClass],
	exclusive bool) (*privatev1.NetworkClass, error) {
	networkClass, err := lockNetworkClass(ctx, networkClassDao, exclusive)
	if err != nil {
		return nil, err
	}
	if networkClass == nil {
		return nil, grpcstatus.Error(grpccodes.FailedPrecondition, "no NetworkClass is configured")
	}
	if networkClass.GetMetadata().HasDeletionTimestamp() {
		return nil, grpcstatus.Error(grpccodes.FailedPrecondition, "the NetworkClass is being deleted")
	}
	if state := networkClass.GetStatus().GetState(); state != privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY {
		return nil, grpcstatus.Errorf(grpccodes.FailedPrecondition,
			"the NetworkClass is not READY (current state: %s)", state)
	}
	return networkClass, nil
}

// isAutomatic reports whether a record is an automatic resource: creator system and both auto-created labels. Labels
// that a tenant sets never make a resource automatic.
func isAutomatic(metadata *privatev1.Metadata) bool {
	labels := metadata.GetLabels()
	return metadata.GetCreator() == systemCreator && labels[autoCreatedLabel] == "true" &&
		labels[autoCreatedForLabel] != ""
}

// isSystemCreate reports whether a create comes from the fulfillment controllers acting as the system.
func isSystemCreate(ctx context.Context, metadata *privatev1.Metadata) bool {
	return auth.IsControllerServiceAccount(ctx) && metadata.GetCreator() == systemCreator
}

// validateReservedCreate rejects tenant creates that set the default label or use a name prefix reserved for
// automatic resources. Run it on the prepared candidate, whose creator is set.
func validateReservedCreate(ctx context.Context, metadata *privatev1.Metadata, reservedPrefix string) error {
	if _, ok := metadata.GetLabels()[defaultLabel]; ok && !isSystemCreate(ctx, metadata) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "label %s is set only by the system", defaultLabel)
	}
	if reservedPrefix != "" && metadata.GetCreator() != systemCreator &&
		strings.HasPrefix(metadata.GetName(), reservedPrefix) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"name prefix '%s' is reserved for system-created resources", reservedPrefix)
	}
	return nil
}

// rejectDeleteIfAnyExists rejects a delete while any record of the dependent kind exists, including records being
// deleted. Use a guard DAO, so records in every tenant and project count.
func rejectDeleteIfAnyExists[O lifecycleResource](
	ctx context.Context,
	dependentDao *dao.GenericDAO[O],
	dependentKind string,
	parentKind string,
	parentID string,
) error {
	count, firstID, err := countRecords(ctx, dependentDao, "")
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to check %s references", parentKind)
	}
	if count == 0 {
		return nil
	}
	return grpcstatus.Errorf(grpccodes.FailedPrecondition,
		"cannot delete %s '%s': %d %s resource(s) still exist (including '%s')",
		parentKind, parentID, count, dependentKind, firstID)
}

// countRecords counts the records that match the filter (all records when it is empty) and returns the ID of the
// first one.
func countRecords[O dao.Object](ctx context.Context, objectDao *dao.GenericDAO[O], filter string) (int, string,
	error) {
	request := objectDao.List().SetLimit(1)
	if filter != "" {
		request.SetFilter(filter)
	}
	response, err := request.Do(ctx)
	if err != nil {
		return 0, "", err
	}
	if len(response.GetItems()) == 0 {
		return 0, "", nil
	}
	count := int(response.GetTotal())
	if count < 1 {
		count = len(response.GetItems())
	}
	return count, response.GetItems()[0].GetId(), nil
}
