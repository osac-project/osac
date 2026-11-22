# Shipped machine-client realm authorization contracts

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

## Fresh-import machine-client contracts

There are **seven positive client/import combinations**. Every positive obtains
a real `client_credentials` token using its distinct Secret lookup value,
verifies it through production JWKS/JWT code, and exercises generated RPCs with
the production authentication/authorization interceptors and embedded OPA
policy. There are no emergency accounts or policy overrides.

| Client | Positive imports | Requested OAuth scopes | Audiences | Representative method permissions |
| --- | --- | --- | --- | --- |
| `osac-csi-driver` | Helm and prerequisites | Default (none added) | `osac-api` | Private Volumes/List allowed; private and public Clusters/List denied |
| `osac-controller` | Helm and prerequisites | `openid` | `osac-api`, `account`, `realm-management` | Private Volumes/List and Clusters/List, public Clusters/List and Users/Create allowed |
| `osac-admin` | Helm and prerequisites | `openid` | `osac-api`, `account` | Same four method allows as controller |
| `osac-ui-backend` | Helm | Default (none added) | `osac-api` | Real v1 reflection and public Clusters/List allowed; private Volumes/List and Clusters/List and public Users/Create denied |

The CSI request follows the [driver](../../../osac-csi-driver/cmd/osac-csi-driver/main.go).
Controller and CLI use the [credentials flow](../../internal/oauth/oauth_credentials_flow.go)
and [default `openid` scope](../../internal/oauth/oauth_token_source.go).
UI follows the [proxy token request](../../../osac-ui/proxy/auth/oidc.go) and
[reflection consumer](../../../osac-ui/proxy/bridge/connectjson.go): credentials
in the token form, no added scope, and bearer metadata on the v1 reflection
stream. The reflection test uses the real registered server, lists services and
fetches a generated service descriptor through both production **stream**
interceptors. An anonymous request is rejected before that backend.

Expected claims are reviewed constants in [client_contract_test.go](client_contract_test.go),
independent of imported JSON and observed tokens. Every positive requires the
fixture issuer, the exact client `azp`, exported service-account `sub` and
`service-account-<client>` principal, exact audience/scope sets and exact
realm/client role sets. Production identity extraction must agree. The
[shipped realm](../../../osac-installer/charts/osac-infra/files/realm.json)
provides users, role assignments, scopes and mappers:

- CSI emits only the `osac-csi-driver` realm role, with no client roles.
  Its direct realm-role scope mapping intersects the assigned service-account
  role while `fullScopeAllowed=false`.
- UI emits no realm or client roles: it has no matching role scope and retains
  `fullScopeAllowed=false`.
- Controller and admin emit `default-roles-my realm`, `offline_access` and
  `uma_authorization`. Their account roles are `view-profile`,
  `manage-account` and its composite child `manage-account-links`.
- Controller additionally emits realm-management roles `manage-realm`,
  `manage-users`, `view-realm`, `view-users`, `manage-identity-providers`,
  `view-identity-providers`, `create-client`, `view-clients` and their composite
  children `query-groups`, `query-users` and `query-clients`. This excludes
  `realm-admin` and every other undeclared role.
- The roles audience resolver adds `account`/`realm-management` where those
  client roles are emitted; the explicit API mapper adds `osac-api`. Only
  `profile`/`email` default scopes opt into the scope claim; controller/admin
  also request `openid`.

Exact sets detect privilege leakage, including unexpected clients or roles.
Group, tenant and Kubernetes identity claims are absent from these default
machine-client contracts. Pure synthetic mutation controls verify the assertion
helper's rejection behavior; they never enter runtime token/RPC tests. Those
controls are test-harness evidence, not additional issued-client contracts.

The [policy](../../internal/auth/policies/authz.rego) explicitly grants the
controller/admin account names all methods. Their negative coverage uses real
wrong-client-secret rejection and claim-drift controls; it does not invent a
forbidden business RPC for an administrator. UI's public Clusters/List allow is
only the generic client method rule, not proof of access to tenant data.
The original CSI private calls still execute before role/success assertions.
Each client/import pair also rejects another client's actual Secret lookup with
HTTP 401 / `unauthorized_client`. Output contains checked source constants,
fixed-field mismatch diagnostics, statuses and counters, never credentials,
bearers or token-endpoint response bodies.

**Known missing positive: prerequisites/UI ([OSAC-8052](https://redhat.atlassian.net/browse/OSAC-8052)).**
The prerequisites resolver leaves the UI secret placeholder unresolved. A named,
executed diagnostic uses the intended distinct UI Secret and confirms real
HTTP 401 / `unauthorized_client` with no issued token. It is negative evidence
of the gap, not an eighth passing positive, a skipped test or a repaired fixture.
The production resolver remains unchanged.

The backend handlers return empty responses and count calls. They make authorization
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
not one container per client or safety case. Every fresh-import spec keeps a
five-minute budget because selecting any one case also runs `BeforeAll`.
New matrix probes additionally bound steady-state work to 45 seconds per
positive case, 20 seconds per rejection and 10 seconds per reflection stream;
HTTP requests have ten-second deadlines. Each persisted lifecycle retains eight
minutes including startup/restart. The required leg retains its 30-minute suite
and 40-minute job budgets; the matrix adds no cold starts. Local normal/race
timings are validation evidence, not hosted or production performance guarantees.

For the explicit administrator procedure, authorization requirements, operation
record handling and concurrency assumptions, see the
[existing-realm runbook](../../../docs/guides/admin/keycloak-csi-role-scope.md).
Rollback does not revoke previously issued JWTs or clear cached tokens. These
contracts do not establish credential placement, rotation, tenant data
isolation, global audience/authorized-party enforcement, other upgrades or the
missing prerequisites/UI positive contract.
