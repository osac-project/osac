# OSAC Workflows Collection

This collection provides workflow playbooks for OSAC (Open Sovereign AI Cloud) with customer override support.

## Overview

The osac.workflows collection contains orchestration playbooks for cluster provisioning, compute instance management, and infrastructure operations. Each workflow is designed with fine-grained override points, allowing customers to customize behavior without forking code.

## Features

- **Override Pattern**: Replace any workflow step by passing override variables
- **Multi-level Customization**: Override at workflow, step, role, or template level
- **No Code Duplication**: Customers import workflows and pass overrides instead of duplicating playbooks

## Workflow Playbooks

### Cluster Workflows
- `osac.workflows.cluster.create` - Create hosted cluster from ClusterOrder
- `osac.workflows.cluster.delete` - Delete hosted cluster
- `osac.workflows.cluster.post_install` - Post-installation configuration

### Compute Instance Workflows
- `osac.workflows.compute_instance.create` - Create compute instance/VM
- `osac.workflows.compute_instance.delete` - Delete compute instance

### Reporting Workflows
- `osac.workflows.reporting.report_cluster_status` - Report cluster status

## Usage

### Basic Usage

Import a workflow playbook directly:

```yaml
---
- name: Create cluster
  ansible.builtin.import_playbook: osac.workflows.cluster.create
  vars:
    cluster_order: "{{ osac_job_vars.resource }}"
```

### Override a Workflow Step

Replace a specific step with your custom role:

```yaml
---
- name: Create cluster with custom infrastructure
  ansible.builtin.import_playbook: osac.workflows.cluster.create
  vars:
    cluster_order: "{{ osac_job_vars.resource }}"

    # Override infrastructure creation step
    template_step_create_infra_override:
      name: my_custom_infra
      tasks_from: main.yml
```

### Override Multiple Steps

```yaml
---
- name: Create cluster with multiple overrides
  ansible.builtin.import_playbook: osac.workflows.cluster.create
  vars:
    cluster_order: "{{ osac_job_vars.resource }}"

    # Override defaults application
    step_apply_defaults_override:
      name: my_defaults
      tasks_from: main.yml

    # Override template selection
    template_id_override: my.collection.custom_template

    # Override infrastructure
    template_step_create_infra_override:
      name: my_infra
      tasks_from: main.yml
```

## Override Variable Naming Convention

All override variables follow this pattern:

```
{phase}_{action}_override
```

Where:
- `phase`: Workflow phase (step, template_step)
- `action`: Specific action (apply_defaults, create_infra, etc.)
- `override`: Always ends with `_override`

Each override variable must have this structure:

```yaml
override_var:
  name: role_name_or_collection.namespace.role
  tasks_from: task_file.yml
```

## Common Override Points

### Cluster Creation Workflow

- `step_apply_defaults_override` - Override default settings application
- `step_extract_template_info_override` - Override template info extraction
- `step_determine_namespace_override` - Override namespace determination
- `step_acquire_lock_override` - Override lock acquisition
- `step_add_finalizer_override` - Override finalizer addition
- `step_call_template_override` - Override template invocation
- `template_id_override` - Override which template is called

### Template-Level Overrides

Templates may define their own override points:

- `template_step_create_hosted_cluster_override` - Override cluster creation
- `template_step_create_infra_override` - Override infrastructure setup
- `template_step_external_access_override` - Override external access configuration
- `template_step_retrieve_kubeconfig_override` - Override kubeconfig retrieval

## Example: Mass Open Cloud (MOC) Customization

```yaml
---
# MOC cluster creation with ESI integration
- name: MOC Cluster Creation
  ansible.builtin.import_playbook: osac.workflows.cluster.create
  vars:
    cluster_order: "{{ osac_job_vars.resource }}"

    # Use MOC template collection
    template_id_override: osac.templates.ocp_small

    # Override infrastructure to use ESI
    template_step_create_infra_override:
      name: moc_cluster_infra
      tasks_from: main.yml

    # Override external access for MOC networking
    template_step_external_access_override:
      name: moc_external_access
      tasks_from: main.yml
```

## Dependencies

- `osac.service` - Service orchestration roles
- `osac.templates` - Base cluster and VM templates (optional)
- `osac.esi` - ESI bare-metal API wrapper

## License

Apache License 2.0

## Support

For issues and questions, please use the [GitHub issue tracker](https://github.com/osac-project/osac/issues).
