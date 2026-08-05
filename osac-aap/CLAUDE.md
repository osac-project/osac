# CLAUDE.md

@AGENTS.md

## Project Overview

Ansible automation for OSAC infrastructure provisioning. See `AGENTS.md` for complete collection reference, development commands, linting configuration, CI workflows, and PR checklist.

## Detailed Rules (auto-loaded from `.claude/rules/`)

- **`playbook-patterns.md`** — Playbook naming, template roles, service roles, standard patterns, variable flow
- **`networking-cudn.md`** — CUDN networking implementation details (VirtualNetwork, Subnet, SecurityGroup)

## Common Pitfalls

1. **venv not activated** — `ansible-playbook: command not found` → `source .venv/bin/activate` or `uv run`
2. **Stale vendored collections** — re-vendor after updating `collections/requirements.yml`
3. **Implementation strategy mismatch** — for new roles, role dir name, `meta/osac.yaml`, and CR annotation must all match (use underscores). See `AGENTS.md` for existing exceptions (`metallb_l2`, `vast_storage`)
4. **Missing remote kubeconfig** — always include `osac.service.common` with `tasks_from: get_remote_cluster_kubeconfig`
5. **Namespace label syntax** — `k8s.ovn.org/primary-user-defined-network: ""` (empty string, not missing value)

## Cross-Repo Coordination

**Adding a new field to a resource (e.g., `mtu` to Subnet):**
1. fulfillment-service: Add field to proto, regenerate
2. osac-operator: Add field to CRD spec, update controller
3. osac-aap: Extract field in playbook, pass to role
4. osac-aap: Role reads field and provisions infrastructure

**Adding a new networking implementation:**
1. osac-aap: Create template role with `meta/osac.yaml`
2. osac-aap: Run config-as-code to publish NetworkClass
3. fulfillment-service: NetworkClass auto-discovered in API
4. Users: Create VirtualNetwork with new `networkClass`

## Links

- [Ansible Documentation](https://docs.ansible.com/)
- [kubernetes.core collection](https://docs.ansible.com/ansible/latest/collections/kubernetes/core/)
- [osac-operator](https://github.com/osac-project/osac-operator) — Kubernetes operator integration
- [fulfillment-service](https://github.com/osac-project/fulfillment-service) — Backend API
