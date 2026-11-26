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
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/quotausage"
	"github.com/osac-project/osac/fulfillment-service/internal/reflection"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	quota "github.com/osac-project/osac/proto/quota"
)

// quotaExceededReason is the ErrorInfo reason of a refused request.
const quotaExceededReason = "QUOTA_EXCEEDED"

// quotaEnforcer checks each create and update of a quota-annotated kind against the tenant's
// limits, inside the request's transaction, after the private server filled in the request. The
// check and the write share the transaction, so a request over a limit writes nothing.
//
// The enforcer names no kind: the checked kinds, their price and the spec fields that trigger the
// update check all come from the quota registry (proto/quota). The built-in shared and system
// tenants hold the provider's own records and are never checked.
type quotaEnforcer struct {
	logger      *slog.Logger
	counter     *quotausage.Counter
	objects     quotaObjects
	checked     map[string]bool
	specPaths   map[string][]string
	kindTables  map[string]string
	kindMessage map[string]protoreflect.FullName
	kindLinks   map[string]quota.Link
	// uncreated names the kinds the installation never creates for a request, so their
	// planned creates and dimensions count toward no limit. The shared parts capture it once
	// from the installation's networking settings, the same settings that skip the creation
	// itself.
	uncreated []string
	// parentKinds names the kinds another kind was created for, so an update of one nets the
	// ask it grows against the children it already has. Derived from the registry's links.
	parentKinds map[string]bool
	metrics     *quotaMetrics
}

// newQuotaEnforcer builds the enforcer. It fails when the quota registry or the usage counter
// can't be built, so a broken annotation is a startup failure. The installation's uncreated
// kinds come from the shared parts, so every enforcer prices exactly what its shared counter
// counts.
func newQuotaEnforcer(
	logger *slog.Logger, registerer prometheus.Registerer,
) (result *quotaEnforcer, err error) {
	if logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	parts, err := loadSharedEnforcerParts()
	if err != nil {
		return nil, err
	}
	metrics, err := newQuotaMetrics(registerer)
	if err != nil {
		return nil, err
	}
	// A type is a kind other kinds resolve through; it counts toward no limit, so creates and
	// updates of a type are never checked:
	types := map[string]bool{}
	for _, kind := range parts.kinds {
		for _, field := range kind.Fields {
			if field.Through != "" {
				types[field.Through] = true
			}
		}
	}
	result = &quotaEnforcer{
		logger:      logger,
		counter:     parts.counter,
		checked:     map[string]bool{},
		specPaths:   map[string][]string{},
		kindTables:  map[string]string{},
		kindMessage: map[string]protoreflect.FullName{},
		kindLinks:   map[string]quota.Link{},
		uncreated:   parts.uncreated,
		parentKinds: map[string]bool{},
		metrics:     metrics,
	}
	for _, link := range parts.links {
		result.kindLinks[link.Kind] = link
	}

	// Every kind another kind is created for is a parent whose updates net the children it
	// already holds:
	for _, link := range parts.links {
		if link.Parent != "" {
			result.parentKinds[link.Parent] = true
		}
	}
	for i := range parts.kinds {
		kind := parts.kinds[i]
		result.kindMessage[kind.Name] = kind.Message
		result.kindTables[kind.Name] = dao.TableNameFor(string(kind.Message.Name()))
		if types[kind.Name] {
			continue
		}
		result.checked[kind.Name] = true
		result.specPaths[kind.Name] = quotaSpecPaths(&kind)
	}
	return result, nil
}

// uncreatedQuotaKinds returns the kinds the installation never creates for any request: with
// provider networking disabled the writers create no automatic addresses, so neither the check
// nor the counter may price or count them. Both read the list the shared parts captured once
// from these settings, so they never disagree.
func uncreatedQuotaKinds(settings NetworkingSettings) []string {
	if settings.ProvisioningEnabled {
		return nil
	}
	return []string{"ExternalIP"}
}

// enforcerParts holds the parts of the enforcer that carry no per-server state: the usage
// counter with its generated statements, the registry and its links, and the uncreated kinds
// derived from the installation's networking settings. Every generic server builds an enforcer of
// its own, so these parts are built once and shared; they are pure functions of the compiled
// protos and the installation's networking settings, none of which change while the process runs.
type enforcerParts struct {
	counter   *quotausage.Counter
	kinds     []quota.Kind
	links     []quota.Link
	uncreated []string
}

var (
	sharedPartsOnce sync.Once
	sharedParts     enforcerParts
	sharedPartsErr  error
)

// loadSharedEnforcerParts loads the shared parts the first time a server builds an enforcer.
func loadSharedEnforcerParts() (enforcerParts, error) {
	sharedPartsOnce.Do(func() {
		var err error
		sharedParts.uncreated = uncreatedQuotaKinds(installationNetworkingSettings)
		sharedParts.counter, err = quotausage.NewCounter().
			SetLogger(slog.Default()).
			SetUncreatedQuotaKinds(sharedParts.uncreated...).
			Build()
		if err != nil {
			sharedPartsErr = err
			return
		}
		sharedParts.kinds, err = quota.Kinds()
		if err != nil {
			sharedPartsErr = fmt.Errorf("failed to load the quota registry: %w", err)
			return
		}
		sharedParts.links, err = quota.Links()
		if err != nil {
			sharedPartsErr = fmt.Errorf("failed to load the quota registry's links: %w", err)
		}
	})
	return sharedParts, sharedPartsErr
}

// resetSharedEnforcerParts discards the shared parts, so the next build derives them again from
// the current installation settings. Only SetInstallationNetworkingSettings calls it, before the
// startup builds any server.
func resetSharedEnforcerParts() {
	sharedPartsOnce = sync.Once{}
	sharedParts = enforcerParts{}
	sharedPartsErr = nil
}

// quotaSpecPaths returns the spec field paths of the kind that its annotations read: the
// annotated fields themselves and the brace paths of their dimensions, such as
// "spec.storage_tier" from "storage.{spec.storage_tier}.gib".
func quotaSpecPaths(kind *quota.Kind) []string {
	var paths []string
	add := func(path string) {
		if strings.HasPrefix(path, "spec.") {
			paths = append(paths, path)
		}
	}
	for _, field := range kind.Fields {
		add(field.Path)
		if field.Dimension == "" {
			continue
		}
		bracePaths, err := quota.BracePaths(field.Dimension)
		if err != nil {
			continue
		}
		for _, path := range bracePaths {
			add(path)
		}
	}
	return paths
}

// quotaKindName returns the kind's short name, such as "ComputeInstance".
func quotaKindName(message proto.Message) string {
	return string(message.ProtoReflect().Descriptor().FullName().Name())
}

// Checks reports whether the message's kind is checked: annotated, and not a type.
func (e *quotaEnforcer) Checks(message proto.Message) bool {
	return e.checked[quotaKindName(message)]
}

// updateChecked reports whether an update of the message's kind runs the check: a checked kind,
// and either no field mask or a mask that names a spec field the annotations read. Status-only
// updates never run the check.
func (e *quotaEnforcer) updateChecked(message proto.Message, mask *fieldmaskpb.FieldMask) bool {
	name := quotaKindName(message)
	if !e.checked[name] {
		return false
	}
	// The helper normalizes each mask path the way the field-path compiler does, so a mask
	// like "spec. instance_type" can't update the field while skipping the check, and it
	// treats a nil or empty mask as a full replacement:
	return updateIncludesField(mask, e.specPaths[name]...)
}

// quotaObjects reads the stored objects of the kind a server handles. The reads go through the
// server's DAO, so they apply the caller's visibility, and they take no lock.
type quotaObjects interface {
	// tenantOf returns the tenant of the stored object with the given identifier.
	tenantOf(ctx context.Context, id string) (string, error)

	// nameExists reports whether a live object holds the name in the tenant and project.
	nameExists(ctx context.Context, tenant, project, name string) (bool, error)
}

// quotaDAO reads the objects of one kind through the DAO of the server that handles them.
type quotaDAO[O dao.Object] struct {
	objects *dao.GenericDAO[O]
}

func (q *quotaDAO[O]) tenantOf(ctx context.Context, id string) (string, error) {
	response, err := q.objects.Get().SetId(id).Do(ctx)
	if err != nil {
		return "", err
	}
	return metadataOf(response.GetObject()).GetTenant(), nil
}

func (q *quotaDAO[O]) nameExists(ctx context.Context, tenant, project, name string) (bool, error) {
	filter := fmt.Sprintf(
		"this.metadata.tenant == %s && this.metadata.project == %s && "+
			"this.metadata.name == %s && this.metadata.deletion_timestamp == null",
		strconv.Quote(tenant), strconv.Quote(project), strconv.Quote(name),
	)
	response, err := q.objects.List().SetFilter(filter).SetLimit(1).Do(ctx)
	if err != nil {
		return false, err
	}
	return response.GetSize() > 0, nil
}

// quotaGate is one request's passage through the quota check. The generic path opens a gate
// before it fills in the candidate, so the tenant lock comes before every other row of the
// request, and closes it after the fill-in, when the candidate is final. The zero value of a
// gate checks nothing, so a kind that is not checked carries no branch.
type quotaGate struct {
	enforcer *quotaEnforcer
}

// GateCreate opens the gate of a create. It takes the tenant lock for a checked kind and
// returns an empty gate for any other kind.
func (e *quotaEnforcer) GateCreate(ctx context.Context, requestObject proto.Message) (quotaGate, error) {
	if !e.Checks(requestObject) {
		return quotaGate{}, nil
	}
	if err := e.lockTenant(ctx, metadataOf(requestObject).GetTenant()); err != nil {
		return quotaGate{}, err
	}
	return quotaGate{enforcer: e}, nil
}

// CloseCreate checks the final candidate of a create. A refusal rolls back whatever the fill-in
// wrote before it.
func (g quotaGate) CloseCreate(ctx context.Context, candidate proto.Message) error {
	if g.enforcer == nil {
		return nil
	}
	if err := g.enforcer.checkCreate(ctx, candidate); err != nil {
		if conflict := g.enforcer.retryConflict(ctx, candidate, err); conflict != nil {
			return failWithRollback(ctx, conflict)
		}
		return failWithRollback(ctx, err)
	}
	return nil
}

// GateUpdate opens the gate of an update. It reads the tenant of the stored object the way the
// caller can see it, so a caller who names an object it can't see takes no lock, and then locks
// that tenant. It returns an empty gate for an update that is not checked.
func (e *quotaEnforcer) GateUpdate(
	ctx context.Context, requestObject proto.Message, mask *fieldmaskpb.FieldMask,
) (quotaGate, error) {
	if !e.updateChecked(requestObject, mask) {
		return quotaGate{}, nil
	}
	if err := e.lockTenantOf(ctx, objectID(requestObject)); err != nil {
		return quotaGate{}, err
	}
	return quotaGate{enforcer: e}, nil
}

// CloseUpdate checks the final candidate of an update. The check adds the positive difference
// between the candidate and the stored object, and a refusal rolls back whatever the fill-in
// wrote before it.
func (g quotaGate) CloseUpdate(ctx context.Context, current, candidate proto.Message) error {
	if g.enforcer == nil {
		return nil
	}
	if err := g.enforcer.checkUpdate(ctx, current, candidate); err != nil {
		return failWithRollback(ctx, err)
	}
	return nil
}

// lockTenantRow locks the tenant's row before any other row of the request, so two concurrent
// requests in one tenant never both pass a check or warning write: the second waits for the lock
// and then sees the first, at READ COMMITTED. FOR NO KEY UPDATE doesn't block the foreign-key
// checks on the tenant's row, so inserts that don't take the lock don't wait for it. The
// built-in tenants are never locked: their shared row would serialize unrelated tenants' work,
// and no request in them is checked. A tenant being deleted refuses the request. The metrics are
// optional, so callers outside an enforcer, like the networking creates, pass nil.
func lockTenantRow(ctx context.Context, metrics *quotaMetrics, tenant string) error {
	return lockTenantRowMode(ctx, metrics, tenant, true)
}

// lockTenantRowForCleanup locks a tenant's row even when its deletion was accepted. Only the
// teardown paths that remove a deleting tenant's quota rows use it: admission already refuses a
// deleting tenant, so no checked request can race the cleanup, and refusing the cleanup would
// strand the quota rows the tenant's archival depends on.
func lockTenantRowForCleanup(ctx context.Context, metrics *quotaMetrics, tenant string) error {
	return lockTenantRowMode(ctx, metrics, tenant, false)
}

// lockTenantRowMode locks the tenant's row in the mode FOR NO KEY UPDATE and, when refuseDeleting
// is true, refuses a tenant whose deletion was accepted.
func lockTenantRowMode(
	ctx context.Context, metrics *quotaMetrics, tenant string, refuseDeleting bool,
) error {
	if tenant == "" || tenant == auth.SystemTenant || tenant == auth.SharedTenant {
		return nil
	}
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return err
	}
	lockStart := time.Now()
	var deleting bool
	err = tx.QueryRow(ctx,
		`select deletion_timestamp <> 'epoch' from tenants where id = $1 for no key update`, tenant,
	).Scan(&deleting)
	metrics.observeLockWait(time.Since(lockStart))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && (pgErr.Code == pgerrcode.DeadlockDetected ||
			pgErr.Code == pgerrcode.LockNotAvailable) {
			return grpcstatus.Error(grpccodes.Aborted, "concurrent modification detected, please retry")
		}
		// The caller's context, not the request, ended: a canceled or timed out client sees
		// the cancellation, not an Internal failure:
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return grpcstatus.FromContextError(err).Err()
		}
		return grpcstatus.Errorf(grpccodes.Internal, "failed to lock tenant '%s'", tenant)
	}
	if deleting && refuseDeleting {
		return grpcstatus.Errorf(grpccodes.FailedPrecondition, "tenant '%s' is being deleted", tenant)
	}
	return nil
}

// lockTenant locks the request's tenant's row.
func (e *quotaEnforcer) lockTenant(ctx context.Context, tenant string) error {
	return lockTenantRow(ctx, e.metrics, tenant)
}

// lockTenantOf locks the tenant row of the stored object. The tenant never changes, so an
// unlocked read finds it. The read applies the caller's visibility, so a caller who names an
// object it can't see takes no lock, and the request fails before it locks anything. A missing
// object locks nothing: the update fails NotFound later. The built-in tenants are never locked,
// because the check skips them.
func (e *quotaEnforcer) lockTenantOf(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	tenant, err := e.objects.tenantOf(ctx, id)
	if err != nil {
		var notFound *dao.ErrNotFound
		if errors.As(err, &notFound) {
			return nil
		}
		var denied *dao.ErrDenied
		if errors.As(err, &denied) {
			return grpcstatus.Error(grpccodes.PermissionDenied, denied.Reason)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return grpcstatus.FromContextError(err).Err()
		}
		e.logger.ErrorContext(ctx, "Failed to read the tenant of the object",
			slog.String("id", id),
			slog.Any("error", err))
		return grpcstatus.Errorf(grpccodes.Internal, "failed to read the tenant of '%s'", id)
	}
	return lockTenantRow(ctx, e.metrics, tenant)
}

// checkCreate refuses a create that would take one of its scopes over a limit.
func (e *quotaEnforcer) checkCreate(ctx context.Context, candidate proto.Message) error {
	checkStart := time.Now()
	err := e.check(ctx, nil, candidate)
	e.metrics.observeCheck(time.Since(checkStart))
	return err
}

// checkUpdate refuses an update that would take one of its scopes over a limit. The update adds
// the positive difference between the new and the old version of the resource, so a resize down
// frees no quota until the resource runs smaller.
func (e *quotaEnforcer) checkUpdate(ctx context.Context, current, candidate proto.Message) error {
	checkStart := time.Now()
	err := e.check(ctx, current, candidate)
	e.metrics.observeCheck(time.Since(checkStart))
	return err
}

// retryConflict returns the conflict that an earlier create left behind when a refused create
// names a live object. The first create landed, so the caller must see the conflict the write
// would report, not the refusal of a second copy: the CSI driver recovers only from the
// conflict, and a lost response would otherwise strand the volume. It returns nil when the
// refusal is no quota refusal, or when the name belongs to no live object.
func (e *quotaEnforcer) retryConflict(
	ctx context.Context, candidate proto.Message, refusal error,
) error {
	if !isQuotaRefusal(refusal) {
		return nil
	}
	metadata := metadataOf(candidate)
	name := metadata.GetName()
	if name == "" {
		return nil
	}
	exists, err := e.objects.nameExists(ctx, metadata.GetTenant(), metadata.GetProject(), name)
	if err != nil {
		return e.internalCheckError(ctx, metadata.GetTenant(), err)
	}
	if !exists {
		return nil
	}
	table := e.kindTables[quotaKindName(candidate)]
	conflict := &dao.ErrAlreadyExists{Kind: dao.KindNameFor(table), Name: name}
	return ConvertDAOErrorToGRPC(conflict, "create", "")
}

// isQuotaRefusal reports whether the error is a refusal of the check.
func isQuotaRefusal(err error) bool {
	status := grpcstatus.Convert(err)
	if status.Code() != grpccodes.FailedPrecondition {
		return false
	}
	for _, detail := range status.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if ok && info.GetReason() == quotaExceededReason {
			return true
		}
	}
	return false
}

// check prices the candidate, counts the tenant's usage from the database and compares each
// dimension the request adds to against the limits of the request's project, each ancestor
// project and the tenant. A request that adds nothing to a dimension fits even at a limit of 0.
// A create the system makes for an admitted parent adds only what the parent's ask didn't
// already reserve for it (see linkedRelief).
func (e *quotaEnforcer) check(
	ctx context.Context, current, candidate proto.Message,
) error {
	metadata := metadataOf(candidate)
	if metadata == nil {
		return nil
	}
	tenant := metadata.GetTenant()
	if tenant == "" || tenant == auth.SystemTenant || tenant == auth.SharedTenant {
		return nil
	}
	project := metadata.GetProject()

	lookup := &quotaTypeLookup{ctx: ctx, tenant: tenant, enforcer: e}
	candidateQuote, err := quota.Price(
		candidate.ProtoReflect(), lookup, quota.WithoutCreatesOf(e.uncreated...),
	)
	if lookup.failure != nil {
		return e.internalCheckError(ctx, tenant, lookup.failure)
	}
	if err != nil {
		return e.candidatePriceError(ctx, tenant, err)
	}

	added := map[string]int64{}
	if current == nil {
		for dimension, amount := range candidateQuote.Dimensions {
			if amount > 0 {
				added[dimension] = amount
			}
		}
		// A resource the system creates for an admitted parent nets against the room the
		// parent's ask already reserved for it, so it adds only what the parent didn't ask
		// for:
		relief, err := e.linkedRelief(ctx, tenant, candidate, candidateQuote.Dimensions)
		if err != nil {
			return e.internalCheckError(ctx, tenant, err)
		}
		for dimension, amount := range relief {
			added[dimension] -= amount
			if added[dimension] <= 0 {
				delete(added, dimension)
			}
		}
	} else {
		currentQuote, err := quota.Price(
			current.ProtoReflect(), lookup, quota.WithoutCreatesOf(e.uncreated...),
		)
		if lookup.failure != nil {
			return e.internalCheckError(ctx, tenant, lookup.failure)
		}
		if err != nil {
			return e.internalCheckError(ctx, tenant, err)
		}
		for dimension, amount := range candidateQuote.Dimensions {
			delta := amount - currentQuote.Dimensions[dimension]
			if delta > 0 {
				added[dimension] = delta
			}
		}
		// A parent's counted amount is greatest(ask - linked, 0) per dimension: its linked
		// children already count in their own rows. Net the delta the same way, so growing
		// the ask while the parent still holds surplus children adds only what runs beyond
		// them:
		if err = e.netLinkedParentDelta(ctx, tenant, candidate, currentQuote, candidateQuote, added); err != nil {
			return e.internalCheckError(ctx, tenant, err)
		}
	}
	if len(added) == 0 {
		return nil
	}

	usage, err := e.counter.Count(ctx, tenant)
	if err != nil {
		return e.internalCheckError(ctx, tenant, err)
	}
	state, err := e.readLimits(ctx, tenant)
	if err != nil {
		return e.internalCheckError(ctx, tenant, err)
	}

	refusal := firstQuotaRefusal(added, usage, state, project)
	if refusal != nil {
		return e.refuse(tenant, refusal, candidateQuote.Sources[refusal.dimension])
	}
	// A dry run writes nothing, so it leaves no warning: usage never moves, so the
	// crossing would fire again on every repeated dry run and pile up outbox rows for a
	// request that never happened.
	if !isDryRun(ctx) {
		// The chain plus the tenant itself: a threshold can watch the whole tenant.
		scopes := append(quotaProjectChain(project), "")
		if err = e.warnCrossings(ctx, tenant, usage, state, scopes, added); err != nil {
			return e.internalCheckError(ctx, tenant, err)
		}
	}
	return nil
}

// netLinkedParentDelta replaces each positive delta of a parent update with the change in the
// amount its row is counted at: greatest(candidate ask - linked children, 0) minus
// greatest(current ask - linked children, 0). The linked children are already counted in their
// own kind's rows, so a grow that stays within them adds nothing, and only the part that runs
// beyond them adds. A kind that is no parent has no linked children, so its delta is unchanged.
func (e *quotaEnforcer) netLinkedParentDelta(
	ctx context.Context,
	tenant string,
	candidate proto.Message,
	currentQuote, candidateQuote quota.Quote,
	added map[string]int64,
) error {
	if len(added) == 0 || !e.parentKinds[quotaKindName(candidate)] {
		return nil
	}
	parentID := objectID(candidate)
	if parentID == "" {
		return nil
	}
	linked, err := e.counter.ParentLinked(ctx, tenant, quotaKindName(candidate), parentID)
	if err != nil {
		return err
	}
	for dimension := range added {
		children := linked[dimension]
		if children <= 0 {
			continue
		}
		newCounted := max(candidateQuote.Dimensions[dimension]-children, 0)
		oldCounted := max(currentQuote.Dimensions[dimension]-children, 0)
		net := newCounted - oldCounted
		if net <= 0 {
			delete(added, dimension)
			continue
		}
		added[dimension] = net
	}
	return nil
}

// objectID returns the object's identifier, or an empty string for a message that carries none.
func objectID(message proto.Message) string {
	type identified interface {
		GetId() string
	}
	if object, ok := message.(identified); ok {
		return object.GetId()
	}
	return ""
}

// quotaRefusal names the first limit a request would take its scope over: the scope
// (the request's project, one of its ancestors, or the tenant), the refusing scope's
// project, the dimension, the limit, the usage and what the request adds.
type quotaRefusal struct {
	scope     publicv1.QuotaScope
	project   string
	dimension string
	limit     int64
	usage     int64
	added     int64
}

// firstQuotaRefusal compares what the request adds against the usage and the limits of each
// scope that holds a limit row: the request's project and each ancestor project that has a limit
// of its own, then the tenant. A project without a limit of its own is bound by the tenant's, so
// the tenant step reports it, and a refusal always names the scope whose limit it shows. It
// returns the first crossing, or nil when every dimension fits. The dimensions are compared
// sorted, so the first refusal a caller sees is stable.
func firstQuotaRefusal(
	added map[string]int64,
	usage map[string]map[string]int64,
	limits *quotaLimits,
	project string,
) *quotaRefusal {
	scopes := quotaProjectChain(project)
	for i, scope := range scopes {
		scopeUsage := quotaSubtreeUsage(usage, scope)
		for _, dimension := range quota.SortedKeys(added) {
			limit, found := limits.projectLimit(scope, dimension)
			if !found {
				continue
			}
			if scopeUsage[dimension]+added[dimension] > limit {
				scopeKind := publicv1.QuotaScope_QUOTA_SCOPE_ANCESTOR_PROJECT
				if i == 0 {
					scopeKind = publicv1.QuotaScope_QUOTA_SCOPE_PROJECT
				}
				return &quotaRefusal{
					scope:     scopeKind,
					project:   scope,
					dimension: dimension,
					limit:     limit,
					usage:     scopeUsage[dimension],
					added:     added[dimension],
				}
			}
		}
	}
	tenantUsage := quotaSubtreeUsage(usage, "")
	for _, dimension := range quota.SortedKeys(added) {
		limit := limits.limitOf("", dimension)
		if tenantUsage[dimension]+added[dimension] > limit {
			return &quotaRefusal{
				scope:     publicv1.QuotaScope_QUOTA_SCOPE_TENANT,
				dimension: dimension,
				limit:     limit,
				usage:     tenantUsage[dimension],
				added:     added[dimension],
			}
		}
	}
	return nil
}

// candidatePriceError returns the failure of a price the candidate fails. A candidate that
// leaves out a value an annotation reads is the caller's mistake, so the request is refused as
// invalid and the missing value is named; any other price failure is a failure of the check.
func (e *quotaEnforcer) candidatePriceError(ctx context.Context, tenant string, err error) error {
	value, ok := missingPriceValue(err)
	if !ok {
		return e.internalCheckError(ctx, tenant, err)
	}
	return grpcstatus.Errorf(grpccodes.InvalidArgument, "the request doesn't set '%s'", value)
}

// missingPriceValue reads the price error that names a value the request left out: an
// annotation reads a field the candidate doesn't set, as in "creates set path
// 'baremetal_instance_type' isn't set". The price returns plain errors, so their two shapes
// are matched here.
func missingPriceValue(err error) (value string, ok bool) {
	const suffix = "' isn't set"
	for _, prefix := range []string{"creates set path '", "creates count field '"} {
		rest, found := strings.CutPrefix(err.Error(), prefix)
		if !found {
			continue
		}
		value, found = strings.CutSuffix(rest, suffix)
		if found && value != "" {
			return value, true
		}
	}
	return "", false
}

// internalCheckError hides the failure's detail from the caller and keeps it in the log.
func (e *quotaEnforcer) internalCheckError(ctx context.Context, tenant string, err error) error {
	e.logger.ErrorContext(ctx, "Failed to check the quota",
		slog.String("tenant", tenant),
		slog.Any("error", err))
	return grpcstatus.Errorf(grpccodes.Internal, "failed to check the quota")
}

// linkedRelief returns the room the candidate's parent reserved for it, per dimension: the
// smaller of the parent's reserved-but-uncreated room and the candidate's own price. The usage
// counter counts a parent's ask beyond what OSAC created for it, so a resource the system
// creates for an admitted parent converts reserved room into an existing row and adds nothing;
// a resource beyond the parent's ask, or with no parent, adds its full price. The creator gate
// is the same trust gate the counter's exists side uses: a user's create never nets, or a user
// could claim a parent and get room for free.
func (e *quotaEnforcer) linkedRelief(
	ctx context.Context, tenant string, candidate proto.Message, price map[string]int64,
) (map[string]int64, error) {
	if metadataOf(candidate).GetCreator() != systemCreator {
		return nil, nil
	}
	parentKind, parentID, ok := e.linkedParent(candidate)
	if !ok {
		return nil, nil
	}
	excess, err := e.counter.ParentExcess(ctx, tenant, parentKind, parentID)
	if err != nil {
		return nil, err
	}
	relief := map[string]int64{}
	for dimension, room := range excess {
		amount := min(room, price[dimension])
		if amount > 0 {
			relief[dimension] = amount
		}
	}
	return relief, nil
}

// linkedParent resolves the parent a linked candidate names, through the link the registry
// declares for its kind: a field link reads the parent's identifier from the candidate's
// reference field, such as a host's "status.cluster"; a name-prefix link reads it from the
// candidate's name, where it is the 36 characters after the literal prefix, because a parent's
// identifier is a uuidv7. ok is false when the kind links by labels (the parent's kind sits on
// the candidate itself, and the resources that use that link, the automatic addresses, are
// inserted through the DAO, past this check), when the kind links to no parent, or when the
// link names no parent.
func (e *quotaEnforcer) linkedParent(candidate proto.Message) (parentKind, parentID string, ok bool) {
	link, found := e.kindLinks[quotaKindName(candidate)]
	if !found || link.Parent == "" {
		return "", "", false
	}
	switch {
	case link.Field != "":
		id, resolved := reflection.ResolveFieldPath[string](candidate, link.Field+".id")
		if !resolved || id == "" {
			return "", "", false
		}
		return link.Parent, id, true
	case link.NamePrefix != "":
		id, resolved := quotaParentIDFromName(metadataOf(candidate).GetName(), link.NamePrefix)
		if !resolved {
			return "", "", false
		}
		return link.Parent, id, true
	}
	return "", "", false
}

// quotaParentIDFromName reads the parent's identifier from the candidate's name: the link's
// name prefix is "ci-{parent}-...", and a parent's identifier is a uuidv7, a fixed 36
// characters, so the identifier is the 36 characters after the literal prefix. ok is false
// when the name doesn't start with the prefix, is too short to carry the identifier, or the
// 36 characters are not a UUID, so a caller-chosen name resolves to no parent instead of a
// wrong one.
func quotaParentIDFromName(name, namePrefix string) (id string, ok bool) {
	prefix, _, _ := strings.Cut(namePrefix, "{parent}")
	if !strings.HasPrefix(name, prefix) {
		return "", false
	}
	if len(name) < len(prefix)+36 {
		return "", false
	}
	id = name[len(prefix) : len(prefix)+36]
	if _, err := uuid.Parse(id); err != nil {
		return "", false
	}
	return id, true
}

// warnOrRollback runs one warning write and, when it fails, logs it, marks the request's
// transaction to roll back, and returns the Internal failure the caller returns. The write the
// warning follows never lands without its warning, and the log keeps the failure's detail that
// the caller's error hides.
func warnOrRollback(
	ctx context.Context, logger *slog.Logger, message, clientMessage string, warn func() error,
) error {
	if err := warn(); err != nil {
		logger.ErrorContext(ctx, message, slog.Any("error", err))
		return failWithRollback(ctx, grpcstatus.Errorf(grpccodes.Internal, "%s", clientMessage))
	}
	return nil
}

// refuse builds the quota refusal: a FailedPrecondition whose detail carries the fields
// that name the failing scope, the dimension, the limit, the usage and what the request
// adds.
func (e *quotaEnforcer) refuse(tenant string, refusal *quotaRefusal, sources []string) error {
	e.metrics.recordRefusal(refusal.dimension)
	scopeName := refusal.project
	if scopeName == "" {
		scopeName = "default project"
	}
	subject := fmt.Sprintf("project %s", scopeName)
	if refusal.scope == publicv1.QuotaScope_QUOTA_SCOPE_TENANT {
		subject = fmt.Sprintf("tenant %s", tenant)
	}
	status := grpcstatus.New(grpccodes.FailedPrecondition, fmt.Sprintf(
		"quota exceeded: %s, %s: limit %d, usage %d, adding %d",
		subject, refusal.dimension, refusal.limit, refusal.usage, refusal.added,
	))
	detailed, err := status.WithDetails(
		&errdetails.ErrorInfo{Reason: quotaExceededReason, Domain: "osac"},
		publicv1.QuotaFailure_builder{
			Scope:     refusal.scope,
			Tenant:    tenant,
			Project:   refusal.project,
			Dimension: refusal.dimension,
			Limit:     refusal.limit,
			Usage:     refusal.usage,
			Added:     refusal.added,
			Sources:   sources,
		}.Build(),
	)
	if err != nil {
		return grpcstatus.Errorf(grpccodes.Internal, "failed to attach the quota failure detail: %v", err)
	}
	return detailed.Err()
}

// quotaLimits holds the tenant's limits: its overrides, its projects' limits and the defaults.
type quotaLimits struct {
	defaults  map[string]int64
	overrides map[string]int64
	projects  map[string]map[string]int64
}

// limitOf resolves the limit of a dimension at a scope: the project's own limit when one
// exists, else the tenant's override, the default, 0.
func (l *quotaLimits) limitOf(scope, dimension string) int64 {
	if value, found := l.projectLimit(scope, dimension); found {
		return value
	}
	return resolveQuotaLimit(l.overrides, l.defaults, dimension)
}

// projectLimit returns the project's own limit of a dimension, when the project has one.
func (l *quotaLimits) projectLimit(project, dimension string) (int64, bool) {
	value, found := l.projects[project][dimension]
	return value, found
}

// effectiveLimitOf resolves the limit that binds a scope: the tightest of the tenant's limit
// and the own limit of the scope and each of its named ancestors. Enforcement walks that
// chain and refuses at the first scope over its limit, so the tightest bound is the limit
// that binds the scope, not its own-or-tenant fallback.
func (l *quotaLimits) effectiveLimitOf(scope, dimension string) int64 {
	limit := resolveQuotaLimit(l.overrides, l.defaults, dimension)
	for _, ancestor := range quotaProjectChain(scope) {
		if value, ok := l.projects[ancestor][dimension]; ok && value < limit {
			limit = value
		}
	}
	return limit
}

// readLimits reads the tenant's overrides, its projects' limits and the default limits, in the
// request's transaction. A cleared limit is deleted, so only rows without a deletion timestamp
// apply. It reads the limits directly rather than through the DAO because the DAO applies caller
// visibility, which the check must not: it always sees all of the tenant's limits.
func (e *quotaEnforcer) readLimits(ctx context.Context, tenant string) (result *quotaLimits, err error) {
	tx, err := database.TxFromContext(ctx)
	if err != nil {
		return nil, err
	}
	result = &quotaLimits{
		defaults:  map[string]int64{},
		overrides: map[string]int64{},
		projects:  map[string]map[string]int64{},
	}
	defaultRows, err := tx.Query(ctx, `
		select data -> 'spec' ->> 'dimension', coalesce((data -> 'spec' ->> 'value')::bigint, 0)
		from quota_default_limits
		where deletion_timestamp = 'epoch'`)
	if err != nil {
		return nil, err
	}
	defer defaultRows.Close()
	for defaultRows.Next() {
		var dimension string
		var value int64
		if err = defaultRows.Scan(&dimension, &value); err != nil {
			return nil, err
		}
		result.defaults[dimension] = value
	}
	if err = defaultRows.Err(); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select project::text, data -> 'spec' ->> 'dimension', coalesce((data -> 'spec' ->> 'value')::bigint, 0)
		from quota_limits
		where tenant = $1 and deletion_timestamp = 'epoch'`, tenant,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var project, dimension string
		var value int64
		if err = rows.Scan(&project, &dimension, &value); err != nil {
			return nil, err
		}
		if project == "" {
			result.overrides[dimension] = value
			continue
		}
		if result.projects[project] == nil {
			result.projects[project] = map[string]int64{}
		}
		result.projects[project][dimension] = value
	}
	return result, rows.Err()
}

// quotaProjectChain returns the project and each of its named ancestors, most specific
// first. The default project has no name of its own: a limit on it is the tenant's override,
// so the tenant check covers it and the chain of a request in the default project is empty.
func quotaProjectChain(project string) []string {
	if project == "" {
		return nil
	}
	segments := strings.Split(project, ".")
	chain := make([]string, 0, len(segments))
	for i := len(segments); i >= 1; i-- {
		chain = append(chain, strings.Join(segments[:i], "."))
	}
	return chain
}

// quotaSubtreeUsage sums the usage of the scope's project and the projects inside it. The
// default project is the root of the tree, so an empty scope sums the whole tenant.
func quotaSubtreeUsage(usage map[string]map[string]int64, scope string) map[string]int64 {
	result := map[string]int64{}
	for project, dimensions := range usage {
		if scope != "" && project != scope && !strings.HasPrefix(project, scope+".") {
			continue
		}
		for dimension, amount := range dimensions {
			result[dimension] += amount
		}
	}
	return result
}
