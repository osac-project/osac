# Shipped CSI realm authorization contract

Run from `fulfillment-service/` with the repository's Go/Ginkgo versions,
Python 3, Bash, and a working Podman or Docker runtime:

```bash
ginkgo run --timeout 10m -v it/realmauth
```

`CONTAINER_TOOL=docker` selects Docker explicitly; otherwise the test prefers
Podman. Missing infrastructure fails setup; it never silently skips.

Each installer path gets one fresh Keycloak using its declared image. Its
ordered cases reuse that import without changing the realm, with separate
tokens, RPC connections and handler counters. Setup reads the actual Helm or
prerequisites realm, executes that path's secret resolver, then imports the
result with `start --import-realm`.

Only the Kubernetes Secret store is replaced. It contains distinct random
disposable values for each supported key; the realm administrator has a separate
credential. The stand-in accepts exact resolver lookup commands and rejects
unknown keys, operations and extra arguments. The prerequisites resolver's
absolute file paths point into the test directory, and the UI URL points at
localhost. No roles, scopes, users or realm settings are repaired after import.
`fullScopeAllowed` must remain false for CSI.

Before substitution, the complete raw realm artifacts must be JSON-equivalent.
The guard ignores whitespace and object key order, rejects duplicate keys, and
retains all values, array order, IDs and placeholders. A deliberate mutation of
the unrelated browser client's `enabled` field in a temporary copy verifies
drift detection; this is a mutation control, not an existing product defect.
Equivalence does not prove that identical changes to both files are correct, or
that their different resolvers perform equivalent substitutions.

The client uses the same OAuth `clientcredentials.Config` as the CSI driver,
including its default scopes, and calls the generated private `Volumes/List`
and `Clusters/List` clients over TLS. The server uses the production JWKS
discovery, JWT validator, authentication interceptor, authorization interceptor
and embedded OPA policy, without emergency accounts or policy overrides.
Both RPCs execute before role/success assertions. Output records only issuance,
verification, role presence, gRPC status and handler counts; no credentials.
An independent issuance attempt uses the actual controller lookup value as the
CSI secret and must receive HTTP 401 / `unauthorized_client` from the same Keycloak.

Issuance and RPC coverage is limited to CSI. Supplying separate controller,
admin, UI backend and MCP lookup values does not establish their token or API
contracts. In particular, the prerequisites resolver does not substitute the UI
backend secret. Seeding that fixture key does not repair the imported client;
prerequisites/UI positive coverage and the remaining client matrix are pending.

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
test itself, plus UI proxy and credential-consumer paths, on pull requests and
merge groups. The required `CI` job includes its result; the scheduled integration
workflow also runs it. There is no label or environment opt-in.

## Future existing-realm rollout acceptance

This suite exercises fresh imports. Keycloak
[skips an existing realm during startup import](https://www.keycloak.org/26.6/server/importExport#_importing_a_realm_during_startup),
so replacing the artifact does not establish an update to persisted state.
No updater or existing-realm regression is implemented here. The following
acceptance work requires a separately approved implementation and environment:

1. Start from the pre-fix realm in disposable persistent storage and record
   unrelated users, clients, credentials and role mappings as preservation
   sentinels, without logging credentials. Establish the backup/rollback owner.
2. Restart with the corrected artifact and confirm startup import retains the
   old mapping. Obtain a fresh CSI token and reproduce private Volumes/List
   denial to demonstrate the persistent-state rollout gap.
3. Review and implement a targeted operation that adds only the missing CSI
   realm-role scope mapping, keeping `fullScopeAllowed=false`. Apply it twice;
   the second application must be a no-op. Verify that the sentinels, existing
   clients and unrelated role/scope mappings remain unchanged.
4. Obtain fresh tokens, verify their claims and require CSI private Volumes/List
   success and Clusters/List denial. Recheck unaffected client contracts.
5. Roll back only the mapping introduced by that operation, if originally absent.
   Verify preservation and the original fresh-token denial, then reapply for
   acceptance. Define cached-token handling and credential reload separately.

These contracts do not establish credential placement, rotation, tenant data
isolation, global audience/authorized-party enforcement, or upgrade safety.
