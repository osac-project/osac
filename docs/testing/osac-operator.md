# osac-operator

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Follow this guide for suite setup and limits; inspect test source for
individual cases. Commands and inline paths are relative to `osac-operator/`
unless explicitly marked as repository-root commands.

Touched-area requirements: [component guide](../../osac-operator/AGENTS.md#integration-testing).

## Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | Co-located `*_test.go`; `make test` | Controller helpers, validation, provisioning state logic | External APIs and providers are mocked. |
| Envtest | Existing Controller Suite in `internal/controller/`; run by `make test` | Kubernetes API server, etcd, loaded CRDs and public in-process reconciliation | Provider/fulfillment responses and external CR progression are test-controlled; this is not a deployed worker. |
| Component integration | `test/integration/`; deploy the current operator into Kind, then `make integration-tests` | Installed operator, Kubernetes API, CRDs, controller-manager, console proxy, networking, worker service-account authorization and startup without LVMS/TopoLVM | AAP/provider provisioning and external infrastructure are not real; some existing tests remove finalizers to bypass external-provider boundaries. |
| Component integration (CI) | `make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=operator` (repository root) | Thin Kind deployment with the Fulfillment client disabled | Same external-provider limitations as the local Kind suite; installation-setting verification needs a separate deployed test. |
| Contract | `test/contract/`; included by `make test` | Helm chart RBAC templates against the operator permission contract | No deployed operator or external provider. |
| E2E | `../tests/e2e/` | Cross-component fulfillment journeys | Depends on the deployed environment and configured providers. |

## Coverage boundaries

The Kind operator leg normally disables provider networking and Fulfillment.
It proves installed CRD/admission/RBAC and controller wiring, not a deployed
Fulfillment endpoint, AAP job or provider. The manager-registration phase enables
networking to inspect registrations; it still does not run real provider tasks.
Inspect `test/integration/` setup and labels before choosing a focused run.

The network task engine and networking Controller Suite use an in-memory AAP.
Shared input/result goldens in `../tests/contract/network-task/` are read by
both operator and AAP tests. They bind the covered create/apply/attach shapes
at Unit/Envtest tier, not the deployed AAP boundary. Delete/detach,
SecurityGroup, DHCP-lease, FabricDomain, cluster-ingress and physical-interface
shapes remain unbound between the two sides, with no owning ticket.

Local TLS/OIDC endpoints prove client handling, not deployed cross-cluster
trust or credentials. Track cross-cluster OAuth execution under
[OSAC-5939](https://redhat.atlassian.net/browse/OSAC-5939).
LVMS Envtest uses a minimal TopoLVM CRD and fixture status; real device
provisioning and CSI mounting remain E2E under
[OSAC-3711](https://redhat.atlassian.net/browse/OSAC-3711).
Remote StorageClass tests have both dual-Envtest and a second Kind cluster
(`osac-remote`); inspect `test/integration/remote_storage_cluster_test.go`
for the required cluster setup.

## Bare-metal worker coverage ([DEV])

Worker Unit specs use the Ginkgo BareMetalWorker Suite and descriptive behavior
names; RBAC Contract specs use the Operator Contract Suite. All worker persistence
specs share `internal/controller/suite_test.go` with the other operator tests. They are `internal/controller/baremetalworker_*_test.go`,
labelled `baremetalworker`; there is no separate acceptance suite or reusable
external-environment framework. The fulfillment/ignition doubles are private to
that test binary. Explicit CR fixtures write only the evidence the worker reads.
Retain the minimal InfraEnv/ClusterDeployment and existing Agent/NodePool test
CRDs required by these cases. None of these fixtures runs Assisted Service,
CAP-Agent, HyperShift, BMF, AAP or hardware provisioning.

Focused commands from `osac-operator/` (included by `make test`):

```bash
go test ./internal/controller/baremetalworker -count=1
go test -race ./internal/controller/baremetalworker -count=1
KUBEBUILDER_ASSETS="$PWD/bin/k8s/1.31.0-linux-amd64" \
  go test ./internal/controller -count=1 -ginkgo.label-filter=baremetalworker
```

`make test` obtains platform-appropriate Envtest assets; the focused command
above uses already installed Kubernetes 1.31.0 assets. Read the suite and
`baremetalworker_*_test.go` files for cases and fixtures rather than maintaining
a parallel assertion inventory here.

## R03 unified worker cleanup implementation checkpoint

Worker cleanup exercises real Kubernetes persistence and UID preconditions,
with test-controlled Fulfillment and external Agent progression. It does not
prove real provider deletion or owner-driven CAP-Agent detachment. Reservation
cancellation requires the generated CRD and draining older worker controllers;
mixed-version controllers that ignore create state are unsafe. A persisted
Create intent without a provider outcome remains ambiguous and can block deletion.

## Networking cleanup lifecycle coverage (NET-CLEAN-07)

The deployed Kind cases exercise OSAC-owned networking cleanup and restart
recovery through the real Kubernetes API. Test finalizers and persisted launch
claims hold cleanup at controlled checkpoints; no AAP or real provider runs.
Inspect `test/integration/` for assertions and fixture behavior.

Run the following commands from the repository root, using a dedicated Kind
cluster when testing in parallel. Use a local-only
Kind config with unique HTTP and HTTPS host ports, and set the HTTPS port to the
same value as `FULFILLMENT_IT_HTTPS_PORT`. Do not check environment-specific Kind
configs into the repository. For example:

```bash
export KIND_CLUSTER_NAME=osac-5749-operator
export KIND_CONFIG="$HOME/.config/kind/osac-5749-operator.yaml" # local config maps HTTP 38080 and HTTPS 38443
export FULFILLMENT_IT_HTTPS_PORT=38443

KUBECONFIG= make -C osac-installer install-infra PLATFORM=kind PROFILE=dev NS=osac
KUBECONFIG= make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=operator
KUBECONFIG= make -C osac-installer uninstall PLATFORM=kind PROFILE=dev NS=osac
```

Run NET-CLEAN-08 in a separate cluster with its own local Kind config,
kubeconfig, and host ports when validating both suites in parallel. Deployed QE
coverage is tracked under
[OSAC-5750](https://redhat.atlassian.net/browse/OSAC-5750); real provider
cleanup coverage is tracked under
[OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).

## Coverage gaps and ownership

Real private-API fixture validation, fulfillment-generated tenant-owned
ClusterOrders, Postgres scoped-name uniqueness and lost-ack recovery, actual
fulfillment deletion/name reuse, backend outage/authorization, deployed metrics
HTTP and manager Agent-watch delivery require qualifying **[DEV] Contract or
component integration** coverage under
[OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843). Neither the controller
fixture's uniqueness model nor installed RBAC asserts those boundaries. The
current Kind suite has no worker contract fixture exercising them; do not count
Unit/Envtest, compile checks or chart renders as substitutes. Optional same-UID
Agent binding-change Delete resourceVersion coverage (R03-E6) remains proposed,
not a completion gate. No owner or Jira follow-up has been identified, so this
coverage gap remains unresolved.

Real Assisted Service artifacts/selectors/binding, AAP/HyperShift/CAP-Agent,
provider/drain and hardware create/scale/delete journeys remain **[QE] E2E**
under the same follow-up. Production archived-Cluster ownership lookup still
needs an approved fix and a dedicated owner/ticket (unresolved). Bound-worker
cleanup waits for owner-driven detachment; no Machine/CAP-Agent hooks or
NodePool replicas are manipulated to force cleanup. These gaps do not require
a new environment for local status-projection or cleanup-policy changes.
