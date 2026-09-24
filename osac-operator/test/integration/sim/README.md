# Sim Integration Tests

Integration tests that run against a real fulfillment-service + Postgres stack
deployed in a kind cluster. These validate DB-backed behaviors that the in-memory
fake (`internal/controller/baremetalworker/fake/`) cannot reproduce.

## Quick Start

```bash
make sim-up                # Deploy kind cluster with fulfillment-service stack
make test-integration-sim  # Run sim tests (starts port-forward automatically)
make sim-down              # Tear down
```

Set `CLUSTER_NAME=osac-dev` to reuse an existing kind-dev cluster.

## How It Works

`sim-up` creates a kind cluster and deploys the OSAC umbrella chart
(`osac-installer/charts/osac/`) with sim-specific values (`hack/sim-values.yaml`):

- **cert-manager + trust-manager** — deployed as separate Helm releases before the umbrella chart
- **Self-signed CA** — ClusterIssuer + trust-manager Bundle
- **Bundled Postgres** — deployed inline by the umbrella chart
- **fulfillment-service** — the real gRPC server backed by real Postgres

No Keycloak, Envoy Gateway, Authorino, KubeVirt, AWX, or other heavy components.
Auth uses emergency service account tokens (K8s SA → JWT, bypasses OPA).

Tests connect via NodePort (mapped to `localhost:8001` by kind's `extraPortMappings`).

## What runs here vs acceptance

| Assertion | Sim | Acceptance | Why |
|-----------|-----|------------|-----|
| BMI create + read-back | Yes | Yes (via fake) | Smoke test for both paths |
| AlreadyExists on duplicate create (OSAC-3266) | **Yes** | Yes (simulated) | Real UNIQUE constraint in Postgres |
| InfraEnv creation | No | **Yes** | Requires envtest + CRDs |
| Management-state skip | No | **Yes** | Requires envtest |
| Ignition size warning | No | **Yes** | Requires envtest event recorder |

Sim tests are the anti-drift check for the fake: they confirm that the DB behaviors
the fake simulates are real. If a sim test fails but the corresponding acceptance test
passes, the fake has drifted from the real implementation.
