# Ansible Collection - vastdata.vms

Ansible Collection for the VAST Management System (VMS). Manage VAST Data
storage and platform resources declaratively: views, view policies, quotas,
VIP pools, tenants, users, groups, identity providers, snapshots, protection
and replication, S3 policies, hardware, and much more.

## Requirements

- ansible-core >= 2.15
- Python >= 3.9 on the control node (matching your ansible-core)
- `requests` (Python library) on the control node
- A reachable VAST VMS endpoint. Supported VAST product versions: 5.4.x - 5.5.x
  (enforced at runtime by the modules).

## Installation

```bash
ansible-galaxy collection install vastdata.vms
```

## Content

The collection ships:

- ~329 modules covering VMS resources. Most resources provide a managed module
  (e.g. `views`) plus a read-only `*_info` module (e.g. `views_info`); there are
  150 `*_info` modules in total.
- The `protectedpath` role for declarative VAST protected-path workflows
  (replication VIP pools, native replication peers, protection policies,
  protected paths, multistream replication, and standby streams).
- Lookup plugins `vast_name_to_id` and `vast_target_cluster_to_remote_target_id`
  for resolving names to IDs.

Modules are auto-generated from the VAST Swagger/OpenAPI specification by
`tools/generate_from_swagger.py`. Regenerating reproduces the modules, info
modules, unit tests, `meta/runtime.yml` action groups, and `schema_overrides.py`.

Browse modules with:

```bash
ansible-doc -l vastdata.vms
ansible-doc vastdata.vms.views
```

## Authentication

Every module takes a `vms:` connection dict (see the `vastdata.vms.conn` doc
fragment). Authenticate with an API token (VAST 5.3+) or username/password
(mutually exclusive):

```yaml
vms:
  host: vms.example.com
  token: "{{ vast_api_token }}"
  validate_certs: true
```

## Quickstart

```yaml
- name: Manage a VAST view
  hosts: localhost
  gather_facts: false
  tasks:
    - name: Ensure a view exists
      vastdata.vms.views:
        vms:
          host: vms.example.com
          token: "{{ vast_api_token }}"
        path: /ansible-demo
        policy_id: 1
        protocols: [NFS, SMB]
        state: present

    - name: Fetch views
      vastdata.vms.views_info:
        vms:
          host: vms.example.com
          token: "{{ vast_api_token }}"
      register: views
```

### Setting the connection once with module_defaults

`meta/runtime.yml` defines the `vastdata.vms.all` action group, so the `vms:`
block can be set once for every module in a play:

```yaml
- name: Manage VAST resources
  hosts: localhost
  gather_facts: false
  module_defaults:
    group/vastdata.vms.all:
      vms:
        host: vms.example.com
        token: "{{ vast_api_token }}"
  tasks:
    - vastdata.vms.views:
        path: /ansible-demo
        policy_id: 1
        state: present
    - vastdata.vms.tenants_info:
```

## Features

- Idempotent `present`/`absent` lifecycle with minimal PATCH diffing.
- Check mode and diff mode support across managed modules.
- Multi-version VAST support (5.4.x - 5.5.x): per-field version gating, request
  transforms, and runtime cluster-version validation.

## Versioning and changelog

The collection follows [Semantic Versioning](https://semver.org/) independently
of the VAST product version. The changelog is generated with
[antsibull-changelog](https://github.com/ansible-community/antsibull-changelog);
see [CHANGELOG.rst](CHANGELOG.rst) and the contribution guide for the fragment
workflow.

## License

Apache-2.0. See [LICENSE](LICENSE).
