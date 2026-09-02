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

### Distrobox (Linux/x86_64)

See `tools/distrobox/Containerfile`. Requires [podman](https://podman.io/) and [distrobox](https://distrobox.it/) on Linux. Image tool binaries are x86_64 only.

```bash
make enter                     # Build image and enter distrobox
make status                    # Check image and distrobox status
make rebuild                   # Rebuild image from scratch
```

The distrobox shares your home directory by default (override with `HOME_DIR`). See `make help`.

### Local toolchain

Install Go, Node.js, buf, kubectl, kind, jira CLI, gh CLI, and jq.

### Local Kind cluster

See `osac-installer/AGENTS.md` (`PLATFORM=kind`, e.g. `make test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment`) and `fulfillment-service/README.md` for service-level setup.

After clone, run `tools/bootstrap.sh` to vendor AI skills and workflows (see [AI-Assisted Development](#ai-assisted-development)).

### Parallel worktrees

Source [`tools/osac-helpers.sh`](tools/osac-helpers.sh) and run `osac-new-worktree <branch>` from this repo. It adds a sibling worktree (`../osac-<branch-basename>`), runs `tools/bootstrap.sh` there (extra args after the branch are forwarded, e.g. `--no-fork` or `--fork-name origin`), and appends Jira context to `.claude/CLAUDE.md` when the branch name contains `OSAC-NNNN`. Override the parent directory with `OSAC_WORKTREE_PARENT`. Clean up with `git worktree remove` on that path (default `../osac-<suffix>`; with `OSAC_WORKTREE_PARENT`, `$OSAC_WORKTREE_PARENT/osac-<suffix>`).

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

`tools/bootstrap.sh` clones these into gitignored directories at this repo
root (skill-relative paths when this checkout is the project root). By
default it also forks the writeable three to your GitHub account (`origin` =
osac-project, `fork` = you — the `$PUSH_REMOTE` that `resolve-remotes.sh`
reports) so `/create-pr` has a push remote. `--fork-name origin` uses the
conventional GitHub layout on **writeable siblings only** (`origin` = you,
`upstream` = osac-project). Pick a name and stick with it; re-running with a
different name mutates sibling remotes. This osac checkout, `osac-ux`, and
vendor clones are never renamed — skills keep resolving remotes by URL.
`--no-fork` skips forking even if `--fork-name` is also passed. A later
`--no-fork` after `--fork-name origin` does not rewrite remotes back and
skips updates on siblings whose `origin` is already the fork (no `gh`).
Never forked:
`osac-ux`, `.osac-ai-skills`, `.ai-workflows`. Pass `--no-fork` for
read-only or CI clones (requires no `gh`). Default path requires
authenticated `gh`. The GitHub fork of [osac-project/docs](https://github.com/osac-project/docs)
is named `osac-docs` (not `docs`); extra mappings go in `tools/fork-overrides.sh`
(see `tools/fork-overrides.sh.example`).

| Repo | Local path | Fork remote | Description |
|------|------------|-------------|-------------|
| [osac-ui](https://github.com/osac-project/osac-ui) | `osac-ui/` | yes | Web console (React, PatternFly 6) |
| [osac-ux](https://github.com/osac-project/osac-ux) | `osac-ux/` | no | Read-only UI reference (`@temp-api` types) |
| [enhancement-proposals](https://github.com/osac-project/enhancement-proposals) | `enhancement-proposals/` | yes | PRDs and design documents (two-stage EP flow) |
| [docs](https://github.com/osac-project/docs) | `osac-docs/` | yes | Architecture guides and personas |

In-tree [`docs/`](docs/) (`ARCHITECTURE.md`, `CONVENTIONS.md`) is **not**
the osac-project/docs checkout at `osac-docs/`. Skills read
`osac-docs/personas.md`.

[osac-test-infra](https://github.com/osac-project/osac-test-infra) is **not**
cloned. E2E pytest suites live in [`tests/e2e/`](tests/e2e/).
osac-test-infra keeps infrastructure backends, reusable e2e caller
workflows, and a `tests/` placeholder — do not add or modify suites
there. Clone osac-test-infra only for infra or `/debug-e2e` work.

## UI Reference (osac-ux)

`osac-ux/` is cloned read-only from [osac-project/osac-ux](https://github.com/osac-project/osac-ux).
No PRs against it from backend workflow sessions.

### What to read during /design:research and /implement:ingest

| Path | Purpose |
|------|---------|
| `osac-ux/libs/ui-components/src/pages/tenant/` | Tenant screens — form fields, list columns, actions |
| `osac-ux/libs/ui-components/src/pages/provider/` | Provider admin screens |
| `osac-ux/libs/ui-components/src/pages/admin/` | Tenant admin screens |
| `osac-ux/libs/ui-components/src/api/v1/` | @temp-api types — use as primary proto field input |
| `osac-ux/apps/e2e/cypress/e2e/flows/` | User journeys for Cypress scenario planning |

For **any EP**, if `osac-ux/libs/ui-components/src/api/v1/<resource>.ts` exists,
use those TypeScript fields as proto names (camelCase → snake_case) and include
a `## UX Alignment` mapping table. `cd osac-ux && node scripts/gen-api-diff.mjs`
surfaces API gaps against the current UI.

## AI-Assisted Development

Run `tools/bootstrap.sh` once after clone (and anytime to refresh). It vendors
[`osac-ai-skills`](https://github.com/osac-project/osac-ai-skills) and
[`flightctl/ai-workflows`](https://github.com/flightctl/ai-workflows), clones
the [External Repos](#external-repos), and links Claude Code / Cursor / Gemini
CLI skill discovery under this repo. This checkout is the project root — no
separate `osac-workspace` or manual `osac-ai-skills` clone is required.

A directory nested as `osac-workspace/osac/` still aborts (override:
`OSAC_ALLOW_NESTED_BOOTSTRAP=1`) so it cannot install a second skill overlay.
Use a standalone clone or worktree of this repo, not that nested copy. For a
clone-only sibling pass (no GitHub forks), use `tools/bootstrap.sh --no-fork`.
`--fork-name origin` only rearranges remotes on writeable siblings (see
[External Repos](#external-repos)).

Edit OSAC-native skills only in `osac-project/osac-ai-skills`. Local `skills/`
and `.osac-ai-skills/` are bootstrap-managed and gitignored, as are the
sibling checkouts listed under [External Repos](#external-repos). Bump
`metadata.version` in any skill you change (see [Critical Rules](#critical-rules)).
PRD/design ingest reads `.design/context/` (fan-out from osac-ai-skills).

Recommended skill sequence (Feature → PRD → Design → Jira sync → Implement →
E2E) lives in osac-ai-skills, not in this repo. After bootstrap, see
`~/.osac-ai-skills/README.md` or `.osac-ai-skills/README.md` (section
**Recommended Skill Sequence**), or the
[upstream README](https://github.com/osac-project/osac-ai-skills#recommended-skill-sequence).

## Enhancement Proposals

OSAC uses the flightctl PRD and design skills with project-level template
overrides. Both documents publish to the `enhancement-proposals` sibling
cloned by bootstrap.

### Docs repo and paths

- Local path: `./enhancement-proposals/` — give this path when `/prd:publish`
  or `/design:publish` asks for the docs repo
- Skip the "release" question — use `enhancements` as the fixed directory prefix
- Feature directory: `enhancements/<jira-key>-<feature-slug>/`, where
  `<jira-key>` is the Jira **Feature**-level key exactly as it appears in Jira
  (no zero-padding), e.g. `enhancements/OSAC-42-example-feature/`
- PRD filename: `prd.md`; design (EP) filename: `design.md`; both in that
  same directory

### Feature dimensions and templates

PRD and design ingest must read all files in `.design/context/`:

- **`osac-dimensions.md`** — which cross-cutting dimensions apply; guides
  clarifying questions, persona/user-story scope, and design coverage (see
  also `osac-docs/personas.md`)
- **`review-patterns.md`** — common design-reviewer themes and anti-patterns

Templates live in the sibling clone; section guidance is vendored from
osac-ai-skills (edit there, not the local copy):

- Design: `enhancement-proposals/guidelines/design_template.md` and
  `.design/templates/section-guidance.md`
- PRD: `enhancement-proposals/guidelines/prd_template.md` and
  `.prd/templates/section-guidance.md`

Design and implement ingest must read the `AGENTS.md` of each affected
component. For fulfillment-service API work (proto, services,
request/response), [`fulfillment-service/docs/API.md`](fulfillment-service/docs/API.md)
is canonical — see also [`fulfillment-service/AGENTS.md`](fulfillment-service/AGENTS.md).

## E2E tests

`/e2e` writes pytest suites in [`tests/e2e/`](tests/e2e/) in this repo.
Discover patterns from `tests/e2e/<suite>/` and
[`tests/e2e/core/`](tests/e2e/core/). Do not add, modify, or delete test suites in
[osac-test-infra](https://github.com/osac-project/osac-test-infra) —
osac-test-infra's `tests/` tree is a placeholder plus the infrastructure backend
contract. `/debug-e2e` still lives there; clone that repo only when
debugging Prow or changing infra backends.

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
  tests/e2e/                         E2E pytest suites (BMaaS, VMaaS, CaaS, catalog, storage, references)
osac-ui                            Web console (React, PatternFly 6)
osac-ux                            Read-only UI reference (@temp-api)
enhancement-proposals              Design documents and RFCs
osac-docs                          Architecture docs and guides (osac-project/docs)
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
