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

## Verifying container image signatures

Container images published to `ghcr.io/osac-project/*` from this repo's GitHub
Actions workflows are signed keylessly with [cosign](https://docs.sigstore.dev/),
using each workflow run's GitHub Actions OIDC identity via Fulcio/Rekor — no
long-lived private key is involved. Images are signed both from ordinary
pushes to `main` and from component-scoped release tags; the certificate
identity's workflow filename and ref reflect whichever build produced the
image, so pin both rather than accepting any workflow or any tag in this repo:

| Component (+ manifest image, where built) | Image                                 | Workflow file                             | Release tag prefix                |
|--------------------------------------------|----------------------------------------|---------------------------------------------|------------------------------------|
| osac-operator                               | `osac-project/osac-operator`           | `build-image.yaml`                          | `osac-operator`                    |
| fulfillment-service                         | `osac-project/fulfillment-service`     | `publish-image.yaml`                        | `fulfillment-service`              |
| bare-metal-fulfillment-operator             | `osac-project/bare-metal-fulfillment-operator` | `build-bmf-image.yaml`              | `bare-metal-fulfillment-operator`  |
| osac-aap                                    | `osac-project/osac-aap`                | `execution-environment.yml`                 | `osac-aap`                         |
| metering-service                            | `osac-project/metering-service`        | `build-metering-service-image.yaml`         | `osac-metering`                    |
| metering-m360-adapter                       | `osac-project/metering-m360-adapter`   | `build-metering-m360-adapter-image.yaml`    | `osac-metering`                    |
| metering-echo-adapter                       | `osac-project/metering-echo-adapter`   | `build-metering-echo-adapter-image.yaml`    | `osac-metering`                    |
| osac-csi-driver                             | `osac-project/osac-csi-driver`         | `publish-csi-driver-image.yaml`             | `osac-csi-driver`                  |

`nightly-build.yaml` independently rebuilds and republishes every image above
on its own schedule (`schedule`/`workflow_dispatch`, always off `main`), so
`nightly-build.yaml@refs/heads/main` is also a valid signer identity for any
image in this table — not just the workflow listed. Which identity you
actually see on a given digest depends on which workflow signed it first: if
a night's rebuild is byte-identical to that day's regular build, the digest
already carries the regular workflow's signature and nightly never re-signs
it; a rebuild that differs gets its own `nightly-build.yaml` signature.

Verify an image, substituting the workflow file and tag prefix from the table above:

```bash
cosign verify \
  --certificate-identity-regexp '^https://github\.com/osac-project/osac/\.github/workflows/<workflow-file>@refs/(heads/main|tags/<tag-prefix>/.+)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/<image>@sha256:<digest>
```

For example, to verify an osac-operator image:

```bash
cosign verify \
  --certificate-identity-regexp '^https://github\.com/osac-project/osac/\.github/workflows/build-image\.yaml@refs/(heads/main|tags/osac-operator/.+)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/osac-project/osac-operator@sha256:<digest>
```

## Verifying Helm chart signatures

Helm charts published to `oci://ghcr.io/osac-project/charts/*` are signed the
same way. All charts built from this mono-repo's own components (everything
except `osac`, the umbrella chart) are published by `publish-charts.yaml`,
which only ever runs via `workflow_run` off the repository's default branch
(`main`) — never a tag ref, regardless of which component's tag triggered the
originating image build. `publish-osac-installer-chart.yaml` (the umbrella
chart) is `workflow_dispatch`-only and normally also runs from `main`, but can
be dispatched against one of the umbrella chart's own `osac/v*` tags.

`nightly-build.yaml` also independently packages and republishes every chart
above (including the umbrella chart) as part of its nightly run, off `main`.
The same rule as images applies: `nightly-build.yaml@refs/heads/main` is a
valid alternate identity for any chart here, but you'll only see it on a
digest whose nightly rebuild actually differed from the existing published
chart — byte-identical rebuilds keep the earlier workflow's signature rather
than being re-signed.

`helm pull`/`helm push` print the artifact's digest directly, so no extra
tooling is needed to resolve it:

```bash
helm pull oci://ghcr.io/osac-project/charts/<chart-name> --version <version>
# Digest: sha256:<digest>

cosign verify \
  --certificate-identity-regexp '^https://github\.com/osac-project/osac/\.github/workflows/publish-charts\.yaml@refs/heads/main$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/osac-project/charts/<chart-name>@sha256:<digest>
```

For the umbrella chart, accept either ref:

```bash
cosign verify \
  --certificate-identity-regexp '^https://github\.com/osac-project/osac/\.github/workflows/publish-osac-installer-chart\.yaml@refs/(heads/main|tags/osac/.+)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/osac-project/charts/osac@sha256:<digest>
```

Always verify by digest (`@sha256:...`), not by mutable tag — resolve a tag to
its digest first with `skopeo inspect docker://ghcr.io/osac-project/<component>:<tag>`
if needed. Images pushed to `quay.io/redhat-user-workloads/osac-tenant/...` via
Konflux are signed separately by Konflux's own Enterprise Contract pipeline;
see that pipeline's documentation for verifying those instead.

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
skill-relative sibling repos (see `AGENTS.md`), forks the writeable siblings
to your GitHub account, and links Claude Code / Cursor / Gemini CLI skill
discovery. Requires an authenticated `gh` session unless you pass `--no-fork`.
`--fork-name origin` sets writeable sibling remotes to `origin` = your fork and
`upstream` = osac-project; it does not change this checkout or skill vendor
remotes. `--no-fork` wins over `--fork-name`. After `--fork-name origin`, a
later `--no-fork` run skips updates on those origin-as-fork siblings rather
than calling `gh`. The GitHub fork of `osac-project/docs` is `osac-docs`. This
repo is the project root. A nested `osac-workspace/osac/` checkout aborts;
use a standalone clone or worktree instead.

The Feature → PRD → Design → Jira sync → Implement → E2E sequence is documented in
[osac-ai-skills](https://github.com/osac-project/osac-ai-skills#recommended-skill-sequence)
(local after bootstrap: `~/.osac-ai-skills/README.md` or
`.osac-ai-skills/README.md`). See [`AGENTS.md`](AGENTS.md)
for bootstrap details and component conventions.

Using OpenAI Codex? See [`docs/codex-getting-started.md`](docs/codex-getting-started.md)
for Codex-specific onboarding (install, `/import`, permissions, trusting the
repo's hooks, and skill discovery under `.agents/skills`).

## Distrobox (Linux/x86_64)

Requires [podman](https://podman.io/) and [distrobox](https://distrobox.it/) on Linux. Image tool binaries are x86_64 only. From this repo root:

```bash
make enter                     # Build image and enter
make claude                    # Run Claude Code inside the distrobox
make status
make rebuild
```

The image lives in `tools/distrobox/`. It shares `$HOME` by default (`HOME_DIR` to override).

## Parallel worktrees

```bash
source tools/osac-helpers.sh
osac-new-worktree feat/OSAC-1234
```

Creates `../osac-OSAC-1234` by default (or `$OSAC_WORKTREE_PARENT/osac-OSAC-1234`), checks out the new branch, and runs `tools/bootstrap.sh` (extra args after the branch are forwarded, e.g. `--no-fork` or `--fork-name origin`). Remove with `git worktree remove` on that path from the original clone.

> [!WARNING]
> Be mindful of the content you commit to this repository. Do not commit any
> material containing Red Hat confidential content, including information about
> future product development plans.
