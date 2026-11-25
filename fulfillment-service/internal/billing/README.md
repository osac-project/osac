# Billing

This guide describes the private billing API and generic CLI support for Cloud Provider Admins.
See the [API service conventions](../../docs/API.md#services) and
[authorization configuration](../../docs/AUTH.md#authorization-configuration).

## Billing API

`TenantBillingAssignments.Signal` is a private, synchronous exception: it revalidates the current
assignment through the same path as re-submitting `Update`, without creating another history
interval. Closed intervals are rejected. The normalized object table has no generic change trigger
or billing controller, so it emits no outbox signal and starts no background reconciliation. Only
Cloud Provider Admins and controllers may call it.

The Cloud Provider Admin workflow uses the private API to discover accounts with
`BillingAccounts.List`, check configuration with `BillingProviderStatuses.Get`, and list, get,
create, or update tenant billing assignments. The private API also provides assignment `Signal` for
administrators/controllers. Assignment reads and writes are additionally constrained by the
authenticated subject's trusted tenant scope; current platform administrators have universal
tenant scope. A `cloud-provider-admin` realm claim alone grants no access. There is no tenant
self-service assignment read. The assignment's `metadata.tenant` is data, not an authorization
credential. The private metering read and identity are owned by
[OSAC-6888](https://redhat.atlassian.net/browse/OSAC-6888), not this administrative API.

Account `Get`, `Create`, `Update` and `Delete`, and assignment `Delete`, are descriptor-only
scaffolds. They are defined on the private API, so administrators can reach them; each returns
`Unimplemented`. Accounts have no `Signal` RPC. Provider status is a get-only singleton, not a
lifecycle object.

Accounts and assignments use the standard `id`, `metadata`, `spec`, `status` envelope:

- Discovered accounts are system-global: `metadata.tenant` is `system`, project is empty,
  new discovery records use creator `system`, and `spec` is empty. Migrated legacy accounts
  retain unknown creation/creator evidence (epoch/empty), not invented discovery-time history.
  Their stable `metadata.name` is `ba-` plus the first 60 lowercase hexadecimal SHA-256
  characters of the UTF-8 OSAC `id`. Provider labels never determine identity. All observations
  live in `status`, including `provider_key`, `display_name`, `base_currency`, `state` and
  `state_observation_timestamp`.
- The private-only `status.provider_account_reference` is the provider-native account identity,
  not the OSAC `id` or display label.
- `status.external_billing_account_id` is the optional, locally retained, provider-agnostic
  attribution token, not an M360 account ID, tenant ID or credential. Absence and a present
  empty value are distinct, and stored raw values are not trimmed or rewritten. It can be
  established only while assigning an account, including when the assignment is `PENDING` and
  no provider account carries it yet.
- The private-only `status.provider_external_id` is the raw provider-reported external ID,
  separate from the assignment token. Discovery retains each account by its native identity even
  if multiple accounts report the same external ID. Administrators can inspect the reported
  values or filter on `this.status.provider_external_id` to find accounts reporting an exact
  value. A new observation reflects provider corrections; absence clears the observed ID.
  Discovery records no conflict status and never reserves an assignment token.
- Assignments use `metadata.tenant` for tenant ownership and a typed `spec.billing_account`
  reference, accepting `id`, `name` or both; both billing-account selectors must agree. Responses
  retain canonical identities even after tenant deletion. The owner is also recorded in the
  `osac.openshift.io/tenant` annotation; assignments are not project-scoped. Only the atomic
  `spec.billing_account` reference is writable on Update; its selectors are not merged independently.
  Replacement closes the old interval and creates a new ID with the same metadata name;
  same-account revalidation preserves both ID and `status.effective_from_timestamp`.
  `status.effective_to_timestamp` is absent for the current interval; closed intervals are
  immutable `[from, to)` history, including zero-length intervals. `status.connection_state` and
  `status.message` report validation/remediation.

Billing assignments use standard OSAC object envelopes and typed references rather than the scalar
IDs shown in the design.

For direct gRPC `TenantBillingAssignments.Update`, omit `update_mask` or select
`spec.billing_account`, `spec.billing_account.id`, or `spec.billing_account.name`. These paths all
replace the entire reference atomically; selector fields are not merged independently. A present
empty mask leaves the assignment unchanged without provider revalidation.

The private REST `PATCH /api/private/v1/billing/tenant_billing_assignments/{id}` takes the object
fields directly, for example `{"spec":{"billing_account":{"name":"new-account"}}}`. Use `id`
instead of `name` to select by ID. The generated gateway infers update masks from body fields
(`proto/gen/osac/private/v1/tenant_billing_assignments_service.pb.gw.go`) and does not normalize
selector paths. `PrivateTenantBillingAssignmentsServer.Update` in
`internal/servers/private_tenant_billing_assignments_server.go` accepts the whole-reference and
selector paths, applying them as one atomic replacement; other paths are rejected. No body or `{}`
yields an empty mask and a no-op; an empty `update_mask` query value is not an empty path list.

Billing filters use nested descriptor paths, such as `this.status.state == 1` (ACTIVE),
`this.status.connection_state == 2` (CONNECTED), `!has(this.status.effective_to_timestamp)`, and
`status.external_billing_account_id`. Enum comparisons are numeric, not the type-prefixed names used
by JSON responses. Timestamp comparisons use `now` or `null`, as documented in
[FILTER.md](../../docs/FILTER.md). List ordering remains unimplemented, as for other Fulfillment
object types; the request field is ignored and callers must not rely on result order. Old flat
account fields, `correlation_id`, scalar `tenant_id`/`billing_account_id`, and
`effective_from`/`effective_to` are not aliases. The merged design's example request body predates
this amendment; it used the flat scalar fields and does not parse against this API. REST and CLI
structured output use the nested snake-case field names.

The billing schema is a forward-only set with no down migrations, numbered after main's last migration
(156). Migration 157 creates `billing_accounts` with the standard envelope, JSON-expression uniqueness
indexes and validation triggers. Migration 158 creates `tenant_billing_assignments` with JSON
current-interval uniqueness, validation and reference triggers, and history protection. Neither table
has a foreign key on the tenant, and the account reference is checked by trigger. Migration 159
releases finalized tenant domains, 160 creates the deleted tenant identity store, and 161 holds the
tenant-lifecycle triggers. Coordinate service/database cutover: old binaries require the old schema,
and binaries expecting these tables require the new one. Stable correlation-related reason codes and
provider-native `externalId` remain unchanged. This does not add an assignment-to-metering bridge.

Discovery refreshes a supplied, nonempty `base_currency` from a non-older provider observation;
empty or stale observations do not erase it. Three-letter currency codes are normalized to
uppercase, and no currency is invented or defaulted. Rate-card-derived currency resolution is owned
by [OSAC-6890](https://redhat.atlassian.net/browse/OSAC-6890); account discovery alone does not
implement that pricing boundary.

Correlation resolution is read-only for every adapter. It retains, adopts or generates a local
identifier, then completes one bounded exact-carrier walk. `CONNECTED` requires the selected
account to be the sole provider-side carrier; zero carriers leaves the assignment `PENDING`. An
external administrator sets the provider `externalId` to the
`status.external_billing_account_id` shown by `BillingAccounts.List`, then re-submits same-account
Update (or private Signal) to validate the unique match without replacing or retiming the interval.
Foreign, multiple or inconsistent carriers fail with `ACCOUNT_AMBIGUOUS`. Fix duplicate external
IDs in the provider and retry; the next discovery refresh reflects the correction. A token already
used by an assignment is not rotated by discovery. OSAC makes no provider account writes,
regardless of vendor capabilities. `PENDING` requires manual provider-side remediation, and the
gated publish/launch checks fail with `BILLING_ACCOUNT_UNAVAILABLE` until successful revalidation.
This is not a provider setter or write/readback workflow.

Assignment creation, replacement, and same-account revalidation verify the identity of the selected
provider account and take its state and external ID from that same read. The stored state is the last
observation and cannot alone decide eligibility at preflight: an inactive or unknown account is
rejected even if discovery previously stored `ACTIVE`, a stale stored `INACTIVE` or `UNKNOWN` does
not block an account the provider reports active now, and provider-read failure is not permission to
fall back to that cache. State observations retain their observation time and cannot overwrite a
newer stored observation; a newer observation already stored, or a stored state change committed
after the assignment read, can reject the request. A later observation of the selected account
during the exact-carrier walk can also reject it if that observation is not active. The selected read
and exact-carrier walk share one provider deadline and finish before business locks. This is
assignment-time validation, not continuous provider monitoring.

Every billing unary RPC uses the ordinary ambient request transaction. Validation, provider, page,
mapping or persistence failure rolls back all local changes; a failed outer commit returns
`Internal`. Discovery's full inventory, unseen sweep and requested page are atomic, and its
per-process freshness window advances only after that commit succeeds. Provider reads may occupy a
database connection but hold no business row locks. Assignment finalization locks the tenant, the
complete original selected/previous account set in SQL id order, then the current interval; it
rereads material inputs before merging observations or adopting a token. Changed snapshots and SQL
deadlocks abort without hidden provider retries. One post-I/O, post-lock database clock sample,
clamped to all-history boundaries, is used for both the old interval's end and its replacement's
start. No durable billing operation lease or separate eligibility transaction is used.

Tenant deletion closes its active assignment at the stored deletion boundary, releasing the account
for another tenant. The billing history of a deleted tenant also reserves its name; see
[Deleted tenant identity store](#deleted-tenant-identity-store). Retaining billing history does not
bypass other restrictive tenant dependencies and does not define a purge schedule or reserve
never-known names.

## Authorization and attribution

Billing is private API functionality for Cloud Provider Admins (`is_admin`), with assignment access
additionally limited to the subject's trusted tenant scope. Current platform administrators have
universal tenant scope. `TenantBillingAssignments.Signal` is admin-only. No tenant role or other
realm role grants billing access. No `cloud-provider-admin` realm role is provisioned; a token that
claims it grants nothing. Cloud Provider Admin names a workflow/persona using an existing `is_admin`
identity, not a new realm-role grant.

Account `Get`, `Create`, `Update` and `Delete`, and assignment `Delete`, are descriptor-only
scaffolds: administrators can reach them through the private API, where their generated handlers
return `Unimplemented`. Accounts are system-global objects, with empty Spec and read-only nested
Status; only private descriptors expose `status.provider_account_reference`. Assignment access
checks the stored canonical tenant against trusted scope, not a replacement request reference or
selected CLI project. Generic CLI discovery/completion does not implement these methods or expose
private fields.

The OAuth `aud` claim identifies the Fulfillment API audience, not a billing account or metering
consumer. `status.external_billing_account_id` is an attribution token, not an authentication
token, M360 account ID or permission grant. Neither a provider capability nor private API access
authorizes OSAC to write provider accounts; matching is read-only and `PENDING` remediation is
performed externally.

## Billing CLI

Billing uses generic descriptor-driven `get`, `create`, `edit`, `label` and `delete` dispatch, not
billing-specific command adapters. Descriptor-defined scaffold calls through the private API return
`Unimplemented`. Use private mode for the Cloud Provider Admin workflow; private Signal is an
internal revalidation operation.

Canonical short types are `billingaccount`/`billingaccounts`,
`tenantbillingassignment`/`tenantbillingassignments`, and
`billingproviderstatus`/`billingproviderstatuses`; fully qualified private names also work. Private
mode prefers private descriptors for short names. The former `billing-accounts`,
`tenant-billing-assignments`, `billing-provider-status` and `tba` names are retired, as are
billing-specific `--limit`/`--offset` flags and per-page notices.

Account keys match OSAC ID or stable metadata name, not display label, private native identity or
attribution token. Accounts ignore selected tenant/project filters; assignments retain selected
tenant scoping but never selected project scoping. Explicit CEL filters still narrow results and
cannot expand server-authorized visibility. Create/edit use the standard envelope and typed
references, not the retired scalar flags. In `osac edit tenantbillingassignment <key>`, replace the
entire `spec.billing_account` reference; when selecting by just ID or name, remove the other
selector rather than leaving it set to the previous account. Leave other fields unchanged. Saving
without changes sends an empty mask and does not revalidate the account.

The account, assignment and provider-status table layouts are generic. Accounts show ID, NAME,
provider, display label, currency, state, attribution token and observation time; assignments show
ID, NAME, canonical tenant/account, state, interval timestamps and message. Descriptor-supported
lifecycle objects also show DELETING. Provider status instead uses six horizontal status columns:
state, provider, message, capabilities, last successful check and usage delivery; it has no
lifecycle columns. Native identity is not a default table column, even privately. Table cells
sanitize control characters without rewriting structured output or stored raw tokens.

JSON/YAML render each item through protobuf Any with `@type` and snake-case fields: zero items are
an empty list, one item is an object, and multiple items are a list. There is no CLI
`size`/`total`/`items` envelope; the API List envelope remains unchanged. Provider status performs
exactly one empty Get and rejects keys, `--filter` and `--watch`, including explicit empty/false
flags. Its capability display describes provider information, not permission for provider writes.

## Deleted tenant identity store

Migration 160 creates `deleted_tenant_identities`, a credential-free store keyed by tenant name. It
reserves a deleted tenant name and records who deleted it. Its columns are `tenant_name` (primary
key), `tenant_created_at`, `deletion_timestamp`, `deleted_by` (nullable), `finalized_at` (nullable)
and `billing_retained`.

Triggers in migration 161 on `tenants` write the store:

- `validate_tenant_insert` (before insert) rejects a new tenant whose name is retained, with SQLSTATE
  `Z0002` and the message "tenant identity is retained after deletion".
- `capture_tenant_deletion` (before a soft delete) records the marker, with the deleting actor.
- `prepare_tenant_finalization` (before a physical delete) records the marker for a direct delete.
- `finalize_deleted_tenant_identity` (after a physical delete) sets `finalized_at`.

In `private_tenants_server.go`, Create calls `CheckTenantIdentityReusable` and returns FailedPrecondition
with reason `TENANT_IDENTITY_RETAINED` for a reserved name. Update, Delete and Signal take
`LockTenantIdentity` on the name, which serializes them with Create.

A name is reserved while either a deletion is pending (`finalized_at` is null), a finalized marker
exists, or billing history for the name exists in `tenant_billing_assignments`, even when
`billing_retained` is false. A live tenant with the same name is not reported as reserved; the normal
duplicate-name rule applies.

Delete sets `osac.tenant_deletion_actor` from the authenticated subject. The stored `deleted_by`
keeps the first deleter, and a direct database delete records none.

Migration 160 backfills from archived tenants and from tenants already marked deleted. It stops on
invalid chronology or ambiguous incarnations (SQLSTATE `23514`), and on a lock timeout (`55P03`) it
asks the operator to drain tenant writers before retrying.

Revocation of a soft-deleted tenant's access is [OSAC-8145](https://redhat.atlassian.net/browse/OSAC-8145).
Tenant deletion, tombstone and recreation are tracked by
[OSAC-6886](https://redhat.atlassian.net/browse/OSAC-6886).

## Follow-up boundaries

Billing List requests stop at 100 entries until OSAC-8148 is implemented. A soft-deleted tenant
keeps access through its token until OSAC-8145 is implemented.
