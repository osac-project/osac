# osac-operator

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence; inspect test source for individual cases. Commands and paths are
relative to `osac-operator/` unless marked as repository-root commands.
Touched-area requirements:
[component guide](../../osac-operator/AGENTS.md#integration-testing).

Discover targets with `make help`: `make test` (Unit, Envtest, Contract) and
`make integration-tests` (pre-existing Kind cluster) are the public entry points.
The CI job runs the installer's Kind target with the operator suite
(repository-root `make -C osac-installer help`, `.github/workflows/`).

## Test tiers

| Tier | Suite | Real | Faked or omitted |
|---|---|---|---|
| Unit | Co-located Go unit tests | Helpers, validation, state logic | External APIs/providers mocked. |
| Envtest | `internal/controller/` | API, etcd, CRDs, in-process reconcile | Provider/fulfillment responses and external progression test-controlled; not a deployed worker. |
| Component integration | Deployed `test/integration/` | Installed operator, API, CRDs, console proxy, networking, worker RBAC, startup without LVMS/TopoLVM | No AAP/provider/infra; some tests remove finalizers to bypass provider boundaries. |
| Component integration (CI) | Installer Kind target, operator suite | Thin Kind install, Fulfillment client disabled | Same provider limits; installation settings need separate deployed coverage. |
| Component integration ([DEV] [OSAC-6685](https://redhat.atlassian.net/browse/OSAC-6685)) | Installer Kind target, compute-storage suite | Real Fulfillment APIs/PostgreSQL, installed storage and ComputeInstance controllers, and target StorageClass lookup | AAP is a recording double; tenant compute readiness is supplied by the fixture. No provider or VM execution; see the [fulfillment-service guide](fulfillment-service.md#computeinstance-disk-handoff-osac-6685) for the boundary. |
| Contract | `test/contract/` | Helm RBAC templates vs permission contract | No deployed operator/provider. |
| E2E | `../tests/e2e/` | Cross-component fulfillment journeys | Environment/provider-dependent. |

## Coverage boundaries

The Kind operator leg disables provider networking and Fulfillment: it proves
CRD/admission/RBAC and controller wiring, not a deployed Fulfillment endpoint,
AAP job or provider. Manager registration enables networking only to inspect
registrations, not to run provider tasks. Inspect the deployed suite before a
focused run.

The networking suite and task engine use an in-memory AAP; shared wire goldens
under `../tests/contract/network-task/` bind only covered create/apply/attach
shapes at Unit/Envtest tier, not the deployed AAP boundary. Delete/detach,
SecurityGroup, DHCP-lease, FabricDomain, cluster-ingress and physical-interface
shapes remain unbound with no owning ticket.

Local TLS/OIDC endpoints prove client handling, not deployed cross-cluster trust
or credentials
([OSAC-5939](https://redhat.atlassian.net/browse/OSAC-5939)); LVMS Envtest uses
a minimal TopoLVM CRD and fixture status, so real device provisioning and CSI
mounting remain E2E
([OSAC-3711](https://redhat.atlassian.net/browse/OSAC-3711)). Remote StorageClass
coverage needs dual Envtest plus a second Kind cluster.

Subnet compute eligibility is covered at Unit and Envtest tier, in the
Fulfillment servers suite (see the
[fulfillment-service guide](fulfillment-service.md)), and by the
`cudn_evpn` component-integration target in osac-aap (see the
[osac-aap guide](osac-aap.md)). These checks do not establish live OVN
readiness or packet continuity; deployed provider coverage remains
[OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).

## Bare-metal worker coverage ([DEV])

Worker specs use the `baremetalworker` label in the `internal/controller/`
Envtest suite; there is no separate acceptance suite or external framework.
Fulfillment/ignition doubles and CR fixtures are private to that binary and
supply only the evidence the worker reads. Keep the minimal
InfraEnv/ClusterDeployment and Agent/NodePool test CRDs. No fixture runs Assisted
Service, CAP-Agent, HyperShift, BMF, AAP or hardware provisioning. Run
`make test`; see the component AGENTS.md.

## R03 unified worker cleanup implementation checkpoint

Worker cleanup exercises real Kubernetes persistence and UID preconditions with
test-controlled Fulfillment and external Agent progression. It does not prove
real provider deletion or owner-driven CAP-Agent detachment. Reservation
cancellation requires the generated CRD and draining older worker controllers;
mixed-version controllers that ignore create state are unsafe. A persisted Create
intent without a provider outcome remains ambiguous and can block deletion.

## Networking cleanup lifecycle coverage (NET-CLEAN-07)

The deployed Kind cases exercise OSAC-owned networking cleanup and restart
recovery through the real Kubernetes API; test finalizers and persisted launch
claims hold cleanup at checkpoints, and no AAP or real provider runs. Inspect the
deployed suite's fixtures.

Run this leg through the installer's Kind target with the operator suite
(repository-root `make -C osac-installer help`), on a dedicated cluster with environment configs
kept out of the repository; see
[Multi-branch test isolation](../MULTI-BRANCH-ISOLATION.md). Run NET-CLEAN-08 in
a separate cluster with its own ports when both suites run in parallel. Deployed
QE coverage is [OSAC-5750](https://redhat.atlassian.net/browse/OSAC-5750); real
provider cleanup is [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).

## Coverage gaps and ownership

Real private-API fixture validation, fulfillment-generated tenant-owned
ClusterOrders, Postgres scoped-name uniqueness and lost-ack recovery, actual
fulfillment deletion/name reuse, backend outage/authorization, deployed metrics
HTTP and manager Agent-watch delivery need **[DEV] Contract or component
integration** coverage under
[OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843). Neither the fixture
uniqueness model nor installed RBAC asserts those; the Kind suite has no worker
contract fixture. Do not count Unit/Envtest, compile checks or chart renders as
substitutes. Optional same-UID Agent binding-change Delete resourceVersion
coverage (R03-E6) remains proposed, not a completion gate. No owner or Jira
follow-up is identified, so this gap remains unresolved.

Real Assisted Service artifacts/selectors/binding, AAP/HyperShift/CAP-Agent,
provider/drain and hardware create/scale/delete journeys remain **[QE] E2E**
under the same follow-up. Production archived-Cluster ownership lookup needs an
approved fix and a dedicated owner/ticket (unresolved). Bound-worker cleanup
waits for owner-driven detachment; no Machine/CAP-Agent hooks or NodePool
replicas are manipulated to force cleanup. No new environment is required for
local status-projection or cleanup-policy changes.
