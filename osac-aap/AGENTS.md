# AGENTS.md

Guide for AI agents working on osac-aap — Ansible Automation Platform roles and playbooks for OSAC infrastructure provisioning.

## Overview

osac-aap contains Ansible collections that provision and manage OSAC infrastructure. Backends are added over time — the lists below are examples, not exhaustive:
- **Network backends**: e.g. CUDN, Netris, OpenStack, MetalLB
- **Compute backends**: e.g. OpenShift Virtualization (KubeVirt)
- **Cluster provisioning**: e.g. OpenShift 4.20 templates
- **Storage backends**: e.g. VAST
- **Bare metal provisioning**: e.g. OpenStack Ironic (via the ESI collection), Metal3, NICo

Integration flow: fulfillment-service (API) → osac-operator (CRs) → AAP job templates → osac-aap playbooks → template roles

## Development Environment

### Setup

Install dependencies:
```bash
uv sync --all-groups
source .venv/bin/activate  # or prefix commands with `uv run`
```

`uv sync` creates or updates `.venv` in place to match `pyproject.toml`/`uv.lock` — re-run it after dependency changes instead of recreating the venv manually.

### Key Configuration Files

- `pyproject.toml` — Python dependencies (Ansible 11.4.0+, kubernetes 32.0.1+)
- `uv.lock` — Locked dependency versions
- `ansible.cfg` — Jinja2 native mode, `collections_path=./vendor:./collections` (vendored collections are searched before local `collections/`)
- `ansible-navigator.yml` — Logging and playbook artifact configuration
- `.python-version` — Python 3.13+ for local development (container runtime uses Python 3.12 as specified in `execution-environment.yaml`)
- `devfile.yaml` — Dev Containers configuration (ansible-workspace-env-reference image)

### Collections Management

Collections live in two directories:
- `collections/ansible_collections/` — Local collections across multiple namespaces: `osac.*` (service, templates, workflows, config_as_code, esi, steps, test_overrides), `agentless_net.*`, `ci.*`, `dns.*`, `netris.*`, `nico.*`
- `vendor/` — Vendored upstream collections

Re-vendor after updating `collections/requirements.yml`:
```bash
rm -rf vendor && ansible-galaxy collection install -r collections/requirements.yml
git add vendor/  # commit vendored collections
```

Note: Requires Red Hat Automation Hub token for proprietary collections.

## Repository Structure

```
osac-aap/
├── playbook_osac_*.yml              # AAP job templates (entry points)
├── collections/ansible_collections/
│   ├── osac/service/                # Utilities (common, finalizer, lease, wait_for)
│   ├── osac/templates/              # Infrastructure provisioning roles
│   ├── osac/workflows/              # Multi-step orchestration
│   ├── osac/config_as_code/        # AAP configuration as code
│   ├── osac/esi/                   # Elastic Secure Infrastructure
│   ├── osac/steps/                 # Workflow step definitions
│   ├── osac/test_overrides/        # Test template overrides
│   ├── ci/                         # CI workflow steps
│   ├── netris/                     # Netris network backend
│   ├── nico/                       # NVIDIA NICo bare metal backend
│   ├── dns/                        # DNS management
│   └── agentless_net/              # CaaS networking
├── vendor/                         # Vendored collections
├── tests/integration/              # Integration test suites
│   ├── targets/                    # Test scenarios
│   ├── fixtures/                   # Sample CRs
│   └── common_vars.yml             # Shared test variables
├── execution-environment/          # Container build definitions
├── charts/aap/                     # Helm charts for AAP deployment
└── samples/                        # Example payloads
```

## Template Role Pattern

Each template role in `osac.templates` declares its metadata in `meta/osac.yaml`. The fields present depend on `template_type` (`network`, `compute_instance`, `bare_metal_instance`, `storage_provider`, or `cluster`) — see the exact structure for each type below.

Running `playbook_osac_config_as_code.yml` publishes these as NetworkClass/ComputeClass resources in Kubernetes.

### Template Type-Specific `meta/osac.yaml` Structures

**Network templates** (e.g., `cudn_net`, `metallb_l2`):
```yaml
template_type: network
implementation_strategy: <name>
fabric_manager: <name>       # optional
k8s_manager: <name>          # optional
is_default: true/false       # optional
capabilities:
  supports_ipv4: true
  supports_ipv6: true
  supports_dual_stack: true
```

**Compute instance templates** (e.g., `ocp_virt_vm`):
```yaml
template_type: compute_instance
spec_defaults:
  # cores/memory_gib are reserved (removed) on ComputeInstanceTemplateSpecDefaults —
  # instance_type is now the sole, mandatory way to size a ComputeInstance.
  # Set spec_defaults.instance_type here to give the template a default, or
  # omit it to require callers to always pass instance_type explicitly.
  boot_disk:
    size_gib: 10
  image:
    source_type: registry
    source_ref: "quay.io/containerdisks/fedora:latest"
parameters:
  - name: exposed_ports
    title: Exposed Ports
    description: Port configuration
    type: string
    required: false
    default: "22/tcp"
    validation:
      pattern: '^([0-9]+/(tcp|udp))(,[0-9]+/(tcp|udp))*$'
```

**Bare metal instance templates** (e.g., `bm_host_agent_provisioning`):
```yaml
template_type: bare_metal_instance
# No standard fields beyond template_type
```

**Storage provider templates** (e.g., `vast_storage`):
```yaml
template_type: storage_provider
implementation_strategy: <name>
capabilities:
  supported_protocols:
    - nfs
    - block
  provisioning_targets:
    - vmaas
    - hcp_control_plane
```

**Cluster templates** (e.g., `ocp_small`):
```yaml
title: Display name
description: Cluster template description
default_node_request:
  - resourceClass: fc430
    numberOfNodes: 2
allowed_resource_classes: []
# Note: Typically no template_type, implementation_strategy, or capabilities
# fields. Exception: ocp_4_20_ai_maas has both template_type: cluster and
# implementation_strategy — verify the actual file rather than assuming.
```

### Template Roles by Type

**Networking:**
- `cudn_net` — ClusterUserDefinedNetwork (CUDN) with OVN-Kubernetes
- `netris` — Netris Controller API backend
- `openstack` — OpenStack Neutron
- `network_policy` — Kubernetes NetworkPolicy (SecurityGroups)
- `metallb_l2` — MetalLB-based ExternalIPPool/ExternalIP

**Compute:**
- `ocp_virt_vm` — KubeVirt VMs on OpenShift Virtualization

**Clusters:**
- `ocp_small` — Standard OpenShift cluster template
- `ocp_4_20_ai_maas` — OpenShift 4.20 with AI/MaaS
- `ocp_4_20_small_nico` — OpenShift 4.20 with NICo networking
- `ocp_ci_small` — CI cluster template

**Storage:**
- `vast_storage` — VAST storage backend

**Bare Metal:**
- `bm_host_agent_provisioning` / `bm_host_agent_deprovisioning`
- `bm_host_provisioning` — Dispatches to Metal3 or OpenStack Ironic
- `bm_private_network` / `bm_host_private_network`

### Service Roles (osac.service)

Shared utilities imported by template roles:
- `cleanup_stale_network_resources` — Cleanup for stale network resources
- `cluster_infra` — Cluster infrastructure utilities
- `cluster_settings` — Cluster configuration management
- `cluster_working_namespace` — Cluster namespace utilities
- `common` — Kubeconfig and credential management (`get_remote_cluster_kubeconfig`)
- `enumerate_templates` — Template enumeration
- `external_access` — External access configuration
- `extract_template_info` — Template metadata extraction
- `finalizer` — Kubernetes finalizer lifecycle (`add_finalizer`, `remove_finalizer`)
- `hosted_cluster` — Hosted cluster management
- `lease` — Bare metal lease management
- `manage_agents` — Agent management utilities
- `metallb_ingress` — MetalLB ingress setup
- `nmstate_config` — Network configuration with nmstate
- `openstack_bm_node` — OpenStack bare metal node preflight and preparation
- `publish_templates` — Registers template metadata as NetworkClass/ComputeClass
- `retrieve_kubeconfig` — Kubeconfig retrieval
- `storage_provider` — Storage provider utilities
- `tenant_storage_class` — StorageClass discovery for tenants
- `tenant_target_namespace` — Tenant namespace utilities
- `wait_for` — Polling utilities for pods, deployments, CRs
- `write_ssh_keys` — SSH key management

## Testing

### Running Tests

```bash
make test   # Full integration suite (setup → run → teardown)
make lint   # ansible-lint validation
```

### Integration Test Structure

Location: `tests/integration/`

Test orchestration:
- `setup_test_env.sh` — Provisions kind cluster and test fixtures
- `run_tests.sh` — Executes test playbooks
- `teardown_test_env.sh` — Cleanup

Test targets in `tests/integration/targets/`:
- `cluster_*` — ClusterOrder lifecycle (create, delete, post_install, status_reporting)
- `compute_instance_*` — ComputeInstance lifecycle (create, delete, with_gpu)
- `storage_provider_*` — Storage onboarding, setup, teardown, rollback
- `finalizer`, `lease`, `tenant_target_namespace` — Service role tests
- `config_as_code_pod_specs` — AAP configuration validation

Test fixtures: `tests/integration/fixtures/` contains sample CRs (ClusterOrder, ComputeInstance with/without GPU).

### CI Workflows

**`.github/workflows/ansible-lint.yml`:**
- Dedicated ansible-lint validation on PRs

**`.github/workflows/integration-tests.yml`:**
- Full test suite with kind cluster (storage tests conditionally enabled via `STORAGE_TESTS_ENABLED=true` environment variable in CI)

**`.github/workflows/e2e-vmaas-full-install.yml`:**
- End-to-end VMaaS installation testing
- Triggers: `pull_request`, scheduled (every 12h), `workflow_dispatch`

**`.github/workflows/e2e-bmaas-full-install.yml`:**
- End-to-end BM-as-a-Service installation testing

**`.github/workflows/pre-commit.yaml`:**
- Runs all pre-commit hooks on PRs

**`.github/workflows/helm-lint.yaml`:**
- Validates Helm chart syntax

**`.github/workflows/execution-environment.yml`:**
- Container images built from `execution-environment/execution-environment.yaml`
- Validates the build definition and produces images for AAP deployment

**`.github/workflows/publish-charts.yaml`:**
- Publishes Helm charts on release tags

**`.github/workflows/ok-to-test-label-cleanup.yml`:**
- Removes `ok-to-test` label on new pushes to PRs

**`.github/workflows/slash-command.yml`:**
- Handles slash commands in PR comments

## Building

### Execution Environment (Container)

Build definition: `execution-environment/execution-environment.yaml`

- **Base image**: UBI 10.2 (Red Hat Universal Base Image)
- **Python**: 3.12 in container runtime
- **System packages**: systemd-libs, systemd-devel, gcc, python3.12-devel, git-core, bind-utils, krb5-devel
- **CLI tools**: oc and kubectl copied from `quay.io/openshift/origin-cli:4.19`
- **Build files**: Copies local `collections/` and `vendor/` into container

No compilation step — Ansible playbooks are interpreted at runtime.

### Helm Charts

Location: `charts/aap/`
Purpose: AAP deployment configuration
Validation: CI runs `helm lint` on all PRs

## Code Standards

### Naming Conventions

- **Playbooks**: `playbook_osac_{action}_{resource}.yml`
- **Roles**: Use underscores, not hyphens (e.g., `cudn_net`, not `cudn-net`)
- **Implementation strategy**: Should match role name exactly (underscores) for new roles.
  Existing exceptions: `metallb_l2` role publishes `implementation_strategy: metallb-l2`
  (hyphen), and `vast_storage` role publishes `implementation_strategy: vast`
- **Collections**: Namespace `osac.*` (service, templates, workflows, config_as_code, esi, steps, test_overrides)

### Ansible-lint Rules

Configuration: `.ansible-lint.yml`

Enforced:
- FQCN for all modules (e.g., `ansible.builtin.debug`, not `debug`)
- All tasks must have `name:` attribute

Skipped:
- `role-name[path]` — Role names use underscores (OSAC convention)
- `parser-error` — False positive on multi-play test playbooks
- `fqcn[keyword]` — Workflows use `collections:` keyword

Warnings:
- `risky-file-permissions` — Test code uses `lineinfile` without mode

Excluded:
- `vendor/`, `.github/`, `execution-environment/`

### YAML Linting

Configuration: `.yamllint.yaml`

Disabled:
- line-length
- document-start

Customized:
- `indent-sequences: whatever` — Flexible list indentation
- `hyphens.max-spaces-after: 4`
- `truthy.check-keys: false`
- `comments.min-spaces-from-content: 1`
- Octal values forbidden (both implicit and explicit)

### Pre-commit Hooks

Configuration: `.pre-commit-config.yaml`

Hooks:
- Standard checks: trailing-whitespace, check-merge-conflict, end-of-file-fixer, check-added-large-files
- Security: detect-private-key
- Format: check-json, check-symlinks, check-case-conflict
- YAML: yamllint with `--strict`

Excluded: `vendor/`, `charts/`

## PR Workflow

### Commit Messages

Format:
- `OSAC-XXXX: description` (with Jira ticket)
- `NO-ISSUE: description` when no Jira ticket applies
- Or conventional commits: `feat:`, `fix:`, `test:`, `docs:`, `chore:`, `refactor:`

DCO: Sign-off required (`git commit -s`)

AI attribution: Add trailer when AI-assisted:
```
Assisted-by: Claude Code <noreply@anthropic.com>
```

Note: Use `Assisted-by`, not `Co-Authored-By` (Red Hat attribution standard).

### Fork-Based Workflow

- Push to `fork` remote, not `origin`
- PRs go from `fork/<branch>` to `origin/main`
- Include the Jira ticket key in the PR title when one applies (e.g., "OSAC-12345: add VAST storage backend"); use `NO-ISSUE` otherwise, matching the commit message format above

### CI Checks

All PRs must pass (docs-only PRs — `*.md`, `docs/` — skip checks 1-3 via `paths-ignore`):
1. `pre-commit` — All pre-commit hooks (`pre-commit.yaml`)
2. `ansible-lint` — Playbook and role linting (`ansible-lint.yml`)
3. `integration-tests` — Full test suite with kind cluster (`integration-tests.yml`)
4. `helm-lint` — Helm chart validation (`helm-lint.yaml`)
5. `execution-environment` — Container image build validation (`execution-environment.yml`)

### Review Process

- Cross-repo changes documented in PR description (link related PRs)
- `meta/osac.yaml` updated for template role changes
- Collections re-vendored after `collections/requirements.yml` updates
- E2E tests require `/ok-to-test` comment for fork PRs (org membership check)

## Common Fix Locations

Use this table to go directly to the right file for common patterns:

| Pattern | Location |
|---------|----------|
| Add new network backend | `collections/ansible_collections/osac/templates/roles/<backend>/` + `meta/osac.yaml` |
| Add new compute backend | `collections/ansible_collections/osac/templates/roles/<backend>/` + `meta/osac.yaml` |
| Add new cluster template | `collections/ansible_collections/osac/templates/roles/<template>/` + `meta/osac.yaml` |
| Add new bare metal template | `collections/ansible_collections/osac/templates/roles/<backend>/` + `meta/osac.yaml` |
| Add AAP job template | `playbook_osac_<action>_<resource>.yml` (top-level) |
| Add shared utility | `collections/ansible_collections/osac/service/roles/<utility>/` |
| Add integration test | `tests/integration/targets/<test_name>/` |
| Fix CR status updates | Template role's main task file (look for `kubernetes.core.k8s` with `status:` key) |
| Add new capability | `meta/osac.yaml` in template role, then run `playbook_osac_config_as_code.yml` |

## Security Guidelines

### Tenant Isolation

All resources must include tenant metadata:
- `osac.openshift.io/tenant` annotation
- `osac.openshift.io/owner-reference` annotation for resource hierarchy

OPA policies enforce isolation at runtime (handled by fulfillment-service).

### Secrets Management

- Pre-commit hook `detect-private-key` scans for leaked secrets
- Example files use `.example` suffix (e.g., `tests/golden-image-vars.yml.example`)
- Environment variables: `OSAC_AAP_TOKEN` for AAP authentication (set by osac-operator)

### Container Security

- Base image: Red Hat UBI 10.2 (official, maintained)
- System updates: glibc updated before package installation
- Minimal packages: Only required system dependencies installed

### Network Security

- SecurityGroup CR translates to Kubernetes NetworkPolicy
- Supports TCP, UDP, ICMP with port ranges and CIDR sources
- Implementation in `osac.templates.network_policy` and `osac.templates.cudn_net`

## Common Workflows

### Adding a New Network Backend

1. Create role directory: `collections/ansible_collections/osac/templates/roles/<backend>/`
2. Add `meta/osac.yaml`:
   ```yaml
   template_type: network
   implementation_strategy: <backend>
   fabric_manager: <backend>       # optional
   k8s_manager: <k8s_component>    # optional
   is_default: false               # optional
   capabilities:
     supports_ipv4: true
     supports_ipv6: true           # optional
     supports_dual_stack: true     # optional
   ```
3. Implement tasks in `tasks/main.yml` (provision and deprovision logic)
4. Add integration test in `tests/integration/targets/<backend>_test/`
5. Update `playbook_osac_config_as_code.yml` if needed
6. Run `make test` and `make lint`

### Adding a New Compute Backend

1. Create role directory: `collections/ansible_collections/osac/templates/roles/<backend>/`
2. Add `meta/osac.yaml`:
   ```yaml
   template_type: compute_instance
   implementation_strategy: <backend>
   spec_defaults:
     # cores/memory_gib are reserved (removed); instance_type is mandatory.
     boot_disk:
       size_gib: 10
   parameters:
     - name: param_name
       title: Display Name
       description: Parameter description
       type: string
       required: false
       default: "value"
   ```
3. Implement tasks in `tasks/main.yml` (provision and deprovision logic)
4. Add integration test in `tests/integration/targets/<backend>_test/`
5. Run `make test` and `make lint`

### Debugging Integration Tests

Inspect test environment after running tests:
```bash
make test
# Cluster name is determined by setup scripts, check with:
kind get clusters
# Inspect the cluster:
kubectl --context kind-<cluster-name> get pods -A
# Cleanup when done:
kind delete cluster --name <cluster-name>
```

Note: The test scripts manage cluster lifecycle automatically. To preserve clusters for debugging, modify `tests/integration/teardown_test_env.sh` directly.

## Cross-Repo Dependencies

When making changes that affect other OSAC components:

1. **New CR fields in osac-operator** → Update playbooks to consume new fields
2. **New template capabilities** → Run `playbook_osac_config_as_code.yml` to publish NetworkClass/ComputeClass
3. **New AAP job templates** → Update osac-operator to trigger new jobs
4. **Collection updates** → Update `collections/requirements.yml` and re-vendor

Link related PRs in description (e.g., "Depends on osac-operator#123").

## References

- `docs/` — Integration guides (Netris, NICo, BCM, agents)
- `samples/` — Example payloads and configurations
- `meta/osac.yaml` — Template role metadata format
- `ansible.cfg` — Jinja2 native mode and collections path
- `ansible-navigator.yml` — Logging and artifact configuration
