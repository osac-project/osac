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

## Dependencies

- `ansible_network.network_runner` (vendored in osac-aap)
