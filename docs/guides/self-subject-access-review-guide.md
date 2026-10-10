# Self-Subject Access Review API

This guide explains how to use the SelfSubjectAccessReview API to check your own permissions before performing operations in OSAC. It covers the request structure, response format, validation rules, and best practices for using permission checks in client applications.

Each example shows the OSAC CLI command (when available), gRPC, and REST API calls so that the guide works for both CLI users and developers integrating with OSAC's API.

## Contents

- [Overview](#overview)
- [Who can use this API](#who-can-use-this-api)
- [When to use permission checks](#when-to-use-permission-checks)
- [Understanding advisory results](#understanding-advisory-results)
- [Request structure](#request-structure)
- [Response structure](#response-structure)
- [Examples](#examples)
  - [Check cluster creation permission](#check-cluster-creation-permission)
- [Error handling](#error-handling)
- [Best practices](#best-practices)

---

## Overview

The SelfSubjectAccessReview API allows authenticated users to check whether they would be authorized to perform a specific operation without actually performing it. This enables:

- **Permission-aware UIs** — Hide or disable actions the user cannot perform
- **Proactive validation** — Check permissions before starting complex workflows
- **Clear feedback** — Show users why certain actions are unavailable

The API follows the Kubernetes SelfSubjectAccessReview pattern: you specify a hypothetical operation (service + method + optional tenant context), and the API returns whether you would be authorized to perform it.

**Important:** Permission check results are **advisory snapshots**, not authoritative guarantees. Authorization state can change between the check and the actual operation (role revoked, policy updated). Always treat results as guidance, not guarantees.

---

## Who can use this API

**Any authenticated user** can call `SelfSubjectAccessReviews.Create` to check their own permissions. No special role or permission is required — the API only reveals information about the caller's own identity and the permissions they already know or can derive.

Users can only check their own permissions (self-subject). There is no API for checking another user's permissions.

---

## When to use permission checks

Permission checks are useful when you need to:

1. **Conditionally display UI elements** — Show "Create Cluster" button only if the user can create clusters
2. **Validate workflows before execution** — Check all required permissions before starting a multi-step process
3. **Provide helpful error messages** — Tell users why an action is unavailable instead of waiting for it to fail

Permission checks are **not** necessary for:

- Simple request-response flows where the user will see immediate feedback
- Backend services that already handle authorization errors
- Operations where you want to attempt the action regardless

---

## Understanding advisory results

**Permission checks return point-in-time snapshots.** The result tells you whether you would be authorized *at the moment of the check*, not whether you will be authorized when you perform the operation.

Authorization state can change between the check and the operation.

**Actual operations always re-evaluate authorization independently.** Never cache permission check results or treat them as guarantees. Use them for UX guidance, not security decisions.

---

## Request structure

The `SelfSubjectAccessReview` message has three parts:

```protobuf
message SelfSubjectAccessReview {
  Metadata metadata = 1;

  // Spec describes the operation to check
  SelfSubjectAccessReviewSpec spec = 2;

  // Status is filled in by the server (output only)
  SelfSubjectAccessReviewStatus status = 3;
}
```

### Spec

The `spec` field describes the hypothetical operation:

- **`service`** (string, required): Full OSAC service name (e.g., `"osac.public.v1.Clusters"`, `"osac.public.v1.ComputeInstances"`)
  - Must match pattern: `osac.public.v1.[A-Z][a-zA-Z]*`
  - Min length: 1, Max length: 128
- **`method`** (string, required): The operation to check (e.g., `"Create"`, `"Get"`, `"List"`, `"Update"`, `"Delete"`)
  - Method names are case-sensitive and must match protobuf method definitions
  - Min length: 1, Max length: 64

### Status (output only)

The `status` field is filled in by the server and contains the evaluation result:

- **`allowed`** (bool): `true` if you would be authorized, `false` if denied
- **`reason`** (string): Why the request was denied
  - **For tenant-related errors:** Contains a message like "there is no default tenant" or tenant visibility errors
  - **For OPA policy denials:** Empty — current OPA policy does not export denial reasons
  - The reason is sanitized and never reveals cross-tenant information

---

## Response structure

The `Create` method returns a `SelfSubjectAccessReviewsCreateResponse` containing the evaluated `SelfSubjectAccessReview` object with the `status` field populated:

```json
{
  "object": {
    "metadata": {
      "tenant": "org-a"
    },
    "spec": {
      "service": "osac.public.v1.Clusters",
      "method": "Create"
    },
    "status": {
      "allowed": true,
      "reason": ""
    }
  }
}
```

---

## Examples

### Prerequisites

**CLI users:**

- The `osac` CLI is installed and configured (API endpoint, authentication token).
- Authenticated into the CLI

**API users:**

- `grpcurl` (for gRPC) or `curl` (for REST) is installed.
- Set the following environment variables for the examples in this guide:

  ```bash
  export OSAC_API="<api-endpoint>"           # e.g., fulfillment-api.osac.svc:443
  export TOKEN="<authentication-token>"

  # Skip TLS verification in development environments:
  # export GRPCURL_FLAGS="-insecure"
  # export CURL_FLAGS="-k"
  ```

---

### Check cluster creation permission

Check whether you can create clusters:

**CLI:**

```bash
osac create selfsubjectaccessreview --name create-check --service osac.public.v1.Clusters --method Create
```

**Output:**

```bash
Permission check ALLOWED: osac.public.v1.Clusters.Create
```

**Interpretation:** The `ALLOWED` means you are authorized to create clusters.

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" -d '{
  "object": {
    "metadata": {
      "name": "create-cluster-check"
    },
    "spec": {
      "service": "osac.public.v1.Clusters",
      "method": "Create"
    }
  }
}' $OSAC_API osac.public.v1.SelfSubjectAccessReviews/Create
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -X POST \
  -d '{
    "metadata": {
      "name": "create-cluster-check"
    },
    "spec": {
      "service": "osac.public.v1.Clusters",
      "method": "Create"
    }
  }' \
  "https://$OSAC_API/api/fulfillment/v1/self_subject_access_reviews"
```

**Response:**

```json
{
  "object": {
    "metadata": {
      "name": "create-cluster-check"
    },
    "spec": {
      "service": "osac.public.v1.Clusters",
      "method": "Create"
    },
    "status": {
      "allowed": true
    }
  }
}
```

**Interpretation:** The user is authorized to create clusters.

---

## Error handling

### Validation errors

The API validates requests using protobuf validation rules. Invalid input returns `InvalidArgument` errors:

**Unknown service:**

```bash
$ osac create selfsubjectaccessreview --name create-cluster --service osac.public.v1.BareMetalInstanceType --method Create
```

**Error response:**

```bash
Error: failed to create self-subject access review: rpc error: code = InvalidArgument desc = unknown service: osac.public.v1.BareMetalInstanceType
```

**Unsupported method:**

Some services do not support all standard methods. For example, `ExternalIPPools` supports `List` and `Get` but not `Create`, `Update`, or `Delete`:

```bash
# Create is not a supported method for BareMetalInstanceTypes
$ osac create selfsubjectaccessreview --name create-cluster --service osac.public.v1.BareMetalInstanceTypes --method Create
```

**Error response:**

```bash
Error: failed to create self-subject access review: rpc error: code = InvalidArgument desc = method Create not supported for service osac.public.v1.BareMetalInstanceTypes
```

### Authentication errors

Unauthenticated requests return `Unauthenticated` errors:

```bash
# Request to create BareMetalInstanceCatalogItems as a regular tenant user
$ ./osac create selfsubjectaccessreview --name create-cluster --service osac.public.v1.BareMetalInstanceCatalogItems --method Create
```

**Error response:**

```bash
Permission check DENIED: osac.public.v1.BareMetalInstanceCatalogItems.Create
```

### Internal errors

If the authorization evaluator fails (OPA service unavailable, policy error), the API returns `Internal` errors:

```json
{
  "code": 13,
  "message": "authorization evaluation failed",
  "details": []
}
```

**Recovery:** These are typically transient failures. Retry the request. If the error persists, contact your administrator.

---

## Best practices

### 1. Use specific service names

Always use fully-qualified service names (`osac.public.v1.ServiceName`), not shortened forms:

**Good:** `"osac.public.v1.Clusters"`
**Bad:** `"Clusters"`, `"clusters"`, `"osac.Clusters"`

### 2. Method names are case-sensitive

Protobuf method names are capitalized. Use exact protobuf method names:

**Good:** `"Create"`, `"Get"`, `"List"`, `"Update"`, `"Delete"`
**Bad:** `"create"`, `"CREATE"`, `"list"`

### 3. Use the `reason` field carefully

The `reason` field may contain helpful information for tenant-related errors, but is empty for most OPA policy denials:

**When `reason` is populated:**
- Tenant visibility errors (e.g., user not a member of the specified tenant)
- Missing default tenant: `"there is no default tenant"`

**When `reason` is empty:**
- OPA policy denials (most authorization failures)


### 4. Validate supported methods at design time

Not all services support all methods. Check the service's protobuf definition to see which methods are available:

- `ExternalIPPools`: `List`, `Get` (no Create/Update/Delete)
- `Clusters`: `Create`, `Get`, `List`, `Update`, `Delete`
- `VirtualNetworks`: `Create`, `Get`, `List`, `Update`, `Delete`

Requesting an unsupported method returns `InvalidArgument`, not `allowed=false`.

### 5. Use permission checks for UX, not security

Permission checks improve user experience by hiding unavailable actions. They do **not** replace server-side authorization:

- ✅ Use permission checks to show/hide UI elements
- ✅ Use permission checks to provide helpful error messages
- ❌ Don't skip server-side authorization because a permission check passed
- ❌ Don't use permission checks as a security control

Security is enforced by the actual operation's authorization evaluation, not by permission checks.

---

## Limitations in v1

The v1 implementation has the following limitations:

1. **Method-level checks only:** You can check "can I call this method?" but not "can I modify resource X specifically?" There is no way to check permissions for a named resource.

2. **Limited denial reasons:** The `reason` field is populated for tenant-related errors (e.g., "there is no default tenant") but empty for OPA policy denials. Future versions may add more detailed denial reasons from the OPA policy output.

3. **No bulk checks:** You must make separate requests for each permission check. There is no API to check multiple permissions in a single request.

4. **No caching:** The API does not cache results. Each request evaluates OPA policy from scratch.

These limitations are documented in the [design document](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-2476-self-subject-access-review/design.md) and may be addressed in future versions.

---

## Service and method reference

The following table lists common services and their supported methods:

| Service | Create | Get | List | Update | Delete | Notes |
|---------|--------|-----|------|--------|--------|-------|
| `osac.public.v1.Clusters` | ✅ | ✅ | ✅ | ✅ | ✅ | |
| `osac.public.v1.ComputeInstances` | ✅ | ✅ | ✅ | ✅ | ✅ | |
| `osac.public.v1.VirtualNetworks` | ✅ | ✅ | ✅ | ✅ | ✅ | |
| `osac.public.v1.Subnets` | ✅ | ✅ | ✅ | ✅ | ✅ | |
| `osac.public.v1.SecurityGroups` | ✅ | ✅ | ✅ | ✅ | ✅ | |
| `osac.public.v1.ExternalIPs` | ✅ | ✅ | ✅ | ❌ | ✅ | No Update |
| `osac.public.v1.ExternalIPAttachments` | ✅ | ✅ | ✅ | ❌ | ✅ | No Update |
| `osac.public.v1.ExternalIPPools` | ❌ | ✅ | ✅ | ❌ | ❌ | Read-only (platform resource) |
| `osac.public.v1.Tenants` | ✅ | ✅ | ✅ | ✅ | ✅ | |
| `osac.public.v1.NATGateways` | ✅ | ✅ | ✅ | ✅ | ✅ | |

For the complete list of services and methods, see the [public API proto definitions](https://github.com/osac-project/osac/tree/main/proto/public/osac/public/v1).

---

## Related documentation

- [Design document](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-2476-self-subject-access-review/design.md) — Full technical design and implementation details
- [Authorization guide](../../keycloak-configuration.md) — How OSAC authorization works (Keycloak + OPA)
- [Tenant setup guide](tenant-setup.md) — Creating and managing tenants
- [API design guidelines](../../../fulfillment-service/docs/API.md) — Proto conventions and API patterns
