# AGENTS.md — docs

Documentation content for the OSAC project: architecture guides, feature documentation, and developer guides. No application code, no build system, no tests — pure Markdown content with PlantUML diagrams.

This directory is part of the `osac-project/osac` mono-repo (formerly the
separate `osac-project/docs` repository, merged in with its full commit
history). It follows the mono-repo's own contribution workflow, described in
the repository root [`AGENTS.md`](../AGENTS.md) — fork/branch/commit/PR
conventions below are superseded by that file where they conflict. The
sections below describing this content's own organization and conventions
(file layout, PlantUML generation, Markdown style) remain accurate and
predate the merge; a follow-up will reorganize/update them for their new
location.

## What This Repo Contains

This directory also holds the mono-repo's pre-existing cross-component
conventions files (`ARCHITECTURE.md`, `CONVENTIONS.md`,
`INTEGRATION-TESTING.md`, `RELEASING.md`, `codex-getting-started.md`,
`pr-dashboard/`) — see [`README.md`](README.md) for those; this section
covers only the project documentation merged in from the former
`osac-project/docs` repo.

**Architecture documentation** (`architecture/`)
- `cluster-fulfillment.md` — Cluster fulfillment workflows
- `vm-fulfillment.md` — VM fulfillment patterns
- `publicip-networking.md` — PublicIP networking architecture
- `aap-provisioning/` — AAP provisioning state machines and sequence diagrams (PlantUML sources)
- `bm-server-fulfillment.md` — Bare metal server fulfillment

**Guides** (`guides/`)
- `keycloak-configuration.md`, `keycloak-upgrade-rollback.md` — Keycloak setup and upgrade/rollback
- `customer-ldap-federation-guide.md` — Customer LDAP federation
- `tenant-identity-and-access-guide.md` — Tenant identity and access
- `admin/` — `bcm-backend.md`, `metal3-backend.md`
- `developer/` — Tenant setup, ComputeInstance creation/catalog items, InstanceType management, PublicIP allocation, networking examples

**Feature documentation** (`features/`)
- `MGMT-22670-console-access.md` — Console access patterns
- `netris-caas-networking.md` — Netris CaaS networking integration

**Networking lab guides** (`networking/`)
- `setup-bpg-vrf-lite/README.md` — ClusterUserDefinedNetwork (CUDN) with provider network integration lab

**Root documentation**
- `designdoc.md` — AI-in-a-Box high-level design document (working document)
- `importing-esi-nodes.md` — Steps to import an ESI node into ACM
- `personas.md` — User archetypes and use cases
- `AI-POLICY.md` — Transparency and disclosure requirements for AI-assisted contributions

**Diagrams**: PlantUML source files (`.puml`) with container-based PNG generation. Standalone images (`images/`) support root-level documents.

## File Organization

```text
docs/
├── architecture/
│   ├── aap-provisioning/         # AAP state machines, sequence diagrams, PlantUML generation script
│   ├── bm-server-fulfillment.md
│   ├── cluster-fulfillment.md
│   ├── publicip-networking.md
│   ├── README.md
│   └── vm-fulfillment.md
├── features/
│   ├── MGMT-22670-console-access.md
│   ├── netris-caas-networking.md
│   └── README.md
├── guides/
│   ├── admin/
│   │   ├── bcm-backend.md
│   │   └── metal3-backend.md
│   ├── developer/
│   │   ├── computeinstance-catalogitem-guide.md
│   │   ├── computeinstance-guide.md
│   │   ├── instancetype-guide.md
│   │   ├── networking-guide.md
│   │   ├── publicip-guide.md
│   │   └── tenant-setup.md
│   ├── customer-ldap-federation-guide.md
│   ├── keycloak-configuration.md
│   ├── keycloak-upgrade-rollback.md
│   └── tenant-identity-and-access-guide.md
├── images/                        # Standalone diagrams for root-level docs
├── networking/
│   └── setup-bpg-vrf-lite/        # CUDN provider network integration lab
├── AI-POLICY.md
├── designdoc.md
├── importing-esi-nodes.md
├── OWNERS                         # Prow approvers/reviewers
├── personas.md
└── README.md
```

## Development Workflow

### Setup

Clone and set up remotes per the mono-repo root [`AGENTS.md`](../AGENTS.md)'s
"Mandatory Git and contribution workflow" (fork as `origin`, base off
`upstream/main`). No dependencies to install for docs content itself, no
services to run — use any Markdown editor.

### Editing Documentation

1. **Find or create a Jira issue** (see root `AGENTS.md`) or use a `NO-ISSUE:` commit/PR prefix.
2. **Get stakeholder feedback** on proposed changes before starting
3. **Create a feature branch** with descriptive name (e.g., `feat/update-vm-fulfillment-guide`)
4. **Edit Markdown files** directly
5. **Regenerate diagrams** if modifying `.puml` files (see below)
6. **Commit with DCO sign-off**: `git commit -s`
7. **Add AI attribution** if AI-assisted:
   ```text
   Assisted-by: Claude Code <noreply@anthropic.com>
   ```

### PlantUML Diagram Generation

If you modify `.puml` files in `architecture/aap-provisioning/`:

```bash
cd architecture/aap-provisioning/
./generate_images.sh  # Requires Docker or Podman
```

The script:
- Uses `docker.io/plantuml/plantuml:latest` container
- Converts `.puml` to PNG with `:Z` SELinux label for volume mounts
- Moves generated images from `diagrams/` to `images/` subdirectory

**When to regenerate**: Only if you modified `.puml` source files. Pre-generated PNGs are committed to the repo.

**PlantUML file format**: PlantUML diagrams use simple text-based syntax. Example:
```plantuml
@startuml
!theme plain
actor User
participant "Fulfillment API" as API
participant "AAP Controller" as AAP
User -> API: Create VirtualNetwork
API -> AAP: Provision Network
AAP --> API: Success
API --> User: VirtualNetwork Ready
@enduml
```

See existing `.puml` files in `architecture/aap-provisioning/diagrams/` for examples.

## Pull Request Workflow

### Before Opening PR

- Ensure all Markdown changes are complete
- Regenerate diagrams if you modified `.puml` files
- Verify internal links still resolve (check relative paths in `[text](../path/file.md)` links)
- Check that examples in developer guides are accurate
- Review content for factual accuracy against source code and actual behavior
- Ensure DCO sign-off is present on all commits (`git commit -s`)

### PR Process

Follow the mono-repo root [`AGENTS.md`](../AGENTS.md)'s git and contribution
workflow: push to your fork (never to `upstream`), open the PR against
`osac-project/osac:main` with an `OSAC-XXXX:`/`NO-ISSUE:`-prefixed title,
link the issue, and summarize the documentation changes. This directory's own
`OWNERS` file no longer gates review directly (approval follows the
mono-repo's own review process); reconciling or retiring it is part of the
follow-up reorg.

## Documentation Conventions

### Markdown Style

- **Heading hierarchy**: Use `#` for title, `##` for major sections, `###` for subsections
- **Code blocks**: Fence with triple backticks and language identifier
- **Internal links**: Relative paths (e.g., `[Personas](../personas.md)`)
- **Examples**: Include concrete commands, API payloads, troubleshooting steps in developer guides

### Content Organization

- **Architecture docs**: Technical depth with sequence diagrams, state machines, component relationships
- **Feature docs**: User-facing explanations of what features do and how to use them
- **Developer guides**: Step-by-step procedures with examples and troubleshooting

### Diagram Best Practices

- **Image sizing**: Keep diagrams readable (max width ~1200px for high-DPI displays)
- **Alt text**: Always provide descriptive alt text for accessibility: `![Alt text description](./image.png)`
- **File format**: Use PNG for PlantUML-generated diagrams; SVG acceptable for other vector graphics
- **Placement**: Embed diagrams near the relevant text that explains them

### Cross-References

Link to related content in other OSAC repos, or to the relevant subdirectory of
the `osac` mono-repo:
- Enhancement proposals: `https://github.com/osac-project/enhancement-proposals`
- API definitions: `https://github.com/osac-project/osac/tree/main/fulfillment-service`
- Operator implementation: `https://github.com/osac-project/osac/tree/main/osac-operator`

### Versioning and Release-Specific Content

This repository documents the **current development version** of OSAC. There is no branching strategy for documentation versions — all content reflects the latest `main` branch state.

**If documenting version-specific behavior**:
- Add a note at the top of the section: `> **Note**: As of OSAC v1.2, behavior changed to...`
- Use conditional wording: "In versions prior to 1.2..." vs. "Starting in version 1.2..."
- Link to enhancement proposals for context on when features were introduced

**For breaking changes**: Update both the architecture docs and developer guides to reflect the new behavior, with callouts for users on older versions.

## Common Tasks

### Add a new architecture document

1. Create directory under `architecture/<feature-name>/`
2. Add `README.md` with architectural overview
3. Include diagrams (PlantUML or embedded images)
4. Update root `README.md` if it's a major new section
5. Cross-reference from related docs

### Update a developer guide

1. Navigate to `guides/developer/<guide-name>.md`
2. Update procedures, examples, or troubleshooting
3. Test commands/examples if they reference live systems
4. Add version context if behavior changed in a release

### Add PlantUML diagrams

Currently, PlantUML generation is only set up for `architecture/aap-provisioning/`:

1. Create `.puml` file in `architecture/aap-provisioning/diagrams/`
2. Run `architecture/aap-provisioning/generate_images.sh`
3. Commit both `.puml` source and generated PNG
4. Embed in Markdown: `![Diagram Title](./images/diagram.png)`

**To add PlantUML diagrams in other directories**: Adapt the `generate_images.sh` script for the new location (update `SCRIPT_DIR`, `DIAGRAMS_DIR`, and `IMAGES_DIR` paths).

## Access Control

**OWNERS file** defines:
- **Approvers**: 22 total (9 architects + 6 leads + 7 managers)
- **Reviewers**: 27 total (includes all 22 approvers + 5 additional contributors)

Prow uses this file for PR approval workflows. To merge a PR, you need:
- LGTM from at least one reviewer
- `/approve` from at least one approver
- All CI checks passing

**CI Checks**: This repository has minimal automated checks (no build system, no tests). CI typically validates:
- DCO sign-off presence on commits
- Basic Markdown linting (if configured)
- Prow approval workflow compliance

## AI Policy Compliance

When using AI assistance (Claude Code, GitHub Copilot, etc.):

1. **Disclosure**: Add commit trailer:
   ```text
   Assisted-by: Claude Code <noreply@anthropic.com>
   ```
   or
   ```text
   Generated-By: Claude Code <noreply@anthropic.com>
   ```

2. **Review**: Verify factual accuracy of AI-generated documentation against source code and actual behavior

3. **Transparency**: Full policy in `AI-POLICY.md` — read before contributing AI-assisted documentation

## Troubleshooting

### PlantUML Generation Failures

**Container not found**:
```bash
docker pull docker.io/plantuml/plantuml:latest
# or
podman pull docker.io/plantuml/plantuml:latest
```

**SELinux permission denied**:
- The `:Z` flag in the volume mount should handle SELinux contexts automatically
- If issues persist, check SELinux mode: `getenforce`
- Temporarily test with `sudo setenforce 0` (re-enable after: `sudo setenforce 1`)

**Syntax errors in .puml files**:
- Validate PlantUML syntax at http://www.plantuml.com/plantuml/uml/
- Check for missing `@startuml` / `@enduml` tags
- Verify theme compatibility (use `!theme plain` for consistency)

### Merge Conflicts

When updating documentation that others have modified, rebase onto
`upstream/main` per the root `AGENTS.md` workflow, resolve conflicts in the
Markdown files, and force-push your branch to your fork (`origin`) with
`--force-with-lease`.

### DCO Sign-Off

Configure git to automatically sign off commits:
```bash
git config --global user.name "Your Name"
git config --global user.email "your.email@example.com"
# Always use -s flag: git commit -s
```

Or set up a commit template with DCO trailer in `~/.gitmessage`.

## Security Notes

- **Public repository**: All content is open-source under Apache 2.0 license
- **No secrets**: Documentation contains no credentials, API keys, or sensitive data
- **Container security**: PlantUML generation script uses official Docker Hub image with SELinux-aware volume mounts (`:Z` flag)
- **Access control**: GitHub-based with OWNERS file integration

## Key Files

| File | Purpose |
|------|---------|
| `OWNERS` | Prow approver/reviewer definitions |
| `AI-POLICY.md` | AI assistance disclosure requirements |
| `personas.md` | User archetypes referenced in architecture docs |
| `README.md` | Project overview and contribution workflow |
| `architecture/aap-provisioning/generate_images.sh` | PlantUML diagram generation |

## Related Components and Repositories

Documentation references code and designs from:
- [`../fulfillment-service/`](../fulfillment-service/) — gRPC API definitions and server implementation
- [`../osac-operator/`](../osac-operator/) — Kubernetes operator for cluster/VM provisioning
- [`../osac-aap/`](../osac-aap/) — Ansible Automation Platform roles
- [`../osac-installer/`](../osac-installer/) — Installation manifests and setup scripts
- [`enhancement-proposals`](https://github.com/osac-project/enhancement-proposals) — Design RFCs and feature proposals (still a separate repo)

When documenting new features, verify implementation details in the relevant component directory before finalizing architecture or developer guide content.

## Quick Reference

```bash
# From an osac mono-repo checkout, on a branch based on upstream/main
git checkout -b feat/update-architecture-docs upstream/main

# Edit Markdown files
vim docs/architecture/cluster-fulfillment.md

# Regenerate diagrams (if needed)
cd docs/architecture/aap-provisioning/
./generate_images.sh
cd -

# Commit with DCO and AI attribution
git add docs/
git commit -s -m "NO-ISSUE: update cluster fulfillment architecture

Document new HostedControlPlane integration patterns.

Assisted-by: Claude Code <noreply@anthropic.com>"

# Push to your fork (origin) and open a PR against osac-project/osac:main
git push origin feat/update-architecture-docs
```
