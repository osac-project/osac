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

Prerequisites: Node.js 20+, pnpm 9+, Go 1.25.5+, and `oc` for automatic
authentication with a protected API

```bash
pnpm install
```

Start the Go proxy (requires a running fulfillment API) and Vite dev server.
When the API requires authentication, log in to OpenShift first. The script
reads the UI credentials from the `osac` project by default:

```bash
FULFILLMENT_API_URL=https://fulfillment.your-env.example.com pnpm dev
```

The script reads the OIDC issuer from ConfigMap `osac-ui` and the UI backend's
client credentials from Secret `osac-ui-backend-credentials` in that project.
You need permission to read both resources. Use an externally reachable
`FULFILLMENT_API_URL` when running outside the cluster.

To use another project, set `OSAC_UI_NAMESPACE`, for example:

```bash
OSAC_UI_NAMESPACE="other_ns" FULFILLMENT_API_URL=https://fulfillment.your-env.example.com pnpm dev
```

Values already set in `OIDC_ISSUER_URL`, `OIDC_SERVICE_CLIENT_ID`, or
`OIDC_SERVICE_CLIENT_SECRET` take precedence over cluster values; when all
three are set, the script does not call `oc`. If `oc` cannot provide the
settings, the proxy can still start against an API with public reflection.
The script passes credentials through the development proxy process tree's
environment, without printing or storing them.

Any `OIDC_ISSUER_URL` override must be
reachable and match the issuer trusted by fulfillment. For self-signed
certificates, set `OIDC_TLS_CA_FILE` and `FULFILLMENT_TLS_CA_FILE` to the
appropriate CA bundle paths, or use the development-only
`OIDC_TLS_INSECURE=1` and `FULFILLMENT_TLS_INSECURE=1` flags.

## Documentation

| Document | Description |
|----------|-------------|
| [OpenShift deployment guide](docs/deployment-openshift-guide.md) | Step-by-step guide for deploying on OpenShift with Keycloak and fulfillment-service |
| [API query architecture](docs/api-query-arch.md) | How the API layer is split between `ui-components` and the app |
| [AGENTS.md](AGENTS.md) | Dev environment setup, scripts reference, and coding conventions |
