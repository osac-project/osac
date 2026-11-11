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

# SCSI devices on a node. VENDOR NETAPP marks a NetApp LUN the node
# already has, such as a boot LUN
oc debug node/<node> -- chroot /host lsblk -S
```

Run the node commands on every node the method changes. Trident's node
preparation runs in Trident's node pods, which by default run on every
Linux node, control plane included. A MachineConfig changes the nodes of
every pool that uses it: one with the `worker` role also changes the nodes
of custom pools such as `infra`, because custom pools inherit the worker
pool's MachineConfigs.

Both methods are for nodes that do not use multipath yet and have no
NetApp LUN: no MachineConfig manages the file, the file does not exist on
the nodes, and none of them has multipath devices in use or a NetApp LUN
already, such as a boot LUN. If the check finds otherwise, do not use
either method as described; see
[Nodes that need their own configuration](#nodes-that-need-their-own-configuration).

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

On a node with no `/etc/multipath.conf`, node preparation writes the
[configuration below](#fibre-channel-machineconfig), which blacklists every
device except NetApp LUNs, and enables `iscsid` and `multipathd`.

Enable `nodePrep` only when the [check](#check-the-nodes-first) shows that
none of the nodes running a Trident node pod uses multipath yet or already
has a NetApp LUN. On a node that has multipath devices in use but no file,
the configuration Trident writes blacklists every one of them that is not
a NetApp LUN. On a node that already has a NetApp LUN, such as a boot LUN
on a single path, that configuration lets multipath claim it. On a node
that already has the file, Trident does not write this configuration: it
edits the existing file, or stops with an error, and what multipath claims
afterwards depends on what the file already had. For those nodes see
[Nodes that need their own configuration](#nodes-that-need-their-own-configuration).

### Fibre Channel: MachineConfig

Trident has no node preparation for Fibre Channel. On OpenShift, apply the
multipath configuration with a MachineConfig. For iSCSI without `nodePrep`,
the same MachineConfig with its `iscsid.service` unit uncommented makes the
changes `nodePrep` makes on a node with no `/etc/multipath.conf`: it writes
this file and enables `iscsid` and `multipathd`.

A MachineConfig replaces the whole file; nothing merges it with an existing
one. Apply the example below only when the [check](#check-the-nodes-first)
shows that none of the nodes it changes uses multipath yet or already has
a NetApp LUN. Anywhere else it replaces the existing configuration, its
blacklist excludes every device that is not a NetApp LUN, including a
multipath boot device or another storage system's LUNs, and its exception
lets multipath claim a NetApp LUN the node already has, such as a boot LUN
on a single path. For those nodes see
[Nodes that need their own configuration](#nodes-that-need-their-own-configuration).

**1. Generate the MachineConfig.** It writes `/etc/multipath.conf` and
enables `multipathd`. A MachineConfig carries a file's contents as an
encoded `data:` URL, so the first command encodes the multipath
configuration and the second writes the MachineConfig around it:

```shell
MULTIPATH_CONF=$(base64 <<'EOF' | tr -d '\n'
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
EOF
)

cat > 99-worker-netapp-multipath.yaml <<EOF
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
            source: data:text/plain;charset=utf-8;base64,${MULTIPATH_CONF}
    systemd:
      units:
        - name: multipathd.service
          enabled: true
        # iSCSI without Trident nodePrep only:
        # - name: iscsid.service
        #   enabled: true
EOF
```

The multipath configuration is what Trident's own node preparation writes
on a node that has no `/etc/multipath.conf`. With `find_multipaths no`,
multipath claims every path device that is not blacklisted, local disks
included. The blacklist with the NetApp exception limits it to NetApp LUNs.

To read back what the generated MachineConfig writes:

```shell
grep -o 'base64,.*' 99-worker-netapp-multipath.yaml | cut -d, -f2 | base64 --decode
```

The example targets the `worker` MachineConfigPool, and with it every
custom pool, since custom pools inherit the worker pool's MachineConfigs.
On single-node and compact three-node clusters the nodes are in the
`master` pool; change the role label and the name to match.

**2. Apply it.** By default the Machine Config Operator drains and reboots
the nodes of each pool the MachineConfig changes, one node at a time in
each pool. Apply it before the cluster carries tenant workloads, or in a
maintenance window.

```shell
oc apply -f 99-worker-netapp-multipath.yaml

# Progress of every pool
oc get machineconfigpool

# Pools whose nodes all run the new MachineConfig
oc get machineconfigpool -o json | jq -r '.items[]
  | select(any(.status.configuration.source[]?;
      .name == "99-worker-netapp-multipath"))
  | .metadata.name'
```

The rollout is done when the second command lists every pool the
MachineConfig changes. If the MachineConfig was renamed, use its name in
the command. `UPDATED` alone does not show that: right after `oc apply`, a
pool still reports `True` for its previous configuration.

### Nodes that need their own configuration

When the [check](#check-the-nodes-first) finds a MachineConfig that manages
`/etc/multipath.conf`, the file on a node, multipath devices in use, or a
NetApp LUN a node already has, do not enable `nodePrep` and do not apply
the example MachineConfig. Write the configuration yourself, or change the
existing one where it falls short, so that it has:

- `find_multipaths no` in the `defaults` section.
- A blacklist that covers every device multipath must leave alone. With
  `find_multipaths no`, multipath claims every path device that is not
  blacklisted, local disks included.
- The LUNs Trident attaches (vendor `NETAPP`, product `LUN`) not excluded
  by the blacklist. A `blacklist_exceptions` entry for that vendor and
  product lifts a vendor and product blacklist, and it does so for every
  NetApp LUN on the node. To keep one of them out of multipath, such as a
  boot LUN, blacklist it by WWID, which that exception does not lift.
- Every device multipath already serves on the node, such as a multipath
  boot device or another storage system's LUNs, still served.
- `multipathd` enabled, and `iscsid` for iSCSI.

Make the change where the file is managed. If a MachineConfig manages it,
change that MachineConfig: the Machine Config Operator treats a managed
file that was changed on the node as
[configuration drift](https://docs.redhat.com/en/documentation/openshift_container_platform/4.19/html/machine_configuration/machine-config-index#machine-config-drift-detection_machine-config-overview),
marks the node and its pool `Degraded`, and cannot update a degraded node.
If no MachineConfig manages it, the
[commands above](#fibre-channel-machineconfig) generate one from any
multipath configuration: put the complete configuration in place of the
example's. That MachineConfig writes the same file to every node of the
pools it changes, custom pools included, so the configuration must meet
the needs of every one of those nodes.

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
