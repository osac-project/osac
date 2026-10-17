# Category 1E: Mirroring AAP (Ansible Automation Platform) Images

## Aim

Mirror Ansible Automation Platform controller and hub images, along with
their dependent Redis and PostgreSQL images, into a disconnected mirror
registry.

> **Relationship to the main guide**: this document covers AAP-specific
> mirroring details that extend the base `ImageSetConfiguration` from
> [disconnected-install.md](../disconnected-install.md).  The AAP operator
> itself (`ansible-automation-platform-operator`) is mirrored as part of
> [mirroring-operators.md](mirroring-operators.md).  The `osac-aap` core
> image is mirrored as part of
> [mirroring-core-components.md](mirroring-core-components.md).

## Prerequisites

| Check | Command / evidence |
| ----- | ------------------ |
| Phase 1A complete: `ansible-automation-platform-operator` installed | `oc get csv -A \| grep ansible \| grep Succeeded` |
| Phase 1B complete: `osac-aap:0.0.19` image mirrored | Image present in mirror registry |
| AAP subscription manifest available | `license.zip` file |
| IDMS/ITMS applied to the cluster | `oc get idms` `oc get itms` `oc get clustercatalog` `oc get catalogsource` |
| Mirror registry reachable from cluster nodes | Node-level pull test succeeds |

## Resources Required

| Resource | Minimum |
| -------- | ------- |
| Additional disk (mirror registry) | ~3 GiB for AAP operand images |
| AAP subscription manifest | `license.zip` from Red Hat Subscription Management |
| Execution environment image(s) | Custom EE images used by OSAC playbooks, default: `osac-aap:0.0.19` |

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

| Image | Purpose | How it's configured in Operator | How it's configured in the osac helm chart |
| ----- | ------- | ------------------------------- | ------------------------------------------ |
| Redis | Session cache and task queue for AAP controller | AAP operator env var `RELATED_IMAGE_REDIS` | `aap.aap.instance.redisMode: standalone` |
| PostgreSQL | Database for AAP controller and hub | AAP operator env var `RELATED_IMAGE_POSTGRES` | Derived automatically by operator deployment spec |
| AAP Controller (awx-ee) | Default execution environment | `RELATED_IMAGE_EE` in AWX database | `configAsCode.eeImage: ghcr.io/osac-project/osac-aap:0.0.19` |
| AAP Hub (galaxy-ng) | Automation content hub | AAP operator env var `RELATED_IMAGE_HUB` | `aap.aap.instance.hub.disabled: true` |

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

The `osac-aap` image is included in the core components (Category 1B):

```yaml
  additionalImages:
    - name: ghcr.io/osac-project/osac-aap:0.0.19
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
    - name: quay.io/openshift/origin-cli:4.20
```

## Execution Steps

Here, `NS` denotes the namespace in which osac helm chart is installed.

### Step 1 — Verify AAP operator images are mirrored

The AAP operator catalog, mirrored in Category 1A, includes all operand
images via `RELATED_IMAGE_*` environment variables.  Verify:

```bash
# Check AAP operator CSV for related images
oc get csv -n ansible-aap -o yaml | grep -i "RELATED_IMAGE"
```

### Step 2 — Verify IDMS includes AAP image references

```bash
oc get idms -o yaml | grep -i "ansible\|awx\|galaxy\|redis\|aap"
```

### Step 3 — Apply AAP subscription manifest

Upload the AAP subscription manifest to the cluster:

```bash
oc create secret generic config-as-code-manifest-ig --from-file=license.zip="$AAP_LICENSE_FILE" -n "$NS" --dry-run=client -o yaml | oc apply --server-side -f -
```

### Step 4 — Verify AAP controller deployment

```bash
# Wait for AAP controller to become ready
oc get pods -n ansible-aap -l app.kubernetes.io/managed-by=automationcontroller-operator

# Check AAP controller route
oc get route -n ansible-aap
```

## Validation Commands

```bash
# 1. Verify AAP operator CSV is Succeeded
oc get csv -n ansible-aap | grep -i ansible

# 2. Verify AAP controller pods are running
oc get pods -n ansible-aap

# 3. Check AAP operator related images (resolved via IDMS)
oc get csv -n ansible-aap -o json | \
  jq -r '.items[].spec.install.spec.deployments[].spec.template.spec.containers[].env[] |
    select(.name | startswith("RELATED_IMAGE")) | "\(.name)=\(.value)"'

# 4. Verify no image-pull failures or back-off in AAP namespace
oc get events -n "$NS" --field-selector reason=Failed -o wide | grep -i pull
oc get events -n "$NS" --field-selector reason=BackOff -o wide | grep -i pull

# 5. Check AAP controller route
oc get route -n ansible-aap -o jsonpath='{.items[0].spec.host}'

# 6. Verify subscription manifest secret exists
oc get secret config-as-code-manifest-ig -n $NS

# 7. Verify osac-aap integration pod
oc get pods -n "$NS" -l app.kubernetes.io/name=osac-aap
```

## Troubleshooting

| Symptom | Likely cause | Resolution |
| ------- | ------------ | ---------- |
| AAP controller stuck in `Pending` | Operator images not mirrored via IDMS | Verify `oc get idms -o yaml \| grep awx`; re-mirror MCE catalog |
| Redis `ImagePullBackOff` | RELATED_IMAGE_REDIS not resolved via IDMS | Check IDMS entries for Redis image; the image is in the operator bundle |
| PostgreSQL `ImagePullBackOff` | RELATED_IMAGE_POSTGRES not resolved via IDMS | Check IDMS entries for PostgreSQL image; the image is in the operator bundle |
| AAP license error | Manifest not uploaded or expired | Verify secret `config-as-code-manifest-ig` exists in `$NS` and manifest is valid |
| Ansible job fails with `EE image not found` | Custom EE image not mirrored | Add EE image to `additionalImages`; re-run `oc-mirror` |
| AAP controller route not accessible | Route not created or TLS misconfigured | Check `oc get route -n $NS`; verify cert-manager ClusterIssuer |

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
