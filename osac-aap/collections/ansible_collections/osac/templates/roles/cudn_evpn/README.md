# cudn_evpn: OSAC Kubernetes Manager for network type `evpn`

`osac.templates.cudn_evpn` implements the eight Kubernetes Manager tasks of
the [network manager contract](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1433-network-manager-integration-contract-networking/design.md)
(§4.10 "OSAC Kubernetes Manager tasks"). OSAC runs each task through
`playbook_osac_network_task.yml` with `osac_job_vars`; the role calls only the
hub's Kubernetes API with the job pod's in-cluster service account and
publishes `osac_result` / `osac_error` with `set_stats`. It never reads or
writes OSAC CRs or VMs, and it takes no locks.

All decisions about inputs, names and manifests live in the filter plugin
`osac.templates.cudn_evpn_*` (`plugins/filter/cudn_evpn.py`); the task files
only read and write hub objects.

## Registration

The installer renders it in the OSAC namespace when
`global.networking.k8sManager: cudn_evpn`
(`osac-installer/charts/osac/templates/networking/cudn-evpn-registration.yaml`):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: osac-network-k8s-manager-cudn-evpn
  labels:
    osac.openshift.io/network-k8s-manager: "true"
immutable: true
data:
  name: cudn_evpn
  ansible_role: osac.templates.cudn_evpn
  network_type: evpn
```

## Hub objects

Every object is named from an API ID and carries
`osac.openshift.io/tenant`, `osac.openshift.io/virtualnetwork-id`,
`osac.openshift.io/owner-kind`, `osac.openshift.io/owner-id`,
`osac.openshift.io/k8s-manager: cudn_evpn`, and the annotations
`osac.openshift.io/tenant` and `osac.openshift.io/owner-reference`.

| Object | Name | Owner | Created by / removed by |
|---|---|---|---|
| Namespace (VMs run here) | `osac-vn-<VN ID>` | VirtualNetwork | `create_virtual_network` / `delete_virtual_network` |
| NetworkPolicy baseline deny | `osac-baseline-deny` in the namespace | VirtualNetwork | with the namespace |
| ClusterUserDefinedNetwork | `osac-vn-<VN ID>` | the owner Subnet (`osac.openshift.io/owner-subnet`) | owner's `create_subnet` / `delete_subnet` |
| NetworkPolicy per SecurityGroup | `osac-sg-<SG ID>` in the namespace | SecurityGroup | `create_security_group` / `delete_security_group` |

- **Namespace.** Created with `k8s.ovn.org/primary-user-defined-network: ""`
  (OVN-Kubernetes needs it at creation). `apply_network_attachment` returns
  it as `vm_attachment.namespace`.
- **Baseline deny.** Selects every pod with `policyTypes: [Ingress, Egress]`
  and allows only the DHCP exchange (egress UDP 67, ingress UDP 68).
  OVN-Kubernetes always allows ARP/ND past NetworkPolicy, but not DHCP.
- **Hub network (one per VirtualNetwork).** OVN-Kubernetes needs a distinct
  IP-VRF VNI per network and the fabric gives one per VirtualNetwork, so one
  Subnet owns the network (contract "`cudn_evpn` limit"). Creating the CUDN is
  the atomic claim: a server-side apply without force under the Subnet's own
  field manager, so a CUDN that another Subnet created meanwhile conflicts
  (retryable) instead of being taken over. The first Subnet whose `create_subnet` runs owns it; every
  other Subnet's create and delete succeed without changes, and VM
  attachments on them fail as final with the implementation limit. There is
  no handover: after the owner's delete, the next Subnet created claims a new
  network. The CUDN:
  - is Layer2, `role: Primary`, `transport: EVPN`, `mtu: 1500`,
    `ipam: {mode: Enabled, lifecycle: Persistent}` (VMs keep their address
    across restart and live migration);
  - selects the namespace by `osac.openshift.io/virtualnetwork-id` and tenant;
  - has `subnets: [<Subnet CIDR>]`, `defaultGatewayIPs: [ipam.gateway_ipv4]`
    and `infrastructureSubnets: [<gateway>/32, <network + 2>/32]`:
    OVN-Kubernetes serves the VM gateway (routing it into the IP-VRF,
    symmetric IRB) and uses network + 2 as its management-port address; both
    are excluded from VM allocation. The fabric does not serve the VM
    gateway;
  - has `reservedSubnets` = the Subnet minus `ipam.overlay_ipv4_cidrs`, as the fewest CIDRs, so VMs get addresses only from the overlay
    range (at most 25 entries; more fails as final);
  - carries the label `evpn: "true"` (selected by the installer's
    RouteAdvertisements `osac-evpn`) and `evpn.vtep: tenant-vtep`,
    `macVRF {vni: l2_vni, routeTarget: "0:<l2_vni>"}`,
    `ipVRF {vni: l3_vni, routeTarget: "0:<l3_vni>"}`.

  `l2_vni` comes from the launch key `network_segment {type: evpn, l2_vni}`
  and `l3_vni` from the stored VirtualNetwork snapshot
  `virtual_network.status.l3Vni` (EVPN handoff); `segment_vnis` is the only reader. `create_subnet` waits until the CUDN reports
  `TransportAccepted`, `NetworkCreated` and `NetworkAllocationSucceeded` True
  (OVN-Kubernetes 4.22 sets no `Ready` condition).
- **SecurityGroup policy.** Rules map to NetworkPolicy rules (`tcp`/`udp`
  with `port` and `endPort`, `all` without ports, the CIDR as `ipBlock`);
  `policyTypes` is always both. The podSelector holds the bound VMs
  (`osac.openshift.io/compute-instance-id In [...]`) and matches no pod while
  none is bound. A rule update replaces the rules and keeps the members; a
  bind or unbind changes the members and keeps the rules. Both are JSON
  patches that test `uid` and `resourceVersion`, so a concurrent change fails
  the task as retryable instead of being overwritten.

## Tasks

| Task | Does | Result |
|---|---|---|
| `create_virtual_network` | Create or verify the namespace; create or restore the baseline deny. | none |
| `delete_virtual_network` | Delete the namespace if it has this VirtualNetwork's owner labels; wait until gone. | none |
| `create_subnet` | No CUDN: check the VTEP, claim it, wait until ready. Owned by this Subnet: check it matches, restore metadata and selector, wait until ready. Owned by another Subnet: no change. Being deleted: retry. | `compute_instance_supported`, `compute_instance_unsupported_reason` |
| `delete_subnet` | Owner: delete the CUDN, wait until gone. Otherwise nothing. Never deletes the namespace. | none |
| `create_security_group` | Create the group policy, or replace its rules and keep its members. | none |
| `delete_security_group` | Delete the group policy; wait until gone. | none |
| `apply_network_attachment` | Check the namespace, that this Subnet owns a ready CUDN, and add `target.id` to each group policy of `security_group_ids`. Ignores `interface.ipv4_address`. | `vm_attachment: {namespace: osac-vn-<VN ID>}` |
| `delete_network_attachment` | Remove `target.id` from every group policy that selects it. | none |

Every create is idempotent and makes the hub match its input; every delete
finds objects by API ID, removes only what carries its owner labels, uses a
`uid` precondition, waits until the object is gone, and succeeds when it is
already absent.

## Errors

Every task publishes `osac_error: {message, retryable}` and fails the play.
Messages are tenant-visible and name only the step, the object and the HTTP
status/reason; they never contain module arguments or tokens.

| Final (`retryable: false`) | Retryable |
|---|---|
| An input OSAC never sends (shape, IDs, segment, layout) | Kubernetes API errors and timeouts |
| An `icmp` SecurityGroup rule (NetworkPolicy cannot match ICMP) | A conflicting concurrent change (HTTP 409/422 on a guarded patch) |
| An attachment on a Subnet that does not own the hub network | VTEP `tenant-vtep` missing or not Accepted |
| More than 25 `reservedSubnets` entries | Namespace, CUDN or group policy not there yet, being deleted, or not ready |
| A hub object with our name but someone else's owner labels | |
| An owned CUDN whose network spec no longer matches the Subnet | |

OSAC retries delete tasks after every failure, whatever `retryable` says.

## Settings

The role reads no setting of `network-fulfillment-ig`. It uses the job's own
service account on the hub cluster.

## Prerequisites and permissions

- Installer EVPN host networking (`osac-infra` `evpn.enabled: true`): the VTEP
  `tenant-vtep` (Accepted) and the RouteAdvertisements `osac-evpn` selecting
  CUDNs labelled `evpn: "true"`, FRR peering with the fabric.
- The hub is the cluster the job runs in; no remote cluster.
- Hub permissions of the job service account (bound to `cluster-admin` by the
  installation): namespaces (get, create, patch, delete),
  clusteruserdefinednetworks.k8s.ovn.org (get, create, patch, delete),
  networkpolicies (get, list, create, patch, delete), vteps.k8s.ovn.org (get).

## Known gaps

- **DNS.** A VM on a primary Layer2 UDN gets the ClusterIP of the cluster-wide
  DNS Service (`dns-service-namespace` / `dns-service-name` of ovnkube) as its
  DHCP `dns_server`. The CUDN has no DNS field and OSAC configures no
  per-resource DNS.
- **ICMP.** `icmp` rules fail `create_security_group` as final; `all` for the
  CIDR allows ICMP with everything else.
- **Claim during the owner's delete.** A Subnet created while the owner Subnet
  CR is being deleted but before its Kubernetes `delete_subnet` runs finds the
  network owned and stays non-owner for life. The role sees only the CUDN, not
  the OSAC Subnet CR (contract Edge case "Kubernetes Manager hub network").
- **Rogue DHCP replies.** The baseline accepts UDP 68 from any source in the
  VirtualNetwork, so a VM could answer another VM's DHCP within the same
  VirtualNetwork.
- **Node reachability.** With a SecurityGroup that allows all egress, a VM can
  ping the node that hosts it. Node services stay out of reach: see
  [networking decisions](../../../../../../../docs/agent-context/networking-decisions.md).

## Lab procedure (VMaaS happy path)

Run this against the Netris Fabric Manager on the Netris lab. Install with
provider networking and EVPN host networking (instance values):

```yaml
global:
  services:
    vmaas: {enabled: true}
  networking:
    provisioningEnabled: true
    fabricManager: netris
    k8sManager: cudn_evpn
    excludedIpv4Cidrs:
      - 192.0.2.0/24          # management and node networks
aap:
  instanceGroups:
    networkFulfillment:
      enabled: true
```

and infra values with `evpn.enabled: true`, `vtepCIDR`, `bgp` and `nodes`
(see the "Netris + CUDN EVPN CI overlays" section of `osac-installer/README.md`, which also lists
the Netris controller settings). The chart renders the `netris` and
`cudn_evpn` registrations.

Check the prerequisites and the registration:

```bash
NS=osac
oc get vtep tenant-vtep -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}{"\n"}'   # True
oc get routeadvertisements osac-evpn
oc -n $NS get cm osac-network-k8s-manager-cudn-evpn -o yaml
oc get networkclass -A   # Ready, networkType evpn
```

Create the resources as a tenant (`ICMP` rules are rejected; use `all`):

```bash
osac create virtualnetwork --name lab-vn --ipv4-cidr 10.60.0.0/16
osac create subnet --name lab-subnet --virtual-network lab-vn --ipv4-cidr 10.60.1.0/24
osac create securitygroup --name lab-sg --virtual-network lab-vn \
  --ingress protocol=all,ipv4-cidr=10.60.1.0/24 \
  --ingress protocol=tcp,port-from=22,port-to=22,ipv4-cidr=0.0.0.0/0 \
  --egress protocol=all,ipv4-cidr=0.0.0.0/0
osac create computeinstance --template <vm template> --name lab-vm1 \
  --network-attachment subnet=lab-subnet,security-groups=lab-sg
osac create computeinstance --template <vm template> --name lab-vm2 \
  --network-attachment subnet=lab-subnet,security-groups=lab-sg
```

Check the hub (`VN` is the VirtualNetwork API ID, `SG` the SecurityGroup's):

```bash
oc get ns osac-vn-$VN --show-labels             # primary-UDN, tenant, virtualnetwork-id labels
oc get cudn osac-vn-$VN -o jsonpath='{range .status.conditions[*]}{.type}={.status} {end}{"\n"}'
                                                 # TransportAccepted, NetworkCreated, NetworkAllocationSucceeded True
oc get cudn osac-vn-$VN -o jsonpath='{.spec.network.layer2}{"\n"}'   # reservedSubnets = outside overlay range
oc -n osac-vn-$VN get networkpolicy              # osac-baseline-deny, osac-sg-$SG
oc -n osac-vn-$VN get networkpolicy osac-sg-$SG -o jsonpath='{.spec.podSelector}{"\n"}'   # both VM IDs
oc -n osac-vn-$VN get vm,vmi -o wide             # both VMs here, VMI IPs in the overlay range
oc -n osac-vn-$VN get ipamclaims                 # one persistent claim per VM
```

In each VM console (`virtctl console -n osac-vn-$VN lab-vm1`): `ip addr`
shows an address from the overlay range, `ip route` the default route via
`.1`; `ping -c3 10.60.1.1` reaches the gateway, `ping -c3 <lab-vm2 address>`
reaches the other VM (check with `oc get vmi -o wide` that both run on one
node: cross-node traffic needs the real fabric and is out of scope here).
Check `osac get computeinstance` reports both Ready with their addresses;
restart one VM and check its address is unchanged. Remove the `all` rule
from the group (`osac edit securitygroup lab-sg`) and check the ping between
the VMs stops while SSH still works.

Delete the VMs, the SecurityGroup, the Subnet and the VirtualNetwork, and
check that `oc get ns osac-vn-$VN`, `oc get cudn osac-vn-$VN` and the group
policy are gone and every resource's finalizers were released.

Also check on the lab: a VM gets its DHCP lease with the baseline deny in
place (DHCP passes), ARP to the gateway works before any group is bound, and
which DNS server the guest received.
