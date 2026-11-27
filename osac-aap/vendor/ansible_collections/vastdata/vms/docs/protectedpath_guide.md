# Protected Path Role Example Guide

This guide explains how to use the `vastdata.vms.protectedpath` role with the example playbook shipped in this collection (`playbooks/protectedpath.yml`).

## What The Role Creates

`protectedpath` creates the objects needed for VAST protected paths:

- VMS connection map normalization from the `vms` variable.
- Replication VIP pools from `vippools`.
- Native replication peers from `replicationpeers`.
- Protection policies from `protectionpolicies`.
- Protected paths, replication streams, and standby streams from `protectedpaths`.

The role runs those stages in that order because later objects depend on IDs resolved from earlier objects.

## Run The Example

After installing the collection (`ansible-galaxy collection install vastdata.vms`),
run the bundled example playbook by its fully-qualified name:

```bash
ansible-playbook vastdata.vms.protectedpath \
  -e @replicationgroup_protected_path_vars.yml
```

The playbook loads `replicationgroup_protected_path_vars.yml` by default. Copy the
shipped `playbooks/replicationgroup_protected_path_vars.yml.example` to that name
and edit it, or pass a different vars file with `-e @...` as shown above.

Use Ansible Vault or another secret manager for real passwords and tokens.

## Variable Model

### `vms`

`vms` is a map of cluster aliases to VMS connection details. The alias is the stable name used by all other sections.

Supported auth forms:

```yaml
vms:
  source:
    vms_host: vms-source.example.com
    vms_token: "{{ vault_source_token }}"
    validate_certs: true
    api_version: latest

  target:
    vms_host: vms-target.example.com
    vms_username: admin
    vms_password: "{{ vault_target_password }}"
    validate_certs: true
    api_version: latest
```

Define token auth or username/password auth per cluster, not both.

### `vippools`

`vippools` defines replication VIP pools on the clusters that receive replicated data.

```yaml
vippools:
  - cluster: target
    name: replication
    role: REPLICATION
    subnet_cidr: 24
    ip_ranges:
      - ["192.0.2.10", "192.0.2.20"]
```

For native replication, the role uses these ranges to match a replication peer's `leading_vip` to the target cluster.

### `replicationpeers`

`replicationpeers` defines native replication remote targets on source clusters.

```yaml
replicationpeers:
  - cluster: source
    name: source_to_target
    secure_mode: SECURE
    transport_mode: TCP
    leading_vip: "192.0.2.10"
```

If exactly one VIP pool exists for the same cluster, the role can infer it. Otherwise set `pool`, `pool_name`, `vippool`, or `pool_id`.

### `protectionpolicies`

`protectionpolicies` defines snapshot and replication schedules.

Local policy:

```yaml
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
```

Native replication policy:

```yaml
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
```

`target_cluster` is required for `NATIVE_REPLICATION`. It must match a key in `vms`.

### `protectedpaths`

Local protected path:

```yaml
protectedpaths:
  - name: prod-local
    cluster: source
    tenant: default
    dir: /prod
    capabilities: LOCAL
    protection_policy: local-hourly
```

Replicated protected path:

```yaml
protectedpaths:
  - name: prod-replicated
    cluster: source
    tenant: default
    dir: /prod
    streams:
      - remote_cluster: target
        remote_tenant: default
        remote_dir: /prod-dr
        protection_policy: target-replication
        capabilities: ASYNC_REPLICATION
```

If `streams` is omitted, the path must use `capabilities: LOCAL` and a local `protection_policy`.

Failover can be requested on a regular stream after the remote protected path exists:

```yaml
protectedpaths:
  - name: prod-replicated
    cluster: source
    tenant: default
    dir: /prod
    streams:
      - remote_cluster: target
        remote_tenant: default
        remote_dir: /prod-dr
        protection_policy: target-replication
        capabilities: ASYNC_REPLICATION
        failover: true
        graceful: false
```

When `failover: true` is set, the role validates the protected path exists on `remote_cluster` and then triggers failover there. `graceful` defaults to `true`; set `graceful: false` to force an ungraceful, remote-only failover without source-cluster protected path validation.

## Multistream And Standby Streams

A multistream path replicates one source path to multiple remote clusters:

```yaml
protectedpaths:
  - name: prod-multistream
    cluster: source
    tenant: default
    dir: /prod
    streams:
      - remote_cluster: target-a
        remote_tenant: default
        remote_dir: /prod-a
        protection_policy: target-a-policy
        capabilities: ASYNC_REPLICATION
      - remote_cluster: target-b
        remote_tenant: default
        remote_dir: /prod-b
        protection_policy: target-b-policy
        capabilities: ASYNC_REPLICATION
```

A standby stream creates a stream from one remote cluster to another remote cluster after the source stream is ready:

```yaml
standby_streams:
  - source_cluster: target-a
    remote_cluster: target-b
    protection_policy: target-b-policy
    capabilities: ASYNC_REPLICATION
```

`source_cluster` must match exactly one `streams[].remote_cluster` entry on the same protected path.

## Replication Group Example: between `cluster1`, `cluster2`, `cluster3`

This example models a three-cluster replication group where `cluster1` protects `/prod` with two async streams: one to `cluster2` and one to `cluster3`.

```yaml
vms:
  cluster1:
    vms_host: vms-cluster1.example.com
    vms_username: admin
    vms_password: "{{ vault_cluster1_password }}"
    validate_certs: true
    api_version: latest
  cluster2:
    vms_host: vms-cluster2.example.com
    vms_username: admin
    vms_password: "{{ vault_cluster2_password }}"
    validate_certs: true
    api_version: latest
  cluster3:
    vms_host: vms-cluster3.example.com
    vms_username: admin
    vms_password: "{{ vault_cluster3_password }}"
    validate_certs: true
    api_version: latest

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
    secure_mode: SECURE
    transport_mode: TCP
    leading_vip: "192.0.2.20"
  - cluster: cluster1
    name: cluster1_to_cluster3
    secure_mode: SECURE
    transport_mode: TCP
    leading_vip: "192.0.2.30"

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

protectedpaths:
  - name: prod-replication-group
    cluster: cluster1
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
```

## Optional State And Wait Controls

Most objects support `state`, `wait`, and `wait_timeout` because the role passes those fields to the underlying `vastdata.vms` modules.

For stream creation, the role also supports retry controls:

```yaml
protectedpaths:
  - name: prod-replicated
    cluster: source
    tenant: default
    dir: /prod
    stream_retries: 18
    stream_retry_delay: 10
    standby_stream_wait_retries: 60
    standby_stream_wait_delay: 10
    streams:
      - remote_cluster: target
        remote_tenant: default
        remote_dir: /prod-dr
        protection_policy: target-replication
```

## Troubleshooting

- Unknown cluster errors mean a `cluster`, `target_cluster`, `remote_cluster`, or `source_cluster` value does not match a key in `vms`.
- `NATIVE_REPLICATION` policy failures usually mean `target_cluster` is missing or the role cannot match exactly one replication peer from the source cluster to the target cluster.
- Stream validation failures usually mean `remote_cluster`, `remote_tenant`, `remote_dir`, or `protection_policy` is missing.
- Standby stream validation failures usually mean `source_cluster` does not match a regular stream on the same path.
- Name lookup failures mean the referenced tenant, policy, VIP pool, or replication peer does not exist or is not unique in VMS.
