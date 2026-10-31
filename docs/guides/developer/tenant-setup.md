# Tenant Setup Guide

This guide walks through the steps required to set up a Tenant and create a ComputeInstance
on an OSAC management cluster. It is intended for developers working on or testing the
OSAC operator.

## Contents

- [Concepts](#concepts)
- [First-time setup](#first-time-setup)
  - [Step 3a: Dedicated tenant StorageClass](#step-3a-dedicated-tenant-storageclass-target-cluster)
  - [Step 3b: Shared default StorageClass (optional)](#step-3b-shared-default-storageclass-optional-target-cluster)
- [Returning users: what has changed](#returning-users-what-has-changed)
- [Creating a ComputeInstance](#creating-a-computeinstance)
- [Troubleshooting](#troubleshooting)

---

## Concepts

### Two-cluster model

OSAC distinguishes between two cluster roles:

- **Management cluster**: where the OSAC operator and its CRDs run. Tenant CRs and
  ComputeInstance CRs are created here.
- **Target cluster**: where VMs and their associated resources (namespaces, PVCs,
  StorageClasses) actually run.

Management and target clusters may be the same cluster or different clusters. When the
operator is configured with `OSAC_REMOTE_CLUSTER_KUBECONFIG`, it uses that kubeconfig to
reach the target cluster. When that variable is not set, the operator uses the cluster it is
running on as both management and target. A remote target cluster is supported only with
provider networking disabled (`global.networking.provisioningEnabled=false`): the network
managers work on the management cluster, so the operator refuses to start with both.

### Operator namespace

The OSAC operator uses the Kubernetes Downward API to read its own namespace at startup.
Several `OSAC_*_NAMESPACE` variables, including `OSAC_TENANT_NAMESPACE` and
`OSAC_COMPUTE_INSTANCE_NAMESPACE`, are set this way rather than as explicit string values in
the deployment spec. If you inspect the deployment and see `valueFrom.fieldRef.fieldPath:
metadata.namespace` instead of a literal value, that is expected: the pod resolves it to its
own namespace at runtime.

To find your operator namespace:

```bash
oc get pods -A -l control-plane=controller-manager
```

The rest of this guide uses `osac-devel` as a concrete example. Replace it with your actual
operator namespace if it differs.

### What the namespace variables control

- `OSAC_TENANT_NAMESPACE`: the namespace on the management cluster where the operator watches
  for Tenant CRs. Tenant CRs must be created here.
- `OSAC_COMPUTE_INSTANCE_NAMESPACE`: the namespace on the management cluster where the
  operator watches for ComputeInstance CRs. ComputeInstance CRs must be created here.

Both resolve to the operator's own namespace by default. The tenant namespace on the target
cluster (where VMs actually run) is a separate concept: it is determined by the Tenant CR
name, not by these variables.

### Tenant CR and namespace coupling

A Tenant CR on the management cluster is coupled by name to a namespace on the target cluster.
If the Tenant CR is named `my-tenant`, the operator expects a namespace called `my-tenant` to
exist on the target cluster. The names must match exactly.

### Tenant Ready condition

A Tenant CR reaches `Ready` only when the following are satisfied on the **target** cluster:

1. **Namespace**: a namespace with the same name as the Tenant CR exists.
2. **StorageClass** (priority order — the operator uses the first match that is unambiguous):
   - **Dedicated**: exactly one StorageClass labeled `osac.openshift.io/tenant: <tenant-name>`
     (for example `osac.openshift.io/tenant: my-tenant` when the Tenant CR is `my-tenant`).
   - **Shared fallback**: if no dedicated StorageClass exists, exactly **one** StorageClass
     labeled `osac.openshift.io/tenant: Default` (capital **D**). This value is a sentinel:
     it is a valid Kubernetes label value but cannot collide with a Tenant name, because
     `Default` is not a valid resource name.
   - If neither case applies, or more than one StorageClass matches the chosen label key,
     the Tenant stays in `Progressing`.

The Tenant `status.storageClass` field is set to the name of the resolved StorageClass.

The Tenant also reports **Kubernetes-style status conditions** (for example `NamespaceReady`
and `StorageClassReady`). Useful `StorageClassReady` reasons include:

| Reason | Meaning |
|--------|---------|
| `Found` | A single tenant-specific StorageClass was selected |
| `SharedDefault` | No tenant-specific class; a single shared `Default`-labeled class was selected |
| `NotFound` | No suitable StorageClass (missing dedicated and shared, or namespace issue) |
| `MultipleFound` | More than one StorageClass has `osac.openshift.io/tenant: <tenant-name>` |
| `MultipleDefaultsFound` | More than one StorageClass has `osac.openshift.io/tenant: Default` |

To inspect conditions:

```bash
oc get tenant my-tenant -n osac-devel \
  -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.reason}{"\n"}{end}'
```

The same StorageClass priority is applied when Ansible provisions workloads: if there is no
tenant-specific class, automation falls back to the shared `Default` class and emits a
**warning** that storage is not isolated to that tenant. If you need isolation, create a
dedicated labeled StorageClass per tenant.

---

## First-time setup

### Prerequisites

- `oc` CLI configured and logged in to the management cluster.
- If management and target clusters are separate: a second `oc` context (or separate terminal)
  pointed at the target cluster.
- All OSAC CRDs are applied. The networking CRDs (`SecurityGroup`, `Subnet`, `VirtualNetwork`)
  must exist before the operator can start. Verify:

  ```bash
  oc get crd | grep osac.openshift.io
  ```

  Expected CRDs include (at minimum):

  ```
  computeinstances.osac.openshift.io
  tenants.osac.openshift.io
  securitygroups.osac.openshift.io
  subnets.osac.openshift.io
  virtualnetworks.osac.openshift.io
  ```

  If any are missing, apply them from the operator repository before starting the operator:

  ```bash
  oc apply -f config/crd/bases/
  ```

- The OSAC operator is deployed and running:

  ```bash
  oc get pods -n osac-devel -l control-plane=controller-manager
  ```

> **Upgrading from cloudkit-operator?** If your cluster previously ran cloudkit-operator,
> complete the [Migrating from cloudkit-operator](#migrating-from-cloudkit-operator) steps
> before proceeding. The leftover `tenants.cloudkit.openshift.io` CRD will cause `oc get tenant`
> to resolve to the wrong resource throughout this guide.

---

### Step 1: Create the tenant namespace (target cluster)

The namespace name must exactly match the name you will give the Tenant CR in the next step.

```bash
oc create namespace my-tenant
```

Verify:

```bash
oc get namespace my-tenant
```

---

### Step 2: Create the Tenant CR (management cluster)

The Tenant CR goes in the operator namespace (`OSAC_TENANT_NAMESPACE`, which defaults to the
operator's own namespace). A minimal spec is sufficient:

```yaml
apiVersion: osac.openshift.io/v1alpha1
kind: Tenant
metadata:
  name: my-tenant
  namespace: osac-devel
spec: {}
```

Apply it:

```bash
oc apply -f tenant.yaml
```

After applying, the Tenant may be in `Progressing` until storage is resolved: either you
create a **dedicated** StorageClass ([Step 3a](#step-3a-dedicated-tenant-storageclass-target-cluster)),
or the cluster already provides a **shared** default ([Step 3b](#step-3b-shared-default-storageclass-optional-target-cluster)).

---

### Step 3a: Dedicated tenant StorageClass (target cluster)

Use this when you want storage **isolated** to your tenant. The operator prefers a
StorageClass labeled `osac.openshift.io/tenant: <tenant-name>`. There must be **exactly one**
such StorageClass for your tenant name.

The StorageClass provisioner and parameters depend on what storage is available on your
target cluster. The example below uses LVMS (topolvm):

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: my-tenant-default
  labels:
    osac.openshift.io/tenant: my-tenant
provisioner: topolvm.io
parameters:
  csi.storage.k8s.io/fstype: xfs
  topolvm.io/device-class: vg1
reclaimPolicy: Delete
allowVolumeExpansion: true
volumeBindingMode: WaitForFirstConsumer
```

To find the available LVMS device classes on your cluster:

```bash
oc get lvmcluster -n openshift-storage -o jsonpath='{.items[*].spec.storage.deviceClasses[*].name}'
```

Apply the StorageClass:

```bash
oc apply -f storageclass.yaml
```

---

### Step 3b: Shared default StorageClass (optional, target cluster)

If your environment does **not** require per-tenant storage isolation, a **CSP admin**
(platform operator with permissions to create cluster-wide `StorageClass` objects) can
install **one** shared StorageClass labeled `osac.openshift.io/tenant: Default`. Any tenant
that does not have its own dedicated class (Step 3a) will use this shared class, and the
Tenant can still become `Ready`.

Example (adjust `provisioner` and `parameters` for your platform):

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: osac-shared-default-sc
  labels:
    osac.openshift.io/tenant: Default
provisioner: topolvm.io
parameters:
  csi.storage.k8s.io/fstype: xfs
  topolvm.io/device-class: vg1
reclaimPolicy: Delete
allowVolumeExpansion: true
volumeBindingMode: WaitForFirstConsumer
```

There must be **at most one** StorageClass with `osac.openshift.io/tenant: Default`. If more
than one exists, the Tenant controller cannot choose a shared default and the Tenant stays
in `Progressing` (`StorageClassReady` reason `MultipleDefaultsFound`).

If you skip Step 3a and rely on Step 3b, you do not create a tenant-specific StorageClass
unless you later need isolation.

---

### Step 4: Verify the Tenant is Ready

Once the namespace and StorageClass resolution succeed, the Tenant should transition to `Ready`:

```bash
oc get tenant my-tenant -n osac-devel \
  -o jsonpath='{.status.phase}{"\n"}'
# Expected: Ready
```

For more detail:

```bash
oc get tenant my-tenant -n osac-devel -o yaml
```

When you use a **dedicated** StorageClass (Step 3a), `status` will look like:

```yaml
status:
  phase: Ready
  namespace: my-tenant
  storageClass: my-tenant-default
```

When you rely on the **shared** default (Step 3b only), `storageClass` is the shared class
name and `StorageClassReady` uses reason `SharedDefault`, for example:

```yaml
status:
  phase: Ready
  namespace: my-tenant
  storageClass: osac-shared-default-sc
  conditions:
    - type: NamespaceReady
      status: "True"
      reason: Found
    - type: StorageClassReady
      status: "True"
      reason: SharedDefault
```

If it stays `Progressing`, see [Troubleshooting](#troubleshooting).

---

## Returning users: what has changed

If you have worked with OSAC before, you may encounter differences from the setup you
remember. This section describes what has changed and what you need to do to bring an existing
environment up to date.

### Migrating from cloudkit-operator

The OSAC operator was previously named cloudkit-operator. On clusters that ran
cloudkit-operator before the rename, the `tenants.cloudkit.openshift.io` CRD may still be
present. In that case, `oc get tenant` resolves to the wrong CRD.

The clean fix is to delete the old CRD (since cloudkit-operator no longer exists):

```bash
oc delete crd tenants.cloudkit.openshift.io
```

This also deletes any existing `tenants.cloudkit.openshift.io` CRs, so verify there are none
you need to preserve first:

```bash
oc get tenants.cloudkit.openshift.io -A
```

If you cannot delete the CRD, use the fully qualified form for all OSAC tenant commands:

```bash
oc get tenants.osac.openshift.io -n osac-devel
```

---

### Tenant CRs are no longer auto-created

In earlier versions of the operator, Tenant CRs were created automatically. They are now
managed manually by the developer or admin. If you find a Tenant CR that was auto-created
and is in an unexpected state, delete it and follow [Step 2](#step-2-create-the-tenant-cr-management-cluster)
to create it fresh.

```bash
oc delete tenant my-tenant -n osac-devel
```

### StorageClass resolution (dedicated or shared)

The Tenant controller resolves storage in this order:

1. Exactly one StorageClass with `osac.openshift.io/tenant: <your-tenant-name>`, or
2. If none, exactly one StorageClass with `osac.openshift.io/tenant: Default`.

You no longer **must** create a per-tenant StorageClass if your cluster already has a valid
shared default (see [Step 3b](#step-3b-shared-default-storageclass-optional-target-cluster)).
You still must not have **multiple** StorageClasses with the same tenant label value for the
same resolution path (duplicate dedicated labels for your tenant, or duplicate `Default`
labels cluster-wide).

If a Tenant is stuck in `Progressing` after upgrading the operator, check both paths:

```bash
oc get sc -l osac.openshift.io/tenant=my-tenant
oc get sc -l osac.openshift.io/tenant=Default
```

- If the first command returns **one** SC, you are using a dedicated class.
- If the first returns **none** and the second returns **one** SC, you are using the shared
  fallback.
- If either query returns **more than one** SC, remove or relabel until only one remains for
  that label value.

To create a dedicated class, follow [Step 3a](#step-3a-dedicated-tenant-storageclass-target-cluster).

### Networking CRDs must be applied when upgrading

The networking CRDs (`VirtualNetwork`, `Subnet`, `SecurityGroup`, `NetworkClass`,
`NetworkAttachment`, and the external IP, NAT gateway, and FabricDomain CRDs) are part of
the OSAC operator. If you are upgrading an existing deployment that
predates this work, these CRDs will not exist on your cluster and the operator will fail to
start because it cannot set up watches for these resource types.

Apply the CRDs before (re)starting the operator:

```bash
oc apply -f config/crd/bases/
```

Also update the operator's ClusterRole to include the new permissions:

```bash
oc apply -f config/rbac/role.yaml
```

After applying CRDs and RBAC, restart the operator:

```bash
oc rollout restart deployment osac-operator-controller-manager -n osac-devel
```

Verify all controllers start:

```bash
oc logs -l control-plane=controller-manager -n osac-devel --tail=50 | grep "Starting Controller"
```

> **Note:** With provider networking enabled (`global.networking.provisioningEnabled`),
> every ComputeInstance attaches to a Subnet. Through the fulfillment API, a ComputeInstance
> without a network attachment gets the tenant's default networking. A ComputeInstance CR
> applied directly must carry the `osac.openshift.io/compute-instance-id` label and
> `spec.networkAttachments` with a Subnet and SecurityGroups, or its `NetworkReady`
> condition fails. See the [Networking Guide](networking-guide.md).

---

## Creating a ComputeInstance

Once the Tenant is `Ready`, you can create a ComputeInstance. The CR goes in the operator
namespace (`OSAC_COMPUTE_INSTANCE_NAMESPACE`, which defaults to the operator's own namespace).
This is distinct from the tenant namespace on the target cluster: the ComputeInstance CR is
a management-plane object, and the operator uses it to provision the actual VM in the tenant
namespace.

The `osac.openshift.io/tenant` annotation must match the Tenant CR name exactly.

When using **osac** or other tooling that authenticates with a **service account
token**, the effective tenant name used during provisioning (for example
`spec.tenantReference.name` on the ComputeInstance) must match the Tenant CR you expect.
A token from the **tenant namespace** typically yields that tenant name; a token from the
**operator namespace** may yield a different name and therefore a different StorageClass
lookup (dedicated label vs shared `Default`). If provisioning fails with storage errors,
verify which tenant name the API request is using and that StorageClasses exist for that
resolution path.

```yaml
apiVersion: osac.openshift.io/v1alpha1
kind: ComputeInstance
metadata:
  name: my-vm
  namespace: osac-devel
  annotations:
    osac.openshift.io/tenant: my-tenant
spec:
  templateID: osac.templates.ocp_virt_vm
  templateParameters: '{}'
  cores: 2
  memoryGiB: 4
  bootDisk:
    sizeGiB: 20
  image:
    sourceType: registry
    sourceRef: quay.io/containerdisks/fedora:latest
  runStrategy: Always
```

> **Note:** The `namespace` field in the metadata is optional if you pass `-n osac-devel` to
> `oc apply`. Including it in the manifest is recommended to avoid accidental creation in the
> wrong namespace.

Apply and watch:

```bash
oc apply -f computeinstance.yaml
oc get computeinstance my-vm -n osac-devel -w
```

`DiskSpec` (used for `bootDisk` and `additionalDisks`) only carries disk size; there is no
`storageClass` field on the ComputeInstance spec. Provisioning always resolves the
StorageClass automatically using the same dedicated-then-shared label priority as the Tenant
controller (the `tenant_storage_class` Ansible role queries tenant-specific labels first,
then the shared `Default` label).

---

## Troubleshooting

### Tenant stays in Progressing

Check prerequisites on the **target** cluster:

```bash
# Namespace exists
oc get namespace my-tenant

# Dedicated path: at most one SC for this tenant name
oc get sc -l osac.openshift.io/tenant=my-tenant

# Shared path: at most one SC with the Default sentinel (only relevant if dedicated is empty)
oc get sc -l osac.openshift.io/tenant=Default
```

Inspect `status.conditions` on the Tenant (see [Tenant Ready condition](#tenant-ready-condition)):

```bash
oc get tenant my-tenant -n osac-devel -o yaml
```

Check the operator logs for the reconcile message:

```bash
oc logs -l control-plane=controller-manager -n osac-devel --tail=50 | grep -i tenant
```

Common log messages:

| Message | Cause |
|---------|-------|
| `namespace not found` | The tenant namespace does not exist on the target cluster |
| `no StorageClass found` / `NotFound` | Neither a dedicated SC for the tenant nor a single shared `Default` SC |
| `count: 2` (or higher) for tenant label | More than one StorageClass shares `osac.openshift.io/tenant: <tenant-name>` (`MultipleFound`) |
| Multiple `Default` SCs | More than one StorageClass has `osac.openshift.io/tenant: Default` (`MultipleDefaultsFound`) |

### Operator controllers do not start

If only some controllers start (check for `Starting Controller` log lines), the operator
may be missing RBAC permissions or CRDs for a resource type. This typically happens after
upgrading to a version that introduced new CRDs.

Diagnose:

```bash
oc logs -l control-plane=controller-manager -n osac-devel --tail=100 | grep -i "failed to watch\|forbidden"
```

Fix by applying the updated RBAC and restarting:

```bash
oc apply -f config/rbac/role.yaml
oc rollout restart deployment osac-operator-controller-manager -n osac-devel
```

### ComputeInstance stays in Starting

Check the operator logs for errors:

```bash
oc logs -l control-plane=controller-manager -n osac-devel --tail=100 | grep -i "computeinstance\|error"
```

Verify the Tenant is Ready:

```bash
oc get tenant my-tenant -n osac-devel \
  -o jsonpath='{.status.phase}{"\n"}'
```
