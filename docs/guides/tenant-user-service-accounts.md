# Tenant User Guide: Use a Service Account

**Audience**: Tenant users running workloads that call OSAC APIs
**Interface**: OAuth 2.0 `client_credentials` and the `osac` CLI

A tenant admin creates the service account, grants it a role through a tenant RoleBinding, and
provides its client ID and secret through the tenant's approved secret-management system. This
guide covers workload use of those credentials; the UI walkthrough remains pending the separately
owned UI implementation.

## Obtain an access token

Use the client ID and secret with the OAuth 2.0 `client_credentials` grant against the token
endpoint published by the configured OSAC OIDC issuer. For a CLI check, `osac login` performs that
grant and reads the credentials from files:

```shell
osac login https://fulfillment-api.example.com \
  --client-id-file /run/secrets/osac/client-id \
  --client-secret-file /run/secrets/osac/client-secret
```

The file-based flags keep credentials out of shell history and process arguments. Avoid passing a
client secret directly as a command-line value or printing it in application logs.

The issued token carries the service-account identity and tenant. Calls are authorized by the
active RoleBinding for that account. For example, if its assigned role permits listing clusters:

```shell
osac get clusters -o json
```

The token cannot access other tenants. An ordinary service-account user cannot read raw Secrets or
watch managed Events. Ask your tenant admin to change the account's RoleBinding if a required API
returns `PermissionDenied`.

For an application, use an OAuth/OIDC client library configured for the issuer's discovered token
endpoint and the `client_credentials` grant. Keep the client ID and secret in the workload's
mounted secret files or secret manager; do not embed the credential in an image or configuration
repository. Call only the OSAC APIs allowed by the account's assigned role.

## Respond to credential rotation

The tenant admin may rotate the secret. Rotation has no grace period: the old secret stops working
when the Keycloak update applies, while access tokens already issued remain valid until their normal
expiry. Update the workload's secret source with the replacement provided by the tenant admin, then
restart or refresh the workload's client-credentials exchange.

Removing the account's RoleBinding revokes its API permissions on the next request. If the workload
needs different access, request a change from the tenant admin.
