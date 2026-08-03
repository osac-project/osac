# OSAC Mono-Repo

OSAC (Open Sovereign AI Cloud) is a fulfillment system for provisioning Kubernetes clusters, compute instances, bare-metal hosts, and networking. This mono-repo contains six components, each with its own `CLAUDE.md`/`AGENTS.md` — **read the component's docs before making changes in it** (progressive disclosure).

## Components

| Component | Description |
|-----------|-------------|
| `fulfillment-service/` | gRPC/REST API server + `osac` CLI (PostgreSQL, OPA) |
| `osac-operator/` | Kubernetes operator for CRDs (ClusterOrder, ComputeInstance, Tenant, networking) |
| `osac-aap/` | Ansible playbooks for infrastructure provisioning |
| `osac-installer/` | Helm-based three-phase deployment orchestrator |
| `bare-metal-fulfillment-operator/` | Kubernetes operator for bare-metal host pools |
| `osac-csi-driver/` | CSI meta-driver aggregating vendor storage drivers |

External repos (clone as siblings for cross-repo workflows):

| Repo | Path |
|------|------|
| [osac-test-infra](https://github.com/osac-project/osac-test-infra) | `../osac-test-infra/` |
| [osac-ui](https://github.com/osac-project/osac-ui) | `../osac-ui/` |
| [enhancement-proposals](https://github.com/osac-project/enhancement-proposals) | `../enhancement-proposals/` |
| [docs](https://github.com/osac-project/docs) | `../osac-docs/` |

## Cross-Component Changes

All components share this mono-repo — a feature spanning any of them lands in a single branch and PR. Apply changes in this order:

1. **fulfillment-service** — Update proto definitions, regenerate
2. **osac-operator** — Update CRD types, controller logic, `buf generate`
3. **osac-aap** — Update Ansible roles/playbooks
4. **osac-installer** — Update RBAC if needed

## Git Workflow

- **Fork-based**: push to `fork` remote, never to `origin`. PRs go from `fork/<branch>` to `origin/main`
- **Branch naming**: `<type>/<ticket-or-description>` (e.g., `feat/OSAC-23607`, `fix/duplicate-aap-jobs`)
- **DCO sign-off**: `git commit -s` on all commits
- **AI attribution**: `Assisted-by: Claude Code <noreply@anthropic.com>` — never `Co-Authored-By` for AI tools (Red Hat attribution standard)
- **Commit message**: `OSAC-XXXXX: description` (Jira key) or `NO-ISSUE: description`

## Deployment Coordination

`osac-installer/scripts/sync-image-tags.sh` computes each component's SHA-based image tag and writes it into Helm values files. Since all components share this mono-repo, run `sync-image-tags.sh --fix` in the same PR if it touches values files. External dependencies that still need explicit bumps:

- **osac-ui** — OCI chart + image, version-tagged
- **osac-csi-driver** — git submodule under `osac-installer/base/`

## Enhancement Proposals

PRD and design workflows publish to the separate `enhancement-proposals` repo (clone as sibling at `../enhancement-proposals/`). Feature directory: `enhancements/<jira-key>-<feature-slug>/` with `prd.md` and `design.md`. Push to `fork` remote.

## Jira Conventions

- OSAC uses Jira **Tasks** (not Stories) for implementation work
- Use `jira` CLI for Jira access (e.g., `jira issue view OSAC-1234 --plain`), not Jira MCP

## AI-Assisted Workflows

Run `tools/bootstrap.sh` to install [flightctl/ai-workflows](https://github.com/flightctl/ai-workflows) (bugfix, implement, prd, design, e2e). OSAC-specific skills will be installed from a separate repo.
