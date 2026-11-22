# Update the CSI role scope in an existing Keycloak realm

The corrected installer exports give `osac-csi-driver` access to its assigned
realm role while retaining `fullScopeAllowed=false`. Fresh installations import
that mapping. Keycloak 26.6.4 [skips existing realms during startup import](https://www.keycloak.org/26.6/server/importExport#_importing_a_realm_during_startup),
so replacing the export or restarting Keycloak leaves an older persisted realm
unchanged.

An existing installation can use
[`update-csi-realm-scope.py`](../../../osac-installer/scripts/update-csi-realm-scope.py)
to add that one missing direct realm-role scope mapping. An administrator runs
it explicitly; installing or upgrading the charts does not run it.

## Prerequisites

- Python 3.11 or later on the administrator's Linux workstation, with network access
  to the correct Keycloak HTTPS endpoint. Use the server root, including any
  configured context path, rather than a realm or Admin API URL.
- An **already authorized Keycloak administrator**, or existing equivalent
  rights to look up/view the target client and role, manage that client and map the
  role into its client scope. OSAC API administrative permissions do not
  establish Keycloak administrative permission. The updater creates no accounts,
  role grants, Kubernetes access or credential distribution.
- The bundled `osac` realm, client and role identities. The tool checks the
  exported realm UUID `01e6c34f-6906-4907-9cbd-2dec7a6a0e1d`, client UUID
  `d5e6f7a8-b9c0-4d1e-2f3a-567890abcdef` and realm-role UUID
  `c4d5e6f7-a8b9-0c1d-2e3f-456789abcdef`. It refuses reconstructed/custom
  identities, public or disabled clients, unexpected flows/authenticators,
  composite/client roles and `fullScopeAllowed=true`. Review those differences
  with the Keycloak administrator; do not relax the tool's checks.
- An agreed backup and rollback owner, using the installation's normal database
  backup procedure. Serialize administrative changes to this client, role and
  direct scope mappings through validation and any rollback. A local record
  lock does not coordinate other administrators or different record files.

The [Keycloak 26.6.4 client resource](https://github.com/keycloak/keycloak/blob/26.6.4/services/src/main/java/org/keycloak/services/resources/admin/ClientResource.java#L378)
requires client view/manage rights. Its [scope-mapping resource](https://github.com/keycloak/keycloak/blob/26.6.4/services/src/main/java/org/keycloak/services/resources/admin/ScopeMappedResource.java#L221)
additionally requires permission to map the role. The tool uses only target
client lookup/read, the named realm role and direct realm-scope endpoints.
HTTP 401/403 stops the operation; it does not grant itself more rights.

## Inspect and apply

Run commands from the repository root. Create a private operation directory:

```bash
umask 077
CSI_UPGRADE_DIR=$(mktemp -d "${XDG_RUNTIME_DIR:-/tmp}/osac-csi-scope.XXXXXX")
```

Use your existing approved administrator login or credential-manager workflow
to place a **raw short-lived bearer token** in
`$CSI_UPGRADE_DIR/admin.token`. The file must be a regular file, owned by your
OS user, mode `0600`, with no symlink or hard link. Never put the token in
command arguments, shell history, logs, tickets or chat. The tool accepts a
file path, performs no login and prints no response bodies or credentials.
Renew the file securely through the same login workflow if the token expires.

Replace the example server and CA path below with the approved endpoint and
its trust bundle. The optional `--ca-file` adds trusted PEM certificates to
system trust. Omit it for a publicly trusted server. Certificate and hostname
verification are always enabled. Redirects, plaintext HTTP and URLs containing
credentials, queries or fragments are refused. Proxy environment variables
are not used; connect directly from the authorized workstation.

```bash
python3 osac-installer/scripts/update-csi-realm-scope.py \
  --server https://keycloak.example.invalid \
  --token-file "$CSI_UPGRADE_DIR/admin.token" \
  --ca-file /path/to/keycloak-ca.pem
```

The default is read-only. `would-add` means the direct mapping is missing;
`already-present` means it exists. Read access alone does not prove write
permission. There are no changes to clients, flags, secrets, users, role
definitions or unrelated mappings.

Apply using a new record path in the private directory:

```bash
python3 osac-installer/scripts/update-csi-realm-scope.py \
  --server https://keycloak.example.invalid \
  --token-file "$CSI_UPGRADE_DIR/admin.token" \
  --ca-file /path/to/keycloak-ca.pem \
  --apply --record "$CSI_UPGRADE_DIR/csi-scope.json"
```

`added` with `changed=true` confirms the single POST and subsequent state
verification. Repeating the same command returns `already-present` with
`changed=false`, without another mutation or replacing the ownership record.
If the mapping was present before this operation, the record is non-owning;
its rollback can never delete that preexisting binding.

Keep the secret-free operation record, which contains target identifiers and
state fingerprints, in its original private directory. Files use mode `0600`
and atomic replacement with filesystem synchronization. Preserve the record
through the rollback window using your normal protected operational storage;
remove the token file after use. Temporary runtime directories may disappear
on logout or reboot. Never put credential files in a source checkout.

Have the workload owner obtain a **fresh** CSI token through its normal
credential mechanism and validate private `Volumes/List` access in the
installation. Unrelated private `Clusters/List` must remain denied under the
shipped policy. Record only statuses, not tokens. The updater verifies mapping
state; it does not certify the running service or storage provisioning.

## Roll back this operation

While the administrative window is still exclusive, use the record belonging
to the operation that added the mapping:

```bash
python3 osac-installer/scripts/update-csi-realm-scope.py \
  --server https://keycloak.example.invalid \
  --token-file "$CSI_UPGRADE_DIR/admin.token" \
  --ca-file /path/to/keycloak-ca.pem \
  --rollback --record "$CSI_UPGRADE_DIR/csi-scope.json"
```

Rollback verifies the server, fixed identities, client state and direct scopes
against the record. It deletes only the one binding that this operation added,
then verifies the original scope state. `rolled-back` reports that deletion;
`rollback-noop` reports an already completed rollback or a non-owning record.
Reapply after rollback with a **new** operation record. A consumed record is
not authorization to delete a later addition.

Newly issued CSI tokens again lack the role after rollback, so private
`Volumes/List` is denied. Previously issued JWTs remain valid until expiry;
rollback neither revokes them nor clears a driver's cached token source.
Credential rotation and workload reloads follow their existing procedures.

## Failures and concurrency

Requests have a default 10-second timeout (`--timeout` accepts 1–30 seconds),
a response size limit and a 120-second overall operation deadline. The tool
never retries a mutation automatically. It refuses changed/ambiguous state,
invalid JSON, unexpected responses and unsafe credential or record files.

An HTTP timeout, interrupted process or failed record write can occur after
Keycloak has accepted a mutation. The record is written as `prepared` before
POST and `rollback-pending` before DELETE; only successful read-back advances
it to a completed state. An uncertain record is refused by both apply and
rollback. Do not edit it to force progress. Have the Keycloak administrator
reconcile the named binding against the operation record and Admin API audit
history, establish ownership, and begin a new operation only after that review.

The Admin API provides no compare-and-swap transaction with the local record.
State is checked again immediately before mutation, but concurrent changes
between that check and the write cannot be excluded. Removing and re-adding
an identical binding externally is also indistinguishable from the original
binding. This is why exclusive administration throughout the operation and
rollback window is required. Rollback deliberately fails if unrelated direct
scopes have changed; it never clears all mappings or replaces the realm.

## Validation boundary

The [realm authorization suite](../../../fulfillment-service/it/realmauth/README.md)
executes this entry point against both installer artifacts on disposable
Keycloak 26.6.4 databases. It covers persisted pre-fix state, corrected-import
restart, fresh JWT verification, real private RPC authorization, preservation
sentinels, idempotence and rollback. Protocol fixtures cover failure and
transport paths. These checks do not establish credential placement, tenant
data filtering, a deployed CSI/PVC/provider lifecycle, or contracts for all
other machine clients.
