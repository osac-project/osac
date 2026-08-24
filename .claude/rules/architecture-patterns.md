# Architecture Patterns

## Multi-tenancy

Tenant-scoped resources include tenant isolation metadata:
- `metadata.annotations["osac.openshift.io/tenant"]` for tenant scoping
- `metadata.annotations["osac.openshift.io/owner-reference"]` for resource hierarchy
- OPA policies enforce isolation at runtime
- Never skip tenant isolation metadata in new tenant-scoped resources
- Use annotations for owner references, not separate fields
- Provider-defined resources (NetworkClass, ExternalIPPool) are exempt — tenants do not interact with them directly

## Resource Hierarchy

```text
Cluster Resources:
  ClusterOrder → provisions OpenShift clusters via Hosted Control Planes

Compute Resources:
  ComputeInstance → KubeVirt VM, attached to Subnets + SecurityGroups
  DiskImage → disk image source (registry, URL)

Networking Resources:
  NetworkClass (platform-defined, internal)
  └── VirtualNetwork (tenant L2 network with CIDR)
        ├── Subnet (CIDR range within VirtualNetwork)
        ├── SecurityGroup (firewall rules scoped to VirtualNetwork)
        └── NATGateway (SNATs egress traffic through an ExternalIP)

External IP Resources:
  ExternalIPPool (platform-defined, external IP ranges)
  ├── ExternalIP (allocated from pool)
  └── ExternalIPAttachment (binds ExternalIP to ComputeInstance)

Tenant Resources:
  Tenant → namespace and resource isolation
```

Parent-child relationships use owner reference annotations (`osac.openshift.io/owner-reference`).

## Service Stack (fulfillment-service)

- PostgreSQL for persistent storage
- gRPC with grpc-gateway for REST/JSON support
- Controller-runtime for Kubernetes integration
- OPA for authorization policies
- Prometheus for metrics

## Integration Testing (fulfillment-service)

- Runs against a Kind cluster (named "osac-dev"), created via
  `make -C osac-installer install-infra PLATFORM=kind PROFILE=dev NS=osac`
- TLS with SNI routing via Envoy Gateway
- Keycloak for authentication
- Requires `/etc/hosts` entries:
  - `127.0.0.1 keycloak.keycloak.svc.cluster.local`
  - `127.0.0.1 fulfillment-api.osac.svc.cluster.local`
  - `127.0.0.1 fulfillment-internal-api.osac.svc.cluster.local`
- Clean up with: `make -C osac-installer uninstall PLATFORM=kind PROFILE=dev NS=osac`

## Cross-Component Coordination

### Dependency Order for Multi-Component Changes
When a PR spans multiple components, changes MUST land in dependency order:

```
fulfillment-service (proto changes)
  ↓
osac-operator (CRD + controller consuming proto types)
  ↓
osac-aap (Ansible playbooks consuming CRD fields via extra_vars)
  ↓
osac-installer (RBAC, Helm chart updates)
```

**How to check**: If proto changes without corresponding CRD updates in same PR, flag as cross-PR dependency gap.

### Helm Chart Sync Requirements
CRD changes in osac-operator MUST be synced to Helm charts:

1. Controller `// +kubebuilder:rbac:` markers → `osac-installer/charts/osac-operator/templates/clusterrole.yaml`
2. CRD YAML in `osac-operator/config/crd/bases/` → `osac-installer/charts/osac-operator/crds/`

**How to check**: Run `git diff` on both locations—should match.

### gRPC Gateway Header Forwarding
Custom HTTP headers require explicit allowlist in gateway config (`fulfillment-service/internal/cmd/service/start/restgateway/start_rest_gateway_cmd.go`).

**Default**: Only permanent HTTP headers + `Grpc-Metadata-*` prefix are forwarded.

**How to check**: If PR adds custom header dependency, verify gateway config updated or header renamed to `Grpc-Metadata-` format.

### OPA Policy Coverage
New tenant-scoped resources MUST have OPA policy in `fulfillment-service/internal/auth/policies/authz.rego`:
- `allow_<action>` for each RBAC verb
- Tenant isolation check
- Cross-resource reference validation

## Common Bug Patterns (For Code Review)

### UpdateMask Field Omissions
**Pattern**: New field added to proto, not to UpdateMask allowlist.  
**Impact**: gRPC Update calls silently ignore the field.  
**Detection**: Check if new proto field name appears in handler's `ValidateUpdateMask` or `updateMask.Paths`.

### CRD Immutability Bypass
**Pattern**: Optional field added to struct with `self == oldSelf` validation.  
**Impact**: Immutability broken when `oldSelf` lacks the optional field.  
**Detection**: Check if CEL expression handles `omitempty` fields correctly.

### Ansible Wait Loop Without Failure Exit
**Pattern**: `retries:` set without `failed_when:` for early exit.  
**Impact**: Loop exhausts all retries on permanently failed resources.  
**Detection**: Tasks with `retries: > 1` should have `failed_when:` for resources with failure states (CSV, Job, Pod).

### Regex Validation Edge Cases
**Pattern**: Regex with alternation order, escaping, or anchor bugs.  
**Impact**: Accepts invalid input or rejects valid input.  
**Detection**: Check alternation order (longest first), anchors (`^$`), escaping (`.` vs `\.`).

### Cross-PR Dependency Gaps
**Pattern**: CRD field added without reconciler in same PR.  
**Impact**: Field accepted but never acted upon.  
**Detection**: CRD schema change should have corresponding controller code change in same PR.

### RBAC Marker vs Helm Chart Drift
**Pattern**: `// +kubebuilder:rbac:` added but Helm ClusterRole not updated.  
**Impact**: Works in dev, fails in production Helm deployment.  
**Detection**: Compare kubebuilder-generated `config/rbac/role.yaml` with Helm template.

### Test Coverage Gaps (Happy Path Only)
**Pattern**: Tests exist but skip edge cases.  
**Impact**: Edge case bugs slip through CI.  
**Detection**: PR claims to handle edge cases but tests only check non-empty/valid values.

### Documentation Staleness
**Pattern**: Code changed, docs not updated.  
**Detection**: New CRD → check AUTH.md; new resource type → check architecture-patterns.md hierarchy; PR description mismatch with implementation.
