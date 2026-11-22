# Shipped CSI realm authorization contract

Run from `fulfillment-service/` with the repository's Go/Ginkgo versions,
Python 3, Bash, and a working Podman or Docker runtime:

```bash
ginkgo run --timeout 10m -v it/realmauth
```

`CONTAINER_TOOL=docker` selects Docker explicitly; otherwise the test prefers
Podman. Missing infrastructure fails setup; it never silently skips.

Each table entry runs a fresh Keycloak using the image declared by its installer
path. It reads the actual Helm realm or prerequisites realm, executes that
path's secret resolver, then imports the result with `start --import-realm`.
Only the Kubernetes Secret store is replaced with disposable test secrets. The
prerequisites resolver's absolute file paths point into the test directory, and
the UI URL points at localhost. No roles, scopes, users or realm settings are
repaired after import. `fullScopeAllowed` must remain false.

The client uses the same OAuth `clientcredentials.Config` as the CSI driver,
including its default scopes, and calls the generated private `Volumes/List`
and `Clusters/List` clients over TLS. The server uses the production JWKS
discovery, JWT validator, authentication interceptor, authorization interceptor
and embedded OPA policy, without emergency accounts or policy overrides.
Both RPCs execute before role/success assertions. Output records only issuance,
verification, role presence, gRPC status and handler counts; no credentials.

The backend handlers return empty lists and count calls. They make authorization
observable without a database. Keycloak uses a disposable file database, and
both TLS endpoints use a generated localhost certificate with verification
enabled. This is an identity/authorization contract, not a deployed Fulfillment
or storage lifecycle test: persistence, tenant filtering, Kubernetes, CSI
provisioning, vendor operations and mounted PVCs are outside its boundary.
Containers and temporary files are removed by test cleanup.

The `Contract test (CSI realm authorization)` integration leg in
[`ci-tests.yml`](../../../.github/ci-tests.yml) reuses the Ginkgo runner in
[`tests.yml`](../../../.github/workflows/tests.yml). Its dedicated path filter
selects changes to the realms, installer, CSI, Fulfillment, generated API and
test itself on pull requests and merge groups. The required `CI` job includes
its result; the scheduled integration workflow also runs it. There is no label
or environment opt-in.
