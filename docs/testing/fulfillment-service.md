# fulfillment-service

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Inspect test source for individual cases. Paths are relative to
`fulfillment-service/` unless marked as repository-root paths.

Touched-area requirements: [component guide](../../fulfillment-service/AGENTS.md#integration-tests).

## Test tiers

| Tier | Suite (by directory/role) | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | Go suites under `internal/` | Local logic and controlled-client reconcilers | External clients are doubles where the suite declares them; some packages also start PostgreSQL. |
| Component integration (server/database) | `internal/servers` | In-process servers and a real PostgreSQL testcontainer | Not a deployed service; tenancy, attribution, and external clients may be doubles. |
| Component integration (deployed) | The deployed `it/` suite, run through the installer integration target with the fulfillment suite selected | Deployed Fulfillment Service, PostgreSQL, authenticated APIs, Kubernetes, and the CLI from this checkout | Provider networking is disabled. Only the `networking-operator` and `compute-storage` phases deploy the current operator; other cases do not establish operator/provider behavior. |
| Component integration (identity) ([DEV] [OSAC-6553](https://redhat.atlassian.net/browse/OSAC-6553), [OSAC-6554](https://redhat.atlassian.net/browse/OSAC-6554), [OSAC-6555](https://redhat.atlassian.net/browse/OSAC-6555), [OSAC-6556](https://redhat.atlassian.net/browse/OSAC-6556)) | Service-account, RoleBinding, and service-account CLI suites in `it/`, via the deployed target above | Fulfillment, PostgreSQL, Keycloak, and the configured runtime identities | Failure-injection cases run the service and reconciler in process against PostgreSQL and Keycloak; they do not exercise the deployed controller loop, and external provider provisioning is out of scope. |
| E2E | `../tests/e2e/` | Deployed cross-component user journeys | Requires the corresponding operators, AAP, and providers. |

For commands, see the
[component guide](../../fulfillment-service/AGENTS.md#integration-tests) and
run `make -C osac-installer help` from the repository root for the installer targets that drive the
deployed suite.

## ComputeInstance disk handoff (OSAC-6685)

The `compute-storage` component-integration phase runs through the installer
integration target with real Fulfillment APIs, PostgreSQL, the installed
storage and ComputeInstance controllers, and Kind StorageClass lookup. AAP is
a recording double; no provider, CSI, or VM execution. The operator-side
boundary is in the [osac-operator guide](osac-operator.md); deployed disk
identity E2E is [OSAC-6614](https://redhat.atlassian.net/browse/OSAC-6614).

## Selecting coverage

- CLI/API-only contracts belong in `it/`; complete provisioning journeys across
  operators and providers belong in E2E. Use the suite setup and skip
  conditions to identify the actual boundary.
- Tests gated on provider networking skip in the standard fulfillment target;
  their presence is not evidence for the enabled-provider path.
- ExternalIPPool tenant placement: a pool created without a tenant is stored
  as shared, any other tenant is rejected without persisting, a shared pool
  cannot move into a tenant, and a pool stored in a tenant before this rule
  keeps it and still takes status updates. Unit specs use real PostgreSQL with
  mocked tenancy; the deployed private-API rejection does not depend on
  provider networking.
- ExternalIP finalizer retry signaling ([DEV] OSAC-5977): controller unit
  suites cover retry after a transient signal failure and stopping on `NotFound`,
  and verify deletion with provider cleanup pending schedules another signal
  while the Kubernetes finalizer remains. They use mocked gRPC clients; deployed
  provider cleanup stays covered by the OSAC-6307 operator contract tests and
  the OSAC-4843 E2E follow-up.
- Fixture-written readiness conditions prove status projection, not automatic
  watch delivery or the upstream controller's calculation of readiness.
- The networking fixture uses distinct Hub entries/namespaces on one cluster;
  it does not establish isolation between separate Kubernetes clusters.
- The MCP protocol suites in `it/` use the real MCP SDK against deployed
  Fulfillment and PostgreSQL; the network provider is omitted, so that
  coverage does not imply a provisioned VM or provider execution. Read the
  fixtures before extending those cases.
- MCP write safety ([OSAC-5851](https://redhat.atlassian.net/browse/OSAC-5851))
  uses typed client failure injection, a pass-through token exchanger, deployed
  Fulfillment, and PostgreSQL. Its unit suite also uses the generated public
  gRPC client and an in-process server to cover a successful Create whose
  response exceeds the client receive limit. Broader transport faults and
  host-driven stop behavior remain with
  [OSAC-5852](https://redhat.atlassian.net/browse/OSAC-5852). Provider
  provisioning is out of scope.
- MCP network exposure ([DEV] [OSAC-5854](https://redhat.atlassian.net/browse/OSAC-5854)):
  `internal/cmd/service/start/mcpserver` covers typed exposure inputs,
  caller-token forwarding, public-client dispatch, result mapping, and safe
  dependency and authorization errors. `it/it_mcp_networking_test.go` covers
  exposure authorization, no persistence on rejection, and the MCP-to-public
  Fulfillment API path with PostgreSQL. Its ExternalIP and NATGateway
  lifecycle case requires provider networking and is skipped by the standard
  `SUITE=fulfillment` deployment, which disables that feature; this run does
  not establish provider lifecycle coverage. Its ExternalIPAttachment request
  uses missing dependencies and verifies rejection without persistence. Real
  address reachability and NAT behavior belong to
  [QE] [OSAC-5856](https://redhat.atlassian.net/browse/OSAC-5856).
- MCP network replacement is covered only at the DEV boundary in
  [OSAC-5855](https://redhat.atlassian.net/browse/OSAC-5855); provider-backed
  replacement and cleanup remain QE coverage in
  [OSAC-5856](https://redhat.atlassian.net/browse/OSAC-5856).
- VM power and restart coverage
  ([OSAC-5850](https://redhat.atlassian.net/browse/OSAC-5850)) uses
  `it_mcp_server_test.go` through the deployed Fulfillment and PostgreSQL
  boundary, including persisted run-strategy and restart-trigger state,
  an explicit optimistic-version-conflict assertion for
  `update_compute_instance`, conflict retries for the power and restart tools,
  and cross-tenant rejection. The network provider is omitted, so these cases
  do not prove VM execution; deployed lifecycle behavior belongs to
  [OSAC-5852](https://redhat.atlassian.net/browse/OSAC-5852).
- FabricDomain setup and focused execution are documented in
  [the suite README](../../fulfillment-service/it/README.fabric-domain.md).
- Deployed service-account journeys, secret rotation included, are the
  [OSAC-6557](https://redhat.atlassian.net/browse/OSAC-6557) [QE] regression
  suite in `tests/e2e/` (see the [tests/e2e guide](e2e.md)). The UI
  walkthrough remains with the separately owned UI implementation.
- Omitted network-attachment defaulting
  ([OSAC-6480](https://redhat.atlassian.net/browse/OSAC-6480), DEV) is
  covered by deployed public-API cases and unit coverage in the servers
  suites; provider provisioning remains E2E coverage.
- Default networking rejection
  ([OSAC-6601](https://redhat.atlassian.net/browse/OSAC-6601),
  [OSAC-7044](https://redhat.atlassian.net/browse/OSAC-7044), DEV): creates
  with a missing or not-ready tenant default are rejected in the private
  servers suite and the deployed public Create; provider provisioning remains
  E2E coverage.
- Quota tables and APIs ([DEV] [OSAC-7613](https://redhat.atlassian.net/browse/OSAC-7613)):
  the five quota tables are covered by
  `internal/database/migrations/154_create_quota_tables_test.go` (per-scope
  dimension uniqueness including pending deletion, identity and ownership
  immutability, tenant and project foreign keys, the one-pending-request rule,
  the changes-table audit of limit changes, the outbox's tenant and project
  references, and the active companions); the concurrent link indexes the
  usage counter nets against, the volumes name-pattern index and the
  bare-metal instances cluster-link index (migrations 155 and 156), by their
  own migration tests; the
  quota limit, default limit, threshold, limit request and usage services by
  `internal/servers/quota_servers_test.go` against real PostgreSQL; and the
  registry-driven usage counter by `internal/quotausage/counter_test.go`
  against real resource rows, including the larger-of rule for a mid-resize
  VM, created resources counting in their own kind with the parent adding
  only the excess, one parent row's reserved-but-uncreated room read per
  row, counting until archive rather than delete, children in failed states
  still counting, and a stored reference resolving its type by the scope it
  carries, so a same-name type of another scope can't replace it. The
  tenant and project reconcilers'
  quota-row cleanup, and the outbox rows the tenants and projects servers
  remove with an accepted delete, are covered by the controllers suites and
  `internal/servers`. Tenancy and attribution are suite
  doubles; the reviewed reader grant of `QuotaUsage/Get` is asserted in
  `internal/auth`, and the role grants are exercised through the server
  specs' visibility doubles, which check the servers enforce each role's
  visibility. `it/it_quota_apis_test.go` covers the deployed REST journeys: a
  tenant override, a project limit and the usage report round-trip through the
  gateway, a checked request prices an id-only tier reference, and the
  Approve path under a deployed Cloud Provider Admin identity saves the
  request and writes the tenant's override while the tenant identity is
  denied. No CLI journey covers the quota APIs.
- Quota enforcement in the request path
  ([DEV] [OSAC-7614](https://redhat.atlassian.net/browse/OSAC-7614)):
  `internal/servers/quota_enforcement_test.go` covers the
  generic server's check against real PostgreSQL: a create or update over a
  project, ancestor-project or tenant limit fails with `FAILED_PRECONDITION`,
  ErrorInfo reason `QUOTA_EXCEEDED` and a `QuotaFailure` detail naming the
  scope, dimension, limit, usage, added amount and the field paths that add
  it, and writes nothing; limit resolution as the tightest of the project's
  own limit, its ancestors' and the tenant's (a project limit above the
  tenant's doesn't bypass the tenant's); the dry run of a create runs the check; a
  create that adds nothing to a dimension fits at limit 0; the check is
  skipped for the built-in `shared` and `system` tenants; an update whose
  mask names no annotated spec field is not checked; a resize down frees no
  quota until the resource runs smaller (the larger-of rule through the
  counter); two parallel creates for the last vCPU in separate transactions
  let exactly one pass (the tenant row lock before the count at READ
  COMMITTED); a checked create in one tenant finishes while another tenant's
  row is locked, and two counts of one check see a commit that lands between
  them, the statement-level window of READ COMMITTED; a cluster create and a VM
  create that lock the same subnet row in concurrent transactions both finish; crossed thresholds, a threshold set
  or lowered below the usage, and a created or lowered limit tighter than
  the usage it applies to (a project limit, a tenant override whose
  descendant projects hold the thresholds, an approved limit request, and a
  lowered default after its change commits) write their quota warnings to
  the outbox, while a raised limit, a deleted threshold and a tenant whose
  override keeps its limit warn nothing; and the four quota metrics are
  exposed through a Prometheus registry.
  `internal/servers/quota_enforcement_decisions_test.go` (plain Go table
  cases, no database) covers the arithmetic of the refusal decision: the
  project chain, the subtree sums, the limit resolution order, the scope
  walk and first crossing of the comparison, the update-mask coverage and
  the parent-id parser.
  `internal/servers/quota_enforcement_composite_test.go` (a DescribeTable)
  covers the netting of the system's creates against the
  parent's ask: the operator's create of a host linked to an admitted
  cluster adds nothing at a full tenant and passes, first host and last
  host alike, and so does the create of a VM's boot-disk volume for an
  admitted VM; a user's create, a host beyond the cluster's ask, a host of
  a missing parent, a host after the cluster's node set was removed, and a
  host of a full node set with room in another node set all add their full
  price.
  `internal/servers/private_clusters_server_test.go` covers the automatic
  endpoints of the check (its quota specs): a cluster whose two
  automatic external IPs exceed the external-IP limit is refused before any
  write (no cluster, no address, pool capacity unchanged), and a cluster
  whose two automatic addresses exactly fit the limit passes while a second
  is refused reporting the two addresses the tenant really holds. The
  suite's `BeforeEach` seeds generous default limits (including
  trigger-seeded defaults for host types, storage tiers and GPU resources as
  they appear), because the check refuses a request that adds to a dimension
  with no limit, the way an unconfigured provider does. Tenancy and
  attribution are suite doubles. Deployed coverage:
  `tests/e2e/vmaas/regression/test_quota_enforcement.py` drives the deployed private
  API: an admin caps a project, a tenant user's over-limit order is refused
  with the scope and dimension in the message, and the volume count is
  unchanged. `tests/e2e/caas/regression/test_cluster_host_quota.py` drives
  the deployed cluster-hosts-counted-once journey on real bare metal: the
  tenant's host-type limit leaves room for exactly one cluster's node-set
  ask, the usage report shows the full ask before any host exists and
  exactly the ask after the cluster is Ready with all its hosts, and a
  second cluster of the same size is refused upfront. The E2E suite sets
  generous default limits at session start (tests/e2e/conftest.py) and
  where it creates types, tiers and GPU resources, because a dimension
  with no default has limit 0. Not exercised: the quota publisher (the
  notifications API, [OSAC-75](https://redhat.atlassian.net/browse/OSAC-75),
  does not exist; warnings accumulate in the outbox by design), and the
  host-retry journey, whose retrying count is not implemented
  ([OSAC-7791](https://redhat.atlassian.net/browse/OSAC-7791)).

- **StorageTier/Volume reference guard ([OSAC-7049](https://redhat.atlassian.net/browse/OSAC-7049), DEV):** `internal/cmd/service/start/grpcserver`, `internal/servers` and `internal/database/migrations` exercise production DAOs and in-process handlers against temporary PostgreSQL, including concurrent access. Tenancy and attribution are controlled; deployed service, operator and provider behavior is not covered.

## Coverage gaps

- Provider-enabled networking and FabricDomain-to-Netris lifecycle need their
  deployed environments. The latter is owned by
  [OSAC-4786](https://redhat.atlassian.net/browse/OSAC-4786).
- Storage feedback fixtures do not prove AAP, CSI, provider cleanup, LVMS
  placement or pending LogicalVolume cleanup. The deployed storage placement
  and pending-volume cleanup coverage remains unresolved; the existing
  [OSAC-6614](https://redhat.atlassian.net/browse/OSAC-6614) /
  [OSAC-6615](https://redhat.atlassian.net/browse/OSAC-6615) work does not
  explicitly require an LVMS tier.
- ComputeInstance Volume tests currently use a placeholder `osac.io/node`;
  the accepted [OSAC-6037](https://redhat.atlassian.net/browse/OSAC-6037) design
  still specifies unset topology. Passing current tests does not resolve that
  contract difference. Cross-component completion/cleanup belongs to
  [OSAC-6040](https://redhat.atlassian.net/browse/OSAC-6040); its QE suite path
  and command remain unspecified.
- Existing-token revocation is separate from fresh-login RoleBinding checks:
  [OSAC-6375](https://redhat.atlassian.net/browse/OSAC-6375).

## Fulfillment integration reports

The deployed fulfillment suite can emit native Ginkgo JSON reports, gated on
the `FULFILLMENT_IT_REPORT_DIR` variable in the installer Makefile (empty by
default). One report is written per phase; the fulfillment selection writes
four phase reports and the compute-storage selection runs only its own phase.
Only the selected suite's report files are cleared before the run, so a
reused local directory cannot retain stale reports for unreached phases; a
missing report does not imply a passing phase. The reports are CI
diagnostics for maintainers investigating suite runtime or fixture costs: they
include captured output and failures, not only timings. Fixtures called inside
an `It` remain part of its body, build and deployment time sit outside suite
runtime, and runtime savings require comparable deployed runs, not unit tests.

The same make targets also emit Ginkgo JUnit reports, gated on the
`JUNIT_REPORT_DIR` variable (empty by default, in both the installer and the
CaaS integration Makefiles): one `<phase>.xml` file per phase, with the same
stale-report clearing as the JSON reports. CI sets the variable and merges the
phase files into the single `junit.xml` at the root of the run's
`osac-logs-it-<suite>-<run_id>` log bundle, which the ci-observe monitoring
reads; the phases of one suite share Ginkgo's suite description, so each
merged `<testsuite>` name carries the phase file's stem.
