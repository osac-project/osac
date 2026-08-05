# openstack

Provisions networking resources using OpenStack Neutron.

## Resources

### VirtualNetwork

VirtualNetworks define the top-level network isolation boundary with CIDR allocation and implementation strategy selection via NetworkClass.

**Key behaviors:**
- Creates Neutron network resource in OpenStack
- Network MTU defaults to 1500 (configurable via `default_network_mtu`)
- Networks are created as internal (non-external) networks
- One VirtualNetwork maps to one Neutron network
- Network name matches VirtualNetwork CR name

**Implementation:**
- Uses `openstack.cloud.network` module
- Network properties configured from defaults (MTU)
- Network deletion is idempotent (no error if already deleted)

### Subnet

Subnets subdivide VirtualNetworks into logical segments with DHCP, DNS, and IP allocation.

**Key behaviors:**
- Creates Neutron subnet within the parent VirtualNetwork's network
- IP allocation pool reserves first 10 and last 10 IPs in CIDR range
- Gateway set to first usable IP in CIDR
- DNS nameservers default to `8.8.8.8` (configurable via `default_subnet_dns_nameservers`)
- Optional router creation for external connectivity (controlled by `default_subnet_external_gateway`)
- One Subnet maps to one Neutron subnet (plus optional router)

**IP Allocation:**
- For `10.0.0.0/24`:
  - Gateway: `10.0.0.1`
  - Allocation pool: `10.0.0.10` - `10.0.0.245`
  - Reserved: first 9 IPs and last 10 IPs

**Router behavior:**
- When `default_subnet_external_gateway` is `true`, creates router named `router-{subnet-name}`
- Router connects subnet to external network (default: `external`, configurable via `default_subnet_external_network`)
- Router deleted automatically when subnet is deleted

**Fallback CIDR:**
- If VirtualNetwork or Subnet does not specify `ipv4Cidr`, uses `default_subnet_ipv4_cidr` (default: `192.168.50.0/24`)

### SecurityGroup

**Not yet implemented.**

## Implementation Strategy

This role implements the `openstack` NetworkClass strategy using OpenStack Neutron. The implementation follows these patterns:

**For VirtualNetworks:**
- Create Neutron network with MTU configuration
- Network name matches CR name for idempotent operations

**For Subnets:**
- Create Neutron subnet within parent network
- Configure DHCP, DNS, and allocation pools
- Optionally create router for external connectivity

**For SecurityGroups:**
- Planned: will translate to Neutron security groups and rules

## Task Files

- `tasks/create_virtual_network.yaml` - Creates Neutron network from VirtualNetwork resource
- `tasks/delete_virtual_network.yaml` - Removes Neutron network
- `tasks/create_subnet.yaml` - Creates Neutron subnet and optional router from Subnet resource
- `tasks/delete_subnet.yaml` - Removes router and Neutron subnet

## Usage

### Example: VirtualNetwork Provisioning

```yaml
- name: Create VirtualNetwork
  ansible.builtin.include_role:
    name: openstack
    tasks_from: create_virtual_network
  vars:
    virtual_network: "{{ ansible_eda.event.payload }}"
    virtual_network_name: "{{ ansible_eda.event.payload.metadata.name }}"
```

### Example: Subnet Provisioning

```yaml
- name: Create Subnet
  ansible.builtin.include_role:
    name: openstack
    tasks_from: create_subnet
  vars:
    subnet: "{{ ansible_eda.event.payload }}"
    subnet_name: "{{ ansible_eda.event.payload.metadata.name }}"
```

### Example: Custom Subnet Configuration

```yaml
- name: Create Subnet with custom settings
  ansible.builtin.include_role:
    name: openstack
    tasks_from: create_subnet
  vars:
    subnet: "{{ ansible_eda.event.payload }}"
    subnet_name: "{{ ansible_eda.event.payload.metadata.name }}"
    default_subnet_dns_nameservers:
      - 10.0.0.10
      - 10.0.0.11
    default_subnet_external_gateway: false
```
