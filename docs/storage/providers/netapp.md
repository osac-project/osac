# NetApp

> **Status: Dev Preview (CSP-managed).** NetApp is the provider of the
> CSP-managed Dev Preview profile, and NetApp backends must be
> CSP-managed: the API rejects `cspManaged: false`, including when the
> field is omitted, since the default is `false`. OSAC-managed NetApp
> support is planned.

NetApp storage is integrated through ONTAP and the Trident CSI driver:

```
NetApp ONTAP cluster
  └── storage virtual machine (SVM)
        └── virtual storage pools  <->  OSAC StorageTiers
                    |
                    v
Trident CSI driver (on the VMaaS target cluster)
  ├── backend config (TridentBackendConfig) pointing at the SVM
  └── StorageClasses labeled per the OSAC contract
```

## Requirements

Preparing NetApp storage for OSAC involves:

1. **ONTAP cluster** with a storage virtual machine (SVM) serving the tenants
   that will consume storage. In the shared-tenancy profile, one SVM serves
   all tenants (see [tenancy/shared.md](../tenancy/shared.md)).
2. **Virtual storage pools** on the SVM that back the offered tiers. Each
   pool's service characteristics (for example, media type and QoS) map to an
   OSAC StorageTier.
3. **Transport** between the VMaaS target cluster's hosts and the SVM:
   Fibre Channel or iSCSI for block volumes, Ethernet for NFS. Zoning,
   networking, and multipathing are configured and validated by the storage
   operator, including on every worker node (see
   [Worker node preparation](#worker-node-preparation)). OSAC does not
   validate the transport (see below).
4. **Trident CSI operator** installed and operated on the VMaaS target
   cluster. OSAC does not install or operate Trident.
5. **TridentBackendConfig** connecting Trident to the SVM and its pools. In
   the shared-tenancy profile, one shared backend config serves all tenants.
6. **StorageClasses**: one per offered tier, carrying the OSAC labels (see
   [StorageClass contract](#storageclass-contract)) and the
   `csi.trident.netapp.io` provisioner.

Refer to the
[NetApp Trident documentation](https://docs.netapp.com/us-en/trident/index.html)
for installation and configuration steps; Trident installation and operation
belong to the ownership model chosen for the deployment (see
[ownership/](../ownership/)).

## Protocols and data plane

The OSAC API distinguishes only `NFS` (file) and `BLOCK` (volume) at the
tier level. The concrete transport is a Trident driver choice on the target
cluster:

| OSAC tier protocol | Trident driver | Transports |
|---|---|---|
| `BLOCK` | `ontap-san` | Fibre Channel, iSCSI |
| `BLOCK` | `ontap-san-economy` | iSCSI |

The supported NetApp profile provides block storage only. NFS-backed tiers
are not supported for NetApp in this release, even though the tier
protocol exists in the API.

OSAC's readiness check verifies **StorageClass presence only**. It does not
validate Fibre Channel zoning, iSCSI networking, the SVM's health, or
Trident's operational state. Transport validation remains with the storage
operator.

## Worker node preparation

Block volumes reach a worker node over Fibre Channel or iSCSI through
DM-Multipath. Trident checks the node every time it attaches a block
volume, and the attach fails unless:

- `multipathd` is running on the node, and
- `find_multipaths` is `no` in `/etc/multipath.conf`. Trident rejects `yes`
  and `smart`.

Fibre Channel also needs the worker's HBA ports zoned to the SVM's FC LIFs
on the switch. iSCSI also needs `iscsid` enabled on the worker.

This is node configuration on the VMaaS target cluster, done once per
cluster by whoever operates it (the CSP in the CSP-managed model). OSAC
does not apply it and does not check it. NetApp's
[worker node preparation guide](https://docs.netapp.com/us-en/trident/trident-use/worker-node-prep.html)
is the reference for every transport; this section covers the multipath
part on OpenShift.

### What happens on an unprepared node

Nothing fails until a workload uses a volume. Binding the
TridentBackendConfig and creating a volume go through Trident's controller
and the ONTAP management interface, not through the worker, so OSAC's
readiness check passes and volumes are provisioned. The pod that mounts the
volume then stays in `ContainerCreating`, and its events carry Trident's
message:

```text
multipathd is not running
```

or, when `find_multipaths` is `yes`:

```text
multipathd: unsupported find_multipaths: yes value; please set the value to no in /etc/multipath.conf file
```

With Fibre Channel and no zoning between the worker and the SVM, the
message is `no matching WWPN found for node <node>`.

The fix is on the node; the kubelet retries the mount on its own. The check
runs per node, so a cluster with only some workers prepared fails only for
VMs that land on an unprepared one. Prepare every worker that can run
tenant VMs, including live-migration targets.

### Check the nodes first

Both methods below write `/etc/multipath.conf`. Before using either, find
out what the nodes have today:

```shell
# MachineConfigs that already manage /etc/multipath.conf
oc get machineconfig -o json | jq -r '.items[]
  | select(any(.spec.config.storage.files[]?; .path == "/etc/multipath.conf"))
  | .metadata.name'

# The file on a node, if there is one
oc debug node/<node> -- chroot /host cat /etc/multipath.conf

# Multipath devices already in use on a node
oc debug node/<node> -- chroot /host multipath -ll
```

Run the node commands on every node the method changes. Trident's node
preparation runs in Trident's node pods, which by default run on every
Linux node, control plane included. A MachineConfig changes the nodes of
its pool.

Either method can be used as described when no MachineConfig manages the
file, the file does not exist on those nodes, and none of them has
multipath devices in use. Otherwise read the cases under the method before
using it.

### iSCSI: Trident node preparation

For iSCSI, Trident can configure and enable `iscsid` and `multipathd` on
the nodes itself. Set `nodePrep` on the TridentOrchestrator:

```yaml
spec:
  nodePrep:
    - iscsi
```

NetApp's
[OpenShift OperatorHub installation guide](https://docs.netapp.com/us-en/trident/trident-install/openshift-operator-deploy.html)
shows it in a complete TridentOrchestrator. `iscsi` is the only value
`nodePrep` supports, and on OpenShift 4.19 and later it needs Trident
25.06.1 or later (see the `nodePrep` row in NetApp's
[TridentOrchestrator options](https://docs.netapp.com/us-en/trident/trident-install/kubernetes-customize-deploy.html)).

What node preparation does to `/etc/multipath.conf` depends on what the
node has:

| The node has | Node preparation |
|---|---|
| No `/etc/multipath.conf` | Writes the [configuration below](#fibre-channel-machineconfig), which blacklists every device except NetApp LUNs |
| A file that sets `find_multipaths no`, or has no `defaults` section | Rewrites the file, adding the NetApp device exception and, where there is no `defaults` section, one with `find_multipaths no` |
| A file whose `defaults` section does not set `find_multipaths no` | Stops with an error and leaves the file unchanged |

Do not enable `nodePrep` when the [check](#check-the-nodes-first) found
either of these:

- **Multipath devices in use on a node that has no `/etc/multipath.conf`.**
  The configuration Trident writes there blacklists every device that is
  not a NetApp LUN, including a multipath boot device or another storage
  system's LUNs. Use a MachineConfig with a configuration that keeps those
  devices instead.
- **A MachineConfig that manages `/etc/multipath.conf`.** Trident rewrites
  the file on the node. The Machine Config Operator treats a managed file
  that no longer matches its MachineConfig as
  [configuration drift](https://docs.redhat.com/en/documentation/openshift_container_platform/4.19/html/machine_configuration/machine-config-index#machine-config-drift-detection_machine-config-overview):
  it marks the node and its pool `Degraded`, and a degraded node cannot be
  updated. Change that MachineConfig instead: it needs `find_multipaths no`,
  NetApp LUNs (vendor `NETAPP`, product `LUN`) must not be blacklisted, and
  it must enable `iscsid` and `multipathd`.

### Fibre Channel: MachineConfig

Trident has no node preparation for Fibre Channel. On OpenShift, apply the
multipath configuration with a MachineConfig. For iSCSI without `nodePrep`,
the same MachineConfig with its `iscsid.service` unit uncommented makes the
changes `nodePrep` makes on a node with no `/etc/multipath.conf`: it writes
this file and enables `iscsid` and `multipathd`.

A MachineConfig replaces the whole file; nothing merges it with an existing
one. If the [check](#check-the-nodes-first) found no MachineConfig managing
the file, no file on the nodes, and no multipath devices in use, the
example below can be applied as it is. Otherwise do not apply it as it is:
it replaces the existing configuration, and its blacklist excludes every
device that is not a NetApp LUN, including a multipath boot device or
another storage system's LUNs. Change the existing configuration instead:
it needs `find_multipaths no`, and NetApp LUNs (vendor `NETAPP`, product
`LUN`) must not be blacklisted.

**1. The multipath configuration.** This is what Trident's own node
preparation writes on a node that has no `/etc/multipath.conf`:

```text
defaults {
    find_multipaths no
}
blacklist {
    device {
        vendor ".*"
        product ".*"
    }
}
blacklist_exceptions {
    device {
        vendor "NETAPP"
        product "LUN"
    }
}
```

With `find_multipaths no`, multipath claims every path device that is not
blacklisted, local disks included. The blacklist with the NetApp exception
limits it to NetApp LUNs.

**2. The MachineConfig.** It writes that file and enables `multipathd`:

```yaml
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 99-worker-netapp-multipath
  labels:
    machineconfiguration.openshift.io/role: worker
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
        - path: /etc/multipath.conf
          mode: 420  # 0644
          overwrite: true
          contents:
            # base64 of the multipath configuration above
            source: data:text/plain;charset=utf-8;base64,ZGVmYXVsdHMgewogICAgZmluZF9tdWx0aXBhdGhzIG5vCn0KYmxhY2tsaXN0IHsKICAgIGRldmljZSB7CiAgICAgICAgdmVuZG9yICIuKiIKICAgICAgICBwcm9kdWN0ICIuKiIKICAgIH0KfQpibGFja2xpc3RfZXhjZXB0aW9ucyB7CiAgICBkZXZpY2UgewogICAgICAgIHZlbmRvciAiTkVUQVBQIgogICAgICAgIHByb2R1Y3QgIkxVTiIKICAgIH0KfQo=
    systemd:
      units:
        - name: multipathd.service
          enabled: true
        # iSCSI without Trident nodePrep only:
        # - name: iscsid.service
        #   enabled: true
```

The example targets the `worker` MachineConfigPool. On single-node and
compact three-node clusters the nodes are in the `master` pool; change the
role label and the name to match.

**3. Apply it.** By default the Machine Config Operator drains and reboots
the nodes of the pool, one at a time. Apply it before the cluster carries
tenant workloads, or in a maintenance window.

```shell
oc apply -f 99-worker-netapp-multipath.yaml

# Done when UPDATED is True
oc get machineconfigpool worker
```

### Verifying a node

Run this for each worker that can run tenant VMs:

```shell
oc debug node/<node> -- chroot /host sh -c \
  'systemctl is-active multipathd; multipathd show config | grep -w find_multipaths'
```

Expect `active` and `find_multipaths "off"`; `multipathd` reports `no` as
`off`. Trident's check reads the same `multipathd show config` output.

## StorageClass contract

OSAC resolves NetApp-backed tiers by label, regardless of the driver or
parameters. For the shared-tenancy CSP-managed profile, each class carries:

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: ontap-gold
  labels:
    osac.openshift.io/tenant: csp-managed
    osac.openshift.io/storage-tier: gold
provisioner: csi.trident.netapp.io
parameters:
  backendType: ontap-san
  # ... Trident parameters selecting the SVM's pool for this tier
```

Exactly one class must exist per configured tier. See
[concepts/storageclass.md](../concepts/storageclass.md) for the resolution
rules and the full label contract.

## Dev Preview boundaries

- **Ownership:** CSP-managed only. OSAC-managed NetApp is planned.
- **Profile:** VMaaS. CaaS and BMaaS consumption is planned.
- **Tenancy:** shared SVM. Dedicated-SVM profiles are planned.

See [guides/csp-managed-netapp-shared.md](../guides/csp-managed-netapp-shared.md)
for the end-to-end profile guide.
