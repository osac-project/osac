# NVIDIA Bare Metal Manager Ansible Collection

`nvidia.bare_metal` is an Ansible collection for managing resources on the
NVIDIA Bare Metal Manager REST API. All modules
are generated from the OpenAPI specification, keeping them in sync with the
API surface.

## Requirements

- Ansible >= 2.14
- Python >= 3.9
- A JWT bearer token for the Bare Metal Manager API

## Installation

### From Git

```bash
ansible-galaxy collection install git+https://github.com/fabiendupont/nvidia-bare-metal-manager-ansible.git
```

### From a local build

```bash
cd bare-metal-manager-ansible
ansible-galaxy collection build
ansible-galaxy collection install nvidia-bare_metal-1.0.0.tar.gz
```

## Authentication

Every module and the inventory plugin accept four common parameters. They
can be set per-task, in `module_defaults`, or via environment variables.

| Parameter | Environment Variable | Description |
|---|---|---|
| `api_url` | `NVIDIA_BMM_API_URL` | Base URL of the API (or the NVIDIA proxy) |
| `api_token` | `NVIDIA_BMM_API_TOKEN` | JWT bearer token |
| `org` | `NVIDIA_BMM_ORG` | Organization name |
| `api_path_prefix` | `NVIDIA_BMM_API_PATH_PREFIX` | `carbide` (direct) or `forge` (proxy). Default: `carbide` |

### Obtaining a token

Exchange SSA credentials for a JWT:

```bash
export NVIDIA_BMM_API_TOKEN=$(curl -s -X POST \
  "$SSA_TOKEN_URL" \
  -d "client_id=$CLIENT_ID&client_secret=$CLIENT_SECRET&grant_type=client_credentials" \
  | jq -r '.access_token')
```

### Using module_defaults

Set auth once for all tasks in a play:

```yaml
- hosts: localhost
  module_defaults:
    group/nvidia.bare_metal.all:
      api_url: "{{ bmm_api_url }}"
      api_token: "{{ bmm_api_token }}"
      org: "{{ bmm_org }}"
      api_path_prefix: forge
  tasks:
    - nvidia.bare_metal.vpc:
        state: present
        name: my-vpc
        site_id: "{{ site_id }}"
```

## Quick start

### Create a VPC and provision an instance

```yaml
- hosts: localhost
  tasks:
    - name: Create VPC
      nvidia.bare_metal.vpc:
        state: present
        name: lab-vpc
        site_id: "{{ site_id }}"
        network_virtualization_type: FNN
      register: vpc

    - name: Create VPC prefix
      nvidia.bare_metal.vpc_prefix:
        state: present
        name: lab-prefix
        vpc_id: "{{ vpc.resource.id }}"
        ip_block_id: "{{ ip_block_id }}"
        prefix_length: 24
      register: prefix

    - name: Create instance
      nvidia.bare_metal.instance:
        state: present
        name: gpu-worker-01
        tenant_id: "{{ tenant_id }}"
        instance_type_id: "{{ instance_type_id }}"
        vpc_id: "{{ vpc.resource.id }}"
        operating_system_id: "{{ os_id }}"
        interfaces:
          - vpc_prefix_id: "{{ prefix.resource.id }}"
            is_physical: true
        ssh_key_group_ids:
          - "{{ ssh_key_group_id }}"
        labels:
          env: lab
      register: instance
```

### List resources

```yaml
- name: Get all VPCs at a site
  nvidia.bare_metal.vpc_info:
    site_id: "{{ site_id }}"
  register: vpcs

- name: Get a specific instance by ID
  nvidia.bare_metal.instance_info:
    id: "{{ instance_id }}"
  register: inst
```

### Delete resources

```yaml
- name: Delete an instance
  nvidia.bare_metal.instance:
    state: absent
    name: gpu-worker-01

- name: Delete a VPC
  nvidia.bare_metal.vpc:
    state: absent
    name: lab-vpc
    site_id: "{{ site_id }}"
```

### Batch create instances

```yaml
- name: Create 4 topology-optimized instances
  nvidia.bare_metal.instance_batch:
    name_prefix: gpu-worker
    count: 4
    tenant_id: "{{ tenant_id }}"
    instance_type_id: "{{ instance_type_id }}"
    vpc_id: "{{ vpc_id }}"
    operating_system_id: "{{ os_id }}"
    interfaces:
      - vpc_prefix_id: "{{ prefix_id }}"
        is_physical: true
    topology_optimized: true
```

## Dynamic inventory

The `nvidia.bare_metal.bmm` inventory plugin discovers instances from the
API and builds the Ansible inventory at runtime.

Create a file ending in `.bmm.yml`:

```yaml
# inventory/bmm.bmm.yml
plugin: nvidia.bare_metal.bmm
api_url: https://bmm-api.example.com
api_path_prefix: forge  # or 'carbide' for direct API access

filters:
  status: Ready
  site_id: "{{ site_id }}"

ansible_host_source: first_interface_ip

groups_from:
  - vpc_id
  - status

group_by_labels: true

compose:
  ansible_user: "'root'"

groups:
  gpu_nodes: bmm_labels.get('role') == 'compute'
```

Then use it like any inventory source:

```bash
ansible-inventory -i inventory/bmm.bmm.yml --list
ansible-playbook -i inventory/bmm.bmm.yml playbook.yml
```

### Inventory host variables

Each discovered host gets the following variables:

| Variable | Description |
|---|---|
| `ansible_host` | First IP address from the instance's interfaces |
| `bmm` | Full instance dict (all fields, snake_case keys) |
| `bmm_id` | Instance ID |
| `bmm_site_id` | Site ID |
| `bmm_vpc_id` | VPC ID |
| `bmm_instance_type_id` | Instance Type ID |
| `bmm_machine_id` | Machine ID |
| `bmm_status` | Instance status |
| `bmm_labels` | Instance labels dict |

### Auto-grouping

Groups are created from instance fields and labels:

- `bmm_site_id_<id>` -- one group per site
- `bmm_vpc_id_<id>` -- one group per VPC
- `bmm_status_Ready` -- one group per status value
- `bmm_label_<key>_<value>` -- one group per label key-value pair

## Modules

### CRUD modules

Manage resources with `state: present` (create or update) and `state: absent` (delete).
All support `check_mode`, `wait`, and `wait_timeout`.

| Module | Resource |
|---|---|
| `allocation` | Allocation |
| `allocation_constraint` | Allocation Constraint (nested under allocation) |
| `dpu_extension_service` | DPU Extension Service |
| `expected_machine` | Expected Machine |
| `infiniband_partition` | InfiniBand Partition |
| `instance` | Instance |
| `instance_type` | Instance Type |
| `ip_block` | IP Block |
| `machine` | Machine (update and delete only; machines are discovered, not created) |
| `network_security_group` | Network Security Group |
| `nvlink_logical_partition` | NVLink Logical Partition |
| `operating_system` | Operating System |
| `site` | Site |
| `ssh_key` | SSH Key |
| `ssh_key_group` | SSH Key Group |
| `subnet` | Subnet |
| `tenant_account` | Tenant Account |
| `vpc` | VPC |
| `vpc_prefix` | VPC Prefix |

### Info modules

Retrieve resources. Return `resource` (single) or `resources` (list).

| Module | Resource |
|---|---|
| `allocation_info` | Allocation |
| `allocation_constraint_info` | Allocation Constraint |
| `audit_info` | Audit log entries |
| `dpu_extension_service_info` | DPU Extension Service |
| `expected_machine_info` | Expected Machine |
| `infiniband_partition_info` | InfiniBand Partition |
| `infrastructure_provider_info` | Infrastructure Provider |
| `instance_info` | Instance |
| `instance_type_info` | Instance Type |
| `ip_block_info` | IP Block |
| `machine_capability_info` | Machine Capability |
| `machine_info` | Machine |
| `metadata_info` | API Metadata |
| `network_security_group_info` | Network Security Group |
| `nvlink_logical_partition_info` | NVLink Logical Partition |
| `operating_system_info` | Operating System |
| `rack_info` | Rack |
| `service_account_info` | Service Account |
| `site_info` | Site |
| `sku_info` | SKU |
| `ssh_key_group_info` | SSH Key Group |
| `ssh_key_info` | SSH Key |
| `subnet_info` | Subnet |
| `tenant_account_info` | Tenant Account |
| `tenant_info` | Tenant |
| `user_info` | User |
| `vpc_info` | VPC |
| `vpc_prefix_info` | VPC Prefix |

### Batch modules

| Module | Description |
|---|---|
| `instance_batch` | Batch-create multiple instances with topology-optimized allocation |

## Idempotency

CRUD modules are idempotent:

- **Create**: looks up the resource by `name` (within scope fields like `site_id`
  or `vpc_id`). If found, compares fields and patches only what changed. If not
  found, creates.
- **Update**: pass `id` to target a specific resource. Only changed fields are
  sent in the PATCH.
- **Delete**: if the resource doesn't exist, reports `changed: false`.
- **Nested structures**: interfaces, InfiniBand interfaces, and other nested
  objects are compared correctly across camelCase (API) and snake_case (Ansible).
- **Labels**: label keys are user-defined strings and are never case-converted.

Use `id` for deterministic lookups; use `name` for human-friendly idempotency.

## Wait behavior

By default, modules wait for asynchronous operations (create, update, delete) to
complete. The resource is polled until its `status` reaches a ready state or an
error state.

```yaml
- nvidia.bare_metal.instance:
    state: present
    name: my-instance
    # ...
    wait: true          # default
    wait_timeout: 600   # seconds, default
```

Set `wait: false` to return immediately after the API call.

Resources without a `status` field (e.g., allocation constraints) skip the
wait automatically.

## Code generation

The modules in `plugins/modules/` are generated from the OpenAPI spec.

```bash
# Regenerate after spec changes
make generate

# Run unit tests
make test

# Syntax-check all modules
make lint
```

The generator reads `bare-metal-manager-rest/openapi/spec.yaml`, resolves
`$ref` references, groups operations by tag, and produces one Python file
per resource. Hand-written code lives in `plugins/module_utils/` and is
not regenerated.

## License

Apache-2.0. See [LICENSE](LICENSE) for details.
