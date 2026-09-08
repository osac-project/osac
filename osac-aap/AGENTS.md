# OSAC AAP

Ansible roles and playbooks used by OSAC to provision infrastructure through
Ansible Automation Platform.

This component is part of the OSAC monorepo, not an isolated project. Its APIs,
generated artifacts, deployment configuration, and runtime behavior may affect
other components. Apply the repository-wide rules in
[`../AGENTS.md`](../AGENTS.md), consider downstream consumers before changing
behavior, and follow the instructions for every affected component.

## Required context

Before changing this component, identify the documents relevant to the change
below, then read and follow them. These documents are authoritative for their
respective areas.

- Component setup and playbook entry points: [`README.md`](README.md)
- Role and workflow examples: `collections/ansible_collections/osac/`
- Integration fixtures and targets: `tests/integration/`
- Deployment contracts: [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md)

## Invariants

- `pyproject.toml` and `uv.lock` own development dependencies; rerun
  `uv sync --all-groups` after changing them.
- Local collections live under `collections/ansible_collections/`; third-party
  collections live under `vendor/`. `ansible.cfg` searches `vendor/` before
  local collections, so stale vendored content can hide local changes.
- After changing `collections/requirements.yml`, reinstall the collections and review the complete `vendor/` diff.
- Use fully qualified Ansible module names and give every task a `name:`.
- Role directories use underscores. A network template role must define at least one `fabric_manager` or `k8s_manager`, and manager names must match the corresponding role directory names.
- A template role's `meta/osac.yaml` must match its actual `template_type`; verify the real template before copying fields from another type.
- Network roles are not published as ComputeClass-family resources by `playbook_osac_config_as_code.yml`. Cluster, compute-instance, and bare-metal template metadata is published by config-as-code; do not assume that behavior applies to network roles.
- NetworkClass resources are created by the installer from configured manager names; role capabilities are informational. Only one NetworkClass may exist per deployment, so adding a backend replaces the configured NetworkClass rather than creating another.
- Preserve tenant and owner-reference annotations on resources created by playbooks.
- Use the shared remote-kubeconfig service role for remote workflows and preserve the required empty primary-network label syntax.
- Never expose credentials in tasks, logs, fixtures, examples, or generated artifacts.

## Generated and vendored files

- There is no source-code generator for roles. Do not hand-edit third-party content under `vendor/`.
- Reinstall vendored collections after dependency changes. Confirm Automation Hub credentials and required environment variables first, then run:
  `rm -rf vendor && ansible-galaxy collection install -r collections/requirements.yml`.
- Review the vendor diff and commit it with the requirements change when vendoring is required.

## Validation

From `osac-aap/`:

```bash
uv sync --all-groups
uv run pytest tests/unit      # Unit tests
make lint                     # uv run ansible-lint
make test                     # Kind setup, integration tests, teardown
helm lint charts/aap
```

`make test` creates and removes a Kind test environment; run it for
integration-affecting changes.

For a focused workflow, run the relevant playbook from `tests/integration/`,
for example `ansible-playbook targets/<workflow>/tasks/baseline.yml -e "@common_vars.yml" -v`.
Build the execution environment with `make execution-environment-build` when
its definition or dependency inputs change.
