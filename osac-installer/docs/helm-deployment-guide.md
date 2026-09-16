# OSAC Helm Deployment Guide

Deploy OSAC on a clean connected OpenShift cluster using the three-phase
Helm install.

## Requirements

| Requirement | Details |
|-------------|---------|
| OpenShift | 4.17+ with cluster-admin access |
| CLI tools | `oc`, `helm`, `git`, `make` |
| Network | Outbound access to github.com, ghcr.io, quay.io, registry.redhat.io |
| AAP license | Subscription manifest (`license.zip`) from [Red Hat Customer Portal](https://access.redhat.com/) |

## Quick Start

```bash
git clone https://github.com/osac-project/osac.git
cd osac/osac-installer
make helm-deps

# Place your AAP license file
cp /path/to/license.zip values/vmaas-ci/

# Install (infra + osac)
make install PLATFORM=openshift PROFILE=vmaas-ci NS=osac
```

## How It Works

OSAC installs in three phases. Each phase's outputs are the next phase's
inputs. This is required because Helm validates all templates before applying
any — a template that references a CRD must have that CRD already registered
on the cluster.

### `make install-infra`

Installs infrastructure in two Helm releases:

1. **osac-deps** — OLM operator Subscriptions (cert-manager, AAP, LVMS, MetalLB,
   CNV, MCE). Post-install hooks wait for each operator's CSV to succeed and CRDs
   to register. Each operator is gated by a values toggle.

2. **osac-infra** — CRD instances: CertManager CR, ClusterIssuer, CA certificates,
   trust-manager Bundle, Keycloak, LVMCluster, HyperConverged, MetalLB IPAddressPool,
   controller credentials, and bundled PostgreSQL (dev/CI only).

### `make install-osac`

Installs OSAC: operator, fulfillment-service, AAP bootstrap, UI. All
prerequisites are ready - certificates issued, secrets created, CRDs
registered.

### Post-Install Hooks

After Phase 3, Helm runs post-install (and post-upgrade) hooks that
finalize the deployment:

| Hook | Weight | What it does |
|------|--------|-------------|
| `osac-publish-templates` | 20 | Publishes cluster templates to the fulfillment service catalog |

**Cluster template publishing** (`osac-publish-templates`): An init
container waits for the fulfillment REST gateway to be healthy (up to
600s), then launches the `osac-publish-templates` AAP job template and
polls until it completes. Helm blocks until this hook succeeds, so
`helm install` and `helm upgrade` will not report success until cluster
templates are available. The underlying Ansible role uses a PATCH/POST
pattern, making re-publish on upgrade safe and idempotent.

This hook is enabled by default (`aap.instanceGroups.publishTemplates.enabled: true`).
To disable it (e.g., in environments without CaaS):

```yaml
aap:
  instanceGroups:
    publishTemplates:
      enabled: false
```

## Values Files

Each profile has two files: `infra.yaml` (infrastructure config) and `instance.yaml` (OSAC instance config).

| Profile | Use case |
|---------|----------|
| `values/vmaas-ci/` | VMaaS CI (compute instances) |
| `values/caas-ci/` | CaaS CI (cluster provisioning) |
| `values/bmaas-ci/` | BMaaS CI (bare metal) |
| `values/dev/` | Local dev (Kind) |

Copy and customize for your environment:

```bash
mkdir -p values/my-env
cp values/dev/infra.yaml values/my-env/
cp values/dev/instance.yaml values/my-env/
# Edit to match your cluster
```

Key settings:

| Setting | Description |
|---------|-------------|
| `service.externalHostname` | Required. Set automatically by `make install-osac`. |
| `service.internalHostname` | Required. Set automatically by `make install-osac`. |
| `service.auth.issuerUrl` | Keycloak realm URL (default works for in-cluster Keycloak) |
| `operator.controllers.*` | Enable/disable individual controllers |

## CI/Dev-Only Features

These values control bundled dev/CI services. Disable in production.

### Infra chart (`osac-infra`) values

| Value | Default | What it does |
|-------|---------|-------------|
| `bundledPostgres.enabled` | `false` | Deploys a single-pod ephemeral PostgreSQL. Uses `fsync=off` and `emptyDir` — data lost on restart. Not for production. |
| `bundledVault.enabled` | `true` | Deploys a single-pod ephemeral OpenBao (Vault-compatible) secret store in the `osac-infra` namespace. Dev mode — data is lost on restart. Not for production. The OSAC instance chart connects via FQDN (`openbao.osac-infra.svc.cluster.local`). |

### Instance chart (`osac`) values

| Value | Default | What it does |
|-------|---------|-------------|
| `hubAccess.enabled` | `false` | Creates hub-access SA/RBAC and registers local cluster as a hub. Only for environments where fulfillment-service and hub are the same cluster. |

## Infrastructure Configuration

### Keycloak Route with Public Ingress Certificates

For clusters with publicly-trusted wildcard ingress certificates (e.g., production OpenShift clusters with Let's Encrypt), you can configure Keycloak's Route to use `reencrypt` termination instead of `passthrough`:

```yaml
# values/<profile>/infra.yaml
keycloak:
  route:
    publicIngress: true  # Switches Route from passthrough to reencrypt
    hostname: keycloak.apps.example.com  # Optional: custom hostname
```

**How it works:**
- `passthrough` (default): Browser connects directly to Keycloak's internal TLS cert (self-signed CA)
- `reencrypt`: Router presents the cluster's public ingress cert to browsers, then re-encrypts traffic to Keycloak's internal TLS endpoint using the CA cert

**Requirements:**
- `caIssuer.enabled: true` (default) — The hook depends on cert-manager's CA bundle
- The `osac-infra-patch-keycloak-route` hook Job automatically sets `destinationCACertificate` at install/upgrade time

**When to use:**
- Production clusters where browser cert warnings are unacceptable
- Environments with corporate CA or Let's Encrypt ingress certs

## External (Brownfield) AAP

When deploying OSAC on a cluster that already has an Ansible Automation
Platform controller (e.g. installed by ACM), use the external AAP path
instead of letting the installer deploy its own. This avoids duplicate
operators, field-manager conflicts, and bootstrap hangs.

### Prerequisites

1. The existing AAP controller must be reachable from the OSAC namespace
   (URL and network path). This path does **not** create an
   `AnsibleAutomationPlatform` CR in the OSAC namespace, so the existing
   operator does not need to watch that namespace.
2. The existing AAP controller must be pre-configured with the OSAC
   organization, job templates, credentials, and execution environment.
   See `osac-aap/docs/` for the expected config-as-code layout.
   The post-install `osac-publish-templates` hook is still enabled by
   default and will call the job template named
   `<operator.aap.templatePrefix>-publish-templates` (default
   `osac-publish-templates`) on that controller. Create that template
   before install, or Helm `--wait` fails. To skip the hook instead,
   set `aap.instanceGroups.publishTemplates.enabled: false`.
3. Create a Secret in the OSAC namespace with a valid AAP API token
   **before** install. When `externalAap` is enabled, the operator and BMF
   require that Secret (`secretKeyRef.optional: false`). A missing Secret
   fails the pods with `CreateContainerConfigError` instead of an empty
   `OSAC_AAP_TOKEN`.

```bash
oc create secret generic my-aap-token \
  --from-literal=token=<your-aap-token> -n <osac-namespace>
```

### Configuration

**Infra values** (`values/<profile>/infra.yaml`) — disable the operator
install:

```yaml
aapOperator:
  enabled: false
```

**Instance values** (`values/<profile>/instance.yaml`) — point at the
external controller. Set `global.externalAap` so the operator and BMF
auto-wire `OSAC_AAP_URL` / `OSAC_AAP_TOKEN`. Copy the same block to
`aap.externalAap` (a YAML anchor is enough) for umbrella validation:

```yaml
global:
  externalAap: &externalAap
    enabled: true
    url: "https://my-aap.aap-namespace.apps.mycluster.example.com/api/controller"
    tokenSecret:
      name: "my-aap-token"
      key: "token"

aap:
  externalAap: *externalAap
  aap:
    instance:
      enabled: false
  bootstrap:
    enabled: false
  apiToken:
    create: false
```

Do not set `operator.aap.url` or `bmf.env.aapUrl` for this path; those
are filled from `global.externalAap`. The URL must be `https://`; the
pre-install hook rejects HTTP because the client sends a bearer token.

TLS skip-verify is already on by default
(`operator.aap.insecureSkipVerify` and `bmf.env.aapInsecureSkipVerify`).
ACM-managed or other internal-CA AAP works without extra values. For a
production CA, set both to `"false"` so the operators verify the
controller certificate.

A complete example overlay is provided at
`values/examples/external-aap.yaml`.

### Install

Helm values and Make are a pair. Use both, or the install disagrees with
itself:

| Helm `externalAap.enabled` | Make `EXTERNAL_AAP=true` | Result |
|---|---|---|
| true | true | Brownfield path (intended) |
| true | unset | Fails looking for `license.zip` you do not need |
| false | true | Skips the license, then still deploys managed AAP |

```bash
make install-infra PLATFORM=openshift PROFILE=<profile> NS=<namespace>
make install-osac  PLATFORM=openshift PROFILE=<profile> NS=<namespace> \
  EXTERNAL_AAP=true
```

Pass the overlay with `INSTANCE_VALUES_EXTRA="-f values/examples/external-aap.yaml"`
(and keep `aapOperator.enabled: false` in infra values).

The pre-install validation hook will fail if external AAP is enabled but
the URL is empty or not `https://`, the token secret name is empty,
`aap.externalAap` does not match `global.externalAap`, or conflicting
instance/bootstrap/apiToken flags are still on. The token Secret itself
must exist before operator and BMF pods start (`secretKeyRef.optional: false`).

## Makefile Targets

All targets require `PLATFORM=kind|openshift PROFILE=dev|vmaas-ci|... NS=<namespace>`.

| Target | Description |
|--------|-------------|
| `make install` | Full install (infra + osac) |
| `make install-infra` | Infrastructure only (osac-deps + osac-infra) |
| `make install-osac` | OSAC instance only |
| `make uninstall` | Full uninstall (reverse order) |
| `make test` | Run integration tests (SUITE= required) |
| `make helm-lint` | Lint all charts |

## Uninstall

```bash
make uninstall PLATFORM=openshift PROFILE=vmaas-ci NS=osac
```

## Troubleshooting

### AAP Bootstrap Failing

```bash
oc logs -f job/osac-aap-bootstrap -n ${NAMESPACE}
oc get secret config-as-code-manifest-ig -n ${NAMESPACE}  # license exists?
```

### Fulfillment Pods CrashLooping

```bash
oc logs deployment/fulfillment-grpc-server -n ${NAMESPACE}
```

Common causes: missing `fulfillment-db` secret, cert-manager certificates
not issued (`oc get certificate -n ${NAMESPACE}`), missing controller
credentials.

### Helm Install Timeout

The AAP bootstrap hook can take 10-40 minutes. Monitor with:

```bash
oc logs -f job/osac-aap-bootstrap -n ${NAMESPACE}
```

### Template Publish Hook Failing

The `osac-publish-templates` post-install hook must complete for Helm to
report success. If it fails, cluster templates may be missing or incomplete
(on upgrade, previously published templates may still exist).

**Check hook pod status and logs:**

```bash
oc get pods -n ${NAMESPACE} | grep publish-templates
oc logs job/osac-publish-templates -n ${NAMESPACE} -c wait-for-fulfillment  # init container
oc logs job/osac-publish-templates -n ${NAMESPACE} -c publish-templates     # main container
```

**Common causes:**

- **Fulfillment service not ready** - The init container polls
  `https://fulfillment-rest-gateway:8000/healthz` for up to 600s. If it
  times out, check that the fulfillment service pods are running and the
  `fulfillment-rest-gateway` Service exists.
- **AAP token missing or empty** - The main container reads the AAP token
  Secret. Managed AAP uses `osac-aap-api-token`. External AAP uses
  `global.externalAap.tokenSecret.name` (and that Secret's key). Verify
  the Secret exists and the token key is non-empty, for example:
  `oc get secret osac-aap-api-token -n ${NAMESPACE} -o jsonpath='{.data.token}' | base64 -d`
  or, for brownfield, the same command with your configured Secret name
  and key.
- **AAP job template not found** - The `osac-publish-templates` job template
  must exist in AAP. Verify via AAP UI or API after the bootstrap job
  completes.
- **AAP job failure** - The hook logs include the AAP job stdout on failure.
  Check AAP for the job run details.

The hook has `backoffLimit: 6` and `activeDeadlineSeconds: 1300`. After
6 retries or 1300s, the Job fails and Helm reports the install as failed.

### Hook Job Failed

Failed hook pods are preserved for debugging (`hook-succeeded` delete
policy). Check logs:

```bash
oc get pods -n ${NAMESPACE} | grep -v Running | grep -v Completed
oc logs <failed-pod> -n ${NAMESPACE}
```
