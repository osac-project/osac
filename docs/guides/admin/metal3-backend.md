# Metal3 Backend Configuration Guide

This guide walks through configuring the bare-metal-fulfillment-operator to use
Metal3 (BareMetalHost) as the inventory and management backend. It covers
prerequisites, host preparation, Helm chart configuration, and AAP template
configuration.

## Contents

- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Preparing BareMetalHost resources](#preparing-baremetalhost-resources)
- [Enabling Metal3 in the Helm chart](#enabling-metal3-in-the-helm-chart)
- [AAP template configuration](#aap-template-configuration)
- [End-to-end installation walkthrough](#end-to-end-installation-walkthrough)


---

## Overview

The bare-metal-fulfillment-operator supports pluggable backends for host
inventory and management. The Metal3 backend uses BareMetalHost custom resources
(from the [Metal3](https://metal3.io/) BareMetalOperator) as the inventory
source and power management interface.

Use the Metal3 backend when:

- Your bare metal hosts are already managed by the OpenShift BareMetalOperator
- BareMetalHost resources exist in the cluster
- No external provisioning system exists

The operator assigns available BareMetalHost resources to BareMetalInstance
requests, and controls power state through the BareMetalHost API. An AAP template
role handles image provisioning and deprovisioning.

---

## Prerequisites

Before configuring the Metal3 backend, verify the following:

1. **The `baremetal` cluster capability is enabled.** The Cluster Baremetal
   Operator (CBO) and the BareMetalOperator (BMO) are deployed by this
   capability. It is enabled by default, but cluster administrators can disable
   it during installation. If disabled, the cluster cannot provision or manage
   bare-metal nodes. See [Viewing the cluster capabilities](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/installation_overview/cluster-capabilities#viewing-cluster-capabilities_cluster-capabilities)
   and [Enabling additional enabled capabilities](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/installation_overview/cluster-capabilities#enabling-additional-enabled-capabilities_cluster-capabilities)
   in the OpenShift documentation.

   Verify the capability is enabled:

   ```bash
   oc get clusterversion version \
     -o jsonpath='{.status.capabilities.enabledCapabilities}' | grep -q baremetal \
     && echo "enabled" || echo "disabled"
   ```

2. **A Provisioning CR exists with `watchAllNamespaces` enabled.** On
   installer-provisioned infrastructure (IPI) clusters with `platform: baremetal`,
   the Provisioning CR is created automatically. On other installation types, you
   must create it manually. See [Configuring a provisioning resource](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/installing_on_bare_metal/scaling-a-user-provisioned-cluster-with-the-bare-metal-operator#configuring-a-provisioning-resource-to-scale-user-provisioned-clusters_scaling-a-user-provisioned-cluster-with-the-bare-metal-operator)
   in the OpenShift documentation.

   By default, the BareMetalOperator only watches BareMetalHost resources in the
   `openshift-machine-api` namespace. OSAC manages hosts in a separate namespace,
   so `watchAllNamespaces` must be enabled.

   If the Provisioning CR does not exist, create it with `watchAllNamespaces`
   already enabled:

   ```yaml
   apiVersion: metal3.io/v1alpha1
   kind: Provisioning
   metadata:
     name: provisioning-configuration
   spec:
     provisioningNetwork: "Disabled"
     watchAllNamespaces: true
   ```

   If the Provisioning CR already exists, patch it:

   ```bash
   oc patch provisioning provisioning-configuration \
     --type merge -p '{"spec":{"watchAllNamespaces": true}}'
   ```

   Verify the setting:

   ```bash
   oc get provisioning provisioning-configuration \
     -o jsonpath='{.spec.watchAllNamespaces}{"\n"}'
   ```

3. **BareMetalHost resources are available** in the target namespace. Hosts must
   be enrolled with BMC credentials and reach the `available` provisioning state
   before OSAC can assign them. See [Creating a BMC secret](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/installing_on_bare_metal/bare-metal-using-bare-metal-as-a-service#bmo-creating-a-bmc-secret_bare-metal-using-bmaas)
   and [Creating a bare-metal host resource](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/installing_on_bare_metal/bare-metal-using-bare-metal-as-a-service#bmo-creating-a-bare-metal-host-resource_bare-metal-using-bmaas)
   in the OpenShift documentation.

   ```bash
   oc get baremetalhost -n <namespace>
   ```

---

## Preparing BareMetalHost resources

OSAC uses labels on BareMetalHost resources to match hosts to BareMetalInstance
requests. Before hosts can be discovered by the operator, an administrator must
apply the required labels.

### Admin-managed labels

#### `osac.openshift.io/host-type`

**Required.** This label identifies the type of hardware (for example, `fc430`,
`gpu-h100`). When a BareMetalInstance request specifies a `hostType`, the
operator filters BareMetalHost resources by this label.

Apply the label to each host:

```bash
oc label baremetalhost <host-name> -n <namespace> \
  osac.openshift.io/host-type=<host-type>
```

For example, to label a host as a GPU node:

```bash
oc label baremetalhost worker-0 -n osac-host-inventory \
  osac.openshift.io/host-type=gpu-h100
```

### System-managed labels and fields

The following are set automatically by the operator during host assignment. Do
not modify them manually.

| Field | Type | Purpose |
|-------|------|---------|
| `osac.openshift.io/managed-by` | Label | Used by profiles to track host provisioning state. Hosts without this label are treated as `managed-by=baremetal`. Do not set manually — it is managed by profile configuration. |
| `osac.openshift.io/pool-id` | Label | Identifies the BareMetalPool that owns the host |
| `spec.consumerRef` | Spec field | Marks the host as claimed by a BareMetalInstance (prevents double-booking by the BareMetalOperator) |

### Host eligibility

A BareMetalHost is eligible for assignment when all of the following conditions
are met:

| Condition | Required value |
|-----------|---------------|
| `status.operationalStatus` | `OK` |
| `status.provisioning.state` | `available` |
| `spec.consumerRef` | Not set (`nil`) |
| `osac.openshift.io/host-type` | Matches the requested `hostType` (if specified) |
| `osac.openshift.io/managed-by` | Defaults to `baremetal` if absent; must match the profile's `hostSelector` (also defaults to `baremetal`) |

To check the state of hosts in the Metal3 namespace:

```bash
oc get baremetalhost -n <namespace> \
  -o custom-columns=NAME:.metadata.name,STATUS:.status.operationalStatus,STATE:.status.provisioning.state,HOST-TYPE:.metadata.labels.osac\\.openshift\\.io/host-type
```

---

## Enabling Metal3 in the Helm chart

The Metal3 backend is enabled through Helm chart values during OSAC installation.
The Helm chart automatically generates the inventory and management Secrets that
the operator needs.

### Helm values

Set the following values in your `values.yaml` override file or pass them with
`--set` during installation:

```yaml
bmf:
  metal3:
    enabled: true
    namespace: "osac-host-inventory"
    hostClass: "metal3"
```

| Value | Description | Default |
|-------|-------------|---------|
| `bmf.metal3.enabled` | Enable the Metal3 backend. Generates inventory and management Secrets automatically. | `false` |
| `bmf.metal3.namespace` | The Kubernetes namespace where BareMetalHost resources are located. **Required** when `enabled` is `true`. | `""` |
| `bmf.metal3.hostClass` | The host class value assigned to discovered hosts. Must match the value used in AAP template configuration. | `"metal3"` |

### Mutual exclusivity

The Metal3 and BCM backends are mutually exclusive. The Helm chart rejects an
installation where both `bmf.metal3.enabled` and `bmf.bcm.enabled` are `true`.

### Example

Add the Metal3 values to your environment-specific values file:

```yaml
# values/<profile>/instance.yaml
bmf:
  metal3:
    enabled: true
    namespace: "osac-host-inventory"
```

Then install using the standard `make` target:

```bash
make install-osac PLATFORM=openshift PROFILE=<profile> NS=osac
```

Or pass the values directly via `EXTRA_HELM_ARGS`:

```bash
make install-osac PLATFORM=openshift PROFILE=<profile> NS=osac \
  EXTRA_HELM_ARGS="--set bmf.metal3.enabled=true --set bmf.metal3.namespace=osac-host-inventory"
```

After installation, verify the Secrets were created:

```bash
oc get secret osac-inventory-config osac-management-config -n osac
```

---

## AAP template configuration

The bare-metal-fulfillment-operator delegates image provisioning and
deprovisioning to Ansible Automation Platform (AAP) via job templates. When a
BareMetalInstance is created or deleted, the operator triggers the appropriate
AAP job template, which runs an Ansible role to provision or deprovision the
BareMetalHost.

### How provisioning works

1. The operator creates a BareMetalInstance and triggers the AAP job template
   `osac-create-bare-metal-instance`
2. AAP runs `playbook_osac_create_bare_metal_instance.yml`, which includes the
   role specified by the instance's `templateID` (typically
   `osac.templates.bm_host_provisioning`)
3. The `bm_host_provisioning` role reads the instance's `hostClass` and
   dispatches to the backend-specific provisioning tasks — for Metal3, this is
   `create_metal3.yaml`
4. The Metal3 provisioning tasks:
   - Parse the BareMetalHost namespace and name from `externalHostID`
   - Apply image, SSH key, userData, and networkData configuration to the
     BareMetalHost
   - Wait for the BareMetalHost to reach the `provisioned` state (up to 60
     minutes)

### Template parameters

The `bm_host_provisioning` role accepts the following template parameters. These
are set on the BareMetalInstance's `spec.templateParameters` field as a JSON
string:

| Parameter | Required | Default | Description |
|-----------|----------|---------|-------------|
| `imageURL` | No | `oci://quay.io/osac-project/fedora-cloud-bmi:44` | OS image URL to provision on the host. Supports OCI (`oci://`) and HTTP(S) URLs. |
| `imageChecksum` | Conditional | — | Checksum of the image. Required for non-OCI images. |
| `checksumType` | Conditional | — | Checksum algorithm (e.g., `sha256`). Required when `imageChecksum` is set. |
| `imageSourceType` | No | — | Set to `registry` for OCI images to skip checksum validation. |
| `sshPublicKey` | No | — | SSH public key to inject into the host for remote access. |
| `userDataSecret` | No | — | Name of a Kubernetes Secret in the BareMetalInstance namespace containing cloud-init userdata. |
| `networkData` | No | — | Network configuration data (cloud-init network config format). |

### Deprovisioning

When a BareMetalInstance is deleted, the operator triggers the AAP job template
`osac-delete-bare-metal-instance`, which runs the `bm_host_provisioning` role's
delete tasks. For Metal3, the deprovisioning process:

1. Removes the image, userData, metaData, and networkData from the
   BareMetalHost
2. Waits for the BareMetalHost to return to the `available` state (up to 60
   minutes)
3. Cleans up the provisioning Secrets created during provisioning

---

## End-to-end installation walkthrough

This section ties together the prerequisites, configuration, and verification
steps for a complete Metal3 backend deployment.

### Step 1: Verify the baremetal cluster capability

Confirm the `baremetal` capability is enabled on the cluster. See
[Prerequisites](#prerequisites) §1 for details.

```bash
oc get clusterversion version \
  -o jsonpath='{.status.capabilities.enabledCapabilities}' | grep -q baremetal \
  && echo "enabled" || echo "disabled"
```

### Step 2: Configure the Provisioning CR

The Provisioning CR must have `watchAllNamespaces: true` so BMO watches hosts
outside `openshift-machine-api`. This is a cluster-wide admin action. See
[Prerequisites](#prerequisites) §2 for details.

```bash
# Create if it doesn't exist, or patch if it does:
oc patch provisioning provisioning-configuration \
  --type merge -p '{"spec":{"watchAllNamespaces": true}}'

# Verify:
oc get provisioning provisioning-configuration \
  -o jsonpath='{.spec.watchAllNamespaces}{"\n"}'
```

### Step 3: Prepare the BMH namespace and enroll hosts

Create a namespace for BareMetalHost resources and enroll hosts with BMC
credentials. See [Prerequisites](#prerequisites) §3 and the OpenShift
documentation links there.

```bash
oc create namespace osac-host-inventory

# Verify hosts are enrolled and available:
oc get baremetalhost -n osac-host-inventory
```

### Step 4: Label BareMetalHost resources

Apply the required `osac.openshift.io/host-type` label to each host. See
[Preparing BareMetalHost resources](#preparing-baremetalhost-resources) for
details.

```bash
oc label baremetalhost <host-name> -n osac-host-inventory \
  osac.openshift.io/host-type=<host-type>
```

Verify the labels and host state:

```bash
oc get baremetalhost -n osac-host-inventory \
  -o custom-columns=NAME:.metadata.name,STATUS:.status.operationalStatus,STATE:.status.provisioning.state,HOST-TYPE:.metadata.labels.osac\\.openshift\\.io/host-type
```

### Step 5: Install OSAC with Metal3 enabled

Install OSAC using the Helm installer with the Metal3 backend enabled. See
[Enabling Metal3 in the Helm chart](#enabling-metal3-in-the-helm-chart) for
the chart values and the
[Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md)
for the full installation procedure.

```bash
make install PLATFORM=openshift PROFILE=<profile> NS=osac \
  EXTRA_HELM_ARGS="--set bmf.metal3.enabled=true --set bmf.metal3.namespace=osac-host-inventory"
```

### Step 6: Verify the deployment

Confirm the operator is running and the Metal3 configuration Secrets were
created:

```bash
# Operator pod is running:
oc get pods -n osac -l app.kubernetes.io/name=bare-metal-fulfillment-operator

# Inventory and management Secrets exist:
oc get secret osac-inventory-config osac-management-config -n osac
```

### Step 7: Create a BareMetalInstance and verify provisioning

Use the `osac` CLI to create a bare metal instance. The `--catalog-item` flag
is required and selects the instance type (hardware profile):

```bash
osac create baremetalinstance \
  --name my-instance \
  --catalog-item <catalog-item-name> \
  --ssh-key "ssh-ed25519 AAAA..."
```

Monitor the instance until it reaches the `Ready` state:

```bash
osac get baremetalinstances
osac describe baremetalinstance my-instance
```

On the cluster, verify the corresponding BareMetalHost was provisioned:

```bash
oc get baremetalhost -n osac-host-inventory \
  -o custom-columns=NAME:.metadata.name,STATE:.status.provisioning.state
```

The BareMetalInstance should progress through `Allocating` → `Progressing` →
`Ready`, and the corresponding BareMetalHost should reach the `provisioned`
state.

---

## References

- [Bare Metal Fulfillment Enhancement Proposal](https://github.com/osac-project/enhancement-proposals/tree/main/enhancements/bare-metal-fulfillment)
- [AAP Provisioning Architecture](../../architecture/aap-provisioning/)
- [Helm Deployment Guide](https://github.com/osac-project/osac/blob/main/docs/guides/installation/helm-deployment-guide.md) — full OSAC installation procedure
- [bare-metal-fulfillment-operator Helm chart](https://github.com/osac-project/osac/tree/main/bare-metal-fulfillment-operator/charts/operator) — chart values and templates
- [bm_host_provisioning role](https://github.com/osac-project/osac/tree/main/osac-aap/collections/ansible_collections/osac/templates/roles/bm_host_provisioning) — AAP provisioning role source
- [Metal3 Project](https://metal3.io/)
- [BareMetalHost API Reference](https://github.com/metal3-io/baremetal-operator/blob/main/docs/api.md)
- [OpenShift: Creating a BMC secret](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/installing_on_bare_metal/bare-metal-using-bare-metal-as-a-service#bmo-creating-a-bmc-secret_bare-metal-using-bmaas) — BMC credential setup for BareMetalHost resources
- [OpenShift: Creating a bare-metal host resource](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/installing_on_bare_metal/bare-metal-using-bare-metal-as-a-service#bmo-creating-a-bare-metal-host-resource_bare-metal-using-bmaas) — enrolling BareMetalHost resources
- [OpenShift: Cluster capabilities](https://docs.redhat.com/en/documentation/openshift_container_platform/4.22/html/installation_overview/cluster-capabilities) — viewing and enabling the `baremetal` capability
