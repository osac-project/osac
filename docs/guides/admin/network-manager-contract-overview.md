# OSAC networking contract in brief

This is a reading guide to the proposed networking contract. The implementation
is still catching up with the designs. See the [provider guide](network-manager-provider-guide.md)
for packaging, payloads, task names, and implementation requirements.

## The agreement

**OSAC owns the workflow. The provider owns the backend behavior.**

| Piece | Responsibility |
|---|---|
| OSAC | Validate tenant requests, choose the registered manager, provide resolved inputs, track jobs, store results, and order cleanup. |
| Fabric manager | Tenant networks, physical interfaces and policy, external addresses, NAT, and CaaS ingress reservations. |
| Kubernetes manager | VM overlay networking and VM interface policy. Needed for VM support. |
| Workload controllers | Create VMs, provision servers and clusters, and wait at networking gates before starting traffic or reusing resources. |

A provider supplies Ansible entry points for every task assigned to its role.
Those tasks can call any backend API or controller. The provider installs the
adapters and their dependencies in the execution environment, registers the
manager and its data models, supplies configuration/secrets, and selects the
manager through the deployment's NetworkClass.

No OSAC provider-specific source change should be needed once the generic
contract and installer work are implemented. Infrastructure prerequisites and
actual network behavior remain the provider's responsibility.

Here, “networking profile” means the selected NetworkClass and managers, not
another resource. NetworkClass is OSAC configuration, not a tenant network.
It has no provider create/delete task or runtime outputs. Shared provider
settings use the existing installation/environment configuration.

Separate VirtualNetworks may reuse private CIDRs and IPs. Two independent
endpoints sharing one Subnet must use different IPs. Address allocation and
cleanup always retain the owning network identity.

Every workload interface attached to a Subnet gets its tenant IPv4 address,
prefix, default gateway, and DNS resolver configuration through Dynamic Host
Configuration Protocol (DHCP). The workload OS must request DHCP on that
interface. The manager configures the network path and does not assign a static
address inside the guest or host.
DHCP on the tenant Subnet is separate from a bare-metal server's provisioning
network DHCP and from ExternalIP or CaaS ingress address reservations.

## The three data objects

| Object | Example |
|---|---|
| `NetworkDataModel` | “A Subnet VLAN ID is an integer from 1 to 4094.” The definition includes its meaning and owner scope. |
| `NetworkManager` | “My Fabric manager produces that model”; “my Kubernetes manager requires it.” |
| `NetworkData` | “This Subnet's VLAN ID is 217.” The immutable value for one API resource, stored authoritatively by fulfillment-service and projected as a CR in the networking hub. |

A Fabric create or reconcile task returns missing values for declared owners
present in its `network_owner_context`, including NetworkClass, VirtualNetwork,
and Subnet owners. A NetworkClass-scoped value can first be returned by the
first eligible resource operation; a Subnet operation can first return a
VirtualNetwork-scoped value. OSAC validates and stores the complete result
before starting consumers, and identical existing writes succeed. The
fulfillment service is authoritative and projects each record as a
NetworkData CR in the networking hub. OSAC reads values through its private
API and supplies them in AAP inputs; provider roles need neither database
credentials nor direct CR access.
Concurrent tasks that can produce the same missing owner/model value must
converge on one backend allocation and return the same value.
Matching model names proves declared compatibility; it does not prove that
packets can flow or that policy is enforced.

## Workload attachment flows

```mermaid
sequenceDiagram
    participant OSAC
    participant Fabric
    participant K8s as Kubernetes manager
    participant VMaaS
    participant BMaaS
    participant VM
    participant Host as Bare-metal host
    OSAC->>Fabric: Create network and Subnet
    Fabric-->>OSAC: Required network values
    OSAC->>OSAC: Validate and store values
    opt Kubernetes Manager is configured
        OSAC->>K8s: Create Subnet overlay using stored values
        K8s-->>OSAC: Success
    end
    alt VM overlay
        VMaaS->>VM: Create halted VM without tenant network/interface fields
        VMaaS->>OSAC: SubnetAttachment request for VM
        OSAC->>K8s: apply_subnet_attachment
        K8s->>VM: Add network/interface fields; install policy
        K8s-->>OSAC: Success for the request UID
        OSAC-->>VMaaS: Attachment ready
        VMaaS->>VM: Start VM
        VM-->>VM: Obtain tenant IPv4 configuration through DHCP
        VM-->>VMaaS: Tenant interface address discovered from VM status
        VMaaS-->>OSAC: Report network ready
    else Bare-metal physical interface
        BMaaS->>Host: Provision over provisioning connection
        BMaaS->>OSAC: SubnetAttachment request for BaremetalInstance
        OSAC->>Fabric: apply_subnet_attachment
        Fabric->>Host: Map selected switch port to Subnet segment; install policy
        Fabric-->>OSAC: Apply success for the request UID
        BMaaS->>Host: Handoff reboot onto tenant network
        OSAC->>Fabric: query_dhcp_lease for selected port
        Fabric-->>OSAC: Tenant address for selected port
        OSAC-->>BMaaS: Network ready after address discovery
    end
```

`apply_subnet_attachment` is one OSAC action with two manager paths. OSAC
selects the Kubernetes Manager for `overlay` and the Fabric Manager for
`physical`. In the VM path, VMaaS creates the exact VM halted without tenant
network/interface fields. Kubernetes adds the fields for the requested Subnet
and installs policy before the VM starts. The first boot then requests tenant
configuration through DHCP, and VMaaS discovers the assigned address from VM
status. In the bare-metal path, Fabric reconciles the selected physical port's
segment membership to the requested Subnet and installs policy after host
provisioning. The host receives a separate provisioning-network lease during
provisioning; after the network handoff reboot, its tenant interface requests
configuration through DHCP and BMaaS discovers that lease by selected-port
MAC.

The internal `SubnetAttachment` tracks the request, jobs, readiness and
cleanup. Its target is the VM or BaremetalInstance; it is not the VM interface
or switch-port mapping. The manager receives both request and target identity,
plus the resolved interface, Subnet and complete SecurityGroup rules.

Deletion follows the same ownership split in reverse. The service stops the
workload first; `delete_subnet_attachment` removes its policy and restores
the physical interface to that extension's baseline. For a VM, the service
deletes the stopped VM after policy cleanup. Subnet cleanup waits until its
workload attachments are gone.

BMaaS uses Fabric for that attachment. BMF waits for apply, performs the
handoff reboot, then networking discovers the lease using the selected port's
MAC. CaaS creates one such request for each bare-metal worker. OSAC verifies
the worker's parent Cluster and supplies that Cluster's tenant as the network
context, even though the hidden worker itself belongs to `system`.

For a new flow between two OSAC workloads, both source egress and destination
ingress rules must allow it. SecurityGroup rules combine as a union separately
at each endpoint, and established replies are allowed. Each manager enforces
policy on its own attachment; it does not look up the peer's SecurityGroups.
Traffic with an endpoint outside OSAC workload policy is governed by the
attached workload's rule in the direction of that traffic.

## How CaaS gets its ingress address

Fabric reserves an IP and returns it. OSAC records that IP in the ClusterOrder
annotation `osac.openshift.io/ingress-allocated-address`, then configures
MetalLB with it. Ingress becomes ready only after the Service confirms that
address. Fabric handles uniqueness and retry recovery; OSAC has no second
IPAM registry. Normal cleanup removes consumers, asks Fabric to release the
address, and then removes the annotation.

## Which identity owns the network?

The stable OSAC API resource ID owns backend allocations. Kubernetes UIDs
protect writes to particular objects. If a hub projection is recreated, OSAC
recovers its existing jobs and allocations under the same API ID. Deleting an
API resource and creating another gives a new ID and a new owner.

## What happens on failures?

- OSAC records the operation before launching it. A lost acknowledgment stays
  unresolved until OSAC can account for the original job; it does not start
  a conflicting delete or blindly launch again.
- Job success is followed by result validation and durable persistence. A
  restart resumes unfinished persistence using the same accepted result.
- Every AAP job is idempotent: rerunning after success or interruption converges
  without duplicate effects. Providers reuse owned resources, recover partial
  work, and support safe reruns after compatible adapter upgrades.
- Normal deletion removes consumers before producers and retains finalizers
  until cleanup is confirmed. Manager replacement waits for every bound
  resource and operation to drain.
- Globally disabled provisioning has a separate documented skip behavior;
  it does not claim the backend was cleaned up. Re-enabling requires draining
  existing workloads and reconciling provider leftovers. OSAC blocks new
  workload creation until the transition is ready; it does not migrate running
  workloads automatically.

## Can a provider use a no-op?

Yes. Its Ansible task may do nothing and return success. The entry point must
still exist, return the standard envelope, and supply required real values.
An address-allocation task cannot invent an IP that was never reserved.

OSAC trusts the provider's report. A no-op policy task is not proof that
SecurityGroups are enforced. Providers document their no-ops and own the
consequences; behavioral checks establish which outcomes they actually deliver.

## What must implementation tasks cover?

The generic registry and installer, durable operation tracking, validated
value exchange, workload gates, CaaS address ownership, provider adapters,
and a provider-runnable selection of OSAC's own E2E tests all need explicit coverage. Completing
registration alone does not complete bring-your-own networking.
