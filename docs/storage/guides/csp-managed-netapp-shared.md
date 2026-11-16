# CSP-managed NetApp with a shared storage tenant

> **Status: Dev Preview.** This guide covers the profile combining
> [CSP-managed ownership](../ownership/csp-managed.md), the
> [NetApp provider](../providers/netapp.md), and
> [shared tenancy](../tenancy/shared.md) on VMaaS.

In this profile, the Cloud Provider Admin (CSP) operates a NetApp storage
service and offers its tiers to tenants through OSAC. One shared SVM serves
all tenants; OSAC verifies the offered classes exist and maps tenant
workload disks to them. OSAC never modifies the CSP-owned storage
resources.

```
CSP admin (outside OSAC)                    CSP admin (in OSAC)
  prepare ONTAP SVM + pools         create StorageBackend (cspManaged)
  install + operate Trident           + StorageTiers
  configure shared TBC                        |
  create labeled StorageClasses                v
          |                            OSAC onboards tenants
          v                                     |
  OSAC readiness check <-----------------------+
  (one class per configured tier)
          |
          v
  StorageReady + resolved classes
          |
          v
  Tenant users create ComputeInstances,
  selecting a tier per disk
```

## Prerequisites

Complete these before onboarding tenants to the storage service:

**Storage side (outside OSAC)**

- [ ] A NetApp ONTAP cluster with a shared SVM serving the tenants
- [ ] Virtual storage pools on the SVM for each offered tier
- [ ] Transport between the VMaaS target hosts and the SVM (Fibre Channel
      or iSCSI for block), configured and validated by the CSP
- [ ] Trident CSI operator installed and operated on the VMaaS target
- [ ] Worker nodes on the VMaaS target prepared for the block transport:
      `multipathd` running with `find_multipaths no`. See
      [providers/netapp.md](../providers/netapp.md#worker-node-preparation)
- [ ] A shared TridentBackendConfig connecting Trident to the SVM
- [ ] One StorageClass per offered tier with the
      `osac.openshift.io/tenant=csp-managed` and
      `osac.openshift.io/storage-tier=<tier>` labels and a filesystem
      type (`fsType`) in its parameters. See
      [providers/netapp.md](../providers/netapp.md#storageclass-contract)

**OSAC side**

- [ ] The osac-operator is deployed with access to the VMaaS target cluster
      it should check. The operator queries its explicitly configured
      target and reports an error rather than falling back to the local
      cluster. (Deployment configuration for *remote* targets is in
      development; see the installation guide for current options.)
- [ ] No backend management credentials are needed: CSP-managed readiness
      does not depend on hub Secrets or provider automation.

### Verifying the prerequisites

The OSAC readiness check only verifies StorageClass presence. Before
onboarding the first tenant, confirm the storage side on the VMaaS target:

```shell
# Trident is registered
oc get csidriver

# The shared Trident backend is bound
oc get tridentbackendconfigs -A

# One class per offered tier; the last column is each class's tier label
oc get storageclass -l osac.openshift.io/tenant=csp-managed \
  -L osac.openshift.io/storage-tier

# Every class sets a filesystem type: no <none> in the FSTYPE column
oc get storageclass -l osac.openshift.io/tenant=csp-managed \
  -o custom-columns=NAME:.metadata.name,FSTYPE:.parameters.fsType

# Each worker that can run tenant VMs: multipathd is active and
# find_multipaths is "off" (how multipathd reports "no")
oc debug node/<node> -- chroot /host sh -c \
  'systemctl is-active multipathd; multipathd show config | grep -w find_multipaths'
```

OSAC lists the classes by the same tenant label during onboarding, then
resolves each configured tier by the `osac.openshift.io/storage-tier` label.
Each configured tier must appear exactly once in the last column. A class
whose tier label is missing or misspelled is listed but does not count for
its tier, and two classes with the same tier make that tier ambiguous.
Either one surfaces later as a `StorageReady=False` condition. See the
[resolution rules](../concepts/storageclass.md#resolution-rules).

The node check has no OSAC equivalent. An unprepared worker passes
onboarding and volume provisioning, and first fails when a tenant VM
attaches a disk there. See
[Worker node preparation](../providers/netapp.md#worker-node-preparation)
for the configuration and the failure messages.

The filesystem type check has no OSAC equivalent either. A class without
a filesystem type passes onboarding and provisions volumes, and VM disk
imports on it fail. A class that sets `csi.storage.k8s.io/fstype` instead
of `fsType` shows `<none>` in that check and needs no change. See
[Filesystem type](../providers/netapp.md#filesystem-type).

## Configure storage in OSAC

Register one backend for the NetApp cluster and one tier per offered pool:

```shell
# 1. Register the CSP-managed backend (endpoint/credentials optional)
osac create storagebackend --name netapp-shared \
  --provider netapp \
  --csp-managed

# 2. Create a tier per offered storage pool
osac create storagetier --name gold \
  --backend-id <backend-id> \
  --protocol BLOCK \
  --max-read-bandwidth-mbs 500 \
  --max-write-bandwidth-mbs 200

osac describe storagebackend netapp-shared   # note the backend ID
osac get storagetier
```

A NetApp backend must be CSP-managed: the API rejects the creation if
`--csp-managed` is omitted, since the default is `false`.

The exact StorageClass-to-tier contract the operator matches against:

```yaml
metadata:
  labels:
    osac.openshift.io/tenant: csp-managed
    osac.openshift.io/storage-tier: gold
```

## Onboard a tenant and verify readiness

Onboard the tenant through the normal OSAC flow. The operator then checks
the target cluster for exactly one matching class per configured tier and
latches the result:

```shell
# Conditions on the operator's Tenant resource (management cluster)
oc get tenant <tenant-name> -o jsonpath='{.status.conditions}'

# Resolved tier-to-class mapping
oc get tenant <tenant-name> -o jsonpath='{.status.storageClasses}'
# [{"name":"ontap-gold","tier":"gold"},{"name":"ontap-silver","tier":"silver"}]
```

`StorageReady=True` records that every configured tier resolved to
exactly one class when the milestone was set. It remains `True` when
tiers are added later, so inspect the current tier mapping before using
a newly added tier. See
[concepts/readiness.md](../concepts/readiness.md) for the full semantics.

Tenant users can then create ComputeInstances, selecting a tier for the
boot disk and each additional disk (see
[Storage tier selection](../../guides/developer/computeinstance-guide.md#storage-tier-selection)).

## Ownership boundaries

OSAC reads the CSP-provided classes and consumes their tier mapping. It
does **not**:

- Create, update, or delete the shared StorageClasses, the shared
  TridentBackendConfig, Trident, the SVM, or anything else on the storage
  system, including during tenant teardown.
- Use backend management credentials, even if they were supplied when the
  backend was registered.
- Validate the classes' parameters, the transport (Fibre Channel zoning,
  iSCSI networking, worker node multipath configuration), Trident health,
  or the TridentBackendConfig. Readiness is StorageClass presence only.

## Troubleshooting

| Symptom | Cause | Resolution |
|---|---|---|
| `StorageReady=False`, reason `NotFound`; message names a tier | The tier has no matching StorageClass on the target cluster | Create the class with both labels (`osac.openshift.io/tenant=csp-managed`, `osac.openshift.io/storage-tier=<tier>`). See the contract above |
| `StorageReady=False`, reason `MultipleFound`; message lists class names | More than one class matches a tier | Remove or relabel the duplicate so exactly one class matches |
| Readiness not evaluated; operator logs show target access errors | The operator cannot reach its configured target cluster | Check the operator's target access configuration; the operator fails closed and never queries the local cluster as a fallback. Remote-target deployment wiring is in development |
| A class exists but the tier still doesn't resolve | The class is labeled for a different tier, or the tier isn't configured in OSAC | Verify both label values; check `osac get storagetier` |
| Classes exist for tiers OSAC doesn't know about | Extra classes for unconfigured tiers are ignored by design | No action needed; they don't affect readiness |
| CSP-managed backend for a provider other than NetApp | Not supported in the Dev Preview | CSP-managed runtime support is NetApp-only; tiers from other CSP-managed providers are never dispatched to provider automation |
| `StorageReady=True`, but the pod mounting a tenant disk stays in `ContainerCreating`; its events show `multipathd is not running` or `unsupported find_multipaths` | The worker node is not prepared for the block transport | Prepare the node. See [Worker node preparation](../providers/netapp.md#worker-node-preparation) |
| `StorageReady=True`, but a VM disk import fails; its importer pod logs `Could not create '/data/disk.img': Permission denied` | The tier's StorageClass sets no filesystem type, so the volume is mounted with root ownership | Recreate the class with `fsType`, then recreate the ComputeInstance. See [Filesystem type](../providers/netapp.md#filesystem-type) |

Warning events on the Tenant (reason `DuplicateStorageClass`) repeat the
duplicate details:

```shell
oc describe tenant <tenant-name>
```

## Dev Preview caveats

- StorageBackend UI selection is in development; configure backends and
  tiers through the CLI or private API today.
- Hard-blocking ComputeInstance creation on completed onboarding, with a
  live check of the requested tier, is in development. Today, provisioning
  a disk whose tier has no resolved class fails with an actionable error.
- Volumes are managed as part of their ComputeInstance (created, tracked,
  and deleted with it); standalone volumes are not supported. See
  [volume lifecycle](volume-lifecycle.md).
