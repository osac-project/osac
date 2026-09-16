# Integrating Enterprise LDAP with OSAC via External Identity Provider

**Last Updated**: 2026-07-06
**Audience**: Cloud administrators, tenant administrators
**Status**: Draft

---

## Overview

OSAC uses a central Keycloak instance with an internal `osac` realm for
authentication and authorization. This realm is managed by the OSAC platform
and is **not available for direct tenant configuration**.

If your organization uses an LDAP directory (389 Directory Server, Active
Directory, OpenLDAP, Red Hat Directory Server) for user management, you can
integrate it with OSAC by placing an **OIDC-capable Identity Provider (IdP)**
in front of your LDAP and federating it with the OSAC realm.

This guide covers:

1. Why direct LDAP configuration in the OSAC realm is not supported
2. How to set up your own IdP with LDAP federation
3. How to register your IdP with OSAC for tenant-scoped authentication

### What This Guide Does NOT Cover

- Configuring the OSAC realm itself (managed by the platform)
- The internal LDAP integration architecture (see the internal LDAP +
  Keycloak setup guide, not published in this repo)

---

## Why Direct LDAP in the OSAC Realm Is Not Supported

The `osac` realm is the platform's internal identity boundary. It manages
service accounts, client credentials, and organization membership for all
tenants. Allowing tenants to configure LDAP federation directly in this
realm would:

1. **Break tenant isolation** — LDAP federation is realm-wide; all
   organizations would share the same LDAP provider, and username collisions
   across tenants could cause authentication failures or credential leakage
   to the wrong directory.

2. **Expose the internal realm** — Tenants would need admin access to the
   OSAC Keycloak instance, which is not operationally acceptable.

3. **Prevent password boundary enforcement** — In a multi-tenant environment,
   user passwords must never leave the tenant's own infrastructure. With
   direct LDAP federation, Keycloak sends the password to the LDAP server
   during every authentication — if multiple LDAPs are configured in one
   realm, passwords could be sent to the wrong tenant's directory.

Instead, OSAC uses **Keycloak Organizations with OIDC IdP brokering**:
each tenant provides (or deploys) their own OIDC Identity Provider, and the
OSAC realm brokers authentication to it. Passwords never enter the OSAC
platform — only signed tokens are exchanged.

```
┌──────────────────────────────────────────────────────────────┐
│                    OSAC Platform                              │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐   │
│  │              OSAC Keycloak (osac realm)                │   │
│  │              Organizations: ENABLED                    │   │
│  │                                                       │   │
│  │  ┌─────────────┐           ┌─────────────┐           │   │
│  │  │ Org: Acme   │           │ Org: Globex │           │   │
│  │  │ IdP: oidc   │           │ IdP: oidc   │           │   │
│  │  └──────┬──────┘           └──────┬──────┘           │   │
│  └─────────┼─────────────────────────┼───────────────────┘   │
│            │ OIDC tokens only        │ OIDC tokens only       │
└────────────┼─────────────────────────┼────────────────────────┘
             │                         │
             ▼                         ▼
   ┌─────────────────┐       ┌─────────────────┐
   │  Acme's IdP     │       │  Globex's IdP   │
   │  (Keycloak /    │       │  (Azure AD /    │
   │   Okta / ADFS)  │       │   Okta / ...)   │
   │       │         │       │       │         │
   │       ▼         │       │       ▼         │
   │  Acme's LDAP    │       │  Globex's LDAP  │
   └─────────────────┘       └─────────────────┘

   Passwords stay here        Passwords stay here
```

---

## Prerequisites

Before starting, you need:

| Requirement | Description |
|-------------|-------------|
| LDAP directory | A running LDAP server (389-ds, Active Directory, OpenLDAP, RHDS) accessible from your IdP |
| OIDC-capable IdP | Keycloak, Azure AD/Entra ID, Okta, ADFS, PingFederate, Authentik, or any OIDC-compliant provider |
| Domain | A verified domain for your organization (e.g., `acme-corp.example.com`) |
| Network access | Your IdP must be reachable via HTTPS from the OSAC Keycloak instance |
| TLS certificate | A valid TLS certificate on your IdP, signed by a well-known public CA |

**If you don't have an IdP yet**, see
[Option B: Deploy Your Own IdP for LDAP](#option-b-deploy-your-own-idp-for-ldap)
below.

---

## Option A: Register an Existing OIDC Identity Provider

If your organization already operates an OIDC-capable IdP (Azure AD, Okta,
ADFS, PingFederate, or your own Keycloak), you only need to:

1. Register your IdP with OSAC (which returns the redirect URI)
2. Create an application/client registration in your IdP using that URI

### Step 1: Register Your IdP with OSAC

As a tenant administrator, use the
[OSAC identity providers API](https://github.com/osac-project/osac/blob/main/fulfillment-service/proto/public/osac/public/v1/identity_providers_service.proto)
to register your IdP. The OSAC administrator will provide you with
break-glass credentials to get started.

First, log in and obtain a token:

```bash
osac login
```

Then register the identity provider. You can initially set placeholder
values for `client_id` and `client_secret` — you will update them after
creating the client in your IdP (Step 3).

> **Note:** A dedicated `osac create identityprovider` subcommand is
> planned. In the meantime, use the generic `osac create -f` with a
> YAML file.

```bash
osac create -f - <<'EOF'
"@type": type.googleapis.com/osac.public.v1.IdentityProvider
metadata:
  name: acme-corp-idp
spec:
  title: Acme Corp OIDC
  enabled: true
  oidc:
    issuer: https://your-idp.example.com
    authorization_url: https://your-idp.example.com/protocol/openid-connect/auth
    token_url: https://your-idp.example.com/protocol/openid-connect/token
    client_id: placeholder
    client_secret: placeholder
    default_scopes: openid profile email
EOF
```

**Via OSAC REST API:**

```bash
OSAC_TOKEN=$(osac get token)

curl -s -X POST "${OSAC_API}/api/fulfillment/v1/identity_providers" \
    -H "Authorization: Bearer ${OSAC_TOKEN}" \
    -H "Content-Type: application/json" \
    -d '{
  "metadata": {
    "name": "acme-corp-idp"
  },
  "spec": {
    "title": "Acme Corp OIDC",
    "enabled": true,
    "oidc": {
      "issuer": "https://your-idp.example.com",
      "authorization_url": "https://your-idp.example.com/protocol/openid-connect/auth",
      "token_url": "https://your-idp.example.com/protocol/openid-connect/token",
      "client_id": "placeholder",
      "client_secret": "placeholder",
      "default_scopes": "openid profile email"
    }
  }
}'
```

After registration, the OSAC platform returns the redirect URI for your
IdP. It follows the pattern:

```
https://<osac-host>/realms/osac/broker/<idp-name>/endpoint
```

### Step 2: Ensure LDAP Federation in Your IdP

Your IdP must be configured to authenticate users against your LDAP
directory. This is specific to your IdP:

| IdP | LDAP Integration Method |
|-----|------------------------|
| **Azure AD / Entra ID** | Azure AD Connect or Cloud Sync from on-premises AD |
| **Okta** | Okta AD Agent or LDAP Agent |
| **Keycloak** | User Storage Federation (LDAP provider) |
| **ADFS** | Native Active Directory integration |
| **PingFederate** | LDAP Data Store |
| **Authentik** | LDAP Source |

The key requirement is that users authenticating through your IdP are
validated against your LDAP directory, and the resulting OIDC tokens
include the user's email address as a claim.

### Step 3: Create the OIDC Client in Your IdP

Using the redirect URI from Step 1, create a new OIDC client/application
in your IdP with the following settings:

| Setting | Value |
|---------|-------|
| **Client type** | Confidential (with client secret) |
| **Grant type** | Authorization Code |
| **Redirect URI** | The URI returned by OSAC in Step 1 |
| **Scopes** | `openid`, `email`, `profile` |
| **Token format** | JWT (signed, not opaque) |

Record the **Client ID** and **Client Secret** generated by your IdP.

### Step 4: Update the OSAC Identity Provider

Update the identity provider you registered in Step 1 with the actual
client credentials from your IdP:

```bash
osac edit identityprovider acme-corp-idp
```

Set `spec.oidc.client_id` and `spec.oidc.client_secret` to the values
from your IdP.

### Step 5: Verify the Integration

Verify your token contains the correct organization claim:

```bash
osac get token --payload
```

You should see:

```json
{
  "preferred_username": "alice",
  "email": "alice@acme-corp.example.com",
  "organization": ["acme-corp"]
}
```

### What Happens at Login

After registration, users with your domain are automatically routed
to your IdP:

1. User opens the OSAC UI or runs `osac login`
2. Redirected to the OSAC login page — **user@domain field only**
3. User enters their identifier (e.g., `alice@acme-corp.example.com`)
4. OSAC matches the domain to your organization and redirects to your IdP
5. User authenticates against your IdP (which validates against your LDAP)
6. Redirected back to OSAC with a JWT containing `"organization": ["acme-corp"]`
7. User is logged in with tenant-scoped access

Your password is never seen by the OSAC platform — only a signed token
is exchanged.

---

## Option B: Deploy Your Own IdP for LDAP

If your organization only has a bare LDAP directory without an OIDC-capable
IdP, you need to deploy one to bridge the gap. Any OIDC-compliant IdP
with LDAP federation support will work (Keycloak, Authentik, etc.).

Deploying and configuring an IdP is outside the scope of OSAC documentation.
Refer to your chosen IdP's documentation for installation and LDAP
federation setup:

- **Keycloak**: [RHBK User Storage Federation](https://docs.redhat.com/en/documentation/red_hat_build_of_keycloak/26.6/html/server_administration_guide/user-storage-federation)
- **Authentik**: [LDAP Source](https://docs.goauthentik.io/docs/sources/ldap/)

Once your IdP is running with LDAP federation configured, follow
[Option A](#option-a-register-an-existing-oidc-identity-provider) to
register it with OSAC.

---

## OSAC Administrator: Onboarding a Tenant

This section is for OSAC platform administrators. The only step the OSAC
administrator performs is creating the tenant. After that, the tenant
administrator manages their own identity providers via the OSAC API.

### Create the Tenant

> **Note:** A dedicated `osac create tenant` subcommand is planned. In the
> meantime, use the generic `osac create -f` with a YAML file.

Create a file `tenant.yaml`:

```yaml
"@type": type.googleapis.com/osac.public.v1.Tenant
metadata:
  name: acme-corp
spec:
  domains:
    - acme-corp.example.com
```

Then create the tenant:

```bash
osac create -f tenant.yaml
```

After the tenant is created, provide the tenant administrator with
break-glass credentials. The tenant administrator then registers their
IdP using the steps in [Option A](#option-a-register-an-existing-oidc-identity-provider).

### Verify the Integration

1. Open the OSAC UI or run `osac login`
2. Enter a test user identifier from the tenant's domain
   (e.g., `alice@acme-corp.example.com`)
3. Confirm you are redirected to the tenant's IdP login page
4. Authenticate and verify the JWT contains the `organization` claim:

```json
{
  "preferred_username": "alice",
  "email": "alice@acme-corp.example.com",
  "organization": ["acme-corp"]
}
```

---

## TLS Requirements

### Tenant IdP → OSAC

Your identity provider **must use a TLS certificate signed by a well-known
public Certificate Authority**. The OSAC platform does not currently support
importing custom or private CA certificates for tenant IdP connections.

If your IdP uses a self-signed or private CA certificate, you will need to
obtain a certificate from a well-known CA (e.g., Let's Encrypt, DigiCert,
GlobalSign) before registering it with OSAC.

### Tenant IdP → LDAP

Your IdP's connection to your LDAP server should use LDAPS (port 636) in
production. This is entirely within your infrastructure and does not
involve the OSAC platform.

---

## Supported Identity Providers

Any OIDC-compliant IdP should work with OSAC. **None of the options below
have been tested end-to-end with OSAC yet.** They are listed as examples
based on each product's documented OIDC and LDAP capabilities, not as
recommendations or supported configurations.

### IdPs with Native LDAP Authentication

These IdPs can authenticate users directly against an LDAP directory and
issue OIDC tokens:

| IdP | LDAP Authentication Method | Reference |
|-----|---------------------------|-----------|
| **Keycloak** (community or RHBK) | User Storage Federation | [RHBK User Federation](https://docs.redhat.com/en/documentation/red_hat_build_of_keycloak/26.6/html/server_administration_guide/user-storage-federation) |
| **Okta** | Okta AD Agent / LDAP Agent | [Okta LDAP Agent](https://help.okta.com/en-us/content/topics/directory/ldap-agent-main.htm) |
| **ADFS** | Native Active Directory | [ADFS Overview](https://learn.microsoft.com/en-us/windows-server/identity/ad-fs/ad-fs-overview) |
| **PingFederate** | LDAP Data Store | [PingFederate LDAP](https://docs.pingidentity.com/pingfederate/latest/administrators_reference_guide/pf_c_ldapDataStores.html) |
| **Authentik** (open source) | LDAP Source | [Authentik LDAP](https://docs.goauthentik.io/docs/sources/ldap/) |

### IdPs with Directory Sync (Not Direct LDAP Auth)

These platforms synchronize user data from on-premises directories but do
not perform LDAP bind authentication directly. They can still serve as
OIDC IdPs for OSAC after syncing your directory:

| IdP | Sync Method | Reference |
|-----|------------|-----------|
| **Azure AD / Entra ID** | Azure AD Connect (syncs AD users to cloud) | [Azure AD Connect](https://learn.microsoft.com/en-us/entra/identity/hybrid/connect/whatis-azure-ad-connect) |
| **Google Workspace** | Google Cloud Directory Sync | [GCDS Overview](https://support.google.com/a/answer/106368) |

---

## Frequently Asked Questions

### Can I configure LDAP directly in the OSAC Keycloak realm?

No. The `osac` realm is managed by the OSAC platform and does not support
direct tenant modifications. Place an OIDC IdP in front of your LDAP and
register it with OSAC as described in this guide.

### What if I have multiple domains?

You can register multiple domains for a single organization. All domains
will route to the same IdP:

```json
{
  "name": "acme-corp",
  "domains": [
    {"name": "acme-corp.example.com", "verified": true},
    {"name": "acme.io", "verified": true}
  ]
}
```

### What happens if a user's domain is not registered?

Authentication will fail at the domain-matching step. The user will see
an error on the login page. Contact your tenant administrator to register
the domain.

### Can two tenants share an LDAP server?

Yes, as long as each tenant has its own IdP (or its own realm in a shared
Keycloak instance). The isolation is at the IdP level, not the LDAP level.
Each IdP can point to the same LDAP server with different base DNs or
search filters.

### Do users need to change their workflow?

No. Users authenticate with the same credentials they use today. The only
visible change is that the login page asks for a user@domain identifier
first, then redirects to their organization's IdP for password entry.

### What about CLI access?

CLI tools (e.g., `osac login`) support the Device Authorization Flow:

1. CLI displays a URL and a one-time code
2. User opens the URL in a browser and enters the code
3. Browser redirects to the tenant's IdP for authentication
4. CLI receives a token after the browser flow completes

The user's password is entered in the browser and sent only to the
tenant's IdP — the CLI and the OSAC platform never see it.

---

## References

- [Keycloak Organizations — Managing IdPs](https://docs.redhat.com/en/documentation/red_hat_build_of_keycloak/26.6/html/server_administration_guide/managing_organizations)
- [Keycloak User Storage Federation](https://docs.redhat.com/en/documentation/red_hat_build_of_keycloak/26.6/html/server_administration_guide/user-storage-federation)
- [Keycloak OIDC Identity Brokering](https://www.keycloak.org/docs/latest/server_admin/#_identity_broker_oidc)
- Internal: LDAP + Keycloak Setup Guide (engineering reference, not published in this repo)
- Internal: Multi-Tenant LDAP Isolation Research (not published in this repo)
