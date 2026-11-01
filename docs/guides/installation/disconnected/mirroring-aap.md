# Category E: Mirroring AAP (Ansible Automation Platform) Images

- [Category E: Mirroring AAP (Ansible Automation Platform) Images](#category-e-mirroring-aap-ansible-automation-platform-images)
  - [Aim](#aim)
  - [Prerequisites](#prerequisites)
  - [Resources Required](#resources-required)
  - [ImageSetConfiguration Section](#imagesetconfiguration-section)
    - [Operator-managed images (NOT controlled by Helm)](#operator-managed-images-not-controlled-by-helm)
    - [Image Reference](#image-reference)
    - [OSAC AAP integration image](#osac-aap-integration-image)
    - [Execution environment images for playbooks](#execution-environment-images-for-playbooks)
  - [Execution Steps](#execution-steps)
    - [Step 2 — Verify IDMS includes AAP image references](#step-2--verify-idms-includes-aap-image-references)
    - [Step 3 — Verify mirror inventory *(Doc 1 scope ends here)*](#step-3--verify-mirror-inventory-doc-1-scope-ends-here)
  - [Known Gaps / Limitations](#known-gaps--limitations)

## Aim

Mirror Ansible Automation Platform controller and hub images, along with
their dependent Redis and PostgreSQL images, into a disconnected mirror
registry.

> **Relationship to the main guide**: this document covers AAP-specific
> mirroring details that extend the base `ImageSetConfiguration` from
> [README.md](README.md).  The AAP operator itself
> (`ansible-automation-platform-operator`) is mirrored as part of
> [mirroring-operators.md](mirroring-operators.md).  The `osac-aap` core
> image is mirrored as part of
> [mirroring-core-components.md](mirroring-core-components.md).

## Prerequisites

| Check | Command / evidence |
| ----- | ------------------ |
| Phase 1B complete: `osac-aap:0.0.45` image mirrored | Image present in mirror registry |
| IDMS/ITMS applied to the cluster | `oc get idms` `oc get itms` `oc get clustercatalog` `oc get catalogsource` |
| Mirror registry reachable from cluster nodes | Node-level pull test succeeds |

## Resources Required

| Resource | Minimum |
| -------- | ------- |
| Additional disk (mirror registry) | ~3 GiB for AAP operand images |
| Execution environment image(s) | Custom EE images used by OSAC playbooks, default: `ghcr.io/osac-project/osac-aap:0.0.45` |

## ImageSetConfiguration Section

The AAP operator itself is mirrored in Phase 1A (see
[mirroring-operators.md](mirroring-operators.md)).
The operator's **operand images** (controller, hub, Redis, PostgreSQL) are
bundled within the operator catalog and are pulled automatically when the
`AnsibleAutomationPlatform` CR is created.

### Operator-managed images (NOT controlled by Helm)

The following images are managed by the AAP operator internally via
environment variables on the operator deployment.  They are **not**
configurable through OSAC Helm values:

| Image | Purpose | Operator image-selection metadata |
| ----- | ------- | --------------------------------- |
| Redis | Session cache and task queue for AAP controller | AAP operator env var `RELATED_IMAGE_REDIS` |
| PostgreSQL | Database for AAP controller and hub | AAP operator env var `RELATED_IMAGE_POSTGRES` |
| AAP Controller (awx-ee) | Default execution environment | `RELATED_IMAGE_EE` in AWX database |
| AAP Hub (galaxy-ng) | Automation content hub | AAP operator env var `RELATED_IMAGE_HUB` |

Selected OSAC execution-environment image to mirror: `ghcr.io/osac-project/osac-aap:0.0.45`.

<!-- Moved to installation.md §5. Helm chart configuration parameters. See docs/guides/installation/disconnected/installation.md -->

These images are included in the operator bundle and are resolved via
IDMS when the operator catalog is mirrored.  No explicit `additionalImages`
entries are needed for them.

### Image Reference

The following images are required for AAP integration (version tags are
specified in the ImageSetConfiguration below):

| Image | Registry | Purpose |
| ------------------------------- | ------- | ------------------------------ |
| `ghcr.io/osac-project/osac-aap` | ghcr.io | OSAC AAP integration component |
| `quay.io/openshift/origin-cli` | quay.io | EE image builds and Helm hooks |

### OSAC AAP integration image

The `osac-aap` image is included in the core components (Category B):

```yaml
  additionalImages:
    - name: ghcr.io/osac-project/osac-aap:0.0.45
```

### Execution environment images for playbooks

OSAC playbooks run in Ansible execution environments (EE).  The default
EE image is bundled with the AAP operator, but custom EE images used by
OSAC automation must be mirrored separately:

```yaml
  additionalImages:
    # --- AAP execution environment images ---
    # Mirror any custom EE images used by OSAC playbooks.
    # The origin-cli image is used for building EE images:
    - name: quay.io/openshift/origin-cli:4.20.0
    - name: "ghcr.io/osac-project/osac-aap:0.0.45"
```

## Execution Steps

<!-- Moved to operators.md §2. Install the Selected Operators. See docs/guides/installation/disconnected/operators.md -->

### Step 2 — Verify IDMS includes AAP image references

```bash
oc get idms -o yaml | grep -i "ansible\|awx\|galaxy\|redis\|aap"
```

### Step 3 — Verify mirror inventory *(Doc 1 scope ends here)*

Doc 1 covers mirror inventory and AAP catalog graph checks
only.  License secret creation, operator operand installation,
and AAP controller deployment belong in Doc 2.

```bash
# Verify AAP catalog entries are present in the mirrored catalog
oc get packagemanifest ansible-automation-platform-operator \
  -n openshift-marketplace -o jsonpath='{.status.channels[*].name}'
```

<!-- Moved to operators.md §2. Install the Selected Operators. See docs/guides/installation/disconnected/operators.md -->
<!-- Moved to installation.md §11.2 Other installation failures. See docs/guides/installation/disconnected/installation.md -->

<!-- Moved to installation.md §11.2 Other installation failures. See docs/guides/installation/disconnected/installation.md -->

## Known Gaps / Limitations

1. **Operator-managed images not configurable via Helm** — The Redis and
   PostgreSQL images used by the AAP operator are set via `RELATED_IMAGE_*`
   environment variables on the operator deployment.  These are part of the
   operator bundle and cannot be overridden through OSAC Helm values.
   Image resolution relies entirely on IDMS.

2. **Custom execution environments** — If OSAC playbooks require custom
   execution environment images beyond the default EE bundled with the
   AAP operator, those images must be explicitly added to the
   `additionalImages` section of the `ImageSetConfiguration`.
