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
      - [Enforce NeverContactSource (recommended)](#enforce-nevercontactsource-recommended)
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
| Category B complete: IDMS/ITMS applied, core images mirrored | `oc get idms,itms` |
| VMaaS is enabled in OSAC Helm values | `global.services.vmaas.enabled: true` in values file |

<!-- Moved to prereqs.md §3 diagnostics. See docs/guides/installation/disconnected/prereqs.md -->

H6/H7 must include the selected mirror authority and CA/read-access
binding, with approved node and importer-network reachability evidence.
A bastion or node result alone does not establish importer-network access.

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
upstream reference to `additionalImages` and archive this inventory with
the ISC and workspace as `selected-diskimage-images.txt`.

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

Use `selected-diskimage-images.txt` from the approved upstream inventory:
one full tagged or digest-pinned image reference per line, not mirror
destinations. Derive the tag-only repository input below for ITMS policy
enforcement. Digest-pinned entries are checked against IDMS separately;
ensure their generated IDMS entries also use `NeverContactSource` before
applying them through Category A's mirror-resource application step.
A first mirror does not require a running OSAC API or installed CDI.

<!-- Moved to operators.md §Selected DiskImage inventory export. See docs/guides/installation/disconnected/operators.md -->

```bash

test -s selected-diskimage-images.txt || {
  echo "ERROR: no selected registry DiskImage sources found" >&2
  exit 1
}

sed '/@/d; s/:[^/:]*$//' selected-diskimage-images.txt | \
  sort -u > selected-diskimage-sources.txt

jq -Rn '[inputs] | map(select(length > 0))' \
  < selected-diskimage-sources.txt > selected-diskimage-sources.json

for itms_name in $(oc get itms -o json | \
  jq -r --slurpfile selected selected-diskimage-sources.json \
  '$selected[0] as $sel | [.items[] | select(any(.spec.imageTagMirrors[]?; .source as $src | (.mirrors // []) as $mirs | any($sel[]; . as $s | $s == $src or startswith($src + "/") or any($mirs[]; . as $m | $s == $m or ($s | startswith($m + "/"))))))] | unique_by(.metadata.name)[] | .metadata.name'); do

  oc get itms "$itms_name" -o json | \
    jq -c --slurpfile selected selected-diskimage-sources.json \
    '$selected[0] as $sel | .spec.imageTagMirrors |= map(.source as $src | (.mirrors // []) as $mirs | if any($sel[]; . as $s | $s == $src or startswith($src + "/") or any($mirs[]; . as $m | $s == $m or ($s | startswith($m + "/")))) then . + {mirrorSourcePolicy: "NeverContactSource"} else . end)' | \
    oc apply -f -

done
```

Verify every selected source exists and is fail-closed:

```bash
# ----- ITMS/IDMS NeverContactSource enforcement -----
#
# Intentionally stricter than prefix matching — avoids false positives:
#   Negative fixture 1: source "quay.io/team/disk" must NOT cover
#     "quay.io/team/disk-evil" (prefix overlap, different repo)
#   Negative fixture 2: a safe broad parent must NOT hide an unsafe
#     more-specific child rule

ITMS_JSON=$(oc get itms -o json) || {
  echo "ERROR: cannot retrieve ImageTagMirrorSet resources" >&2
  exit 1
}
IDMS_JSON=$(oc get idms -o json) || {
  echo "ERROR: cannot retrieve ImageDigestMirrorSet resources" >&2
  exit 1
}

fail=0
while IFS= read -r image_ref; do
  [ -n "$image_ref" ] || continue
  if [[ "$image_ref" == *@* ]]; then
    img=${image_ref%@*}
    img=$(printf '%s\n' "$img" | sed 's/:[^/:]*$//')
    policy_kind=IDMS
    policy_field=imageDigestMirrors
    policy_json=$IDMS_JSON
  else
    img=${image_ref%:*}
    policy_kind=ITMS
    policy_field=imageTagMirrors
    policy_json=$ITMS_JSON
  fi

  # Require at least one EXACT source match (not prefix)
  exact_count=$(echo "$policy_json" | jq -r \
    --arg img "$img" --arg field "$policy_field" \
    '[.items[].spec[$field][]? | select(.source == $img)] | length')
  if [ "$exact_count" -eq 0 ]; then
    echo "FAIL: no exact $policy_kind entry for $img" >&2
    fail=1
    continue
  fi

  # ALL exact-match entries must have NeverContactSource and
  # non-empty mirrors — a safe broad parent cannot hide an unsafe
  # more-specific child rule
  unsafe=$(echo "$policy_json" | jq -r \
    --arg img "$img" --arg field "$policy_field" \
    '[.items[].spec[$field][]? |
      select(.source == $img) |
      select(.mirrorSourcePolicy != "NeverContactSource" or
             ((.mirrors // []) | length) == 0)] | length')
  if [ "$unsafe" -ne 0 ]; then
    echo "FAIL: $img has $unsafe $policy_kind entries without" \
      "NeverContactSource or with empty mirrors" >&2
    fail=1
  fi
done < selected-diskimage-images.txt

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "PASS: all selected sources have enforced ITMS/IDMS NeverContactSource"
```

## Consumer handoff constraints

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

- **Public containerdisk repository** — ensure the repository
  holding each mirrored containerdisk is public and allows
  anonymous pulls; the CDI importer performs the registry pull
  itself and is given no credentials.
<!-- Moved to installation.md §Mirror-backed DiskImage preparation and replacement. See docs/guides/installation/disconnected/installation.md -->

<!-- Moved to installation.md §CDI DataVolume import configuration. See docs/guides/installation/disconnected/installation.md -->

<!-- Moved to prereqs.md §Registry trust configuration. See docs/guides/installation/disconnected/prereqs.md -->

<!-- Moved to installation.md §CDI registry configuration and concurrency. See docs/guides/installation/disconnected/installation.md -->

<!-- Moved to prereqs.md §3 diagnostics. See docs/guides/installation/disconnected/prereqs.md -->

<!-- Moved to installation.md §VMaaS lifecycle acceptance. See docs/guides/installation/disconnected/installation.md -->

## Mirroring artifact checks (`oc`)

Check the generated mirroring-policy resources directly:

```bash
# ITMS includes containerdisk entries
oc get itms -o yaml | grep -B5 -A5 "containerdisk"

# NeverContactSource is set on containerdisk ITMS entries
oc get itms -o json | \
  jq '[.items[].spec.imageTagMirrors[] |
    select(.source | test("containerdisk|rhel-guest")) |
    {source, mirrorSourcePolicy}]'
```

<!-- Moved to installation.md §CDI health and configuration diagnostics. See docs/guides/installation/disconnected/installation.md -->

<!-- Moved to installation.md §DataVolume import diagnostics. See docs/guides/installation/disconnected/installation.md -->

<!-- Moved to prereqs.md §3 diagnostics. See docs/guides/installation/disconnected/prereqs.md -->

<!-- Moved to installation.md §VirtualMachineInstance diagnostics. See docs/guides/installation/disconnected/installation.md -->

<!-- Moved to installation.md §VMaaS runtime troubleshooting. See docs/guides/installation/disconnected/installation.md -->
<!-- Moved to installation.md §VMaaS runtime troubleshooting. See docs/guides/installation/disconnected/installation.md -->
<!-- Moved to prereqs.md §Mirror-read credential diagnostics. See docs/guides/installation/disconnected/prereqs.md -->

## Known Gaps / Limitations

1. **`pullMethod` not settable through OSAC** — The CDI `pullMethod`
   setting is per-`DataVolume`, not a global CDI config.  OSAC
   creates the `DataVolume` without it and CDI keeps it unset, so
   the importer pulls the containerdisk itself; neither the OSAC
   API nor the `osac` CLI exposes a way to change it, so CDI
   registry imports always bypass ITMS/IDMS and run
   unauthenticated.

<!-- Moved to installation.md §Mirror-backed DiskImage preparation. See docs/guides/installation/disconnected/installation.md -->

2. **Containerdisk size variability** — OS disk images vary widely in size
   (500 MiB to 10+ GiB per image).  Plan registry storage accordingly
   based on the number and size of the containerdisks your `DiskImage`
   resources reference.
