# Shipped CSI realm authorization contract

Run from `fulfillment-service/` with the repository's Go/Ginkgo versions,
Python 3.11 or later, Bash, and a working Podman or Docker runtime:

```bash
ginkgo run --timeout 30m -v it/realmauth
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
localhost. In these fresh-import fixtures, no roles, scopes, users or realm
settings are repaired after import.
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

Claim and RPC permission contracts are limited to CSI. The persisted-state
fixtures also check other clients' credential validity; those sentinels and the
separate lookup values do not establish their claim or permission contracts. In particular, the prerequisites resolver does not substitute the UI
backend secret. Seeding that fixture key does not repair the imported client;
prerequisites/UI positive coverage and the remaining client matrix are pending.

The backend handlers return empty lists and count calls. They make authorization
observable without a database. Keycloak uses a disposable file database, and
both TLS endpoints use a generated localhost certificate with verification
enabled. This is an identity/authorization contract, not a deployed Fulfillment
or storage lifecycle test: Fulfillment database persistence, tenant filtering, Kubernetes, CSI
provisioning, vendor operations and mounted PVCs are outside its boundary.
Containers and temporary files are removed by test cleanup.

The `Contract test (CSI realm authorization)` integration leg in
[`ci-tests.yml`](../../../.github/ci-tests.yml) reuses the Ginkgo runner in
[`tests.yml`](../../../.github/workflows/tests.yml). Its dedicated path filter
selects changes to the realms, installer, CSI, Fulfillment, generated API and
test itself, plus UI proxy and credential-consumer paths, on pull requests and
merge groups. The required `CI` job includes its result; the scheduled integration
workflow also runs it. There is no label or environment opt-in.

## Existing-realm updater contract

Each artifact also gets one dedicated persisted-state fixture. It removes only
the CSI direct role-scope mapping introduced after base commit
`8d4d0645318c46105db0cc9bd602e9a4e7d06952`, retaining the service account's
assigned role and `fullScopeAllowed=false`. It seeds unrelated users, client
credentials, a realm role, a client scope and direct role mappings before the
initial import. Those sentinels are absent from the corrected shipped export.
A separate disposable Keycloak bootstrap administrator supplies authorization;
OSAC API admin credentials are not assumed to administer Keycloak.

The test replaces the import with the actual resolved corrected artifact,
restarts the same container/database, and verifies a different start timestamp
and unchanged sentinels. A fresh CSI token still lacks the role, and the generated
private `Volumes/List` RPC is denied through production JWT/JWKS/OPA. Startup
import alone must not repair the old state.

Only the production
[`update-csi-realm-scope.py`](../../../osac-installer/scripts/update-csi-realm-scope.py)
entry point then changes the mapping. The lifecycle checks cover read-only
dry-run, a real nonadministrator rejection, apply, repeat apply, preexisting
binding/no-op rollback, owned rollback, repeat rollback and reapply. Each token
observation obtains a new bearer; it never reuses a token source across phases.
Apply/reapply enable the role and private `Volumes/List`; rollback removes the
role from fresh tokens and restores denial. `Clusters/List` remains denied.

State/credential sentinels are checked throughout. Keycloak admin events count
exactly one CREATE for initial apply, no additional mutation on repeated or
preexisting operations, one DELETE for owned rollback, and a second CREATE for
reapply. Protocol tests invoke the same production CLI against TLS doubles to
exercise malformed/ambiguous state, API errors, strict targets, protected
credential/record files, uncertain writes, stale records and transport failures.
These doubles supplement the real Keycloak and RPC proof.

There are four initial Keycloak startups and two restarts across the suite,
not one container per client or safety case. Fresh-import specs have five-minute
budgets; each persisted lifecycle has eight minutes, including startup/restart.
The existing required contract leg allows a 30-minute suite and a 40-minute job,
covering the aggregate spec budgets plus compilation, image setup and safety
cases. Local timing does not establish hosted or production performance.

For the explicit administrator procedure, authorization requirements, operation
record handling and concurrency assumptions, see the
[existing-realm runbook](../../../docs/guides/admin/keycloak-csi-role-scope.md).
Rollback does not revoke previously issued JWTs or clear cached tokens. These
contracts do not establish credential placement, rotation, tenant data
isolation, global audience/authorized-party enforcement, other upgrades or the
full machine-client token/permission matrix.
