# agentless_net.l3

Layer 3 network operations for the agentless networking backend.
Manages Linux namespace routers, SNAT, and DNAT on the net-node.

## Roles

### router

Create and delete per-tenant routers as Linux network namespaces
with VLAN sub-interfaces and veth pairs for external connectivity.

```yaml
- ansible.builtin.include_role:
    name: agentless_net.l3.router
    tasks_from: create
  vars:
    router_name: tenant-a
    router_vlan_id: 100
    router_internal_subnet: "10.100.0.0/24"
    router_internal_gateway: "10.100.0.1"
    router_trunk_interface: eth1
    router_external_ip: "10.254.0.2/30"
    router_external_peer_ip: "10.254.0.1/30"
    router_external_gateway: "10.254.0.1"
```

### snat

Add and remove source NAT (MASQUERADE) rules for tenant outbound traffic.

```yaml
- ansible.builtin.include_role:
    name: agentless_net.l3.snat
    tasks_from: create
  vars:
    snat_router_name: tenant-a
    snat_source_subnet: "10.100.0.0/24"
    snat_veth_interface: v-tenant-a-i
    snat_external_subnet: "10.254.0.0/30"
    snat_external_interface: eth0
```

### dnat

Add and remove destination NAT (port forwarding) rules.
Supports configurable protocol (defaults to TCP).

```yaml
- ansible.builtin.include_role:
    name: agentless_net.l3.dnat
    tasks_from: create
  vars:
    dnat_router_name: tenant-a
    dnat_public_ip: "10.254.0.2"
    dnat_public_port: 6443
    dnat_internal_ip: "10.100.0.10"
    dnat_internal_port: 6443
```
