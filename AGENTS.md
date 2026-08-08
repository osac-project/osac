# OSAC Mono-Repo

OSAC (Open Sovereign AI Cloud) is a fulfillment system for provisioning Kubernetes clusters, compute instances, bare-metal hosts, and networking. This mono-repo contains six components, each with its own `CLAUDE.md`/`AGENTS.md` — **read the component's docs before making changes in it**.

## Components

| Component | Description |
|-----------|-------------|
| `fulfillment-service/` | gRPC/REST API server + `osac` CLI (PostgreSQL, OPA) |
| `osac-operator/` | Kubernetes operator for CRDs (ClusterOrder, ComputeInstance, Tenant, networking) |
| `osac-aap/` | Ansible playbooks for infrastructure provisioning |
| `osac-installer/` | Helm-based three-phase deployment orchestrator |
| `bare-metal-fulfillment-operator/` | Kubernetes operator for bare-metal host pools |
| `osac-csi-driver/` | CSI meta-driver aggregating vendor storage drivers |

## External Repos

Clone as siblings for cross-repo workflows:

| Repo | Description |
|------|-------------|
| [osac-test-infra](https://github.com/osac-project/osac-test-infra) | E2E pytest tests against the fulfillment-service gRPC API |
| [osac-ui](https://github.com/osac-project/osac-ui) | Web console (React, PatternFly 6) |
| [enhancement-proposals](https://github.com/osac-project/enhancement-proposals) | PRDs and design documents (two-stage EP flow) |
| [docs](https://github.com/osac-project/docs) | Architecture docs and guides |

## Cross-Component Changes

A feature spanning multiple components lands in a single branch and PR. Apply changes in dependency order: fulfillment-service (proto) → osac-operator (CRDs, controllers) → osac-aap (playbooks) → osac-installer (RBAC, Helm). For deployment coordination (image tags, submodules), see `osac-installer/AGENTS.md`.

## Expensive e2e CI (OSAC-3370)

Full-install e2e workflows (`e2e-vmaas-full-install`, `e2e-bmaas-full-install`, `e2e-caas-full-install`) do **not** auto-spend runners on every PR push. A cheap `e2e-readiness` job **fails** until unlocked.

**Allow when any of:**
- `coderabbitai[bot]` `APPROVED` on the **current head SHA** → starts e2e (blocked while a human still has `CHANGES_REQUESTED`), or
- fresh `lgtm` label → starts e2e

Human GitHub `APPROVED` does **not** unlock. New pushes clear stale unlock labels.
Fork PRs still need `ok-to-test` (or org membership) for secrets/cluster — readiness is cost-only.

Cheap checks (pre-commit, lint, etc.) stay ungated. Schedules / `workflow_dispatch` / `merge_group` skip the readiness job.

Details + smoke checklist: [`.github/e2e-readiness.md`](.github/e2e-readiness.md).
