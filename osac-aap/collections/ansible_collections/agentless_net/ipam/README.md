# agentless_net.ipam

IP address and VLAN ID pool management for the agentless networking backend.
Allocates and releases resources from configured pools, tracked in a JSON
state file with file-level locking for concurrent access safety.

## Roles

### ip

Allocate and release IP addresses from a pool.

```yaml
- ansible.builtin.include_role:
    name: agentless_net.ipam.ip
    tasks_from: allocate
  vars:
    ipam_state_file: /etc/osac/network_state.json
    ipam_pool_start: "203.0.113.10"
    ipam_pool_end: "203.0.113.50"
    ipam_purpose: tenant-a-snat
    ipam_count: 1  # optional, defaults to 1
```

### vlan_id

Allocate and release VLAN IDs from a pool.

```yaml
- ansible.builtin.include_role:
    name: agentless_net.ipam.vlan_id
    tasks_from: allocate
  vars:
    ipam_state_file: /etc/osac/network_state.json
    ipam_vlan_pool_start: 100
    ipam_vlan_pool_end: 199
    ipam_purpose: tenant-a
```

## State File

Allocations are tracked in a JSON file (default `/etc/osac/network_state.json`):

```json
{
  "vlans": {"tenant-a": 100, "tenant-b": 101},
  "public_ips": {"tenant-a-snat": ["203.0.113.10"]}
}
```

The file is created automatically on first allocation. Concurrent access
is safe via `fcntl.flock` exclusive locking.
