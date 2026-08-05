# Playbook & Template Patterns

## Playbook Naming

Top-level playbooks: `playbook_osac_{action}_{resource}.yml`
AAP job templates: `osac-{action}-{resource}`

| Playbook | AAP Job Template | Triggered By |
|----------|------------------|--------------|
| `playbook_osac_create_subnet.yml` | `osac-create-subnet` | osac-operator SubnetReconciler |
| `playbook_osac_delete_virtual_network.yml` | `osac-delete-virtual-network` | osac-operator VirtualNetworkReconciler |
| `playbook_osac_create_security_group.yml` | `osac-create-security-group` | osac-operator SecurityGroupReconciler |

**Configuration:** osac-operator env var `OSAC_AAP_TEMPLATE_PREFIX` (default: `osac`)

## Standard Playbook Structure

```yaml
---
- name: Create a Subnet resource
  hosts: localhost
  gather_facts: false

  vars:
    subnet: "{{ ansible_eda.event.payload }}"
    subnet_name: "{{ ansible_eda.event.payload.metadata.name }}"
    implementation_strategy: >-
      {{ ansible_eda.event.payload.metadata.annotations
         ['osac.openshift.io/implementation-strategy']
         | default(ansible_eda.event.payload.spec.implementationStrategy, true) }}

  pre_tasks:
    - name: Show EDA Event
      ansible.builtin.debug:
        var: ansible_eda.event.payload

  tasks:
    - name: Call the selected implementation role
      ansible.builtin.include_role:
        name: "osac.templates.{{ implementation_strategy }}"
        tasks_from: create_subnet
```

**Key pattern:**
1. Playbook receives K8s CR as `ansible_eda.event.payload`
2. Extracts implementation strategy from CR annotation (`osac.openshift.io/implementation-strategy`) or `spec.implementationStrategy` — annotation takes precedence when both are present
3. Dynamically includes the appropriate role from `osac.templates`
4. Role performs actual provisioning (creates K8s resources, updates CR)

## Template Roles

Live in `collections/ansible_collections/osac/templates/roles/`. Each must have `meta/osac.yaml`:

```yaml
---
title: CUDN Network Implementation
description: Provisions networking resources using CUDN
template_type: network
implementation_strategy: cudn_net
capabilities:
  supports_ipv4: true
  supports_ipv6: true
  supports_dual_stack: true
```

**Fields:**
- `implementation_strategy` — matches annotation value, role name, and NetworkClass strategy
- `template_type` — `network`, `compute`, or `cluster`
- `capabilities` — feature flags published to NetworkClass

**Note:** Use underscores (`_`), not hyphens (`-`), in role names and `implementation_strategy`.

## Service Roles

| Role | Purpose | Usage |
|------|---------|-------|
| `osac.service.common` | Shared utilities (kubeconfig, credentials) | `tasks_from: get_remote_cluster_kubeconfig` |
| `osac.service.finalizer` | Finalizer management for CRs | `tasks_from: add_finalizer` |
| `osac.service.lease` | Bare-metal lease management | Used by cluster/compute workflows |
| `osac.service.wait_for` | Polling utilities | Wait for pods, deployments, CRs |
| `osac.service.tenant_storage_class` | StorageClass discovery | Find tenant-specific storage |
| `osac.service.publish_templates` | Template registration | Publishes NetworkClass from `meta/osac.yaml` |

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
  ↓ (triggers AAP job template)
playbook_osac_create_subnet.yml
  ↓ (sets implementation_strategy from annotation)
osac.templates.cudn_net (role)
  ↓ (reads subnet.spec.*, creates K8s resources)
Namespace + ClusterUserDefinedNetwork
```

## Runtime Variables and Environment Variables

| Variable | Purpose | Set By |
|----------|---------|--------|
| `ansible_eda.event.payload` | K8s CR data | AAP/EDA event |
| `remote_cluster_kubeconfig` | Path to remote kubeconfig | `osac.service.common` role |
| `implementation_strategy` | Network implementation to use | Extracted from CR annotation |
| `OSAC_AAP_URL` | AAP server URL | osac-operator config |
| `OSAC_AAP_TOKEN` | AAP auth token | osac-operator config |
