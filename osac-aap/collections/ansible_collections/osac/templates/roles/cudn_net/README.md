# cudn_net

Provisions networking resources using ClusterUserDefinedNetwork (CUDN) on OpenShift.

> **Note:** SecurityGroup enforcement (NetworkPolicy) has been extracted to the standalone
> `osac.templates.network_policy` role so it can be reused across any K8s-based NetworkClass.

## Resources

### VirtualNetwork

VirtualNetworks define the top-level network isolation boundary with CIDR allocation and implementation strategy selection via NetworkClass.

**Key behaviors:**
- Creates ClusterUserDefinedNetwork CR in the cluster
- Supports IPv4-only, IPv6-only, and dual-stack configurations
- NetworkClass determines the implementation strategy (cudn_net)
- One VirtualNetwork maps to one ClusterUserDefinedNetwork

**Implementation:**
- ClusterUserDefinedNetwork is a cluster-scoped resource
- CIDR ranges are configured via spec.network field
- Layer2 topology is used for CUDN implementation

### Subnet

Subnets subdivide VirtualNetworks into logical segments with isolated namespaces for workload deployment.

**Key behaviors:**
- Creates namespace with specific labels for CUDN attachment
- Namespace labeled with `osac.openshift.io/virtual-network: {vn-name}`
- Namespace labeled with `k8s.ovn.org/primary-user-defined-network: ""`
- Enables pod connectivity to the parent VirtualNetwork's CUDN
- One Subnet maps to one namespace

**Namespace targeting:**
- Pods deployed in the Subnet namespace automatically connect to the VirtualNetwork's CUDN
- No additional network configuration needed on pods
- Network attachment is namespace-based, not interface-based

## Implementation Strategy

This role implements the `cudn_net` NetworkClass strategy using OpenShift's ClusterUserDefinedNetwork (CUDN) feature. The implementation follows these patterns:

**For VirtualNetworks:**
- Create ClusterUserDefinedNetwork CR with Layer2 topology
- Configure CIDR ranges from VirtualNetwork spec

**For Subnets:**
- Create namespace with CUDN attachment labels
- Label namespace with parent VirtualNetwork reference
- Pods deployed in namespace automatically connect to CUDN

## Task Files

- `tasks/create_virtual_network.yaml` - Creates ClusterUserDefinedNetwork CR from VirtualNetwork resource
- `tasks/delete_virtual_network.yaml` - Removes ClusterUserDefinedNetwork CR
- `tasks/create_subnet.yaml` - Creates namespace with CUDN labels from Subnet resource
- `tasks/delete_subnet.yaml` - Removes namespace

## Usage

### Example: VirtualNetwork Provisioning

```yaml
- name: Create VirtualNetwork
  ansible.builtin.include_role:
    name: cudn_net
    tasks_from: create_virtual_network
  vars:
    virtual_network: "{{ ansible_eda.event.payload }}"
    virtual_network_name: "{{ ansible_eda.event.payload.metadata.name }}"
```

### Example: Subnet Provisioning

```yaml
- name: Create Subnet
  ansible.builtin.include_role:
    name: cudn_net
    tasks_from: create_subnet
  vars:
    subnet: "{{ ansible_eda.event.payload }}"
    subnet_name: "{{ ansible_eda.event.payload.metadata.name }}"
```
