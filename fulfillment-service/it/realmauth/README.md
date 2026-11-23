# Realm authorization contract tests

Requires the repository's Go/Ginkgo versions, Bash, and working Podman or Docker.

Run from `fulfillment-service/`:

```bash
ginkgo run --timeout 30m -v it/realmauth
```

Set `CONTAINER_TOOL=docker` to select Docker; otherwise Podman is preferred.

Both shipped realm imports (Helm and prerequisites) are exercised with real
machine-client tokens and production JWT/JWKS/OPA API authorization.
Seven client/import combinations succeed; the prerequisites UI credential is
a known HTTP 401 rejection, not an eighth positive case.

These contracts cover fresh imports only. Existing realms are not migrated.

This is an authorization contract, not deployed storage E2E: database tenant
filtering, Kubernetes/CSI provisioning, vendor operations and mounted PVCs are
outside its scope.
