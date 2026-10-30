# Category D: Mirroring BMaaS / Metal3 Images

## WIP

## Aim

Mirror Bare Metal as a Service (BMaaS) and Metal3 agent images into a
disconnected mirror registry using the `additionalImages` section of the
`oc-mirror` v2 `ImageSetConfiguration`.  Configure IDMS
(ImageDigestMirrorSet) for image resolution and prepare for the
post-OSAC-4558 OCI reference approach.

> **Relationship to the main guide**: this document covers BMaaS-specific
> images that extend the base `ImageSetConfiguration` from
> [README.md](README.md).  The operator
> required for BMaaS (`multicluster-engine`) is covered in
> [mirroring-operators.md](mirroring-operators.md).  The
> `bare-metal-fulfillment-operator` core image is covered in
> [mirroring-core-components.md](mirroring-core-components.md).

## Prerequisites

| Check | Command / evidence |
| ----- | ------------------ |
| Category B complete: `bare-metal-fulfillment-operator` image mirrored | Image present in mirror registry |
| BMaaS enabled in OSAC Helm values | `global.services.bmaas.enabled: true` in values file |

<!-- HOLD — awaiting BMaaS Doc 2 qualification; proposed target operators.md §BMaaS prerequisite operators. See docs/guides/installation/disconnected/operators.md -->

<!-- HOLD — awaiting BMaaS Doc 2 qualification; proposed target operators.md §BMaaS prerequisite operands. See docs/guides/installation/disconnected/operators.md -->

<!-- HOLD — awaiting BMaaS Doc 2 qualification; proposed target prereqs.md §BMaaS network diagnostics. See docs/guides/installation/disconnected/prereqs.md -->

The mirroring handoff retains the selected mirror authority, CA and
read-access binding plus approved mirror reachability observations.
BMaaS provisioning-network qualification is separate and remains on hold.

## Resources Required

| Resource | Minimum |
| -------- | ------- |
| Additional disk (mirror registry) | ~5 GiB for agent and discovery ISO images |

<!-- HOLD — awaiting BMaaS Doc 2 qualification; proposed target prereqs.md §BMaaS provisioning network. See docs/guides/installation/disconnected/prereqs.md -->

<!-- HOLD — awaiting BMaaS Doc 2 qualification; proposed target operators.md §BMaaS prerequisite APIs. See docs/guides/installation/disconnected/operators.md -->

## ImageSetConfiguration Section

Metal3 agent images are used during bare-metal host discovery and
provisioning.  These images are pulled by the assisted-installer service
and the Ironic provisioning agents.

The `bare-metal-fulfillment-operator` image is already included in the
core components (Category B).  The additional Metal3-specific images
depend on the MCE version and are typically mirrored as part of the MCE
operator catalog.

### Image Reference

The following Metal3 agent images may be required (exact images depend on
the MCE version; version tags are specified in the ImageSetConfiguration
below):

| Image | Registry | Purpose |
| ------- | ---------- | --------- |
| `registry.redhat.io/multicluster-engine/assisted-installer-agent-rhel9` | registry.redhat.io | Assisted installer discovery agent |
| `registry.redhat.io/multicluster-engine/assisted-installer-rhel9` | registry.redhat.io | Assisted installer service |
| `registry.redhat.io/multicluster-engine/assisted-image-service-rhel9` | registry.redhat.io | Assisted image service |

### Agent images as additionalImages

If specific Metal3 agent images need to be mirrored beyond what the MCE
operator catalog provides, add them to the `additionalImages` section:

```yaml
  additionalImages:
    # --- BMaaS / Metal3 agent images ---
    # These are typically included in the MCE operator catalog mirror.
    # Add explicit entries only if specific versions are needed or if
    # the catalog mirror does not include them.
    #
    # The exact images depend on the MCE version and the assisted-service
    # agent configuration.  Common images include:
    # - name: registry.redhat.io/multicluster-engine/assisted-installer-agent-rhel9:<version>
    # - name: registry.redhat.io/multicluster-engine/assisted-installer-rhel9:<version>
    # - name: registry.redhat.io/multicluster-engine/assisted-image-service-rhel9:<version>
```

> **Note**: When the `multicluster-engine` operator is mirrored as part of
> Category A, the MCE catalog includes its operand images.  Explicit
> `additionalImages` entries are only needed for images not bundled in the
> catalog (e.g., custom agent images or OSAC-specific overrides).

## Execution Steps

### Step 1 — Verify MCE operator images are mirrored

The `multicluster-engine` operator catalog, mirrored in Category A,
includes most Metal3 agent images.  Verify they are present:

```bash
# Verify IDMS mirror rules include MCE/assisted entries
# (this confirms mirror configuration, not that images are
# present — test a pull to verify registry content)
oc get idms -o yaml | grep -i "multicluster-engine\|assisted"
```

<!-- HOLD — awaiting BMaaS Doc 2 qualification; proposed target prereqs.md §BMaaS mirrored-agent diagnostics. See docs/guides/installation/disconnected/prereqs.md -->

### Step 2 — Add any additional agent images (if needed)

If specific agent images are missing from the MCE catalog mirror, add them
to the unified `ImageSetConfiguration` and re-run `oc mirror` using the
canonical command from
[mirroring-operators.md § Step 1](mirroring-operators.md#step-1--dry-run-connected-bastion)
(dry run first, then full mirror).

### Step 3 — Apply IDMS (disconnected cluster)

Metal3 agent images mirrored from the MCE operator catalog are
typically referenced by digest and require an **ImageDigestMirrorSet**
(IDMS).  If you added tagged `additionalImages` entries, also apply
the generated **ImageTagMirrorSet** (ITMS):

```bash
oc apply -f oc-mirror-workspace/working-dir/cluster-resources/idms-oc-mirror.yaml

# Apply ITMS only when oc-mirror generated the optional file.
itms_file=oc-mirror-workspace/working-dir/cluster-resources/itms-oc-mirror.yaml
if [ -f "$itms_file" ]; then
  oc apply -f "$itms_file"
else
  echo "INFO: no generated ITMS file; no tagged additionalImages to apply"
fi
```

### BMaaS inventory scope

> **Scope note**: Doc 1 retains only BMaaS mirror inventory and
> mirroring-artifact verification. BMaaS runtime procedures are removed;
> any Doc 2 installation coverage awaits separate qualification.
> MCE and BMF image pins are conditional on BMaaS selection
> (`global.services.bmaas.enabled: true`).

<!-- HOLD — awaiting BMaaS Doc 2 qualification; proposed target operators.md §BMaaS AgentServiceConfig. See docs/guides/installation/disconnected/operators.md -->

<!-- HOLD — awaiting BMaaS Doc 2 qualification; proposed target operators.md §BMaaS BMC Secret and BareMetalHost setup. See docs/guides/installation/disconnected/operators.md -->

## Mirroring artifact checks

<!-- HOLD — awaiting BMaaS Doc 2 qualification; proposed target operators.md §BMaaS operator and operand diagnostics. See docs/guides/installation/disconnected/operators.md -->

```bash
# 4. Check IDMS entries for MCE/Metal3 images
oc get idms -o yaml | grep -c "multicluster-engine"
```

<!-- HOLD — awaiting BMaaS Doc 2 qualification; proposed target operators.md §BMaaS host and AgentServiceConfig diagnostics. See docs/guides/installation/disconnected/operators.md -->

<!-- HOLD — awaiting BMaaS Doc 2 qualification; proposed target installation.md §BMaaS post-install diagnostics. See docs/guides/installation/disconnected/installation.md -->

<!-- HOLD — awaiting BMaaS Doc 2 qualification; proposed target operators.md §BMaaS runtime troubleshooting. See docs/guides/installation/disconnected/operators.md -->
<!-- HOLD — awaiting BMaaS Doc 2 qualification; proposed target installation.md §BMaaS post-install troubleshooting. See docs/guides/installation/disconnected/installation.md -->

## Known Gaps / Limitations

1. **MCE catalog completeness** — The `multicluster-engine` operator
   catalog includes most Metal3 agent images, but the exact set depends
   on the MCE version.  Verify that all required agent images are present
   in the mirror after running `oc-mirror`.

<!-- HOLD — awaiting BMaaS Doc 2 qualification; proposed target prereqs.md §BMaaS provisioning network. See docs/guides/installation/disconnected/prereqs.md -->

2.**Agent image versioning** — Metal3 agent images are versioned with
   the MCE release.  Ensure the mirrored MCE version matches the
   installed operator version to avoid version skew.
