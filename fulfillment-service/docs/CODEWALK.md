# Codewalk: request lifecycle

This document explains three things that are not obvious from reading any single file in
isolation: why there is a single data model instead of the usual DTO/domain/persistence split, how
the gRPC transaction interceptor works and why it exists, and how a `Create` call turns into a
Kubernetes custom resource on a workload cluster. Read this after [docs/API.md](API.md), which
covers the public/private API split referenced throughout.

File paths below link to `main`; if you're reading this from a different branch or commit, the
line numbers in the linked files may have moved.

## The data model: protobuf as the single model, Kubernetes-style

Most services keep three separate representations of an entity: a wire DTO, an in-memory domain
object, and a persistence model, with mapping code between them. This codebase does not do that.

[`internal/api/osac/private/v1.BareMetalInstance`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/api/osac/private/v1/baremetal_instance_type.pb.go)
(generated from
[`proto/private/osac/private/v1/baremetal_instance_type.proto`](https://github.com/osac-project/osac/blob/main/fulfillment-service/proto/private/osac/private/v1/baremetal_instance_type.proto))
is simultaneously:

- the **wire format** for the private gRPC API (`BareMetalInstancesCreateRequest.object`),
- the **object business logic operates on directly** — validation functions such as
  `PrivateBareMetalInstancesServer.validateSpec` in
  [`internal/servers/private_baremetal_instances_server.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/servers/private_baremetal_instances_server.go)
  call `bmi.GetSpec()` / `bmi.SetNetworkAttachments(...)` on the proto type itself, no separate
  domain struct exists, and
- the **persistence model** — `dao.GenericDAO[O]` serializes the object (minus `id` and `metadata`,
  which get their own columns) as JSON into the `data` column of a hand-written Postgres table (see
  [`internal/database/migrations/54_create_baremetal_tables.up.sql`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/database/migrations/54_create_baremetal_tables.up.sql)).

This mirrors Kubernetes itself: a CRD Go type is simultaneously the API type, the etcd storage
format, and the object controllers reconcile against — no intermediate entity layer either. It is a
deliberate choice, not an oversight ([`AGENTS.md`](../AGENTS.md) states the API follows
[Kubernetes API conventions](https://github.com/kubernetes/community/blob/main/contributors/devel/sig-architecture/api-conventions.md)).

The tradeoff: no separate mapping boilerplate between three sets of types for a CRUD-heavy service,
at the cost of coupling business logic to the protobuf-generated accessor API (`Get`/`Set`,
builders) and coupling storage layout to the wire schema.

**Two proto models, not one.** There is still a public/private split:
`publicv1.BareMetalInstance` is generated from the private one by `protoc-gen-cleanapi`, filtering
out fields marked `[(cleanapi.field).private = true]` (see [docs/API.md](API.md)). At runtime, the
public gRPC server
([`internal/servers/baremetal_instances_server.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/servers/baremetal_instances_server.go))
delegates to the private server and converts between the two representations using
`GenericMapper[From, To]`
([`internal/servers/generic_mapper.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/servers/generic_mapper.go)),
which copies matching fields by protobuf reflection. Fields that don't exist on one side are simply
skipped (or ignored explicitly via `AddIgnoredFields`).

**Table naming and JSON encoding are derived by reflection, not hardcoded.**
`GenericDAOBuilder[O].tableName()`
([`internal/database/dao/generic_dao.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/database/dao/generic_dao.go))
converts the proto message name to `snake_case` and pluralizes it (`BareMetalInstance` →
`bare_metal_instances`) unless overridden with `SetTableName`. The same generics pattern
(`GenericServer[O]`, `GenericDAO[O]`) means the CRUD implementation is written once and specialized
per resource purely through Go generics plus protobuf reflection over the resource's own
`ServiceDesc` — not by writing per-resource plumbing code.

## Transactions: why the interceptor commits so late, and what it buys us

`TxInterceptor.UnaryServer`
([`internal/database/database_tx_interceptor.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/database/database_tx_interceptor.go))
wraps every unary gRPC call: it begins a transaction before the handler runs, injects it into the
context (`TxIntoContext`), and commits or rolls back in a deferred block after the handler returns.
Any code downstream — DAOs, the `Notifier` — retrieves the current transaction with
`database.TxFromContext(ctx)` instead of receiving it as a parameter.

This is a known pattern (**transaction-per-request**, e.g. Django's `ATOMIC_REQUESTS`), with the
usual known downside: the transport layer (gRPC) ends up owning a persistence-layer concern
(commit/rollback), and a slow handler holds a Postgres connection for the whole request. Two things
mitigate this here:

- **Transactions are lazy.** `txManager.Begin`
  ([`internal/database/database_tx_manager.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/database/database_tx_manager.go))
  only creates a Go-level wrapper; the real `pgx.Tx` is only opened on the first `Query`/`Exec`
  (`managedTx.ensureReal`). A request that never touches the database never opens a real Postgres
  transaction, so the cost isn't "every request pays for a transaction" — only requests that do DB
  work do.
- **It enables a transactional outbox for events.** `Notifier.Notify`
  ([`internal/database/database_notifier.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/database/database_notifier.go))
  inserts the event payload into a `notifications` table and calls `pg_notify` **using the same
  transaction as the business-logic change** (it reads the transaction from context, same as
  everything else). Because both writes are in the one transaction the interceptor already opened,
  event emission is atomic with the change that caused it — an event is never sent for a change
  that then rolls back, and a committed change never fails to notify because of an unrelated later
  error in the same request. Doing this without the interceptor-managed transaction would mean
  every handler manually opening a transaction and remembering to include the notification insert
  in it — easy to forget in one of the many `*_server.go` files.

Known footgun: rollback is driven by `Tx.ReportError(&err)`
([`internal/database/database_tx.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/database/database_tx.go)),
which must be called (typically via `defer`) by any code path that can produce an error inside the
transaction. If a handler forgets it, its error will not trigger a rollback — this is not enforced
by the type system.

### `pg_notify` is a hint, not a guarantee — and that's by design

It's tempting to read the outbox pattern above as "the event delivery mechanism" and worry about
what happens if a `NOTIFY` is missed — Postgres does not persist `NOTIFY` payloads for clients that
aren't listening at the time, and a dropped gRPC `Watch` stream loses whatever was sent while it was
disconnected. In this system that's fine, because `pg_notify` is only ever used as a **low-latency
hint that something changed**, never as the source of truth for *what* changed:

- The actual event payload lives in the `notifications` table, written in the same transaction
  (see above) — `pg_notify` only carries the row's `id` ([`internal/database/database_notifier.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/database/database_notifier.go)).
  A subscriber that receives the notification still re-reads the row to get the payload, so a
  notification is never "the data" — just a poke to go look.
- Every `controllers.Reconciler[O]`
  ([`internal/controllers/reconciler.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/controllers/reconciler.go))
  also runs a periodic full `List()` (`syncObjects`, `syncInterval`, default one hour) independently
  of the `Watch` stream. If a notification is missed entirely — the controller was disconnected, the
  `EventsServer` restarted, whatever — the next full sync still picks up the object and reconciles
  it, just later. The `Watch` stream is the fast path; the periodic sync is the correctness
  fallback that makes the fast path safe to lose.

This is the same design principle Kubernetes controllers use: informer watches are an optimization
for latency, but every controller is written to tolerate a missed or replayed watch event because a
relist will eventually re-deliver the same state. Here it means the interesting failure mode isn't
"lost update" (the periodic sync heals that) but "stale until the next sync", bounded by
`syncInterval`.

## End-to-end flow: `Create` for a `BareMetalInstance`

```text
grpc client
  │  Create(BareMetalInstancesCreateRequest)
  ▼
publicv1.RegisterBareMetalInstancesServer (generated ServiceDesc, internal/api/osac/public/v1)
  │  routes "/osac.public.v1.BareMetalInstances/Create" to the registered BareMetalInstancesServer
  ▼
servers.BareMetalInstancesServer.Create (internal/servers/baremetal_instances_server.go)
  │  GenericMapper: public BareMetalInstance -> private BareMetalInstance
  ▼
servers.PrivateBareMetalInstancesServer.Create (internal/servers/private_baremetal_instances_server.go)
  │  resource-specific validation: catalog item, spec, default network attachments,
  │  fabric manager requirement, then delegates to the shared CRUD implementation
  ▼
servers.GenericServer[*privatev1.BareMetalInstance].Create (internal/servers/generic_server.go)
  │  calls dao.Create()
  ▼
dao.GenericDAO[O].Create (internal/database/dao/generic_dao_create.go)
  │  INSERT into bare_metal_instances: structured columns (id, name, tenant, labels...)
  │  + JSON-serialized remainder in the `data` column
  │  (runs inside the transaction opened by TxInterceptor for this request)
  ▼
events.Notifier.Notify (internal/database/database_notifier.go)
  │  INSERT into `notifications`, then `pg_notify('events', id)` — same transaction, so this
  │  is atomic with the row insert above
  ▼
  (transaction commits when TxInterceptor's deferred End() runs)
  │
  ▼
servers.EventsServer (Watch RPC, internal/servers/events_server.go)
  │  LISTENs on the `events` channel, re-reads the full payload from `notifications` by id,
  │  streams it to any Watch subscriber
  ▼
controller process (separate binary: internal/cmd/service/start/controller/start_controller_cmd.go)
  │  controllers.Reconciler[*privatev1.BareMetalInstance] (internal/controllers/reconciler.go)
  │  is a gRPC *client* of the private API; its watchEvents loop receives the event and pushes
  │  the object onto objectChannel; the main Start loop re-reads the object fresh (getObject)
  │  and calls the reconciler function
  ▼
baremetalinstance.run / task.update (internal/controllers/baremetalinstance/baremetalinstance_reconciler_function.go)
  │  - adds a finalizer on the fulfillment-service object (via another gRPC Update call)
  │  - selects a target "hub" cluster (hubCache)
  │  - controllerutil.CreateOrPatch(hubClient, &bmfov1alpha1.BareMetalInstance{...}, mutateBMI)
  ▼
Kubernetes CR created/patched on the hub cluster
  (bmfov1alpha1.BareMetalInstance, type owned by bare-metal-fulfillment-operator)
  │
  ▼
bare-metal-fulfillment-operator reconciles the CR against real hardware (separate component)
  │
  ▼
controller's task.syncStatus reads the CR's .Status back, and run() calls
BareMetalInstancesClient.Update (another gRPC round-trip) to persist the synced status in Postgres
```

File references for the diagram above:

- [`internal/api/osac/public/v1`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/api/osac/public/v1) (generated `ServiceDesc`)
- [`internal/servers/baremetal_instances_server.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/servers/baremetal_instances_server.go)
- [`internal/servers/private_baremetal_instances_server.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/servers/private_baremetal_instances_server.go)
- [`internal/servers/generic_server.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/servers/generic_server.go)
- [`internal/database/dao/generic_dao_create.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/database/dao/generic_dao_create.go)
- [`internal/database/database_notifier.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/database/database_notifier.go)
- [`internal/servers/events_server.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/servers/events_server.go)
- [`internal/cmd/service/start/controller/start_controller_cmd.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/cmd/service/start/controller/start_controller_cmd.go)
- [`internal/controllers/reconciler.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/controllers/reconciler.go)
- [`internal/controllers/baremetalinstance/baremetalinstance_reconciler_function.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/controllers/baremetalinstance/baremetalinstance_reconciler_function.go)

Key point: the `controller` binary is not a code path inside `grpc-server` — it is a **separate
process acting as a gRPC client** of the private API, decoupled from the write path purely through
the Postgres `LISTEN`/`NOTIFY` mechanism described above. This is why `Notifier`/`EventsServer`
exist at all: without them, nothing outside the `grpc-server` process would know that a row changed
except by polling (which `syncObjects`, `Reconciler`'s periodic full `List()`, still does as a
fallback safety net — see `syncInterval` in
[`internal/controllers/reconciler.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/controllers/reconciler.go),
and the "`pg_notify` is a hint" note above).

**The controller never touches Postgres directly.** `internal/controllers/` has no dependency on
`internal/database`. Every interaction with fulfillment-service state — reading the object fresh
(`Reconciler.getObject`), listing hubs (`hubsClient.List`), persisting the reconciled status back
(`bareMetalInstancesClient.Update`) — goes through generated gRPC client stubs
(`privatev1.NewBareMetalInstancesClient(connection)` etc.) built on the same `*grpc.ClientConn` the
`controller` process opens to `grpc-server`. This is why the flow diagram above has the controller
calling back into the private API rather than writing to the database itself.

### Why this doesn't loop forever

Step "controller calls back into the gRPC client to update the DB" does not retrigger itself
indefinitely, because `GenericServer[O].Update` only persists (and therefore only fires a new event)
when the merged object actually differs from what's stored:

```go
// internal/servers/generic_server.go
if !s.equivalentObjects(tmpObject, currentObject) {
    // ... dao.Update(), which is what fires the event
} else {
    responseObject = tmpObject // no write, no event
}
```

`equivalentObjects`
([`internal/servers/generic_server.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/servers/generic_server.go))
does a field-by-field protobuf-reflection comparison, deliberately ignoring `creation_timestamp`,
`deletion_timestamp`, and `version` on the metadata message — the fields that would otherwise always
differ between two reads and make every reconcile look like a change. The reconcile loop therefore
*converges* instead of looping forever, the same way a Kubernetes controller converges: each pass
either changes something concrete (a finalizer gets added, `status.hub` gets set, the CR gets
created, a status condition changes) and triggers exactly one more pass, or it changes nothing and
the chain stops. A `BareMetalInstance` typically settles after two or three passes (add finalizer →
set hub/create CR → status catches up to the CR → no more diff).

### Hub selection

A "hub" is a target Kubernetes cluster the CR gets created on (see `Hub`/`HubSpec` in
[`proto/private/osac/private/v1/hub_type.proto`](https://github.com/osac-project/osac/blob/main/fulfillment-service/proto/private/osac/private/v1/hub_type.proto));
OSAC supports multiple hubs for workload placement. `task.selectHub`
([`internal/controllers/baremetalinstance/baremetalinstance_reconciler_function.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/controllers/baremetalinstance/baremetalinstance_reconciler_function.go))
implements the selection:

```go
func (t *task) selectHub(ctx context.Context) error {
    t.hubId = t.bareMetalInstance.GetStatus().GetHub()
    if t.hubId == "" {
        response, err := t.r.hubsClient.List(ctx, privatev1.HubsListRequest_builder{}.Build())
        // ...
        t.hubId = response.Items[rand.IntN(len(response.Items))].GetId()
    }
    hubEntry, err := t.r.hubCache.Get(ctx, t.hubId)
    // ...
}
```

The hub is chosen **once**, at random among all hubs returned by `HubsClient.List`, the first time
the object is reconciled (`status.hub` empty). Once chosen it is persisted to `status.hub` and every
subsequent reconcile reuses it — there's no re-balancing or capacity-aware placement.
`HubCache.Get`
([`internal/controllers/hub_cache.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/controllers/hub_cache.go))
resolves the hub id to a namespace and a controller-runtime client, cached with a 5 minute TTL
(`DefaultHubCacheTTL`).

This exact `selectHub` function is duplicated near-identically across roughly a dozen other
reconciler functions (`cluster`, `computeinstance`, `externalip`, `subnet`, `virtualnetwork`, ...) —
worth knowing if you need to change placement logic, since today it means changing it in every
copy.
