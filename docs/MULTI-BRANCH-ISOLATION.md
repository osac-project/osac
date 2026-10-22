# Multi-branch test isolation

> Part of the [Integration testing](INTEGRATION-TESTING.md) guide.

When two or more checkouts of this repository run integration tests on the same
machine simultaneously — for example, agents testing different feature branches —
the default parameters collide. Kind cluster names, Kubernetes namespaces,
kubeconfig paths, and in-cluster services all default to the same values, so a
teardown or deployment in one checkout can break a running test in another.

Give each checkout a unique suffix (branch name slug, ticket ID, or short hash)
and pass it through the parameters below. All values shown are the codebase
defaults; override them to isolate each environment.

## Kind cluster name and kubeconfig

The installer and operator Makefiles both default to `KIND_CLUSTER_NAME=osac-dev`
and derive the kubeconfig path from it:

```
~/.kube/<KIND_CLUSTER_NAME>-kind.kubeconfig          # PROFILE=dev
~/.kube/<KIND_CLUSTER_NAME>-kind-root.kubeconfig      # PROFILE=dev-full
```

Override `KIND_CLUSTER_NAME` on every `make` invocation to give each checkout its
own cluster and kubeconfig:

```bash
# Checkout A
export KIND_CLUSTER_NAME=osac-feature-a
make -C osac-installer install   PLATFORM=kind PROFILE=dev NS=osac-a KIND_CLUSTER_NAME=osac-feature-a

# Checkout B
export KIND_CLUSTER_NAME=osac-feature-b
make -C osac-installer install   PLATFORM=kind PROFILE=dev NS=osac-b KIND_CLUSTER_NAME=osac-feature-b
```

> **Host-port collision:** The default `kind-config.yaml` maps NodePort
> 30443 → host port 8443 and NodePort 30080 → host port 8080. These host-port
> bindings are per-machine, not per-cluster — a second Kind cluster created with
> the same config will fail to start because the host ports are already
> occupied. To run multiple Kind clusters simultaneously, create per-checkout
> Kind configs with distinct `hostPort` values, or accept that only one cluster
> at a time can use the default ports. **Concurrent clusters using the default
> `kind-config.yaml` are not supported.**

The `osac-aap` integration tests use a hardcoded `osac-test` cluster name in
`tests/integration/setup_test_env.sh` and `teardown_test_env.sh`. Running the
osac-aap test suite concurrently from two checkouts requires editing or
parameterizing those scripts; the installer and operator suites accept the
override without changes.

## Kubernetes namespace

The `NS` parameter controls the OSAC instance namespace for every installer
target (`install`, `install-osac`, `test`, `uninstall`). The E2E conftest reads
`OSAC_NAMESPACE` (default `osac-devel`). Use a distinct value per checkout:

| Scope | Parameter | Default | Override example |
|---|---|---|---|
| Installer / Kind tests | `NS=` | `osac` | `NS=osac-feature-a` |
| E2E pytest | `OSAC_NAMESPACE` | `osac-devel` | `OSAC_NAMESPACE=osac-feature-a` |
| Operator dev namespace | `OSAC_DEV_NAMESPACE` | `osac-operator-dev` | `OSAC_DEV_NAMESPACE=osac-operator-dev-a` |

The `NS` value propagates into the `osac` instance chart (which installs into
the `$(NS)` namespace) and into `global.osacDeploymentId`, so the OSAC
application release is namespace-scoped and independent per `NS` value.
However, `install-infra` installs shared infrastructure (`cert-manager`,
`envoy-gateway`, `strimzi-cluster-operator`, and `osac-infra`) under fixed
release names in fixed namespaces. Running `make install` with different `NS`
values on the same cluster re-configures the single `osac-infra` Helm release
(setting `osacNamespace` to the latest `NS` value) rather than creating
independent infra deployments. **On a shared cluster, only one checkout should
own the infrastructure lifecycle.**

`OSAC_DEV_NAMESPACE` controls the namespaces the operator watches for
ClusterOrder, ComputeInstance, and HostPoolOrder CRs at runtime. It does **not**
isolate the osac-operator integration test suite, which uses a hardcoded
`operatorNamespace = "osac"` constant
(`osac-operator/test/integration/integration_suite_test.go`). Concurrent
integration test runs require separate Kind clusters — changing
`OSAC_DEV_NAMESPACE` alone is not sufficient.

## In-cluster service ports

Fulfillment-service gRPC/HTTP, PostgreSQL, Kafka, and Keycloak all run as
Kubernetes Services inside the cluster. When each checkout uses its own Kind
cluster (distinct `KIND_CLUSTER_NAME`), the in-cluster ports are already
isolated — each cluster has its own network namespace and there is no host-port
collision.

If two checkouts share a single Kind cluster (same `KIND_CLUSTER_NAME`) but use
different `NS` values, namespace-scoped ClusterIP and headless Services deploy
into separate namespaces and do not collide on ports. However, this namespace
separation does not isolate everything:

- **Fixed NodePorts and hostPorts** — Envoy NodePorts (30080, 30443) and debug
  fulfillment hostPorts (30001–30004) bind to the node regardless of namespace
  and will conflict if two releases run on the same cluster.
- **Keycloak** remains in the fixed `keycloak` namespace and **Kafka** remains
  in `osac-kafka`; changing only the OSAC release namespace (`NS`) does not
  give each checkout its own Keycloak or Kafka instance.
- Shared infrastructure installed once by `install-infra` (cert-manager,
  Strimzi, osac-infra) is not duplicated, so only one checkout should own
  infrastructure lifecycle on a shared cluster.

The recommended approach is **one Kind cluster per checkout** using distinct
`KIND_CLUSTER_NAME` values. This provides full isolation without coordinating
namespace ownership or shared infrastructure versions.

## PostgreSQL and database isolation

PostgreSQL is deployed in two ways depending on the test tier:

- **Installer-managed (Kind tests):** The bundled PostgreSQL StatefulSet runs
  inside the cluster in the `osac-infra` namespace, listening on port 5432. Each
  Kind cluster gets its own PostgreSQL instance — no host-port collision.
- **Testcontainer-managed (unit/component tests):** `fulfillment-service` and
  `osac-metering` use container-based PostgreSQL instances started by
  `internal/database/database_container.go` and
  `metering-service/internal/database/container.go` respectively. Each test run
  starts its own container with a random host port, so concurrent runs from
  different checkouts do not collide. Database names within a container are
  created per test from a template using `CREATE DATABASE ... TEMPLATE`.

No additional configuration is needed for database isolation when each checkout
uses its own Kind cluster.

## Kafka isolation

Kafka is deployed by the Strimzi operator during `make -C osac-installer
install-infra`. The Kafka broker and Zookeeper run inside the Kind cluster in
the `osac-kafka` namespace. Separate Kind clusters provide complete isolation.

There is no parameterized Kafka port exposed to the host. If two checkouts must
share a cluster, they share the same Kafka instance; topic isolation would
require application-level topic prefixes, which the current test suites do not
support.

## Cleanup without cross-checkout interference

Tear down one checkout's environment without affecting another by targeting the
correct cluster name:

```bash
# Tear down only checkout A
make -C osac-installer uninstall PLATFORM=kind PROFILE=dev NS=osac-a KIND_CLUSTER_NAME=osac-feature-a

# Tear down only checkout B
make -C osac-installer uninstall PLATFORM=kind PROFILE=dev NS=osac-b KIND_CLUSTER_NAME=osac-feature-b
```

The `uninstall-infra` target for `PLATFORM=kind` calls `kind delete cluster
--name <KIND_CLUSTER_NAME>`, so it removes only the named cluster. Verify the
target before running teardown:

```bash
kind get clusters          # lists all Kind clusters on this machine
```

For the osac-aap test suite, `tests/integration/teardown_test_env.sh` deletes
the `osac-test` cluster specifically. It does not affect clusters created by the
installer with a different `KIND_CLUSTER_NAME`.

## Quick-reference: isolation checklist

| Resource | Isolation mechanism | What to set |
|---|---|---|
| Kind cluster | Separate cluster per checkout | `KIND_CLUSTER_NAME=osac-<branch>` — also requires per-checkout Kind configs with distinct `hostPort` values for concurrent clusters |
| Kubeconfig | Derived from cluster name | Automatic when `KIND_CLUSTER_NAME` differs |
| OSAC namespace | Separate namespace per checkout | `NS=osac-<branch>` |
| E2E namespace | Environment variable | `OSAC_NAMESPACE=osac-<branch>` |
| Operator runtime namespaces | ClusterOrder, ComputeInstance, and HostPoolOrder watch namespace (runtime only) | `OSAC_DEV_NAMESPACE=osac-operator-dev-<branch>` — isolates operator runtime but **not** the integration test suite (hardcoded `osac` namespace); concurrent integration tests need separate Kind clusters |
| PostgreSQL (Kind) | Runs inside each cluster | Isolated by `KIND_CLUSTER_NAME` |
| PostgreSQL (testcontainer) | Random host port per run | Automatic; no override needed |
| Kafka | Runs inside each cluster | Isolated by `KIND_CLUSTER_NAME` |
| Fulfillment service | Cluster-internal addresses | Isolated by `KIND_CLUSTER_NAME` |
| osac-aap test cluster | Hardcoded `osac-test` | Only one osac-aap test run at a time, or parameterize the scripts |
