# OSAC Mono-Repo

OSAC (Open Sovereign AI Cloud) is a fulfillment system for provisioning Kubernetes clusters, compute instances, bare-metal hosts, and networking. This mono-repo contains seven components, each with its own `CLAUDE.md`/`AGENTS.md` — **read the component's docs before making changes in it**.

## Components

| Component | Description |
|-----------|-------------|
| `fulfillment-service/` | gRPC/REST API server + `osac` CLI (PostgreSQL, OPA) |
| `osac-operator/` | Kubernetes operator for CRDs (ClusterOrder, ComputeInstance, Tenant, networking) |
| `osac-aap/` | Ansible playbooks for infrastructure provisioning |
| `osac-installer/` | Helm-based three-phase deployment orchestrator |
| `bare-metal-fulfillment-operator/` | Kubernetes operator for bare-metal host pools |
| `osac-csi-driver/` | CSI meta-driver aggregating vendor storage drivers |
| `osac-metering/` | Metering pipeline — collects usage events via gRPC Watch, publishes CloudEvents to Kafka, and provides Provider Adapters framework for billing integrations |
| `tests/e2e/` | Cross-component e2e pytest suite (bmaas, vmaas, caas, catalog, storage) — runs against a deployed cluster's gRPC API |

See also [`docs/`](docs/README.md) for hand-trimmed cross-component architecture and
conventions content with no home in any single component's own `AGENTS.md`
(not to be confused with the external `docs` repo in the table below, which
covers broader project-level architecture guides and diagrams).

## External Repos

Clone as siblings for cross-repo workflows:

| Repo | Description |
|------|-------------|
| [osac-test-infra](https://github.com/osac-project/osac-test-infra) | E2E workflow orchestration (cluster lifecycle, image builds, secrets); test content has moved to `tests/e2e/` in this repo |
| [osac-ui](https://github.com/osac-project/osac-ui) | Web console (React, PatternFly 6) |
| [enhancement-proposals](https://github.com/osac-project/enhancement-proposals) | PRDs and design documents (two-stage EP flow) |
| [docs](https://github.com/osac-project/docs) | Architecture docs and guides |

## Cross-Component Changes

A feature spanning multiple components lands in a single branch and PR. Apply changes in dependency order:

```text
fulfillment-service (proto)
├→ osac-operator (CRDs, controllers)
│  ├→ osac-aap (playbooks)
│  └→ bare-metal-fulfillment-operator (bare metal types)
├→ osac-csi-driver (storage tier APIs)
├→ osac-metering (usage collection)
└→ osac-installer (RBAC, Helm) — depends on all above
```

For deployment coordination (image tags, submodules), see `osac-installer/AGENTS.md`.

## Knowledge Graph (graphify brain)

CI keeps a structural code graph of this mono-repo fresh (`.github/workflows/graphify-brain-refresh.yaml`) and publishes it for pickup. After `graphify` is installed (see below), a `SessionStart` hook (`.claude/hooks/fetch-graphify-brain.sh`) fetches the latest published bundle into `graphify-out/` automatically at the start of every session — no manual fetch step — and it fails open (falls back to normal cold exploration with a one-line warning) if the fetch fails, `graphify` isn't installed, or the graph is otherwise unavailable.

`graphify` itself needs to be installed once per developer machine before any of this does anything useful:

```bash
uv tool install graphifyy   # recommended
# or: pipx install graphifyy
```

Note the package name is `graphifyy` (double "y") — the CLI command itself is `graphify`, not a typo.

`graphify claude install` has already been run against this repo and its output committed (the `## graphify` section in `CLAUDE.md` and the `PreToolUse` hooks in `.claude/settings.json`) — the whole team inherits the consumption side by default, nothing to run yourself. It installs the CLAUDE.md directive and PreToolUse hook that *nudge* Claude Code to consult the graph before Bash/Grep/Read/Glob calls — an advisory reminder injected into context, not an enforced block; Claude Code can still proceed with a raw read if it chooses to (that would need `--strict` mode, not used in this install). The fetch hook above only keeps the graph's *data* current — this is what prompts something to actually read it.

Do **not** run `graphify hook install` or `graphify --watch` in this repo — those enable local-generation automation that rebuilds the graph from your own uncommitted local state, which would clobber the CI-fetched, org-wide graph with an incomplete single-machine view. Generation is centralized in CI by design.
