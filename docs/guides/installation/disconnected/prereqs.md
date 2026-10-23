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
export PROFILE=core                    # core or core-vmaas
export ENABLE_VMAAS=false              # true or false; must match Doc 1
export CATALOG_SOURCE=redhat-operators
export CATALOG_NAMESPACE=openshift-marketplace
export MIRROR_HOST='<mirror.example.com:8443>'
export MIRROR_CA_FILE='<absolute-path-to-mirror-ca.pem>'
export MIRROR_AUTH_FILE='/secure/mirror-readonly-auth.json'
export DOC1_WORKSPACE='<absolute-path-to-oc-mirror-workspace>'
export OSAC_CHART_REF='oci://ghcr.io/osac-project/charts/osac'
export OSAC_CHART_VERSION='0.0.28'
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
oc get imagedigestmirrorset -o yaml > /tmp/live-idms.yaml
oc get imagetagmirrorset -o yaml > /tmp/live-itms.yaml

grep -F "$MIRROR_HOST" /tmp/live-idms.yaml
grep -F "$MIRROR_HOST" /tmp/live-itms.yaml
rm -f /tmp/live-idms.yaml /tmp/live-itms.yaml
```

If either resource type is absent or does not reference the selected mirror, stop and return to Doc 1. Do not create or modify IDMS or ITMS in this guide.

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
    | jq -e --slurpfile selected selected-diskimage-sources.json '
        [.items[].spec.imageTagMirrors[]] as $entries |
        all($selected[0][]; . as $image |
          any($entries[];
            (.source as $source | .mirrors[]? as $mirror |
              ($image == $source or ($image | startswith($source)) or ($image | startswith($mirror))) and
              .mirrorSourcePolicy == "NeverContactSource")))'
fi
```

The command must return `true`. If the selected source file is missing or any entry lacks `NeverContactSource`, return to the VMaaS mirroring procedure in Doc 1.

### Verify CatalogSource, ClusterCatalog, and signature ConfigMap

Doc 1 sets `targetCatalog: redhat-operators`; later Subscriptions use that exact source name in `openshift-marketplace`.

```bash
oc get catalogsource redhat-operators -n openshift-marketplace \
  -o jsonpath='name={.metadata.name}{" state="}{.status.connectionState.lastObservedState}{" image="}{.spec.image}{"\n"}'
oc get catalogsource redhat-operators -n openshift-marketplace -o json \
  | jq -e --arg mirror "${MIRROR_HOST%%:*}" '
      .metadata.name == "redhat-operators" and
      .metadata.namespace == "openshift-marketplace" and
      .status.connectionState.lastObservedState == "READY" and
      (.spec.image | contains($mirror))'
```

The first command must show `name=redhat-operators state=READY`; the second must return `true`.

Verify every generated ClusterCatalog is serving:

```bash
mapfile -t cluster_catalogs < <(oc get clustercatalog -o name)
test "${#cluster_catalogs[@]}" -gt 0
for catalog in "${cluster_catalogs[@]}"; do
  oc wait "$catalog" --for=condition=Serving --timeout=10m
done
```

Verify the managed signature ConfigMap gate used by Doc 1:

```bash
signature_configmaps="$(
  oc get configmap -n openshift-config-managed \
    -l operator.openshift.io/managed=true -o name 2>/dev/null
)"
test -n "$signature_configmaps"
printf '%s\n' "$signature_configmaps"
```

If the CatalogSource is absent/not READY, a ClusterCatalog is not Serving, or no managed signature ConfigMap exists, stop and return to Doc 1. Do not create a second catalog or signature resource.

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
test -r "$MIRROR_AUTH_FILE"
jq -e --arg host "$MIRROR_HOST" '.auths | has($host)' "$MIRROR_AUTH_FILE"
skopeo login --authfile "$MIRROR_AUTH_FILE" "$MIRROR_HOST"
```

All commands must succeed. Keep this protected read-only auth file available through Section 3.

### Verify representative mirrored images

Use exact references from the Doc 1 consolidated `additionalImages` inventory and operator catalog:

```bash
skopeo inspect --authfile "$MIRROR_AUTH_FILE" \
  'docker://<mirrored-redhat-operator-index-v4.22-by-digest>' >/dev/null
skopeo inspect --authfile "$MIRROR_AUTH_FILE" \
  'docker://<mirrored-osac-operator-0.0.25>' >/dev/null
skopeo inspect --authfile "$MIRROR_AUTH_FILE" \
  'docker://<mirrored-fulfillment-service-0.0.118>' >/dev/null
skopeo inspect --authfile "$MIRROR_AUTH_FILE" \
  'docker://<mirrored-osac-aap-0.0.26>' >/dev/null
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
  | jq -e --arg mirror "${MIRROR_HOST%%:*}" '
      .status.connectionState.lastObservedState == "READY" and
      (.spec.image | contains($mirror))'
```

> [!WARNING]
> The source name is not site-selectable for OSAC 0.0.28. Doc 1 uses `targetCatalog: redhat-operators`, and the released Subscription manifests use `source: redhat-operators` with `sourceNamespace: openshift-marketplace`. If the generated source has another name, return to Doc 1 and correct it before first apply; never apply both names.

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
for node in $(oc get nodes -o name); do
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
export TRUST_CONFIGMAP="$(oc get image.config.openshift.io/cluster \
  -o jsonpath='{.spec.additionalTrustedCA.name}')"
export TRUST_CONFIGMAP="${TRUST_CONFIGMAP:-registry-config}"

if oc get configmap "$TRUST_CONFIGMAP" -n openshift-config >/dev/null 2>&1; then
  oc get configmap "$TRUST_CONFIGMAP" -n openshift-config -o json \
    > /tmp/registry-trust-current.json
  jq --rawfile ca "$MIRROR_CA_FILE" --arg key "$MIRROR_CA_KEY" '
      .data = ((.data // {}) + {($key): $ca}) |
      del(.metadata.managedFields, .metadata.creationTimestamp, .metadata.uid)
    ' /tmp/registry-trust-current.json > /tmp/registry-trust-merged.json
  oc replace -f /tmp/registry-trust-merged.json
else
  oc create configmap "$TRUST_CONFIGMAP" -n openshift-config \
    --from-file="${MIRROR_CA_KEY}=${MIRROR_CA_FILE}"
fi

oc patch image.config.openshift.io/cluster --type=merge \
  -p "{\"spec\":{\"additionalTrustedCA\":{\"name\":\"${TRUST_CONFIGMAP}\"}}}"

oc get configmap "$TRUST_CONFIGMAP" -n openshift-config -o json \
  | jq -e --arg key "$MIRROR_CA_KEY" '.data[$key] | contains("BEGIN CERTIFICATE")'
rm -f /tmp/registry-trust-current.json /tmp/registry-trust-merged.json
```

`oc replace` submits the original `metadata.resourceVersion`; the API rejects a concurrent stale write. On conflict, re-read, merge, and retry. Wait for the affected MCPs to converge using Section 1.

### Verify and atomically update the cluster pull secret

Doc 1 requires mirror read-only credentials in the cluster pull secret. Never merge the bastion authfile, which contains mirror-write and upstream-read credentials.

```bash
oc get secret pull-secret -n openshift-config -o json \
  | jq -e --arg host "$MIRROR_HOST" '
      .data[".dockerconfigjson"] | @base64d | fromjson | .auths | has($host)'
```

If the command returns `true`, do not rewrite the Secret. If it returns `false`, merge the approved read-only auth and submit the original `resourceVersion`:

```bash
umask 077
oc get secret pull-secret -n openshift-config -o json \
  > /tmp/pull-secret-current.json

jq --slurpfile mirror "$MIRROR_AUTH_FILE" '
    (.data[".dockerconfigjson"] | @base64d | fromjson) as $current |
    ($current * {auths: ($current.auths + $mirror[0].auths)}) as $merged |
    .data[".dockerconfigjson"] = ($merged | tojson | @base64) |
    del(.metadata.managedFields, .metadata.creationTimestamp, .metadata.uid)
  ' /tmp/pull-secret-current.json > /tmp/pull-secret-merged.json

oc replace -f /tmp/pull-secret-merged.json
rm -f /tmp/pull-secret-current.json /tmp/pull-secret-merged.json
```

On conflict, re-read and merge again. Verify the mirror key without printing credentials:

```bash
oc get secret pull-secret -n openshift-config -o json \
  | jq -e --arg host "$MIRROR_HOST" '
      .data[".dockerconfigjson"] | @base64d | fromjson | .auths | has($host)'
```

### Re-run the Doc 1 registry gate

```bash
oc get catalogsource redhat-operators -n openshift-marketplace \
  -o jsonpath='{.status.connectionState.lastObservedState}{"\n"}' \
  | grep -x READY

skopeo inspect --authfile "$MIRROR_AUTH_FILE" \
  'docker://<mirrored-redhat-operator-index-v4.22-by-digest>' >/dev/null
skopeo inspect --authfile "$MIRROR_AUTH_FILE" \
  'docker://<mirrored-osac-operator-0.0.25>' >/dev/null
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

### Verify dynamic storage

Choose either an existing supported dynamic StorageClass or the with-LVMS profile. Do not install LVMS merely because it is available.

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: osac-prereq-storage-test
  namespace: osac-prereq-check
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 1Gi
  storageClassName: <storage-class>
```

```bash
oc create namespace osac-prereq-check --dry-run=client -o yaml | oc apply -f -
oc apply -f storage-test-pvc.yaml
oc wait -n osac-prereq-check pvc/osac-prereq-storage-test \
  --for=jsonpath='{.status.phase}'=Bound --timeout=5m
oc get pvc osac-prereq-storage-test -n osac-prereq-check \
  -o custom-columns='NAME:.metadata.name,CLASS:.spec.storageClassName,PHASE:.status.phase,VOLUME:.spec.volumeName'
```

Success is `PHASE=Bound` with a nonempty volume. If the claim remains `Pending`, inspect events and either correct the existing provider or deliberately select the with-LVMS profile for `operators.md`. Never leave both providers partially configured.

### Verify the LoadBalancer provider

If an existing provider is selected, apply a site-reviewed test Deployment and `type: LoadBalancer` Service using an image from Doc 1, wait for an address, and verify client reachability. If MetalLB is selected but not yet installed, record the approved non-overlapping address pool and defer the live test to `operators.md`.

```bash
oc apply -f loadbalancer-test.yaml
oc rollout status deployment/osac-prereq-lb-test -n osac-prereq-check --timeout=5m
oc wait service/osac-prereq-lb-test -n osac-prereq-check \
  --for=jsonpath='{.status.loadBalancer.ingress[0]}' --timeout=5m
export LB_ADDRESS="$(oc get service osac-prereq-lb-test -n osac-prereq-check \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}{.status.loadBalancer.ingress[0].hostname}')"
test -n "$LB_ADDRESS"
curl --fail --connect-timeout 10 "http://${LB_ADDRESS}:8080/" >/dev/null
```

A failed allocation remains with the provider owner. An allocated but unreachable address remains with the site network owner. MetalLB installation belongs to `operators.md`; never copy an example range into production.

### Verify PostgreSQL

```bash
PGSERVICEFILE=/secure/pg_service.conf PGSERVICE=osac \
  psql -X -v ON_ERROR_STOP=1 \
  -c 'select current_database(), current_user, version();' \
  -c 'create temporary table osac_prereq_check(id integer); insert into osac_prereq_check values (1); select count(*) from osac_prereq_check;'
```

Success is an authenticated TLS session and a count of `1`. Record endpoint, database, CA identity, backup owner, restore-test date, and Secret names—not secret values. A failure remains with the database owner and blocks `operators.md`.

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
openssl s_client -connect "${VAULT_HOST}:${VAULT_PORT}"   -servername "$VAULT_HOST" -CAfile "$VAULT_CA_FILE" </dev/null 2>/dev/null   | grep -q 'Verify return code: 0 (ok)'

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
    openssl s_client -connect "${KAFKA_HOST}:${KAFKA_PORT}"       -servername "$KAFKA_HOST" -CAfile "$KAFKA_CA_FILE" </dev/null 2>/dev/null       | grep -q 'Verify return code: 0 (ok)'
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
  | tee /tmp/osac-oidc-discovery.json \
  | jq -e --arg issuer "$OIDC_ISSUER" '
      .issuer == $issuer and
      (.authorization_endpoint | length > 0) and
      (.token_endpoint | length > 0) and
      (.jwks_uri | length > 0)'
curl --fail --silent --show-error --cacert '<absolute-path-to-idp-ca.pem>' \
  "$(jq -r '.jwks_uri' /tmp/osac-oidc-discovery.json)" \
  | jq -e '.keys | length > 0'
rm -f /tmp/osac-oidc-discovery.json
```

Success is an exact issuer match, nonempty endpoints, and at least one signing key. Verify client authentication and role mapping through the site's approved process.

### Worked selection: Core + VMaaS with existing providers

A customer with enterprise storage, an existing LoadBalancer implementation, external PostgreSQL, external Vault, external Kafka, and production Keycloak selects Core + VMaaS, without LVMS, without MetalLB, and external Kafka. They run the PVC and LoadBalancer tests, verify all external services, and record provider identities and timestamps.

If the PVC remains `Pending`, they stop. They either correct the enterprise provider or deliberately change to with-LVMS and hand storage setup to `operators.md`. They never leave both providers partially configured.

```bash
oc delete namespace osac-prereq-check --wait=true
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
mkdir -p osac-chart-0.0.28
cd osac-chart-0.0.28
helm pull "$OSAC_CHART_REF" --version "$OSAC_CHART_VERSION" \
  --untar --untardir .
test -f osac/Chart.yaml
yq -e '.version == "0.0.28"' osac/Chart.yaml
sha256sum osac/Chart.yaml > chart-evidence.sha256
```

No `helm registry login` is required.

### Extract and edit released defaults

```bash
helm show values ./osac > values-osac.yaml
chmod 0600 values-osac.yaml
```

Set released keys for namespace/routes, selected services, PostgreSQL, OIDC, AAP, storage, load balancing, and Secret references. For Kafka fields, use [Kafka Configuration](../kafka-configuration.md) as the normative values contract. For Vault/OpenBao fields, use [Secrets Management Configuration](../secrets-management-configuration.md). Do not copy their connection schemas into this guide. Preserve released image references and rely on the validated IDMS/ITMS redirection unless a released value explicitly requires a mirror-backed override. Do not add `global.imageRegistry`; OSAC 0.0.28 does not define it. Do not embed credentials.

### Review and override `cliImage`

OSAC 0.0.28 defaults `cliImage` to `quay.io/openshift/origin-cli:4.20.0`. The chart uses this image in pre-install and post-install hook Jobs. Review it explicitly because the default is an upstream Origin build and its 4.20 client version does not match the supported OpenShift 4.22.6 server.

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

Extract rendered images and compare them with the exact Doc 1 consolidated `additionalImages` inventory and generated IDMS/ITMS mappings.

First create a reproducible, grep-compatible coverage file. It contains upstream source prefixes and exact ISC image names with digests/tags removed. IDMS/ITMS source entries prove cluster redirection; ISC `additionalImages` entries prove the selected workload inventory. Mirror destinations are not used as coverage keys because rendered manifests normally retain upstream names.

```bash
ISC="$DOC1_WORKSPACE/../imageset-config.yaml"
test -f "$ISC"

{
  # Exact additionalImages names from the archived ISC, normalized by removing
  # transport, digest, and tag. The final sed removes only a tag after the last
  # slash, preserving registry ports.
  yq -r '.mirror.additionalImages[]?.name' "$ISC" \
    | sed -E 's#^docker://##; s#@sha256:[0-9a-fA-F]+$##; s#(:[^/:]+)$##'

  # Upstream source prefixes from live digest and tag mirror policies.
  oc get imagedigestmirrorset -o json \
    | jq -r '.items[].spec.imageDigestMirrors[].source'
  oc get imagetagmirrorset -o json \
    | jq -r '.items[].spec.imageTagMirrors[].source'
} | sed '/^$/d' | sort -u > validated-mirror-mappings.txt

test -s validated-mirror-mappings.txt
```

Then extract the rendered images:

```bash
yq -r '.. | select(has("image")) | .image' rendered-osac.yaml \
  | sort -u > rendered-images.txt

: > uncovered-images.txt
while IFS= read -r image; do
  normalized="$(printf '%s\n' "$image" \
    | sed -E 's#^docker://##; s#@sha256:[0-9a-fA-F]+$##; s#(:[^/:]+)$##')"
  if ! awk -v image="$normalized" '
      image == $0 || index(image, $0 "/") == 1 { found=1 }
      END { exit(found ? 0 : 1) }
    ' validated-mirror-mappings.txt; then
    echo "$image" >> uncovered-images.txt
  fi
done < rendered-images.txt

if test -s uncovered-images.txt; then
  echo 'ERROR: rendered images without Doc 1 mirror coverage:' >&2
  cat uncovered-images.txt >&2
  exit 1
fi
```

Upstream image names are expected when mirror redirection covers them. A missing mapping returns to Doc 1; update the authoritative ISC and workspace lineage rather than editing this evidence file by hand.

Do not require server-side dry-run here: operator CRDs and admission webhooks are installed later by `operators.md`. Record evidence:

```bash
sha256sum osac/Chart.yaml values-osac.yaml rendered-osac.yaml rendered-images.txt \
  > prereqs-artifacts.sha256
```

Do not edit `rendered-osac.yaml`; correct `values-osac.yaml` and render again.

## Completion Record

```bash
cat > prereqs-completion.txt <<EOF
completed_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
operator=$(oc whoami)
profile=$PROFILE
enable_vmaas=$ENABLE_VMAAS
doc1_isc=$DOC1_WORKSPACE/../imageset-config.yaml
doc1_workspace=$DOC1_WORKSPACE
operatorhub_disable_all_default_sources=true
idms_names=$(oc get imagedigestmirrorset -o name | paste -sd, -)
itms_names=$(oc get imagetagmirrorset -o name | paste -sd, -)
catalog_source=openshift-marketplace/redhat-operators
cluster_catalogs=$(oc get clustercatalog -o name | paste -sd, -)
signature_configmaps=$(oc get configmap -n openshift-config-managed -l operator.openshift.io/managed=true -o name | paste -sd, -)
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
values_sha256=$(sha256sum values-osac.yaml | awk '{print $1}')
render_sha256=$(sha256sum rendered-osac.yaml | awk '{print $1}')
evidence_checksums=prereqs-artifacts.sha256
EOF

for pool in $AFFECTED_POOLS; do
  oc get machineconfigpool "$pool" \
    -o custom-columns='NAME:.metadata.name,PAUSED:.spec.paused,GEN:.metadata.generation,OBSERVED:.status.observedGeneration,UPDATED:.status.updatedMachineCount,UNAVAILABLE:.status.unavailableMachineCount,DEGRADED:.status.degradedMachineCount' \
    --no-headers >> prereqs-completion.txt
done
```

Replace every angle-bracket field before storing the record. Do not record credentials.

- [ ] The archived Doc 1 ISC and workspace lineage are available.
- [ ] Default OperatorHub sources are disabled.
- [ ] Both generated IDMS and ITMS resources are present and reference the mirror.
- [ ] VMaaS containerdisk ITMS entries use `NeverContactSource` when VMaaS is selected.
- [ ] `redhat-operators` is READY in `openshift-marketplace`.
- [ ] Every generated ClusterCatalog is Serving and a managed signature ConfigMap exists.
- [ ] Affected MCPs are unpaused, updated, and not degraded or unavailable.
- [ ] Core package manifests, plus VMaaS package manifests when selected, come from `redhat-operators`.
- [ ] DNS, registry trust, read-only mirror authentication, cluster pull credentials, and representative pulls passed.
- [ ] Production storage, load balancing, PostgreSQL, and identity checks passed.
- [ ] Vault/OpenBao canonical-guide evidence and disconnected handoff resources passed.
- [ ] Kafka canonical-guide evidence passed for external Kafka, or the in-cluster handoff is ready for `operators.md`.
- [ ] The bastion laptop downloaded the public OSAC 0.0.28 chart, and values/render passed static and Doc 1 mirror-coverage review.
- [ ] `prereqs-completion.txt` and checksum evidence are stored.
- [ ] Installation may proceed to `operators.md`.
