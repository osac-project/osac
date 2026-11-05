# Category C: Mirroring VMaaS Containerdisk Images

- [Category C: Mirroring VMaaS Containerdisk Images](#category-c-mirroring-vmaas-containerdisk-images)
  - [Aim](#aim)
  - [Prerequisites](#prerequisites)
  - [Resources Required](#resources-required)
  - [ImageSetConfiguration Section](#imagesetconfiguration-section)
    - [Image Reference](#image-reference)
    - [How to determine which containerdisks to mirror](#how-to-determine-which-containerdisks-to-mirror)
  - [Execution Steps](#execution-steps)
    - [Step 1 — Add containerdisk images to the ImageSetConfiguration](#step-1--add-containerdisk-images-to-the-imagesetconfiguration)
    - [Step 2 — Run oc-mirror (connected bastion)](#step-2--run-oc-mirror-connected-bastion)
    - [Step 3 — Apply updated ITMS (disconnected cluster)](#step-3--apply-updated-itms-disconnected-cluster)
      - [Required coverage and exact-tag review](#required-coverage-and-exact-tag-review)
  - [Consumer handoff constraints](#consumer-handoff-constraints)
  - [Mirroring artifact checks (`oc`)](#mirroring-artifact-checks-oc)
  - [Known Gaps / Limitations](#known-gaps--limitations)

## Aim

Mirror VM disk images (containerdisks) required by OSAC's VMaaS (Virtual
Machine as a Service) into a disconnected mirror registry using the
`additionalImages` section of the `oc-mirror` v2 `ImageSetConfiguration`.

> **Relationship to the main guide**: this document covers VMaaS-specific
> images that extend the base `ImageSetConfiguration` from
> [README.md](./README.md).  The operators
> required for VMaaS (`kubevirt-hyperconverged`, `lvms-operator`,
> `metallb-operator`) are covered in
> [mirroring-operators.md](mirroring-operators.md).

## Prerequisites

| Check | Command / evidence |
| ----- | ------------------ |
| Category B inventory composed before adding Category C | Shared ISC `mirror.additionalImages` contains the selected core component images; applied IDMS/ITMS and populated mirrors are not prerequisites for inventory composition |
| VMaaS is enabled in OSAC Helm values | `global.services.vmaas.enabled: true` in values file |

<!-- Moved to prereqs.md §3 diagnostics. See docs/guides/installation/disconnected/prereqs.md -->

Before mirroring, H6/H7 supply the approved mirror authority, CA/read-access binding and independently selected source inventory; they do not require a live OSAC instance or a future importer. After the full mirror, obtain the node-transport observation through Category A Step 2c's [shared checkpoint](prereqs.md#pre-handoff-node-transport-checkpoint). Generic Pod-network diagnostics remain in `prereqs.md` §3 after handoff; actual importer-network/client acceptance remains in `installation.md` after product/platform preparation. A bastion or node result alone does not establish importer-network access.

<!-- Moved to prereqs.md §3 diagnostics. See docs/guides/installation/disconnected/prereqs.md -->

## Resources Required

| Resource | Minimum |
| -------- | ------- |
| Additional disk (mirror registry) | Varies by OS image count; typically 2-10 GiB per containerdisk |

## ImageSetConfiguration Section

Containerdisk images are OCI container images that embed a virtual machine
disk (qcow2) as a layer.  They are referenced as `additionalImages` in the
`ImageSetConfiguration` and resolved via ITMS (tags) or IDMS (digests)
for node image pulls on the cluster.

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

Before mirroring, obtain a complete deployment-specific inventory of
upstream image references, including full tags or digests, from an
upstream-backed catalog or the approved deployment configuration. A fresh
installation does not require a running OSAC API. A catalog already using
mirror URLs is not an upstream inventory: retain the original upstream
references alongside their mirror destinations. Add every selected
upstream reference to `additionalImages` and retain the selected repositories,
exact tags (or digests), and intended mirror references with the ISC and
workspace in the organization's existing records.

<!-- Moved to operators.md §Selected DiskImage inventory export. See docs/guides/installation/disconnected/operators.md -->

## Execution Steps

### Step 1 — Add containerdisk images to the ImageSetConfiguration

Extend the unified `ImageSetConfiguration` with the containerdisk entries
for your deployment:

```yaml
  additionalImages:
    # ... existing dependency and core images from Category B ...

    # --- VMaaS containerdisks ---
    - name: <registry>/<repository>/<image>:<tag>
    # Add one entry per DiskImage
```

### Step 2 — Run oc-mirror (connected bastion)

Run the canonical mirror command from
[mirroring-operators.md § Step 1](mirroring-operators.md#step-1--dry-run-connected-bastion)
(dry run first, then full mirror).

### Step 3 — Apply updated ITMS (disconnected cluster)

> **Note**: Adding new digest-only IDMS entries does not trigger node
> drain or restart. On OpenShift 4.22, creating an ITMS or adding tag-based
> entries can drain nodes under the default `Special` disruption policy
> for `/etc/containers/registries.conf`; modifying or deleting mirror
> entries can also cause drains. Mirror-only changes do not normally
> require a reboot. Check the effective node disruption policy, schedule
> a maintenance window when drains are required, and wait for MCPs to
> converge after applying
> (`oc wait mcp --all --for=condition=Updated --timeout=30m`).

Tagged containerdisk images require ITMS; digest-pinned images use IDMS.
Do not apply raw generated files here. Follow Category A
[Step 2d](mirroring-operators.md#2d-apply-generated-artifacts) to patch **all**
generated IDMS and ITMS entries to `NeverContactSource`, validate both kinds
before any apply or handoff, and apply the reviewed artifacts. Digest-pinned
selections need IDMS source/digest coverage review in addition to that shared
all-entry policy gate; tag coverage remains the ITMS review below.

#### Required coverage and exact-tag review

After the shared mirror run and Category A apply gate, verify that the selected
Category B core images are present at their direct mirror references using the
approved administration-host mirror-read client context, and that the
[live-rule acceptance check](README.md#live-core-image-mirror-rules) passes
before handoff. Continue with the Category C checks below.

Keep the selected **upstream containerdisk repositories and exact tags**,
their intended mirror references, and the review results in your organization's
existing records. No particular filename, JSON serialization or OSAC export
is required; a first mirror needs neither a running OSAC API nor installed CDI.
Preserve original upstream references even when the runtime catalog uses
mirror URLs. Do not infer the selection from a live mirror-only catalog.

For **each** selected tagged repository, manually review the complete generated
and live ITMS rules using the
[API matching semantics](https://github.com/openshift/api/blob/f94a02c852f17a74216c277542c391b2c851d6ce/config/v1/types_image_tag_mirror_set.go#L44-L65):

1. Match against `source`, not against mirror destinations. For an ordinary
   host/repository prefix, require an exact match or a `/` boundary. If a
   wildcard-host source is present, evaluate it using the API's documented
   wildcard semantics; do not treat `*` as a literal repository prefix.
2. Use only the **most-specific matching source**. A safe parent rule does
   not rescue an unsafe or empty more-specific rule. Review every object
   defining the same effective source; mirrors for that source can merge.
3. Require nonempty mirror destinations and `NeverContactSource` on every
   applicable entry. Also retain Category A's all-entry gate, not just this
   selected-image review. Missing coverage blocks handoff.
4. Derive each mirror repository by replacing the matched source prefix
   with the mirror prefix and preserving the remaining repository suffix.
   Retain the **exact selected tag**. Read that exact manifest using the
   approved mirror-read client and credentials; verify the expected image
   content and required platform are available. Registry/namespace/repository
   existence or a successful `/v2/` response does **not** prove tag availability.
   Do not accept a failed or ambiguous manifest lookup as coverage.

Examples for the manual review (all sources below are ordinary prefixes):

| Selected repository | Rules | Result |
| --- | --- | --- |
| `quay.io/team/disk` | Safe `quay.io/team` to `mirror.example/team` | Applicable; check `mirror.example/team/disk:<selected-tag>` |
| `quay.io/team/disk-evil` | Only safe `quay.io/team/disk` | No coverage: prefix boundary does not match |
| `quay.io/team/disk` | Safe `quay.io/team`, unsafe `quay.io/team/disk` | Reject: the more-specific rule wins |
| `quay.io/team/disk` | Two entries for `quay.io/team/disk`, one unsafe | Reject: inspect all entries for the effective source |

For digest-pinned selections, apply the same most-specific upstream-source
review to IDMS and read the exact selected mirror digest, using the
[IDMS API semantics](https://github.com/openshift/api/blob/f94a02c852f17a74216c277542c391b2c851d6ce/config/v1/types_image_digest_mirror_set.go#L44-L65).
The joint policy gate does not establish selected tag or digest availability.

An exact-tag or exact-digest mirror read and mirror-policy review are not CDI
import acceptance.
Complete the separate CDI gate in `installation.md` after product preparation.

## Consumer handoff constraints

Distinguish node image pulls from CDI registry imports. ITMS/IDMS govern
matching kubelet/CRI-O image pulls, including Pod images such as
`virt-launcher`, but their presence alone does not establish CDI routing.

CDI's [registry API](https://github.com/kubevirt/containerized-data-importer/blob/15b1fc5958919ae6f50d7165b1ce14a9ce3c8167/staging/src/kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1/types.go#L197-L218)
distinguishes `pullMethod: pod` (default import) from `pullMethod: node`
(node-cache import). The pod path uses an importer registry client; the
[node path](https://github.com/kubevirt/containerized-data-importer/blob/15b1fc5958919ae6f50d7165b1ce14a9ce3c8167/pkg/controller/import-controller.go#L1161-L1188)
uses the selected registry image as a Pod container image. Do not claim that
all CDI imports bypass node mirror configuration, or that ITMS alone proves
an importer registry client's routing.

For the installed release, inspect the actual DataVolume source, effective
pull method, generated Pod images and importer configuration. Qualify its
source, mirror routing, trust and authentication separately. Where the
supported client path requires a direct mirror URL, use the supported
DiskImage lifecycle in `installation.md`. If it supplies no registry
credentials, the selected mirror repository must support approved anonymous
reads; do not enable broad anonymous access or assume every CDI path is
unauthenticated. A successful actual selected-image CDI import remains a
separate acceptance gate, not a result of this IDMS/ITMS policy check.
<!-- Moved to installation.md §Mirror-backed DiskImage preparation and replacement. See docs/guides/installation/disconnected/installation.md -->

<!-- Moved to installation.md §CDI DataVolume import configuration. See docs/guides/installation/disconnected/installation.md -->

<!-- Moved to prereqs.md §Registry trust configuration. See docs/guides/installation/disconnected/prereqs.md -->

<!-- Moved to installation.md §CDI registry configuration and concurrency. See docs/guides/installation/disconnected/installation.md -->

<!-- Moved to prereqs.md §3 diagnostics. See docs/guides/installation/disconnected/prereqs.md -->

<!-- Moved to installation.md §VMaaS lifecycle acceptance. See docs/guides/installation/disconnected/installation.md -->

## Mirroring artifact checks (`oc`)

Use Category A's all-entry pre-apply check and the required coverage and
exact-tag review above. A name-pattern search for `containerdisk` or
`rhel-guest` cannot establish coverage of the selected image inventory.

<!-- Moved to installation.md §CDI health and configuration diagnostics. See docs/guides/installation/disconnected/installation.md -->

<!-- Moved to installation.md §DataVolume import diagnostics. See docs/guides/installation/disconnected/installation.md -->

<!-- Moved to prereqs.md §3 diagnostics. See docs/guides/installation/disconnected/prereqs.md -->

<!-- Moved to installation.md §VirtualMachineInstance diagnostics. See docs/guides/installation/disconnected/installation.md -->

<!-- Moved to installation.md §VMaaS runtime troubleshooting. See docs/guides/installation/disconnected/installation.md -->
<!-- Moved to installation.md §VMaaS runtime troubleshooting. See docs/guides/installation/disconnected/installation.md -->
<!-- Moved to prereqs.md §Mirror-read credential diagnostics. See docs/guides/installation/disconnected/prereqs.md -->

## Known Gaps / Limitations

1. **Release-specific CDI path** — Qualify the installed release's effective
   registry pull method and supported OSAC source/authentication settings.
   Do not assume a global CDI option changes every DataVolume's import path,
   or that a node image pull proves importer-client mirror routing.

<!-- Moved to installation.md §Mirror-backed DiskImage preparation. See docs/guides/installation/disconnected/installation.md -->

2. **Containerdisk size variability** — OS disk images vary widely in size
   (500 MiB to 10+ GiB per image).  Plan registry storage accordingly
   based on the number and size of the containerdisks your `DiskImage`
   resources reference.
