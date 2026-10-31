# Disconnected OSAC Installation

- [Disconnected OSAC Installation](#disconnected-osac-installation)
  - [1. OSAC installation architecture](#1-osac-installation-architecture)
    - [1.1 What is released](#11-what-is-released)
  - [2. Prerequisites](#2-prerequisites)
  - [3. OSAC component versions](#3-osac-component-versions)
  - [4. Installing OSAC](#4-installing-osac)
    - [4.1 Preparing the cluster](#41-preparing-the-cluster)
    - [4.2 Configuring the Helm values](#42-configuring-the-helm-values)
    - [4.3 Installing OSAC](#43-installing-osac)
  - [5. Helm chart configuration parameters](#5-helm-chart-configuration-parameters)
    - [5.1 Values set on the command line](#51-values-set-on-the-command-line)
  - [6. Installation workflows by service](#6-installation-workflows-by-service)
    - [6.1 Installing OSAC for VMaaS](#61-installing-osac-for-vmaas)
    - [6.1.1 Selected mirror-backed DiskImages](#611-selected-mirror-backed-diskimages)
    - [6.1.2 CDI consumer contract and configuration decision](#612-cdi-consumer-contract-and-configuration-decision)
    - [6.2 Installing OSAC for CaaS with the Netris network backend](#62-installing-osac-for-caas-with-the-netris-network-backend)
    - [6.3 Installing OSAC for BMaaS](#63-installing-osac-for-bmaas)
  - [7. Verifying the installation](#7-verifying-the-installation)
    - [7.1 VMaaS acceptance](#71-vmaas-acceptance)
      - [7.1.1 Approve and identify the fresh request](#711-approve-and-identify-the-fresh-request)
      - [7.1.2 Observe the actual request and generated objects](#712-observe-the-actual-request-and-generated-objects)
      - [7.1.3 Generated DataVolume reference and consumer evidence](#713-generated-datavolume-reference-and-consumer-evidence)
      - [7.1.4 Result and cleanup](#714-result-and-cleanup)
    - [7.2 Mandatory metering acceptance](#72-mandatory-metering-acceptance)
  - [8. Postinstallation tasks](#8-postinstallation-tasks)
    - [8.1 Accessing the OSAC consoles](#81-accessing-the-osac-consoles)
    - [8.2 Installing the `osac` CLI](#82-installing-the-osac-cli)
    - [8.3 Registering the hub](#83-registering-the-hub)
    - [8.4 Installing without DNS management](#84-installing-without-dns-management)
    - [8.5 Additional resources](#85-additional-resources)
  - [9. Supported configurations](#9-supported-configurations)
    - [9.1 Supported for production](#91-supported-for-production)
    - [9.2 Evaluation only](#92-evaluation-only)
    - [9.3 Not covered by this guide](#93-not-covered-by-this-guide)
  - [10. Uninstalling OSAC](#10-uninstalling-osac)
  - [11. Troubleshooting](#11-troubleshooting)
    - [11.1 The pre-installation validation hook fails](#111-the-pre-installation-validation-hook-fails)
    - [11.2 Other installation failures](#112-other-installation-failures)
    - [11.2.1 Bounded event diagnostics](#1121-bounded-event-diagnostics)
    - [11.3 VMaaS and CDI import failures](#113-vmaas-and-cdi-import-failures)
  - [12. Glossary](#12-glossary)

> [!IMPORTANT]
> This is the final document in the disconnected installation sequence for OSAC **0.0.62**: [prereqs.md](prereqs.md) → [aap-git-source.md](aap-git-source.md) → [operators.md](operators.md) → **installation.md**. Complete all three preparation guides before continuing.

## 1. OSAC installation architecture

This guide installs the **0.0.62** `osac` Helm release on a cluster prepared through [prerequisites](prereqs.md), [AAP Git source preparation](aap-git-source.md) and [Operator and infrastructure installation](operators.md). It does not install prerequisite Helm releases.

### 1.1 What is released

Use the prepared local `osac` chart and its approved values. The chart deploys the selected OSAC components and performs their initialization; see the [installation architecture](../customer-install-guide.md#1-osac-installation-architecture) for the common component and phase model.

## 2. Prerequisites

Complete the three preparation guides above for the same cluster, namespace and selected services before continuing. Use the common [cluster/access and client-tool requirements](../customer-install-guide.md#2-prerequisites), replacing public registry/Git access with the prepared disconnected sources.

- Required Operators, trust distribution, selected operands and external services are ready as described in [operators.md](operators.md); do not reinstall them here.

- After that provider setup and before Section 4, complete the [mandatory metering provider checkpoint](prereqs.md#41-mandatory-metering-provider-checkpoint) for both Core and Core + VMaaS, including external Fulfillment Kafka. Resolve Q-0012's actual provider/API/watch-scope/RBAC/client contract; observe its exact User and Topic Operator containers with the bounded reads there. Missing or unresolved capability blocks Helm; do not infer a skip from `kafka.enabled=false`, a CSV alone or prepared values.

- Keep the approved `OSAC_NAMESPACE`, `OSAC_CHART_DIR` and `VALUES_FILE` available in the installation shell. `OSAC_CHART_DIR` is the exact downloaded/extracted **0.0.62** chart path; `VALUES_FILE` is the complete prepared values file.

- Mirror policies, pull credentials, required CLI images, EE and internal Git source are already prepared. Keep the selected service scope and credential/trust references unchanged; return missing preparation to its owning guide.

## 3. OSAC component versions

| Item | Source for this installation |
| --- | --- |
| OSAC chart | Prepared **0.0.62** chart at `$OSAC_CHART_DIR` |
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

Use the **0.0.62** umbrella and subchart defaults/schemas for the parameter reference; the [connected parameter tables](../customer-install-guide.md#5-helm-chart-configuration-parameters) explain common settings. Merge the following inputs into the complete values, preserving other keys. The CLI overrides do not replace `aap.bootstrap.image` or the EE.

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

Follow the [VMaaS workflow](../customer-install-guide.md#61-installing-osac-for-vmaas) with the prepared storage/LoadBalancer providers, network settings, mirrored disk images, internal AAP Git source and EE. Keep the required compute templates and storage-tier registration; do not enable an unselected provider. A complete Core + VMaaS sample for this chart version is [`osac-0.0.62-core-vmaas-values.yaml`](osac-0.0.62-core-vmaas-values.yaml); replace site values, credentials and mirror references before use.

**Execution order:** select the prepared Core + VMaaS values → install once through Section 4 → pass the platform checks in Section 7 steps 1–9 → prepare the selected mirror-backed DiskImage and resolved CDI consumer contract in Sections 6.1.1–6.1.2 → complete the one VMaaS acceptance procedure in Section 7.1 → record the selected-service result in Section 7 step 10 → record mandatory metering functional acceptance in step 11 using Section 7.2's approved contract. If that contract uses this service test as its stimulus, arrange its baseline before the test rather than requiring a metering pass first. Do not require all of Section 7 to be complete before the first test VM, and do not treat ready Pods alone as VM acceptance.

### 6.1.1 Selected mirror-backed DiskImages

For this workflow, H6 is the approved prerequisite evidence that every applicable node reaches and trusts the exact mirror authority. H7 is the approved selected DiskImage source inventory, preserving full upstream references, exact mirror runtime URLs, and original-to-mirror lineage. A repository-only policy-check input does not replace H7.

Run this catalog preparation only after the platform checks in Section 7 steps 1–9 pass. It is not a prerequisite for the first mirror and does not call a live OSAC API to produce Doc 1's original inventory. Use the prepared matching CLI with the approved tenant/API/CA authentication context; reuse Section 8.2 now rather than waiting for the physical postinstallation chapter.

**Compatibility gate:** confirm the target release's DiskImage schema, source immutability, supported create/replace/reference-update operations and matching CLI syntax before changing catalog data. The moved source describes `DiskImage.spec.source_ref` with registry source type; that source description is not proof of this release's API. If this contract is not established, stop catalog mutation and record release reconciliation pending. Do not invent registry credential/CA fields or bypass the OSAC API by applying generated DataVolumes directly.

1. Select only the DiskImages/templates/catalog items required by the approved VMaaS profile. Consume the original H7 full upstream references and their exact mirror runtime URLs, including the selected scheme, authority, port, repository and tag/digest. Preserve their original→mirror lineage; a normalized ITMS repository-only input is not a runtime DiskImage URL or proof of matching content.
2. With the version-qualified read interface, inspect each selected DiskImage's identity and source. The following CLI form is a candidate to reconcile against the installed CLI, not a reason to assume it is accepted:

   ```console
   osac describe diskimage <selected-diskimage-name-or-id>
   ```

3. For the qualified source-reference behavior, create the selected mirror-backed DiskImage using the supported OSAC API/CLI. Its source must point directly at the selected mirror URL when the approved CDI client path does not use node mirror rewriting. Confirm the release's actual authentication/trust capabilities first; do not enable anonymous access broadly or put source/write credentials into a tenant resource. Repository access changes remain with the registry owner and approved H6/H7 contract.
4. To replace an upstream-backed entry whose source field is immutable, create a new mirror-backed DiskImage rather than patching the old source in place. Under the catalog owner's authorization, update the intended tenant-facing templates/catalog items to the new identity. Preserve all unrelated catalog records and concurrent writers; re-read and re-review on conflict rather than force ownership.
5. Before retiring an old DiskImage, use the authoritative target-release association interface to prove no active ComputeInstance or required template/catalog reference still depends on it. If associations or identity-safe retirement are unavailable, leave retirement explicitly pending with the owner; do not infer no consumers from an incomplete list or delete by a guessed name.
6. Re-read the selected new DiskImage/template/catalog bindings and compare their full runtime URLs with the approved handoff. A registry-image presence check or a changed catalog string is not VM import acceptance. Continue through the resolved consumer contract in Section 6.1.2 and the fresh request test in Section 7.1.

The source runtime wiring clause is received here once: selected mirror-backed source references belong in the supported DiskImage lifecycle, not repeated in the known-limitations section and not implemented as a global ITMS workaround for an unqualified client path.

### 6.1.2 CDI consumer contract and configuration decision

Before the selected VM acceptance, consume the release-qualified node-pull versus CDI-importer trust/authentication contract from the prerequisite and Virtualization owners. Identify the exact consumer, registry authority/CA binding, supported owner-controlled configuration surface and intended CDI/HCO objects. Existing node transport or CatalogSource readiness does not establish the importer client's CA/authentication behavior.

**Open decision — do not execute a configuration change:** the supported CRI-O/CDI trust mechanism and whether direct mutation of the HCO-managed CDI CR is permitted are unresolved. Do not choose an interim proxy-only/image-config-only mechanism, insecure registry fallback or ownership force to make the test proceed. An unresolved selected consumer contract is pending qualification, not service acceptance. Resolve this with the service/product owners before using a mutation procedure.

Product-controller readiness stays with operators.md and Section 7 step 3. For an evidenced runtime import failure, a bounded read of the exact qualified CDI object's effective insecure-registry setting can support diagnosis; this read does not authorize enabling it:

```bash
: "${CDI_CR:?Resolve the exact approved CDI object; do not select the first list item}"
oc --request-timeout=30s get cdi "$CDI_CR" \
  -o custom-columns='NAME:.metadata.name,UID:.metadata.uid,RV:.metadata.resourceVersion,INSECURE:.spec.config.insecureRegistries' || {
  echo "ERROR: cannot read the exact CDI configuration; retain API diagnostics privately" >&2
  exit 1
}
```

The source direct-patch approach is retained below only as a **candidate for review, not an executable procedure**. It becomes eligible for an executable runbook only if the product/owner decision explicitly permits that configuration surface and registry exception, the actual release schema is qualified, and the authorized change owner accepts the exact target and blast radius. Otherwise use the eventual owner-approved alternative; no option is selected here. A comment, flag or copied example is not approval.

```text
CANDIDATE ONLY — DO NOT EXECUTE WHILE THE CONSUMER/OWNER DECISION IS OPEN
# Explicit identities and registry authority come from the reviewed contract.
: "${CDI_CR:?Exact approved CDI name required}"
: "${CDI_UID:?Exact approved CDI UID required}"
: "${MIRROR_REG:?Approved registry authority including configured port required}"

# One captured object supplies the preconditions and merged list.
CDI_JSON=$(oc --request-timeout=30s get cdi "$CDI_CR" -o json) || {
  echo "ERROR: cannot read the reviewed CDI target" >&2
  exit 1
}
printf '%s\n' "$CDI_JSON" | jq -e --arg uid "$CDI_UID" '
  .metadata.uid == $uid and
  (.metadata.resourceVersion | type == "string" and length > 0) and
  ((.spec.config.insecureRegistries // []) |
    type == "array" and all(.[]; type == "string"))
' >/dev/null || {
  echo "ERROR: CDI identity/version or existing registry-list shape is invalid" >&2
  exit 1
}

PATCH=$(printf '%s\n' "$CDI_JSON" | jq -ce --arg reg "$MIRROR_REG" '
  (.spec.config.insecureRegistries // []) as $old |
  {
    metadata: {
      uid: .metadata.uid,
      resourceVersion: .metadata.resourceVersion
    },
    spec: {config: {insecureRegistries:
      (if ($old | index($reg)) == null then $old + [$reg] else $old end)
    }}
  }
') || {
  echo "ERROR: cannot construct the reviewed same-snapshot CDI patch" >&2
  exit 1
}

oc --request-timeout=30s patch cdi "$CDI_CR" --type=merge -p "$PATCH" || {
  echo "ERROR: CDI patch failed; inspect the actual API error before remediation" >&2
  echo "Only an evidenced conflict permits re-read/re-merge/re-review; do not blindly retry or force ownership" >&2
  exit 1
}
# Require owner-approved post-change object/consumer validation; no PASS yet.
```

This candidate preserves unrelated list entries and binds UID/resourceVersion to one read; it does not prove that the selected release supports the mutation or that HCO will accept/preserve it. Do not refresh only resourceVersion onto an old list after a conflict, use an unguarded merge, classify all patch errors as concurrency or report consumer trust/import success from patch success. The approved executable runbook must also define owner-managed revert/recovery, post-change state/actual-client verification and the affected blast radius before activation.

### 6.2 Installing OSAC for CaaS with the Netris network backend

CaaS provisioning is outside the current disconnected procedure. The [CaaS/Netris workflow](../customer-install-guide.md#62-installing-osac-for-caas-with-the-netris-network-backend) remains a WIP reference for site backend/DNS settings and hosted-cluster release content, not authorization to enable CaaS in the prepared Core/Core + VMaaS profile.

### 6.3 Installing OSAC for BMaaS

**TBD:** disconnected BMaaS installation and operation have not been validated; this scope covers mirroring only. The [BMaaS workflow](../customer-install-guide.md#63-installing-osac-for-bmaas) is reference material, not a disconnected installation procedure.

## 7. Verifying the installation

Perform platform checks 1–9 in order, using the prepared namespace `$NS`, selected providers and exact release object names. Have the prepared matching CLI artifact and site authentication/CA context available before any CLI/API check; Section 8.2 is a reusable instruction, not a requirement to wait until after service acceptance. Stop at the first failure and see [Section 11](#11-troubleshooting). Use the [connected verification sequence](../customer-install-guide.md#7-verifying-the-installation) only for common commands with site endpoints and trusted CA files, never by disabling TLS verification. Step 10 records the selected-service result after the platform checks, not a recursive request to rerun all of Section 6. Step 11 separately records mandatory metering collection/reporting for every profile under Section 7.2. If its approved stimulus is the service test in step 10, prepare the required baseline before that test and review its result afterward; do not turn metering acceptance into a prerequisite for the first test VM.

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

   In 0.0.62, the pre-install validation Job has `hook-succeeded` cleanup: Helm
   records `last_run.phase: Succeeded` before deleting it. Successful database-init
   and publishing Jobs are also deleted. Later `NotFound` or absence is never
   proof of success; a missing record, missing validation hook or any result other
   than `Succeeded` fails this check. Do not continue to acceptance without this
   positive evidence from the successful installation attempt.

3. Confirm the selected Phase 1 Operator CSVs are `Succeeded` and their operands are ready, using the actual namespaces/provider choices from [operators.md](operators.md).

4. Confirm prepared database, Kafka, secret-store and identity services remain ready. Use the configured issuer's discovery URL, not the connected example's Keycloak hostname or infrastructure namespace.

5. Inspect `oc get pods -n "$NS"` and the selected Deployment/DaemonSet rollouts. Require ready current workloads, including Fulfillment, OSAC Operator, AAP and enabled UI, without image-pull or crash loops. Do not require OSAC CSI components in this namespace: the **0.0.62** chart does not deploy the OSAC CSI routing meta-driver, and provider CSI setup is separately owned.

6. Require the release's Certificates to be `Ready` and database initialization to have succeeded; if it failed, inspect the retained `osac-db-init` Pod/container logs rather than creating its generated certificate Secret yourself.

7. Verify `osac-aap` and successful `osac-aap-bootstrap`/`osac-aap-create-token` execution in `$NS`. In Controller, confirm the intended project source, successful synchronization and expected Git revision, playbook paths and EE.

8. Confirm templates needed by the selected service are present. Do not require CaaS templates for a Core-only installation; inspect the retained publishing failure if required templates are missing.

9. Check the site Fulfillment API `/healthz`, then authenticate with the prepared matching CLI and CA trust and run `osac get tenants`. If UI is enabled, confirm its site URL redirects to the approved identity provider and returns to the OSAC console after login.

10. For selected VMaaS, complete [Section 7.1](#71-vmaas-acceptance) after platform checks 1–9 and its catalog/CDI preparation. For Core-only scope, record the service checks applicable to that scope; do not create a VM or enable an unselected service. Record pass, failure or unresolved qualification explicitly; an unresolved selected VMaaS check is not a successful VMaaS acceptance result.

11. For every selected profile, complete [mandatory metering acceptance](#72-mandatory-metering-acceptance) using a release-qualified, owner-approved procedure that positively demonstrates collection/progress and the resulting report for the approved scope. **TBD — Q-0013:** until its commands, correlation, expected outcomes and observation bounds are approved, record unresolved qualification, not a pass, skip or disabled metering. Pod existence/readiness and Kafka connectivity alone do not satisfy this step.

### 7.1 VMaaS acceptance

This selected-service test runs once, after Section 7 platform checks 1–9 and Sections 6.1.1–6.1.2 preparation. It does not rerun Section 4, reinstall operators or create a second acceptance path in the mirroring guide. Core-only runs do not execute this VM workflow. An unresolved release/client/consumer contract is pending qualification, not a VMaaS pass.

#### 7.1.1 Approve and identify the fresh request

Use the matching authenticated CLI and reviewed catalog item, mirror-backed DiskImage, storage tier, network/subnet and SSH credential reference. Obtain human authorization for the test request's resource use and eventual cleanup. Do not put credential values in command lines or shared logs.

Before creating the request, establish the release-qualified mapping from the OSAC resource identity to its Kubernetes ComputeInstance and generated root DataVolume/PVC/VMI/importer objects. Record the existing relevant identities privately. Use server-issued identities and owner/reference evidence, not a name substring, the first list element or `head -n1`. If the target release does not expose the required association, stop and ask the service owner for the supported method.

Require a fresh root import for this test: capture the new request's identity/time and prove that its root DataVolume/PVC UIDs were not reused from a prior run. A fixed `mirror-validation-root-disk` name scan, an empty/failed API list or a preexisting populated PVC is not that proof. If the implementation intentionally uses a cache, that is a different test; do not report it as the fresh disconnected import requested here.

The moved source's creation flags below require matching-CLI/API qualification. Do not execute them merely because they were valid in the later documentation, substitute unreviewed values, or apply a DataVolume manually to manufacture a passing test:

```console
osac create computeinstance \
  --name <approved-unique-test-name> \
  --catalog-item <reviewed-catalog-item> \
  --disk-image <selected-mirror-backed-diskimage> \
  --boot-disk-storage-tier <prepared-tier> \
  --network-attachment subnet=<prepared-subnet-id> \
  --ssh-key <approved-ssh-secret-reference>
```

Preserve the returned request identity and the API/CLI exit status privately. A failed, ambiguous or timed-out create response is not permission to submit the same request again: first determine whether the original request exists through the supported API, then let the owner choose the retry/recovery action.

#### 7.1.2 Observe the actual request and generated objects

Resolve and validate the exact `CI_NS`, `CI_NAME`, root `DV_NAME`, `PVC_NAME`, `VMI_NAME` and, when present, `IMPORTER_POD` from the qualified association contract. Keep their UIDs/resource versions and the request linkage in private evidence. The following reads/waits are proposed bounded observation forms, not a resource-discovery algorithm; all input identities must already be resolved and reviewed. Reconfirm object UIDs before and after observation so a replacement cannot be mistaken for the original result.

```bash
: "${CI_NS:?Resolve the exact test namespace}"
: "${CI_NAME:?Resolve the exact ComputeInstance name}"
: "${DV_NAME:?Resolve its exact root DataVolume name}"
: "${PVC_NAME:?Resolve its exact root PVC name}"
: "${VMI_NAME:?Resolve its exact VirtualMachineInstance name}"

oc --request-timeout=31m wait -n "$CI_NS" "computeinstance/$CI_NAME" \
  --for=condition=Ready --timeout=30m || {
  echo "ERROR: exact ComputeInstance did not satisfy Ready; retain its conditions/events/logs" >&2
  exit 1
}
oc --request-timeout=31m wait -n "$CI_NS" "datavolume/$DV_NAME" \
  --for=jsonpath='{.status.phase}'=Succeeded --timeout=30m || {
  echo "ERROR: exact root import did not reach Succeeded; use Section 11.3" >&2
  exit 1
}
oc --request-timeout=30s get datavolume "$DV_NAME" -n "$CI_NS" \
  -o custom-columns='NAME:.metadata.name,UID:.metadata.uid,PHASE:.status.phase,PROGRESS:.status.progress,CLAIM:.status.claimName' || {
  echo "ERROR: cannot read exact root DataVolume state" >&2
  exit 1
}
oc --request-timeout=30s get pvc "$PVC_NAME" -n "$CI_NS" -o json || {
  echo "ERROR: cannot read exact root PVC identity/state" >&2
  exit 1
}
oc --request-timeout=31m wait -n "$CI_NS" "virtualmachineinstance/$VMI_NAME" \
  --for=jsonpath='{.status.phase}'=Running --timeout=30m || {
  echo "ERROR: exact VMI did not reach Running; inspect the linked launcher and node evidence" >&2
  exit 1
}
```

Review the actual OSAC result with the matching supported describe/get interface as well as the Kubernetes conditions. Require the intended request's service result, successful fresh import and Running VMI; a summary state, ready platform Pod, successful API health request or reused root disk alone does not pass this test. The observation field names and condition behavior above must match the selected release; do not guess a fallback condition when qualification fails.

#### 7.1.3 Generated DataVolume reference and consumer evidence

The following moved source example describes the generated output being observed. It is not a manifest to apply, an override of controller-owned state or proof the target branch emits these exact fields. Compare the actual request-linked DataVolume against the qualified release's behavior, selected mirror URL and storage contract.

```yaml
apiVersion: cdi.kubevirt.io/v1beta1
kind: DataVolume
metadata:
  name: <actual-request-root-datavolume>
spec:
  source:
    registry:
      url: "docker://<selected-mirror-authority-with-port>/<containerdisk-image>:<selected-tag>"
      # The moved example omitted pullMethod and secretRef.
      # Confirm actual target-release behavior; do not add guessed fields.
  pvc:
    accessModes:
      - ReadWriteOnce
    resources:
      requests:
        storage: <selected-boot-disk-size>
    storageClassName: <selected-storage-class>
```

Inspect the actual importer's effective client path, source, authentication and CA mechanism through the resolved Section 6.1.2 contract. Node CRI-O pull evidence is not a substitute for that client. Do not overwrite generated DataVolume/PVC objects to bypass a failed service configuration.

If the exact importer Pod still exists, preserve its logs in the approved private diagnostic location. Resolve its identity/UID from the request/PVC association, not a name substring; if it was removed after completion, use the owner's retained evidence and explicitly record the live-log gap rather than selecting a different importer. Example bounded read, only after those identity checks:

```bash
: "${IMPORTER_POD:?Resolve the exact request-linked importer Pod}"
oc --request-timeout=60s logs -n "$CI_NS" "$IMPORTER_POD" \
  --all-containers --timestamps --tail=500 || {
  echo "ERROR: cannot read exact importer logs; do not report the log check as passed" >&2
  exit 1
}
```

This bounded excerpt is diagnostic, not a complete network audit. Correlate original request/object identities, creation times, state transitions and errors. Keep full available evidence privately when required by the test owner. No upstream URL in a log excerpt does not prove no public contact. Require independently approved egress-denial/observation evidence associated with the actual import to claim that property; otherwise record only what was observed and leave no-external-contact unproven, not accepted by inference.

#### 7.1.4 Result and cleanup

Record the selected profile and exact request, DiskImage/runtime source, storage/network selection, root DataVolume/PVC/VMI identities, relevant conditions, timestamps and any diagnostic/egress limits in private test evidence. Report pass, failure or unresolved qualification separately for each responsibility; do not certify a full VMaaS path from only some successful observations. Redact credentials and unrelated tenant/site material before sharing a summary.

After evidence capture, the test owner retires only the approved test request through the supported OSAC lifecycle. Use the release-qualified identity-safe retirement/ownership procedure; verify identity again and preserve unrelated workloads. A pre-delete name check followed by unconditional name-based deletion is not an atomic identity safeguard. If a supported safe retirement operation is not established, record cleanup pending with its owner rather than inventing a delete flag or deleting generated root resources/namespace by hand.

Verify the intended test request's retirement and expected owned-resource cleanup through the supported interfaces. Leave shared DiskImages, templates, service credentials, operators and infrastructure intact. A failed or unverified cleanup is not silently reported as completed.

### 7.2 Mandatory metering acceptance

> **TBD — Q-0013: production metering acceptance.** Metering is mandatory, but the release-qualified functional commands, observable/reporting contract, observation window and selected monitoring/alert criteria remain for the metering/service, Kafka, operations and QE owners to approve. This section is a non-executable acceptance shape, not an approved test or a default passing result. Until those choices and their evidence are supplied, record metering as **unresolved qualification**, not passed, skipped or disabled.

After the Phase 2 platform is available, use only the approved contract to show both collection/progress and the resulting report for the intended deployment/tenant/test scope. Prepare its baseline and observation context before its approved stimulus. If Q-0013 chooses the already-authorized service test from step 10 as that stimulus, capture the needed baseline first and review the metering result in step 11 afterward; do not require a metering pass before the first VM. Core-only acceptance must not create a VM or enable an unselected service merely to produce a billing event.

```text
TBD Q-0013 — NON-EXECUTABLE ACCEPTANCE SHAPE; NO DEFAULT PASS
Owner and release/schema-qualified procedure: <TBD, approved by named service/operations owners>
Approved deployment/tenant/test scope and correlation method: <TBD; keep site/tenant identities private>
Baseline and observation start: <TBD approved source and timestamp before the stimulus>
Authorized stimulus or explicitly approved existing signal: <TBD; exact identity/time and safe retry semantics>
Collection/progress evidence: <TBD release-qualified observation showing this scope's signal was consumed/processed>
Reporting evidence: <TBD approved resulting usage/report/billing observable correlated by the agreed semantics>
Expected values, units/aggregation and maximum observation window: <TBD; no guessed lag/metric threshold>
Selected metrics/scrape/alert and control/negative case, if required: <TBD owner-approved isolated procedure>
Private evidence and allowlisted result review: <TBD; do not export raw payloads, credentials or unrelated tenant data>
Failure/inconclusive handling and test cleanup owner: <TBD; no blind stimulus replay or unrelated-resource deletion>
```

The completed procedure must positively demonstrate the approved signal-to-metering-progress-to-report relationship, within the approved bounds; unrelated advancing counters, a Ready Pod, Kafka reachability or absence of an error is not that proof. Do not assume one input event maps to one report without the approved aggregation semantics. A denied/failed read, expired observation window or missing correlation is failure or unresolved qualification under that approved procedure, never success by inference. Do not select M360, another adapter, a monitoring stack, an endpoint, a test stimulus or a threshold as an interim answer.

Keep the metering result separate from the selected-service result. Do not combine the checks into a synthetic atomic installation-wide receipt, pre-create chart-owned outputs, bypass TLS or change the provider to make the test pass.

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

Use the tagged **0.0.62** release with the external database, identity and secret-store contracts prepared through [operators.md](operators.md). The [connected production section](../customer-install-guide.md#91-supported-for-production) describes the common topology; follow this guide's release-specific resource ownership when examples differ. Mirrored content does not broaden service qualification.

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

- **Operator CSV, chart or image pull:** use [operators.md](operators.md) for selected catalog/CSV diagnosis and [prereqs.md](prereqs.md) for mirror authentication/trust. Read the exact failing Pod/container image and pull error, including init containers; compare it with the approved released inventory and generated mirror-policy handoff. Return an exact missing image and original ISC/workspace lineage to the mirror owner; do not start an unrelated mirror run or use a public fallback. For a missing CLI image, recheck both `cliImage` and independent `aap.bootstrap.cliImage` in the complete prepared values and local chart render (Section 5). Do not copy a source guide's root-only `--set cliImage` or example tag as a release repair.

- **Helm connection refused before release creation:** distinguish cluster/API transport failure from a hook/application failure. From the approved administrator context, check `oc --request-timeout=30s cluster-info`; if this site uses a prepared SSH tunnel, have its owner verify that path. Do not install a second release, rebuild credentials or assume every site uses the source example's tunnel topology.

- **Database or Certificate:** inspect the retained `osac-db-init` `wait-for-cert`/`psql` logs and Certificate/issuer conditions. Check the prepared database host, client identity and CA trust with the service owner; do not apply the connected guide's older Secret-creation workaround.

- **AAP instance/bootstrap — Git failure example:** check the `osac-aap` instance and bootstrap `wait-for-admin-secret`/`wait-for-aap` containers in `$NS`. If `bootstrap` reports Git unreachable, read `oc logs job/osac-aap-bootstrap -n "$NS" -c bootstrap`, compare the prepared internal URI/ref in [aap-git-source.md](aap-git-source.md), and check reachability/trust from its consumer. Correct the responsible configuration/content before retrying, then repeat Section 7; never switch to public Git.

AAP instance/runtime failure details use the release's actual AAP instance in `$NS`, not the AAP Operator namespace. Reuse the prepared license, Git, EE, TLS and credential bindings from the earlier guides; do not create a second license upload or AAP installation procedure here.

| Symptom | Evidence to inspect and owning correction |
| --- | --- |
| Controller remains Pending | Inspect the exact Pod's scheduling/PVC events and required image pulls. Distinguish capacity/storage/scheduling from a missing image; Pending alone does not prove a mirror failure. |
| Redis ImagePullBackOff | Read the failing container's exact image/error and compare it to the selected AAP bundle/operand inventory. Route missing content to the mirror owner and auth/trust to prerequisites; do not assume an unqualified RELATED_IMAGE variable fixes it. |
| PostgreSQL ImagePullBackOff | Identify whether the failure is an AAP-managed operand or a separately supplied database. Inspect exact image/pull evidence and preserve the approved database ownership; do not deploy a bundled database as a fallback. |
| License rejected, missing or expired | Compare the AAP instance's configured manifest-Secret name/namespace/key with the approved license contract in operators.md; check validity with its owner without printing the Secret or license data. Reuse that contract rather than hard-code the source example's Secret name or re-upload it here. |
| Ansible job reports EE image not found | Inspect the failed task's effective EE reference and expected project revision, compare the exact reference with the approved mirrored EE inventory, and route missing content to the mirror owner. CLI/bootstrap images do not substitute for an EE. |
| Controller Route unavailable or TLS fails | Inspect the release-bound Route/service endpoints and Certificate/issuer conditions in the instance namespace. Distinguish route creation, backend readiness, DNS and TLS/CA/hostname errors. Do not select the first Route in a namespace or disable TLS verification. |

After the responsible owner corrects the evidenced cause, repeat the failed platform check and selected-service acceptance. A successful generic Pod or Route observation is not proof that project synchronization, playbook execution or VM provisioning worked.

- **Publishing:** inspect the `osac-publish-templates` `wait-for-fulfillment` and `publish-templates` containers while retained, then the corresponding AAP job. Check the intended project revision/playbook/EE and required token; do not disable publication when the selected service needs its templates.

- **Fulfillment crash, identity failure or login loop:** inspect the exact failing release workload's logs and compare actual issuer/API hostnames with the prepared identity, database and secret-store contracts. If a separately managed Keycloak workload is in CrashLoopBackOff, its owner must distinguish database availability/authentication from CA/hostname verification and application configuration using that deployment's own logs and health evidence. Do not assume this OSAC release owns a Keycloak Pod, PostgreSQL StatefulSet or trust-manager Bundle. Reuse the database/Certificate diagnosis above and the actual service-owner runbook; do not restart or reconfigure shared services, install bundled replacements or skip TLS checks as an automatic repair.

- **First request fails with healthy Pods:** for selected VMaaS, follow [Section 11.3](#113-vmaas-and-cdi-import-failures) using the exact request/generated-object identities, storage tier/provider registration, mirror-backed disk source and corresponding AAP task. Reuse the one [Section 7.1](#71-vmaas-acceptance) acceptance path after correction; healthy platform Pods alone do not establish successful provisioning.

### 11.2.1 Bounded event diagnostics

For a failing installation or AAP workload, inspect events in its exact namespace first. These read-only observations are diagnostics, not acceptance gates: absence of an event or a matching message does not prove success. Failed and BackOff events must be distinguished from a denied/timed-out API read. Keep event messages private because they can contain site or credential-related context.

```bash
oc --request-timeout=30s get events -n "$NS" \
  --field-selector reason=Failed -o wide || {
  echo "ERROR: cannot read Failed events in the installation namespace" >&2
  exit 1
}
oc --request-timeout=30s get events -n "$NS" \
  --field-selector reason=BackOff -o wide || {
  echo "ERROR: cannot read BackOff events in the installation namespace" >&2
  exit 1
}
```

When the responsible namespace is unknown and the operator has approved cluster-wide diagnostic access, inspect Failed events across namespaces, then narrow to the exact involved workload. Do not use a grep exit status as an installation verdict or assume every failure is an image-pull error.

```bash
oc --request-timeout=30s get events -A --field-selector reason=Failed \
  -o custom-columns='NAMESPACE:.metadata.namespace,OBJECT:.involvedObject.name,REASON:.reason,MESSAGE:.message' || {
  echo "ERROR: cannot read cluster-wide Failed events" >&2
  exit 1
}
```

Correlate the namespace/object, UTC observation time, reason and failing container/image with the retained workload state and logs. Preserve API stderr privately; classify missing content, registry auth/TLS/network, scheduling/storage, and application errors from evidence before choosing remediation. Use scoped diagnostics from prerequisites/operators rather than launch a second probe here.

### 11.3 VMaaS and CDI import failures

Start with the exact failed OSAC request and its qualified ComputeInstance/root DataVolume/PVC/VMI/importer association from Section 7.1. Distinguish a denied/failed observation from an absent resource and from an unhealthy observed resource. Reuse Section 11.2.1 for bounded events and the approved prerequisite diagnostics for network/authentication/trust; do not create a second node or temporary-Pod probe here. Keep logs and site/tenant details private.

| Symptom | Evidence and remediation owner |
| --- | --- |
| Request remains STARTING and importer attempts an upstream registry or times out | Compare the selected DiskImage full runtime source and actual DataVolume source with the approved H7 mirror URL; determine the importer's qualified client path. If an immutable upstream-backed catalog entry is wrong, use Section 6.1.1's reviewed replacement/reference lifecycle, not an in-place source patch or guessed global ITMS repair. Check network/DNS separately before concluding a source mismatch. |
|  Importer reports 401 Unauthorized. | Inspect the actual consumer authentication capability and selected repository's approved access policy. Return mismatches to the registry/VMaaS owners; do not make every mirror repository public, copy bastion/source write credentials to the cluster or invent a DiskImage secretRef. Apply only the resolved release contract. |
| Importer reports x509 unknown authority or hostname/TLS error | Distinguish CA chain/hostname/endpoint errors using the actual importer consumer's evidence. Return to the approved prerequisites/Virtualization trust owner and Section 6.1.2's unresolved decision when applicable. Do not assert proxy trustedCA covers every client, overwrite an existing CA reference or automatically choose insecureRegistries/skip verification. |
| Request fails after import starts or completes | Inspect exact request conditions, root DataVolume phase/progress, PVC state, selected storage tier/provider and corresponding AAP task. Compare observed timestamps and linked identities; retain the actual error rather than treating all timeouts as flakes. A Succeeded import alone is not a Running VM/service pass. |
| virt-launcher ImagePullBackOff or VMI does not start | Resolve the request-linked launcher workload and its exact failing Pod image/error. This is a node/pod pull path, distinct from the CDI registry import. Route missing selected Virtualization images to the mirror owner, product readiness to operators.md, and auth/trust/network to prerequisites. Do not reinstall an unselected operator or bypass a failed image check. |

After an owner-authorized correction, repeat the failed observation and relevant fresh-import/VM acceptance; do not manufacture success by patching controller-owned DataVolumes, reusing an old root disk or deleting unrelated tenant objects. Distinguish diagnosed cause, successful remediation and outstanding qualification/cleanup in the result. Use retained/importer/egress evidence only to the extent it actually supports each claim.

## 12. Glossary

Use the [customer installation glossary](../customer-install-guide.md#12-glossary) and the terminology in the earlier disconnected guides.
