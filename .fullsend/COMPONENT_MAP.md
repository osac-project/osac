# OSAC Component Cross-Reference Map

This document helps fullsend understand which files are related when reviewing PRs, so cross-file consistency checks don't miss important connections.

## When Proto Changes, Check These Files

### fulfillment-service Proto Change
**Change**: `fulfillment-service/proto/<domain>/<message>.proto`

**Related files to review**:
1. **gRPC service handler**: `fulfillment-service/internal/servers/<domain>/<operation>.go`
   - Check: UpdateMask allowlist includes new field
   - Check: Validation logic for new field constraints

2. **OPA policies**: `fulfillment-service/internal/auth/policies/authz.rego`
   - Check: Policy updated if new field affects authorization

3. **Integration tests**: `fulfillment-service/it/<resource>_test.go`
   - Check: Tests cover new field's edge cases

4. **osac-operator CRD**: `osac-operator/api/v1alpha1/<resource>_types.go`
   - Check: Corresponding CRD field added if proto-to-CRD mapping exists

5. **CLI commands**: `fulfillment-service/internal/cmd/cli/<resource>/*.go`
   - Check: CLI flags added for new field

6. **Documentation**:
   - `fulfillment-service/docs/AUTH.md` if new RBAC verb or resource
   - `.claude/rules/architecture-patterns.md` if new resource type

## When CRD Changes, Check These Files

### osac-operator CRD Change
**Change**: `osac-operator/api/v1alpha1/<resource>_types.go`

**Related files to review**:
1. **Controller reconcile logic**: `osac-operator/internal/controller/<resource>_controller.go`
   - Check: New field mapped to Ansible extra_vars or Kubernetes object

2. **Helm CRD copy**: `osac-operator/charts/operator-crds/templates/<crd>.yaml`
   - Check: CRD YAML synced (run `make -C osac-operator generate manifests` to regenerate)

3. **RBAC markers vs Helm ClusterRole**:
   - Source: `// +kubebuilder:rbac:` comments in controller .go files
   - Destination: `osac-operator/charts/operator/templates/clusterrole.yaml`
   - Check: Rules match (verbs, resources, API groups)

4. **Ansible playbooks**: `osac-aap/playbook_osac_*.yml` (top-level workflows)
   - Check: Playbook consumes new CRD field via `extra_vars` or `k8s` module

5. **Validation tests**: `osac-operator/internal/controller/<resource>_controller_test.go`
   - Check: envtest or unit tests cover new field validation rules

6. **Proto definition**: `fulfillment-service/proto/<domain>/<message>.proto`
   - Check: Proto message includes corresponding field if CRD is API-exposed

## When Ansible Playbooks Change, Check These Files

### osac-aap Playbook Change
**Change**: `osac-aap/playbook_osac_*.yml` (top-level workflows)

**Related files to review**:
1. **Controller calling playbook**: `osac-operator/internal/controller/<resource>_controller.go`
   - Check: extra_vars passed to playbook match what playbook expects

2. **Role implementations**: `osac-aap/collections/ansible_collections/osac/workflows/roles/<role-name>/` or `osac-aap/collections/ansible_collections/osac/config_as_code/roles/<role-name>/`
   - Check: Role variables documented in `defaults/main.yaml` or `README.md`

3. **Template roles**: `osac-aap/collections/ansible_collections/osac/workflows/roles/<template-role>/templates/*.j2`
   - Check: Jinja2 templates use variables defined in playbook or role defaults

4. **Collection dependencies**:
   - `osac-aap/collections/ansible_collections/osac/workflows/galaxy.yml`
   - `osac-aap/collections/ansible_collections/osac/config_as_code/galaxy.yml`
   - Check: New modules/plugins have collection dependencies declared

5. **Documentation**:
   - `fulfillment-service/docs/CATALOG_ITEMS.md` if playbook provisions provider-defined resource
   - Role `README.md` if role variables or behavior changed

## When Helm Charts Change, Check These Files

### osac-installer Helm Change
**Change**: `osac-installer/charts/<component>/`

**Related files to review**:
1. **Component templates**: `osac-installer/charts/<component>/templates/*.yaml`
   - Check: `values.yaml` schema matches template references

2. **CRD source**: `osac-operator/config/crd/bases/` for operator CRDs
   - Check: Helm CRDs are in sync with kubebuilder-generated CRDs

3. **RBAC source**: `// +kubebuilder:rbac:` markers in controller code
   - Check: Helm ClusterRole rules match kubebuilder markers

4. **Multi-component dependencies**: `osac-installer/charts/<component>/Chart.yaml`
   - Check: Dependency version bumps if subchart changed

5. **Installation docs**: `osac-installer/docs/helm-deployment-guide.md`
   - Check: New values or installation steps documented

## When SQL Migrations Change, Check These Files

### fulfillment-service Migration
**Change**: `fulfillment-service/internal/database/migrations/<timestamp>_<name>.sql`

**Related files to review**:
1. **Proto definitions**: `fulfillment-service/proto/<domain>/<message>.proto`
   - Check: Proto messages use JSON-serialized protobuf (GenericDAO pattern)

2. **Down migration**: Same file's `-- +goose Down` section
   - Check: Down migration reverses the change (drop column, drop index)

3. **Integration tests**: `fulfillment-service/it/<resource>_test.go`
   - Check: Tests updated for new schema (if CRUD operations affected)

Note: fulfillment-service uses GenericDAO with JSON-serialized protobuf, not traditional models/queries.

## When CLI Commands Change, Check These Files

### osac CLI Command
**Change**: `fulfillment-service/internal/cmd/cli/<command>/*.go`

**Related files to review**:
1. **Proto service**: `fulfillment-service/proto/<domain>/<service>.proto`
   - Check: CLI uses correct gRPC method and message types

2. **Command help text**: `--help` output inline in code
   - Check: Describes all flags, required vs optional, examples

3. **Parent command registration**: `fulfillment-service/internal/cmd/cli/root_cmd.go` or `<parent_cmd>.go`
   - Check: New command added as subcommand

4. **Integration tests**: `fulfillment-service/it/cli_test.go`
   - Check: CLI behavior tested (flags, error messages, output format)

## Documentation Cross-References

### AUTH.md
**Triggers**: New resource type, new RBAC verb, tenant scoping change

**Check**:
- Tenant permissions table includes new resource
- RBAC verbs match OPA policy rules
- Example policies show correct usage

### architecture-patterns.md
**Triggers**: New resource type, new parent-child relationship, new architectural pattern

**Check**:
- Resource hierarchy diagram updated
- Owner reference pattern documented
- Multi-tenancy rules applied

### CATALOG_ITEMS.md (osac-aap)
**Triggers**: New provider-defined resource (NetworkClass, ExternalIPPool, StorageTier)

**Check**:
- New catalog item documented with schema
- Ansible role or playbook that provisions it linked

### Component AGENTS.md
**Triggers**: Architectural decision, new pattern, workflow change

**Check**:
- Design decision documented if non-obvious
- Cross-component integration points updated
- Testing requirements clarified

### PR Description
**Triggers**: EVERY PR (this is meta-documentation)

**Check**:
- Claimed behavior matches implementation
- "Required" vs "optional" matches proto/CRD
- Testing plan matches actual test coverage
- Breaking changes flagged

## Component Dependency Graph

```text
fulfillment-service
├── Depends on: PostgreSQL, OPA, Keycloak (external)
├── Provides: gRPC API, osac CLI
└── Consumed by: osac-operator (watches Secrets for backend creds)

osac-operator
├── Depends on: fulfillment-service (CRD types mirror proto), osac-aap (Ansible playbooks)
├── Provides: CRDs, controllers
└── Consumed by: osac-installer (Helm charts)

osac-aap
├── Depends on: Ansible collections (kubernetes.core, ansible.posix, community.general)
├── Provides: Workflow playbooks, roles
└── Consumed by: osac-operator (controllers call playbooks)

osac-installer
├── Depends on: osac-operator (CRDs), fulfillment-service (service deployment), osac-aap (playbook execution)
├── Provides: Helm charts, three-phase deployment
└── Consumed by: End-user installation

bare-metal-fulfillment-operator
├── Depends on: osac-operator (host pool CRDs)
├── Provides: Bare-metal provisioning
└── Consumed by: osac-operator (references HostPool resources)

osac-csi-driver
├── Depends on: vendor CSI drivers (aggregation)
├── Provides: Meta CSI driver
└── Consumed by: Kubernetes storage classes

osac-metering
├── Depends on: fulfillment-service gRPC Watch API, Kafka
├── Provides: Usage events, CloudEvents, provider adapters
└── Consumed by: External billing systems
```

## File Type Coverage Checklist Template

Use this checklist when reviewing PRs to ensure broad coverage:

```markdown
## File Types Reviewed

- [ ] **Proto definitions** (*.proto) - validation, breaking changes, naming
- [ ] **CRD schemas** (*_types.go) - kubebuilder markers, validation, immutability
- [ ] **Controller logic** (*_controller.go) - reconcile, extra_vars, RBAC markers
- [ ] **gRPC handlers** (internal/servers/*/*.go) - UpdateMask, error handling, authz
- [ ] **OPA policies** (internal/auth/policies/authz.rego) - tenant isolation, cross-resource refs
- [ ] **Ansible playbooks** (playbook_osac_*.yml) - module_defaults, wait conditions, idempotency
- [ ] **Ansible roles** (roles/*/) - variable propagation, templates, defaults
- [ ] **Helm templates** (charts/*/templates/*.yaml) - RBAC, annotations, values refs
- [ ] **Helm CRDs** (charts/*/crds/*.yaml) - sync with osac-operator/config/crd/bases
- [ ] **SQL migrations** (internal/database/migrations/*.sql) - DDL safety, indexes, down migration
- [ ] **CLI commands** (internal/cmd/cli/*/*.go) - flags, help text, error messages
- [ ] **Integration tests** (it/*_test.go) - edge cases, not just happy path
- [ ] **Unit tests** (*_test.go) - new functions covered, mocks reasonable
- [ ] **Documentation** (AUTH.md, architecture-patterns.md, CATALOG_ITEMS.md, AGENTS.md) - sync with code

## Documentation Sync Checks

- [ ] **fulfillment-service/docs/AUTH.md** - new resources/verbs listed in tenant permissions table
- [ ] **.claude/rules/architecture-patterns.md** - resource hierarchy diagram includes new types
- [ ] **fulfillment-service/docs/CATALOG_ITEMS.md** - provider-defined resources documented
- [ ] **Component AGENTS.md** - design decisions and patterns updated
- [ ] **PR description** - matches actual implementation (required vs optional, scope)
- [ ] **Helm README** - new values or installation steps documented
- [ ] **Role README** - new role variables documented in defaults/main.yaml or README.md
```

## Quick Reference: "If X changed, did Y also change?"

| X (Change)                     | Y (Expected Related Change)                              |
|--------------------------------|----------------------------------------------------------|
| Proto field added              | gRPC handler UpdateMask, CLI flag, tests                 |
| CRD field added                | Controller reconcile, Helm CRD copy, Ansible playbook    |
| CRD RBAC marker added          | Helm ClusterRole template                                |
| Ansible playbook variable used | Controller extra_vars or role defaults/main.yaml         |
| SQL column added               | Model struct tag, query functions, down migration        |
| New tenant-scoped resource     | OPA policy, AUTH.md table, architecture-patterns diagram |
| CLI command added              | Parent command registration, help text, integration test |
| Helm values.yaml key added     | Template references the key, README documents it         |
| Provider-defined resource      | CATALOG_ITEMS.md entry, provisioning playbook/role       |

Use this table as a starting point for cross-file consistency checks. If X changed but Y didn't, investigate why (may be intentional, or may be a gap).
