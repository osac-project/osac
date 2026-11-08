# Tenant Service Accounts Guide

**Audience**: OSAC tenant administrators and tenant users running workloads
**Interface**: Fulfillment API, `osac` CLI, and OAuth 2.0 `client_credentials`

A service account is a tenant-owned machine identity. It does not belong to a project. A tenant
administrator creates the account and grants API access through a tenant RoleBinding. The workload
uses the client ID and secret supplied by its administrator to obtain tokens for the APIs allowed by
that role.

The account stores a reference to its credential Secret, not the credential bytes. Keep the client
secret in the tenant's approved secret-management system; do not put it in source files, shell
history, or logs.

## For tenant administrators

### Create and inspect an account

Log in as an administrator for the tenant that will own the account. The API assigns the tenant;
do not copy a tenant ID from another resource into the create file. Create `worker.json` with the
account name and any desired labels:

```json
{
  "@type": "type.googleapis.com/osac.public.v1.ServiceAccount",
  "metadata": {
    "name": "batch-worker",
    "labels": { "team": "data" }
  }
}
```

Create and inspect the account, or list accounts matching a filter:

```shell
osac create -f worker.json
osac get serviceaccounts batch-worker -o json
osac get serviceaccounts --filter "this.metadata.labels['team'] == 'data'" -o json
```

Provisioning is asynchronous. Wait until `status.state` is `READY`, then read the credential Secret
by the ID in `status.client_secret`. Only an authorized tenant administrator can read that Secret.
Its byte values appear base64-encoded in JSON:

```shell
osac get secrets <client-secret-id> -o json
```

Service-account get and list output includes the Secret reference but not its credential bytes.

### Edit account metadata

Use `osac edit`, which opens the account in your configured editor. Only `metadata.labels` can be
changed:

```shell
osac edit serviceaccounts batch-worker
```

`ServiceAccountSpec` has no role grants. Manage access with RoleBindings instead.

### Grant or revoke API access

Create a RoleBinding for the role and account. Use the role's ID, not its display name. A separate
binding per service account makes revocation a single delete and avoids removing another account's
grant.

```json
{
  "@type": "type.googleapis.com/osac.public.v1.RoleBinding",
  "metadata": { "name": "batch-worker-automation" },
  "spec": {
    "role": { "id": "<tenant-automation-role-id>" },
    "service_accounts": [{ "id": "<service-account-id>" }]
  }
}
```

```shell
osac create -f worker-role-binding.json
osac get rolebindings batch-worker-automation -o json
osac delete rolebindings batch-worker-automation
```

Deleting the RoleBinding revokes its grant on the next request. If a binding includes multiple
subjects, remove only the intended service-account reference instead of deleting the whole binding.

### Rotate or delete an account

Regenerate a credential when it is exposed or on your rotation schedule:

```shell
osac regenerate serviceaccount batch-worker
```

The CLI polls the account and its credential Secret until the Secret reference or credential
changes, then returns the account with the replacement client ID and secret as JSON. Protect the
credential output. Rotation has no grace period: the old secret stops working when the Keycloak
update applies. Already-issued access tokens remain valid until their normal expiry. Update the
secret source used by workloads after the CLI returns the replacement.

Account deletion is asynchronous, and the account name remains reserved until cleanup completes:

```shell
osac delete serviceaccounts batch-worker
```

The controller uses a system-scoped Secret for Keycloak client cleanup; the tenant administrator
does not need access to that Secret.

## For tenant users running workloads

The tenant administrator provides the client ID and secret through the approved secret-management
system. Use them with the OAuth 2.0 `client_credentials` flow against the configured OSAC API:

```shell
osac login https://fulfillment-api.example.com --flow credentials \
  --client-id-file /run/secrets/osac/client-id \
  --client-secret-file /run/secrets/osac/client-secret
```

The file flags keep credentials out of command-line values and process arguments. Avoid printing
credentials in application logs.

The resulting token carries the service-account identity and tenant. Each API call is authorized by
the account's active RoleBindings. For example, if its assigned role permits listing clusters:

```shell
osac get clusters -o json
```

The token cannot access another tenant. An ordinary service-account user cannot read raw Secrets or
watch managed Events. If a required API returns `PermissionDenied`, ask the tenant administrator to
change the account's RoleBinding.

For an application, use an OAuth/OIDC client library configured for the issuer's token endpoint and
the `client_credentials` grant. Store the ID and secret in mounted secret files or a secret manager;
do not embed them in an image or configuration repository. Call only the OSAC APIs permitted by the
account's assigned role.

### After a credential rotation

The administrator must provide the replacement secret through the approved secret-management
system. Replace the workload's secret source, then restart the workload or refresh its
client-credentials exchange. The old secret stops working when the Keycloak update applies, while
already-issued tokens remain valid until expiry.

Removing the account's RoleBinding revokes its API permissions on the next request. Ask the tenant
administrator to change the binding if the workload needs different access.
