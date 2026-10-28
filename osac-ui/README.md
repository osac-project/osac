# OSAC UI

OSAC UI is the web console for the [Open Sovereign AI Cloud (OSAC)](https://github.com/osac-project/) project — a self-service platform for deploying OpenShift clusters, virtual machines, and bare metal hosts. It is a pnpm monorepo consisting of a React 19 + PatternFly 6 single-page application and a Go chi reverse proxy that handles OIDC authentication and forwards requests to the upstream [fulfillment-service](https://github.com/osac-project/fulfillment-service).

## Repository layout

| Path | Purpose |
|------|---------|
| `apps/app-frontend/` | React SPA (Vite, React 19, TanStack Query, react-router-dom 7) |
| `libs/api-contracts/` | Shared TypeScript types and wire normalizers |
| `libs/types/` | Generated protobuf types (do not edit) |
| `libs/ui-components/` | Shared PatternFly 6 component library |
| `proxy/` | Go chi reverse proxy — OIDC auth + API forwarding |
| `deploy/chart/` | Helm chart for Kubernetes/OpenShift deployment |
| `docs/` | Architecture and deployment documentation |

## Verifying signatures

Container images published to `ghcr.io/osac-project/osac-ui` and the Helm
chart published to `oci://ghcr.io/osac-project/charts/ui` are signed
keylessly with [cosign](https://docs.sigstore.dev/), using each workflow
run's GitHub Actions OIDC identity via Fulcio/Rekor — no long-lived private
key is involved. Verify by digest (`@sha256:...`), not by mutable tag:

```bash
cosign verify \
  --certificate-identity-regexp '^https://github\.com/osac-project/osac-ui/\.github/workflows/publish-image\.yaml@refs/(heads/main|tags/v.+)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/osac-project/osac-ui@sha256:<digest>

cosign verify \
  --certificate-identity-regexp '^https://github\.com/osac-project/osac-ui/\.github/workflows/publish-charts\.yaml@refs/heads/main$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/osac-project/charts/ui@sha256:<digest>
```

Resolve a tag to its digest with `helm pull oci://ghcr.io/osac-project/charts/ui --version <version>` (prints `Digest: sha256:...`) or `skopeo inspect docker://ghcr.io/osac-project/osac-ui:<tag>`.

## Quick start

Prerequisites: Node.js 20+, pnpm 9+, Go 1.23+

```bash
pnpm install
```

Start the Go proxy (requires a running fulfillment API) and Vite dev server:

```bash
FULFILLMENT_API_URL=https://fulfillment.your-env.example.com pnpm dev
```

The default `make` target builds the production container image with Podman.
From the host at the OSAC repository root, run `make -C osac-ui`; from a host
shell already in `osac-ui/`, run `make`. The Carbonite/OpenShell level-3 sandbox
blocks nested Podman container creation, so a plain `make` there cannot build
this image. After the optional host-build channel is enabled (see the root
README and `docs/guides/developer/carbonite-host-build.md`), `make image-build`
in `osac-ui/` dispatches the build to the host's Podman. For a source build
inside the sandbox, use `pnpm install --frozen-lockfile` and `pnpm build`.

## Documentation

| Document | Description |
|----------|-------------|
| [OpenShift deployment guide](docs/deployment-openshift-guide.md) | Step-by-step guide for deploying on OpenShift with Keycloak and fulfillment-service |
| [API query architecture](docs/api-query-arch.md) | How the API layer is split between `ui-components` and the app |
| [AGENTS.md](AGENTS.md) | Dev environment setup, scripts reference, and coding conventions |
