# Disconnected OSAC Installation

> [!IMPORTANT]
> This is the final document in the disconnected installation sequence for OSAC **0.0.24**: [prereqs.md](prereqs.md) → [aap-git-source.md](aap-git-source.md) → [operators.md](operators.md) → **installation.md**. Complete all three preparation guides before continuing.

## 1. OSAC installation architecture

This guide installs the **0.0.24** `osac` Helm release on a cluster prepared through [prerequisites](prereqs.md), [AAP Git source preparation](aap-git-source.md) and [Operator and infrastructure installation](operators.md). It does not install prerequisite Helm releases.

### 1.1 What is released

Use the prepared local `osac` chart and its approved values. The chart deploys the selected OSAC components and performs their initialization; see the [installation architecture](../customer-install-guide.md#1-osac-installation-architecture) for the common component and phase model.

## 2. Prerequisites

Complete the three preparation guides above for the same cluster, namespace and selected services before continuing. Use the common [cluster/access and client-tool requirements](../customer-install-guide.md#2-prerequisites), replacing public registry/Git access with the prepared disconnected sources.

- Required Operators, trust distribution, selected operands and external services are ready as described in [operators.md](operators.md); do not reinstall them here.

- Keep the approved `OSAC_NAMESPACE`, `OSAC_CHART_DIR` and `VALUES_FILE` available in the installation shell. `OSAC_CHART_DIR` is the exact downloaded/extracted **0.0.24** chart path; `VALUES_FILE` is the complete prepared values file.

- Mirror policies, pull credentials, required CLI images, EE and internal Git source are already prepared. Keep the selected service scope and credential/trust references unchanged; return missing preparation to its owning guide.

## 3. OSAC component versions

| Item | Source for this installation |
| --- | --- |
| OSAC chart | Prepared **0.0.24** chart at `$OSAC_CHART_DIR` |
| Component images | Release identities and mirror coverage established in [prereqs.md](prereqs.md), not the connected guide's example version table |
| AAP Git and EE | Internal source/full commit and effective EE from [aap-git-source.md](aap-git-source.md) and the prepared values |

Preserve released image references where the approved mirror mappings cover them; do not substitute `main`, `latest` or a lab-render image inventory.

## 4. Installing OSAC

### 4.1 Preparing the cluster

Run these steps in the same administrator shell. Reuse the prepared namespace, AAP license Secret, trust and service inputs; do not repeat the connected guide's creation commands or pre-create chart-owned database configuration/Certificate outputs.

```bash
export NS="$OSAC_NAMESPACE"
```

### 4.2 Configuring the Helm values

Use the complete prepared `$VALUES_FILE`, including the service selections in [Section 6](#6-installation-workflows-by-service) and the disconnected values in [Section 5](#5-helm-chart-configuration-parameters). To inspect defaults, use `helm show values "$OSAC_CHART_DIR"`; do not redirect over the approved values file or retrieve the chart from a public registry here.

### 4.3 Installing OSAC

For a new `osac` release, run one installation against the prepared namespace and
local chart. Save Helm's JSON release record for Section 7; it includes the
validation hook's explicit success result even after the Job is deleted. Use a new
file path for each attempt and protect this record like the values file: it can
contain credentials and rendered Secrets. The subshell preserves Helm's exit
status without a pipeline masking failures and refuses to overwrite an existing
record.

```bash
export OSAC_INSTALL_RECORD="$PWD/osac-install.json"
(
  umask 077
  set -o noclobber
  helm install osac "$OSAC_CHART_DIR" \
    --namespace "$NS" \
    --values "$VALUES_FILE" \
    --wait --timeout 40m --output json > "$OSAC_INSTALL_RECORD"
)
```

Do not run competing installations for this target. If Helm fails, stop and use [Section 11](#11-troubleshooting); do not continue to acceptance or bypass hooks. When it completes, verify the installation through [Section 7](#7-verifying-the-installation).

## 5. Helm chart configuration parameters

Use the **0.0.24** umbrella and subchart defaults/schemas for the parameter reference; the [connected parameter tables](../customer-install-guide.md#5-helm-chart-configuration-parameters) explain common settings. Merge the following inputs into the complete values, preserving other keys. The CLI overrides do not replace `aap.bootstrap.image` or the EE.

| Parameter | Disconnected requirement |
| --- | --- |
| `cliImage` | Approved mirror-resident CLI image and required-tool compatibility from prerequisites |
| `aap.bootstrap.cliImage` | Independent override to the approved CLI image; changing `cliImage` alone does not set it |
| `aap.configAsCode.projectGitUri`, `aap.configAsCode.projectGitBranch`, `aap.configAsCode.eeImage` | Preserve the internal Git source/full commit and approved EE; these are not the bootstrap CLI image |
| `kafka.enabled`, `service.kafka.connection` | For external Kafka, retain `kafka.enabled=false` and the explicit ConfigMap/Secret key-to-parameter mappings prepared in [operators.md](operators.md); see [Kafka configuration](../kafka-configuration.md) |

### 5.1 Values set on the command line

The connected command supplies `global.clusterDomain`, `service.externalHostname` and `service.internalHostname` with `--set`. Keep their approved site values in `$VALUES_FILE` here so the installation uses the prepared configuration. There is no `global.imageRegistry` replacement for individual image settings or mirror mappings.

## 6. Installation workflows by service

Use only the service selected during preparation; retain the limits in [Section 9](#9-supported-configurations). Reuse [Section 4](#4-installing-osac), not the connected guide's public downloads or example backend endpoints.

### 6.1 Installing OSAC for VMaaS

Follow the [VMaaS workflow](../customer-install-guide.md#61-installing-osac-for-vmaas) with the prepared storage/LoadBalancer providers, network settings, mirrored disk images, internal AAP Git source and EE. Keep the required compute templates and storage-tier registration; do not enable an unselected provider.

**Example:** select the prepared Core + VMaaS values → install through Section 4 → pass Section 7 → create a test `ComputeInstance` as in the workflow → confirm it reaches `RUNNING`. Pod readiness alone is not this service check.

### 6.2 Installing OSAC for CaaS with the Netris network backend

CaaS provisioning is outside the current disconnected procedure. The [CaaS/Netris workflow](../customer-install-guide.md#62-installing-osac-for-caas-with-the-netris-network-backend) remains a WIP reference for site backend/DNS settings and hosted-cluster release content, not authorization to enable CaaS in the prepared Core/Core + VMaaS profile.

### 6.3 Installing OSAC for BMaaS

**TBD:** disconnected BMaaS installation and operation have not been validated; this scope covers mirroring only. The [BMaaS workflow](../customer-install-guide.md#63-installing-osac-for-bmaas) is reference material, not a disconnected installation procedure.

## 7. Verifying the installation

Perform these checks in order, using the prepared namespace `$NS`, selected providers and release object names. Stop at the first failure and see [Section 11](#11-troubleshooting); use the [connected verification sequence](../customer-install-guide.md#7-verifying-the-installation) for common commands, substituting site endpoints and trusted CA files without disabling TLS verification.

1. Run `helm status osac -n "$NS"`; require `STATUS: deployed` before continuing.

2. Check the saved release record from Section 4 with `jq`; require exit status
   zero and `true` from this check (use the rendered Job name if overridden):

   ```bash
   jq -e --arg ns "$NS" '
     .name == "osac" and .namespace == $ns and .info.status == "deployed" and
     [.hooks[]? |
       select(.kind == "Job" and .name == "osac-pre-install-validate") |
       .last_run.phase] == ["Succeeded"]
   ' "$OSAC_INSTALL_RECORD"
   ```

   In 0.0.24, the pre-install validation Job has `hook-succeeded` cleanup: Helm
   records `last_run.phase: Succeeded` before deleting it. Successful database-init
   and publishing Jobs are also deleted. Later `NotFound` or absence is never
   proof of success; a missing record, missing validation hook or any result other
   than `Succeeded` fails this check. Do not continue to acceptance without this
   positive evidence from the successful installation attempt.

3. Confirm the selected Phase 1 Operator CSVs are `Succeeded` and their operands are ready, using the actual namespaces/provider choices from [operators.md](operators.md).

4. Confirm prepared database, Kafka, secret-store and identity services remain ready. Use the configured issuer's discovery URL, not the connected example's Keycloak hostname or infrastructure namespace.

5. Inspect `oc get pods -n "$NS"` and the selected Deployment/DaemonSet rollouts. Require ready current workloads, including Fulfillment, OSAC Operator, AAP and enabled UI/CSI components, without image-pull or crash loops.

6. Require the release's Certificates to be `Ready` and database initialization to have succeeded; if it failed, inspect the retained `osac-db-init` Pod/container logs rather than creating its generated certificate Secret yourself.

7. Verify `osac-aap` and successful `osac-aap-bootstrap`/`osac-aap-create-token` execution in `$NS`. In Controller, confirm the intended project source, successful synchronization and expected Git revision, playbook paths and EE.

8. Confirm templates needed by the selected service are present. Do not require CaaS templates for a Core-only installation; inspect the retained publishing failure if required templates are missing.

9. Check the site Fulfillment API `/healthz`, then authenticate with the prepared matching CLI and CA trust and run `osac get tenants`. If UI is enabled, confirm its site URL redirects to the approved identity provider and returns to the OSAC console after login.

10. Complete the selected service check in [Section 6](#6-installation-workflows-by-service).

## 8. Postinstallation tasks

### 8.1 Accessing the OSAC consoles

Use the site OSAC console, Fulfillment API, AAP and identity URLs from the prepared configuration. The `fulfillment-api` and enabled `osac-ui` Routes are in `$NS`; locate the AAP instance's Route there, not in the AAP Operator namespace. Use the prepared identity-provider location and assigned accounts, not bundled administrator examples.

### 8.2 Installing the `osac` CLI

Install the matching CLI artifact delivered during preparation; use the [CLI instructions](../customer-install-guide.md#82-installing-the-osac-cli) without their public download or older version example. Authenticate as described in Section 7 with the site's trusted CA.

### 8.3 Registering the hub

Check whether installation already registered the hub. If manual registration is needed, follow [hub registration](../customer-install-guide.md#83-registering-the-hub) using the prepared matching helper and site API/CA, not a live script download. That procedure applies to a co-located Fulfillment Service and hub; it does not add multi-hub support.

### 8.4 Installing without DNS management

CaaS remains outside this procedure. The [no-DNS-management reference](../customer-install-guide.md#84-installing-without-dns-management) retains its release/schema constraints: disabling DNS management does not create DNS records. Applicable API/ingress records must resolve from the hub, AAP EE, managed host and hosted cluster.

### 8.5 Additional resources

See the existing [network, DNS and AAP configuration references](../customer-install-guide.md#85-additional-resources); retain site-local sources and endpoints.

## 9. Supported configurations

### 9.1 Supported for production

Use the tagged **0.0.24** release with the external database, identity and secret-store contracts prepared through [operators.md](operators.md). The [connected production section](../customer-install-guide.md#91-supported-for-production) describes the common topology; follow this guide's release-specific resource ownership when examples differ. Mirrored content does not broaden service qualification.

### 9.2 Evaluation only

Bundled database/secret-store deployments, development identity fixtures and development/CI values remain evaluation-only; see the [connected limitations](../customer-install-guide.md#92-evaluation-only). These are not settings to add to this Phase 2 values file.

### 9.3 Not covered by this guide

Prerequisite setup belongs to the three earlier disconnected guides. Detailed external identity setup and multi-hub topologies remain outside this guide; see [the common scope limitations](../customer-install-guide.md#93-not-covered-by-this-guide).

## 10. Uninstalling OSAC

To remove only the `osac` release from the prepared namespace, run:

```console
helm uninstall osac -n "$NS"
```

CRDs are retained. Do not delete the namespace, shared prerequisites or cluster-scoped resources used by other deployments as routine cleanup. See [operators.md](operators.md) for prerequisite ownership; removal of retained resources is a separate administrator decision.

## 11. Troubleshooting

Use the [existing failure categories](../customer-install-guide.md#11-troubleshooting) with the substitutions below. Inspect retained failed Jobs/Pods before retrying; successful hooks can self-delete and the Kafka-copy Job has a completion TTL. Keep logs private and omit credentials from shared diagnostics.

### 11.1 The pre-installation validation hook fails

Read `oc logs job/osac-pre-install-validate -n "$NS" -c validate` while retained. Missing cert-manager CRDs return to [operators.md](operators.md); `No default StorageClass found` is only a warning from this hook, so verify the selected storage provider rather than treating it as a validation failure. Do not disable checks to bypass missing prerequisites.

### 11.2 Other installation failures

- **Pending Helm action:** inspect `oc get jobs,pods -n "$NS"` and the failed container's logs. A successful hook's later absence is normal; do not blindly uninstall an interrupted release to clear its status.

- **Operator CSV, chart or image pull:** use [operators.md](operators.md) for selected catalog/CSV diagnosis and [prereqs.md](prereqs.md) for mirror authentication/trust. Recheck the prepared local 0.0.24 chart and exact missing image, including init containers and both CLI scopes; return missing content to the mirroring workflow, never a public fallback.

- **Database or Certificate:** inspect the retained `osac-db-init` `wait-for-cert`/`psql` logs and Certificate/issuer conditions. Check the prepared database host, client identity and CA trust with the service owner; do not apply the connected guide's older Secret-creation workaround.

- **AAP instance/bootstrap — Git failure example:** check the `osac-aap` instance and bootstrap `wait-for-admin-secret`/`wait-for-aap` containers in `$NS`. If `bootstrap` reports Git unreachable, read `oc logs job/osac-aap-bootstrap -n "$NS" -c bootstrap`, compare the prepared internal URI/ref in [aap-git-source.md](aap-git-source.md), and check reachability/trust from its consumer. Correct the responsible configuration/content before retrying, then repeat Section 7; never switch to public Git.

- **Publishing:** inspect the `osac-publish-templates` `wait-for-fulfillment` and `publish-templates` containers while retained, then the corresponding AAP job. Check the intended project revision/playbook/EE and required token; do not disable publication when the selected service needs its templates.

- **Fulfillment crash or login loop:** inspect the failing `fulfillment-*` Deployment logs; compare actual issuer/API hostnames and the selected database/secret-store endpoints with prepared values. Correct site DNS/trust/configuration rather than applying bundled-service defaults or skipping certificate checks.

- **First request fails with healthy Pods:** for VMaaS, check the requested storage tier/provider registration and mirrored disk-image access, then the corresponding AAP task. Use the selected-service diagnostics in the connected guide; healthy Pods alone do not establish successful provisioning.

## 12. Glossary

Use the [customer installation glossary](../customer-install-guide.md#12-glossary) and the terminology in the earlier disconnected guides.
