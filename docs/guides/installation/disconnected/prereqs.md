# OSAC Disconnected Installation Prerequisites

- [OSAC Disconnected Installation Prerequisites](#osac-disconnected-installation-prerequisites)
  - [1. Execution and input contract](#1-execution-and-input-contract)
    - [Evidence contract](#evidence-contract)
    - [Pre-handoff node transport checkpoint](#pre-handoff-node-transport-checkpoint)
  - [2. Consume the mirror handoff](#2-consume-the-mirror-handoff)
  - [3. Registry access and bounded configuration](#3-registry-access-and-bounded-configuration)
    - [3.1 Node transport and representative image diagnostic](#31-node-transport-and-representative-image-diagnostic)
    - [3.2 Approved Pod-network diagnostic](#32-approved-pod-network-diagnostic)
    - [3.3 Trust and authentication correction — unresolved consumer mapping](#33-trust-and-authentication-correction--unresolved-consumer-mapping)
  - [4. Select providers and record production contracts](#4-select-providers-and-record-production-contracts)
    - [4.1 Mandatory metering provider checkpoint](#41-mandatory-metering-provider-checkpoint)
  - [5. Prepare the release chart and values](#5-prepare-the-release-chart-and-values)
  - [6. Handoff and worked flow](#6-handoff-and-worked-flow)
    - [Secret-key acceptance reference](#secret-key-acceptance-reference)

> [!IMPORTANT]
> This is Doc 2a for OSAC **0.0.62** on OpenShift **4.22**. The normal sequence is the selected-profile [mirroring handoff](README.md), then §2 and the rest of this guide, then [AAP Git source handoff](aap-git-source.md), then [operators and shared infrastructure](operators.md). During Doc 1 only, after full mirroring and before catalog application, its administrator may invoke just the [pre-handoff node transport checkpoint](#pre-handoff-node-transport-checkpoint); this does not start the remaining Doc 2 preparation. [installation.md](installation.md) owns the OSAC Helm release.

This guide specifies disconnected inputs and acceptance gates. It does not install operators, shared services, provider operands, or the OSAC release. Use the canonical guides for configuration semantics; use the actual downloaded 0.0.62 chart for schema, defaults, images, and rendered consumers. Do not copy a different release's image inventory or version table.

| Canonical reference | Responsibility retained there |
| --- | --- |
| [Customer installation guide](../customer-install-guide.md) | Phase 2 configuration and platform lifecycle |
| [Helm deployment guide](../helm-deployment-guide.md) | Phase 1 architecture and standard prerequisite concepts |
| [Kafka configuration](../kafka-configuration.md) | Broker, connection, TLS/SASL, ACL, and rotation contracts |
| [Secrets management configuration](../secrets-management-configuration.md) | Vault/OpenBao roles, policies, claims, credentials, and rotation |

The dependency and infrastructure charts remain reference implementations: do not install or render them as customer prerequisites. BMaaS and CaaS provisioning are outside this workflow.

## 1. Execution and input contract

Use a secured administration host with cluster-admin access. Standard OpenShift, OLM, certificate, registry, and Helm operations are assumed knowledge. Prepare approved clients before the disconnected window; service-specific probes need their canonical clients.

Keep values, endpoint identities, discovery documents, renders, credentials, and checkpoints in owner-only private runtime storage outside source control. Disable tracing, history, recording, and command-output collection on every participating host. Discard raw command stdout/stderr at source; export only allowlisted completion metadata. A private owner review, not copied logs, handles failure.

> [!WARNING]
> One administrator owns shared mutations across all hosts. Local file locks do not serialize other hosts. Existing trust and pull-secret updates must preserve unrelated entries and submit server-enforced resourceVersion preconditions. Conflicts require a fresh read, merge, and review; never force ownership or replay stale writes.

### Evidence contract

Validation is manual and evidence-based. Record the exact inputs, observed identities, and result of each printed check separately in protected runtime evidence. A result covers only that observation; it does not certify the whole preparation workflow or a consistent snapshot across resources. The actual **0.0.62** chart, its schema, and the commands in this guide are the validation surface; no companion toolkit is used. Missing, stale, ambiguous, or malformed evidence is failure, not a skip. No gate creates an operator, approves an InstallPlan, or repairs Doc 1.

| Private input | Required value or source |
| --- | --- |
| `OSAC_CHART_REF`, `OSAC_CHART_VERSION` | `oci://ghcr.io/osac-project/charts/osac`, `0.0.62` |
| `OSAC_NAMESPACE` | `osac`, or the approved consistent release namespace |
| `DOC1_WORKSPACE` | Original oc-mirror workspace; ISC at its recorded authoritative location |
| `MIRROR_HOST` | Exact registry authority including port; never substring matching |
| `MIRROR_CA_FILE`, `MIRROR_AUTH_FILE` | Approved CA path; owner-readable authfile containing only the exact mirror entry and read-only credentials |
| `CATALOG_SOURCE`, `CATALOG_NAMESPACE` | `redhat-operators`, `openshift-marketplace` |
| `VALUES_FILE`, `OSAC_CHART_DIR` | Protected `values-osac.yaml` and exact path to the downloaded/extracted `0.0.62` chart |
| Approved service inputs | Endpoint, namespace, credential/CA references, and owner evidence; no credential bytes in handoffs |

### Pre-handoff node transport checkpoint

This shared diagnostic is first invoked by Doc 1, after the selected inventory has been fully mirrored and before generated catalog resources are applied. Doc 2 later reuses the same checkpoint under [§3.1](#31-node-transport-and-representative-image-diagnostic) for a fresh node-transport observation, for example after node replacement or a relevant trust/authentication change. It is not entry into §2 or the rest of this preparation guide. Do not run it before the mirror contains its approved debug image, and do not install operators, HCO/CDI or OSAC to obtain a diagnostic context.

Use only the execution/privacy/authorization rules in §1 and these independent inputs: the exact approved `MIRROR_HOST`, an approved existing `NODE_DEBUG_NAMESPACE`, and `NODE_DEBUG_IMAGE` as a **direct mirror reference** already present in the selected mirrored inventory. An upstream reference that needs not-yet-applied IDMS/ITMS rewriting is not usable here. The node/debug-image clients' required trust and mirror-read credential bindings must already be prepared by their authorized owners under the approved site contract. Their preparation is not an output of §2 or the later §3 diagnostics. If that contract is unresolved or the image/context is unusable, stop with the responsible owner; do not select a CA/proxy/CDI mechanism, weaken TLS, use a public fallback or upload bastion write credentials.

Node debugging creates privileged diagnostic resources; use it only under the existing administrator/probe authorization and private-output policy. Review the nonempty applicable node set and keep its identities in the private run record. New/replaced nodes require a fresh observation; this list is not a lock or continuing guarantee.

```bash
: "${MIRROR_HOST:?Set the approved registry authority}"
: "${NODE_DEBUG_IMAGE:?Set the approved mirror-resident debug image}"
: "${NODE_DEBUG_NAMESPACE:?Set the approved diagnostic namespace}"
NODES=$(oc --request-timeout=30s get nodes -o name) || exit 1
test -n "$NODES" || exit 1
for NODE in $NODES; do
  oc --request-timeout=30s debug "$NODE" --to-namespace="$NODE_DEBUG_NAMESPACE" --image="$NODE_DEBUG_IMAGE" -- chroot /host sh -c '
    code=$(curl --connect-timeout 10 --max-time 30 -sS -o /dev/null -w "%{http_code}" "https://$1/v2/") || exit 1
    case "$code" in 200|401) exit 0 ;; *) exit 1 ;; esac
  ' sh "$MIRROR_HOST" >/dev/null 2>&1 || {
    printf 'FAIL node-registry-transport\n' >&2
    exit 1
  }
done
```

**Why:** this checks the chosen node HTTP client's transport/TLS result. HTTP 200/401 is not registry authorization, selected-image availability, kubelet pull-secret usability, or CDI-client trust proof. Do not parse the entire `oc debug` output as an HTTP code; the HTTP result is evaluated inside the node command. Apply an owner-reviewed overall operation deadline in addition to the bounded HTTP/API calls, and handle interrupted diagnostic cleanup by recorded identity.

Keep the observed node identities, registry authority, time and individual result in the existing protected run context; no new receipt or compound `H6: pass` is produced. Return to Doc 1's next step after the observation. The HTTP result does not verify mirror authentication; selected-image pulls and later actual importer-client acceptance remain separate observations.

## 2. Consume the mirror handoff

Doc 1 owns one ISC and workspace lineage, registry population, and generated-resource application. Missing inventory, mirror content, mappings, or package visibility returns to Doc 1; do not create replacement resources here.

| Handoff item | OSAC-specific requirement |
| --- | --- |
| `idms-oc-mirror.yaml` | Generated ImageDigestMirrorSet names and exact source-to-mirror rules |
| `itms-oc-mirror.yaml` | Generated ImageTagMirrorSet names and exact tag rules; required by this handoff |
| `cs-redhat-operator-index-v4-22.yaml` | CatalogSource `redhat-operators` in `openshift-marketplace`; exact generated mirror image |
| `cc-redhat-operator-index-v4-22.yaml` | Generated ClusterCatalog identities; use these objects, not any catalog on the cluster |
| `signature-configmap.yaml` | Generated signature identity and Doc 1 managed-signature evidence |
| ISC and workspace | Original configuration, selected inventory, generated files, and incremental lineage |
| Affected pools | Explicit pool names and expected current generations from the change owner |
| VMaaS source selection (when `enable_vmaas=true`) | Nonempty `selected-diskimage-sources.json` with exact selected upstream references and tags; omit for `enable_vmaas=false` |

Check default-source isolation:

```bash
oc get operatorhub cluster -o json | jq -e '.spec.disableAllDefaultSources == true'
```

Expected local result: `true`. An online default source is not a substitute for missing mirror content.

Review the handed-off resource identities and record the adjacent observations separately:

Retain both generated mirror-set types and the generated signature handoff. For the corresponding dependent steps, require the exact CatalogSource name/namespace/image and its `READY` state, and each generated ClusterCatalog's `Serving=True` condition with reason `Available`, using separate resource checks. OLM Classic PackageManifest visibility does not prove ClusterCatalog readiness or vice versa. These observations do not produce a combined Doc 1 identity/content/policy/lineage certificate.

Check affected-pool convergence:

For each affected pool, record its current state separately: unpaused, observedGeneration=current generation, Updated=True, updatedMachineCount=machineCount, unavailable/degraded counts zero, and Degraded=False. Creating an IDMS does not drain or reboot nodes; modifying or deleting an IDMS can cause node disruption. ITMS changes can drain nodes. See the [OpenShift 4.22 mirror-set behavior](https://docs.openshift.com/it/documentation/openshift_container_platform/4.22/html/disconnected_environments/updating-a-cluster-in-a-disconnected-environment). Consume the completed rollout; do not reapply policies here.

| Package visibility | Required channel | Selection |
| --- | --- | --- |
| `openshift-cert-manager-operator` | `stable-v1` | Core |
| `ansible-automation-platform-operator` | `stable-2.6-cluster-scoped` | Core |
| `amq-streams` | `stable` | Core package coverage; the mandatory same-cluster Strimzi metering capability is required independently of the Fulfillment Kafka selection; provider installation/adoption remains governed by Q-0012 |
| `kubevirt-hyperconverged`, `lvms-operator`, `metallb-operator` | Selected approved Doc 1 channels | Additional VMaaS coverage; package presence does not select installation |

Check the selected packages and channels:

Record the source observation for each selected PackageManifest separately. Each must match both `status.catalogSource=redhat-operators` and `status.catalogSourceNamespace=openshift-marketplace`. Operator namespaces, scopes, CSV pins, and Manual approval belong to the single product table in `operators.md`, not a second table here.

## 3. Registry access and bounded configuration

Use the exact approved registry authority in `MIRROR_HOST` (including port) and the separately reviewed DNS name in `MIRROR_DNS_NAME`; do not derive an IPv6/DNS identity by splitting on a colon. Diagnostic images, target nodes, namespaces and public CA paths must be reviewed for this release before use. These are adjacent observations, not a compound registry-readiness certificate.

| Parameter | Contract |
| --- | --- |
| Registry access | Approved DNS, hostname and chain validation; no `-k`, public image fallback or credential-bearing URL |
| Mirror credentials | Approved mirror-read binding; never the bastion mirror-write authfile or an OpenShift API token |
| Shared authentication | Preserve unrelated `openshift-config/pull-secret` entries and existing ownership; a present key is not successful authentication |
| Node diagnostic image | `NODE_DEBUG_IMAGE`: approved direct mirror reference compatible with node debugging, as required by the shared pre-handoff checkpoint; no implicit public-image fallback |
| Pod diagnostic fixture | `PROBE_NAMESPACE`, `PROBE_IMAGE`, `PROBE_CA_FILE`: approved existing namespace, mirror-resident fixture with `sleep`, `getent`, `curl`, and an already available public CA file inside that fixture |

### 3.1 Node transport and representative image diagnostic

For a fresh node-transport observation, use the single [pre-handoff node transport checkpoint](#pre-handoff-node-transport-checkpoint) above under its existing authority and private-output policy. Recheck after node replacement or a relevant trust/authentication change; the earlier observation is not continuing readiness. At this point the Doc 1 handoff, applied mirror policies and affected-pool observations are available, so the representative original-reference pull below remains a separate post-handoff diagnostic.

For one reviewed original tag/digest reference from the selected image inventory, repeat the representative node pull in its approved context; neither a random registry image nor a future workload is a substitute:

```bash
oc --request-timeout=30s debug "${PULL_NODE:?Set the reviewed node resource}" --to-namespace="${NODE_DEBUG_NAMESPACE:?}" --image="${NODE_DEBUG_IMAGE:?}" -- chroot /host crictl pull "${SELECTED_PULL_IMAGE:?Set the exact selected reference}" >/dev/null 2>&1 || {
  printf 'FAIL selected-node-image-pull\n' >&2
  exit 1
}
```

**Why:** a node-runtime diagnostic does not by itself exercise a future Pod's credential injection or CDI's separate HTTP client. Bind the node, exact original reference, mirror policy and result privately; stop on failure rather than switching to a public image. Raw runtime output remains private.

### 3.2 Approved Pod-network diagnostic

For a fresh installation use an approved **existing** probe namespace/context; do not install HCO/CDI/OSAC merely to obtain a future importer Pod. If that future consumer network is not available yet, this generic diagnostic cannot attest it: perform actual importer-client acceptance after product/platform preparation in `installation.md`. The approved fixture CA path below checks that fixture only; it does not configure or settle the production trust mechanism.

Create a fresh server-named Pod only with explicit probe authority. Do not adopt another run's fixed `mirror-network-check` Pod. The approved fixture must provide the tools and CA path below and satisfy site Pod policy; do not loosen policy or add credentials to make it start.

```bash
set -o pipefail
: "${PROBE_NAMESPACE:?Set the approved existing namespace}"
: "${PROBE_IMAGE:?Set the approved mirror-resident fixture}"
: "${PROBE_CA_FILE:?Set the approved CA path already in the fixture}"
: "${MIRROR_DNS_NAME:?Set the approved registry DNS name}"
: "${PROBE_RUN_ID:?Set the approved unique run label value}"
PROBE_JSON=$(jq -n --arg ns "$PROBE_NAMESPACE" --arg image "$PROBE_IMAGE" --arg run "$PROBE_RUN_ID" '{apiVersion:"v1",kind:"Pod",metadata:{generateName:"osac-mirror-network-",namespace:$ns,labels:{"osac-mirror-probe-run":$run}},spec:{automountServiceAccountToken:false,restartPolicy:"Never",containers:[{name:"check",image:$image,command:["sleep","600"]}]}}' | oc --request-timeout=30s create -f - -o json) || exit 1
PROBE_NAME=$(printf '%s\n' "$PROBE_JSON" | jq -er '.metadata.name | select(type == "string" and length > 0)') || exit 1
PROBE_UID=$(printf '%s\n' "$PROBE_JSON" | jq -er '.metadata.uid | select(type == "string" and length > 0)') || exit 1
```

Record the reviewed run-label value before create; it must be unique to this run and satisfy site label policy. If create fails or its response is lost, the write outcome is unknown: stop, recover matching objects with the owner under that recorded run label, and do not blindly retry or delete by prefix. Record namespace/name/UID and the reviewed fixture context in the existing private run record **before** the next block. Persistence failure stops probe execution and requires owner recovery of this newly created identity. The sleep duration bounds the fixture process, not API-object retention. Coordinate all writers; a UID recheck is an observation, not an atomic exec/cleanup lock.

```bash
oc --request-timeout=30s wait pod/"$PROBE_NAME" -n "$PROBE_NAMESPACE" --for=condition=Ready --timeout=2m >/dev/null || exit 1
CURRENT_PROBE_UID=$(oc --request-timeout=30s get pod "$PROBE_NAME" -n "$PROBE_NAMESPACE" -o jsonpath='{.metadata.uid}') || exit 1
test "$CURRENT_PROBE_UID" = "$PROBE_UID" || exit 1
oc --request-timeout=30s exec -n "$PROBE_NAMESPACE" "$PROBE_NAME" -c check -- getent hosts "$MIRROR_DNS_NAME" >/dev/null 2>&1 || exit 1
HTTP_CODE=$(oc --request-timeout=30s exec -n "$PROBE_NAMESPACE" "$PROBE_NAME" -c check -- curl --connect-timeout 10 --max-time 30 --cacert "$PROBE_CA_FILE" -sS -o /dev/null -w '%{http_code}' "https://${MIRROR_HOST:?}/v2/") || exit 1
case "$HTTP_CODE" in 200|401) : ;; *) printf 'FAIL probe-registry-transport\n' >&2; exit 1 ;; esac
```

**Why:** DNS and trusted transport from this fixture are not image-pull authorization or proof that a different importer has the same mounts, CA, egress or credentials. Inspect scheduling/readiness failures under the private owner policy. On success **or failure**, the owner removes only this recorded Pod using a reviewed server-enforced UID delete precondition; no unguarded name-only deletion, namespace deletion or adopted-resource cleanup is prescribed here. Do not claim automated cleanup: this template leaves that separately authorized identity-bound step with the owner.

### 3.3 Trust and authentication correction — unresolved consumer mapping

> [!WARNING]
> **Q-0003 is unresolved.** The removed reference uses `proxy/cluster.spec.trustedCA`; this branch describes `image.config.openshift.io/cluster.spec.additionalTrustedCA.name`. Neither text establishes a single qualified CA-delivery contract for both CRI-O node pulls and CDI importer HTTP clients. Do not infer equivalence, pick one as a fallback, patch either reference, or disable TLS to pass these diagnostics. The site/product owners must approve the exact consumer, object/field/namespace, public CA material and current reconciliation behavior first.

Once that contract is approved, the existing change owner performs only the reviewed bounded trust/authentication repair. Preserve unrelated CA/auth entries; protect ConfigMap/Secret and reference writes with server-enforced version/identity preconditions. A conflict requires a fresh read, merge and review, never force ownership or a stale payload with a newer version. No CA/proxy mutation command or insecure default is selected by this receiving patch. Reuse §2's affected-pool observation for the actual approved change; do not reapply generated mirroring policies here or replace it with an unqualified all-pools wait.

For an unauthorized result, distinguish transport challenge from an authenticated selected-image pull. Review the approved exact mirror-read binding privately; do not print Secret bodies or treat an auth key's presence as success. Missing manifest/blob/repository or failed redirection returns the exact selected reference and original lineage to Doc 1. DNS, trust and authentication stay with this prerequisite owner. Recheck the relevant actual consumer after owner correction; no blanket `PASS registry` or future-consumer readiness is inferred from a generic probe.

## 4. Select providers and record production contracts

These are **installation selections**, not seven invented Helm values. Persist the original names for the downstream consumer; parse the selection file as data, never source it into a shell.

| Selection field | Allowed value and chart relationship |
| --- | --- |
| `profile` | `core` or `core-vmaas`; no independent Helm key |
| `enable_vmaas` | `false` or `true`; must equal `global.services.vmaas.enabled` |
| `with_lvms` | Explicit boolean; true installs LVMS later, only with VMaaS; not an `osac` chart key |
| `with_metallb` | Explicit boolean; true installs MetalLB later, only with VMaaS; not an `osac` chart key |
| `kafka_provider` | `external` or `in-cluster` for Fulfillment Kafka; `kafka.enabled` false or true respectively. Neither option disables mandatory metering or its same-cluster Strimzi User/Topic Operators; see §4.1 |
| `storage_class` | Approved dynamic class; not a global per-PVC Helm override |
| `LOADBALANCER_PROVIDER` | `existing` with `with_metallb=false`, or `metallb` with `with_metallb=true`; protected site input |

`core` requires `enable_vmaas=false`; `core-vmaas` requires true. Set `global.services.caas.enabled=false`, `global.services.bmaas.enabled=false` and `global.services.maas.enabled=false` for both profiles in this workflow. Mirroring optional packages does not authorize consuming disks or installing another load-balancer provider.

| Production dependency | Required private contract and readiness |
| --- | --- |
| PostgreSQL | Approved host/port/database, TLS CA and client identity, backup/restore owner evidence; authenticated temporary-table test returns count `1` |
| Database URL | Released shape: `postgres://<user>@<host>:5432/<database>?sslmode=verify-full&sslrootcert=<mounted-ca>&sslcert=<mounted-cert>&sslkey=<mounted-key>`; actual paths from render |
| Database consumers | `dbInit.host`, `service.database.connection[]`; URL Secret key `url`; exact mounted keys/names come from rendered consumers |
| External Kafka | Fulfillment bootstrap **and advertised broker** addresses, canonical TLS/SASL/ACL completion, credential and CA references; `service.kafka.connection[]` and `service.certs.caBundle`. Skipping its broker installation does not skip the separate metering Strimzi requirement. |
| In-cluster Kafka | Intended Fulfillment `osac-kafka` namespace/cluster, storage selection and mirror-resident operand images; broker/user/ACL checks deferred to `operators.md`, never reported passed early. Mandatory metering capability is an additional post-setup requirement under §4.1. |
| Mandatory metering | For every profile, an approved same-cluster Strimzi User/Topic Operator capability, actual metering coordinates/RBAC/client inputs, stable deployment identity and database configuration from the canonical metering guide; Q-0012 remains TBD. Run §4.1 only after provider setup, before Helm; no provider or namespace default is selected here. |
| Vault/OpenBao | Approved HTTPS `service.vault.endpoint`, namespace/mount/role/issuer/audience and Secret/CA references from the canonical secrets guide |
| Production identity | Approved exact HTTPS issuer/hosts; discovery issuer equality, authorization/token/JWKS endpoints and nonempty signing keys; approved client/role evidence |
| Dynamic storage | Existing provider: actual bound PVC **and consuming workload write/read**; LVMS selected: device approval now, live provider test in `operators.md` |
| LoadBalancer | Existing provider: allocation plus client traffic from required networks; MetalLB selected: approved non-overlapping pool now, live test in `operators.md` |

No passwords, tokens, private keys, or credential-bearing URLs belong in selection/completion files. Endpoint identities stay private. Verify expected Secret **keys**, not merely nonempty Secret data. Do not pre-create Phase 2 chart-owned database URL Secrets or Certificate outputs; external PostgreSQL does not disable those chart writers or the db-init contract.

Check selection consistency and applicable production contracts:

Record the provider selections and each applicable canonical check result separately. Only the explicit in-cluster Kafka/LVMS/MetalLB live gates may be deferred to `operators.md`; external dependencies must already pass their canonical contracts. Deferred is not ready, and file existence alone is not owner verification. Storage Pending/WaitForFirstConsumer is not successful provisioning.

Check identity without sending credentials to unapproved endpoints:

Record each applicable canonical identity check separately; a single observation does not establish the complete identity-service contract. Approve HTTPS scheme and exact hosts before discovery/JWKS requests or credential POST; reject userinfo, unexpected query/fragment and redirects. Match the same approved issuer across the OSAC clients and Vault contract. Missing roles/claims return to the identity/service owner, not a demonstration fixture.

### 4.1 Mandatory metering provider checkpoint

Metering is mandatory for OSAC **0.0.62**, for both `core` and `core-vmaas`. `kafka_provider` and `kafka.enabled` select the Fulfillment Kafka path; `external`/`false` does not suppress metering's `KafkaUser`/`KafkaTopic` resources or its required same-cluster Strimzi User Operator and Topic Operator. Use the [canonical metering configuration](../kafka-configuration.md#metering); Fulfillment credentials do not configure metering.

> **TBD — Q-0012: production Strimzi/metering provider.** The Kafka/platform owners must approve the actual metering broker/cluster coordinates, same-cluster User/Topic Operator ownership and watch scope, served `kafka.strimzi.io/v1` APIs, credential-access RBAC, CA/authentication and availability/retention/recovery contract. Keep that provider decision open; do not select an interim cluster, namespace or broker. For the reference AMQ Streams installation, retain channel `stable`, Manual approval and the exact resolved-release compatibility review in `operators.md`; a running Pod or floating channel alone is not that decision.

**Ordering:** declare this required capability during prerequisite selection, but run the live observations below only **after** the chosen AMQ Streams/Strimzi provider has been installed or owner-approved for adoption through Phase 1 setup, and **before** the OSAC Helm install. The in-cluster Kafka live-gate deferral above includes this mandatory Strimzi capability even when Fulfillment Kafka is external; the external Fulfillment service must still satisfy its own earlier canonical contract. A deferred metering check is required-post-setup, not ready or skipped. Do not require future metering `KafkaUser`/`KafkaTopic` objects, credential-copy outputs or an OSAC workload to perform this checkpoint; those generated objects stay Phase 2-owned. `installation.md` §2 calls this checkpoint after preparation.

In the same approved Kubernetes cluster as OSAC, have the provider owner resolve the exact current Pod name, namespace, UID and container for **each** operator role from the approved current workload/ownership and watch-scope contract. The two roles may share a Pod; do not require two separate Deployments. The `METERING_USER_OPERATOR_*` and `METERING_TOPIC_OPERATOR_*` parameters below are protected diagnostic inputs, not Helm values. Do not select a first list item, match a name substring or substitute an arbitrary healthy container. Until Q-0012 is resolved and those bindings are approved, stop the dependent Helm installation; filling parameter names is not provider approval.

```bash
set -o pipefail
oc --request-timeout=30s get pod "${METERING_USER_OPERATOR_POD:?Resolve the approved User Operator Pod}" -n "${METERING_USER_OPERATOR_NAMESPACE:?}" -o json | jq -e --arg uid "${METERING_USER_OPERATOR_POD_UID:?}" --arg container "${METERING_USER_OPERATOR_CONTAINER:?}" '(.metadata.uid == $uid) and (.metadata.deletionTimestamp == null) and (.status.phase == "Running") and any(.spec.containers[]?; .name == $container) and any(.status.containerStatuses[]?; .name == $container and .ready == true and (.state.running | type == "object"))' || exit 1
oc --request-timeout=30s get pod "${METERING_TOPIC_OPERATOR_POD:?Resolve the approved Topic Operator Pod}" -n "${METERING_TOPIC_OPERATOR_NAMESPACE:?}" -o json | jq -e --arg uid "${METERING_TOPIC_OPERATOR_POD_UID:?}" --arg container "${METERING_TOPIC_OPERATOR_CONTAINER:?}" '(.metadata.uid == $uid) and (.metadata.deletionTimestamp == null) and (.status.phase == "Running") and any(.spec.containers[]?; .name == $container) and any(.status.containerStatuses[]?; .name == $container and .ready == true and (.state.running | type == "object"))' || exit 1
```

Expected local result for each independent read: `true` and exit status zero. Missing inputs, failed/denied API reads, a changed UID, a terminating/non-Running Pod or a missing/non-ready/non-running selected container stop the dependent install; use the responsible provider owner, not a public fallback, broader permissions or automatic InstallPlan approval. Keep only approved per-check metadata outside protected runtime context.

**Why:** these observations establish only that the owner-qualified User and Topic Operator containers are currently running/ready in the intended cluster. They do not prove watch-scope correctness, successful user/topic reconciliation, broker authentication, metering collection or reporting. Keep the Q-0012 owner/API/RBAC/client contract separate, re-review replacement Pods after rescheduling, and complete actual functional metering acceptance in `installation.md` §7.2 after Phase 2; separate reads are not a locked snapshot.

## 5. Prepare the release chart and values

The chart-delivery path is a controlled connected workstation/bastion with approved tunnel/VPN access to the disconnected API. Pull the published OCI chart there; do not use a repository checkout as the release artifact, push the chart into Quay Mirror, or assume oc-mirror v2 mirrors it. Cluster workloads must not fetch public registries or GitHub. Use a fresh chart staging directory; verify/adopt an existing approved chart instead of overwriting it on retry.

```bash
helm pull oci://ghcr.io/osac-project/charts/osac --version 0.0.62 --untar --untardir .
```

Expected: successful pull and extracted `osac/` chart. Registry login is not required for this public chart.

```bash
yq -e '.version == "0.0.62"' osac/Chart.yaml
```

Expected local result: `true`. **Chart acceptance:** Before rendering or handoff, require successful signature verification of the pulled chart's exact OCI digest and approved OSAC publisher identity per the [repository chart-signature guidance](https://github.com/osac-project/osac/blob/37a47700819b4a1e4e51057dd503630f05a426e2/README.md). Retain the verified digest/result in existing private integrity state, bound to the complete chart/dependency contents actually used. A version match or local checksum alone is insufficient; failed verification or changed/unbound chart contents block use.

```bash
helm show values ./osac
```

Expected local result: released defaults. Initialize the protected `VALUES_FILE` only when absent, using a no-clobber workflow; never truncate an existing approved file. Edit values through the canonical contracts; subsequent corrections modify values, not rendered manifests.

| Values delta | OSAC 0.0.62 requirement |
| --- | --- |
| `cliImage`, `aap.bootstrap.cliImage` | Merge `{cliImage: "<approved-mirror-cli-ref>", aap: {bootstrap: {cliImage: "<approved-mirror-cli-ref>"}}}` into protected `$VALUES_FILE`, preserving other `aap` keys; these independent settings otherwise retain Origin `4.20.0` defaults. Do not rely on a root-only or transient `--set` override. |
| CLI-specific acceptance before Helm | Review each rendered CLI consumer and its exact mirror lineage/availability; perform a fresh cluster pull and non-mutating required-tool smoke test in an approved existing probe context. Pull success alone does not prove script compatibility. |
| CLI failure | Unpullable images block hook startup; missing/incompatible tools fail bootstrap/token scripts and can block or time out Helm hooks. Stop on failed preflight, correct approved values or the mirror/tool contract, then rerun both gates; do not bypass hooks. |
| `aap.configAsCode.eeImage`, component images | Exact selected-chart references plus proven mirrored content; never derive component versions from umbrella version |
| `aap.configAsCode.projectGitUri`, `projectGitBranch` | [aap-git-source.md](aap-git-source.md) supplies the internal full-monorepo handoff and exact commit from the actual chart; mirroring the EE does not deliver Git content |
| `service.database.connection[]`, `service.kafka.connection[]`, `service.vault.*` | Preserve canonical structured sources, Secret keys, CA references and rendered ownership |
| `service.auth.controllerCredentials[]`, `service.idp.credentials[]`, `ui.auth.serviceAccount.credentialsSecret` | Governed per-consumer identity references; no invented operator OIDC credential array |
| Storage | Canonical default StorageClass behavior; explicit selection does not imply every PVC consumes a new `storage_class` value |
| Image redirection | Retain released upstream references where correct IDMS/ITMS applies; no `global.imageRegistry` key exists |

```bash
helm lint ./osac --values "$VALUES_FILE"
```

Expected: no lint errors.

```bash
helm template osac ./osac --namespace "$OSAC_NAMESPACE" --values "$VALUES_FILE" > rendered-osac.yaml
```

Expected: protected nonempty render. Do not require global server-side dry-run: operator APIs/webhooks do not yet exist, and Phase 2 installs additional CRDs.

Review the rendered consumers against the selected services and approved values, including unresolved placeholders and embedded credentials, and record those observations separately. Stop the dependent step on an unresolved placeholder, embedded credential or selected-consumer mismatch. Use Doc 1's selected inventory, intended tag/ITMS or digest/IDMS mappings and exact mirror references when reviewing each image. Return an observed missing image, mapping discrepancy or unavailable destination to the mirror owner with the exact reference; a render or registry-prefix observation does not certify exhaustive inventory, policy specificity, availability or provenance. Retain the selected VMaaS `NeverContactSource` requirement; the separate CDI import path remains a later VMaaS acceptance gate. Record each direct-mirror override, including CLI, with its approved original and mirror references in the private inputs. These narrower observations do not waive approved image/source provenance or mirror-only requirements.

## 6. Handoff and worked flow

Review the individual check results and outstanding work with the change owner. Retain the existing private inputs and keep allowlisted check metadata separate; do not turn them into an installation-wide success receipt or require an atomic generation marker. A failure or interruption can leave work and records incomplete: record what was observed, stop the dependent step and recheck live state before resuming. A past record is not continuing readiness or authorization.

| Artifact | Consumer and boundary |
| --- | --- |
| `prerequisite-selection.env` | Protected, data-only selections; retain explicit fields and consistent approved inputs |
| `prereqs-completion.txt` | Private `profile`, `enable_vmaas`, `with_lvms`, `with_metallb`, `kafka_provider`, `storage_class`, `chart_version=0.0.62`, original `values_sha256` |
| `prereqs-artifacts.sha256` and private input state | Retain the original values/render, approved chart identity and protected `OSAC_CHART_DIR` path (consume unchanged in `aap-git-source.md`), plus Doc 1 lineage and owner/resource references for review and resume, not audit export. A checksum comparison observes local bytes against an already approved record; it is not provenance, live-readiness or cross-resource consistency proof. |
| Fresh prerequisite audit directory | Only schema-versioned allowlisted outcomes, timestamp, release/selections and approved configuration checksums; never endpoints, raw logs, source paths, credential-derived hashes or private input files |

For Core + VMaaS with enterprise storage/network and external Kafka, select `core-vmaas`, `enable_vmaas=true`, both `with_*` fields false, the approved storage class, and `kafka_provider=external`. The administrator sees each fixed gate result and retains exact private bindings; the record agrees with values before `operators.md` begins.

If the workload PVC cannot bind or write, record the failed storage observation and stop its dependent work; do not mark that check successful. The storage owner corrects the existing provider, or approves an explicit with-LVMS selection change and its deferred live test. Regenerate affected selection/render checks; never install both providers opportunistically. If a rendered image is missing, return to Doc 1's same lineage rather than substituting a public image. If another administrator changes trust/pull credentials, the stale write must fail and be re-reviewed.

Remove only this run's approved temporary objects using recorded UID/ownership preconditions; never delete a shared namespace or adopted resource. Retain private runtime inputs through Phase 2 and the approved retry window, then dispose under the site's secret-media policy. Continue to [AAP Git source handoff](aap-git-source.md) with the reviewed prerequisite handoff, then to [operators and shared infrastructure](operators.md) with its validated Git handoff; deliberate later values edits preserve the original prerequisite digest and require a new approved checkpoint, not an overwritten past record.

### Secret-key acceptance reference

| Consumer | Required released key contract |
| --- | --- |
| Cluster registry authentication | `openshift-config/pull-secret`: `.dockerconfigjson`; exact mirror entry validated privately |
| PostgreSQL connection | URL source key `url`; client TLS mount keys and producer ownership resolved from the actual render |
| Default Kafka connection | `fulfillment-service-kafka`: `brokers`, `user`, `password`; external sources may remap keys through the canonical structured connection contract |
| Distributed trust | `ca-bundle` ConfigMap key `bundle.pem`; full approved certificate-set verification belongs to `operators.md` |
| AAP license | Selected manifest Secret key `license.zip`; prepared/verified in `operators.md`, not a prerequisite to catalog visibility |

This table describes consumers, not permission to create their inputs now. Final in-cluster Kafka credentials and database Certificate/Secret outputs remain Phase 2-owned; adopt or prepare only the separately governed Phase 1 inputs identified by the rendered ownership contract.
