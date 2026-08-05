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
git clone https://github.com/osac-project/osac-installer.git
cd osac-installer
git submodule update --init --recursive

# Place your AAP license file
cp /path/to/license.zip values/vmaas-ci/

# Install (all 3 phases)
make install VALUES_FILE=values/vmaas-ci/values.yaml
```

## How It Works

OSAC installs in three phases. Each phase's outputs are the next phase's
inputs. This is required because Helm validates all templates before applying
any — a template that references a CRD must have that CRD already registered
on the cluster.

### Phase 1: `make install-operators`

Installs OLM operator Subscriptions: cert-manager, AAP, LVMS, MetalLB, CNV,
MCE. Post-install hooks wait for each operator's CSV to succeed and CRDs to
register. After this phase, all CRDs exist.

Each operator is gated by a values toggle (e.g., `certManager.enabled`).

### Phase 2: `make install-prereqs`

Creates CRD instances that OSAC depends on: CertManager CR, ClusterIssuer,
CA certificates, trust-manager Bundle, Keycloak (realm with OVERWRITE
strategy), LVMCluster, HyperConverged, MetalLB IPAddressPool, and
controller credentials (read from Keycloak, written to OSAC namespace).

Uses `--wait-for-jobs` to avoid circular dependencies between hook Jobs
and template resources.

### Phase 3: `make install-osac`

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

| File | Use case |
|------|----------|
| `values/vmaas-ci/values.yaml` | VMaaS (compute instances) |
| `values/caas-ci/values.yaml` | CaaS (cluster provisioning) |
| `values/development/values.yaml` | Local dev (all controllers) |

Copy and customize for your environment:

```bash
mkdir -p values/my-env
cp values/development/values.yaml values/my-env/values.yaml
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

These are top-level values, disabled by default. Enable only in CI/dev:

| Value | What it does |
|-------|-------------|
| `hubAccess.enabled` | Creates hub-access SA/RBAC and registers local cluster as a hub. Only for environments where fulfillment-service and hub are the same cluster. |
| `bundledPostgres.enabled` | Deploys a single-pod ephemeral PostgreSQL. Uses `fsync=off` and `emptyDir` — data lost on restart. Not for production. |

## Makefile Targets

| Target | Description |
|--------|-------------|
| `make install` | Full install (all 3 phases) |
| `make install-operators` | Phase 1 only |
| `make install-prereqs` | Phase 2 only |
| `make install-osac` | Phase 3 only |
| `make uninstall` | Full uninstall (reverse order) |
| `make helm-lint` | Lint all charts |
| `make helm-deps` | Build chart dependencies |
| `make sync-charts` | Update submodules + rebuild deps |

## Uninstall

```bash
make uninstall
oc delete namespace ${NAMESPACE} --wait
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
- **AAP token missing or empty** - The main container reads the `osac-aap-api-token`
  Secret and fails if the token is absent or empty. Verify the secret exists
  and contains a valid token:
  `oc get secret osac-aap-api-token -n ${NAMESPACE} -o jsonpath='{.data.token}' | base64 -d`.
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
