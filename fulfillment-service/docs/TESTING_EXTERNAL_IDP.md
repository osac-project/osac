# Testing External Identity Provider Login

This document covers how the integration tests exercise the full OIDC authorization code flow
for external identity providers (IdPs) configured in OSAC.

## Overview

OSAC lets platform administrators configure external OIDC identity providers per tenant.
When an IdP is created via the OSAC API the fulfillment-service controller reconciles it
into Keycloak as an identity brokering provider. End users can then log in via their
organization's IdP and receive a Keycloak-scoped JWT that carries their tenant membership.

The integration tests in `it/it_identity_providers_login_test.go` verify this end-to-end:

```
External IdP user → OIDC redirect chain → Keycloak JWT → OSAC API (tenant-scoped)
```

## Architecture

```
┌─────────────────────────────────────────────┐
│  Test runner (host)                         │
│                                             │
│   mockoidc server        SimulateOIDCLogin  │
│   0.0.0.0:<port>    ◄──  (http.Client with  │
│                          cookie jar +       │
│                          CheckRedirect)     │
└───────────────┬─────────────────────────────┘
                │ bridge IP (172.18.x.1)
┌───────────────▼─────────────────────────────┐
│  Kind cluster                               │
│                                             │
│   Keycloak  ──► token endpoint (bridge IP)  │
│            ──► JWKS endpoint (bridge IP)    │
│                                             │
│   fulfillment-service  ◄── OSAC API calls   │
└─────────────────────────────────────────────┘
```

The mock OIDC server (`github.com/oauth2-proxy/mockoidc`) listens on `0.0.0.0` so that:

- The **test runner** reaches it at `127.0.0.1:<port>` (authorization redirect)
- **Keycloak** (inside Kind) reaches it at `<bridge-ip>:<port>` (token exchange, JWKS)

## Test Suite

Located in `it/it_identity_providers_login_test.go`, these tests require a running Kind cluster.

| Test | What it verifies |
|------|-----------------|
| Allows an IdP user to authenticate | Full OIDC redirect chain returns a KC JWT |
| Scopes IdP user access to their tenant | Token gives access to the correct tenant's resources |
| Denies access to a different tenant | Cross-tenant isolation enforced after IdP login |
| Rejects token from unregistered IdP | Unknown alias → redirect chain fails as expected |
| Reconciles OSAC IdP then allows login | Full path: OSAC API → controller → KC reconcile → login |

## Running the Tests

### Prerequisites

1. The Kind dev cluster must be running:

   ```bash
   make -C ../osac-installer install-infra PLATFORM=kind PROFILE=dev NS=osac
   ```

2. `/etc/hosts` entries must be present (set up by the installer):

   ```
   127.0.0.1  keycloak.keycloak.svc.cluster.local
   127.0.0.1  fulfillment-api.osac.svc.cluster.local
   127.0.0.1  fulfillment-internal-api.osac.svc.cluster.local
   ```

3. Docker must be available so the test can detect the Kind bridge IP via
   `docker network inspect kind`.

### Running only the IdP login tests

```bash
cd fulfillment-service
ginkgo run it --focus="Identity provider login flow"
```

### Running the full integration suite

```bash
cd fulfillment-service
ginkgo run -r
```

### Preserving the cluster on failure (for debugging)

```bash
IT_KEEP_KIND=true ginkgo run it --focus="Identity provider login flow"
```

## How `SimulateOIDCLogin` Works

`SimulateOIDCLogin` in `it/it_idp_login_helpers.go` uses a single `http.Client` with a
cookie jar to drive the entire redirect chain:

1. **GET** KC authorization endpoint with `kc_idp_hint=<alias>` — KC skips its login
   page and redirects straight to the external IdP.
2. **GET** mockoidc `/oidc/authorize` — the queued `MockUser` is popped and an
   authorization code is issued; mockoidc redirects to the KC broker callback.
3. **GET** KC `/broker/<alias>/endpoint?code=<mock-code>` — KC exchanges the code with
   mockoidc's token endpoint (bridge IP), validates the JWKS, creates or links the KC
   user, then redirects to the original `redirect_uri`.
4. `CheckRedirect` intercepts the final redirect to the dummy `redirect_uri` and extracts
   the KC authorization code from the `Location` query string.
5. **POST** KC token endpoint — exchanges the KC code for a KC JWT (`access_token`).

The returned JWT is a normal Keycloak token and can be used directly as a Bearer token
against the OSAC gRPC or REST API.

## Provisioning Test Users

`ProvisionOIDCUser` creates the necessary Keycloak state for a test user:

1. Creates a KC user via the admin API.
2. Links the KC user to the external IdP via a federated identity entry
   (`/users/{id}/federated-identity/{alias}`).
3. Adds the user to the tenant's KC organization so the `organization` claim
   (required by OSAC's OPA policies) appears in the JWT.

Call `MockOIDCState.QueueUser(subject, email, username)` before `SimulateOIDCLogin` to
control which external identity is presented. If the queue is empty, mockoidc uses a
built-in `DefaultUser`.

## Adding New IdP Login Tests

1. Use `BeforeEach` (already done in the suite) to get a fresh `MockOIDCState`, tenant,
   and KC IdP alias.
2. Call `ProvisionOIDCUser` to create the KC user and link them to the IdP.
3. Call `QueueUser` on the `MockOIDCState` to set the external identity that will be
   returned by mockoidc's authorization endpoint.
4. Call `SimulateOIDCLogin` to obtain a KC JWT and optionally `MakeOIDCGRPCConn` to make
   OSAC API calls.

Example:

```go
It("My new IdP login scenario", func() {
    subject := "my-subject-" + uuid.New()
    _, err := tool.ProvisionOIDCUser(ctx,
        "myuser", "myuser@example.com",
        fx.tenantName, fx.idpAlias, subject,
    )
    Expect(err).ToNot(HaveOccurred())

    fx.mockOIDC.QueueUser(subject, "myuser@example.com", "myuser")
    token, err := tool.SimulateOIDCLogin(ctx, fx.idpAlias)
    Expect(err).ToNot(HaveOccurred())

    conn, err := tool.MakeOIDCGRPCConn(ctx, token)
    Expect(err).ToNot(HaveOccurred())
    defer conn.Close()

    // ... make assertions using conn
})
```
