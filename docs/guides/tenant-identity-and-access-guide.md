# Tenant Identity and Access Guide (Fulfillment API / CLI)

**Last Updated**: 2026-10-26
**Audience**: Cloud provider administrators, tenant administrators, developers validating fulfillment-service
**Status**: Draft

## Contents

- [Overview](#overview)
- [Why This Flow Matters](#why-this-flow-matters)
- [Concepts](#concepts)
- [Checking the current user with `osac whoami`](#checking-the-current-user-with-osac-whoami)
- [Flow map](#flow-map)
- [Prerequisites](#prerequisites)
- [Flow 0 — Session exports](#flow-0--session-exports)
- [Flow 1 — Cloud provider admin creates tenant](#flow-1--cloud-provider-admin-creates-tenant)
- [Flow 2 — Break-glass created automatically](#flow-2--break-glass-created-automatically)
- [Flow 3 — Tenant onboarding automatic](#flow-3--tenant-onboarding-automatic)
- [Flow 4 — Configure IdP (OIDC)](#flow-4--configure-idp-oidc)
- [Flow 5 — IdP users auto-join the tenant](#flow-5--idp-users-auto-join-the-tenant)
- [Flow 6 — RoleBinding tenant-admin](#flow-6--rolebinding-tenant-admin)
- [Read-only API access](#read-only-api-access)
- [Flow 7 — Tenant admin SSO proof](#flow-7--tenant-admin-sso-proof)
- [Flow 8 — Tenant admin creates a project](#flow-8--tenant-admin-creates-a-project)
- [Flow 9 — Add a project viewer](#flow-9--add-a-project-viewer)
- [Flow 10 — Viewer IdP login again](#flow-10--viewer-idp-login-again)
- [Flow 11 — Viewer lists project, cannot delete](#flow-11--viewer-lists-project-cannot-delete)
- [Flow 12 — Delete the project](#flow-12--delete-the-project)
- [Troubleshooting](#troubleshooting)
- [Frequently Asked Questions](#frequently-asked-questions)

---

## Overview

This guide shows tenant identity and access on OSAC using the `osac` CLI end to end:
create a tenant, attach an IdP, grant roles, and prove project RBAC.

A **cloud provider administrator** creates the tenant. OSAC then sets up the basics for
that tenant automatically (break-glass account, Keycloak organization, and Kubernetes
namespace). Next you attach the tenant’s own login system (OIDC IdP), promote a user to
`tenant-admin`, create a project, add a viewer, and prove who can and cannot delete the
project.

All steps use the fulfillment API through the CLI — private API for platform admin
actions, public API for tenant users after IdP login.

This guide covers:

1. Create a tenant and confirm automatic onboarding (break-glass, org, namespace)
2. Register a tenant-scoped OIDC identity provider
3. Prove IdP users auto-join the tenant and receive OSAC `User` records via just-in-time provisioning (JIT)
4. Grant `tenant-admin` via RoleBinding and create a project
5. Grant a project viewer via ProjectMembership and prove RBAC
6. Delete the demo project (memberships are removed with it)

---

## Why This Flow Matters

Multi-tenant OSAC keeps **platform control** separate from **tenant identity**.

| Topic | Why it matters |
|-------|----------------|
| **Tenant isolation** | Each tenant gets its own Keycloak organization and usually its own OIDC IdP. Users sign in at the tenant IdP; their passwords stay there. |
| **Break-glass** | Creating a tenant also creates a temporary break-glass account. A tenant admin can recover access without receiving the platform admin token. |
| **Least privilege** | `tenant-admin` is granted with an OSAC RoleBinding. Project access uses ProjectMembership (for example VIEWER), so a user can list a project without being able to delete it. |

### What happens at login (device flow)

After the IdP is registered and READY:

1. User runs `osac login --flow device` against the **public** API.
2. The CLI shows a URL and a one-time code; the user opens that URL in a browser.
3. OSAC Keycloak shows a button for the tenant IdP (alias such as `osac-corp-oidc`).
4. The user signs in at the **tenant IdP** (OSAC never sees the password).
5. The browser returns to OSAC; the CLI stores a JWT for that tenant.
6. The first **real API call** (for example `osac get projects`) creates the OSAC `User` row. `osac whoami` alone does not.

---

## Concepts

### Personas used in this guide

| Role | Example user | How they authenticate |
|------|--------------|------------------------|
| Cloud provider admin | Platform SA `admin` | Private API: `osac login --private --token-script "oc create token …"` |
| Break-glass | `{tenant}-osac-break-glass` | Password printed **once** on tenant create |
| Tenant admin | `alice` | Public API: device login → tenant IdP (`--flow device`) |
| Tenant user | `bob` | Public API: device login → tenant IdP |

### Private vs public API

- **Private API** (`fulfillment-internal-api…`): platform admin operations such as creating
  tenants and (in many labs) registering identity providers.
- **Public API** (`fulfillment-api…`): tenant-scoped operations after IdP login
  (projects, memberships, day-2 RBAC).

### Checking the current user with `osac whoami`

After any login, run:

```bash
osac whoami
```

This prints the identity from the **local CLI token** (username, tenant, roles). Use it in later flows to confirm you are logged in as alice or bob before create/delete commands. For how that relates to OSAC `User` records, see the FAQ below and Flow 5.

---

## Flow map

| Flow | Claim |
|------|--------|
| 0 | Export session variables for your environment |
| 1 | Cloud provider admin creates the tenant |
| 2 | Break-glass account exists (password already shown in Flow 1) |
| 3 | Automatic onboarding: Keycloak org + tenant namespace |
| 4 | Register tenant OIDC identity provider |
| 5 | Two IdP users auto-join (`alice`, `bob`) and JIT OSAC Users |
| 6 | RoleBinding grants `tenant-admin` to alice |
| Reader access | Assign a reader role and review API permissions |
| 7 | Alice logs in again — SSO proves `tenant-admin` in the token |
| 8 | Alice creates a project |
| 9 | Alice adds bob as project viewer (ProjectMembership) |
| 10 | Bob logs in again via IdP |
| 11 | Bob can list the project but cannot delete it |
| 12 | Alice deletes the project |

---

## Prerequisites

Before starting, you need:

| Requirement | Description |
|-------------|-------------|
| Management cluster | OSAC fulfillment-service and Keycloak running |
| `oc` access | Ability to mint a platform admin token (`oc create token -n ${NS} admin`) |
| `osac` CLI | Installed and configured for your cluster API endpoint |
| `curl` / `jq` | Used only to verify Keycloak organization side effects |
| Tenant IdP | OIDC-capable IdP (or a lab mock) with a confidential client for OSAC’s broker redirect URI |

`--insecure` and `curl -sk` are lab-only (self-signed certs); in production, omit them and trust your cluster CA.

---

## Flow 0 — Session exports

**What this flow does:** Sets the environment variables used by every later step.
Replace the example values with your cluster’s namespace, domain, and ingress IP.

```bash
export PATH="$HOME/bin:$HOME/.local/bin:$PATH"
export KUBECONFIG=/path/to/kubeconfig
export NS=osac-devel                    # operator / fulfillment namespace
export DOMAIN=apps.example.com          # cluster apps domain
export INGRESS_IP=192.0.2.10            # ingress IP for --resolve curls (labs)
export KEYCLOAK_NS=keycloak
export REALM=osac
export TENANT=osac-corp
export TENANT_ADMIN=alice
export TENANT_USER=bob
export OSAC="https://fulfillment-internal-api-${NS}.${DOMAIN}"
export OSAC_PUBLIC="https://fulfillment-api-${NS}.${DOMAIN}"
# KC is set from the Keycloak route when you run org verification curls (same host as --resolve)
mkdir -p /tmp/osac-demo
echo "Flow 0 OK: TENANT=$TENANT ADMIN=$TENANT_ADMIN USER=$TENANT_USER"
```

**Expected output:**

```text
Flow 0 OK: TENANT=osac-corp ADMIN=alice USER=bob
```

> Adjust URL patterns if your routes differ (some labs use
> `fulfillment-internal-api-<ns>.apps.<base>`). Confirm with `oc get route -n ${NS}`.

---

## Flow 1 — Cloud provider admin creates tenant

**What this flow does:** The platform admin creates a Tenant on the private API.
On success, fulfillment creates a break-glass user and prints its password **once**.
Save that password immediately — it is not available from later `get` calls.

```bash
osac login --address "$OSAC" --insecure --private \
  --token-script "oc create token -n ${NS} admin"

cat > /tmp/osac-demo/tenant.yaml <<EOF
"@type": type.googleapis.com/osac.private.v1.Tenant
metadata:
  name: ${TENANT}
EOF

# Break-glass password prints once — copy it now; do not save to a file
osac create -f /tmp/osac-demo/tenant.yaml
osac get tenants
```

**Expected output (create):**

```text
Created tenant with name 'osac-corp' and identifier 'osac-corp'.

Break-glass account credentials (shown only once, save them now):
  Username: osac-corp-osac-break-glass
  Password: <generated>

This is a temporary password that must be changed on first login.
```

**Expected output (`osac get tenants`):**

```text
PROJECT  DELETING  ID         NAME       STATE
-        -         osac-corp  osac-corp  SYNCED
-        -         shared     shared     SYNCED
-        -         system     system     SYNCED
```

---

## Flow 2 — Break-glass created automatically

**What this flow does:** Confirms the tenant is `SYNCED` and that a break-glass user id
exists. The password is intentionally **not** stored on the tenant object after create
(it was shown in Flow 1 only).

```bash
osac login --address "$OSAC" --insecure --private \
  --token-script "oc create token -n ${NS} admin"

osac get tenant "${TENANT}" -o yaml | grep -E 'state:|break_glass_user_id|idp_tenant_name'
```

**Expected output:**

```text
  break_glass_user_id: <uuid>
  idp_tenant_name: osac-corp
  state: TENANT_STATE_SYNCED
```

Username is always `{tenant}-osac-break-glass` (here `osac-corp-osac-break-glass`).
Do **not** expect `break_glass_credentials.password` on `get`.

---

## Flow 3 — Tenant onboarding automatic

**What this flow does:** After a tenant reaches `SYNCED`, OSAC finishes onboarding for you. You do not create these by hand — the platform creates both of the following, and this flow only **checks** that they exist.

- **Kubernetes namespace** — same name as the tenant; where tenant workloads live
- **Keycloak organization** — same name as the tenant; the identity boundary users join when they log in through the tenant IdP

### Confirm the tenant namespace

```bash
oc get ns "${TENANT}"
```

**Expected output:**

```text
NAME        STATUS   AGE
osac-corp   Active   ...
```

### Confirm the Keycloak organization

Use your environment’s Keycloak **master** admin credentials (placeholders below — do
not commit real passwords):

```bash
export KC_ADMIN_USER="<keycloak-master-admin-user>"
export KC_ADMIN_PASSWORD="<keycloak-master-admin-password>"

KEYCLOAK_ROUTE=$(oc get route keycloak -n keycloak -o jsonpath='{.spec.host}')
export KC="https://${KEYCLOAK_ROUTE}"
KC_TOKEN=$(curl -sk --resolve "${KEYCLOAK_ROUTE}:443:${INGRESS_IP}" \
  -X POST "${KC}/realms/master/protocol/openid-connect/token" \
  -d "grant_type=password&client_id=admin-cli&username=${KC_ADMIN_USER}&password=${KC_ADMIN_PASSWORD}" \
  | jq -r '.access_token')

curl -sk --resolve "${KEYCLOAK_ROUTE}:443:${INGRESS_IP}" \
  -H "Authorization: Bearer ${KC_TOKEN}" \
  "${KC}/admin/realms/${REALM}/organizations?search=${TENANT}" \
  | jq '[.[] | {id, name, enabled}]'
```

**Expected output:**

```text
[
  {
    "id": "<uuid>",
    "name": "osac-corp",
    "enabled": true
  }
]
```

> If DNS already resolves the Keycloak route, you can omit `--resolve`.

---

## Flow 4 — Configure IdP (OIDC)

**What this flow does:** Connect your tenant to a company login system (Okta, Entra ID,
Keycloak, and so on). Users type their password at **that** system. OSAC never sees the
password.

### Login in plain words

1. User starts login in OSAC.
2. OSAC sends the user to the company IdP to enter the password.
3. After success, the IdP must send the user **back to OSAC**.

That “send me back to OSAC” address is the **redirect URI** (return address):

```text
https://<keycloak-host>/realms/osac/broker/<idp-alias>/endpoint
```

**Put this URL in the company IdP** when you create the OIDC app/client — in the field
named Redirect URI, Callback URL, or Sign-in redirect URI.

**Do not put this URL in the OSAC YAML below.** The YAML only tells OSAC how to reach
the IdP (issuer, client id, client secret). The redirect URI tells the IdP how to return
to OSAC.

If the redirect URI is missing or wrong in the IdP, login fails after the password step.

### Steps

1. In your company IdP, create an OIDC client (with client id and client secret).
2. Set that client’s redirect URI to the URL above (use your Keycloak host and alias,
   for example `osac-corp-oidc`).
3. Copy issuer, authorization URL, token URL, client id, and client secret from the IdP.
4. Register those values in OSAC with the commands below.

### Create the IdentityProvider in OSAC

```bash
osac login --address "$OSAC" --insecure --private \
  --token-script "oc create token -n ${NS} admin"

# Values from your IdP client
export OIDC_ISSUER="https://idp.example.com/realms/osac-corp"
export OIDC_AUTHORIZATION_URL="${OIDC_ISSUER}/protocol/openid-connect/auth"
export OIDC_TOKEN_URL="${OIDC_ISSUER}/protocol/openid-connect/token"
export OIDC_CLIENT_ID="<client-id>"
export OIDC_CLIENT_SECRET="<client-secret>"

cat > /tmp/osac-demo/idp.yaml <<EOF
"@type": type.googleapis.com/osac.private.v1.IdentityProvider
metadata:
  name: oidc
  tenant: ${TENANT}
spec:
  title: "OSAC Corp OIDC"
  enabled: true
  oidc:
    authorizationUrl: "${OIDC_AUTHORIZATION_URL}"
    tokenUrl: "${OIDC_TOKEN_URL}"
    clientId: "${OIDC_CLIENT_ID}"
    clientSecret: "${OIDC_CLIENT_SECRET}"
    issuer: "${OIDC_ISSUER}"
EOF

osac create -f /tmp/osac-demo/idp.yaml
osac get identityprovider oidc --output yaml | grep -E 'phase:|message:|client_id:|alias:'
```

**Expected output (ready IdP):**

```text
  message: 'Identity provider created successfully with alias: osac-corp-oidc'
  phase: IDENTITY_PROVIDER_PHASE_READY
```

The **alias** (for example `osac-corp-oidc`) must match the `<idp-alias>` in the
redirect URI you set on the IdP. That alias is the login button in Flows 5, 7, 10, and 12.

### Lab note (mock IdP)

Demo labs often use a mock OIDC server. With a mock, you usually only register issuer /
client id / secret in OSAC (and may patch the broker issuer). You typically do **not**
paste the redirect URI into a client UI the way you must for Okta, Entra, or another real
IdP.

If browser login fails with “Unexpected error when authenticating with identity
provider”, fix the issuer mismatch between the mock IdP and the Keycloak broker, then
try again.

---

## Flow 5 — IdP users auto-join the tenant

**What this flow does:** Two company users authenticate through the tenant IdP.
Successful login adds them to the Keycloak organization (“auto-join tenant”).
A first real API call (`osac get projects`) JIT-creates each OSAC `User` row needed
later for ProjectMembership.

Use a **fresh Incognito/private window** for each device login. Click the tenant IdP
alias (for example `osac-corp-oidc`). Do not use “Add to existing account”.

### 5a — Alice (tenant admin candidate)

```bash
osac logout 2>/dev/null || true
osac login --insecure --address "${OSAC_PUBLIC}" --flow device
# Browser: Incognito → <tenant-idp-alias> → username alice

osac whoami
osac get projects   # REQUIRED: first real API call → JIT OSAC User
```

**Expected output:**

```text
Authentication succeeded.
Logged in as: alice
Tenant: osac-corp
```

Optional — confirm alice joined the Keycloak organization (reuse `KC_ADMIN_USER` /
`KC_ADMIN_PASSWORD` from Flow 3):

```bash
KEYCLOAK_ROUTE=$(oc get route keycloak -n keycloak -o jsonpath='{.spec.host}')
export KC="https://${KEYCLOAK_ROUTE}"
KC_TOKEN=$(curl -sk --resolve "${KEYCLOAK_ROUTE}:443:${INGRESS_IP}" \
  -X POST "${KC}/realms/master/protocol/openid-connect/token" \
  -d "grant_type=password&client_id=admin-cli&username=${KC_ADMIN_USER}&password=${KC_ADMIN_PASSWORD}" \
  | jq -r '.access_token')
ORG_ID=$(curl -sk --resolve "${KEYCLOAK_ROUTE}:443:${INGRESS_IP}" \
  -H "Authorization: Bearer ${KC_TOKEN}" \
  "${KC}/admin/realms/${REALM}/organizations?search=${TENANT}" \
  | jq -r --arg t "$TENANT" '.[] | select(.name==$t) | .id')
curl -sk --resolve "${KEYCLOAK_ROUTE}:443:${INGRESS_IP}" \
  -H "Authorization: Bearer ${KC_TOKEN}" \
  "${KC}/admin/realms/${REALM}/organizations/${ORG_ID}/members" \
  | jq '[.[].username]'
# expect alice and the break-glass user
```

`osac get users` as alice **before Flow 6** returns `PermissionDenied` (not tenant-admin yet).

### 5b — Bob (viewer candidate)

```bash
osac logout 2>/dev/null || true
osac login --insecure --address "${OSAC_PUBLIC}" --flow device
# Browser: NEW Incognito → <tenant-idp-alias> → username bob

osac whoami
# REQUIRED for Flow 9: JIT OSAC User (whoami alone is not enough)
osac get projects
# Optional: re-run the org members curl from 5a — expect alice, bob, break-glass
```

**Expected output:**

```text
Logged in as: bob
Tenant: osac-corp
```

---

## Flow 6 — RoleBinding tenant-admin

**What this flow does:** Platform admin grants alice the OSAC `tenant-admin` role via
RoleBinding. This is the product path for tenant administration privileges (not a
Keycloak password or realm-role shortcut).

```bash
osac logout 2>/dev/null || true
osac login --address "$OSAC" --insecure --private \
  --token-script "oc create token -n ${NS} admin"

osac get users
# Columns vary by build. Prefer matching on username/name == alice and taking the UUID column.
ALICE_ID=$(osac get users | awk '$3=="alice" {print $1; exit}')
echo "ALICE_ID=$ALICE_ID"
# Must be a UUID. If empty, inspect the table headers and adjust the awk field.

cat > /tmp/osac-demo/rolebinding.yaml <<EOF
"@type": type.googleapis.com/osac.public.v1.RoleBinding
metadata:
  name: tenant-admin-binding
  tenant: ${TENANT}
spec:
  role: tenant-admin
  users:
    - ${ALICE_ID}
EOF

osac create -f /tmp/osac-demo/rolebinding.yaml
osac get rolebinding -o yaml | grep -E 'name:|role:|state:|message:'
```

**Expected:** RoleBinding **READY**, role `tenant-admin`, users = alice’s UUID.

---

## Read-only API access

OSAC provides the `tenant-reader` and `cloud-provider-reader` roles for
authorized users who need to inspect resources without changing them. A Tenant
Admin or Cloud Provider Admin assigns either role through the existing
RoleBindings API; no new API or role-seeding step is required.

### Assign a reader role through the API

Use the RoleBinding request and create/get workflow shown in [Flow 6](#flow-6--rolebinding-tenant-admin).
For the reader assignment, set `spec.role` to `tenant-reader` or
`cloud-provider-reader`, and set `spec.users` to the assigned OSAC user IDs.
The RoleBinding's status reports synchronization progress. `READY` means the
role has been synchronized to Keycloak.

### What a reader role allows

A reader role allows only reviewed public resource methods named exactly
`Get`, `List`, or `Watch`. This is an explicit set of reviewed API methods, not
a grant to every method whose name happens to match one of those verbs. The
user's existing authorization checks still apply: `tenant-reader` stays
within the user's existing tenant scope, and `cloud-provider-reader` stays
within the user's existing provider scope. Neither role adds visibility to
other tenants or providers.

`SelfSubjectAccessReviews/Create` is a separate permission-check operation. An
authenticated user can use it to ask whether they themselves would be allowed
to perform a hypothetical operation. Despite `Create` in the method name, the
request does not run or store the checked operation and does not grant that
permission.

A user with only a reader role is denied direct resource `Create`, `Update`,
and `Delete` requests with `PERMISSION_DENIED`; denied requests do not change
persisted resources. A reader role also does not grant the implicit
general-client fallback. Other explicitly assigned roles or admin grants
remain additive, so they may allow operations beyond the reader role's grants.

### When a reader-role assignment takes effect

Wait for the RoleBinding to reach `READY`, then refresh the user's token before
checking the role claims. `READY` confirms Keycloak synchronization; it does
not change claims in a token that was issued earlier. The existing session can
continue to use its original claims until the token is refreshed.

---

## Flow 7 — Tenant admin SSO proof

**What this flow does:** Alice logs in again through the IdP. The new token should
include `tenant-admin`, proving SSO + RoleBinding without any local password. Alice
can also list users (including bob).

```bash
osac logout 2>/dev/null || true
osac login --insecure --address "${OSAC_PUBLIC}" --flow device
# Browser: Incognito → <tenant-idp-alias> → alice

osac whoami
osac get users
```

**Expected output:**

```text
Logged in as: alice
Tenant: osac-corp
Roles: tenant-admin, offline_access, uma_authorization, default-roles-my realm
ID  ...  NAME   USERNAME  ...  TENANT
...      bob    bob       ...  osac-corp
...      alice  alice     ...  osac-corp
```

If bob is missing from `osac get users`, he never hit the API in Flow 5b — re-login as
bob and run `osac get projects`.

---

## Flow 8 — Tenant admin creates a project

**What this flow does:** Alice creates a named project under the tenant. A nameless
tenant-root project with `STATE=UNSPECIFIED` may already exist; that is normal and
should be left alone.

```bash
osac whoami   # still alice / tenant-admin from Flow 7

cat > /tmp/osac-demo/project.yaml <<EOF
"@type": type.googleapis.com/osac.public.v1.Project
metadata:
  name: default
  tenant: ${TENANT}
spec:
  title: Default Project
  description: Primary project for ${TENANT}
EOF

osac create -f /tmp/osac-demo/project.yaml | tee /tmp/osac-demo/create-project.out
osac get projects

PROJ_ID=$(grep -oE "identifier '[^']+'" /tmp/osac-demo/create-project.out \
  | head -1 | tr -d "'" | awk '{print $2}')
echo "export PROJ_ID=${PROJ_ID}" | tee /tmp/osac-demo/proj-id.sh
echo "PROJ_ID=${PROJ_ID}"
```

**Expected output (example):**

```text
Created project with name 'default' and identifier '<uuid>'.
TENANT     DELETING  PROJECT  NAME     TITLE            STATE
shared     -         -        -        -                ACTIVE
osac-corp  -         -        -        -                UNSPECIFIED
osac-corp  -         -        default  Default Project  ACTIVE
```

The **UNSPECIFIED** row with empty NAME is the system-created tenant root project.
Flow 8’s project is the `default` row.

---

## Flow 9 — Add a project viewer

**What this flow does:** Alice grants bob `VIEWER` on the `default` project using
ProjectMembership. Schema uses `metadata.project` (name) and `spec.users` (list of
OSAC user UUIDs) — not `spec.project` / `spec.user`.

```bash
osac whoami
osac get users
BOB_OSAC_USER_ID=$(osac get users | awk '$3=="bob" {print $1; exit}')
export BOB_OSAC_USER_ID
echo "BOB_OSAC_USER_ID=$BOB_OSAC_USER_ID"
# Must be a UUID — wrong column → FAILED membership

cat > /tmp/osac-demo/bob-membership.yaml <<EOF
"@type": type.googleapis.com/osac.public.v1.ProjectMembership
metadata:
  tenant: ${TENANT}
  project: default
spec:
  role: PROJECT_MEMBERSHIP_ROLE_VIEWER
  users:
    - ${BOB_OSAC_USER_ID}
EOF

osac create -f /tmp/osac-demo/bob-membership.yaml
osac get projectmemberships
```

**Expected output:**

```text
Created projectmembership with identifier '<uuid>'.
TENANT     ...  STATE  PROJECT  USERS     ROLE
osac-corp  ...  READY  default  <bob-id>  VIEWER
```

If create returns `PermissionDenied`, your fulfillment image may predate ProjectMembership
support for tenant-admins. Upgrade fulfillment-service and retry.

---

## Flow 10 — Viewer IdP login again

**What this flow does:** Bob authenticates again through the tenant IdP so subsequent
RBAC checks use a fresh viewer session (no password shortcut).

```bash
osac logout 2>/dev/null || true
osac login --insecure --address "${OSAC_PUBLIC}" --flow device
# Browser: Incognito → <tenant-idp-alias> → bob

osac whoami
```

**Expected output:**

```text
Logged in as: bob
Tenant: osac-corp
Roles: offline_access, uma_authorization, default-roles-my realm
```

Bob should **not** have `tenant-admin`.

---

## Flow 11 — Viewer lists project, cannot delete

**What this flow does:** Proves viewer permissions: bob can see the project but delete
is denied (`PermissionDenied`).

```bash
source /tmp/osac-demo/proj-id.sh
osac whoami
osac get projects

# Expect PermissionDenied — fail the check if delete succeeds or returns another error
set +e
out=$(osac delete project "${PROJ_ID}" 2>&1)
rc=$?
set -e
echo "$out"
if [[ $rc -eq 0 ]]; then
  echo "RBAC check failed: viewer was able to delete the project" >&2
  exit 1
fi
echo "$out" | grep -q PermissionDenied \
  || { echo "RBAC check failed: expected PermissionDenied, got: $out" >&2; exit 1; }
echo "RBAC check OK: viewer cannot delete project"
```

**Expected output:**

```text
TENANT     DELETING  PROJECT  NAME     TITLE            STATE
shared     -         -        -        -                ACTIVE
osac-corp  -         -        -        -                UNSPECIFIED
osac-corp  -         -        default  Default Project  ACTIVE
Failed to delete project '<PROJ_ID>': rpc error: code = PermissionDenied desc = permission denied
RBAC check OK: viewer cannot delete project
```

(`osac get users` as bob → `PermissionDenied` is also normal.)

---

## Flow 12 — Delete the project

**What this flow does:** Alice returns via IdP and deletes the demo project.
ProjectMemberships are removed automatically when the project is deleted. Delete can
still fail if other resources remain in the project.

```bash
source /tmp/osac-demo/proj-id.sh

osac logout 2>/dev/null || true
osac login --insecure --address "${OSAC_PUBLIC}" --flow device
# Browser: Incognito → <tenant-idp-alias> → alice

osac whoami
osac delete project "${PROJ_ID}"
osac get projects
```

**Expected output:**

```text
Logged in as: alice
Roles: tenant-admin, ...
Deleted project '<PROJ_ID>'.
TENANT     DELETING  PROJECT  NAME  TITLE  STATE
shared     -         -        -     -      ACTIVE
osac-corp  -         -        -     -      UNSPECIFIED   # root remains — OK
```

If the project remains `DELETING=Yes`, clear finalizers as a last resort (lab
workaround):

```bash
# Only if stuck
osac edit project "${PROJ_ID}"
# remove metadata.finalizers, save
```

**Caveat:** Delete is validated for the **project creator** who is also `tenant-admin`.
`tenant-admin` alone (non-creator / not in managers) is not covered by this guide.

---

## Troubleshooting

| Symptom | Likely cause | What to do |
|---------|--------------|------------|
| IdP browser: “Unexpected error when authenticating with identity provider” | Broker issuer ≠ IdP issuer | Align issuer on the Keycloak broker / IdP registration |
| `osac get users` missing bob after device login | No JIT User | As bob, run a real API call (`osac get projects`) |
| ProjectMembership `PermissionDenied` | Older fulfillment image | Upgrade; product path requires tenant-admin ProjectMembership support |
| ProjectMembership FAILED | Wrong user UUID / column parse | Re-run `osac get users` and confirm UUID for bob |
| Project stuck `DELETING=Yes` | Other resources still in the project, or finalizers | Remove remaining resources; clear finalizers only as a lab last resort |
| `whoami` looks fine but membership fails | `whoami` does not JIT User | Call `osac get projects` (or create) once per user |
| Nameless project `STATE=UNSPECIFIED` | Tenant root auto-created | Expected — not the Flow 8 `default` project |

---

## Frequently Asked Questions

### Is break-glass the same as tenant-admin?

No. Break-glass is a temporary recovery account created with the tenant. `tenant-admin`
is an OSAC RoleBinding granted to a normal IdP user (alice in this guide). Prefer IdP
users + RoleBinding for day-to-day administration.

### Why doesn’t `osac whoami` make my user show up in `osac get users`?

`osac whoami` only reads the local JWT; it does not call the API, so it does not
create an OSAC `User`. After login, run a real API call once (for example
`osac get projects`). That JIT-creates the User needed for `osac get users` and
ProjectMembership.

### Why can bob see the project but not delete it?

Bob is a ProjectMembership **VIEWER**. Listing is allowed; delete requires a higher
role (and in this guide, the creator / tenant-admin path shown in Flow 12).

### Why can project delete fail even for tenant-admin?

ProjectMemberships are removed with the project. Delete can still fail if other
resources remain in the project, or if you are not the creator path this guide covers.

### Do I need the OSAC UI for this guide?

No. All steps are CLI / API. Device login opens a browser only for the IdP authentication
step.
