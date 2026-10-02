# Connected CaaS integration tests (opt-in)

Run from **this worktree's `osac-operator/` directory**. The suite uses a
marked, disposable `osac-sim` Kind cluster with real fulfillment-service,
PostgreSQL, Keycloak, a registered hub, the fulfillment Cluster reconciler, and
Kubernetes API. It invokes the production bare-metal worker `Reconcile` in the
test process, using the real fulfillment gRPC client. The production operator,
AAP, BMF/Metal3, discovery, HyperShift and fabric controllers do **not** run.
Private API calls use a short-lived emergency service-account token and do not
exercise public tenant authorization. The chart installs a CUDN-only default
NetworkClass, but Kind does not provision real OpenShift CUDN; the suite
explicitly simulates the tenant's default networking readiness.

## Setup and run

Requires Go, Kind, Helm, kubectl, Podman or Docker, and access to chart/image
registries. **Do not point these commands at shared or unmarked clusters.**

```bash
make sim-up                 # Creates/reuses only the marked osac-sim cluster;
                            # builds/loads fulfillment-service from this checkout
make test-integration-caas  # Only ./test/integration/caas/; 11 specs at this checkpoint
```

`sim-up` installs cert-manager, trust-manager, CA, PostgreSQL, Keycloak, OSAC
charts and simulator CRDs, registers the hub, and waits for the fulfillment
controller. It writes `hack/sim.env` (contains a token) and a CA file: **never
print, share, stage, or commit either**. Kind maps NodePort 30001 to host
`localhost:8001`; tests do not start a port-forward. Rerun `make sim-up` after
changing fulfillment-service code/migrations: rerunning tests alone does not
rebuild the deployed checkout-specific image. Bootstrap is costlier than repeat
runs; no fixed runtime is promised. The CaaS `BeforeSuite` checks marked
cluster/context, deployed image against the recorded build tag, controller,
CRDs, TLS, credentials and hub before fixtures. A stale `sim.env` is not proof
of readiness.

For diagnostics, use only the explicit sim kubeconfig and namespace:

```bash
kubectl --kubeconfig "$HOME/.kube/osac-sim-kind.kubeconfig" -n osac get deployments,pods,clusterorders
kubectl --kubeconfig "$HOME/.kube/osac-sim-kind.kubeconfig" -n osac logs deploy/fulfillment-controller --tail=100
```

Logs may contain sensitive values; do not paste tokens/secret contents. Never
run `make sim-down` without intending to delete the marked cluster and its DB.
When explicitly finished with this environment:

```bash
make sim-down  # Destructive: deletes only the owned osac-sim cluster after marker check
```

If the cluster is absent, teardown preserves the generated connection files
for inspection; the files alone do not imply a running backend. Never use
`osac-dev` or another unmarked cluster.

## Adding a connected test

1. Put Ginkgo specs and private API fixtures **only** in
   `test/integration/caas/`. Use run-scoped names, the single marked tenant
   `caas-connected-sim`, the real client connection and Kubernetes client from
   `caas_suite_test.go`; do not import fulfillment's `it` harness or synthesize
   the happy-path ClusterOrder. The fulfillment reconciler must create it.
2. Register `DeferCleanup` immediately after every test-owned creation, in
   reverse dependency order. Before deleting BMIs or clearing a deleting
   order's finalizer, verify tenant, Cluster ID label, owner annotation,
   worker IDs and absence of unexpected BMIs. Production deletion is **not**
   covered by this test-only cleanup; its archived-Cluster ownership bug is
   unresolved. The marked tenant and its API-protected default networking stay
   until explicit `sim-down`.
3. Simulate only the named boundaries: `advanceDefaultNetworking` moves this
   tenant's default VN, subnet and SG to READY via the API. Helm installs the
   deployment-wide default NetworkClass with `fabric_manager=cudn_net` and no
   k8s manager. The suite verifies it through the real API; it never updates it.
   Recreate the owned sim (only after explicit teardown approval) when changing
   this immutable class. Simulated InfraEnv ignition uses `envsim`. Keep tenant
   and owner-reference metadata and identify any further simulator changes.
4. Verify a focused spec with `go test ./test/integration/caas/ -v
   -ginkgo.focus='your spec name' -timeout 10m`; then run
   `make test-integration-caas` twice against the matching backend. Compile
   without fixtures using `go test ./test/integration/caas -run '^$'`.
   Check for leftover test-owned ClusterOrders. Do not use recursive root IT
   commands: `make integration-tests` runs the separate root package and
   fulfillment IT runs only `fulfillment-service/it`.

## Verified boundary and remaining gaps

The connected suite checks real API fixture persistence and invalid references,
fulfillment-created ClusterOrders (one/two NodeSets), real BMI create/read-back,
Postgres duplicate-name `AlreadyExists` (OSAC-3266), authoritative ownership,
three `WaitingForAgent` workers, status aggregates, two per-instance-type
desired/ready gauge series in the **in-process** registry, and early rejection
of a separate mismatched-tenant order. It does **not** test public tenant auth,
real network/fabric provisioning, Agent/MAC binding, NodePool convergence,
deployed metrics HTTP, manager watches, AAP/Metal3 assignment or guest install.
Task 6 binding and Task 7 CI/final validation remain pending; consult
[the current status](../../../../docs/plans/2026-09-25-caas-connected-integration-status.md)
and [plan](../../../../docs/plans/2026-09-25-caas-connected-integration.md).
### R01 lost-acknowledgement coverage (execution-ready, live run blocked)

`R01-C1` runs the real two-type worker fixture with a test-local transport
wrapper. It delegates one Create to the real service, loses its successful
acknowledgement, restarts the reconciler, and omits one initial List. The
subsequent same-name Create must hit real `AlreadyExists`; the real re-list
must recover the original ID. The case counts real persisted BMIs and requires
exactly one owned incarnation per reserved name. No production fault-injection
API is added. Both ordinary and faulted fixtures explicitly drive finalizer,
reservation, single-Create/recovery and fresh-observation checkpoints within
`16 + 8*N` calls for deliberately ready dependencies. Errors and dependency
backoff are not swallowed; this bound is not a production latency guarantee.

The R01 Unit/Envtest cases have executed successfully, including interrupted ID
writes with another reserved slot, delayed visibility across two Lists, and
foreign/ambiguous/deleting recovery rejection. The connected package compiles,
but **R01-C1 has not been executed against a real backend**: the worktree lacks
`hack/sim.env`, and the explicit sim kubeconfig endpoint refuses connections.
The connected command fails in `BeforeSuite`, before fixtures; no cluster has
been recreated or deleted. After restoring an approved marked environment:

```bash
go test ./test/integration/caas/ -v -ginkgo.focus='R01-C1' -timeout 10m
make test-integration-caas   # Run twice against the matching backend.
```

Do not infer real uniqueness or provider cleanup from fake/Envtest results.
The archived-Cluster deletion and deployed provider boundaries above remain
unchanged, tracked under [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843).

The separate envtest acceptance suite remains necessary for controller
lifecycle, fault injection and tenant-safety cases that this real-DB contract
suite does not exercise.
