# Realm authorization contract tests

Requires the repository's Go/Ginkgo versions, Python 3.11+, Bash, and working Podman or Docker.

Run from `fulfillment-service/`:

```bash
ginkgo run --timeout 30m -v it/realmauth
```

Set `CONTAINER_TOOL=docker` to select Docker; otherwise Podman is preferred.

Both shipped realm imports (Helm and prerequisites) are exercised with real
machine-client tokens and production JWT/JWKS/OPA API authorization.
Seven client/import combinations succeed; the prerequisites UI credential is
a known HTTP 401 rejection, not an eighth positive case.

Persisted-realm tests cover the CSI updater, idempotence, rollback/reapply and
unrelated state preservation; see the [administrator guide](../../../docs/guides/admin/keycloak-csi-role-scope.md).

This is an authorization contract, not deployed storage E2E: database tenant
filtering, Kubernetes/CSI provisioning, vendor operations and mounted PVCs are
outside its scope.
