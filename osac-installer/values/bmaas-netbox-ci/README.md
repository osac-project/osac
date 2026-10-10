# bmaas-netbox-ci profile

BMaaS E2E profile using [NetBox](https://netbox.dev/) as the bare-metal
inventory backend. Inherits `bmaas-ci` and overlays only what differs.

## What it does

- Deploys NetBox (`netbox-community/netbox` subchart) into `osac-infra`.
- Provisions an API token via `POST /api/users/tokens/provision/` and writes
  it as `netbox-api-token` into `osac` for the bare-metal-fulfillment-operator.
- Disables Metal3 (`bmf.metal3.enabled: false`), enables NetBox (`bmf.netbox`).

## Install

```bash
make install PLATFORM=openshift PROFILE=bmaas-netbox-ci NS=osac
```

## Credentials

The NetBox chart auto-generates all credential secrets on first install.

| Secret | Namespace | Contents |
|--------|-----------|----------|
| `osac-infra-netbox` | `osac-infra` | `secret_key`, `db_password` |
| `osac-infra-netbox-superuser` | `osac-infra` | `username`, `password` |
| `netbox-api-token` | `osac` | `token` — v2 HMAC token for the operator |

`apiTokenPeppers` is set directly in chart values. NetBox 4.7 requires it for
all token operations; a fixed CI string is acceptable for a test-only deployment.

## Token format

NetBox 4.7 issues v2 HMAC tokens in the form `nbt_{key}.{plaintext}`,
presented as `Authorization: Bearer nbt_{key}.{plaintext}`. The seed Job
calls `/api/users/tokens/provision/` (no prior auth required), parses the
JSON response, and stores the full value in `netbox-api-token`. The Job is
idempotent — if the Secret already exists it exits immediately.

## Inventory configuration

The operator reads its inventory backend from a mounted `inventory.yaml`:

```yaml
name: netbox-inventory
type: netbox
options:
  netbox:
    url: "http://osac-infra-netbox.osac-infra.svc.cluster.local"
    tokenFile: "<path where netbox-api-token is mounted>"
    allowInsecureHTTP: true
hostClass: metal3
```

`hostClass: metal3` is a hard requirement of `NewNetBoxClient` — NetBox
supplies hardware inventory while Metal3 handles BareMetalHost lifecycle.

## OpenShift SCC

The Bitnami chart hardcodes `runAsUser: 1000` and `fsGroup: 1000`, which
`restricted-v2` rejects, and sets seccomp annotations that `anyuid` rejects.
A dedicated `SecurityContextConstraints` object (`osac-infra-netbox`) grants
`RunAsAny` for user/fsGroup/seLinux and allows seccomp, scoped to the single
`osac-infra-netbox` service account.

## CI / GitHub Actions

`.github/workflows/e2e-bmaas-netbox-full-install.yml` — `workflow_dispatch`
only, not a required check.
