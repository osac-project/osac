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

// resolveAndCanonicalizeReference finds a target for a full or local reference. A full reference
// by name uses the owner's tenant/project unless selectors choose another allowed scope; a local
// reference must match the owner's tenant/project. It rejects deleted targets and fills the
// reference with stored ID and name, plus scope selectors for full references. The target stays
// locked through the request transaction; callers check readiness and type-specific rules.
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

// resolveFullResourceReference finds a caller-visible target by ID or by name in the selected
// tenant/project. An ID identifies the target without using the scope selectors, but a supplied
// name must still match. The target must belong to the owner's tenant or the shared tenant.
// The caller checks its lifecycle and fills the reference from the stored target.
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

// resolveResourceInScope finds a caller-visible target in exactly the supplied tenant and
// project. An ID selects the target, but a supplied name must still match. The request transaction
// holds a row lock. The caller checks lifecycle and fills the reference.
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

// getLockedResource reads a caller-visible target by ID and locks its row until the request
// transaction commits or rolls back.
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

// referenceNotFoundError reports a missing target with the caller-selected gRPC code.
// kind names the resource type, identifier is the requested ID or name, and source is an
// optional suffix identifying the referencing field, for example " in fields.disk_image".
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
