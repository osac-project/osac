# MaaS WorkloadCluster E2E

The scenario test is infrastructure-agnostic and follows the normal OSAC E2E
contract: infrastructure publishes connection variables and pytest creates and
cleans up only scenario resources.

For a disposable local Kind run, use the lifecycle runner:

```bash
tests/e2e/maas/run-kind.sh
```

For an interactive demo, use `--setup-only`. The runner will create both
clusters and install OSAC, but leave Hub and WorkloadCluster registration to
the demo commands:

```bash
tests/e2e/maas/run-kind.sh --setup-only
```

Remove the demo environment with:

```bash
tests/e2e/maas/run-kind.sh --cleanup-only
```

The interactive demo helper is available at `tests/e2e/maas/demo.sh`:

```bash
tests/e2e/maas/demo.sh create
tests/e2e/maas/demo.sh status
tests/e2e/maas/demo.sh break
tests/e2e/maas/demo.sh recover
tests/e2e/maas/demo.sh cleanup
```

For the full demo sequence:

```bash
tests/e2e/maas/demo.sh hub-list
tests/e2e/maas/demo.sh hub-add
tests/e2e/maas/demo.sh workload-list
tests/e2e/maas/demo.sh workload-add
tests/e2e/maas/demo.sh managedcluster-remove
tests/e2e/maas/demo.sh status
tests/e2e/maas/demo.sh managedcluster-recover
tests/e2e/maas/demo.sh status
tests/e2e/maas/demo.sh hub-remove
tests/e2e/maas/demo.sh status
```

The runner creates a fresh management Kind cluster and a separate source-Hub
Kind cluster, builds and loads the current fulfillment-service image, installs
OSAC with MaaS enabled and the matching clean Postgres configuration, installs
the minimal `ManagedCluster` API fixture on the source Hub, registers the Hub
using a controller-reachable source API address, exports the standard E2E
variables, runs the pytest scenario, and removes both clusters on exit.

The runner refuses to reuse existing clusters or kubeconfig files. Set
`OSAC_KIND_CLUSTER_NAME`, `OSAC_SOURCE_HUB_CLUSTER_NAME`, or
`OSAC_KEYCLOAK_ADMIN_PASSWORD` when needed for a local environment.
