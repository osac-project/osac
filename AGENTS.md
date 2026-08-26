# OSAC Mono-Repo

OSAC (Open Sovereign AI Cloud) is a fulfillment system for provisioning Kubernetes clusters, compute instances, bare-metal hosts, and networking. This mono-repo contains seven components, each with its own `CLAUDE.md`/`AGENTS.md` — **read the component's docs before making changes in it**.

## Critical Rules

- **Never skip tenant isolation metadata** (`osac.openshift.io/tenant`, `osac.openshift.io/owner-reference` annotations) on new tenant-scoped resources
- **Always `buf lint` before committing** proto changes; regenerate with `buf generate`
- **Fork-based workflow**: push to `$PUSH_REMOTE`, never to `$UPSTREAM_REMOTE` — resolve via vendored `resolve-remotes.sh` (see [Git Workflow](#git-workflow))
- **AI attribution**: use an `Assisted-by: <tool> <contact>` trailer on commits, naming whichever AI tool actually did the work — never use `Co-Authored-By` for AI tools (Red Hat attribution standard)
- **Edit OSAC skills only in [`osac-project/osac-ai-skills`](https://github.com/osac-project/osac-ai-skills)** — `tools/bootstrap.sh` vendors that repo; do not treat local `skills/` as an editable source
- **Bump `metadata.version` in any skill you modify in `osac-ai-skills`** — that repo's `check-skill-version-bump` CI enforces semver patch/minor bumps
- When debugging Kubernetes operators, check for stale `vendor/` directories and cached images before rebuilding

## Dev Environment

Local toolchain: install Go, Node.js, buf, kubectl, kind, jira CLI, gh CLI, and jq.

For a local Kind cluster, see `osac-installer/AGENTS.md` (`PLATFORM=kind`, e.g. `make test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment`) and `fulfillment-service/README.md` for service-level setup. Distrobox/Containerfile tooling lives in `osac-workspace` and is not ported here.

After clone, run `tools/bootstrap.sh` to vendor AI skills and workflows (see [AI-Assisted Development](#ai-assisted-development)).

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

See also [`docs/`](docs/README.md) for hand-trimmed cross-component architecture and
conventions content with no home in any single component's own `AGENTS.md`
(not to be confused with the external `docs` repo in the table below, which
covers broader project-level architecture guides and diagrams).

## External Repos

Clone as siblings for cross-repo workflows:

| Repo | Description |
|------|-------------|
| [osac-test-infra](https://github.com/osac-project/osac-test-infra) | E2E pytest tests against the fulfillment-service gRPC API |
| [osac-ui](https://github.com/osac-project/osac-ui) | Web console (React, PatternFly 6) |
| [enhancement-proposals](https://github.com/osac-project/enhancement-proposals) | PRDs and design documents (two-stage EP flow) |
| [docs](https://github.com/osac-project/docs) | Architecture docs and guides |

## AI-Assisted Development

Run `tools/bootstrap.sh` once after clone (and anytime to refresh). It vendors
[`osac-ai-skills`](https://github.com/osac-project/osac-ai-skills) and
[`flightctl/ai-workflows`](https://github.com/flightctl/ai-workflows), then links
Claude Code / Cursor / Gemini CLI skill discovery under this repo. No separate
checkout of `osac-workspace` or a manual `osac-ai-skills` clone is required.
Do not run this script from an `osac/` nested inside `osac-workspace` — it
aborts (override: `OSAC_ALLOW_NESTED_BOOTSTRAP=1`). Use the workspace
`./bootstrap.sh` instead.

Edit OSAC-native skills only in `osac-project/osac-ai-skills`. Local `skills/`
and `.osac-ai-skills/` are bootstrap-managed and gitignored. Bump
`metadata.version` in any skill you change (see [Critical Rules](#critical-rules)).
PRD/design ingest reads `.design/context/` (fan-out from osac-ai-skills).

## Git Workflow

Fork-based push rules, branch naming, DCO sign-off, AI attribution, and PR
title conventions are shared across OSAC repos. After `tools/bootstrap.sh`,
the canonical copy is [`.claude/rules/dev-conventions.md`](.claude/rules/dev-conventions.md)
(a symlink into the vendored osac-ai-skills tree). `resolve-remotes.sh` is
hosted in osac-ai-skills and available at `~/.osac-ai-skills` or
`./.osac-ai-skills`.

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

For cross-component dependency checks (new CRD types, CLI flags), see
[`docs/CONVENTIONS.md`](docs/CONVENTIONS.md). For image tags and
per-component release tags, see [`osac-installer/AGENTS.md`](osac-installer/AGENTS.md)
— leave mono-repo component tags unpinned on a feature PR; bump `osac-ui`
when a new UI release is needed.

## Architecture

```text
osac/                              Mono-repo: fulfillment-service + osac-operator + osac-aap + osac-installer + bare-metal-fulfillment-operator + osac-csi-driver + osac-metering
  fulfillment-service              gRPC/REST API server, PostgreSQL, resource lifecycle
  osac-operator                    Kubernetes operator, provisions via AAP + Hosted Control Planes
  osac-aap                         Ansible playbooks for infrastructure provisioning
  osac-installer                   Helm charts, deploys all components to OpenShift
  bare-metal-fulfillment-operator  Kubernetes operator for bare metal fulfillment
  osac-csi-driver                  CSI storage driver, routes to vendor backends via storage tiers
  osac-metering                    Metering pipeline for usage events and Kafka publishing
osac-test-infra                    E2E test playbooks against fulfillment-service gRPC API
osac-ui                            Web console (React, PatternFly 6)
enhancement-proposals              Design documents and RFCs
docs                               Architecture docs and guides
```

### Resource Hierarchy

```text
Tenant → namespace and network isolation
ClusterOrder → OpenShift clusters via Hosted Control Planes
VirtualNetwork → L2 network with CIDR (child of NetworkClass)
  ├── Subnet → CIDR range within VirtualNetwork
  └── SecurityGroup → firewall rules
ComputeInstance → KubeVirt VM, attached to Subnets + SecurityGroups
Volume → CSI-backed disk from a storage tier
NATGateway → child of VirtualNetwork, SNATs egress traffic through an ExternalIP
ExternalIPPool → external IP address ranges
  ├── ExternalIP → allocated from pool
  └── ExternalIPAttachment → binds ExternalIP to ComputeInstance
```

### Operator Architecture (osac-operator)

The osac-operator uses controller-runtime to reconcile OSAC custom resources on Kubernetes. Key patterns:

- **All controllers follow the same reconciliation pattern**: finalizer → status update → provisioning/deprovisioning lifecycle
- **Shared provisioning lifecycle**: Controllers use `provisioning.RunProvisioningLifecycle()` for provision and manual deprovision handling
- **CRD types**: ClusterOrder, ComputeInstance, ExternalIP, ExternalIPAttachment, ExternalIPPool, NATGateway, SecurityGroup, Subnet, Tenant, VirtualNetwork, Volume
- **Multi-cluster support**: Controllers use `multicluster-runtime` for management/workload cluster separation
- **Management-state annotation**: All controllers should check `osac.openshift.io/management-state` and skip reconciliation when set to `Unmanaged`
- **Namespace isolation**: Networking controllers filter to a configured namespace via `NetworkingNamespacePredicate`

When fixing bugs or adding features, **check all controllers** that follow the same pattern — a bug in one controller likely exists in others. A missing feature in one controller is also a bug if all controllers are expected to behave consistently.

## Jira Conventions

Issue type is **Task**, not Story. Jira CLI usage and Tasks-not-Stories terminology are documented in the shared `dev-conventions` rule.

OSAC requires a **Component** on every created issue — Jira rejects creates without one. Inherit from the parent Feature; never invent a component the parent lacks. Set on create (`-C "<name>"` / MCP `components`).

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

The graph reflects committed file content only — it helps code-structure questions (tracing symbols, cross-component changes), but it does not help questions about live GitHub state (branch protection rules, actual required checks, run/failure history, current merge-queue state). Verify those directly with `gh api`/`gh run`, not by reading workflow file content.
