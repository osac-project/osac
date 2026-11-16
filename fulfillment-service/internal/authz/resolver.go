/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package authz

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/authz/catalog"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const pageSize int32 = 100

// Subject identifies one authenticated RoleBinding subject and its verified tenants.
type Subject struct {
	Username         string
	ServiceAccountID string
	Tenants          collections.Set[string]
}

// BindingGrant records the binding and Role that supplied a tenant's methods.
type BindingGrant struct {
	BindingID     string
	BindingTenant string
	RoleID        string
	RoleName      string
	RoleTenant    string
	Rules         []*privatev1.PermissionRule
}

// Grants contains expanded methods for one tenant and their provenance.
type Grants struct {
	Tenant   string
	Methods  []string
	Bindings []BindingGrant
}

// Resolver reads bindings and referenced objects from the database.
type Resolver struct {
	store   Store
	catalog *catalog.Catalog
}

// Store reads the bindings and referenced objects needed to resolve grants.
type Store interface {
	ListBindings(context.Context, string, int32, int32) ([]*privatev1.RoleBinding, error)
	GetRole(context.Context, string) (*privatev1.Role, error)
	GetUser(context.Context, string) (*privatev1.User, error)
	GetUserByName(context.Context, string, string) (*privatev1.User, error)
}

// DAOStore adapts generic DAOs to the Store interface.
type DAOStore struct {
	Bindings *dao.GenericDAO[*privatev1.RoleBinding]
	Roles    *dao.GenericDAO[*privatev1.Role]
	Users    *dao.GenericDAO[*privatev1.User]
}

func (s DAOStore) ListBindings(ctx context.Context, tenant string, offset, limit int32) ([]*privatev1.RoleBinding, error) {
	filter := fmt.Sprintf("this.metadata.tenant == %q && this.metadata.deletion_timestamp == null", tenant)
	response, err := s.Bindings.List().SetFilter(filter).SetLimit(limit).SetOffset(offset).Do(ctx)
	if err != nil {
		return nil, err
	}
	return response.GetItems(), nil
}

func (s DAOStore) GetRole(ctx context.Context, id string) (*privatev1.Role, error) {
	response, err := s.Roles.Get().SetId(id).Do(ctx)
	if err != nil {
		return nil, err
	}
	return response.GetObject(), nil
}

func (s DAOStore) GetUser(ctx context.Context, id string) (*privatev1.User, error) {
	response, err := s.Users.Get().SetId(id).Do(ctx)
	if err != nil {
		return nil, err
	}
	return response.GetObject(), nil
}

func (s DAOStore) GetUserByName(ctx context.Context, tenant, name string) (*privatev1.User, error) {
	filter := fmt.Sprintf("this.metadata.tenant == %q && this.metadata.name == %q && this.metadata.deletion_timestamp == null",
		tenant, name)
	response, err := s.Users.List().SetFilter(filter).SetLimit(1).Do(ctx)
	if err != nil {
		return nil, err
	}
	if len(response.GetItems()) == 0 {
		return nil, &dao.ErrNotFound{IDs: []string{name}}
	}
	return response.GetItems()[0], nil
}

// NewResolver creates a resolver reading from store and expanding methods through permissionCatalog.
func NewResolver(store Store, permissionCatalog *catalog.Catalog) (*Resolver, error) {
	if store == nil || permissionCatalog == nil {
		return nil, errors.New("store and catalog are required")
	}
	return &Resolver{store: store, catalog: permissionCatalog}, nil
}

// Resolve computes grants for one verified subject in one tenant. Any database or catalog error discards the result.
func (r *Resolver) Resolve(ctx context.Context, tenant string, subject Subject) (*Grants, error) {
	if tenant == "" || !subject.Tenants.Contains(tenant) {
		return nil, errors.New("requested tenant must belong to the authenticated subject")
	}
	if (subject.Username == "") && (subject.ServiceAccountID == "") {
		return nil, errors.New("exactly one subject identifier is required")
	}
	res := newResolution(r, tenant, subject)
	readCtx := res.readContext(ctx)
	for binding, err := range r.liveBindings(readCtx, tenant) {
		if err != nil {
			return nil, err
		}
		if err := res.addBinding(readCtx, binding); err != nil {
			return nil, err
		}
	}
	res.grants.Methods = slices.Sorted(maps.Keys(res.methods))
	return res.grants, nil
}

// liveBindings iterates over the live RoleBindings of tenant, one database page at a time. The
// tenant and deletion checks repeat the store-side filter defensively. A store error is yielded
// once and ends the iteration.
func (r *Resolver) liveBindings(ctx context.Context, tenant string) iter.Seq2[*privatev1.RoleBinding, error] {
	return func(yield func(*privatev1.RoleBinding, error) bool) {
		for offset := int32(0); ; offset += pageSize {
			page, err := r.store.ListBindings(ctx, tenant, offset, pageSize)
			if err != nil {
				yield(nil, err)
				return
			}
			for _, binding := range page {
				if !liveInTenant(binding, tenant) {
					continue
				}
				if !yield(binding, nil) {
					return
				}
			}
			if len(page) < int(pageSize) {
				return
			}
		}
	}
}

// liveInTenant reports whether binding belongs to tenant and is not being deleted.
func liveInTenant(binding *privatev1.RoleBinding, tenant string) bool {
	return binding.GetMetadata().GetTenant() == tenant && binding.GetMetadata().GetDeletionTimestamp() == nil
}

// resolution accumulates the grants of one Resolve call together with the caches shared by its
// steps. The caches live per call so that re-resolution reflects current database state, which
// keeps the Resolver itself stateless and safe for concurrent use.
type resolution struct {
	store   Store
	catalog *catalog.Catalog
	tenant  string
	subject Subject

	grants  *Grants
	methods map[string]struct{}

	roles       map[string]*privatev1.Role
	roleMethods map[string][]string
	usersByID   map[string]*privatev1.User
	usersByName map[string]*privatev1.User
}

func newResolution(r *Resolver, tenant string, subject Subject) *resolution {
	return &resolution{
		store:       r.store,
		catalog:     r.catalog,
		tenant:      tenant,
		subject:     subject,
		grants:      &Grants{Tenant: tenant},
		methods:     make(map[string]struct{}),
		roles:       make(map[string]*privatev1.Role),
		roleMethods: make(map[string][]string),
		usersByID:   make(map[string]*privatev1.User),
		usersByName: make(map[string]*privatev1.User),
	}
}

// readContext returns the context used for store reads. DAO visibility needs a subject even when
// resolution precedes the API authorization decision, and project metadata does not scope grants:
// resolution reads every project of the verified tenant. The internal read-only subject therefore
// uses managed-service-account visibility for that tenant.
func (res *resolution) readContext(parent context.Context) context.Context {
	return auth.ContextWithSubject(parent, &auth.Subject{
		User:                  cmp.Or(res.subject.Username, res.subject.ServiceAccountID),
		Tenants:               collections.NewSet(res.tenant),
		ManagedServiceAccount: true,
	})
}

// addBinding folds one live binding into the resolution when it matches the subject and references
// a usable Role.
func (res *resolution) addBinding(ctx context.Context, binding *privatev1.RoleBinding) error {
	matched, err := res.matches(ctx, binding)
	if err != nil {
		return err
	}
	if !matched {
		return nil
	}
	roleID := binding.GetSpec().GetRole().GetId()
	if roleID == "" {
		return nil
	}
	role, err := res.role(ctx, roleID)
	if err != nil {
		return err
	}
	if !usableIn(role, res.tenant) {
		return nil
	}
	methods, err := res.methodsForRole(roleID, role)
	if err != nil {
		return err
	}
	for _, method := range methods {
		res.methods[method] = struct{}{}
	}
	res.grants.Bindings = append(res.grants.Bindings, BindingGrant{
		BindingID:     binding.GetId(),
		BindingTenant: res.tenant,
		RoleID:        roleID,
		RoleName:      role.GetMetadata().GetName(),
		RoleTenant:    role.GetMetadata().GetTenant(),
		Rules:         role.GetSpec().GetPermissions(),
	})
	return nil
}

// matches reports whether the binding's subject references select the resolved subject.
func (res *resolution) matches(ctx context.Context, binding *privatev1.RoleBinding) (bool, error) {
	if res.subject.ServiceAccountID != "" {
		return serviceAccountMatches(binding.GetSpec().GetServiceAccounts(), res.subject.ServiceAccountID), nil
	}
	return res.userMatches(ctx, binding)
}

// serviceAccountMatches reports whether one of the references carries the service account ID.
func serviceAccountMatches(references []*privatev1.ServiceAccountReference, id string) bool {
	return slices.ContainsFunc(references, func(ref *privatev1.ServiceAccountReference) bool {
		return ref.GetId() == id
	})
}

// userMatches reports whether one of the binding's user references selects the resolved user.
func (res *resolution) userMatches(ctx context.Context, binding *privatev1.RoleBinding) (bool, error) {
	tenant := binding.GetMetadata().GetTenant()
	for _, ref := range binding.GetSpec().GetUsers() {
		if !userReferenceInScope(ref, tenant) {
			continue
		}
		user, err := res.userForReference(ctx, tenant, ref)
		if err != nil {
			return false, err
		}
		if res.selectsSubject(ref, user, tenant) {
			return true, nil
		}
	}
	return false, nil
}

// userReferenceInScope reports whether ref can select a user in tenant: an explicit reference
// tenant must match, and shared references only apply within the shared tenant.
func userReferenceInScope(ref *privatev1.UserReference, tenant string) bool {
	if ref.GetTenant() != "" && ref.GetTenant() != tenant {
		return false
	}
	return !ref.GetShared() || tenant == auth.SharedTenant
}

// selectsSubject reports whether the user row selected by ref in tenant is the resolved subject:
// the row must be live, belong to tenant, and carry the subject's username. A name-qualified
// reference must additionally match the row's name.
func (res *resolution) selectsSubject(ref *privatev1.UserReference, user *privatev1.User, tenant string) bool {
	if user == nil || user.GetMetadata().GetTenant() != tenant ||
		user.GetMetadata().GetDeletionTimestamp() != nil {
		return false
	}
	if user.GetSpec().GetUsername() != res.subject.Username {
		return false
	}
	return ref.GetName() == "" || ref.GetName() == user.GetMetadata().GetName()
}

// userForReference returns the user selected by ref in tenant. A reference carries either an ID,
// resolved globally, or a name, resolved within tenant; a reference without either selects no
// user. Missing users are cached as nil so each is fetched at most once.
func (res *resolution) userForReference(ctx context.Context, tenant string, ref *privatev1.UserReference) (*privatev1.User, error) {
	if ref.GetId() != "" {
		return res.userByID(ctx, ref.GetId())
	}
	if ref.GetName() != "" {
		return res.userByName(ctx, tenant, ref.GetName())
	}
	return nil, nil
}

// userByID returns the user with the given ID, or nil when it does not exist.
func (res *resolution) userByID(ctx context.Context, id string) (*privatev1.User, error) {
	if user, known := res.usersByID[id]; known {
		return user, nil
	}
	user, err := res.store.GetUser(ctx, id)
	if err != nil && !isNotFound(err) {
		return nil, err
	}
	res.usersByID[id] = user
	return user, nil
}

// userByName returns the user with the given name in tenant, or nil when it does not exist.
func (res *resolution) userByName(ctx context.Context, tenant, name string) (*privatev1.User, error) {
	if user, known := res.usersByName[name]; known {
		return user, nil
	}
	user, err := res.store.GetUserByName(ctx, tenant, name)
	if err != nil && !isNotFound(err) {
		return nil, err
	}
	res.usersByName[name] = user
	return user, nil
}

// role returns the Role with the given ID, or nil when it does not exist. Missing roles are
// cached as nil so each is fetched at most once.
func (res *resolution) role(ctx context.Context, id string) (*privatev1.Role, error) {
	if role, known := res.roles[id]; known {
		return role, nil
	}
	role, err := res.store.GetRole(ctx, id)
	if err != nil {
		if !isNotFound(err) {
			return nil, err
		}
		role = nil
	}
	res.roles[id] = role
	return role, nil
}

// usableIn reports whether role can grant methods in tenant: it must exist, not be deleted, and
// belong to the tenant or to the shared tenant.
func usableIn(role *privatev1.Role, tenant string) bool {
	if role == nil || role.GetMetadata().GetDeletionTimestamp() != nil {
		return false
	}
	roleTenant := role.GetMetadata().GetTenant()
	return roleTenant == tenant || roleTenant == auth.SharedTenant
}

// methodsForRole returns the catalog-expanded methods of the Role's permission rules, expanded at
// most once per Role.
func (res *resolution) methodsForRole(roleID string, role *privatev1.Role) ([]string, error) {
	if methods, expanded := res.roleMethods[roleID]; expanded {
		return methods, nil
	}
	var methods []string
	for _, rule := range role.GetSpec().GetPermissions() {
		expanded, err := res.catalog.Expand(rule.GetService(), rule.GetMethods())
		if err != nil {
			return nil, fmt.Errorf("expand Role %q: %w", roleID, err)
		}
		methods = append(methods, expanded...)
	}
	res.roleMethods[roleID] = methods
	return methods, nil
}

// isNotFound reports whether err is a not-found error from the store.
func isNotFound(err error) bool {
	_, ok := errors.AsType[*dao.ErrNotFound](err)
	return ok
}
