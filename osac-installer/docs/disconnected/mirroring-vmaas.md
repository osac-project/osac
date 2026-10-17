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
> [disconnected-install.md](../disconnected-install.md).  The operators
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
| Mirror registry reachable from importer pod network | CDI importer pods pull containerdisks from their own network, not through CRI-O; verify DNS resolution and an L3 route or gateway from the importer pod network to the mirror registry — a NetworkPolicy or SecurityGroup egress rule alone does not provide a route |
| VMaaS is enabled in OSAC Helm values | `vmaas.enabled: true` in values file |

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

```bash
oc-mirror --v2 --config=imageset-config.yaml --workspace \
  file://$(pwd)/oc-mirror-workspace docker://<mirror-registry>:8443/
```

### Step 3 — Apply updated ITMS (disconnected cluster)

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

By default `oc-mirror` v2 generates ITMS entries without a
`mirrorSourcePolicy`, which allows CRI-O to fall back to the
upstream registry.  In a strictly disconnected environment, set
`NeverContactSource` on every containerdisk ITMS source entry
to guarantee no upstream contact is attempted:

```bash
# Patch every generated ITMS resource whose sources match the
# containerdisk images.  oc-mirror v2 may generate ITMS
# resources with names like itms-generic-0; discover them
# dynamically rather than assuming a fixed name.
for itms_name in $(oc get itms -o json | \
  jq -r '[.items[] |
    select(.spec.imageTagMirrors[]? |
      .source | test("containerdisk|rhel-guest"))] |
    unique_by(.metadata.name)[] | .metadata.name'); do
  oc get itms "$itms_name" -o json | \
    jq '(.spec.imageTagMirrors[] |
      select(.source | test("containerdisk|rhel-guest")))
      .mirrorSourcePolicy = "NeverContactSource"' | \
    oc apply -f -
done
```

Verify the policy is set:

```bash
oc get itms -o json | \
  jq '[.items[].spec.imageTagMirrors[] |
    select(.source | test("containerdisk|rhel-guest")) |
    {source, mirrorSourcePolicy}]'
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
- **Mirror-URL `DiskImage`** — point each OSAC `DiskImage`'s
  `spec.source_ref` at the mirror registry's image URL; the
  `DataVolume` registry URL is copied verbatim from the
  `DiskImage`, so the import targets the mirror directly instead
  of relying on ITMS rewriting.

The resulting `DataVolume`, as OSAC creates it at provisioning
time:

```yaml
apiVersion: cdi.kubevirt.io/v1beta1
kind: DataVolume
metadata:
  name: <compute-instance-name>-root-disk
spec:
  source:
    registry:
      url: "docker://<mirror-registry>/<containerdisk-image>:<tag>"
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

The `image.config.openshift.io/cluster` `additionalTrustedCA`
setting is a separate mechanism that serves the image registry
operator, imagestream imports, and builds; it does not reach the
nodes, so it cannot replace the proxy `trustedCA` here.

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

### 2. Provision a VM from a mirrored DiskImage

Create a compute instance from a catalog item, forcing it onto
the `DiskImage` being validated (adjust the remaining flags to
what the catalog item already defaults):

```bash
osac create computeinstance \
  --name mirror-validation \
  --catalog-item <catalog-item> \
  --disk-image <mirror-backed-diskimage> \
  --boot-disk-storage-tier <tier> \
  --network-attachment subnet=<subnet-id> \
  --ssh-key <ssh-secret>
```

Then watch it progress:

```bash
# Live view: the instance passes through STARTING (the
# containerdisk import) to RUNNING:
osac get computeinstances --watch

# Summary view:
osac describe computeinstance mirror-validation

# Full detail, including state and internal IP address:
osac get computeinstance mirror-validation -o yaml
```

`RUNNING` with a populated `status.internal_ip_address` is the
first acceptance signal.  Before treating the category as
validated, also confirm the full import chain with no reused
PVC or DataVolume and no manual modifications:

```bash
# Verify the root DataVolume reached Succeeded (CDI import
# completed from the mirror without an external registry
# attempt):
oc get datavolume -A -o custom-columns=\
'NAME:.metadata.name,PHASE:.status.phase' | \
  grep mirror-validation

# Verify the backing VMI is Running:
oc get vmi -A -o custom-columns=\
'NAME:.metadata.name,PHASE:.status.phase' | \
  grep mirror-validation
```

If the DataVolume does not reach `Succeeded` or the VMI does
not reach `Running`, the CDI import path has a disconnected
limitation — document it rather than claiming end-to-end
VMaaS support.

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
