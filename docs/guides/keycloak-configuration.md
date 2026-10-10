# OSAC Keycloak Configuration

This guide documents the OSAC-specific Keycloak configuration: the "osac" realm,
required clients, custom scopes, group-based multi-tenancy, and how OSAC services
integrate with Keycloak. For general Keycloak installation and administration, see
the [Keycloak documentation](https://www.keycloak.org/documentation).

For the Keycloak client and OIDC settings needed by Vault or OpenBao, see the
[Secrets Management setup guide](installation/secrets-management-configuration.md).

## Table of Contents

- [Overview](#overview)
- [OSAC Realm](#osac-realm)
  - [Clients](#clients)
  - [Client Scopes](#client-scopes)
  - [Groups and Multi-Tenancy](#groups-and-multi-tenancy)
  - [Realm Roles](#realm-roles)
- [Integration with OSAC Services](#integration-with-osac-services)
  - [Fulfillment Service Authentication](#fulfillment-service-authentication)
  - [Controller Credentials](#controller-credentials)
  - [CLI Authentication](#cli-authentication)
  - [Helm Values](#helm-values)
  - [Verification](#verification)
- [Deployment via Helm (default)](#deployment-via-helm-default)
  - [Helm Values](#helm-values-1)
  - [Admin Credentials](#admin-credentials)
  - [Realm Import (Helm)](#realm-import-helm)
  - [Upgrade (Helm)](#upgrade-helm)
- [Deployment via OLM](#deployment-via-olm)
  - [Operator Installation](#operator-installation)
  - [Keycloak Instance](#keycloak-instance)
  - [Realm Import (OLM)](#realm-import-olm)
- [BYO Keycloak](#byo-keycloak)

---

## Overview

OSAC uses Keycloak as its identity provider for OAuth 2.0 / OpenID Connect
authentication. All OSAC services authenticate against a single realm named
`osac`. The realm defines:

- **Clients** for the CLI, controller, and admin tools
- **Client scopes** that add OSAC-specific claims (audience, username, groups) to tokens
- **Organizations** that map to OSAC tenants for multi-tenant isolation
- **Realm roles** for authorization (e.g., `tenant-admin`)

The authoritative realm definition lives in the
[fulfillment-service](https://github.com/osac-project/osac)
repository at `fulfillment-service/it/charts/keycloak/files/realm.json`. The
[osac-installer](https://github.com/osac-project/osac) maintains a
deployment-ready copy at `osac-installer/charts/osac-prereqs/files/realm.json`.

---

## OSAC Realm

Realm name: **`osac`**

### Clients

OSAC defines three application-specific clients. Standard Keycloak clients
(`account`, `admin-cli`, `broker`, etc.) are also present with default settings.

| Client ID | Type | Auth Method | Purpose |
|-----------|------|-------------|---------|
| `osac-cli` | Public | Direct access grants (password flow) | CLI tool authentication for end users |
| `osac-controller` | Confidential | Client credentials (service account) | Fulfillment controller → fulfillment API |
| `osac-admin` | Confidential | Client credentials (service account) | Administrative operations |

#### osac-cli

- **Public client** — no client secret required
- **Direct access grants enabled** — supports username/password login from the CLI
- **Redirect URI:** `http://localhost` (for local CLI callback)
- **Default scopes:** `groups`, `basic`, `username`, `organization`, `roles`, `osac-api`
- **Token claims:** username, group membership, `osac-api` audience

#### osac-controller

- **Confidential client** — requires a client secret
- **Service account enabled** — authenticates via client credentials grant
- **Service account username:** `service-account-osac-controller`
- **Client roles (realm-management):** `manage-realm`, `manage-users`, `view-realm`,
  `view-users`, `manage-identity-providers`, `view-identity-providers`
- **Default scopes:** standard Keycloak scopes (web-origins, profile, roles, email)

The controller's client secret is extracted from `realm.json` and stored in a
Kubernetes Secret (`fulfillment-controller-credentials`). The OPA authorization
policy expects the JWT `preferred_username` to be `service-account-osac-controller`.

#### osac-admin

- **Confidential client** — requires a client secret
- **Service account enabled** — authenticates via client credentials grant
- **Default scopes:** standard scopes plus `osac-api`
- **Token claims:** includes `osac-api` audience (required for API access)

### Client Scopes

Three custom client scopes add OSAC-specific claims to access tokens:

| Scope | Mapper Type | Claim | Description |
|-------|------------|-------|-------------|
| `osac-api` | Audience mapper | `aud: "osac-api"` | Adds the `osac-api` audience to access tokens. Required for the fulfillment API to accept the token. |
| `username` | User attribute mapper | `username` | Adds the user's username as a top-level claim in the token. |
| `groups` | Group membership mapper | `groups` | Adds the user's group memberships as an array claim. Used for tenant-scoped authorization. |

All three scopes are assigned as **default scopes** on the `osac-cli` client,
ensuring CLI tokens always contain these claims.

### Groups and Multi-Tenancy

OSAC maps Keycloak Organization to tenants. Each Organization represents a tenant, and users
are assigned to Keycloak Organization Groups to grant access to that tenant's resources.

| Organization | Purpose |
|-------|---------|
| `admins` | Platform administrators |
| `tenant1` | Test tenant 1 |
| `tenant2` | Test tenant 2 |
| `my_group` | Test group |
| `your_group` | Test group |

The `organization` claim in the JWT token carries the user's tenant memberships. The
fulfillment service uses this claim to enforce tenant isolation — users can only
access resources belonging to their Organization.

Users can only belong to one tenant (Organization) at a time.

### Realm Roles

| Role | Description |
|------|-------------|
| `tenant-admin` | Grants elevated permissions within a tenant (e.g., managing users, creating resources) |
| `tenant-idp-manager` | Grants permission to manage identity providers within a tenant |
| `default-roles-my realm` | Composite role assigned to all users (includes `offline_access`, `uma_authorization`, `view-profile`, `manage-account`) |

---

## Integration with OSAC Services

### Fulfillment Service Authentication

The fulfillment service validates JWTs issued by Keycloak. It connects to
Keycloak using these configuration values:

| Setting | Value | Description |
|---------|-------|-------------|
| `auth.issuerUrl` | `https://keycloak.keycloak.svc.cluster.local/realms/osac` | OIDC issuer URL for token validation |
| `idp.provider` | `keycloak` | Identity provider type |
| `idp.url` | `https://keycloak.keycloak.svc.cluster.local` | Keycloak base URL for admin operations |

The fulfillment service fetches the OIDC discovery document from
`{issuerUrl}/.well-known/openid-configuration` to discover JWKS endpoints for
token signature verification.

### Controller Credentials

The fulfillment controller authenticates to the fulfillment API using the
`osac-controller` client's credentials:

```bash
# Extract the client secret from realm.json
FC_CLIENT_SECRET=$(jq -r \
  '.clients[] | select(.clientId == "osac-controller") | .secret' \
  prerequisites/keycloak/files/realm.json)

# Create the Kubernetes secret
oc create secret generic fulfillment-controller-credentials \
  --from-literal=client-id=osac-controller \
  --from-literal=client-secret="${FC_CLIENT_SECRET}" \
  -n ${NAMESPACE} --dry-run=client -o yaml | oc apply -f -
```

### CLI Authentication

The `osac` CLI authenticates using the `osac-cli` public client with the
password grant flow:

```bash
# The CLI prompts for username/password and obtains a token from:
# https://keycloak.keycloak.svc.cluster.local/realms/osac/protocol/openid-connect/token
osac login --username tenant1_admin
```

### Helm Values

Configure the fulfillment service Helm chart to point to Keycloak:

```yaml
service:
  auth:
    issuerUrl: https://keycloak.keycloak.svc.cluster.local/realms/osac
    controllerCredentials:
      secretName: fulfillment-controller-credentials
  idp:
    provider: keycloak
    url: https://keycloak.keycloak.svc.cluster.local
```

These values are set in `osac-installer/values/*/values.yaml` and passed
during `helm upgrade --install`.

### Verification

After deploying Keycloak and the fulfillment service, verify the integration:

```bash
# 1. Check the Keycloak realm is accessible
curl -sk https://keycloak.keycloak.svc.cluster.local/realms/osac \
  | jq .realm
# Expected: "osac"

# 2. Obtain a token using the CLI client
TOKEN=$(curl -sk \
  https://keycloak.keycloak.svc.cluster.local/realms/osac/protocol/openid-connect/token \
  -d "client_id=osac-cli" \
  -d "username=tenant1_admin" \
  -d "password=foobar" \
  -d "grant_type=password" | jq -r .access_token)

# 3. Inspect the token claims
echo $TOKEN | cut -d. -f2 | base64 -d 2>/dev/null | jq '{username, groups, aud}'
# Expected: username=tenant1_admin, groups=["/tenant1"], aud includes "osac-api"

# 4. Call the fulfillment API
curl -sk -H "Authorization: Bearer $TOKEN" \
  https://fulfillment-api-${NAMESPACE}.${DOMAIN}/v1/tenants
```

---

## Deployment via Helm (default)

Keycloak is deployed as part of the `osac-prereqs` Helm chart and is enabled by
default (`keycloak.enabled: true`). No OLM or operator is involved — Keycloak
runs as a standard Kubernetes `Deployment`.

### Helm Values

Key values in your `values.yaml`:

```yaml
keycloak:
  enabled: true
  hostname: keycloak.keycloak.svc.cluster.local  # Used as KC_HOSTNAME
  adminUsername: admin
  adminPassword: ""          # Generated randomly by init container (OSAC-2115)
  defaultUserPassword: ""    # Set on all test/demo users
  realmOverwrite: false      # Set to true to force-overwrite realm on restart
  images:
    keycloak: quay.io/keycloak/keycloak:26.6.4
    postgres: quay.io/sclorg/postgresql-18-c10s:c10s
  route:
    hostname: ""             # External route hostname (optional)
```

Keycloak listens on HTTPS port `8001`. The `keycloak` Service maps port `443 → 8001`,
so internal callers use `https://keycloak.keycloak.svc.cluster.local`.

### Admin Credentials

The Helm chart creates a `keycloak-admin-credentials` Secret in the `keycloak`
namespace containing:

| Key | Description |
|-----|-------------|
| `admin-username` | Keycloak master realm admin username |
| `admin-password` | Randomly generated by init container on first install |
| `default-user-password` | Password set on all demo/test users |

To retrieve the admin password after deployment:

```bash
oc get secret keycloak-admin-credentials -n keycloak \
  -o jsonpath='{.data.admin-password}' | base64 -d
```

### Realm Import (Helm)

The realm is imported at startup via `--import-realm`. An init container
(`resolve-realm-secrets.sh`) runs first to inject randomized client secrets
into the realm.json before Keycloak starts.

The realm source is bundled in the chart at
`osac-installer/charts/osac-prereqs/files/realm.json`.

To force a re-import of the realm (e.g. after config changes), set
`keycloak.realmOverwrite: true` and run `helm upgrade`, then set it back to
`false`.

### Upgrade (Helm)

```bash
# Update keycloak.images.keycloak in values.yaml, then:
helm upgrade osac-prereqs charts/osac-prereqs/ -f values/<env>/values.yaml -n osac

# Rollback if needed
helm rollback osac-prereqs -n osac
```

> **Note**: Keycloak uses Liquibase for database migrations — these run
> automatically on startup and are forward-only. Always back up the database
> before upgrading.

---

## Deployment via OLM

> **Note**: This section covers the OLM-based (carrier-grade) Keycloak deployment.
> The default `osac-installer` deployment uses a Helm-managed Keycloak Deployment —
> if that is your setup, refer to the osac-installer chart documentation instead.

OSAC deploys Keycloak using the upstream Keycloak Operator via OLM (Operator
Lifecycle Manager). This section summarizes the deployment; for full details
see the [osac-installer prerequisites](https://github.com/osac-project/osac/tree/main/osac-installer/prerequisites/keycloak).

### Operator Installation

```bash
# Apply the OLM manifests (Namespace + OperatorGroup + Subscription)
oc apply -f prerequisites/keycloak/operator.yaml

# Wait for the operator CSV to succeed
until KC_CSV=$(oc get csv --no-headers -n keycloak 2>/dev/null \
  | awk '/keycloak/ { print $1 }' | tail -1) && [[ -n "${KC_CSV}" ]]; do
  sleep 10
done
oc wait csv/${KC_CSV} -n keycloak \
  --for=jsonpath='{.status.phase}'=Succeeded --timeout=300s
```

The Subscription pins to a specific operator version via `startingCSV`
(e.g. `keycloak-operator.v26.6.4`, matching Keycloak server `26.6.4`) with
`installPlanApproval: Manual` for controlled upgrades.

### Keycloak Instance

```bash
# Deploy the PostgreSQL database
oc apply -k prerequisites/keycloak/database/

# Deploy the Keycloak CR, TLS certificate, and Route
oc apply -f prerequisites/keycloak/keycloak-instance.yaml

# Wait for the Keycloak CR to be ready
oc wait keycloak/osac-keycloak -n keycloak \
  --for=jsonpath='{.status.conditions[?(@.type=="Ready")].status}'=True \
  --timeout=600s
```

The operator creates a Service named `osac-keycloak-service` (derived from the
CR name `osac-keycloak`) on port 8443 (HTTPS).

### Realm Import (OLM)

The OSAC realm is imported via a `KeycloakRealmImport` CR generated dynamically
from `realm.json`:

```bash
jq -n --arg realm "$(cat prerequisites/keycloak/files/realm.json)" '{
    apiVersion: "k8s.keycloak.org/v2beta1",
    kind: "KeycloakRealmImport",
    metadata: {name: "osac-realm-import", namespace: "keycloak"},
    spec: {keycloakCRName: "osac-keycloak", realm: ($realm | fromjson)}
}' | oc apply -f -

# Wait for the import to complete
oc wait keycloakrealmimport/osac-realm-import -n keycloak \
  --for=jsonpath='{.status.conditions[?(@.type=="Done")].status}'=True \
  --timeout=300s
```

---

## BYO Keycloak

OSAC supports using an existing (Bring Your Own) Keycloak instance. Requirements:

1. **Create an "osac" realm** with the clients, scopes, and groups described above
2. **Configure the Helm values** to point to your Keycloak instance:

```yaml
service:
  auth:
    issuerUrl: https://your-keycloak.example.com/realms/osac
  idp:
    provider: keycloak
    url: https://your-keycloak.example.com
```

3. **Create the controller credentials secret** with the `osac-controller` client
   ID and secret from your realm
4. **Ensure the `osac-api` audience scope** is configured on your clients — the
   fulfillment service validates this audience in tokens

For the realm itself, either create it manually or import the `realm.json` from
the osac-installer repository as a starting point and customize for your
environment:

```bash
# Import realm.json via the Keycloak Admin API
curl -k -sf -X POST \
  "https://your-keycloak.example.com/admin/realms" \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H "Content-Type: application/json" \
  -d @osac-installer/charts/osac-prereqs/files/realm.json
```
