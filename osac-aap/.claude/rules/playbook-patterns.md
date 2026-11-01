# Playbook & Template Patterns

## Networking jobs

Every networking job template (`osac-<task>`, for example `osac-create-subnet`)
runs `playbook_osac_network_task.yml`, the generic playbook of the network
manager contract. The operator launches it with `osac_job_vars` (the task, the
manager with its `ansible_role`, the resource snapshot and the task's other
inputs). The playbook checks the task against the manager type's fixed task set,
finds the role's task file, and includes it. The role publishes only
`osac_result` / `osac_error` with `set_stats`; it never reads or writes OSAC CRs.

Other top-level playbooks: `playbook_osac_{action}_{resource}.yml`, job template
`osac-{action}-{resource}`.

### Show Resource Metadata and Sensitive `payload.spec` Fields

Before debugging a resource in a non-networking playbook, check its schema for
secrets (for example `blockEncryptionPassphrase`), and use
`osac.service.common` `show_resource_metadata` instead of debugging the whole
object.

## Template Roles

Live in `collections/ansible_collections/osac/templates/roles/`. Each must have `meta/osac.yaml`:

```yaml
---
title: Network Manager
description: Provisions resources for a registered network manager
template_type: network
fabric_manager: <registration-name>
capabilities:  # informational only here -- see the `capabilities` field note below
  supports_ipv4: true
  supports_ipv6: true
  supports_dual_stack: true
```

**Fields:**
- `fabric_manager`/`k8s_manager` — at least one required for `template_type: network`; informational, matches the registration `name` and the role directory name
- `template_type` — `network`, `compute`, or `cluster`
- `capabilities` — informational only for `template_type: network` roles; not published anywhere (NetworkClass creation happens via osac-installer's Helm chart, independent of this file)

**Note:** Use underscores (`_`), not hyphens (`-`), in role names and `fabric_manager`/`k8s_manager`. Network roles are registered by a registration ConfigMap (`ansible_role`), not by `meta/osac.yaml`.

## Service Roles

| Role | Purpose | Usage |
|------|---------|-------|
| `osac.service.common` | Shared utilities (kubeconfig, credentials) | `tasks_from: get_remote_cluster_kubeconfig` |
| `osac.service.finalizer` | Finalizer management for CRs | `tasks_from: add_finalizer` |
| `osac.service.lease` | Generic Kubernetes Lease-based mutex | Used by cluster/compute/storage workflows |
| `osac.service.wait_for` | Polling utilities | Wait for pods, deployments, CRs |
| `osac.service.tenant_storage_class` | StorageClass discovery | Find tenant-specific storage |
| `osac.service.publish_templates` | Template registration | Publishes ComputeClass-family resources from `meta/osac.yaml` |
| `osac.service.csi_driver_install` | Idempotent OSAC CSI driver Helm install | Called directly from hub-targeting storage dispatch playbooks |

## Common Ansible Patterns

### Extracting CR Fields

```yaml
- name: Extract Subnet configuration
  ansible.builtin.set_fact:
    subnet_name: "{{ subnet.metadata.name }}"
    subnet_namespace: "{{ subnet.metadata.namespace }}"
    subnet_id: "{{ subnet.metadata.labels['osac.openshift.io/subnet-uuid'] }}"
    subnet_ipv4_cidr: "{{ subnet.spec.ipv4Cidr | default('') }}"
    subnet_tenant_id: "{{ subnet.metadata.annotations['osac.openshift.io/tenant'] }}"
```

### Creating K8s Resources on Remote Cluster

```yaml
- name: Get remote cluster kubeconfig
  ansible.builtin.include_role:
    name: osac.service.common
    tasks_from: get_remote_cluster_kubeconfig

- name: Create Namespace for Subnet
  kubernetes.core.k8s:
    kubeconfig: "{{ remote_cluster_kubeconfig | default(omit) }}"
    state: present
    definition:
      apiVersion: v1
      kind: Namespace
      metadata:
        name: "{{ subnet_name }}"
        labels:
          osac.openshift.io/subnet-id: "{{ subnet_id }}"
          osac.openshift.io/tenant: "{{ subnet_tenant_id }}"
```

## Variable Flow

```text
osac-operator
  ↓ (launches osac-<task> with osac_job_vars and a job label)
playbook_osac_network_task.yml
  ↓ (include_role osac_job_vars.manager.ansible_role, tasks_from: <task>)
role selected by the manager registration
  ↓ (backend and hub objects keyed by API IDs)
osac_result / osac_error (set_stats)
```

## Runtime Variables and Environment Variables

| Variable | Purpose | Set By |
|----------|---------|--------|
| `osac_job_vars.resource` | K8s CR data | osac-operator extra vars |
| `remote_cluster_kubeconfig` | Path to remote kubeconfig | `osac.service.common` role |
| `OSAC_AAP_URL` | AAP server URL | osac-operator config |
| `OSAC_AAP_TOKEN` | AAP auth token | osac-operator config |
