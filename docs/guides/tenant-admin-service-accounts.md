# Tenant Admin Guide: Service Accounts

**Audience**: OSAC tenant administrators
**Interface**: Fulfillment API and `osac` CLI

This guide covers creating and maintaining tenant service accounts. It documents the CLI/API
workflow; the UI walkthrough remains pending the separately owned UI implementation.

## Before you start

Log in as a tenant admin for the tenant that owns the account. Service accounts are always scoped
to the caller's tenant. The API assigns the tenant; do not copy a tenant ID from another resource
into a create file.

The commands below emit JSON. Create and secret rotation wait for asynchronous controller work to
finish, then read the new credential from the tenant-scoped Secret referenced by
`status.client_secret`. The API Create and RegenerateSecret responses do not include secret bytes.
The CLI prints the retrieved `client_secret` once in the create or rotation JSON response. Protect
that output as a password; do not save it in source files, shell history, or logs.

## Create and inspect an account

Create `worker.json` with a name and any desired account metadata:

```json
{
  "@type": "type.googleapis.com/osac.public.v1.ServiceAccount",
  "metadata": {
    "name": "batch-worker",
    "display_name": "Batch worker",
    "description": "Runs tenant batch jobs"
  }
}
```

Create the account and read it later without exposing the credential:

```shell
osac serviceaccount create -f worker.json
osac serviceaccount get batch-worker
osac serviceaccount list --filter "this.metadata.labels['team'] == 'data'"
```

Create output includes `service_account`, `client_id`, and `client_secret`. The account object
contains only a Secret reference. Get, list, and update output never include credential bytes. If
the configured Fulfillment service predates the asynchronous credential API, the CLI reports that
the service must be upgraded.

## Update account metadata

An update file identifies the account with `id` or `metadata.name`. It can set
`metadata.display_name`, `metadata.description`, and `metadata.labels`:

```json
{
  "id": "<service-account-id>",
  "metadata": {
    "display_name": "Batch worker (production)",
    "labels": { "environment": "production" }
  }
}
```

```shell
osac serviceaccount update -f worker-update.json
```

`ServiceAccountSpec` has no role grants. Authorization comes from an active RoleBinding in the
account's tenant; create or delete a RoleBinding to change access.

## Grant and revoke API access

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

Deleting the RoleBinding revokes access on the next request. If a binding includes multiple
subjects, remove only the intended service-account reference from that binding instead of deleting
it.

## Rotate or delete credentials

Regenerate a credential when a secret is exposed or on your rotation schedule:

```shell
osac serviceaccount regenerate-secret batch-worker
```

The command waits for `secret_rotation_state` to become complete and reads the replacement through
`Secrets/Get`. Rotation has no grace period: the old secret stops working when the Keycloak update
applies. Existing access tokens remain valid until their normal expiry. Replace the stored secret
used by workloads after the CLI returns the new value.

Request account deletion with:

```shell
osac serviceaccount delete batch-worker
```

Deletion is asynchronous. The account name remains reserved until the controller completes cleanup.
The controller stores the per-client Keycloak registration token in a system-scoped Secret through
the private Secrets API. It uses that token to delete the client; the tenant admin does not need
access to it.

## Tenant boundary and workload access

An ordinary service-account user cannot read raw Secrets or watch managed Events. It can call only
the APIs permitted by its assigned RoleBinding, and only within its tenant. Keep credential
retrieval with an authorized tenant admin and provide the workload with the client ID and secret
through your approved secret-management system.
