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
	"google.golang.org/protobuf/proto"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// referenceScope identifies where a name is looked up. A local reference must also belong to
// this exact tenant and project; a full reference may select a shared tenant or another project.
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

func validateImmutableReferenceIdentity[T interface {
	fullResourceReference
	proto.Message
}](current, candidate T, path, label string, allowMissingID bool) error {
	identityChanged := candidate.GetId() != current.GetId()
	if allowMissingID && candidate.GetId() == "" {
		identityChanged = candidate.GetName() != current.GetName()
	}
	if !current.ProtoReflect().IsValid() || !candidate.ProtoReflect().IsValid() ||
		identityChanged ||
		(candidate.GetName() != "" && candidate.GetName() != current.GetName()) ||
		(candidate.GetProject() != "" && candidate.GetProject() != current.GetProject()) ||
		(candidate.GetShared() && !current.GetShared()) {
		return grpcstatus.Errorf(grpccodes.InvalidArgument,
			"cannot change %s from '%s' to '%s': %s is immutable",
			path, refKey(current), refKey(candidate), label)
	}
	return nil
}

type referenceGetFunc[O dao.Object] func(context.Context, *dao.GenericDAO[O], string) (O, error)

// catalogItemScope uses the tenant and project assigned to the Catalog Item being saved. Policy
// resolvers use this as their starting scope; full references can select another project or the
// shared tenant. Missing metadata produces an empty scope.
func catalogItemScope(item catalogItem) referenceScope {
	metadata := item.GetMetadata()
	if metadata == nil {
		return referenceScope{}
	}
	return referenceScope{tenant: metadata.GetTenant(), project: metadata.GetProject()}
}

// resolveAndCanonicalizeReference loads the referenced object and fills the reference with
// its stored ID and name. Full references also receive the stored project and shared flag.
// Deleted targets are rejected; readiness and other resource-specific checks belong to the caller.
//
// Parameters:
//   - ctx carries the caller's authorization and the current database transaction.
//   - resourceDao reads the referenced resource type and enforces caller visibility.
//   - ownerMetadata is the assigned metadata of the object containing the reference.
//     It supplies the default tenant/project; local references must match both exactly.
//   - reference is modified only after lookup, ownership, and deletion checks succeed.
//     Full references can select the shared tenant or another project when looking up a name.
//   - kind is a human-readable resource name for errors, for example "disk image".
//   - notFoundCode is the gRPC code to return when no target exists in the allowed scope.
func resolveAndCanonicalizeReference[O referenceResource](
	ctx context.Context,
	resourceDao *dao.GenericDAO[O],
	ownerMetadata *privatev1.Metadata,
	reference resourceReference,
	kind string,
	notFoundCode grpccodes.Code,
) (O, error) {
	return resolveAndCanonicalizeReferenceWithGet(ctx, resourceDao, ownerMetadata, reference, kind, notFoundCode, getReferenceResource[O])
}

// resolveAndCanonicalizeLockedReference resolves and fills a reference while holding an
// exclusive lock on the target through the request transaction. Catalog Item creation and
// authoring use this when the target must remain stable until their changes are saved.
func resolveAndCanonicalizeLockedReference[O referenceResource](
	ctx context.Context,
	resourceDao *dao.GenericDAO[O],
	ownerMetadata *privatev1.Metadata,
	reference resourceReference,
	kind string,
	notFoundCode grpccodes.Code,
) (O, error) {
	return resolveAndCanonicalizeReferenceWithGet(ctx, resourceDao, ownerMetadata, reference, kind, notFoundCode, getLockedReferenceResource[O])
}

func resolveAndCanonicalizeReferenceWithGet[O referenceResource](
	ctx context.Context,
	resourceDao *dao.GenericDAO[O],
	ownerMetadata *privatev1.Metadata,
	reference resourceReference,
	kind string,
	notFoundCode grpccodes.Code,
	get referenceGetFunc[O],
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
		object, err = resolveFullResourceReferenceWithGet(
			ctx, resourceDao, ownerScope, fullReference, kind, "", notFoundCode, get,
		)
	} else {
		object, err = resolveResourceInScopeWithGet(
			ctx, resourceDao, ownerScope, reference.GetId(), reference.GetName(), kind, "", notFoundCode, get,
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

// resolveFullResourceReference loads a full reference without modifying it. With a name,
// shared and project select the lookup scope. With an ID, those selectors are ignored, but
// a supplied name must still match. The target must belong to the owner's tenant or shared.
// The caller checks deletion/readiness and copies stored values into the reference if needed.
//
// For an owner in acme/apps, {name: "vm-base", shared: true, project: "templates"}
// selects shared/templates. An explicit ID may select any caller-visible project in acme
// or shared, but cannot select another tenant's object just because the caller can see it.
//
// Parameters:
//   - ctx carries caller authorization and the database transaction; resourceDao enforces visibility.
//   - ownerScope is the tenant/project of the object containing reference.
//   - reference supplies an ID, a name, or both, plus optional name-lookup selectors.
//   - kind labels the target type in errors; source is an optional error suffix such as
//     " in fields.version". source does not affect which object is selected.
//   - notFoundCode is the gRPC code used for a missing target.
func resolveFullResourceReference[O referenceResource](
	ctx context.Context,
	resourceDao *dao.GenericDAO[O],
	ownerScope referenceScope,
	reference fullResourceReference,
	kind string,
	source string,
	notFoundCode grpccodes.Code,
) (O, error) {
	return resolveFullResourceReferenceWithGet(ctx, resourceDao, ownerScope, reference, kind, source, notFoundCode, getReferenceResource[O])
}

// resolveLockedFullResourceReference has the same scope rules as
// resolveFullResourceReference and holds an exclusive lock on the target until the
// request transaction ends. Catalog Item authoring uses it for protected dependencies.
func resolveLockedFullResourceReference[O referenceResource](
	ctx context.Context,
	resourceDao *dao.GenericDAO[O],
	ownerScope referenceScope,
	reference fullResourceReference,
	kind string,
	source string,
	notFoundCode grpccodes.Code,
) (O, error) {
	return resolveFullResourceReferenceWithGet(ctx, resourceDao, ownerScope, reference, kind, source, notFoundCode, getLockedReferenceResource[O])
}

func resolveFullResourceReferenceWithGet[O referenceResource](
	ctx context.Context,
	resourceDao *dao.GenericDAO[O],
	ownerScope referenceScope,
	reference fullResourceReference,
	kind string,
	source string,
	notFoundCode grpccodes.Code,
	get referenceGetFunc[O],
) (O, error) {
	if reference.GetId() == "" {
		scope := selectedReferenceScope(ownerScope, reference.GetShared(), reference.GetProject())
		object, err := resolveResourceInScopeWithGet(
			ctx, resourceDao, scope, "", reference.GetName(), kind, source, notFoundCode, get,
		)
		if err == nil {
			err = validateDependencyOwnerScope(ownerScope, object.GetMetadata(), kind, source)
		}
		return object, err
	}

	identifier := referenceIdentifier(reference.GetId(), reference.GetName())
	object, err := get(ctx, resourceDao, reference.GetId())
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

// resolveResourceInScope loads an object in exactly one tenant/project without changing
// the caller's reference. For example, a subnet in acme/apps cannot resolve to a same-named
// subnet in acme/test, even when the caller can see both. This applies to IDs as well as names.
// The caller checks deletion/readiness and copies stored values into the reference if needed.
//
// Parameters:
//   - ctx carries caller authorization and the database transaction; resourceDao enforces visibility.
//   - scope is the required tenant/project. An empty project means no project.
//   - id selects the object directly. If it is empty, name is resolved within scope first.
//     At least one is required; when both are supplied, the stored name must match.
//   - kind labels the target type in errors, for example "subnet".
//   - source adds context to errors, for example " in fields.network_attachments"; it may be empty.
//   - notFoundCode is returned when the target is missing, invisible, or outside scope.
func resolveResourceInScope[O referenceResource](
	ctx context.Context,
	resourceDao *dao.GenericDAO[O],
	scope referenceScope,
	id, name, kind, source string,
	notFoundCode grpccodes.Code,
) (O, error) {
	return resolveResourceInScopeWithGet(ctx, resourceDao, scope, id, name, kind, source, notFoundCode, getReferenceResource[O])
}

// resolveLockedResourceInScope follows the same scope rules as resolveResourceInScope
// and holds an exclusive lock on the target until the request transaction ends.
func resolveLockedResourceInScope[O referenceResource](
	ctx context.Context,
	resourceDao *dao.GenericDAO[O],
	scope referenceScope,
	id, name, kind, source string,
	notFoundCode grpccodes.Code,
) (O, error) {
	return resolveResourceInScopeWithGet(ctx, resourceDao, scope, id, name, kind, source, notFoundCode, getLockedReferenceResource[O])
}

func resolveResourceInScopeWithGet[O referenceResource](
	ctx context.Context,
	resourceDao *dao.GenericDAO[O],
	scope referenceScope,
	id, name, kind, source string,
	notFoundCode grpccodes.Code,
	get referenceGetFunc[O],
) (O, error) {
	var zero O
	if id == "" && name == "" {
		return zero, grpcstatus.Errorf(grpccodes.InvalidArgument, "%s reference%s must specify id or name", kind, source)
	}
	identifier := referenceIdentifier(id, name)

	// Names are only unique within a scope. Resolve the name to an ID before loading
	// the object; a caller-supplied ID needs no preliminary lookup.
	resolvedID := id
	if resolvedID == "" {
		lookup := references.NewScopedDAOLookupFunc(resourceDao)
		resolved, err := lookup(ctx, scope.tenant, scope.project, "", name)
		if err != nil {
			return zero, resourceLookupError(err, kind, identifier, source, notFoundCode)
		}
		resolvedID = resolved.ID
	}

	// Load the actual object. The caller chose an ordinary or locked read.
	object, err := get(ctx, resourceDao, resolvedID)
	if err != nil {
		return zero, resourceLookupError(err, kind, identifier, source, notFoundCode)
	}

	// DAO visibility means the caller may see this object, not that it belongs to
	// the required tenant/project. Check the loaded row for both lookup paths.
	metadata := object.GetMetadata()
	if metadata == nil {
		return zero, grpcstatus.Errorf(grpccodes.Internal, "resolved %s '%s' has no metadata", kind, identifier)
	}
	if metadata.GetTenant() != scope.tenant || metadata.GetProject() != scope.project {
		return zero, referenceNotFoundError(notFoundCode, kind, identifier, source)
	}
	if name != "" && metadata.GetName() != name {
		return zero, grpcstatus.Errorf(grpccodes.InvalidArgument, "%s reference%s: id and name do not refer to the same resource", kind, source)
	}
	return object, nil
}

// selectedReferenceScope starts with the owner's scope. shared=true selects the shared
// tenant, and a nonempty project replaces the owner's project. An empty project keeps the
// owner's project, including when switching to shared. For example, acme/apps plus shared=true
// selects shared/apps unless a different project is supplied.
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

// inheritReferenceScope updates ref when a default was copied from the object described by
// owner. It carries that object's tenant/project into later name lookup. For example, an image
// default from a shared Template must still select the shared image when used by an acme VM.
// An explicitly shared ref is left alone; otherwise the shared flag is set from owner and
// only an omitted project is filled. This function does not look up or validate the target.
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

// validateDependencyOwnerScope checks target's tenant against owner, independently of what
// the caller can see. An acme owner may use acme or shared targets; a shared owner may use
// only shared targets. Project checks for local references belong to resolveResourceInScope.
// kind names the referenced type in errors; source adds the referencing field, or is empty.
func validateDependencyOwnerScope(owner referenceScope, target *privatev1.Metadata, kind, source string) error {
	if target.GetTenant() != auth.SharedTenant && target.GetTenant() != owner.tenant {
		return grpcstatus.Errorf(grpccodes.InvalidArgument, "%s reference%s must belong to the owning tenant or shared tenant", kind, source)
	}
	return nil
}

// getReferenceResource reads a stored object by ID using caller visibility and the
// request transaction. It does not check ownership, deletion, or readiness.
func getReferenceResource[O dao.Object](ctx context.Context, resourceDao *dao.GenericDAO[O], id string) (O, error) {
	return executeReferenceGet(ctx, resourceDao.Get().SetId(id))
}

// getLockedReferenceResource takes an exclusive row lock until the request transaction ends.
func getLockedReferenceResource[O dao.Object](ctx context.Context, resourceDao *dao.GenericDAO[O], id string) (O, error) {
	return executeReferenceGet(ctx, resourceDao.Get().SetId(id).SetLock(true))
}

func executeReferenceGet[O dao.Object](ctx context.Context, request *dao.GetRequest[O]) (O, error) {
	response, err := request.Do(ctx)
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

// referenceNotFoundError reports a missing target with the caller-selected gRPC code.
// kind names the resource type, identifier is the requested ID or name, and source is an
// optional suffix identifying the referencing field, for example " in fields.disk_image".
func referenceNotFoundError(code grpccodes.Code, kind, identifier, source string) error {
	return grpcstatus.Errorf(code, "%s '%s'%s not found", kind, identifier, source)
}

// resourceLookupError translates err from either a scoped name lookup or DAO Get into a
// gRPC error. kind names the resource type, identifier is the requested ID/name, and source
// is an optional error suffix identifying the referencing field. notFoundCode controls the
// missing-target response; denied access and deadlocks retain their own status codes.
// Unexpected failures return Internal without exposing database details.
func resourceLookupError(err error, kind, identifier, source string, notFoundCode grpccodes.Code) error {
	// The scoped name lookup and DAO Get use different not-found error types.
	var lookupNotFound interface{ IsNotFound() bool }
	if errors.As(err, &lookupNotFound) && lookupNotFound.IsNotFound() {
		return referenceNotFoundError(notFoundCode, kind, identifier, source)
	}
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

// canonicalComputeInstanceTemplateReference copies the resolved object's ID, name,
// project, and shared-tenant selector into a new reference.
func canonicalComputeInstanceTemplateReference(resolved *privatev1.ComputeInstanceTemplate) *privatev1.ComputeInstanceTemplateReference {
	return privatev1.ComputeInstanceTemplateReference_builder{
		Id:      resolved.GetId(),
		Name:    resolved.GetMetadata().GetName(),
		Project: resolved.GetMetadata().GetProject(),
		Shared:  resolved.GetMetadata().GetTenant() == auth.SharedTenant,
	}.Build()
}

// canonicalClusterTemplateReference copies the resolved object's ID, name, project,
// and shared-tenant selector into a new reference.
func canonicalClusterTemplateReference(resolved *privatev1.ClusterTemplate) *privatev1.ClusterTemplateReference {
	return privatev1.ClusterTemplateReference_builder{
		Id:      resolved.GetId(),
		Name:    resolved.GetMetadata().GetName(),
		Project: resolved.GetMetadata().GetProject(),
		Shared:  resolved.GetMetadata().GetTenant() == auth.SharedTenant,
	}.Build()
}

// canonicalBareMetalInstanceTemplateReference copies the resolved object's ID, name,
// project, and shared-tenant selector into a new reference.
func canonicalBareMetalInstanceTemplateReference(resolved *privatev1.BareMetalInstanceTemplate) *privatev1.BareMetalInstanceTemplateReference {
	return privatev1.BareMetalInstanceTemplateReference_builder{
		Id:      resolved.GetId(),
		Name:    resolved.GetMetadata().GetName(),
		Project: resolved.GetMetadata().GetProject(),
		Shared:  resolved.GetMetadata().GetTenant() == auth.SharedTenant,
	}.Build()
}

// canonicalInstanceTypeReference copies the resolved object's ID, name, project, and
// shared-tenant selector into a new reference.
func canonicalInstanceTypeReference(resolved *privatev1.InstanceType) *privatev1.InstanceTypeReference {
	return privatev1.InstanceTypeReference_builder{
		Id:      resolved.GetId(),
		Name:    resolved.GetMetadata().GetName(),
		Project: resolved.GetMetadata().GetProject(),
		Shared:  resolved.GetMetadata().GetTenant() == auth.SharedTenant,
	}.Build()
}

// canonicalDiskImageReference copies the resolved object's ID, name, project, and
// shared-tenant selector into a new reference.
func canonicalDiskImageReference(resolved *privatev1.DiskImage) *privatev1.DiskImageReference {
	return privatev1.DiskImageReference_builder{
		Id:      resolved.GetId(),
		Name:    resolved.GetMetadata().GetName(),
		Project: resolved.GetMetadata().GetProject(),
		Shared:  resolved.GetMetadata().GetTenant() == auth.SharedTenant,
	}.Build()
}

// canonicalStorageTierReference copies the resolved object's ID and name into a new reference.
func canonicalStorageTierReference(resolved *privatev1.StorageTier) *privatev1.StorageTierReference {
	return privatev1.StorageTierReference_builder{Id: resolved.GetId(), Name: resolved.GetMetadata().GetName()}.Build()
}

// canonicalSecretLocalReference copies the resolved object's ID and name into a new local reference.
func canonicalSecretLocalReference(resolved *privatev1.Secret) *privatev1.SecretLocalReference {
	return privatev1.SecretLocalReference_builder{Id: resolved.GetId(), Name: resolved.GetMetadata().GetName()}.Build()
}

// canonicalBareMetalInstanceTypeLocalReference copies the resolved object's ID and name
// into a new local reference.
func canonicalBareMetalInstanceTypeLocalReference(resolved *privatev1.BareMetalInstanceType) *privatev1.BareMetalInstanceTypeLocalReference {
	return privatev1.BareMetalInstanceTypeLocalReference_builder{Id: resolved.GetId(), Name: resolved.GetMetadata().GetName()}.Build()
}

// canonicalSubnetLocalReference copies the resolved object's ID and name into a new local reference.
func canonicalSubnetLocalReference(resolved *privatev1.Subnet) *privatev1.SubnetLocalReference {
	return privatev1.SubnetLocalReference_builder{Id: resolved.GetId(), Name: resolved.GetMetadata().GetName()}.Build()
}

// canonicalSecurityGroupLocalReference copies the resolved object's ID and name into a new local reference.
func canonicalSecurityGroupLocalReference(resolved *privatev1.SecurityGroup) *privatev1.SecurityGroupLocalReference {
	return privatev1.SecurityGroupLocalReference_builder{Id: resolved.GetId(), Name: resolved.GetMetadata().GetName()}.Build()
}
