# Category 1E: Mirroring AAP (Ansible Automation Platform) Images

## Aim

Mirror Ansible Automation Platform controller and hub images, along with
their dependent Redis and PostgreSQL images, into a disconnected mirror
registry.  Configure playbook `extra_vars` overrides so that Ansible jobs
execute using mirrored execution environment images.

> **Relationship to the main guide**: this document covers AAP-specific
> mirroring details that extend the base `ImageSetConfiguration` from
> [disconnected-install.md](../disconnected-install.md).  The AAP operator
> itself (`ansible-automation-platform-operator`) is mirrored as part of
> [mirroring-operators.md](mirroring-operators.md).  The `osac-aap` core
> image is mirrored as part of
> [mirroring-core-components.md](mirroring-core-components.md).

## Prerequisites (TDD: define checks first)

| Check | Command / evidence |
| ----- | ------------------ |
| Category 1A complete: `ansible-automation-platform-operator` installed | `oc get csv -n ansible-aap \| grep ansible \| grep Succeeded` |
| Category 1B complete: `osac-aap:0.0.19` image mirrored | Image present in mirror registry |
| AAP subscription manifest available | `license.zip` file |
| IDMS/ITMS applied to the cluster | `oc get idms,itms` |
| Mirror registry reachable from cluster nodes | Node-level pull test succeeds |

## Resources Required

| Resource | Minimum |
| -------- | ------- |
| Additional disk (mirror registry) | ~3 GiB for AAP operand images |
| AAP subscription manifest | `license.zip` from Red Hat Subscription Management |
| Execution environment image(s) | Custom EE images used by OSAC playbooks |

## ImageSetConfiguration Section

The AAP operator itself is mirrored via the operator catalog (Category 1A).
The operator's **operand images** (controller, hub, Redis, PostgreSQL) are
bundled within the operator catalog and are pulled automatically when the
`AnsibleAutomationPlatform` CR is created.

### Operator-managed images (NOT controlled by Helm)

The following images are managed by the AAP operator internally via
environment variables on the operator deployment.  They are **not**
configurable through OSAC Helm values:

| Image | Purpose | How it's configured |
| ----- | ------- | ------------------- |
| Redis | Session cache and task queue for AAP controller | AAP operator env var `RELATED_IMAGE_REDIS` |
| PostgreSQL | Database for AAP controller and hub | AAP operator env var `RELATED_IMAGE_POSTGRES` |
| AAP Controller (awx-ee) | Default execution environment | AAP operator env var `RELATED_IMAGE_EE` |
| AAP Hub (galaxy-ng) | Automation content hub | AAP operator env var `RELATED_IMAGE_HUB` |

These images are included in the operator bundle and are resolved via
IDMS when the operator catalog is mirrored.  No explicit `additionalImages`
entries are needed for them.

### Image Reference

The following images are required for AAP integration (version tags are
specified in the ImageSetConfiguration below):

| Image | Registry | Purpose |
|-------|----------|---------|
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
oc create secret generic automationcontroller-license \
  --from-file=manifest=license.zip \
  -n ansible-aap
```

### Step 4 — Configure playbook extra_vars for disconnected mode

OSAC playbooks that pull images during execution need `extra_vars`
overrides to reference mirrored images.  These overrides are set in the
OSAC Helm values (osac chart):

```yaml
# In osac values file:
aap:
  extraVars:
    # Override image references used by playbooks
    cli_image: "<mirror-registry>:8443/openshift/origin-cli:4.20"
    # Additional playbook-specific overrides as needed
```

These `extra_vars` are passed to the `AutomationController` job templates
and override hardcoded upstream image references within playbooks.

### Step 5 — Verify AAP controller deployment

```bash
# Wait for AAP controller to become ready
oc get pods -n ansible-aap -l app.kubernetes.io/managed-by=automationcontroller-operator

# Check AAP controller route
oc get route -n ansible-aap
```

## Verification (BDD Scenarios)

```gherkin
Feature: AAP image mirroring for disconnected OSAC

  Background:
    Given a disconnected OpenShift 4.22 cluster
    And the ansible-automation-platform-operator is installed
    And the AAP operator catalog is mirrored (Category 1A)
    And IDMS resources include AAP-related image mirror entries

  Scenario: AAP operator deploys controller using mirrored images
    Given the AnsibleAutomationPlatform CR is created
    When the AAP operator deploys the controller
    Then the controller pods use images resolved via IDMS
    And no external registry access is attempted

  Scenario: AAP operator-managed Redis uses mirrored image
    When the AAP controller deployment includes Redis
    Then the Redis container image resolves via IDMS
    And Redis reaches Ready state

  Scenario: AAP operator-managed PostgreSQL uses mirrored image
    When the AAP controller deployment includes PostgreSQL
    Then the PostgreSQL container image resolves via IDMS
    And PostgreSQL reaches Ready state

  Scenario: Ansible job executes using mirrored execution environment
    Given an OSAC playbook job template exists
    And the job template references a mirrored EE image
    When the job is launched
    Then the execution environment pod starts successfully
    And the job completes without image pull errors

  Scenario: Playbook extra_vars override image references
    Given OSAC Helm values include aap.extraVars overrides
    When a playbook runs that references an image
    Then the playbook uses the extra_vars image reference
    And the image resolves to the mirror registry

  Scenario: AAP subscription manifest is valid
    Given the automationcontroller-license secret exists
    When the AAP controller starts
    Then the controller accepts the subscription manifest
    And the AAP dashboard is accessible
```

## Validation Commands

```bash
# 1. Verify AAP operator CSV is Succeeded
oc get csv -n ansible-aap | grep ansible

# 2. Verify AAP controller pods are running
oc get pods -n ansible-aap

# 3. Check AAP operator related images (resolved via IDMS)
oc get csv -n ansible-aap -o json | \
  jq -r '.items[].spec.install.spec.deployments[].spec.template.spec.containers[].env[] |
    select(.name | startswith("RELATED_IMAGE")) | "\(.name)=\(.value)"'

# 4. Verify no ImagePullBackOff in AAP namespace
oc get events -n ansible-aap --field-selector reason=Failed | grep -i pull

# 5. Check AAP controller route
oc get route -n ansible-aap -o jsonpath='{.items[0].spec.host}'

# 6. Verify subscription manifest secret exists
oc get secret automationcontroller-license -n ansible-aap

# 7. Test Ansible job execution
# (Run a test playbook and check for success)
oc get ansiblejob -n osac -o custom-columns=\
'NAME:.metadata.name,STATUS:.status.ansibleJobResult.status'

# 8. Verify osac-aap integration pod
oc get pods -n osac -l app.kubernetes.io/name=osac-aap
```

## Troubleshooting

| Symptom | Likely cause | Resolution |
| ------- | ------------ | ---------- |
| AAP controller stuck in `Pending` | Operator images not mirrored via IDMS | Verify `oc get idms -o yaml \| grep awx`; re-mirror MCE catalog |
| Redis `ImagePullBackOff` | RELATED_IMAGE_REDIS not resolved via IDMS | Check IDMS entries for Redis image; the image is in the operator bundle |
| PostgreSQL `ImagePullBackOff` | RELATED_IMAGE_POSTGRES not resolved via IDMS | Check IDMS entries for PostgreSQL image; the image is in the operator bundle |
| AAP license error | Manifest not uploaded or expired | Verify secret `automationcontroller-license` exists and manifest is valid |
| Ansible job fails with `EE image not found` | Custom EE image not mirrored | Add EE image to `additionalImages`; re-run `oc-mirror` |
| Playbook uses wrong image reference | `extra_vars` not configured in Helm values | Set `aap.extraVars` in the osac chart values file |
| AAP controller route not accessible | Route not created or TLS misconfigured | Check `oc get route -n ansible-aap`; verify cert-manager ClusterIssuer |

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

3. **OSAC-6160: AAP operator PostgreSQL** — There is ongoing work to
   align the AAP operator's internal PostgreSQL with OSAC's bundled
   PostgreSQL.  In disconnected environments, both PostgreSQL instances
   must be mirrored (one via the operator catalog, one via
   `additionalImages`).

4. **Playbook image references** — Some OSAC playbooks contain hardcoded
   image references that must be overridden via `extra_vars`.  Review
   playbook templates for any upstream image references that need
   disconnected overrides.
