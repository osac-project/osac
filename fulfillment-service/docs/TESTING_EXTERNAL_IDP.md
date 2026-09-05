# Testing External Identity Provider Login

## Overview

The integration tests in `it/it_identity_providers_login_test.go` verify the end-to-end
external IdP login flow for OSAC. The test philosophy is:

> Test exactly what a user does: register an IdP through OSAC, log in through the OIDC
> redirect chain (the same path as `osac login`), and assert access via OSAC API calls.

No direct Keycloak admin API calls are used for authentication. Keycloak admin calls are
limited to test scaffolding (user provisioning), which is analogous to how other tests
create tenants or hubs via the private API.

---

## Architecture

```
Test runner (host)
│
│  /etc/hosts: 127.0.0.1 keycloak.keycloak.svc.cluster.local
│              127.0.0.1 fulfillment-api.osac.svc.cluster.local
│              ...
│
│  mockoidc server (bound to 0.0.0.0:<random-port>)
│  ├─ 127.0.0.1:<port>       ← test runner follows OIDC redirects here
│  └─ <bridge-ip>:<port>     ← Keycloak inside Kind exchanges codes here
│
↓ 127.0.0.1:8443 (Kind NodePort → Envoy Gateway)
┌────────────────────────────────────────────────────────────┐
│  Kind cluster                                              │
│                                                            │
│  Keycloak (keycloak ns)                                    │
│  └─ IdP registered via OSAC controller                    │
│     authorizationUrl → 127.0.0.1:<port> (test runner)     │
│     tokenUrl         → <bridge-ip>:<port> (in-cluster)    │
│                                                            │
│  fulfillment-service (osac ns)                             │
│  └─ IdentityProvider controller → reconciles IdP to KC    │
│                                                            │
│  OSAC public API (osac ns)                                 │
│  └─ asserts token accepted, tenant scoped correctly        │
└────────────────────────────────────────────────────────────┘
```

### OIDC Redirect Chain (SimulateOIDCLogin)

```
1. Test runner  → KC /auth?kc_idp_hint=<alias>&code_challenge=...   (PKCE S256)
2.              ← 302 → mockoidc /oidc/authorize
3. Test runner  → mockoidc /authorize   (pre-queued user auto-approved)
4.              ← 302 → KC /broker/<alias>/endpoint?code=<mock-code>
5. Test runner  → KC broker callback
6. KC (in-Kind) → POST mockoidc /oidc/token  (<bridge-ip>:<port>)
7. KC (in-Kind) → GET  mockoidc /oidc/.well-known/jwks.json
8.              ← 302 → http://localhost?code=<kc-code>
9. Test runner intercepts redirect, extracts kc-code
10. Test runner → POST KC /token (authorization_code grant + PKCE verifier)
11.             ← KC JWT access_token
12. Test runner → OSAC public API with JWT → assert correct tenant scoping
```

---

## Test Suite

All 5 specs share a `BeforeEach` that:

1. Creates a fresh OSAC tenant
2. Starts a `mockoidc` server (bound to `0.0.0.0:0`)
3. **Registers the IdP via the OSAC private API** → waits up to 2 minutes for the
   `IdentityProvider` controller to reconcile it to Keycloak (`READY` phase)

### Spec 1 — Allows an IdP-linked user to authenticate and obtain a token

Provisions a user, runs `SimulateOIDCLogin`, asserts a non-empty KC JWT is returned.

### Spec 2 — Scopes IdP user access to their tenant

Authenticates, then calls `Capabilities.Get` on the OSAC public API to confirm the token
is accepted and tenant-scoped correctly.

### Spec 3 — Denies an IdP user access to resources in a different tenant

Authenticates as a user in tenant A, attempts to create an IdP in tenant B. Expects
`PermissionDenied` or `Unauthenticated`.

### Spec 4 — Rejects login via an unregistered IdP alias

`SimulateOIDCLogin` with a random alias that doesn't exist in KC. Expects an error.

### Spec 5 — Verifies OSAC controller status and alias reported

Reads back the OSAC `IdentityProvider` object, asserts `phase == READY` and the status
message contains the KC alias (`<tenantName>-<idpName>`). Then authenticates to confirm
the reconciled IdP is functional.

---

## Key Helpers

### `StartMockOIDC() (*MockOIDCState, error)`

Starts an in-process mock OIDC server on all interfaces. Returns:

- `LocalAuthURL()` — `http://127.0.0.1:<port>/oidc/authorize` (test runner follows redirects here)
- `ClusterTokenURL()` — `http://<bridge-ip>:<port>/oidc/token` (Keycloak exchanges codes here)
- `LocalIssuer()` — the `iss` claim value mockoidc puts in every token it issues

### `MockOIDCState.QueueUser(subject, email, username)`

Enqueues a user for the next authorization request. mockoidc auto-approves the login and
returns this user's claims. **Must be called before `SimulateOIDCLogin`** so the
subject matches the federated identity link set up in Keycloak.

### `ProvisionOIDCUser(ctx, username, email, tenantName, idpAlias, externalSubject)`

Test scaffolding — creates a Keycloak user with:
1. A password (via `PUT /users/{id}/reset-password`)
2. A federated identity link to the IdP (`externalSubject` = the mock user's `Subject`)
3. KC organization membership (for the `organization` claim in the JWT)

Keycloak's first-broker-login flow matches returning users by their external subject.
Pre-creating the link prevents KC from interrupting the OIDC flow to prompt the user to
review their profile.

### `SimulateOIDCLogin(ctx, idpAlias) (token, error)`

Drives the full OIDC authorization code redirect chain with PKCE (RFC 7636). Returns a
Keycloak JWT access token. The redirect chain mirrors what `osac login` does.

### `MakeOIDCGRPCConn(ctx, jwtToken) (*grpc.ClientConn, error)`

Wraps a raw JWT into a gRPC connection to the OSAC external API for asserting access.

---

## Running the Tests

### Prerequisites

1. Kind cluster running with OSAC deployed:
   ```bash
   cd osac/fulfillment-service
   make -C ../osac-installer install-infra PLATFORM=kind PROFILE=dev NS=osac
   make -C ../osac-installer install-osac  PLATFORM=kind PROFILE=dev NS=osac
   ```

2. `/etc/hosts` entries (set once when the dev cluster is created):
   ```
   127.0.0.1  keycloak.keycloak.svc.cluster.local
   127.0.0.1  fulfillment-api.osac.svc.cluster.local
   127.0.0.1  fulfillment-internal-api.osac.svc.cluster.local
   ```

### Run

```bash
cd osac/fulfillment-service

# All IdP login specs (each BeforeEach waits up to 2 minutes for controller reconciliation)
ginkgo run -v --focus="Identity provider login flow" it

# Single spec
ginkgo run -v --focus="Scopes IdP user access to their tenant" it
```

---

## If SimulateOIDCLogin Fails (Networking)

`SimulateOIDCLogin` requires Keycloak (inside Kind) to reach the mockoidc server on the
host via the Podman bridge IP for the code exchange (step 6 above). If this fails with
a 502 or connection error, the test will report something like:

```
redirect chain stopped without KC code — redirects:
redirect #1 → http://localhost?error=...
```

**Diagnosis:**

```bash
# Find the bridge IP Keycloak would use
podman network inspect kind | python3 -c \
  "import sys,json; nets=json.load(sys.stdin); \
   print([s['gateway'] for n in nets for s in n.get('subnets',[])])"

# Check if Keycloak can reach the host at that IP (run from inside KC pod)
kubectl exec -n keycloak deployment/keycloak-service -- \
  curl -sf http://<bridge-ip>:<mockoidc-port>/oidc/.well-known/openid-configuration
```

**If Keycloak cannot reach the host (Podman network isolation):**

The long-term fix is to deploy Dex (or another OIDC provider) as a pod inside the Kind
cluster so Keycloak can reach it via cluster DNS. This eliminates the host-to-cluster
networking requirement entirely. See the [Dex deployment plan](../docs/DEX_IDP_PLAN.md)
for the implementation roadmap.

---

## Adding New Tests

1. Add a new `It` block in `it_identity_providers_login_test.go`
2. Use `provisionAndLogin(ctx, tenantName, idpAlias)` for authentication
3. Assert using OSAC public API clients (`publicv1.New*Client(conn)`)
4. Never authenticate via Keycloak admin endpoints — use `SimulateOIDCLogin`
