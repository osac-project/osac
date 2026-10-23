# OSAC Disconnected Installation Prerequisites

> [!IMPORTANT]
> This is Doc 2a of the disconnected installation guide. Begin only after the selected-profile handoff in the [Disconnected Mirroring Guide](README.md) passes. Complete this document before [preparing operators and shared infrastructure](operators.md).

This guide consumes the read-only handoff from Doc 1, validates cluster access to mirrored content, configures cluster trust and pull credentials where required, records production service contracts, and builds the customer-owned OSAC values file. It does not install operators, operands, shared OSAC infrastructure, or the OSAC Helm release.

Run cluster commands from a secured administration host with `oc`, `jq`, `yq`, `dig` or `nslookup`, `openssl`, `curl`, `skopeo`, `sha256sum`, and Helm installed. The host must reach the OpenShift API and mirror registry. Section 5 additionally requires a controlled workstation or bastion laptop that can reach both the public OCI chart registry and the disconnected cluster API through the approved SSH tunnel or VPN.

Install these clients only when their corresponding production service is selected:

- `psql` for external PostgreSQL verification.
- `vault` or `bao` for external Vault/OpenBao verification.
- `kcat` for external Kafka verification.

Log in as a cluster administrator. Replace every value enclosed in angle brackets before running a command.

```bash
# Use a dedicated Bash 4.4+ session, not a shared interactive shell.
set -Eeuo pipefail
shopt -s inherit_errexit
umask 077
trap 'rc=$?; printf "ERROR: prereqs check failed: line=%s status=%s; stop before the next section.\n" "$LINENO" "$rc" >&2; exit "$rc"' ERR
PREREQ_RUN_DIR="$(mktemp -d "${TMPDIR:-/tmp}/osac-prereqs.XXXXXXXX")"
export PREREQ_RUN_DIR
cd "$PREREQ_RUN_DIR"
# Copy approved site input files here; keep the same session for all sections.
export PROFILE=core                    # core or core-vmaas; match Doc 1
export ENABLE_VMAAS=false              # core:false or core-vmaas:true only
case "$PROFILE:$ENABLE_VMAAS" in
  core:false|core-vmaas:true) ;;
  *) printf 'ERROR: use core:false or core-vmaas:true and match Doc 1.\n' >&2; exit 1 ;;
esac
export CATALOG_SOURCE=redhat-operators
export CATALOG_NAMESPACE=openshift-marketplace
export MIRROR_HOST='<mirror.example.com:8443>'
export MIRROR_CA_FILE='<absolute-path-to-mirror-ca.pem>'
export MIRROR_AUTH_FILE='/secure/mirror-readonly-auth.json'
export DOC1_WORKSPACE='<absolute-path-to-oc-mirror-workspace>'
export OSAC_CHART_REF='oci://ghcr.io/osac-project/charts/osac'
export OSAC_CHART_VERSION='0.0.24'
export OSAC_NAMESPACE=osac
```

> [!WARNING]
> Use one administrator for the mutation window in Sections 2 and 3. Record current state before changing the cluster pull secret or trust configuration. Every shared-resource mutation below carries a server-enforced `resourceVersion` precondition; if another administrator changes the resource, the API rejects the stale write and you must re-read and merge again.

## 1. Consume the Doc 1 Handoff

Doc 1 uses one authoritative `imageset-config.yaml`, one `oc mirror` workspace lineage, and one dry-run/full-run pair for the selected profile. It applies five generated artifacts before handing ownership to this guide:

- `idms-oc-mirror.yaml`
- `itms-oc-mirror.yaml`
- `cs-redhat-operator-index-v4-22.yaml`
- `cc-redhat-operator-index-v4-22.yaml`
- `signature-configmap.yaml`

Doc 1 also disables default OperatorHub sources, names the generated CatalogSource `redhat-operators` through `targetCatalog`, waits for MachineConfigPools, verifies selected package manifests, and archives the ISC/workspace metadata for incremental updates.

### Verify the archived handoff lineage

```bash
test -f "$DOC1_WORKSPACE/../imageset-config.yaml"
test -f "$DOC1_WORKSPACE/working-dir/cluster-resources/idms-oc-mirror.yaml"
test -f "$DOC1_WORKSPACE/working-dir/cluster-resources/itms-oc-mirror.yaml"
test -f "$DOC1_WORKSPACE/working-dir/cluster-resources/cs-redhat-operator-index-v4-22.yaml"
test -f "$DOC1_WORKSPACE/working-dir/cluster-resources/cc-redhat-operator-index-v4-22.yaml"
test -f "$DOC1_WORKSPACE/working-dir/cluster-resources/signature-configmap.yaml"

yq -e '.mirror.operators[] | select(.targetCatalog == "redhat-operators")' \
  "$DOC1_WORKSPACE/../imageset-config.yaml" >/dev/null
```

All checks must succeed. If the ISC, workspace, or generated artifacts are unavailable, stop and return to Doc 1. Do not reconstruct the handoff from cluster state alone; the archived lineage is required for reproducible incremental mirroring.

### Verify default OperatorHub source state

```bash
oc get operatorhub cluster \
  -o jsonpath='disableAllDefaultSources={.spec.disableAllDefaultSources}{"\n"}'
oc get operatorhub cluster -o json \
  | jq -e '.spec.disableAllDefaultSources == true'
```

The first command must print `disableAllDefaultSources=true`; the second must return `true`. If not, stop and return to Doc 1. Do not continue with online default catalogs enabled.

### Verify both generated mirror sets

Doc 1 applies both IDMS and ITMS for every selected profile. The unified ISC contains tag-referenced operator, dependency, OSAC, AAP, and optional VMaaS images. VMaaS containerdisks additionally require ITMS and `NeverContactSource` for the selected `DiskImage` source set.

```bash
oc get imagedigestmirrorset
oc get imagetagmirrorset

idms_resources="$(oc get imagedigestmirrorset -o name 2>/dev/null)"
itms_resources="$(oc get imagetagmirrorset -o name 2>/dev/null)"
test -n "$idms_resources"
test -n "$itms_resources"
printf '%s\n' "$idms_resources"
printf '%s\n' "$itms_resources"
```

Both lists must be nonempty. Compare the live resources with the generated files:

```bash
# Requires Mike Farah yq v4. Mirror preference order is significant.
cat > "$PREREQ_RUN_DIR/normalise-policy.jq" <<'JQ'
[.[] | if .kind == "List" then .items[] else . end |
 select(.kind == $kind) |
 {name: .metadata.name,
  mappings: [.spec[$field][] |
    {source, mirrors, mirrorSourcePolicy: (.mirrorSourcePolicy // "AllowContactingSource")}
  ] | sort_by(.source, .mirrorSourcePolicy, .mirrors)}] |
if length == 0 or (map(.name) | length != (unique | length)) then
  error("missing or duplicate expected policy identity")
elif all(.[]; (.name | type == "string" and length > 0) and
  (.mappings | length > 0) and all(.mappings[];
    (.source | type == "string" and length > 0) and
    (.mirrors | type == "array" and length > 0) and
    all(.mirrors[]; type == "string" and length > 0))) then sort_by(.name)
else error("invalid policy mapping") end
JQ
verify_doc1_policies() {
  local kind resource field artifact stem
  for kind in ImageDigestMirrorSet ImageTagMirrorSet; do
    case "$kind" in
      ImageDigestMirrorSet)
        resource=imagedigestmirrorset; field=imageDigestMirrors
        artifact=idms-oc-mirror.yaml; stem=idms ;;
      ImageTagMirrorSet)
        resource=imagetagmirrorset; field=imageTagMirrors
        artifact=itms-oc-mirror.yaml; stem=itms ;;
    esac
    yq ea -o=json -I=0 '.' "$DOC1_WORKSPACE/working-dir/cluster-resources/$artifact" \
      > "$PREREQ_RUN_DIR/$stem-documents.json" || return
    jq -s --arg kind "$kind" --arg field "$field" \
      -f "$PREREQ_RUN_DIR/normalise-policy.jq" \
      "$PREREQ_RUN_DIR/$stem-documents.json" \
      > "$PREREQ_RUN_DIR/$stem-expected.json" || return
    oc get "$resource" -o json > "$PREREQ_RUN_DIR/$stem-live-list.json" || return
    jq '.items' "$PREREQ_RUN_DIR/$stem-live-list.json" \
      | jq --arg kind "$kind" --arg field "$field" \
          -f "$PREREQ_RUN_DIR/normalise-policy.jq" \
      > "$PREREQ_RUN_DIR/$stem-live.json" || return
    jq -e --slurpfile expected "$PREREQ_RUN_DIR/$stem-expected.json" '
      . as $live | all($expected[0][]; . as $e | any($live[]; . == $e))
    ' "$PREREQ_RUN_DIR/$stem-live.json" >/dev/null || return
    jq -e --arg mirror "$MIRROR_HOST" '
      all(.[]; all(.mappings[]; any(.mirrors[]; split("/")[0] == $mirror)))
    ' "$PREREQ_RUN_DIR/$stem-expected.json" >/dev/null || return
    printf 'PASS: %s archived identities and mappings match live state.\n' "$kind"
  done
  jq -s '
    [.[0][] | .mappings[] | . + {mode: "digest"}] +
    [.[1][] | .mappings[] | . + {mode: "tag"}]
  ' "$PREREQ_RUN_DIR/idms-expected.json" "$PREREQ_RUN_DIR/itms-expected.json" \
    > "$PREREQ_RUN_DIR/verified-image-policies.json" || return
}
verify_doc1_policies
```

Every archived name and complete normalized mapping list must match its live object. Ignore server metadata and mapping-entry order, not mirror preference order. Both policy kinds remain mandatory; unrelated live resources cannot substitute for archived identities. Missing/changed mappings stop at Doc 1; do not repair policies here. Retain snapshots for Section 5 and diagnosis.

When VMaaS is selected, verify the exact containerdisk source set recorded by Doc 1 and require `NeverContactSource`.

Doc 1 does not emit `selected-diskimage-sources.json` as a generated cluster resource. Create it from the approved `DiskImage` source list used by the VMaaS mirroring procedure. The file is a JSON array of exact upstream image references, including tags, for only the `DiskImage` objects selected for this installation:

```json
[
  "registry.redhat.io/rhel9/rhel-guest-image:9.6",
  "registry.redhat.io/rhel8/rhel-guest-image:8.10"
]
```

Keep this file with the Doc 1 handoff evidence and regenerate it whenever the selected `DiskImage` set changes. Then verify the live ITMS entries:

```bash
if test "$ENABLE_VMAAS" = true; then
  test -s selected-diskimage-sources.json
  jq -e 'type == "array" and length > 0 and all(.[]; type == "string" and length > 0)' \
    selected-diskimage-sources.json
  oc get imagetagmirrorset -o json \
    | jq -e --arg mirror "$MIRROR_HOST" \
        --slurpfile selected selected-diskimage-sources.json '
      [.items[].spec.imageTagMirrors[]] as $entries |
      all($selected[0][]; . as $image |
        ($image | sub("^docker://"; "") | sub(":[^/:]+$"; "")) as $repository |
        ($image | contains("@") | not) and
        ($image | test(":[^/:]+$")) and
        any($entries[]; .source as $source |
          ($repository == $source or ($repository | startswith($source + "/"))) and
          .mirrorSourcePolicy == "NeverContactSource" and
          any(.mirrors[]?; split("/")[0] == $mirror)))'
fi
```

The command must return `true`. If the selected source file is missing or any entry lacks `NeverContactSource`, return to the VMaaS mirroring procedure in Doc 1.

### Verify CatalogSource, ClusterCatalog, and signature ConfigMap

Doc 1 sets `targetCatalog: redhat-operators`; later Subscriptions use that exact source name in `openshift-marketplace`.

```bash
oc get catalogsource redhat-operators -n openshift-marketplace \
  -o jsonpath='name={.metadata.name}{" state="}{.status.connectionState.lastObservedState}{" image="}{.spec.image}{"\n"}'
oc get catalogsource redhat-operators -n openshift-marketplace -o json \
  | jq -e --arg mirror "$MIRROR_HOST" '
      .metadata.name == "redhat-operators" and
      .metadata.namespace == "openshift-marketplace" and
      .status.connectionState.lastObservedState == "READY" and
      (.spec.image | split("/")[0]) == $mirror'
```

The first command must show `name=redhat-operators state=READY`; the second must return `true`.

Verify every generated ClusterCatalog is serving:

```bash
cluster_catalog_names="$(oc get clustercatalog -o name)"
test -n "$cluster_catalog_names"
mapfile -t cluster_catalogs <<< "$cluster_catalog_names"
test "${#cluster_catalogs[@]}" -gt 0
for catalog in "${cluster_catalogs[@]}"; do
  oc wait "$catalog" --for=condition=Serving --timeout=10m
done
```

Verify expected signature ConfigMap identities and required data from the archived artifact, not a broad managed-resource label:

```bash
yq ea -o=json -I=0 '.' \
  "$DOC1_WORKSPACE/working-dir/cluster-resources/signature-configmap.yaml" \
  > "$PREREQ_RUN_DIR/signature-documents.json"
jq -s '
  [.[] | if .kind == "List" then .items[] else . end | select(.kind == "ConfigMap")] |
  if length > 0 and all(.[];
    (.metadata.name | type == "string" and length > 0) and
    .metadata.namespace == "openshift-config-managed" and
    (((.data // {}) | length) + ((.binaryData // {}) | length) > 0))
  then . else error("missing signature ConfigMap identity or data") end
' "$PREREQ_RUN_DIR/signature-documents.json" > "$PREREQ_RUN_DIR/signature-expected.json"
jq -c '.[]' "$PREREQ_RUN_DIR/signature-expected.json" \
  > "$PREREQ_RUN_DIR/signature-expected.jsonl"
while IFS= read -r expected; do
  name="$(printf '%s\n' "$expected" | jq -er '.metadata.name')"
  namespace="$(printf '%s\n' "$expected" | jq -er '.metadata.namespace')"
  oc get configmap "$name" -n "$namespace" -o json \
    > "$PREREQ_RUN_DIR/signature-live.json"
  jq -e --argjson expected "$expected" '
    . as $live |
    .metadata.name == $expected.metadata.name and
    .metadata.namespace == $expected.metadata.namespace and
    all(($expected.data // {} | to_entries[]);
      . as $kv | $live.data[$kv.key] == $kv.value) and
    all(($expected.binaryData // {} | to_entries[]);
      . as $kv | $live.binaryData[$kv.key] == $kv.value)
  ' "$PREREQ_RUN_DIR/signature-live.json" >/dev/null
  printf 'PASS: signature ConfigMap %s/%s identity and required data.\n' "$namespace" "$name"
done < "$PREREQ_RUN_DIR/signature-expected.jsonl"
signature_configmaps="$(jq -r 'map(.metadata.namespace + "/" + .metadata.name) | join(",")' \
  "$PREREQ_RUN_DIR/signature-expected.json")"
```

A missing/changed expected identity or key stops at Doc 1; extra unrelated live keys are preserved. Do not create a second catalog/signature resource here.

### Verify affected MachineConfigPool convergence

Doc 1 states that applying IDMS/ITMS restarts CRI-O and kubelet across nodes. Check the pools recorded by the handoff. A paused pool is not converged and must be deliberately unpaused or explicitly excluded by the cluster owner before installation.

```bash
oc get machineconfigpool \
  -o custom-columns='NAME:.metadata.name,PAUSED:.spec.paused,GEN:.metadata.generation,OBSERVED:.status.observedGeneration,MACHINES:.status.machineCount,UPDATED:.status.updatedMachineCount,READY:.status.readyMachineCount,UNAVAILABLE:.status.unavailableMachineCount,DEGRADED:.status.degradedMachineCount'

export AFFECTED_POOLS='<worker>'
for pool in $AFFECTED_POOLS; do
  test "$(oc get machineconfigpool "$pool" -o jsonpath='{.spec.paused}')" != true
  oc wait machineconfigpool/"$pool" --for=condition=Updated --timeout=45m
  oc wait machineconfigpool/"$pool" --for=condition=Degraded=False --timeout=5m
  oc get machineconfigpool "$pool" -o json | jq -e '
    (.spec.paused != true) and
    (.status.observedGeneration == .metadata.generation) and
    (.status.updatedMachineCount == .status.machineCount) and
    ((.status.unavailableMachineCount // 0) == 0) and
    ((.status.degradedMachineCount // 0) == 0)'
done
```

Each `jq` command must return `true`. If a check fails, inspect the affected pool and nodes:

```bash
oc describe machineconfigpool <pool-name>
oc get nodes
oc describe node <node-name>
```

Remain in this document for a trust convergence failure introduced by Section 3. Return to Doc 1 for a mirror-policy rollout failure.

### Authenticate to the mirror with read-only credentials

Doc 1 separates the bastion authfile, which has source-read and mirror-write access, from the cluster pull secret, which has mirror-read-only access. Never upload or reuse the bastion authfile here.

```bash
umask 077
if ! test -s "$MIRROR_AUTH_FILE" || ! test -r "$MIRROR_AUTH_FILE"; then
  printf 'ERROR: missing, empty or unreadable mirror authfile.\n' >&2; exit 1
fi
jq -e --arg host "$MIRROR_HOST" \
  '.auths[$host] | type == "object" and length > 0' "$MIRROR_AUTH_FILE" >/dev/null
```

Use the approved persistent read-only authfile directly with exact mirror-image `skopeo inspect --authfile` checks. Missing/expired/denied credentials stop; never substitute an OpenShift token or write-capable bastion authfile. Do not prompt or rewrite the authfile. Keep it through Section 3 and Section 5 exact-image checks. This does not add `helm registry login`.

### Verify representative mirrored images

Use exact references from the Doc 1 consolidated `additionalImages` inventory and operator catalog:

```bash
skopeo inspect --authfile "$MIRROR_AUTH_FILE" \
  'docker://<mirrored-redhat-operator-index-v4.22-by-digest>' >/dev/null
skopeo inspect --authfile "$MIRROR_AUTH_FILE" \
  'docker://<exact-mirrored-osac-operator-from-approved-0.0.24-inventory>' >/dev/null
skopeo inspect --authfile "$MIRROR_AUTH_FILE" \
  'docker://<exact-mirrored-fulfillment-service-from-approved-0.0.24-inventory>' >/dev/null
skopeo inspect --authfile "$MIRROR_AUTH_FILE" \
  'docker://<exact-mirrored-osac-aap-from-approved-0.0.24-inventory>' >/dev/null
```

For VMaaS, also prove node-level tag redirection with an upstream containerdisk reference selected in Doc 1:

```bash
if test "$ENABLE_VMAAS" = true; then
  oc debug node/<node-name> -- chroot /host \
    crictl pull '<upstream-containerdisk-image>:<tag>'
fi
```

Every inspection must succeed. A certificate or authentication error remains in Section 3. A `manifest unknown`, missing blob, absent repository, or failed upstream-reference redirection returns to Doc 1; do not substitute a public image.

### Verify selected package visibility from `redhat-operators`

Core always requires three packages:

```bash
for package in openshift-cert-manager-operator ansible-automation-platform-operator amq-streams; do
  oc get packagemanifest "$package" -n openshift-marketplace -o json \
    | jq -e --arg package "$package" '
        .metadata.name == $package and
        .status.catalogSource == "redhat-operators" and
        .status.catalogSourceNamespace == "openshift-marketplace"'
done
```

Core + VMaaS adds three packages:

```bash
if test "$ENABLE_VMAAS" = true; then
  for package in kubevirt-hyperconverged lvms-operator metallb-operator; do
    oc get packagemanifest "$package" -n openshift-marketplace -o json \
      | jq -e --arg package "$package" '
          .metadata.name == $package and
          .status.catalogSource == "redhat-operators" and
          .status.catalogSourceNamespace == "openshift-marketplace"'
  done
fi
```

Verify required channels and source together:

```bash
oc get packagemanifest openshift-cert-manager-operator -n openshift-marketplace -o json \
  | jq -e '.status.catalogSource == "redhat-operators" and (.status.channels | any(.name == "stable-v1"))'
oc get packagemanifest ansible-automation-platform-operator -n openshift-marketplace -o json \
  | jq -e '.status.catalogSource == "redhat-operators" and (.status.channels | any(.name == "stable-2.6-cluster-scoped"))'
oc get packagemanifest amq-streams -n openshift-marketplace -o json \
  | jq -e '.status.catalogSource == "redhat-operators" and (.status.channels | any(.name == "stable"))'
```

PackageManifest is the OLM Classic view. ClusterCatalog is the OLM v1 view and is verified separately above. If a selected package or channel is absent from `redhat-operators`, stop and return to Doc 1. Do not enable a default online catalog to compensate.

## 2. Catalog Source Configuration

Later Subscription manifests use the exact source identity established by Doc 1. Record it locally; do not create or rename a CatalogSource here.

```yaml
# disconnected-site.yaml
catalogSource: redhat-operators
catalogSourceNamespace: openshift-marketplace
```

```bash
export CATALOG_SOURCE="$(yq -r '.catalogSource' disconnected-site.yaml)"
export CATALOG_NAMESPACE="$(yq -r '.catalogSourceNamespace' disconnected-site.yaml)"

test "$CATALOG_SOURCE" = redhat-operators
test "$CATALOG_NAMESPACE" = openshift-marketplace

oc get catalogsource "$CATALOG_SOURCE" -n "$CATALOG_NAMESPACE" \
  -o jsonpath='state={.status.connectionState.lastObservedState}{" image="}{.spec.image}{"\n"}'
oc get catalogsource "$CATALOG_SOURCE" -n "$CATALOG_NAMESPACE" -o json \
  | jq -e --arg mirror "$MIRROR_HOST" '
      .metadata.name == "redhat-operators" and
      .metadata.namespace == "openshift-marketplace" and
      .status.connectionState.lastObservedState == "READY" and
      (.spec.image | split("/")[0]) == $mirror'
```

> [!WARNING]
> The source name is not site-selectable for this OSAC 0.0.24 installation. Doc 1 uses `targetCatalog: redhat-operators`; use `source: redhat-operators` and `sourceNamespace: openshift-marketplace` in the operator procedures. If the generated source has another name, return to Doc 1 and correct it before first apply; never apply both names.

## 3. DNS, TLS, and Registry Trust Configuration

Configure and verify cluster access to the mirror registry and catalog endpoints without public-network fallback.

### Verify DNS resolution

From the administration host:

```bash
dig +short "${MIRROR_HOST%%:*}"
# If dig is unavailable:
nslookup "${MIRROR_HOST%%:*}"
```

Verify from a cluster workload using an image already present in the disconnected environment:

```bash
oc run dns-check -n default --restart=Never --rm -i \
  --image='<mirrored-origin-cli-4.20.0>' --command -- \
  getent hosts "${MIRROR_HOST%%:*}"
```

Both contexts must return the site-approved address. If the administration host fails, correct site DNS before continuing. If only the workload fails, inspect cluster DNS and network policy in this document. Do not add `/etc/hosts` entries to nodes as a workaround.

### Verify the mirror certificate from every node

Doc 1 requires certificate-validating registry checks without `-k`. Repeat that gate:

```bash
nodes="$(oc get nodes -o name)"
test -n "$nodes"
for node in $nodes; do
  code="$(
    oc debug "$node" -- chroot /host \
      curl -sS -o /dev/null -w '%{http_code}' \
      "https://${MIRROR_HOST}/v2/"
  )" || {
    echo "ERROR: TLS validation or reachability failed on $node" >&2
    exit 1
  }
  case "$code" in
    200|401) printf '%s %s\n' "$node" "$code" ;;
    *) echo "ERROR: unexpected registry response on $node: HTTP $code" >&2; exit 1 ;;
  esac
done
```

Every node must return HTTP `200` or `401`. A TLS, DNS, or reachability failure remains in this section.

### Merge the mirror CA into cluster registry trust

OpenShift uses `image.config.openshift.io/cluster.spec.additionalTrustedCA` for additional registry CAs. Preserve every existing CA key and use a server-enforced `resourceVersion` precondition.

```bash
export MIRROR_CA_KEY="${MIRROR_HOST//:/..}"
if ! test -s "$MIRROR_CA_FILE" || ! test -r "$MIRROR_CA_FILE"; then
  printf 'ERROR: missing, empty or unreadable mirror CA.\n' >&2; exit 1
fi
# Snapshot BEFORE deciding which ConfigMap to merge.
oc get image.config.openshift.io/cluster -o json \
  > "$PREREQ_RUN_DIR/image-config-original.json"
image_rv="$(jq -er '.metadata.resourceVersion | select(length > 0)' \
  "$PREREQ_RUN_DIR/image-config-original.json")"
image_uid="$(jq -er '.metadata.uid | select(length > 0)' \
  "$PREREQ_RUN_DIR/image-config-original.json")"
TRUST_CONFIGMAP="$(jq -r '.spec.additionalTrustedCA.name // ""' \
  "$PREREQ_RUN_DIR/image-config-original.json")"
export TRUST_CONFIGMAP="${TRUST_CONFIGMAP:-registry-config}"
oc get configmap "$TRUST_CONFIGMAP" -n openshift-config --ignore-not-found -o json \
  > "$PREREQ_RUN_DIR/registry-trust-current.json"
if test -s "$PREREQ_RUN_DIR/registry-trust-current.json"; then
  jq --rawfile ca "$MIRROR_CA_FILE" --arg key "$MIRROR_CA_KEY" '
    .data = ((.data // {}) + {($key): $ca}) |
    del(.metadata.managedFields, .metadata.creationTimestamp)
  ' "$PREREQ_RUN_DIR/registry-trust-current.json" \
    > "$PREREQ_RUN_DIR/registry-trust-merged.json"
  oc replace -f "$PREREQ_RUN_DIR/registry-trust-merged.json"
else
  oc create configmap "$TRUST_CONFIGMAP" -n openshift-config \
    --from-file="${MIRROR_CA_KEY}=${MIRROR_CA_FILE}"
fi
image_patch="$(jq -n --arg rv "$image_rv" --arg uid "$image_uid" --arg name "$TRUST_CONFIGMAP" '
  {metadata: {resourceVersion: $rv, uid: $uid},
   spec: {additionalTrustedCA: {name: $name}}}
')"
if oc patch image.config.openshift.io/cluster --type=merge -p "$image_patch"; then
  printf 'PASS: registry CA reference updated from original ImageConfig snapshot.\n'
else
  printf 'ERROR: ImageConfig update failed; stop and re-read both objects.\n' >&2; exit 1
fi
oc get configmap "$TRUST_CONFIGMAP" -n openshift-config -o json \
  | jq -e --arg key "$MIRROR_CA_KEY" --rawfile ca "$MIRROR_CA_FILE" '.data[$key] == $ca'
```

Only NotFound may produce an empty ConfigMap read; API/auth failures stop. ConfigMap replace retains its rv/UID and unrelated data/binaryData. Concurrent create must fail, not overwrite. ConfigMap and ImageConfig updates are separate CAS operations, not a transaction. On conflict re-read the reference and selected ConfigMap, then decide a fresh merge; never refresh only rv while keeping a stale name, and never delete the CA object because the pointer patch failed. Wait for MCP convergence using Section 1.

### Verify and atomically update the cluster pull secret

Doc 1 requires mirror read-only credentials in the cluster pull secret. Never merge the bastion authfile, which contains mirror-write and upstream-read credentials.

```bash
oc get secret pull-secret -n openshift-config -o json \
  > "$PREREQ_RUN_DIR/pull-secret-current.json"
auth_state="$(jq -er --arg host "$MIRROR_HOST" '
  .data[".dockerconfigjson"] | @base64d | fromjson | .auths |
  if type != "object" then error("invalid pull-secret auths")
  elif has($host) then "present" else "absent" end
' "$PREREQ_RUN_DIR/pull-secret-current.json")"
case "$auth_state" in
  present) printf 'Mirror key present; preserving Secret.\n' ;;
  absent)
    jq -e --arg host "$MIRROR_HOST" \
      '.auths[$host] | type == "object" and length > 0' "$MIRROR_AUTH_FILE" >/dev/null
    jq --arg host "$MIRROR_HOST" --slurpfile mirror "$MIRROR_AUTH_FILE" '
      (.data[".dockerconfigjson"] | @base64d | fromjson) as $current |
      ($current * {auths: ($current.auths + {($host): $mirror[0].auths[$host]})}) as $merged |
      .data[".dockerconfigjson"] = ($merged | tojson | @base64) |
      del(.metadata.managedFields, .metadata.creationTimestamp, .metadata.uid)
    ' "$PREREQ_RUN_DIR/pull-secret-current.json" \
      > "$PREREQ_RUN_DIR/pull-secret-merged.json"
    oc replace -f "$PREREQ_RUN_DIR/pull-secret-merged.json"
    ;;
  *) printf 'ERROR: invalid pull-secret check result.\n' >&2; exit 1 ;;
esac
oc get secret pull-secret -n openshift-config -o json \
  | jq -e --arg host "$MIRROR_HOST" '
      .data[".dockerconfigjson"] | @base64d | fromjson | .auths | has($host)'
rm -f "$PREREQ_RUN_DIR/pull-secret-current.json" "$PREREQ_RUN_DIR/pull-secret-merged.json"
```

A missing mirror key is a remediation decision, not an ignored API/JSON failure. On conflict repeat the complete read/decision/merge/replace block. Never suppress failure with `|| true` or print credential data.

### Re-run the Doc 1 registry gate

```bash
oc get catalogsource redhat-operators -n openshift-marketplace \
  -o jsonpath='{.status.connectionState.lastObservedState}{"\n"}' \
  | grep -x READY

skopeo inspect --authfile "$MIRROR_AUTH_FILE" \
  'docker://<mirrored-redhat-operator-index-v4.22-by-digest>' >/dev/null
skopeo inspect --authfile "$MIRROR_AUTH_FILE" \
  'docker://<exact-mirrored-osac-operator-from-approved-0.0.24-inventory>' >/dev/null
```

If DNS, certificate trust, authentication, or MCP convergence fails, remain in this document. If the mirror responds but an image or package is absent, return to Doc 1.

## 4. Production Infrastructure Prerequisites

Select one supported service tier and prove every customer-owned production dependency is ready before `operators.md`.

### Select the service tier

| Prerequisite | Core | Core + VMaaS | Evidence to record |
| --- | ---: | ---: | --- |
| cert-manager Operator | Required | Required | CSV, CRD, webhook, and operand evidence collected in `operators.md` |
| trust-manager `v0.20.0` | Required | Required | Deployment, webhook, Bundle CRD, and `ca-bundle` evidence collected in `operators.md` |
| AAP Operator | Required | Required | Pinned CSV and Manual InstallPlan evidence collected in `operators.md` |
| Kafka | In-cluster operator or supported external service | Same | Broker, TLS, authentication, ACL, and advertised-address evidence |
| OpenShift Virtualization | Not required | Required | CSV and HyperConverged readiness collected in `operators.md` |
| Dynamic storage | Required | Required | Selected StorageClass and test PVC |
| LoadBalancer provider | As required by selected services | Required where VMaaS exposure needs it | Test Service allocation and reachability |

BMaaS installation and host-provisioning procedures are deferred and are not part of this guide.

Record the selection:

```bash
cat > prerequisite-selection.env <<EOF
PROFILE=$PROFILE
ENABLE_VMAAS=$ENABLE_VMAAS
STORAGE_CLASS=<storage-class>
LOADBALANCER_PROVIDER=<existing-or-metallb>
KAFKA_PROVIDER=<external-or-in-cluster>
POSTGRES_HOST=<postgres-host>
VAULT_ADDR=<https://vault.example.com>
OIDC_ISSUER=<https://keycloak.example.com/realms/osac>
EOF
chmod 0600 prerequisite-selection.env
```

Do not put passwords, tokens, private keys, or credential-bearing URLs in this file.

Create a new run-owned namespace; never adopt a pre-existing test namespace. Preserve its returned UID:

```bash
test ! -e "$PREREQ_RUN_DIR/namespace-owned.json"
PREREQ_RUN_ID="${PREREQ_RUN_DIR##*/}"
oc create -f - -o json > "$PREREQ_RUN_DIR/namespace-owned.json" <<EOF
apiVersion: v1
kind: Namespace
metadata:
  generateName: osac-prereq-check-
  labels:
    osac-project.org/prereq-run: "${PREREQ_RUN_ID}"
EOF
PREREQ_NAMESPACE="$(jq -er '.metadata.name | select(length > 0)' "$PREREQ_RUN_DIR/namespace-owned.json")"
PREREQ_NAMESPACE_UID="$(jq -er '.metadata.uid | select(length > 0)' "$PREREQ_RUN_DIR/namespace-owned.json")"
export PREREQ_RUN_ID PREREQ_NAMESPACE PREREQ_NAMESPACE_UID
printf 'Test namespace=%s uid=%s run=%s\n' "$PREREQ_NAMESPACE" "$PREREQ_NAMESPACE_UID" "$PREREQ_RUN_ID"
```

Use only this namespace for this run's tests. Creation errors or incomplete ownership evidence stop; never guess a namespace name for cleanup. Setup refuses a second initialization in the same run directory to preserve the earlier ownership record.

### Verify dynamic storage

Choose either an existing supported dynamic StorageClass or the with-LVMS profile. Do not install LVMS merely because it is available.

```bash
: "${PREREQ_NAMESPACE:?run-owned test namespace is required}"
export STORAGE_CLASS='<storage-class>'
oc get storageclass "$STORAGE_CLASS" >/dev/null
cat > "$PREREQ_RUN_DIR/storage-test-pvc.yaml" <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: osac-prereq-storage-test
  namespace: ${PREREQ_NAMESPACE}
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 1Gi
  storageClassName: ${STORAGE_CLASS}
EOF
oc apply -f "$PREREQ_RUN_DIR/storage-test-pvc.yaml"
oc wait -n "$PREREQ_NAMESPACE" pvc/osac-prereq-storage-test \
  --for=jsonpath='{.status.phase}'=Bound --timeout=5m
oc get pvc osac-prereq-storage-test -n "$PREREQ_NAMESPACE" \
  -o custom-columns='NAME:.metadata.name,CLASS:.spec.storageClassName,PHASE:.status.phase,VOLUME:.spec.volumeName'
```

Success is `PHASE=Bound` with a nonempty volume. If the claim remains `Pending`, inspect events and either correct the existing provider or deliberately select the with-LVMS profile for `operators.md`. Never leave both providers partially configured.

### Verify the LoadBalancer provider

If an existing provider is selected, apply a site-reviewed test Deployment and `type: LoadBalancer` Service using an image from Doc 1, wait for an address, and verify client reachability. If MetalLB is selected but not yet installed, record the approved non-overlapping address pool and defer the live test to `operators.md`.

```bash
# Approved test YAML must omit namespaces or use this exact run namespace.
: "${PREREQ_NAMESPACE:?run-owned test namespace is required}"
oc apply -n "$PREREQ_NAMESPACE" -f loadbalancer-test.yaml
oc rollout status deployment/osac-prereq-lb-test -n "$PREREQ_NAMESPACE" --timeout=5m
oc wait service/osac-prereq-lb-test -n "$PREREQ_NAMESPACE" \
  --for=jsonpath='{.status.loadBalancer.ingress[0]}' --timeout=5m
LB_ADDRESS="$(oc get service osac-prereq-lb-test -n "$PREREQ_NAMESPACE" \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}{.status.loadBalancer.ingress[0].hostname}')"
export LB_ADDRESS
test -n "$LB_ADDRESS"
curl --fail --connect-timeout 10 "http://${LB_ADDRESS}:8080/" >/dev/null
```

A failed allocation remains with the provider owner. An allocated but unreachable address remains with the site network owner. MetalLB installation belongs to `operators.md`; never copy an example range into production.

### Verify PostgreSQL

```bash
if ! test -s /secure/postgres-ca.pem || ! test -r /secure/postgres-ca.pem; then
  printf 'ERROR: missing, empty or unreadable PostgreSQL CA.\n' >&2; exit 1
fi
PGSERVICEFILE=/secure/pg_service.conf \
  psql -X -v ON_ERROR_STOP=1 \
  --dbname='service=osac sslmode=verify-full sslrootcert=/secure/postgres-ca.pem' \
  -c 'select current_database(), current_user, version();' \
  -c 'select ssl from pg_stat_ssl where pid = pg_backend_pid();' \
  -c 'create temporary table osac_prereq_check(id integer); insert into osac_prereq_check values (1); select count(*) from osac_prereq_check;'
```

Provision the trusted CA through the service handoff. The `osac` service supplies the approved DNS hostname/database/user; credentials stay in protected files. Explicit conninfo enforces `verify-full`/root CA even over weaker service-file settings. Require successful hostname/chain verification, `ssl=t` and count `1`. Record only endpoint/database/CA identity, backup owner, restore-test date and Secret names. Any failure blocks `operators.md` with the database owner.

### Verify Vault or OpenBao handoff

Use [Secrets Management Configuration](../secrets-management-configuration.md) as the normative production contract. It owns supported versions, namespaces, Keycloak claims, JWT authentication, policies, roles, credential Secret shape, Helm values, CA bundle, verification, rotation, and troubleshooting. Do not reproduce those procedures here.

Retain only the disconnected evidence gate:

```bash
export VAULT_ADDR='<https://vault.example.com>'
export VAULT_HOST='<vault.example.com>'
export VAULT_PORT='<8200>'
export VAULT_CA_FILE='<absolute-path-to-vault-ca.pem>'
export VAULT_SECRET_NAMESPACE='<osac-namespace>'
export VAULT_SECRET_NAME='<vault-credential-secret>'
export VAULT_CA_CONFIGMAP='<vault-ca-configmap>'

getent hosts "$VAULT_HOST"
if ! test -s "$VAULT_CA_FILE" || ! test -r "$VAULT_CA_FILE"; then
  printf 'ERROR: missing, empty or unreadable Vault CA.\n' >&2; exit 1
fi
if openssl s_client -brief -connect "${VAULT_HOST}:${VAULT_PORT}" \
    -servername "$VAULT_HOST" -verify_hostname "$VAULT_HOST" \
    -verify_return_error -CAfile "$VAULT_CA_FILE" </dev/null \
    > "$PREREQ_RUN_DIR/vault-tls.log" 2>&1; then
  printf 'PASS: Vault DNS-hostname and CA verification.\n'
else
  printf 'ERROR: Vault TLS failed; inspect protected evidence %s/vault-tls.log.\n' "$PREREQ_RUN_DIR" >&2
  exit 1
fi

oc get secret "$VAULT_SECRET_NAME" -n "$VAULT_SECRET_NAMESPACE" -o json   | jq -e '.data | type == "object" and length > 0'
oc get configmap "$VAULT_CA_CONFIGMAP" -n "$VAULT_SECRET_NAMESPACE" -o json   | jq -e '.data | type == "object" and length > 0'
```

Success means disconnected DNS/TLS reachability passes, the referenced Secret and CA ConfigMap exist with nonempty data, and the canonical guide's completion evidence is recorded. Do not print Secret values. A missing role, policy, namespace, Keycloak claim, or service-side configuration returns to the canonical guide and the service owner; a disconnected DNS, CA, or mirror-coverage failure remains in this guide.

Record only the handoff:

```bash
cat > vault-handoff.txt <<EOF
canonical_guide=../secrets-management-configuration.md
evidence_location=<approved-evidence-location>
evidence_timestamp=<rfc3339-timestamp>
endpoint=$VAULT_ADDR
credential_secret=$VAULT_SECRET_NAMESPACE/$VAULT_SECRET_NAME
ca_configmap=$VAULT_SECRET_NAMESPACE/$VAULT_CA_CONFIGMAP
EOF
```

### Verify Kafka handoff

Route this gate according to `KAFKA_PROVIDER` in `prerequisite-selection.env` and use [Kafka Configuration](../kafka-configuration.md) as the normative contract for broker requirements, TLS/SASL, advertised brokers, ACLs, connection resources, Helm values, Strimzi configuration, verification, rotation, and troubleshooting. Do not reproduce those procedures here.

- `external`: the canonical guide must be complete before `operators.md`. Retain only disconnected DNS/TLS reachability and the existence of the referenced credential Secret and CA ConfigMap.
- `in-cluster`: no broker exists yet. Verify Doc 1 package/image coverage, record the intended namespace, broker identity, storage class, and canonical-guide evidence destination, then defer operator, broker, user/ACL, and connection verification to `operators.md`.

Read the recorded selection as data before the case below; never source an unchecked shell file:

```bash
test -r "$PREREQ_RUN_DIR/prerequisite-selection.env"
KAFKA_PROVIDER="$(jq -Rser '
  split("\n") | map(select(startswith("KAFKA_PROVIDER="))) |
  if length != 1 then error("expected exactly one KAFKA_PROVIDER record")
  else .[0] | ltrimstr("KAFKA_PROVIDER=") end
' "$PREREQ_RUN_DIR/prerequisite-selection.env")"
case "$KAFKA_PROVIDER" in
  external|in-cluster) export KAFKA_PROVIDER ;;
  *) printf 'ERROR: recorded KAFKA_PROVIDER must be external or in-cluster.\n' >&2; exit 1 ;;
esac
```

```bash
case "$KAFKA_PROVIDER" in
  external)
    export KAFKA_HOST='<advertised-broker-host>'
    export KAFKA_PORT='<9093>'
    export KAFKA_CA_FILE='<absolute-path-to-kafka-ca.pem>'
    export KAFKA_SECRET_NAMESPACE='<osac-namespace>'
    export KAFKA_SECRET_NAME='<kafka-credential-secret>'
    export KAFKA_CA_CONFIGMAP='<kafka-ca-configmap>'

    getent hosts "$KAFKA_HOST"
    if ! test -s "$KAFKA_CA_FILE" || ! test -r "$KAFKA_CA_FILE"; then
      printf 'ERROR: missing, empty or unreadable Kafka CA.\n' >&2; exit 1
    fi
    if openssl s_client -brief -connect "${KAFKA_HOST}:${KAFKA_PORT}" \
        -servername "$KAFKA_HOST" -verify_hostname "$KAFKA_HOST" \
        -verify_return_error -CAfile "$KAFKA_CA_FILE" </dev/null \
        > "$PREREQ_RUN_DIR/kafka-tls.log" 2>&1; then
      printf 'PASS: external Kafka DNS-hostname and CA verification.\n'
    else
      printf 'ERROR: Kafka TLS failed; inspect protected evidence %s/kafka-tls.log.\n' "$PREREQ_RUN_DIR" >&2
      exit 1
    fi
    oc get secret "$KAFKA_SECRET_NAME" -n "$KAFKA_SECRET_NAMESPACE" -o json       | jq -e '.data | type == "object" and length > 0'
    oc get configmap "$KAFKA_CA_CONFIGMAP" -n "$KAFKA_SECRET_NAMESPACE" -o json       | jq -e '.data | type == "object" and length > 0'

    cat > kafka-handoff.txt <<EOF
provider=external
canonical_guide=../kafka-configuration.md
evidence_location=<approved-evidence-location>
evidence_timestamp=<rfc3339-timestamp>
endpoint=${KAFKA_HOST}:${KAFKA_PORT}
credential_secret=$KAFKA_SECRET_NAMESPACE/$KAFKA_SECRET_NAME
ca_configmap=$KAFKA_SECRET_NAMESPACE/$KAFKA_CA_CONFIGMAP
EOF
    ;;
  in-cluster)
    oc get packagemanifest amq-streams -n openshift-marketplace -o json       | jq -e '.status.catalogSource == "redhat-operators" and (.status.channels | any(.name == "stable"))'
    cat > kafka-handoff.txt <<EOF
provider=in-cluster
canonical_guide=../kafka-configuration.md
evidence_location=<operators-evidence-location>
namespace=osac-kafka
broker_name=<kafka-cluster-name>
storage_class=<storage-class>
verification_owner=operators.md
EOF
    ;;
  *)
    echo "ERROR: KAFKA_PROVIDER must be external or in-cluster" >&2
    exit 1
    ;;
esac
```

Success is a complete `kafka-handoff.txt`. For external Kafka, the canonical guide's connectivity evidence must already exist. For in-cluster Kafka, `operators.md` owns the live broker and canonical connection-contract gate. Do not print Secret values.

### Verify identity

```bash
export OIDC_ISSUER='<https://keycloak.example.com/realms/osac>'
curl --fail --silent --show-error --cacert '<absolute-path-to-idp-ca.pem>' \
  "$OIDC_ISSUER/.well-known/openid-configuration" \
  | tee "$PREREQ_RUN_DIR/osac-oidc-discovery.json" \
  | jq -e --arg issuer "$OIDC_ISSUER" '
      .issuer == $issuer and
      (.authorization_endpoint | length > 0) and
      (.token_endpoint | length > 0) and
      (.jwks_uri | length > 0)'
curl --fail --silent --show-error --cacert '<absolute-path-to-idp-ca.pem>' \
  "$(jq -r '.jwks_uri' "$PREREQ_RUN_DIR/osac-oidc-discovery.json")" \
  | jq -e '.keys | length > 0'
rm -f "$PREREQ_RUN_DIR/osac-oidc-discovery.json"
```

Success is an exact issuer match, nonempty endpoints, and at least one signing key. Verify client authentication and role mapping through the site's approved process.

### Worked selection: Core + VMaaS with existing providers

A customer with enterprise storage, an existing LoadBalancer implementation, external PostgreSQL, external Vault, external Kafka, and production Keycloak selects Core + VMaaS, without LVMS, without MetalLB, and external Kafka. They run the PVC and LoadBalancer tests, verify all external services, and record provider identities and timestamps.

If the PVC remains `Pending`, they stop. They either correct the enterprise provider or deliberately change to with-LVMS and hand storage setup to `operators.md`. They never leave both providers partially configured.

```bash
: "${PREREQ_NAMESPACE:?missing run namespace}"
: "${PREREQ_NAMESPACE_UID:?missing namespace UID}"
: "${PREREQ_RUN_ID:?missing run identity}"
oc get namespace "$PREREQ_NAMESPACE" --ignore-not-found -o json \
  > "$PREREQ_RUN_DIR/namespace-cleanup-current.json"
if test -s "$PREREQ_RUN_DIR/namespace-cleanup-current.json"; then
  jq -e --arg uid "$PREREQ_NAMESPACE_UID" --arg run "$PREREQ_RUN_ID" '
    .metadata.uid == $uid and
    .metadata.labels["osac-project.org/prereq-run"] == $run and
    .metadata.deletionTimestamp == null
  ' "$PREREQ_RUN_DIR/namespace-cleanup-current.json" >/dev/null
  jq '{apiVersion: "v1", kind: "DeleteOptions",
       preconditions: {uid: .metadata.uid, resourceVersion: .metadata.resourceVersion}}' \
    "$PREREQ_RUN_DIR/namespace-cleanup-current.json" \
    > "$PREREQ_RUN_DIR/namespace-delete-options.json"
  oc delete --raw="/api/v1/namespaces/${PREREQ_NAMESPACE}" \
    -f "$PREREQ_RUN_DIR/namespace-delete-options.json"
  oc wait --for=delete "namespace/$PREREQ_NAMESPACE" --timeout=5m
else
  printf 'Run namespace already absent; no deletion sent.\n'
fi
```

## 5. Build the Customer Values File on the Bastion Laptop

Doc 1 identifies the established chart-delivery path: the OSAC chart is published at `oci://ghcr.io/osac-project/charts/osac`, and `oc mirror` v2 does not mirror this OCI chart. A controlled internet-connected workstation or bastion laptop reaches the disconnected cluster API through the approved SSH tunnel or VPN, downloads the chart without registry login, extracts defaults, renders and validates it, and later runs Helm installation.

### Verify the bastion path

```bash
helm version
curl --fail --silent --show-error https://ghcr.io/v2/ >/dev/null
oc cluster-info
oc whoami --show-server
```

All commands must succeed. If public chart access or the approved cluster tunnel fails, correct the bastion path. Do not push the chart into Quay Mirror or substitute a repository checkout.

### Download and verify the released chart

```bash
test "$OSAC_CHART_VERSION" = 0.0.24
mkdir "$PREREQ_RUN_DIR/osac-chart-${OSAC_CHART_VERSION}"
cd "$PREREQ_RUN_DIR/osac-chart-${OSAC_CHART_VERSION}"
helm pull "$OSAC_CHART_REF" --version "$OSAC_CHART_VERSION" \
  --untar --untardir .
test -f osac/Chart.yaml
yq -e '.version == "0.0.24"' osac/Chart.yaml
sha256sum osac/Chart.yaml > chart-evidence.sha256
```

No `helm registry login` is required.

### Extract and edit released defaults

```bash
helm show values ./osac > values-osac.yaml
chmod 0600 values-osac.yaml
```

Set released keys from the downloaded OSAC 0.0.24 chart for namespace/routes, selected services, PostgreSQL, OIDC, AAP, storage, load balancing and Secret references. For Kafka fields, use [Kafka Configuration](../kafka-configuration.md); for Vault/OpenBao fields, use [Secrets Management Configuration](../secrets-management-configuration.md). Do not duplicate their schemas. Preserve released images and validated IDMS/ITMS redirection unless a released key requires a mirror-backed override. Do not invent `global.imageRegistry`, reuse another release's schema or embed credentials.

### Review and override `cliImage`

Review `cliImage` in the downloaded OSAC 0.0.24 defaults and rendered hook Jobs. The `osac/v0.0.24` source default is `quay.io/openshift/origin-cli:4.20.0`, an upstream Origin build. Compare its CLI minor with the target cluster and select the approved Red Hat replacement when required. Verify the packaged default rather than assuming another release's values apply; the mirror-backed override and exact-image checks below remain mandatory.

For a supported customer environment, select the corresponding Red Hat `ose-cli` image for the cluster minor version and ensure that exact image is already mirror-resident. If Doc 1 does not cover it, add it to the authoritative ISC `additionalImages`, rerun the same Doc 1 workspace lineage, apply the regenerated IDMS/ITMS, and repeat the handoff gates before continuing.

Set the released value to the approved mirror-resident reference:

```yaml
cliImage: <mirror-registry>/<repository-path>/ose-cli:v4.22
```

Verify the chosen image before rendering:

```bash
export CLI_IMAGE="$(yq -r '.cliImage' values-osac.yaml)"
test -n "$CLI_IMAGE" && test "$CLI_IMAGE" != null
skopeo inspect --authfile "$MIRROR_AUTH_FILE" \
  "docker://${CLI_IMAGE}" >/dev/null
```

The rendered-image coverage gate below remains the enforcement backstop for `cliImage` and every other chart image.

### Validate and render

```bash
helm lint ./osac --values values-osac.yaml
helm template osac ./osac --namespace "$OSAC_NAMESPACE" \
  --values values-osac.yaml > rendered-osac.yaml
test -s rendered-osac.yaml
```

```bash
if grep -nE '<[^>]+>|TODO|PLACEHOLDER' rendered-osac.yaml; then
  echo 'ERROR: unresolved value found in rendered manifest' >&2
  exit 1
fi
if grep -nEi '(password|token|private[-_ ]?key):[[:space:]]+[^{$]' rendered-osac.yaml; then
  echo 'ERROR: possible embedded credential in rendered manifest' >&2
  exit 1
fi
```

Verify three separate facts per rendered image: exact tag/digest membership in the selected Doc 1 inventory (or its exact mapped destination), the appropriate typed policy to the selected mirror, and retrieval of that exact mirror manifest. Prefix membership alone is not coverage. Direct mirror overrides, including `cliImage`, need inventory provenance and exact availability too.

```bash
verify_doc1_policies
ISC="$DOC1_WORKSPACE/../imageset-config.yaml"
test -f "$ISC"
yq -r '.mirror.additionalImages[]?.name' "$ISC" > "$PREREQ_RUN_DIR/isc-images.txt"
jq -Rse 'split("\n") | map(select(length > 0) | sub("^docker://"; "")) |
  if length > 0 then unique else error("empty exact ISC inventory") end' \
  "$PREREQ_RUN_DIR/isc-images.txt" > "$PREREQ_RUN_DIR/isc-images.json"
cat > "$PREREQ_RUN_DIR/image-candidates.jq" <<'JQ'
def image_ref:
  sub("^docker://"; "") as $id |
  if ($id | test("^[^[:space:]<>]+/[^[:space:]<>]+$")) and
     (($id | test("@sha256:[0-9a-fA-F]{64}$")) or
      (($id | contains("@") | not) and ($id | test(":[^/:]+$"))))
  then ($id | sub("@.*$"; "") | sub(":[^/:]+$"; "")) as $repo |
    {id: $id, repo: $repo, suffix: $id[($repo | length):],
     mode: (if ($id | contains("@")) then "digest" else "tag" end)}
  else error("require a fully qualified explicit tag or SHA-256 digest") end;
def source_matches($repo; $source):
  $repo == $source or ($repo | startswith($source + "/"));
[ $inventory[0][] | image_ref ] as $refs |
[ $refs[] as $ref |
  if ($ref.repo | split("/")[0]) == $mirror then
    {source: $ref.id, destination: $ref.id, mode: $ref.mode, policy: "direct-mirror"}
  else
    [$policies[0][] | select(.mode == $ref.mode and source_matches($ref.repo; .source))] as $matches |
    if ($matches | length) == 0 then empty else
      ($matches | map(.source | length) | max) as $longest |
      [$matches[] | select((.source | length) == $longest)] as $chosen |
      if ($chosen | length) != 1 then error("ambiguous most-specific policy; reconcile Doc 1")
      else $chosen[0] as $policy |
        $policy.mirrors[] | select(split("/")[0] == $mirror) as $destination |
        {source: $ref.id, mode: $ref.mode, policy: $policy.source,
         destination: ($destination + ($ref.repo | ltrimstr($policy.source)) + $ref.suffix)}
      end
    end
  end
]
JQ
jq -n --arg mirror "$MIRROR_HOST" \
  --slurpfile inventory "$PREREQ_RUN_DIR/isc-images.json" \
  --slurpfile policies "$PREREQ_RUN_DIR/verified-image-policies.json" \
  -f "$PREREQ_RUN_DIR/image-candidates.jq" > "$PREREQ_RUN_DIR/image-candidates.json"
yq -r '.. | select(has("image")) | .image' rendered-osac.yaml \
  | sort -u > rendered-images.txt
test -s rendered-images.txt
: > uncovered-images.txt
: > validated-mirror-mappings.partial.jsonl
image_index=0
while IFS= read -r image; do
  image_index=$((image_index + 1))
  image="${image#docker://}"
  jq --arg image "$image" --arg mirror "$MIRROR_HOST" '
    [.[] | select(
      if ($image | split("/")[0]) == $mirror
      then .destination == $image else .source == $image end)]
  ' "$PREREQ_RUN_DIR/image-candidates.json" > "$PREREQ_RUN_DIR/current-candidates.json"
  jq -r '.[].destination' "$PREREQ_RUN_DIR/current-candidates.json" \
    > "$PREREQ_RUN_DIR/current-candidates.txt"
  covered=false
  candidate_index=0
  while IFS= read -r destination; do
    candidate_index=$((candidate_index + 1))
    manifest="$PREREQ_RUN_DIR/image-${image_index}-${candidate_index}.manifest.json"
    diagnostic="$PREREQ_RUN_DIR/image-${image_index}-${candidate_index}.inspect.log"
    if skopeo inspect --authfile "$MIRROR_AUTH_FILE" --raw \
        "docker://${destination}" > "$manifest" 2> "$diagnostic"; then
      observed_digest="$(skopeo manifest-digest "$manifest")"
      case "$destination" in
        *@*)
          expected_digest="${destination##*@}"
          if test "$observed_digest" != "$expected_digest"; then
            printf 'Digest mismatch for %s; expected=%s observed=%s\n' \
              "$destination" "$expected_digest" "$observed_digest" >> "$diagnostic"
            continue
          fi
          ;;
      esac
      checked_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
      jq -c --arg image "$image" --arg destination "$destination" \
        --arg digest "$observed_digest" --arg at "$checked_at" --arg release "$OSAC_CHART_VERSION" '
        map(select(.destination == $destination))[0] |
        {schema_version: 1, release: $release, rendered: $image, inventory_source: .source,
         policy_type: .mode, policy_source: .policy, mirror: $destination,
         observed_digest: $digest, checked_at: $at}
      ' "$PREREQ_RUN_DIR/current-candidates.json" >> validated-mirror-mappings.partial.jsonl
      covered=true
      break
    fi
  done < "$PREREQ_RUN_DIR/current-candidates.txt"
  if test "$covered" != true; then
    printf '%s\n' "$image" >> uncovered-images.txt
    printf 'ERROR: image check=%s ref=%s has no verified available mirror candidate; evidence=%s/image-%s-*.inspect.log\n' \
      "$image_index" "$image" "$PREREQ_RUN_DIR" "$image_index" >&2
  fi
done < rendered-images.txt
if test -s uncovered-images.txt; then
  printf 'ERROR: exact image coverage failed; inspect uncovered-images.txt and protected run evidence.\n' >&2
  exit 1
fi
mv validated-mirror-mappings.partial.jsonl validated-mirror-mappings.jsonl
```

Support explicit tags and SHA-256 digests; unsupported/implicit identities stop. Tags need ITMS and digests need IDMS; both resources stay mandatory. Select the most-specific source, preserve its mirror preference order, and stop on ambiguity. Missing inventory/routing/content goes to Doc 1; auth/CA problems remain in Section 3. Never inspect upstream as fallback or hand-edit evidence. Observed digest is point-in-time evidence, not tag immutability; repeat if inputs/policies/values change before installation.

Do not require server-side dry-run here: operator CRDs and admission webhooks are installed later by `operators.md`. Record evidence:

```bash
sha256sum osac/Chart.yaml values-osac.yaml rendered-osac.yaml rendered-images.txt \
  validated-mirror-mappings.jsonl "$PREREQ_RUN_DIR/isc-images.json" \
  "$PREREQ_RUN_DIR/verified-image-policies.json" > prereqs-artifacts.sha256
```

Do not edit `rendered-osac.yaml`; correct `values-osac.yaml` and render again.

## Completion Record

```bash
completed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
operator="$(oc whoami)"
idms_names="$(oc get imagedigestmirrorset -o name | paste -sd, -)"
itms_names="$(oc get imagetagmirrorset -o name | paste -sd, -)"
cluster_catalog_names="$(oc get clustercatalog -o name | paste -sd, -)"
values_sha256="$(sha256sum values-osac.yaml | awk '{print $1}')"
render_sha256="$(sha256sum rendered-osac.yaml | awk '{print $1}')"
test -n "$operator"
test -n "$idms_names"
test -n "$itms_names"
test -n "$cluster_catalog_names"
: "${signature_configmaps:?expected signature identities were not verified}"
cat > "$PREREQ_RUN_DIR/prereqs-completion.pending" <<EOF
completed_at=$completed_at
operator=$operator
profile=$PROFILE
enable_vmaas=$ENABLE_VMAAS
doc1_isc=$DOC1_WORKSPACE/../imageset-config.yaml
doc1_workspace=$DOC1_WORKSPACE
operatorhub_disable_all_default_sources=true
idms_names=$idms_names
itms_names=$itms_names
catalog_source=openshift-marketplace/redhat-operators
cluster_catalogs=$cluster_catalog_names
signature_configmaps=$signature_configmaps
mirror_host=$MIRROR_HOST
affected_mcps=$AFFECTED_POOLS
storage_class=<storage-class>
loadbalancer_provider=<existing-or-metallb>
postgres_endpoint=<postgres-host-and-port>
vault_endpoint=<vault-or-openbao-url>
vault_credential_secret=<namespace/name>
vault_ca_configmap=<namespace/name>
vault_canonical_evidence=<location-and-rfc3339-timestamp>
kafka_provider=<external-or-in-cluster>
kafka_endpoint=<external-endpoint-or-in-cluster-broker-name>
kafka_credential_secret=<namespace/name-or-pending-operators>
kafka_ca_configmap=<namespace/name-or-pending-operators>
kafka_canonical_evidence=<location-and-rfc3339-timestamp>
oidc_issuer=<oidc-issuer-url>
chart_reference=$OSAC_CHART_REF
chart_version=$OSAC_CHART_VERSION
values_sha256=$values_sha256
render_sha256=$render_sha256
evidence_checksums=prereqs-artifacts.sha256
EOF

for pool in $AFFECTED_POOLS; do
  oc get machineconfigpool "$pool" \
    -o custom-columns='NAME:.metadata.name,PAUSED:.spec.paused,GEN:.metadata.generation,OBSERVED:.status.observedGeneration,UPDATED:.status.updatedMachineCount,UNAVAILABLE:.status.unavailableMachineCount,DEGRADED:.status.degradedMachineCount' \
    --no-headers >> "$PREREQ_RUN_DIR/prereqs-completion.pending"
done
mv "$PREREQ_RUN_DIR/prereqs-completion.pending" prereqs-completion.txt
```

Replace every angle-bracket field before storing the record. Do not record credentials.

- [ ] The archived Doc 1 ISC and workspace lineage are available.
- [ ] Default OperatorHub sources are disabled.
- [ ] Both generated IDMS and ITMS resources are present and reference the mirror.
- [ ] VMaaS containerdisk ITMS entries use `NeverContactSource` when VMaaS is selected.
- [ ] `redhat-operators` is READY in `openshift-marketplace`.
- [ ] Every generated ClusterCatalog is Serving; archived signature ConfigMap identities and required data match live state.
- [ ] Affected MCPs are unpaused, updated, and not degraded or unavailable.
- [ ] Core package manifests, plus VMaaS package manifests when selected, come from `redhat-operators`.
- [ ] DNS, registry trust, read-only mirror authentication, cluster pull credentials, and representative pulls passed.
- [ ] Production storage, load balancing, PostgreSQL, and identity checks passed.
- [ ] Vault/OpenBao canonical-guide evidence and disconnected handoff resources passed.
- [ ] Kafka canonical-guide evidence passed for external Kafka, or the in-cluster handoff is ready for `operators.md`.
- [ ] The bastion laptop verified the public OSAC 0.0.24 chart; values/render, exact inventory, typed routing and mirror availability checks passed.
- [ ] `prereqs-completion.txt` and checksum evidence are stored.
- [ ] Installation may proceed to `operators.md`.
