# OSAC

This is the mono-repo for the [Open Sovereign AI Cloud (OSAC)](https://github.com/osac-project)
project. It hosts multiple components as subdirectories, each retaining its own
documentation:

- **[fulfillment-service/](fulfillment-service/README.md)** — a gRPC server (with REST gateway)
  that manages infrastructure resources such as clusters, hosts, compute instances, and
  networking. It uses PostgreSQL for storage and OPA for authorization, and ships an `osac` CLI
  alongside the service binary.
- **[osac-operator/](osac-operator/README.md)** — a Kubernetes operator that reconciles the
  custom resources created by the fulfillment service (or elsewhere), such as `ClusterOrder`,
  `ComputeInstance`, `Tenant`, `VirtualNetwork`, `Subnet`, and `SecurityGroup`. It provisions
  infrastructure via Ansible Automation Platform and includes a console proxy for KubeVirt VM
  console/VNC access.
- **[osac-aap/](osac-aap/README.md)** — the Ansible automation layer: playbooks, roles, and
  collections that provision and manage infrastructure resources (networking, compute,
  bare-metal hosts, OpenShift clusters) when triggered by osac-operator via Ansible Automation
  Platform (AAP).
- **[osac-csi-driver/](osac-csi-driver/README.md)** — an aggregating CSI meta-driver that
  presents a single CSI identity to Kubernetes and routes storage requests to vendor-specific
  CSI drivers (NetApp Trident, VAST, Pure Storage) based on storage tier resolution from the
  fulfillment service.

See each subdirectory's `README.md` (and `docs/`, where present) for setup, build, test, and
deployment instructions specific to that component. This repo's own top-level
**[docs/](docs/README.md)** holds hand-trimmed cross-component architecture and
conventions content that doesn't belong in any single component's docs (not to be confused
with the external [osac-project/docs](https://github.com/osac-project/docs) repo, which
covers broader project-level architecture guides and diagrams).

## Local development with go.work

The root [`go.work`](go.work) file wires all Go modules in the mono-repo —
`fulfillment-service`, `osac-operator` (plus its `api` submodule),
`bare-metal-fulfillment-operator`, `osac-csi-driver`, and the three `osac-metering`
modules (`schema`, `metering-service`, `adapters`) — together as a Go workspace, so
cross-module changes can be built and tested locally without publishing intermediate
versions. Go tooling run from the repo root will automatically use the workspace; no
extra flags are needed.

## AI-assisted development

After clone, run `tools/bootstrap.sh` from this repo root. It vendors
[osac-ai-skills](https://github.com/osac-project/osac-ai-skills) and
[flightctl/ai-workflows](https://github.com/flightctl/ai-workflows), clones
skill-relative sibling repos (see `AGENTS.md`), and links Claude Code / Cursor /
Gemini CLI skill discovery. Do not run it from an `osac/` nested inside
`osac-workspace`.

The Feature → PRD → Design → Jira sync → Implement → E2E sequence is documented in
[osac-ai-skills](https://github.com/osac-project/osac-ai-skills#recommended-skill-sequence)
(local after bootstrap: `~/.osac-ai-skills/README.md` or
`.osac-ai-skills/README.md`). See [`AGENTS.md`](AGENTS.md)
for bootstrap details and component conventions.

> [!WARNING]
> Be mindful of the content you commit to this repository. Do not commit any
> material containing Red Hat confidential content, including information about
> future product development plans.
