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
	"context"
	"errors"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// referenceScope identifies the tenant and project used for name lookup and local-reference checks.
type referenceScope struct {
	tenant  string
	project string
}

// referenceResource is a persisted DAO object whose metadata supplies dependency ownership.
type referenceResource interface {
	dao.Object
	GetMetadata() *privatev1.Metadata
}

// resourceReference provides mutable ID/name identity for local and full resource references.
type resourceReference interface {
	GetId() string
	GetName() string
	SetId(string)
	SetName(string)
}

// fullResourceReference adds shared-tenant and project selectors to resource identity.
type fullResourceReference interface {
	resourceReference
	GetShared() bool
	GetProject() string
	SetShared(bool)
	SetProject(string)
}

// catalogItemScope returns a Catalog Item's prepared ownership scope; missing metadata produces an empty scope.
func catalogItemScope(item catalogItem) referenceScope {
	metadata := item.GetMetadata()
	if metadata == nil {
		return referenceScope{}
	}
	return referenceScope{tenant: metadata.GetTenant(), project: metadata.GetProject()}
}

// resolveAndCanonicalizeReference resolves a dependency relative to its owner, rejects deleted objects, and
// updates the supplied reference with stored identity. It returns the resolved object for lifecycle checks.
// Full-reference IDs select caller-visible objects; names use owner/explicit scope, while local
// references require exact tenant/project ownership. Call with a detached reference and the request
// transaction in context. Readiness and resource-specific compatibility remain the caller's responsibility.
func resolveAndCanonicalizeReference[O referenceResource](
	ctx context.Context,
	resourceDao *dao.GenericDAO[O],
	ownerMetadata *privatev1.Metadata,
	reference resourceReference,
	kind string,
	notFoundCode grpccodes.Code,
) (O, error) {
	var zero O
	if ownerMetadata == nil {
		return zero, grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"cannot resolve %s reference without owner metadata", kind,
		)
	}

	ownerScope := referenceScope{tenant: ownerMetadata.GetTenant(), project: ownerMetadata.GetProject()}
	var object O
	var err error
	if fullReference, ok := reference.(fullResourceReference); ok {
		object, err = resolveFullResourceReference(
			ctx, resourceDao, ownerScope, fullReference, kind, "", notFoundCode,
		)
	} else {
		object, err = resolveResourceInScope(
			ctx, resourceDao, ownerScope, reference.GetId(), reference.GetName(), kind, "", notFoundCode,
		)
	}
	if err != nil {
		return object, err
	}
	if err := validateResourceNotDeleted(kind, object.GetId(), "", object.GetMetadata()); err != nil {
		return object, err
	}

	reference.SetId(object.GetId())
	reference.SetName(object.GetMetadata().GetName())
	if fullReference, ok := reference.(fullResourceReference); ok {
		fullReference.SetProject(object.GetMetadata().GetProject())
		fullReference.SetShared(object.GetMetadata().GetTenant() == auth.SharedTenant)
	}
	return object, nil
}

// resolveFullResourceReference returns a locked object subject to caller visibility and dependency ownership.
// IDs identify the object independently of shared/project selectors; a supplied name must still match.
// Name-only references use the owner scope or explicit selectors. The request transaction owns
// the lock. The caller remains responsible for deletion/readiness checks and canonicalization.
func resolveFullResourceReference[O referenceResource](
	ctx context.Context,
	resourceDao *dao.GenericDAO[O],
	ownerScope referenceScope,
	reference fullResourceReference,
	kind string,
	source string,
	notFoundCode grpccodes.Code,
) (O, error) {
	if reference.GetId() == "" {
		scope := selectedReferenceScope(ownerScope, reference.GetShared(), reference.GetProject())
		object, err := resolveResourceInScope(
			ctx, resourceDao, scope, "", reference.GetName(), kind, source, notFoundCode,
		)
		if err == nil {
			err = validateDependencyOwnerScope(ownerScope, object.GetMetadata(), kind, source)
		}
		return object, err
	}

	identifier := referenceIdentifier(reference.GetId(), reference.GetName())
	object, err := getLockedResource(ctx, resourceDao, reference.GetId())
	if err != nil {
		return object, resourceLookupError(err, kind, identifier, source, notFoundCode)
	}
	metadata := object.GetMetadata()
	if metadata == nil {
		var zero O
		return zero, grpcstatus.Errorf(grpccodes.Internal, "resolved %s '%s' has no metadata", kind, identifier)
	}
	if reference.GetName() != "" && metadata.GetName() != reference.GetName() {
		var zero O
		return zero, grpcstatus.Errorf(
			grpccodes.InvalidArgument,
			"%s reference%s: id and name do not refer to the same resource",
			kind, source,
		)
	}
	if err := validateDependencyOwnerScope(ownerScope, metadata, kind, source); err != nil {
		var zero O
		return zero, err
	}
	return object, nil
}

// resolveResourceInScope returns a visible object locked in one exact tenant/project scope.
// An ID takes precedence for lookup, but a supplied name must match the stored name.
// The context must contain the request transaction, which owns the lock until completion.
// This function neither changes a reference nor checks deletion/readiness; callers do those checks.
func resolveResourceInScope[O referenceResource](
	ctx context.Context,
	resourceDao *dao.GenericDAO[O],
	scope referenceScope,
	id, name, kind, source string,
	notFoundCode grpccodes.Code,
) (O, error) {
	var zero O
	if id == "" && name == "" {
		return zero, grpcstatus.Errorf(grpccodes.InvalidArgument, "%s reference%s must specify id or name", kind, source)
	}
	// Read by ID when available, then check the supplied name against the same object.
	lookupName := name
	if id != "" {
		lookupName = ""
	}
	identifier := referenceIdentifier(id, name)
	ref, err := references.NewScopedDAOLookupFunc(resourceDao)(ctx, scope.tenant, scope.project, id, lookupName)
	if err != nil {
		var notFound interface{ IsNotFound() bool }
		if errors.As(err, &notFound) && notFound.IsNotFound() {
			return zero, referenceNotFoundError(notFoundCode, kind, identifier, source)
		}
		return zero, resourceLookupError(err, kind, identifier, source, notFoundCode)
	}
	object, err := getLockedResource(ctx, resourceDao, ref.ID)
	if err != nil {
		return zero, resourceLookupError(err, kind, identifier, source, notFoundCode)
	}
	metadata := object.GetMetadata()
	if metadata == nil {
		return zero, grpcstatus.Errorf(grpccodes.Internal, "resolved %s '%s' has no metadata", kind, identifier)
	}
	if metadata.GetTenant() != scope.tenant || metadata.GetProject() != scope.project {
		return zero, referenceNotFoundError(notFoundCode, kind, identifier, source)
	}
	if id != "" && name != "" && metadata.GetName() != name {
		return zero, grpcstatus.Errorf(grpccodes.InvalidArgument, "%s reference%s: id and name do not refer to the same resource", kind, source)
	}
	return object, nil
}

// selectedReferenceScope applies name-lookup selectors to the owner scope.
// An omitted project retains the owner project, including when shared is selected.
func selectedReferenceScope(scope referenceScope, shared bool, project string) referenceScope {
	result := scope
	if shared {
		result.tenant = auth.SharedTenant
	}
	if project != "" {
		result.project = project
	}
	return result
}

// inheritReferenceScope fills omitted selectors using the Template that supplied a full reference.
// It mutates only selectors, preserving explicit shared/project choices before resource-side resolution.
func inheritReferenceScope(ref interface {
	GetShared() bool
	SetShared(bool)
	GetProject() string
	SetProject(string)
}, owner *privatev1.Metadata) {
	if ref.GetShared() {
		return
	}
	ref.SetShared(owner.GetTenant() == auth.SharedTenant)
	if ref.GetProject() == "" {
		ref.SetProject(owner.GetProject())
	}
}

// validateDependencyOwnerScope rejects dependencies outside the owner tenant and shared tenant,
// even when the caller can see them. A shared owner can therefore depend only on shared objects.
// Full references may cross projects; local-reference resolution checks exact project separately.
func validateDependencyOwnerScope(owner referenceScope, target *privatev1.Metadata, kind, source string) error {
	if target.GetTenant() != auth.SharedTenant && target.GetTenant() != owner.tenant {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s reference%s must belong to the owning tenant or shared tenant", kind, source)
	}
	return nil
}

// getLockedResource retrieves a caller-visible object by ID and locks its row in the context's request transaction.
// The lock lasts until commit or rollback, preventing deletion between validation and persistence.
func getLockedResource[O dao.Object](ctx context.Context, resourceDao *dao.GenericDAO[O], id string) (O, error) {
	response, err := resourceDao.Get().SetId(id).SetLock(true).Do(ctx)
	if err != nil {
		var zero O
		return zero, err
	}
	return response.GetObject(), nil
}

// referenceIdentifier chooses the supplied ID, or otherwise the name, for diagnostic messages.
func referenceIdentifier(id, name string) string {
	if id != "" {
		return id
	}
	return name
}

// referenceNotFoundError returns the caller-selected status code with the reference identity and diagnostic location.
func referenceNotFoundError(code grpccodes.Code, kind, identifier, source string) error {
	return grpcstatus.Errorf(code, "%s '%s'%s not found", kind, identifier, source)
}

// resourceLookupError translates DAO lookup, visibility, and lock failures into gRPC status errors.
// The source suffix identifies the referencing field; unexpected failures do not expose DAO details.
func resourceLookupError(err error, kind, identifier, source string, notFoundCode grpccodes.Code) error {
	var notFoundErr *dao.ErrNotFound
	if errors.As(err, &notFoundErr) {
		return referenceNotFoundError(notFoundCode, kind, identifier, source)
	}
	var deniedErr *dao.ErrDenied
	if errors.As(err, &deniedErr) {
		return grpcstatus.Errorf(grpccodes.PermissionDenied, "%s", deniedErr.Reason)
	}
	var deadlockErr *dao.ErrDeadlock
	if errors.As(err, &deadlockErr) {
		return grpcstatus.Errorf(grpccodes.Aborted, "%s", deadlockErr.Error())
	}
	return grpcstatus.Errorf(grpccodes.Internal, "failed to retrieve %s '%s'%s", kind, identifier, source)
}

// canonicalComputeInstanceTemplateReference builds a new canonical reference from a resolved object's stored identity.
// It performs no lookup or validation and does not mutate the object.
func canonicalComputeInstanceTemplateReference(resolved *privatev1.ComputeInstanceTemplate) *privatev1.ComputeInstanceTemplateReference {
	return privatev1.ComputeInstanceTemplateReference_builder{
		Id:      resolved.GetId(),
		Name:    resolved.GetMetadata().GetName(),
		Project: resolved.GetMetadata().GetProject(),
		Shared:  resolved.GetMetadata().GetTenant() == auth.SharedTenant,
	}.Build()
}

// canonicalClusterTemplateReference builds a new canonical reference from a resolved object's stored identity.
// It performs no lookup or validation and does not mutate the object.
func canonicalClusterTemplateReference(resolved *privatev1.ClusterTemplate) *privatev1.ClusterTemplateReference {
	return privatev1.ClusterTemplateReference_builder{
		Id:      resolved.GetId(),
		Name:    resolved.GetMetadata().GetName(),
		Project: resolved.GetMetadata().GetProject(),
		Shared:  resolved.GetMetadata().GetTenant() == auth.SharedTenant,
	}.Build()
}

// canonicalInstanceTypeReference builds a new canonical reference from a resolved object's stored identity.
// It performs no lookup or validation and does not mutate the object.
func canonicalInstanceTypeReference(resolved *privatev1.InstanceType) *privatev1.InstanceTypeReference {
	return privatev1.InstanceTypeReference_builder{
		Id:      resolved.GetId(),
		Name:    resolved.GetMetadata().GetName(),
		Project: resolved.GetMetadata().GetProject(),
		Shared:  resolved.GetMetadata().GetTenant() == auth.SharedTenant,
	}.Build()
}

// canonicalDiskImageReference builds a new canonical reference from a resolved object's stored identity.
// It performs no lookup or validation and does not mutate the object.
func canonicalDiskImageReference(resolved *privatev1.DiskImage) *privatev1.DiskImageReference {
	return privatev1.DiskImageReference_builder{
		Id:      resolved.GetId(),
		Name:    resolved.GetMetadata().GetName(),
		Project: resolved.GetMetadata().GetProject(),
		Shared:  resolved.GetMetadata().GetTenant() == auth.SharedTenant,
	}.Build()
}

// canonicalStorageTierReference builds a new canonical reference from a resolved object's stored identity.
// It performs no lookup or validation and does not mutate the object.
func canonicalStorageTierReference(resolved *privatev1.StorageTier) *privatev1.StorageTierReference {
	return privatev1.StorageTierReference_builder{Id: resolved.GetId(), Name: resolved.GetMetadata().GetName()}.Build()
}

// canonicalSecretLocalReference builds a new canonical reference from a resolved object's stored identity.
// It performs no lookup or validation and does not mutate the object.
func canonicalSecretLocalReference(resolved *privatev1.Secret) *privatev1.SecretLocalReference {
	return privatev1.SecretLocalReference_builder{Id: resolved.GetId(), Name: resolved.GetMetadata().GetName()}.Build()
}

// canonicalSubnetLocalReference builds a new canonical reference from a resolved object's stored identity.
// It performs no lookup or validation and does not mutate the object.
func canonicalSubnetLocalReference(resolved *privatev1.Subnet) *privatev1.SubnetLocalReference {
	return privatev1.SubnetLocalReference_builder{Id: resolved.GetId(), Name: resolved.GetMetadata().GetName()}.Build()
}

// canonicalSecurityGroupLocalReference builds a new canonical reference from a resolved object's stored identity.
// It performs no lookup or validation and does not mutate the object.
func canonicalSecurityGroupLocalReference(resolved *privatev1.SecurityGroup) *privatev1.SecurityGroupLocalReference {
	return privatev1.SecurityGroupLocalReference_builder{Id: resolved.GetId(), Name: resolved.GetMetadata().GetName()}.Build()
}
