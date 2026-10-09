# Category 1D: Mirroring BMaaS / Metal3 Images

## Aim

Mirror Bare Metal as a Service (BMaaS) and Metal3 agent images into a
disconnected mirror registry using the `additionalImages` section of the
`oc-mirror` v2 `ImageSetConfiguration`.  Configure IDMS
(ImageDigestMirrorSet) for image resolution and prepare for the
post-OSAC-4558 OCI reference approach.

> **Relationship to the main guide**: this document covers BMaaS-specific
> images that extend the base `ImageSetConfiguration` from
> [disconnected-install.md](../disconnected-install.md).  The operator
> required for BMaaS (`multicluster-engine`) is covered in
> [mirroring-operators.md](mirroring-operators.md).  The
> `bare-metal-fulfillment-operator` core image is covered in
> [mirroring-core-components.md](mirroring-core-components.md).

## Prerequisites (TDD: define checks first)

| Check | Command / evidence |
| ----- | ------------------ |
| Category 1A complete: `multicluster-engine` operator installed | `oc get csv -n multicluster-engine \| grep multicluster \| grep Succeeded` |
| Category 1B complete: `bare-metal-fulfillment-operator` image mirrored | Image present in mirror registry |
| Metal3 / Ironic agent services deployed by MCE | `oc get pods -n multicluster-engine -l app=metal3` |
| Assisted-service (infrastructure-operator) is running | `oc get pods -n multicluster-engine -l app=assisted-service` |
| BMaaS enabled in OSAC Helm values | `bmaas.enabled: true` in values file |
| Mirror registry reachable from provisioning network | Baseboard management controllers (BMC) can reach the cluster |

## Resources Required

| Resource | Minimum |
| -------- | ------- |
| Additional disk (mirror registry) | ~5 GiB for agent and discovery ISO images |
| Provisioning network | Layer 2 network for IPMI/Redfish BMC access |
| BareMetalHost CRD | Provided by the `multicluster-engine` operator |

## ImageSetConfiguration Section

Metal3 agent images are used during bare-metal host discovery and
provisioning.  These images are pulled by the assisted-installer service
and the Ironic provisioning agents.

The `bare-metal-fulfillment-operator` image is already included in the
core components (Category 1B).  The additional Metal3-specific images
depend on the MCE version and are typically mirrored as part of the MCE
operator catalog.

### Image Reference

The following Metal3 agent images may be required (exact images depend on
the MCE version; version tags are specified in the ImageSetConfiguration
below):

| Image | Registry | Purpose |
|-------|----------|---------|
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
> Category 1A, the MCE catalog includes its operand images.  Explicit
> `additionalImages` entries are only needed for images not bundled in the
> catalog (e.g., custom agent images or OSAC-specific overrides).

### Post-OSAC-4558: OCI Reference Approach

After OSAC-4558 is implemented, BMaaS provisioning images will be
referenced as OCI artifacts instead of raw HTTP downloads.  This will
enable them to be mirrored as standard `additionalImages` entries and
resolved via IDMS.

```yaml
  # Post-OSAC-4558 approach (future):
  additionalImages:
    - name: <registry>/osac-project/bmaas-agent-image:<version>
    # OCI-packaged discovery ISO and ramdisk images
```

## Execution Steps

### Step 1 — Verify MCE operator images are mirrored

The `multicluster-engine` operator catalog, mirrored in Category 1A,
includes most Metal3 agent images.  Verify they are present:

```bash
# Check that MCE-related images are in the mirror
oc get idms -o yaml | grep -i "multicluster-engine\|assisted"
```

### Step 2 — Add any additional agent images (if needed)

If specific agent images are missing from the MCE catalog mirror, add them
to the unified `ImageSetConfiguration` and re-run `oc-mirror`:

```bash
oc-mirror --v2 --config=imageset-config.yaml --workspace \
  file://$(pwd)/oc-mirror-workspace docker://<mirror-registry>:8443/
```

### Step 3 — Apply IDMS (disconnected cluster)

Metal3 agent images are typically referenced by digest, so they require
an **ImageDigestMirrorSet** (IDMS):

```bash
oc apply -f oc-mirror-workspace/working-dir/cluster-resources/idms-oc-mirror.yaml
```

### Step 4 — Configure AgentServiceConfig for disconnected

The `AgentServiceConfig` CR controls where the assisted-service looks for
OS images.  In disconnected environments, point it to mirrored images:

```bash
oc get agentserviceconfig agent -o yaml
```

Verify the `osImages` section references images accessible from the mirror
registry or from a local HTTP server on the provisioning network.

### Step 5 — Configure BareMetalHost resources

Create `BareMetalHost` resources with BMC credentials:

```yaml
apiVersion: metal3.io/v1alpha1
kind: BareMetalHost
metadata:
  name: worker-0
  namespace: osac
spec:
  online: true
  bootMACAddress: "AA:BB:CC:DD:EE:FF"
  bmc:
    address: idrac-virtualmedia+https://192.168.1.10/redfish/v1/Systems/System.Embedded.1
    credentialsName: worker-0-bmc-secret
    disableCertificateVerification: true
```

## Verification (BDD Scenarios)

```gherkin
Feature: BMaaS image mirroring for disconnected OSAC

  Background:
    Given a disconnected OpenShift 4.22 cluster with BMaaS enabled
    And the multicluster-engine operator is installed and healthy
    And Metal3 agent images are available in the mirror registry
    And IDMS resources include Metal3 image mirror entries

  Scenario: Metal3 agent images resolve via IDMS
    When the assisted-service requests an agent image
    Then the image reference resolves via IDMS to the mirror registry
    And the image pull succeeds

  Scenario: BareMetalHost discovery succeeds in disconnected mode
    Given a BareMetalHost CR is created with valid BMC credentials
    When the Metal3 provisioning agent boots the host
    Then the host transitions to "inspecting" state
    And the host eventually reaches "available" state

  Scenario: BareMetalHost provisioning completes
    Given a BareMetalHost is in "available" state
    When OSAC requests provisioning of the bare-metal host
    Then the bare-metal-fulfillment-operator creates the provisioning request
    And the host transitions through "provisioning" to "provisioned"
    And the host is registered in OSAC inventory

  Scenario: Discovery ISO boots without external registry access
    Given the AgentServiceConfig references mirrored OS images
    When a new bare-metal host boots from the discovery ISO
    Then the discovery agent registers with the assisted-service
    And no external registry access is attempted

  Scenario: Post-OSAC-4558 OCI references resolve via IDMS
    Given OSAC-4558 is implemented (future)
    When provisioning images are referenced as OCI artifacts
    Then the images resolve via IDMS to the mirror registry
    And provisioning completes without HTTP download fallback
```

## Validation Commands

```bash
# 1. Verify MCE operator is healthy
oc get csv -n multicluster-engine | grep multicluster

# 2. Verify Metal3 pods are running
oc get pods -n multicluster-engine -l app.kubernetes.io/part-of=metal3

# 3. Verify assisted-service is running
oc get pods -n multicluster-engine -l app=assisted-service

# 4. Check IDMS entries for MCE/Metal3 images
oc get idms -o yaml | grep -c "multicluster-engine"

# 5. Verify BareMetalHost status
oc get baremetalhost -A -o custom-columns=\
'NAME:.metadata.name,STATE:.status.provisioning.state,ERROR:.status.errorMessage'

# 6. Check AgentServiceConfig OS images
oc get agentserviceconfig agent -o jsonpath='{.spec.osImages[*].url}'

# 7. Verify bare-metal-fulfillment-operator is running
oc get pods -n osac -l app.kubernetes.io/name=bare-metal-fulfillment-operator
```

## Troubleshooting

| Symptom | Likely cause | Resolution |
| ------- | ------------ | ---------- |
| BareMetalHost stuck in `registering` | Agent images not mirrored or IDMS missing | Check IDMS includes MCE images; verify agent pod logs |
| Discovery ISO fails to boot | OS image URL not reachable from provisioning network | Verify `AgentServiceConfig` `osImages` URLs are accessible |
| `ImagePullBackOff` on assisted-service pods | MCE operator images not fully mirrored | Re-run `oc-mirror` with the MCE catalog; check catalog completeness |
| BareMetalHost `inspection error` | Ironic inspector cannot reach the host via BMC | Verify BMC credentials and network connectivity |
| `bare-metal-fulfillment-operator` CrashLoop | Missing secrets or OSAC platform not fully deployed | Ensure Phase 2 (osac chart) is complete; check operator logs |

## Known Gaps / Limitations

1. **OSAC-4558: OCI reference approach** — Currently, some Metal3
   provisioning images (discovery ISO, ramdisk) are distributed as HTTP
   downloads rather than OCI artifacts.  OSAC-4558 tracks the migration
   to OCI references, which will enable standard `additionalImages`
   mirroring.  Until then, these images may require a local HTTP server
   on the provisioning network.

2. **MCE catalog completeness** — The `multicluster-engine` operator
   catalog includes most Metal3 agent images, but the exact set depends
   on the MCE version.  Verify that all required agent images are present
   in the mirror after running `oc-mirror`.

3. **Provisioning network requirements** — BMaaS requires a Layer 2
   provisioning network for BMC (IPMI/Redfish) access.  This network
   must be configured independently of the disconnected mirroring setup.

4. **Agent image versioning** — Metal3 agent images are versioned with
   the MCE release.  Ensure the mirrored MCE version matches the
   installed operator version to avoid version skew.
