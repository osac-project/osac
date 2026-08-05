# osac-aap: AI Bot Instructions

## Project Overview

This is an Ansible automation repository for provisioning OSAC (Open Sovereign
AI Cloud) infrastructure. It contains playbooks, four Ansible collections
(`osac.service`, `osac.templates`, `osac.workflows`, `osac.config_as_code`),
integration tests, Helm charts, and an Execution Environment definition.

The project uses **uv** for Python dependency management (Python 3.13),
**ansible-lint** for linting, **pre-commit** for formatting checks, and
**kind** clusters for integration testing.

## Critical Ansible Conventions

These rules are non-negotiable. Violations will fail lint and/or break
runtime behavior:

1. **Use FQCN for all modules**: `ansible.builtin.debug`, not `debug`.
   ansible-lint enforces this.
2. **Every task must have a `name:` field.** No unnamed tasks.
3. **Use underscores in role names and `implementation_strategy`**, never
   hyphens. Role directory name, `meta/osac.yaml`, and CR annotation must
   all match.
4. **Always include `osac.service.common`** (with
   `tasks_from: get_remote_cluster_kubeconfig`) before creating K8s resources
   on remote clusters.
5. **Namespace label syntax**: `k8s.ovn.org/primary-user-defined-network: ""`
   must be an empty string, not a missing value.

## Validation Commands

Run these before considering any change complete:

```bash
# Install dependencies (if not already done)
uv sync --all-groups && source .venv/bin/activate

# Primary lint check (MUST pass)
uv run ansible-lint

# Syntax check individual playbooks
ansible-playbook --syntax-check playbook_osac_<name>.yml

# Pre-commit hooks (trailing whitespace, YAML lint, etc.)
pre-commit run --all-files

# Helm chart lint (only if charts/ was modified)
helm lint charts/aap/
helm template test charts/aap/ > /dev/null
```

### Integration Tests

Integration tests require a kind cluster and significant resources.
**Do not run integration tests automatically.** If your change touches
workflows, service roles, or test fixtures, note in the PR description
that integration testing is needed and let human reviewers trigger it
via CI.

## Commit Format

```
git commit -s -m "OSAC-XXXXX: description of change"
```

Commits must be signed-off (`-s`). Include the Jira ticket key.

## Files and Directories to Never Modify

- `vendor/` -- vendored third-party collections; re-vendor with
  `ansible-galaxy collection install -r collections/requirements.yml`
- `uv.lock` -- auto-generated lockfile; only modify via `uv sync`
- `.github/workflows/` -- CI pipelines owned by the platform team
- `collections/ansible_collections/massopencloud/` -- third-party
- `execution-environment/requirements.txt` -- generated; update the
  `execution-environment.yaml` instead
- `OWNERS` -- repo ownership; requires admin approval
- `LICENSE` -- do not change
- `.ai-bot/` -- bot configuration; changes require human review
- `.ansible-lint-ignore` -- lint suppressions; additions require human approval
- `pyproject.toml` -- Python project configuration
- `.pre-commit-config.yaml` -- pre-commit hook configuration
- `ansible.cfg` -- Ansible core configuration

## Repository Structure Quick Reference

```
playbook_osac_*.yml                    Top-level playbooks (AAP job templates)
collections/ansible_collections/
  osac/service/                        Core utility roles + filter plugins
  osac/templates/                      Infrastructure template roles
  osac/workflows/                      Multi-step workflow playbooks
  osac/config_as_code/                 AAP configuration
  osac/test_overrides/                 Test-only override collection
tests/integration/                     Kind-based integration tests
vendor/                                Vendored dependency collections
charts/aap/                            Helm chart for AAP deployment
samples/                               Example payloads
```

## Playbook Naming Convention

- File: `playbook_osac_{action}_{resource}.yml`
- AAP template: `osac-{action}-{resource}`
- Actions: `create`, `delete`, `report`, `attach`, `detach`, `cleanup`

## Standard Playbook Pattern

Every playbook receives a K8s CR via `ansible_eda.event.payload`, extracts
`implementation_strategy` from the CR annotation
(`osac.openshift.io/implementation-strategy`), and dynamically includes the
matching role from `osac.templates`:

```yaml
- name: Call the selected role
  ansible.builtin.include_role:
    name: "osac.templates.{{ implementation_strategy }}"
    tasks_from: create_<resource>
```

## Template Role Requirements

Every template role needs:
1. `meta/osac.yaml` with `implementation_strategy`, `template_type`, and
   `capabilities`
2. Task files named `tasks/create_<resource>.yaml` and
   `tasks/delete_<resource>.yaml`
3. Underscore naming throughout (directory name matches
   `implementation_strategy`)

## ansible-lint Configuration

- **Skip list**: `role-name[path]`, `parser-error`, `fqcn[keyword]`
- **Warn list**: `risky-file-permissions`
- **Excluded paths**: `vendor/`, `.github/`,
  `collections/ansible_collections/massopencloud/`, `execution-environment/`
- **Ignore file**: `.ansible-lint-ignore` lists known acceptable violations
  (mostly `var-naming[no-role-prefix]` in playbooks and `risky-file-permissions`
  in test overrides). **Do not add new entries to `.ansible-lint-ignore`.**
  Fix violations instead. If a violation cannot be fixed, flag it in the
  PR description and let a human decide.

## yamllint Configuration

Extends default with: line-length disabled, document-start disabled,
indent-sequences whatever, hyphens max-spaces-after 4, truthy check-keys
false, comments min-spaces-from-content 1.

## Cross-Repo Dependencies

Changes in osac-aap often require coordinated changes in:
- **osac-operator** -- CRD spec changes, controller logic
- **fulfillment-service** -- proto/API field additions
- **osac-installer** -- submodule bump (automated via CI)

You operate on this repo only. If a fix requires changes in another repo,
document the dependency in the PR description and stop. Do not attempt
cross-repo changes.
