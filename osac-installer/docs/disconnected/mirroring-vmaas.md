# Category 1C: Mirroring VMaaS Containerdisk Images

## Aim

Mirror VM disk images (containerdisks) required by OSAC's VMaaS (Virtual
Machine as a Service) into a disconnected mirror registry using the
`additionalImages` section of the `oc-mirror` v2 `ImageSetConfiguration`.
Configure ITMS (ImageTagMirrorSet) for containerdisk resolution and apply
the CDI `pullMethod` workaround for node-level pulls.

> **Relationship to the main guide**: this document covers VMaaS-specific
> images that extend the base `ImageSetConfiguration` from
> [disconnected-install.md](../disconnected-install.md).  The operators
> required for VMaaS (`kubevirt-hyperconverged`, `lvms-operator`,
> `metallb-operator`) are covered in
> [mirroring-operators.md](mirroring-operators.md).
>
> **Note**: MaaS (networking / Multus-based services) is **NOT** supported
> in disconnected mode.

## Prerequisites (TDD: define checks first)

| Check | Command / evidence |
| ----- | ------------------ |
| Category 1A complete: `kubevirt-hyperconverged` operator installed | `oc get csv -n openshift-cnv \| grep kubevirt \| grep Succeeded` |
| Category 1B complete: IDMS/ITMS applied, core images mirrored | `oc get idms,itms` |
| `HyperConverged` CR created (Phase 1b) | `oc get hyperconverged -n openshift-cnv` |
| CDI (Containerized Data Importer) is running | `oc get pods -n openshift-cnv -l app=cdi-operator` |
| Mirror registry reachable from cluster nodes | `oc debug node/<node> -- chroot /host curl -sSf https://<registry>:8443/v2/ -k` |
| VMaaS is enabled in OSAC Helm values | `vmaas.enabled: true` in values file |

## Resources Required

| Resource | Minimum |
| -------- | ------- |
| Additional disk (mirror registry) | Varies by OS image count; typically 2-10 GiB per containerdisk |
| Storage (cluster) | LVMCluster configured via LVMS operator |

## ImageSetConfiguration Section

Containerdisk images are OCI container images that embed a virtual machine
disk (qcow2) as a layer.  They are referenced as `additionalImages` in the
`ImageSetConfiguration` and resolved via ITMS on the cluster.

> **OSAC-5188**: Containerdisk images are not yet included in the OSAC
> Tier-1 mirror list.  The entries below must be manually added to the
> `ImageSetConfiguration` based on the OS images your deployment requires.

### Image Reference

The following containerdisk images are required (exact images depend on
your VM templates; version tags are specified in the ImageSetConfiguration
below):

| Image | Registry | Purpose |
|-------|----------|---------|
| `quay.io/containerdisks/fedora` | quay.io | Fedora VM containerdisk (example) |
| `quay.io/containerdisks/centos-stream` | quay.io | CentOS Stream containerdisk (example) |
| `registry.redhat.io/rhel9/rhel-guest-image` | registry.redhat.io | RHEL 9 guest image (example) |

Example containerdisk entries (extend the base ISC):

```yaml
  additionalImages:
    # --- VMaaS containerdisk images (extend base ISC) ---
    # Add entries for each OS image your deployment requires.
    # The exact images depend on which VM templates are offered to tenants.
    #
    # Examples (versions are illustrative — use your actual versions):
    # - name: quay.io/containerdisks/fedora:40
    # - name: quay.io/containerdisks/centos-stream:9
    # - name: registry.redhat.io/rhel9/rhel-guest-image:9.4
```

### How to determine which containerdisks to mirror

1. Review the VM templates offered to tenants in your OSAC deployment.
2. Each template references a containerdisk image in its `DataVolume` or
   `DataSource` specification.
3. Add each unique containerdisk reference to the `additionalImages` section
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
    # Add one entry per OS image template
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
oc apply -f oc-mirror-workspace/working-dir/cluster-resources/itms-oc-mirror.yaml
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
# Patch each containerdisk source entry in the generated ITMS
# before applying, or edit the applied resource:
oc get itms itms-oc-mirror -o json | \
  jq '(.spec.imageTagMirrors[] |
    select(.source | test("containerdisk|rhel-guest")))
    .mirrorSourcePolicy = "NeverContactSource"' | \
  oc apply -f -
```

Verify the policy is set:

```bash
oc get itms itms-oc-mirror -o json | \
  jq '.spec.imageTagMirrors[] |
    select(.source | test("containerdisk|rhel-guest")) |
    {source, mirrorSourcePolicy}'
```

### Step 4 — Configure CDI for disconnected registry imports

Two distinct image-pull paths exist for VMaaS:

1. **CRI-O / kubelet pulls** — Pod images (e.g. `virt-launcher`)
   are resolved automatically via ITMS/IDMS.  No extra configuration
   is needed beyond the cluster-wide mirror sets applied in Step 3.

2. **CDI registry imports** — When a `DataVolume` imports a
   containerdisk from a registry, CDI uses its own HTTP client,
   which does **not** honour ITMS/IDMS.  Each registry-backed
   `DataVolume` must set `pullMethod: node` so CDI delegates the
   pull to CRI-O (which does honour ITMS/IDMS).

> **OSAC-2991**: The `pullMethod: node` setting is not yet exposed
> in the OSAC API.  It must be set on each `DataVolume` manually
> or via a template default.

Example `DataVolume` with `pullMethod: node`:

```yaml
apiVersion: cdi.kubevirt.io/v1beta1
kind: DataVolume
metadata:
  name: rhel9-containerdisk
spec:
  source:
    registry:
      url: "docker://<upstream-image>:<tag>"
      pullMethod: node       # delegates to CRI-O / ITMS
  storage:
    resources:
      requests:
        storage: 10Gi
```

#### Optional: CDI insecureRegistries (self-signed certificates only)

If the mirror registry uses a self-signed certificate that is **not**
distributed via trust-manager, add it to CDI's `insecureRegistries`.
Prefer distributing the CA via trust-manager instead.

```bash
# Only needed when the mirror registry CA is not trusted
# cluster-wide.  This script preserves any existing entries.
MIRROR_REG="<mirror-registry>:8443"
CURRENT=$(oc get cdi cdi -n openshift-cnv \
  -o jsonpath='{.spec.config.insecureRegistries}' \
  2>/dev/null)
UPDATED=$(echo "${CURRENT:-[]}" | \
  jq --arg r "$MIRROR_REG" '. + [$r] | unique')
oc patch cdi cdi -n openshift-cnv --type merge \
  -p "$(jq -n --argjson regs "$UPDATED" \
  '{"spec":{"config":{"insecureRegistries":$regs}}}')"
```

### Step 5 — Verify containerdisk resolution via ITMS

Pull using the **upstream** image reference to confirm that ITMS
redirects the request to the mirror registry:

```bash
# Use the upstream tag reference — CRI-O should resolve it via ITMS
oc debug node/<node-name> -- chroot /host \
  crictl pull <upstream-registry>/<containerdisk-image>:<tag>
```

## Verification (BDD Scenarios)

```gherkin
Feature: VMaaS containerdisk mirroring for disconnected OSAC

  Background:
    Given a disconnected OpenShift 4.22 cluster with VMaaS enabled
    And the kubevirt-hyperconverged operator is installed and healthy
    And containerdisk images are mirrored to the local registry
    And ITMS resources include containerdisk mirror entries

  Scenario: Containerdisk images resolve via ITMS
    When a cluster node requests a containerdisk image by its upstream reference
    Then CRI-O resolves the reference via the ITMS to the mirror registry
    And the image pull succeeds

  Scenario: CDI can import a containerdisk using node pull method
    Given a DataVolume is created with pullMethod "node"
    When the DataVolume references a mirrored containerdisk
    Then CDI delegates the pull to CRI-O which resolves via ITMS
    And CDI successfully imports the disk image
    And the resulting PVC contains a valid VM disk

  Scenario: VM boots from a mirrored containerdisk
    Given a VirtualMachine CR references a mirrored containerdisk
    When the VirtualMachine is started
    Then the VMI (VirtualMachineInstance) reaches the "Running" phase
    And the guest OS is accessible via console

  Scenario: Containerdisk ITMS entries block upstream fallback
    Given all containerdisk images are mirrored
    And every containerdisk ITMS source has mirrorSourcePolicy "NeverContactSource"
    When a VM is created and started
    Then CRI-O does not attempt to contact the upstream registry
    And all image pulls resolve to the mirror registry
    # Note: without NeverContactSource, CRI-O may still
    # attempt upstream contact before falling back to the
    # mirror.  In a disconnected network the upstream is
    # unreachable, so the pull succeeds from the mirror
    # but only after a timeout delay.
```

## Validation Commands

```bash
# 1. Verify ITMS includes containerdisk entries
oc get itms -o yaml | grep -B5 -A5 "containerdisk"

# 2. Verify NeverContactSource on containerdisk ITMS entries
oc get itms itms-oc-mirror -o json | \
  jq '.spec.imageTagMirrors[] |
    select(.source | test("containerdisk|rhel-guest")) |
    {source, mirrorSourcePolicy}'

# 3. Verify CDI operator is healthy
oc get pods -n openshift-cnv -l app=cdi-operator

# 4. Test containerdisk pull on a node (use upstream reference
#    to validate ITMS redirect)
oc debug node/<node-name> -- chroot /host \
  crictl pull <upstream-registry>/<containerdisk-image>:<tag>

# 5. Verify CDI insecureRegistries configuration
oc get cdi cdi -n openshift-cnv \
  -o jsonpath='{.spec.config.insecureRegistries}'

# 6. Check DataVolume import status
oc get datavolume -A -o custom-columns=\
'NAME:.metadata.name,PHASE:.status.phase,PROGRESS:.status.progress'

# 7. Verify VirtualMachine status
oc get vmi -A -o custom-columns=\
'NAME:.metadata.name,PHASE:.status.phase,NODE:.status.nodeName'
```

## Troubleshooting

| Symptom | Likely cause | Resolution |
| ------- | ------------ | ---------- |
| DataVolume stuck in `ImportScheduled` | CDI importer pod cannot pull from mirror registry | Check CDI `insecureRegistries`; verify node-level mirror config |
| DataVolume import fails with `x509: certificate signed by unknown authority` | Mirror registry TLS cert not trusted by CDI | Add registry to CDI `insecureRegistries` or distribute CA via trust-manager |
| `ImagePullBackOff` on virt-launcher pods | ITMS entry missing for the containerdisk image | Add the image to `additionalImages` in ISC; re-run `oc-mirror` |
| VM fails to boot — disk import incomplete | DataVolume import timed out or errored | Check DataVolume `.status.conditions` and CDI importer pod logs |
| `ErrImagePull: unauthorized` on node pull | Mirror registry credentials missing from node | Verify global pull secret contains registry entry: `oc get secret/pull-secret -n openshift-config -o jsonpath='{.data.\.dockerconfigjson}' \| base64 -d \| jq '.auths \| keys'` |

## Known Gaps / Limitations

1. **OSAC-5188: Containerdisks not in Tier-1 mirror list** — Containerdisk
   images are not included in the default OSAC `ImageSetConfiguration`.
   Operators must manually identify and add the required containerdisk
   images based on their VM template offerings.

2. **OSAC-2991: `pullMethod: node` not in OSAC API** — The CDI
   `pullMethod` setting is per-`DataVolume`, not a global CDI config.
   It is not yet exposed in the OSAC API.  Each registry-backed
   `DataVolume` must set `pullMethod: node` so CDI delegates the pull
   to CRI-O (which honours ITMS/IDMS).  Until OSAC-2991 is resolved,
   disconnected VMaaS requires manual `DataVolume` configuration.

3. **MaaS (networking) not supported** — Multus-based network services
   (MaaS) are not supported in disconnected mode.  Only VMaaS virtual
   machine workloads are covered by this mirroring category.

4. **Containerdisk size variability** — OS disk images vary widely in size
   (500 MiB to 10+ GiB per image).  Plan registry storage accordingly
   based on the number and size of OS templates offered.
