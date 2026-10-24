# OSAC

## Introduction

There is a worldwide trend towards local and specialized clouds, where
governments and service providers want to offer their own cloud services under
local jurisdiction and specific compliance regimes. Use cases include
traditional VMaaS clouds, neoclouds, and sovereign clouds.

Open Sovereign AI Cloud (OSAC) is an open-source project for organizations
standing up their own clouds. It offers multi-tenant self-service provisioning
of VMs, OpenShift clusters, bare-metal servers, Model-aaS, and more. OSAC
offers standard cloud features including tenancy, RBAC, quota, metering, and
tenant isolation at every layer.

OSAC interfaces:
* **gRPC API**: scalable, secure, and safe to put in front of unrelated tenants. It is standards-based and designed for automation.
* **CLI**: an out-of-the-box CLI for admins and tenants to accomplish their work with OSAC.
* **UI**: a brandable web interface for service providers who prefer an out-of-the-box UI vs building their own.

## Core Services

**Bare Metal-aaS** (BMaaS) allows tenants to allocate groups of computers, place
those computers onto isolated networks, and manage/configure those computers
themselves.  BMaaS is needed by tenants who want to install their own workload
management software (e.g., SLURM), and tenants who want OpenShift clusters with
bare metal nodes.

**VMaaS** allows tenants to create virtual machines using primitives that are
familiar to users of public clouds. VMaaS utilizes [Kubevirt](https://kubevirt.io/)
as the backend VM platform.

**Cluster-aaS** creates OpenShift clusters on demand. By default it uses [Hosted
Control
Planes](https://www.redhat.com/en/topics/containers/what-are-hosted-control-planes)
to achieve the best compute density, provision quickly, and give the service
provider exclusive access to manage critical parts of the control plane.
Cluster-aaS utilizes BMaaS and VMaaS to provision nodes.

**Model-aaS** (MaaS) delivers token-based access to cloud-local inference
endpoints running a curated selection of models. MaaS builds on [OpenShift AI's
MaaS](https://www.redhat.com/en/products/ai/openshift-ai), which is implemented
with [vLLM](https://vllm.ai/).

In addition to the above, OSAC includes a number of supporting services such as
standard cloud storage features and isolated networking via a full Virtual
Private Cloud (VPC) implementation.

## Customization

Each Cloud Service Provider (CSP) makes their own choices about the supporting
infrastructure on which their cloud runs. Those choices include server hardware,
network gear and fabric, GPU selection, hardware inventory, storage solution,
DNS platform, secret store, etc. OSAC needs to interface with each of those
while provisioning and managing cloud services.

Furthermore, CSPs have good reason to customize the details of how provisionable
assets, such as VMs and Clusters, get implemented. For example a CSP may need to
influence the way kubevirt APIs are utilized in order to include
hardware-specific optimizations or other features. Or they may need to customize
the way OpenShift clusters are created in order to turn on or off certain
features.

OSAC comes out of the box with working default integrations, while enabling the
CSP to customize or even replace portions of OSAC's workflows. OSAC does so by
utilizing Ansible roles to implement those portions of workflows that CSPs may
need to customize.

[Ansible Automation
Platform](https://www.redhat.com/en/technologies/management/ansible) (AAP) comes
with an extensive [ecosystem of
Collections](https://docs.ansible.com/projects/ansible/latest/collections/index.html)
that can interface with most of the infrastructure that would be found in a
datacenter. That ecosystem, combined with AAP's job management capabilities,
make AAP an ideal execution engine for OSAC.

## Code Layout

This is the mono-repo for the [Open Sovereign AI Cloud (OSAC)](https://github.com/osac-project)
project. It hosts multiple components as subdirectories, each retaining its own
documentation:

- **[docs/](docs/README.md)** — documentation of features and architecture.
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
- **[osac-metering/](osac-metering/README.md)** — the metering pipeline: watches the
  fulfillment service's gRPC event stream, maps resource lifecycle events to CloudEvents via a
  shared schema, and publishes them to Kafka for downstream billing adapters.

See each subdirectory's `README.md` (and `docs/`, where present) for setup, build, test, and
deployment instructions specific to that component. This repo's top-level
**[docs/](docs/README.md)** holds both hand-trimmed cross-component architecture and
conventions content and the broader project-level documentation (features, architecture
guides, admin/developer guides — formerly the separate `osac-project/docs` repo, merged
in with its full commit history).

## Verifying container image signatures

Container images published to `ghcr.io/osac-project/*` from this repo's GitHub
Actions workflows are signed keylessly with [cosign](https://docs.sigstore.dev/),
using each workflow run's GitHub Actions OIDC identity via Fulcio/Rekor — no
long-lived private key is involved.

`osac-build-and-publish.yaml` (called by `nightly-build.yaml` and
`osac-release.yaml`) is the sole publisher of any image at a real version —
each component's own tag-triggered build workflow only reacts to a push to
`main` now (for a `sha-<short>`-tagged dev image) or a pull request (build
only, never pushed or signed); pushing a `<component>/vX.Y.Z` tag doesn't
trigger anything. So `osac-build-and-publish.yaml@refs/heads/main` is the one
signer identity for any real-version image in this repo:

```bash
cosign verify \
  --certificate-identity-regexp '^https://github\.com/osac-project/osac/\.github/workflows/osac-build-and-publish\.yaml@refs/heads/main$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/<image>@sha256:<digest>
```

`nightly-build.yaml` always rebuilds and republishes every component's image
as part of its nightly run. `osac-release.yaml` only rebuilds and republishes
images whose components are actually selected for rebuilding (named in that
release's `component_versions`, and not already published at the requested
version) — every other component is pinned to its existing published image,
untouched, not resigned. Signing isn't skipped for an already-signed digest:
every run signs whatever digest it pushes, even when a rebuild is
byte-identical to an already-published one, so that digest ends up with more
than one valid signature from different identities rather than only the
newest. A manual dispatch against a non-`main` ref signs under that ref's
identity instead (the reusable workflow call follows whatever ref
`nightly-build.yaml`/`osac-release.yaml` were themselves dispatched
against) — match the regex to the ref actually used if you dispatched it
yourself.

**Verifying an older image, published before each component's own
tag-triggered build workflow stopped reacting to tag pushes:** that older
build's own workflow file and tag prefix are a second valid identity for that
specific digest —

| Component | Image | Workflow file | Release tag prefix |
|---|---|---|---|
| osac-operator | `osac-project/osac-operator` | `build-image.yaml` | `osac-operator` |
| fulfillment-service | `osac-project/fulfillment-service` | `publish-image.yaml` | `fulfillment-service` |
| bare-metal-fulfillment-operator | `osac-project/bare-metal-fulfillment-operator` | `build-bmf-image.yaml` | `bare-metal-fulfillment-operator` |
| osac-aap | `osac-project/osac-aap` | `execution-environment.yml` | `osac-aap` |
| metering-service | `osac-project/metering-service` | `build-metering-service-image.yaml` | `osac-metering` |
| metering-m360-adapter | `osac-project/metering-m360-adapter` | `build-metering-m360-adapter-image.yaml` | `osac-metering` |
| metering-echo-adapter | `osac-project/metering-echo-adapter` | `build-metering-echo-adapter-image.yaml` | `osac-metering` |
| osac-csi-driver | `osac-project/osac-csi-driver` | `publish-csi-driver-image.yaml` | `osac-csi-driver` |

```bash
cosign verify \
  --certificate-identity-regexp '^https://github\.com/osac-project/osac/\.github/workflows/<workflow-file>@refs/tags/<tag-prefix>/.+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/<image>@sha256:<digest>
```

## Verifying Helm chart signatures

Helm charts published to `oci://ghcr.io/osac-project/charts/*` are signed the
same way. Every chart here — every mono-repo component's sub-chart and the
`osac` umbrella chart itself — is packaged, pushed, and signed by
`osac-build-and-publish.yaml`, as part of a nightly run or a real release
(`nightly-build.yaml`/`osac-release.yaml` both call into that same shared
reusable workflow to do so). There is no other publisher for any chart in
this registry.

`helm pull`/`helm push` print the artifact's digest directly, so no extra
tooling is needed to resolve it:

```bash
helm pull oci://ghcr.io/osac-project/charts/<chart-name> --version <version>
# Digest: sha256:<digest>

cosign verify \
  --certificate-identity-regexp '^https://github\.com/osac-project/osac/\.github/workflows/osac-build-and-publish\.yaml@refs/.+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/osac-project/charts/<chart-name>@sha256:<digest>
```

The identity accepts any ref (not pinned to `refs/heads/main`), since a
nightly run or a manually dispatched release can run from a different
branch or tag.

The umbrella chart (`osac`) only ever has one signer identity —
`osac-build-and-publish.yaml`. For a normal nightly run or a release
dispatched from `main` (the common case), verify with just:

```bash
cosign verify \
  --certificate-identity-regexp '^https://github\.com/osac-project/osac/\.github/workflows/osac-build-and-publish\.yaml@refs/heads/main$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/osac-project/charts/osac@sha256:<digest>
```

If `osac-release.yaml` was manually dispatched against a different ref
(GitHub's "Use workflow from" selector), the signature carries that ref
instead — replace `refs/heads/main` above with the exact ref actually used
(e.g. `refs/heads/<branch>`).

Always verify by digest (`@sha256:...`), not by mutable tag — resolve a tag to
its digest first with `skopeo inspect docker://ghcr.io/osac-project/<component>:<tag>`
if needed. Images pushed to `quay.io/redhat-user-workloads/osac-tenant/...` via
Konflux are signed separately by Konflux's own Enterprise Contract pipeline;
see that pipeline's documentation for verifying those instead.

## Verifying binary signatures

The `osac` CLI and `fulfillment-service` binaries are released to GitHub
Releases by `publish-binaries.yaml`, called directly by
`osac-build-and-publish.yaml` right after it tags a release that bumps
fulfillment-service (it has no trigger of its own). Each release binary is
signed the same keyless way as the images and charts above; goreleaser's
`signs` step produces a single Sigstore bundle (`<binary>.sigstore.json`,
containing both the certificate and signature) alongside every binary in
the release.

Download a binary with its bundle, then verify:

```bash
gh release download fulfillment-service/<version> \
  --repo osac-project/osac \
  --pattern 'osac_<os>_<arch>*'

cosign verify-blob \
  --bundle osac_<os>_<arch>.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/osac-project/osac/\.github/workflows/publish-binaries\.yaml@refs/heads/main$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  osac_<os>_<arch>
```

If `osac-release.yaml` was manually dispatched against a different ref,
replace `refs/heads/main` with the exact ref used (same caveat as the
image/chart identities above).

Substitute `fulfillment-service` for `osac` to verify that binary instead —
both are built and signed from the same release. `<os>`/`<arch>` match the
asset names on the release page (e.g. `osac_Linux_x86_64`).

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
than calling `gh`. This
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

## Carbonite / OpenShell (Linux/x86_64)

Each development-container target also has a `-carb` counterpart:

```bash
make check-carb                # Check host commands and the selected gateway
make image-carb                # Build tools/carbonite/Containerfile
make enter-carb                # Create/reuse the sandbox and enter a login shell
make claude-carb ARGS='--model sonnet'
make status-carb
make stop-carb                 # Retain the sandbox workspace
make rm-carb
make rebuild-carb              # Remove, rebuild and enter
```

Ordinary development-container targets default to Distrobox. Select Carbonite
with `make enter USE_CARBONITE=true`, or export
`OSAC_USE_CARBONITE=true` before running the ordinary target. A command-line
`USE_CARBONITE` value overrides the environment, including an explicit
`USE_CARBONITE=false`. The selector applies to `image`, `enter`, `claude`,
`stop`, `rm`, `rebuild`, and `status`; explicit `-carb` targets always use
Carbonite. Only `true` and `false` are valid; any other value fails
immediately.

To dry-run every selector permutation without building either image, run
`bash tools/test/test_carbonite_make_selector.sh`.

These targets use OpenShell directly with the Carbonite image and policy, a
cluster-enabled sandbox (`CARBONITE_NAME`, default `osac-carbonite-kind`) in
the `osac-kind` workspace, and a writable bind mount of this checkout at
`/sandbox/osac`, backed by an approved Podman volume. The default host-only
policy and scoped kubeconfig live under
`${XDG_STATE_HOME:-$HOME/.local/state}/osac-carbonite-kind`; Carbonite fails
fast if those prerequisites are missing. Inside the sandbox,
`PROJECT_DIR=/sandbox/osac` and `KUBECONFIG=/sandbox/kube/config`.

For an ordinary Carbonite sandbox without cluster access, explicitly override
the defaults:

```bash
make enter-carb \
  CARBONITE_NAME=osac-carbonite \
  CARBONITE_WORKSPACE=default \
  CARBONITE_POLICY=tools/carbonite/policy.yaml \
  CARBONITE_KUBECONFIG_DIR=
```

They require host-side Podman, OpenShell 0.1.2 and `jq`, plus a running local
Podman gateway with these settings under `[openshell.drivers.podman]` in its
schema-2 configuration:

```toml
allow_driver_config = true
enable_bind_mounts = true
userns = "keep-id:uid=1000,gid=1000"
```

Podman is a **runtime prerequisite for this Carbonite PoC**. Publishing the
Carbonite image from GitHub CI/CD to an external registry is a separate image
creation step; it does not select the engine that runs the sandbox. The current
`make enter-carb` also runs `make image-carb` locally, creates a Podman-backed
project volume, and passes a `podman` mount configuration to OpenShell. Docker
being installed on the host, or setting `CONTAINER_CMD=docker`, does not make
this Podman gateway workflow Docker-compatible. Supporting OpenShell's Docker
compute driver requires separate gateway, mount, ownership, and verification
work and is deferred. The proposed host-build broker concerns OSAC component
images; it does not remove the Podman prerequisite for running Carbonite here.
For the separate first-time opt-in to approved host image builds, see the
[Carbonite host-build guide](docs/guides/developer/carbonite-host-build.md).
For opt-in access to a host KiND cluster, including the dedicated read-only
kubeconfig mount and API relay, see the
[Carbonite KiND integration guide](docs/guides/developer/carbonite-kind-integration.md).

`make check-carb` checks that the configured container engine, OpenShell and
`jq` are available on the host, that the engine responds, and that the selected
gateway reports both `connected` and `authenticated`. The sandbox targets run
this check automatically before building an image or changing sandbox state,
including with `make -j`. Failures stop the target and print troubleshooting
commands. Install the commands and configure/register the gateway before first
use; renew gateway authentication when needed. `make image-carb` checks only the
container engine, so an image can be built before the gateway is ready. The check
does not validate the gateway's mount settings or the project's policy and
toolchain.

The user mapping makes the image's sandbox user (UID/GID 1000) the host checkout
owner, so it can read and write the project without changing host ownership.
Resource admission stays enabled: the targets label the project volume for
`CARBONITE_WORKSPACE`
(default `default`) before attaching it. `CARBONITE_VOLUME` defaults to
`$(CARBONITE_NAME)-workspace`; use a distinct name for a different checkout.
An existing volume with a different host path or approval labels is rejected.
Before creation, a temporary container applies Podman's shared `:z` SELinux
label to this checkout so Fedora sandboxes can access it while SELinux is
enforcing. Host file ownership is preserved.
The gateway registration defaults to `openshell`; override `CARBONITE_GATEWAY`
to select another local gateway.

The policy defaults to `tools/carbonite/policy.yaml`, adapted from Carbonite
for the OpenShell 0.1 policy schema. Its deployment-internal endpoints are
reserved `.example` placeholders; substitute them for internal deployments
and keep the substituted policy outside the repository. The
[KiND integration guide](docs/guides/developer/carbonite-kind-integration.md)
shows the substitution step. Set `CARBONITE_POLICY` for another
project policy. Use that policy to allow the project's dependencies and cluster
API. `CARBONITE_CREATE_ARGS` accepts additional OpenShell creation options,
including an approved provider, for example
`CARBONITE_CREATE_ARGS='--provider my-claude-provider'`. Authenticate Claude in
the sandbox or attach a provider before using it. The targets use the tools
installed in the image; Carbonite's host-side credential import and plugin setup
are separate launcher features.

Go selects the toolchain required by `go.work` / `go.mod` and may download a
newer version than the image's bundled Go. The image makes `/sandbox/go` and
`/sandbox/.cache/go-build` writable by the `sandbox` user after installing tools
as root. The policy allows the bundled Go and toolchains downloaded under
`/sandbox/go/pkg/mod` to reach the Go module proxy, checksum service and archive
storage. Keep these binary paths aligned if you customize `GOPATH`. Verify cache
permissions on a built image with `bash tools/test/test_carbonite_image.sh`.
The envtest rule allows `setup-envtest*` under `/sandbox/osac/*/bin` to fetch its
release index and Kubernetes test binaries from GitHub. Adjust that path if you
customize `CARBONITE_WORKDIR`.

With OpenShell 0.1.2 and controller-runtime 0.24.1, envtest teardown currently
fails with `operation not permitted`: envtest signals a process group, which
OpenShell rejects. The operator's 972 controller specs pass after explicitly
setting the test API server's advertised address to loopback, but the teardown
failure still makes `make test` / `make` fail. Dependency-download rules do not
resolve this process-control compatibility limit.

For productization after the PoC, a proposed workaround is to adapt envtest's
cleanup to signal each test-owned process by its positive PID: send SIGTERM,
wait for exit, then use SIGKILL if necessary. This requires a supported cleanup
adapter or a maintained controller-runtime dependency patch; a Makefile cleanup
trap alone removes leftovers but does not prevent the existing `AfterSuite`
failure. A trap can provide fallback cleanup for interrupted runs. Keep cleanup
scoped to that run's processes, preserve test failures, and report cleanup
failures. Individual-child signaling was verified; this envtest workaround is
not implemented or validated yet, and requires no broader sandbox permissions.

For `osac-ui`, the Carbonite image includes pnpm 9.15.9 and a writable npm
cache for source development. Inside the sandbox, `pnpm install --frozen-lockfile`
and `pnpm build` build the SPA and Go proxy. With the optional
[host-build setup](docs/guides/developer/carbonite-host-build.md), the default
`make` in `osac-ui/` requests a fixed image build from the host's Podman.
Without that opt-in, run `make -C osac-ui` on the host. OpenShell level 3 does
not permit Podman to create nested containers in the sandbox.

Image, policy, provider and mount options are applied when the sandbox is
created. Use `make rebuild-carb` to recreate it after changing those options.
The checkout remains on the host when the sandbox is removed.
The project volume registration is also retained for reuse.

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
