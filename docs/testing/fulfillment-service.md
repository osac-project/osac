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
| Component integration (deployed) | The deployed `it/` suite, run through the installer integration target with the fulfillment suite selected | Deployed Fulfillment Service, PostgreSQL, authenticated APIs, Kubernetes, and the CLI from this checkout | Provider networking is disabled. Only the `networking-operator` phase deploys the current operator; other cases do not establish operator/provider behavior. |
| Component integration (identity) ([DEV] [OSAC-6553](https://redhat.atlassian.net/browse/OSAC-6553), [OSAC-6554](https://redhat.atlassian.net/browse/OSAC-6554), [OSAC-6555](https://redhat.atlassian.net/browse/OSAC-6555)) | Service-account, RoleBinding, and service-account CLI suites in `it/`, via the deployed target above | Fulfillment, PostgreSQL, Keycloak, the controller's IDP management principal, and the configured runtime identities: CRUD, tenant isolation, PENDING-to-READY credential Secret provisioning with client ID and secret retrieval, rotation, client lifecycle, managed-token role authorization, and the CLI lifecycle ([OSAC-6556](https://redhat.atlassian.net/browse/OSAC-6556)) | Failure-injection cases run the service and reconciler in process against PostgreSQL and Keycloak, injecting Keycloak transport and persistence failures; they do not exercise the deployed controller loop, and external provider provisioning is out of scope. |
| E2E | `../tests/e2e/` | Deployed cross-component user journeys | Requires the corresponding operators, AAP, and providers. |

For commands, see the
[component guide](../../fulfillment-service/AGENTS.md#integration-tests) and
run `make -C osac-installer help` for the installer targets that drive the
deployed suite.

## ComputeInstance disk handoff (OSAC-6685)

A component-integration phase labelled `compute-storage` runs through the
installer integration target with the compute-storage suite selected; the
fulfillment suite also includes this phase. It exercises the public
ComputeInstance API, real Volume records in PostgreSQL, Fulfillment
reconciliation, the installed storage and ComputeInstance operator
controllers, and StorageClass lookup against the Kind API server. The target
and hub are the same disposable cluster; remote-target routing is not covered.

The phase checks distinct boot/additional-disk Volume IDs and their tenant,
project, tier and size; persistence in parent status and the managed CR; the
same IDs and class mappings in the operator's AAP HTTP request; stable IDs on
re-observation; no VM launch while a requested class is missing or ambiguous;
recovery after correcting the classes; and Volume record cleanup after a
simulated successful VM teardown. CSP-managed classes must remain unchanged.

The fixture supplies tenant compute readiness and disables network
provisioning. Only the AAP HTTP job endpoint is a recording double whose
completion the test controls; it rejects unexpected operations, executes no
playbooks, and its protocol contracts run as a Python unittest suite in the
`it/` testdata directory. The operator's Volume controller is disabled to
prevent provider provisioning. The phase makes no claim about physical disks,
DataVolumes, CSI, Trident, FC, NetApp, actual VM execution, or storage-array
cleanup. Registration fault recovery and OSAC-managed record behavior remain
in the existing deployed volume-records suite in `it/`.

Fulfillment registers the records before the operator checks classes: a
missing or ambiguous mapping retains those tracked records and blocks the VM
job; it does not imply that no Volume record was created. Parent correlation
uses `ComputeInstance.status.volumes`, the managed CR's `spec.diskVolumeIDs`,
and stable disk record names, rather than a reverse attachment field on
Volume. The operator-side boundary is summarized in the
[osac-operator guide](osac-operator.md); deployed disk identity E2E is
[OSAC-6614](https://redhat.atlassian.net/browse/OSAC-6614).

## Selecting coverage

- CLI/API-only contracts belong in `it/`; complete provisioning journeys across
  operators and providers belong in E2E. Use the suite setup and skip
  conditions to identify the actual boundary.
- Tests gated on provider networking skip in the standard fulfillment target;
  their presence is not evidence for the enabled-provider path.
- Fixture-written readiness conditions prove status projection, not automatic
  watch delivery or the upstream controller's calculation of readiness.
- The networking fixture uses distinct Hub entries/namespaces on one cluster;
  it does not establish isolation between separate Kubernetes clusters.
- The MCP protocol suites in `it/` cover eight-tool discovery, connection
  summaries, tenant visibility, prerequisite selection, creator attribution,
  asynchronous create/update acceptance, conflict and argument rejection, and
  the networking resource journeys: VirtualNetwork, Subnet, and SecurityGroup
  create/delete with tenant attribution, dependent owner-reference
  annotations, IPv4 and in-place-edit rejection, absent parent prerequisites,
  same-tenant RoleBinding and cross-tenant authorization, bounded rule
  rejection without persistence, ComputeInstance dependency conflict, and
  dependency-safe cleanup. The network provider is omitted and fixture
  helpers complete no-operator deletion; none of this implies a provisioned
  VM or provider execution. Read the fixtures before extending those cases.
- FabricDomain setup and focused execution are documented in
  [the suite README](../../fulfillment-service/it/README.fabric-domain.md).
- Service-account secret rotation is covered by the identity suites, and
  deployed cross-component journeys (rotation included) by the
  [OSAC-6557](https://redhat.atlassian.net/browse/OSAC-6557) regression suite
  in `tests/e2e/` (see the [tests/e2e guide](e2e.md)). The UI walkthrough is
  not covered here; it remains with the separately owned UI implementation.
- Omitted network-attachment defaulting
  ([OSAC-6480](https://redhat.atlassian.net/browse/OSAC-6480), DEV) is
  covered by deployed public-API cases that create ComputeInstance,
  BareMetalInstance, and Cluster with the attachment field omitted and assert
  Create and public Get return one resolved attachment carrying the tenant
  default fixture's exact Subnet and SecurityGroup IDs; the bare-metal case
  also asserts the `data-0` interface from the first fabric port.
  Partial-attachment defaulting (supplied values surviving field-level
  completion) is unit coverage in the servers suites; Cluster default
  resolution has no dedicated partial-input unit spec and is covered by the
  omission case. Provider provisioning remains E2E coverage.
- Default networking rejection
  ([OSAC-6601](https://redhat.atlassian.net/browse/OSAC-6601),
  [OSAC-7044](https://redhat.atlassian.net/browse/OSAC-7044), DEV): omitted
  Cluster attachments are checked through the private servers suite and the
  deployed public Create. A missing default Subnet or compatible
  SecurityGroup returns `InvalidArgument`; a selected PENDING, FAILED, or
  deleting default returns `FailedPrecondition` naming the dependency and
  lifecycle reason. The same selected lifecycle states are checked for
  ComputeInstance and BareMetalInstance Create, and rejected creates use a
  known ID and Get to prove no workload row persisted. The servers tier uses
  real PostgreSQL with suite tenancy and attribution as doubles; the deployed
  cases run through the fulfillment installer target. Provider provisioning
  remains E2E coverage.

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
default). The fulfillment selection writes four phase reports (`service-only`,
`networking-operator`, `bmaas-disabled`, and `compute-storage`); the
compute-storage selection runs only its own phase. The networking-operator and
compute-storage phases deploy the OSAC Operator. Only the selected suite's
report files are cleared before the run, so a reused local directory cannot
retain stale reports for unreached phases; a missing report does not imply a
passing phase. The reports are CI
diagnostics for maintainers investigating suite runtime or fixture costs: they
include captured output and failures, not only timings. Fixtures called inside
an `It` remain part of its body, build and deployment time sit outside suite
runtime, and runtime savings require comparable deployed runs, not unit tests.
