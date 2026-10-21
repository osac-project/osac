# agentless_net.l2

Layer 2 switch operations for the agentless networking backend.
Configures VLANs and port assignments on physical switches via the
`ansible_network.network_runner` collection.

## Roles

### vlan

Create and delete VLANs on switches.

```yaml
- ansible.builtin.include_role:
    name: agentless_net.l2.vlan
    tasks_from: create
  vars:
    vlan_id: 100
```

### port

Configure switch ports as access ports or reset them.

```yaml
- ansible.builtin.include_role:
    name: agentless_net.l2.port
    tasks_from: set_access_port
  vars:
    port_name: swp2
    vlan_id: 100
```

The AgentlessNet BMaaS `move_network_attachment` adapter uses these shared
port tasks to place a mapped host interface on its Subnet VLAN and return it to
the provider's provisioning VLAN. The host/interface-to-switch-port mapping
and provisioning VLAN are provider-owned entries in
`AGENTLESS_NET_VN_INVENTORY` under `all.vars`; they are not BareMetalInstance
fields. Mapped access ports must not overlap a switch's declared `trunk_ports`.

The AgentlessNet `query_dhcp_lease` adapter reads the saved VirtualNetwork DHCP
lease file and matches the expected interface MAC within the requested
Subnet's allocation. It returns the common `leases` artifact consumed by
BMaaS IP discovery.

## Dependencies

- `ansible_network.network_runner` (vendored in osac-aap)
