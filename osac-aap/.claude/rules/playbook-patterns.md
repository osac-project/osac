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
    subnet: "{{ osac_job_vars.resource }}"
    subnet_name: "{{ osac_job_vars.resource.metadata.name }}"
    # Subnet's spec still carries a legacy implementationStrategy field, so its
    # playbooks fall back to it when the annotation is absent. VirtualNetwork's
    # spec field was removed once the dispatcher became the sole routing
    # mechanism (OSAC-1468) — its playbooks read the annotation only, with no
    # fallback (see playbook_osac_create_virtual_network.yml).
    implementation_strategy: >-
      {{ osac_job_vars.resource.metadata.annotations
         ['osac.openshift.io/implementation-strategy']
         | default(osac_job_vars.resource.spec.implementationStrategy, true) }}

  pre_tasks:
    - name: Show resource metadata
      ansible.builtin.debug:
        var: osac_job_vars.resource.metadata

  tasks:
    - name: Call the selected implementation role
      ansible.builtin.include_role:
        name: "osac.templates.{{ implementation_strategy }}"
        tasks_from: create_subnet
```

**Key pattern:**
1. Playbook receives K8s CR as `osac_job_vars.resource`
2. Extracts implementation strategy from the `osac.openshift.io/implementation-strategy` CR annotation, which osac-operator's dispatcher stamps from the NetworkClass's `fabric_manager`/`k8s_manager`. Subnet playbooks fall back to the legacy `spec.implementationStrategy` when the annotation is absent; VirtualNetwork playbooks do not (no such spec field exists anymore).
3. Dynamically includes the appropriate role from `osac.templates`
4. Role performs actual provisioning (creates K8s resources, updates CR)

### Show Resource Metadata and Sensitive `payload.spec` Fields

The bare `debug: var: osac_job_vars.resource` shown above is safe as long as
nothing in `payload.spec` is a secret. Some CR specs are not — e.g.
`blockEncryptionPassphrase` on Tenant/ClusterOrder/ComputeInstance CRs. Before
adding this debug task to a new or existing playbook, inspect the CR
schema for every field that can occur in `payload.spec`. If any field can
contain a secret, use the shared, centralized task instead of debugging the
whole object:

```yaml
  pre_tasks:
    - name: Show resource metadata
      ansible.builtin.include_role:
        name: osac.service.common
        tasks_from: show_resource_metadata
      vars:
        resource_extra_fields:
          template_id: "{{ osac_job_vars.resource.spec.templateID | default('unknown') }}"
```

This logs `kind`/`metadata.name`/`metadata.namespace`/`metadata.uid` plus any
additional non-sensitive fields passed via `resource_extra_fields` — never
source an `resource_extra_fields` value from `payload.spec` without first
confirming it isn't a secret. See
`collections/ansible_collections/osac/service/roles/common/tasks/show_resource_metadata.yaml`.

## Template Roles

Live in `collections/ansible_collections/osac/templates/roles/`. Each must have `meta/osac.yaml`:

```yaml
---
title: CUDN Network Implementation
description: Provisions networking resources using CUDN
template_type: network
fabric_manager: cudn_net
capabilities:
  supports_ipv4: true
  supports_ipv6: true
  supports_dual_stack: true
```

**Fields:**
- `fabric_manager`/`k8s_manager` — at least one required for `template_type: network`; matches the annotation value osac-operator's dispatcher stamps and the role directory name
- `template_type` — `network`, `compute`, or `cluster`
- `capabilities` — feature flags published to NetworkClass

**Note:** Use underscores (`_`), not hyphens (`-`), in role names and `fabric_manager`/`k8s_manager`. `implementation_strategy` is no longer read here — it was reserved on the NetworkClass/VirtualNetwork protos once the dispatcher became the sole routing mechanism (OSAC-1468).

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
| `osac_job_vars.resource` | K8s CR data | osac-operator extra vars |
| `remote_cluster_kubeconfig` | Path to remote kubeconfig | `osac.service.common` role |
| `implementation_strategy` | Network implementation to use | Extracted from CR annotation |
| `OSAC_AAP_URL` | AAP server URL | osac-operator config |
| `OSAC_AAP_TOKEN` | AAP auth token | osac-operator config |
