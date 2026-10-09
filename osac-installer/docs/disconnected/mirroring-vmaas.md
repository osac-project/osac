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

### Step 4 — Configure CDI pullMethod

By default, CDI uses the `pod` pull method, which creates a temporary pod
to pull containerdisk images.  In disconnected environments, this pod may
not have the correct registry credentials.

> **OSAC-2991**: The `pullMethod: node` setting is not yet exposed in the
> OSAC API.  This must be configured manually on the `HyperConverged` CR
> or via the CDI configuration.

Configure CDI to use node-level pulls (which inherit CRI-O mirror config):

```bash
oc patch hyperconverged kubevirt-hyperconverged -n openshift-cnv --type merge -p '
{
  "spec": {
    "storageImport": {
      "insecureRegistries": ["<mirror-registry>:8443"]
    }
  }
}'
```

For the `pullMethod: node` workaround, configure the CDI config:

```bash
oc patch cdi cdi -n openshift-cnv --type merge -p '
{
  "spec": {
    "config": {
      "insecureRegistries": ["<mirror-registry>:8443"]
    }
  }
}'
```

### Step 5 — Verify containerdisk resolution

Test pulling a containerdisk image on a cluster node:

```bash
oc debug node/<node-name> -- chroot /host \
  crictl pull <mirror-registry>:8443/<containerdisk-repo>/<image>:<tag>
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
    Given the CDI configuration has insecureRegistries set for the mirror registry
    When a DataVolume is created referencing a mirrored containerdisk
    Then CDI successfully imports the disk image
    And the resulting PVC contains a valid VM disk

  Scenario: VM boots from a mirrored containerdisk
    Given a VirtualMachine CR references a mirrored containerdisk
    When the VirtualMachine is started
    Then the VMI (VirtualMachineInstance) reaches the "Running" phase
    And the guest OS is accessible via console

  Scenario: No external registry access is attempted
    Given all containerdisk images are mirrored
    When a VM is created and started
    Then no network traffic is directed to external registries
    And all image pulls resolve to the mirror registry
```

## Validation Commands

```bash
# 1. Verify ITMS includes containerdisk entries
oc get itms -o yaml | grep -B5 -A5 "containerdisk"

# 2. Verify CDI operator is healthy
oc get pods -n openshift-cnv -l app=cdi-operator

# 3. Test containerdisk pull on a node
oc debug node/<node-name> -- chroot /host \
  crictl pull <mirror-registry>:8443/<containerdisk-image>

# 4. Verify CDI insecureRegistries configuration
oc get cdi cdi -n openshift-cnv -o jsonpath='{.spec.config.insecureRegistries}'

# 5. Check DataVolume import status
oc get datavolume -A -o custom-columns=\
'NAME:.metadata.name,PHASE:.status.phase,PROGRESS:.status.progress'

# 6. Verify VirtualMachine status
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
| `ErrImagePull: unauthorized` on node pull | Mirror registry credentials missing from node | Verify global pull secret: `oc get secret/pull-secret -n openshift-config -o jsonpath='{.data.\.dockerconfigjson}' \| base64 -d \| jq '.auths'` |

## Known Gaps / Limitations

1. **OSAC-5188: Containerdisks not in Tier-1 mirror list** — Containerdisk
   images are not included in the default OSAC `ImageSetConfiguration`.
   Operators must manually identify and add the required containerdisk
   images based on their VM template offerings.

2. **OSAC-2991: `pullMethod: node` not in OSAC API** — The CDI `pullMethod`
   configuration is not exposed in the OSAC API.  It must be set manually
   on the `HyperConverged` or `CDI` CR.  This means disconnected VMaaS
   requires post-install manual configuration.

3. **MaaS (networking) not supported** — Multus-based network services
   (MaaS) are not supported in disconnected mode.  Only VMaaS virtual
   machine workloads are covered by this mirroring category.

4. **Containerdisk size variability** — OS disk images vary widely in size
   (500 MiB to 10+ GiB per image).  Plan registry storage accordingly
   based on the number and size of OS templates offered.
