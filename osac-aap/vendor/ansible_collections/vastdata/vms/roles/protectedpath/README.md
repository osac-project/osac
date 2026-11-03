# protectedpath

Configure VAST protected paths and the supporting native replication objects they depend on.

The role can manage, in order:

- Replication VIP pools
- Native replication peers
- Protection policies
- Local, replicated, and multistream protected paths
- Standby streams for chained replication topologies

## Requirements

- `vastdata.vms` collection installed.
- Network access from the Ansible control host to every VMS endpoint in `vms`.
- Existing tenants and source directories referenced by the protected paths.
- For replication, each target cluster must have a replication VIP pool and each source cluster must have a native replication peer that points at the target replication VIP.

## Role Variables

Control which resource groups the role manages:

```yaml
protectedpath_manage_vippools: true
protectedpath_manage_replicationpeers: true
protectedpath_manage_protectionpolicies: true
protectedpath_manage_protectedpaths: true
```

Primary input variables:

- `vms`: map of cluster aliases to VMS connection details.
- `vippools`: replication VIP pools to create or update.
- `replicationpeers`: native replication peers to create or update.
- `protectionpolicies`: local or native replication protection policies.
- `protectedpaths`: protected path definitions, including optional `streams` and `standby_streams`.

Cluster aliases are used as references throughout the role. For example, `protectedpaths[].cluster` must match a key in `vms`, and `streams[].remote_cluster` must also match a key in `vms`.

## Minimal Local Example

```yaml
vms:
  source:
    vms_host: vms-source.example.com
    vms_auth: basic
    vms_username: admin
    vms_password: "{{ vault_vms_password }}"
    validate_certs: true
    api_version: latest

protectionpolicies:
  - cluster: source
    name: local-hourly
    tenant: default
    clone_type: LOCAL
    prefix: local-hourly
    frames:
      - every: "1h"
        keep-local: "24h"
        start-at: "2026-01-01 00:00:00"

protectedpaths:
  - cluster: source
    name: prod-local
    tenant: default
    dir: /prod
    capabilities: ASYNC_REPLICATION
    protection_policy: local-hourly
```

## Native Replication Example

```yaml
vippools:
  - cluster: target
    name: replication
    role: REPLICATION
    subnet_cidr: 24
    ip_ranges:
      - ["192.0.2.10", "192.0.2.20"]

replicationpeers:
  - cluster: source
    name: source_to_target
    leading_vip: "192.0.2.10"
    secure_mode: SECURE
    transport_mode: TCP

protectionpolicies:
  - cluster: source
    name: target-replication
    tenant: default
    target_cluster: target
    clone_type: NATIVE_REPLICATION
    prefix: target-replication
    frames:
      - every: "10m"
        keep-local: "3h"
        keep-remote: "6h"
        start-at: "2026-01-01 00:00:00"

protectedpaths:
  - cluster: source
    name: prod-replicated
    tenant: default
    dir: /prod
    streams:
      - remote_cluster: target
        remote_tenant: default
        remote_dir: /prod-dr
        protection_policy: target-replication
        capabilities: ASYNC_REPLICATION
```

## Multistream Replication Group Example

This example creates a replication group where `cluster1` replicates one protected path to both `cluster2` and `cluster3`, then adds a standby stream from `cluster2` to `cluster3`.

```yaml
vippools:
  - cluster: cluster2
    name: replication-cluster2
    role: REPLICATION
    subnet_cidr: 24
    ip_ranges:
      - ["192.0.2.20", "192.0.2.29"]
  - cluster: cluster3
    name: replication-cluster3
    role: REPLICATION
    subnet_cidr: 24
    ip_ranges:
      - ["192.0.2.30", "192.0.2.39"]

replicationpeers:
  - cluster: cluster1
    name: cluster1_to_cluster2
    leading_vip: "192.0.2.20"
    secure_mode: SECURE
    transport_mode: TCP
  - cluster: cluster1
    name: cluster1_to_cluster3
    leading_vip: "192.0.2.30"
    secure_mode: SECURE
    transport_mode: TCP
  - cluster: cluster2
    name: cluster2_to_cluster3
    leading_vip: "192.0.2.30"
    secure_mode: SECURE
    transport_mode: TCP

protectionpolicies:
  - cluster: cluster1
    name: replicate-to-cluster2
    tenant: default
    target_cluster: cluster2
    clone_type: NATIVE_REPLICATION
    prefix: cluster2-copy
    frames:
      - every: "10m"
        keep-local: "3h"
        keep-remote: "6h"
        start-at: "2026-01-01 00:00:00"
  - cluster: cluster1
    name: replicate-to-cluster3
    tenant: default
    target_cluster: cluster3
    clone_type: NATIVE_REPLICATION
    prefix: cluster3-copy
    frames:
      - every: "10m"
        keep-local: "3h"
        keep-remote: "6h"
        start-at: "2026-01-01 00:00:00"
  - cluster: cluster2
    name: standby-to-cluster3
    tenant: default
    target_cluster: cluster3
    clone_type: NATIVE_REPLICATION
    prefix: cluster2-standby-cluster3
    frames:
      - every: "10m"
        keep-local: "3h"
        keep-remote: "6h"
        start-at: "2026-01-01 00:00:00"

protectedpaths:
  - cluster: cluster1
    name: prod-replication-group
    tenant: default
    dir: /prod
    streams:
      - name: prod_to_cluster2
        remote_cluster: cluster2
        remote_tenant: default
        remote_dir: /prod
        protection_policy: replicate-to-cluster2
        capabilities: ASYNC_REPLICATION
      - name: prod_to_cluster3
        remote_cluster: cluster3
        remote_tenant: default
        remote_dir: /prod
        protection_policy: replicate-to-cluster3
        capabilities: ASYNC_REPLICATION
        failover: true
        graceful: false
    standby_streams:
      - name: prod_cluster2_to_cluster3_standby
        source_cluster: cluster2
        remote_cluster: cluster3
        remote_tenant: default
        remote_dir: /prod-standby
        protection_policy: standby-to-cluster3
        capabilities: ASYNC_REPLICATION
```

## Notes

- Token auth uses `vms_token` or `token`.
- Basic auth uses `vms_username`/`vms_password` or `username`/`password`.
- Define either token auth or basic auth per cluster, not both.
- `NATIVE_REPLICATION` policies require `target_cluster`.
- Protected path and stream capabilities default to `ASYNC_REPLICATION`.
- Regular streams can set `failover: true`; `graceful` defaults to `true`, while `graceful: false` performs remote-only failover without source-cluster protected path validation.
- `SYNC_REPLICATION` streams require a matching replication peer so the role can resolve `remote_target_id`.

See [`docs/protectedpath_guide.md`](../../docs/protectedpath_guide.md) for a detailed
walkthrough, the runnable `playbooks/protectedpath.yml` example playbook, and
`playbooks/replicationgroup_protected_path_vars.yml.example` for a complete
multicluster variable set.
