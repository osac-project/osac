# Category 1C: Mirroring VMaaS Containerdisk Images

## Aim

Mirror VM disk images (containerdisks) required by OSAC's VMaaS (Virtual
Machine as a Service) into a disconnected mirror registry using the
`additionalImages` section of the `oc-mirror` v2 `ImageSetConfiguration`.
Configure ITMS (ImageTagMirrorSet) for containerdisk resolution and make
CDI registry imports work by pointing `DiskImage` references at the
mirror registry with anonymous pull access.

> **Relationship to the main guide**: this document covers VMaaS-specific
> images that extend the base `ImageSetConfiguration` from
> [README.md](./README.md).  The operators
> required for VMaaS (`kubevirt-hyperconverged`, `lvms-operator`,
> `metallb-operator`) are covered in
> [mirroring-operators.md](mirroring-operators.md).

## Prerequisites

| Check | Command / evidence |
| ----- | ------------------ |
| Category 1A complete: `kubevirt-hyperconverged` operator installed | `oc get csv -n openshift-cnv \| grep kubevirt \| grep Succeeded` |
| Category 1B complete: IDMS/ITMS applied, core images mirrored | `oc get idms,itms` |
| `HyperConverged` CR created (Phase 1b) | `oc get hyperconverged -n openshift-cnv` |
| CDI (Containerized Data Importer) is running | `oc get pods -n openshift-cnv -l app=cdi-operator` |
| Mirror registry reachable and trusted from cluster nodes | `oc debug node/<node> -- chroot /host curl -sS https://<registry>:8443/v2/` (no `-k`) — an HTTP 401 response confirms reachability and CA trust; see [Registry TLS trust](#registry-tls-trust) |
| Mirror registry reachable from importer pod network | Run the importer-network diagnostic below; DNS, TLS, and TCP reachability must succeed from the target namespace before provisioning a VM |
| VMaaS is enabled in OSAC Helm values | `global.services.vmaas.enabled: true` in values file |

CDI importer pods pull containerdisks from the pod network, not through
CRI-O. Bastion and node checks do not prove this path. Run a temporary
diagnostic pod in the namespace where CDI creates importer pods:

```bash
IMPORT_NAMESPACE="<target-namespace>"
MIRROR_HOST="<registry-hostname>"
MIRROR_PORT="8443"

oc run mirror-network-check \
  -n "$IMPORT_NAMESPACE" \
  --image=registry.redhat.io/openshift4/ose-cli:v4.20.0 \
  --restart=Never \
  --command -- sleep 600

oc wait pod/mirror-network-check \
  -n "$IMPORT_NAMESPACE" \
  --for=condition=Ready \
  --timeout=2m

oc exec -n "$IMPORT_NAMESPACE" mirror-network-check -- \
  getent hosts "$MIRROR_HOST"

oc exec -n "$IMPORT_NAMESPACE" mirror-network-check -- \
  curl -sS -o /dev/null -w '%{http_code}\n' \
  "https://${MIRROR_HOST}:${MIRROR_PORT}/v2/"

oc delete pod/mirror-network-check -n "$IMPORT_NAMESPACE"
```

Expected: DNS resolves the mirror hostname and the registry returns HTTP
`200` or `401` without `-k`. A timeout, `no route to host`, DNS failure,
or TLS error blocks VMaaS acceptance. A NetworkPolicy or SecurityGroup egress rule alone does not
create the required route.
If the diagnostic pod cannot pull the CLI image before mirror policy is active, use an already mirrored diagnostic image that provides getent and curl; do not fall back to an external image.

## Resources Required

| Resource | Minimum |
| -------- | ------- |
| Additional disk (mirror registry) | Varies by OS image count; typically 2-10 GiB per containerdisk |
| Storage (cluster) | LVMCluster configured via LVMS operator or any other supported storage operator |

## ImageSetConfiguration Section

Containerdisk images are OCI container images that embed a virtual machine
disk (qcow2) as a layer.  They are referenced as `additionalImages` in the
`ImageSetConfiguration` and resolved via ITMS on the cluster.

### Image Reference

The following containerdisk images are required (exact images depend on
the `DiskImage` resources offered in your deployment; version tags are
specified in the ImageSetConfiguration below):

| Image | Registry | Purpose |
| ------ | ---------- | --------- |
| `quay.io/containerdisks/fedora` | quay.io | Fedora VM containerdisk (example) |
| `quay.io/containerdisks/centos-stream` | quay.io | CentOS Stream containerdisk (example) |
| `registry.redhat.io/rhel9/rhel-guest-image` | registry.redhat.io | RHEL 9 guest image (example) |

Example containerdisk entries (extend the base ISC):

```yaml
  additionalImages:
    # --- VMaaS containerdisk images (extend base ISC) ---
    # Add entries for each OS image your deployment requires.
    # The exact images depend on the DiskImage resources offered to tenants.
    #
    # Examples (versions are illustrative — use your actual versions):
    # - name: quay.io/containerdisks/fedora:40
    # - name: quay.io/containerdisks/centos-stream:9
    # - name: registry.redhat.io/rhel9/rhel-guest-image:9.4
```

### How to determine which containerdisks to mirror

OSAC tracks containerdisk references as `DiskImage` resources, not in
`DataVolume` or `DataSource` objects.  Tenant-facing VM templates
(`ComputeInstanceTemplate`) and catalog items reference a `DiskImage`,
and each `DiskImage` carries the containerdisk's OCI artifact URL in its
`spec.source_ref` field (`spec.source_type` is always `registry`).  The
provisioning pipeline copies that URL onto the `ComputeInstance` CR
(`spec.image.sourceRef`) and generates the `DataVolume` from it only at
provisioning time, so the `DiskImage` catalog is the source of truth for
what to mirror.
The CLI accepts `registry` as the source-type input; the stored
resource uses the protobuf enum `SOURCE_TYPE_REGISTRY`, which is
the value used in programmatic jq filters.

1. List the `DiskImage` resources offered in your OSAC deployment:

   ```bash
   osac get diskimages --tenant shared -o json
   ```

   The `DiskImages.List` API (`GET /api/private/v1/disk_images`) returns
   the same data programmatically.

2. Read each image's containerdisk URL from `spec.source_ref`.  For a
   single image, run `osac describe diskimage ID|NAME` and use the
   `Source Ref` line.

3. Add each unique `source_ref` value to the `additionalImages` section
   of your `ImageSetConfiguration`.

## Execution Steps

### Step 1 — Add containerdisk images to the ImageSetConfiguration

Extend the unified `ImageSetConfiguration` with the containerdisk entries
for your deployment:

```yaml
  additionalImages:
    # ... existing dependency and core images from Category 1B ...

    # --- VMaaS containerdisks ---
    - name: <registry>/<repository>/<image>:<tag>
    # Add one entry per DiskImage
```

### Step 2 — Run oc-mirror (connected bastion)

Run the canonical mirror command from
[mirroring-operators.md § Step 1](mirroring-operators.md#step-1--dry-run-connected-bastion)
(dry run first, then full mirror).

### Step 3 — Apply updated ITMS (disconnected cluster)

> **Note**: IDMS/ITMS changes cause CRI-O and kubelet restarts and
> MCP convergence — do not instruct a full node reboot.  Wait for
> MCPs to converge after applying (`oc wait mcp --all
> --for=condition=Updated --timeout=30m`).

Containerdisk images are typically referenced by tag, so they require an
**ImageTagMirrorSet** (ITMS) for resolution:

```bash
oc apply -f \
  oc-mirror-workspace/working-dir/cluster-resources/itms-oc-mirror.yaml
```

Verify the ITMS includes containerdisk entries:

```bash
oc get itms -o yaml | grep -A2 "containerdisk\|rhel-guest"
```

#### Enforce NeverContactSource (recommended)

Build the exact source set from the `DiskImage` resources selected for this
mirror run. Do not infer it from repository names:

```bash
osac get diskimages --tenant shared -o json | \
  jq -r '.[] | select(.spec.source_type == "SOURCE_TYPE_REGISTRY") | .spec.source_ref' | \
  sed -E 's#^docker://##; s#@sha256:.*$##; s#:[^/:]+$##' | \
  sort -u > selected-diskimage-sources.txt

test -s selected-diskimage-sources.txt || {
  echo "ERROR: no selected registry DiskImage sources found" >&2
  exit 1
}

jq -Rn '[inputs] | map(select(length > 0))' \
  < selected-diskimage-sources.txt > selected-diskimage-sources.json

for itms_name in $(oc get itms -o json | \
  jq -r --slurpfile selected selected-diskimage-sources.json \
  '$selected[0] as $sel | [.items[] | select(any(.spec.imageTagMirrors[]?; .source as $src | (.mirrors // []) as $mirs | any($sel[]; . as $s | $s == $src or startswith($src + "/") or any($mirs[]; . as $m | $s == $m or startswith($m + "/")))))] | unique_by(.metadata.name)[] | .metadata.name'); do

  oc get itms "$itms_name" -o json | \
    jq -c --slurpfile selected selected-diskimage-sources.json \
    '$selected[0] as $sel | .spec.imageTagMirrors |= map(.source as $src | (.mirrors // []) as $mirs | if any($sel[]; . as $s | $s == $src or startswith($src + "/") or any($mirs[]; . as $m | $s == $m or startswith($m + "/"))) then . + {mirrorSourcePolicy: "NeverContactSource"} else . end)' | \
    oc apply -f -

done
```

Verify every selected source exists and is fail-closed:

```bash
oc get itms -o json | \
  jq -e --slurpfile selected selected-diskimage-sources.json '
    $selected[0] as $sel |
    [.items[].spec.imageTagMirrors[]] as $entries |
    all($sel[]; . as $img |
      any($entries[];
        (.source as $src | .mirrors[]? as $mir |
         $img == $src or ($img | startswith($src)) or ($img | startswith($mir))) and
        .mirrorSourcePolicy == "NeverContactSource"
      )
    )'
```

### Step 4 — Configure CDI for disconnected registry imports

Two distinct image-pull paths exist for VMaaS:

1. **CRI-O / kubelet pulls** — Pod images (e.g. `virt-launcher`)
   are resolved automatically via ITMS/IDMS.  No extra configuration
   is needed beyond the cluster-wide mirror sets applied in Step 3.

2. **CDI registry imports** — When a `DataVolume` imports a
   containerdisk from a registry, CDI pulls it with its own
   registry client inside the importer pod, which does **not**
   honour ITMS/IDMS.  OSAC creates the `DataVolume` without a
   `pullMethod` and without a registry `secretRef`, and CDI
   leaves `pullMethod` unset, so the importer performs the pull
   itself rather than letting the kubelet pull the containerdisk
   as a pod image.  Neither the OSAC API nor the `osac` CLI
   exposes a way to change this, so the CDI import can be neither
   redirected through ITMS nor authenticated with registry
   credentials.

Work around this by making the CDI import target the mirror
registry directly and anonymously:

- **Public containerdisk repository** — ensure the repository
  holding each mirrored containerdisk is public and allows
  anonymous pulls; the CDI importer performs the registry pull
  itself and is given no credentials.
- **Mirror-URL `DiskImage`** - set `spec.source_ref` to the mirror
  registry image URL when creating the `DiskImage`. The field is
  immutable after creation. The generated `DataVolume` copies the URL
  verbatim, so the import targets the mirror directly instead of relying
  on ITMS rewriting. To replace an existing upstream-backed DiskImage,
  create a new DiskImage with the mirror URL, update tenant-facing
  templates or catalog entries to reference the new DiskImage, verify
  that no active ComputeInstance depends on the old entry, and then
  retire the old DiskImage through the supported OSAC API or CLI. Do not
  patch `spec.source_ref` in place.

The resulting `DataVolume`, as OSAC creates it at provisioning
time. The URL must include the site's actual mirror endpoint because CDI
does not apply ITMS rewriting; the standard endpoint in this guide is
`:8443` (use `:443` only when that is the configured endpoint):

```yaml
apiVersion: cdi.kubevirt.io/v1beta1
kind: DataVolume
metadata:
  name: <compute-instance-name>-root-disk
spec:
  source:
    registry:
      url: "docker://<mirror-registry>:8443/<containerdisk-image>:<tag>"
      # pullMethod and secretRef stay unset: CDI's importer
      # performs the pull itself, anonymously.
  pvc:
    accessModes:
      - ReadWriteOnce
    resources:
      requests:
        storage: <boot-disk-size>
    storageClassName: <storage-tier-class>
```

#### Registry TLS trust

If the mirror registry's certificate is not signed by a CA the
cluster already trusts (a self-signed certificate or an internal
CA), both pull paths must be given that CA:

- **CRI-O / kubelet pulls** trust the registry through the node
  trust store, which OpenShift populates from the cluster proxy
  `trustedCA` setting.
- **CDI registry imports** trust it because the CDI controller
  copies the proxy `trustedCA` bundle into every importer pod.

Distributing the CA through the cluster proxy covers both paths
at once:

```bash
oc create configmap mirror-registry-ca -n openshift-config \
  --from-file=ca-bundle.crt=<mirror-registry-ca>.crt
oc patch proxy cluster --type merge \
  -p '{"spec":{"trustedCA":{"name":"mirror-registry-ca"}}}'
oc wait mcp --all --for=condition=Updated --timeout=30m
```

> **Preserve existing CA bundles**: if the cluster proxy already
> references a `trustedCA` ConfigMap, the patch above replaces that
> reference.  Merge the mirror registry certificate into the
> existing bundle ConfigMap instead, or use these commands only
> when no other custom CA bundle is configured.

Verify from a node — the request must succeed **without** `-k`;
an HTTP `401` response is normal (the registry's anonymous token
challenge), and a TLS error means the CA is not yet distributed:

```bash
oc debug node/<node> -- chroot /host curl -sS \
  https://<mirror-registry>:8443/v2/
```

The `image.config.openshift.io/cluster` `additionalTrustedCA` setting is
a separate mechanism used by the image registry operator, imagestream
imports, builds, and pod image pulls on nodes. It does not configure
CRI-O node pulls or CDI registry imports, so it cannot replace the proxy
`trustedCA` for the two paths covered here.

#### Optional: CDI insecureRegistries (insecure fallback)

As a last resort when the CA cannot be distributed, add the
mirror registry to CDI's `insecureRegistries` to make CDI
importers skip TLS verification for that registry.  The CDI CR is
cluster-scoped (no namespace) and managed by the Hyperconverged
Operator — `cdi-kubevirt-hyperconverged` on OpenShift
Virtualization — so discover its name instead of assuming `cdi`:

```bash
# Only needed when the mirror registry CA is not trusted
# cluster-wide.  This script preserves any existing entries.
MIRROR_REG="<mirror-registry>:8443"
CDI_CR=$(oc get cdi -o jsonpath='{.items[0].metadata.name}')
CURRENT=$(oc get cdi "$CDI_CR" -o json | \
  jq -c '.spec.config.insecureRegistries // []')
UPDATED=$(echo "$CURRENT" | \
  jq --arg r "$MIRROR_REG" '. + [$r] | unique')
oc patch cdi "$CDI_CR" --type merge \
  -p "$(jq -n --argjson regs "$UPDATED" \
  '{"spec":{"config":{"insecureRegistries":$regs}}}')"
```

Prefer the CA distribution above: it is ordinary cluster
configuration, while the CDI CR is owned by the Hyperconverged
Operator on OpenShift Virtualization.

### Step 5 — Verify containerdisk resolution via ITMS

Pull using the **upstream** image reference to confirm that ITMS
redirects the request to the mirror registry:

```bash
# Use the upstream tag reference — CRI-O should resolve it via ITMS
oc debug node/<node-name> -- chroot /host \
  crictl pull <upstream-registry>/<containerdisk-image>:<tag>
```

## Validation

Validation follows the VMaaS tenant journey end to end: the
`DiskImage` catalog must point at the mirror registry, and a VM
provisioned through the OSAC API must reach the `RUNNING` state.
Use the `osac` CLI for every step it covers; the raw OpenShift
objects it hides (ITMS, CDI, DataVolumes) only need checking
when something fails.

### 1. Verify the DiskImage catalog points at the mirror

```bash
# List the DiskImages offered to tenants:
osac get diskimages --tenant shared

# Confirm each image's Source Ref references the mirror
# registry, not the upstream:
osac describe diskimage <name>
```

Every `Source Ref` must be a
`<mirror-registry>/<repo>/<image>:<tag>` reference.  A
`Source Ref` that still points at the upstream registry produces
the classic disconnected failure — CDI ignores ITMS and dials
the upstream directly (see Troubleshooting).

### 2. Provision a fresh VM from a mirrored DiskImage

Before creation, prove that no prior root `DataVolume` or PVC can be reused:

```bash
if oc get datavolume,pvc -A -o name | grep -q '/mirror-validation-root-disk$'; then
  echo "ERROR: stale mirror-validation root disk exists" >&2
  exit 1
fi
```

Create a compute instance from a catalog item, forcing it onto the
`DiskImage` being validated (adjust the remaining flags to what the catalog
item already defaults):

```bash
osac create computeinstance \
  --name mirror-validation \
  --catalog-item <catalog-item> \
  --disk-image <mirror-backed-diskimage> \
  --boot-disk-storage-tier <tier> \
  --network-attachment subnet=<subnet-id> \
  --ssh-key <ssh-secret>
```

Then watch it progress and require the OSAC `ComputeInstance` Ready
condition, not only its summary state:

```bash
osac get computeinstances --watch
osac describe computeinstance mirror-validation
osac get computeinstance mirror-validation -o yaml

CI_NS=$(oc get computeinstance -A \
  -l osac.openshift.io/name=mirror-validation \
  -o jsonpath='{.items[0].metadata.namespace}')
CI_NAME=$(oc get computeinstance -n "$CI_NS" \
  -l osac.openshift.io/name=mirror-validation \
  -o jsonpath='{.items[0].metadata.name}')
oc wait -n "$CI_NS" "computeinstance/$CI_NAME" \
  --for=condition=Ready --timeout=30m
```

Capture the generated root `DataVolume` and PVC identities, require a
successful import and running VMI, and preserve importer logs as evidence:

```bash
DV_NAME=$(oc get datavolume -n "$CI_NS" -o json | \
  jq -r '.items[] | select(.metadata.name | contains("mirror-validation")) | .metadata.name' | head -n1)
test -n "$DV_NAME" || { echo "ERROR: root DataVolume not found" >&2; exit 1; }

oc wait -n "$CI_NS" "datavolume/$DV_NAME" \
  --for=jsonpath='{.status.phase}'=Succeeded --timeout=30m
PVC_NAME=$(oc get datavolume -n "$CI_NS" "$DV_NAME" \
  -o jsonpath='{.status.claimName}')
printf 'ComputeInstance=%s DataVolume=%s PVC=%s\n' \
  "$CI_NAME" "$DV_NAME" "$PVC_NAME"

oc get vmi -n "$CI_NS" -o custom-columns=\
'NAME:.metadata.name,PHASE:.status.phase' | grep mirror-validation

IMPORTER_POD=$(oc get pod -n "$CI_NS" \
  -l cdi.kubevirt.io=importer -o json | \
  jq -r --arg pvc "$PVC_NAME" '.items[] | select(.metadata.name | contains($pvc)) | .metadata.name' | head -n1)
test -n "$IMPORTER_POD" || { echo "ERROR: CDI importer pod not found" >&2; exit 1; }
oc logs -n "$CI_NS" "$IMPORTER_POD" --all-containers \
  > "${CI_NAME}-cdi-importer.log"
if grep -E 'https?://(quay.io|registry.redhat.io|registry.access.redhat.com)' \
  "${CI_NAME}-cdi-importer.log"; then
  echo "ERROR: importer log contains an upstream registry URL" >&2
  exit 1
fi
```

The log check proves only what the importer logged. If egress is not blocked
or independently observed, record no-external-contact as inferred rather than
proven. If the Ready condition, DataVolume, or VMI check fails, document the
disconnected limitation instead of claiming end-to-end VMaaS support.

Delete the instance when done:

```bash
osac delete computeinstance mirror-validation
```

### 3. Cluster-side checks (`oc`)

The `osac` CLI has no view of the OpenShift objects this
document configures; check them directly when validation fails:

```bash
# ITMS includes containerdisk entries
oc get itms -o yaml | grep -B5 -A5 "containerdisk"

# NeverContactSource is set on containerdisk ITMS entries
oc get itms -o json | \
  jq '[.items[].spec.imageTagMirrors[] |
    select(.source | test("containerdisk|rhel-guest")) |
    {source, mirrorSourcePolicy}]'

# CDI operator is healthy
oc get pods -n openshift-cnv -l app.kubernetes.io/managed-by=cdi-operator

# CDI insecureRegistries (only if the insecure fallback is used)
oc get cdi -o custom-columns=\
'NAME:.metadata.name,INSECURE:.spec.config.insecureRegistries'

# DataVolume import status
oc get datavolume -A -o custom-columns=\
'NAME:.metadata.name,PHASE:.status.phase,PROGRESS:.status.progress'

# Node pull via ITMS — use the upstream reference; CRI-O must
# resolve it to the mirror
oc debug node/<node-name> -- chroot /host \
  crictl pull <upstream-registry>/<containerdisk-image>:<tag>

# VirtualMachineInstance status
oc get vmi -A -o custom-columns=\
'NAME:.metadata.name,PHASE:.status.phase,NODE:.status.nodeName'
```

## Troubleshooting

Symptoms are listed as the tenant sees them (`osac`), with the
underlying OpenShift diagnosis behind each.

| Symptom | Likely cause | Resolution |
| ------- | ------------ | ---------- |
| Compute instance stuck in `STARTING`; CDI importer logs `dial tcp <upstream-registry>:443: i/o timeout` | `DiskImage` `source_ref` still points at the upstream registry — CDI's importer ignores ITMS/IDMS and dials the upstream directly | Confirm with `osac describe diskimage <name>`, then point the `DiskImage` at the mirror registry image URL (see Step 4) and re-provision |
| Compute instance stuck in `STARTING`; CDI importer logs `401 Unauthorized` | Mirrored containerdisk repository requires authentication — the CDI importer is given no credentials | Make the repository public and allow anonymous pulls |
| Compute instance stuck in `STARTING` or `FAILED`; CDI importer logs `x509: certificate signed by unknown authority` | Mirror registry CA not trusted by CDI | Distribute the CA via the cluster proxy `trustedCA` (see [Registry TLS trust](#registry-tls-trust)); CDI `insecureRegistries` is the insecure fallback |
| Compute instance `FAILED` after the import | DataVolume import timed out or errored | Inspect `osac get computeinstance <name> -o yaml`, then `oc get datavolume -A` and the CDI importer pod logs |
| `ImagePullBackOff` on virt-launcher pods — instance never starts | KubeVirt operator images (pod images, not containerdisks) not mirrored or missing ITMS entries | Complete the VMaaS operator mirroring in [mirroring-operators.md](mirroring-operators.md) |
| `ErrImagePull: unauthorized` on a node pull from the mirror | Mirror registry credentials missing from the node for private repositories | Verify global pull secret contains registry entry: `oc get secret/pull-secret -n openshift-config -o jsonpath='{.data.\.dockerconfigjson}' \| base64 -d \| jq '.auths \| keys'` |

## Known Gaps / Limitations

1. **`pullMethod` not settable through OSAC** — The CDI `pullMethod`
   setting is per-`DataVolume`, not a global CDI config.  OSAC
   creates the `DataVolume` without it and CDI keeps it unset, so
   the importer pulls the containerdisk itself; neither the OSAC
   API nor the `osac` CLI exposes a way to change it, so CDI
   registry imports always bypass ITMS/IDMS and run
   unauthenticated.  Disconnected VMaaS works around this by
   pointing `DiskImage` `spec.source_ref` at the mirror registry
   URL and allowing anonymous pulls on the containerdisk
   repository (see Step 4).

2. **Containerdisk size variability** — OS disk images vary widely in size
   (500 MiB to 10+ GiB per image).  Plan registry storage accordingly
   based on the number and size of the containerdisks your `DiskImage`
   resources reference.
