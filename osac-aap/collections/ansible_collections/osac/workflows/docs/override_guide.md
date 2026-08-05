# OSAC Workflows Override Guide

This guide explains how to customize OSAC workflows using the override pattern.

## Overview

The osac.workflows collection allows you to customize any workflow step without forking or duplicating code. Each workflow defines override points where you can inject your own logic.

## How Overrides Work

### Basic Pattern

Every workflow step follows this pattern:

```yaml
- name: Step - Do something
  ansible.builtin.include_role:
    name: "{{ (step_name_override | default(step_name_default)).name }}"
    tasks_from: "{{ (step_name_override | default(step_name_default)).tasks_from }}"
```

If you pass `step_name_override`, your role is called. Otherwise, the default role is used.

### Override Variable Structure

All override variables must follow this structure:

```yaml
step_name_override:
  name: collection.namespace.role_name  # or just role_name for local roles
  tasks_from: task_file.yml
```

## Extension Point Types

OSAC workflows provide three types of customization:

### 1. Generic Hooks (Workflow Boundaries)

Add custom logic at the very beginning or end of workflows:

```yaml
---
- name: Cluster creation with monitoring hooks
  ansible.builtin.import_playbook: osac.workflows.cluster.create
  vars:
    cluster_order: "{{ osac_job_vars.resource }}"

    # Hook at workflow start for initialization
    hook_workflow_start:
      name: my_hooks
      tasks_from: initialize.yml

    # Hook at workflow end for notifications
    hook_workflow_complete:
      name: my_hooks
      tasks_from: finalize.yml
```

**Use hooks for:**
- Validation and prerequisites
- External API notifications
- Metrics collection
- Logging and monitoring
- Compliance checks

### 2. Modification Hooks (YAML Transformation)

Modify Kubernetes resource definitions before they're applied:

```yaml
---
- name: Cluster with custom labels
  ansible.builtin.import_playbook: osac.workflows.cluster.create
  vars:
    cluster_order: "{{ osac_job_vars.resource }}"

    # Modify HostedCluster YAML
    hosted_cluster_modify_definition_hook:
      name: my_modifications
      tasks_from: cluster_definition.yml

    # Modify NodePool YAML
    nodepool_modify_definitions_hook:
      name: my_modifications
      tasks_from: nodepool_definitions.yml
```

**Use modification hooks for:**
- Adding custom labels/annotations
- Changing networking configuration
- Modifying resource requests/limits
- Adjusting replica counts

### 3. Phase Overrides (Replace Major Steps)

Replace entire workflow phases:

```yaml
---
- name: Cluster with custom namespace logic
  ansible.builtin.import_playbook: osac.workflows.cluster.create
  vars:
    cluster_order: "{{ osac_job_vars.resource }}"

    # Replace namespace determination
    step_determine_namespace_override:
      name: my_namespace_logic
      tasks_from: main.yml

    # Use different template
    template_id_override: my.collection.custom_template
```

**Use phase overrides for:**
- Custom default logic
- Custom namespace patterns
- External locking systems
- Custom finalizers
- Alternative templates

## Extension Points Reference

### Cluster Create Workflow (`osac.workflows.cluster.create`)

#### Generic Hooks (2)
- `hook_workflow_start` - Very beginning (validation, logging, initialization)
- `hook_workflow_complete` - Very end (notifications, metrics, cleanup)

#### Modification Hooks (2)
- `hosted_cluster_modify_definition_hook` - Transform HostedCluster YAML before applying
- `nodepool_modify_definitions_hook` - Transform NodePool YAML before applying

#### Phase Overrides (6)
- `step_apply_defaults_override` - Replace default settings logic
- `step_determine_namespace_override` - Replace namespace determination
- `step_acquire_lock_override` - Replace locking mechanism
- `step_add_finalizer_override` - Replace finalizer addition
- `step_call_template_override` - Replace template execution
- `template_id_override` - Select different template

#### Protected Critical Steps (NOT Overrideable)

These steps cannot be overridden to protect workflow integrity:
- Cluster order name extraction
- Lock holder ID generation
- Template info parsing
- All Kubernetes resource creation (but YAML can be modified via hooks)

### Cluster Delete Workflow (`osac.workflows.cluster.delete`)

#### Generic Hooks (2)
- `hook_workflow_start` - Very beginning (validation, logging, pre-delete checks)
- `hook_workflow_complete` - Very end (notifications, metrics, cleanup confirmation)

#### Phase Overrides (4)
- `step_apply_defaults_override` - Replace default settings logic
- `step_determine_namespace_override` - Replace namespace determination
- `step_call_template_override` - Replace template delete execution
- `step_remove_finalizer_override` - Replace finalizer removal
- `template_id_override` - Select different template

#### Protected Critical Steps (NOT Overrideable)

These steps cannot be overridden to protect workflow integrity:
- Cluster order name extraction
- Template info parsing

### Cluster Post-Install Workflow (`osac.workflows.cluster.post_install`)

#### Generic Hooks (2)
- `hook_workflow_start` - Very beginning (validation, logging, pre-configuration checks)
- `hook_workflow_complete` - Very end (notifications, metrics, verification)

#### Phase Overrides (2)
- `step_apply_defaults_override` - Replace default settings logic
- `step_call_template_override` - Replace template post_install execution
- `template_id_override` - Select different template

#### Protected Critical Steps (NOT Overrideable)

These steps cannot be overridden to protect workflow integrity:
- Cluster order name extraction
- Template info parsing

#### Special Environment

This workflow sets `KUBECONFIG` environment variable from `admin_kubeconfig` variable.
Ensure `admin_kubeconfig` contains the cluster's admin kubeconfig content.

## Complete Example: MOC Integration

Here's how Mass Open Cloud uses hooks and modifications for ESI integration:

### MOC Repository Structure

```
osac-aap-moc/
├── ansible.cfg
├── requirements.yml
├── playbooks/
│   └── cluster_create.yml
└── roles/
    ├── moc_hooks/
    │   └── tasks/
    │       ├── workflow_start.yml
    │       └── workflow_complete.yml
    └── moc_modifications/
        └── tasks/
            ├── cluster_definition.yml
            └── nodepool_definitions.yml
```

### MOC Playbook (playbooks/cluster_create.yml)

```yaml
---
- name: MOC Cluster Creation with ESI
  ansible.builtin.import_playbook: osac.workflows.cluster.create
  vars:
    cluster_order: "{{ osac_job_vars.resource }}"

    # Use MOC template
    template_id_override: osac.templates.ocp_small

    # Generic hooks for monitoring
    hook_workflow_start:
      name: moc_hooks
      tasks_from: workflow_start.yml

    hook_workflow_complete:
      name: moc_hooks
      tasks_from: workflow_complete.yml

    # Modification hooks for ESI-specific YAML
    hosted_cluster_modify_definition_hook:
      name: moc_modifications
      tasks_from: cluster_definition.yml

    nodepool_modify_definitions_hook:
      name: moc_modifications
      tasks_from: nodepool_definitions.yml
```

### MOC Hook Role (roles/moc_hooks/tasks/workflow_start.yml)

```yaml
---
- name: Record workflow start time
  ansible.builtin.set_fact:
    moc_workflow_start_time: "{{ ansible_date_time.epoch }}"

- name: Notify monitoring system
  uri:
    url: "https://monitoring.moc.edu/api/events"
    method: POST
    body_format: json
    body:
      event: cluster_create_started
      cluster: "{{ cluster_order_name }}"

- name: Validate ESI prerequisites
  ansible.builtin.assert:
    that:
      - cluster_order.spec.templateID is defined
      - cluster_order.metadata.name is defined
```

### MOC Hook Role (roles/moc_hooks/tasks/workflow_complete.yml)

```yaml
---
- name: Send Slack notification
  uri:
    url: "{{ slack_webhook_url }}"
    method: POST
    body_format: json
    body:
      text: "Cluster {{ cluster_order_name }} created successfully"

- name: Record metrics
  uri:
    url: "https://metrics.moc.edu/api/push"
    method: POST
    body_format: json
    body:
      metric: cluster_creation_duration_seconds
      value: "{{ ansible_date_time.epoch | int - moc_workflow_start_time | int }}"
```

### MOC Modification Role (roles/moc_modifications/tasks/cluster_definition.yml)

```yaml
---
# Add MOC-specific labels
- name: Add MOC labels to HostedCluster
  ansible.builtin.set_fact:
    hosted_cluster_definition: >-
      {{ hosted_cluster_definition | combine({
        'metadata': {
          'labels': hosted_cluster_definition.metadata.labels | combine({
            'moc.mass.edu/region': 'nerc',
            'moc.mass.edu/billing-project': 'osac-demo'
          })
        }
      }, recursive=true) }}

# Customize networking for MOC
- name: Customize HostedCluster networking
  ansible.builtin.set_fact:
    hosted_cluster_definition: >-
      {{ hosted_cluster_definition | combine({
        'spec': {
          'networking': {
            'clusterNetwork': [{'cidr': '10.200.0.0/14'}],
            'serviceNetwork': [{'cidr': '172.50.0.0/16'}]
          }
        }
      }, recursive=true) }}

# Add MOC annotations
- name: Add MOC annotations
  ansible.builtin.set_fact:
    hosted_cluster_definition: >-
      {{ hosted_cluster_definition | combine({
        'metadata': {
          'annotations': {
            'moc.mass.edu/created-by': 'osac-fulfillment',
            'moc.mass.edu/esi-network': 'network-{{ hosted_cluster_name }}'
          }
        }
      }, recursive=true) }}
```

### MOC Modification Role (roles/moc_modifications/tasks/nodepool_definitions.yml)

```yaml
---
# Add MOC labels to all NodePools
- name: Add MOC labels to NodePools
  ansible.builtin.set_fact:
    nodepool_definitions: >-
      {{ nodepool_definitions | map('combine', {
        'metadata': {
          'labels': item.metadata.labels | combine({
            'moc.mass.edu/region': 'nerc'
          })
        }
      }, recursive=true) | list }}
  loop: "{{ nodepool_definitions }}"
```

## Customization Patterns

### Pattern 1: Generic Hooks (Recommended for Side Effects)

Use hooks for external integrations and monitoring:

```yaml
---
# Hook role for workflow start
- name: Initialize external systems
  # Validation, logging, API calls

- name: Record workflow start
  ansible.builtin.set_fact:
    workflow_start_time: "{{ ansible_date_time.epoch }}"
```

**Best for:** Notifications, validation, metrics, logging

### Pattern 2: Modification Hooks (Recommended for YAML Changes)

Transform resource definitions using Ansible combine filter:

```yaml
---
# Modification hook role
- name: Add custom labels
  ansible.builtin.set_fact:
    hosted_cluster_definition: >-
      {{ hosted_cluster_definition | combine({
        'metadata': {
          'labels': hosted_cluster_definition.metadata.labels | combine({
            'custom-label': 'custom-value'
          })
        }
      }, recursive=true) }}

- name: Modify networking
  ansible.builtin.set_fact:
    hosted_cluster_definition: >-
      {{ hosted_cluster_definition | combine({
        'spec': {
          'networking': {
            'clusterNetwork': [{'cidr': '10.200.0.0/14'}]
          }
        }
      }, recursive=true) }}
```

**Best for:** Labels, annotations, networking, resource limits

### Pattern 3: Phase Override - Wrapper

Override role calls vendor role with custom pre/post logic:

```yaml
---
# Your override role wraps vendor logic
- name: Custom pre-processing
  # Your custom tasks

- name: Call vendor implementation
  ansible.builtin.include_role:
    name: osac.service.original_role

- name: Custom post-processing
  # Your custom tasks
```

**Best for:** Adding custom steps around vendor logic

### Pattern 4: Phase Override - Delegation

Override role delegates to multiple specialized roles:

```yaml
---
# Your override orchestrates multiple roles
- name: Custom network setup
  ansible.builtin.include_role:
    name: my.custom.network

- name: Vendor agent management
  ansible.builtin.include_role:
    name: osac.service.manage_agents

- name: Custom registration
  ansible.builtin.include_role:
    name: my.custom.registration
```

**Best for:** Complex custom workflows

### Pattern 5: Phase Override - Complete Replacement

Override role completely replaces vendor logic:

```yaml
---
# Your override is completely custom
- name: Custom implementation
  # All your custom tasks
  # No calls to vendor roles
```

**Best for:** Fundamentally different implementations

## Testing Overrides

### Syntax Validation

```bash
# Check syntax
ansible-playbook --syntax-check playbooks/cluster_create.yml

# Lint playbook
ansible-lint playbooks/cluster_create.yml
```

### Dry Run

```bash
# Run with --check to see what would happen
ansible-playbook playbooks/cluster_create.yml \
  --extra-vars "cluster_order=$(cat test-cluster-order.json)" \
  --check
```

### Override Resolution Test

```bash
# Test that override is actually applied
# This should fail with "role test_override not found"
ansible-playbook playbooks/cluster_create.yml \
  --extra-vars "step_apply_defaults_override={'name': 'test_override', 'tasks_from': 'main.yml'}" \
  --check
```

## Troubleshooting

### Override Not Being Applied

**Problem**: Your override role is not being called.

**Solution**: Verify the override variable structure:

```yaml
# Wrong - missing tasks_from
step_name_override:
  name: my_role

# Wrong - typo in name
step_naem_override:  # typo!
  name: my_role
  tasks_from: main.yml

# Correct
step_name_override:
  name: my_role
  tasks_from: main.yml
```

### Variables Not Passed to Override Role

**Problem**: Override role doesn't have access to workflow variables.

**Solution**: Workflow variables are automatically available. Ensure your role references the correct variable names:

```yaml
# In your override role, these are available:
- debug:
    msg: "Cluster name: {{ cluster_order_name }}"
    msg: "Working namespace: {{ cluster_working_namespace }}"
    msg: "Template ID: {{ template_id }}"
```

### Override Role Not Found

**Problem**: Error "could not find role xyz"

**Solutions**:
1. Ensure role is in `roles/` directory of your repository
2. Check `roles_path` in `ansible.cfg`
3. If using collection role, verify collection is installed

```bash
# Check installed collections
ansible-galaxy collection list

# Install missing collections
ansible-galaxy collection install -r requirements.yml
```

## Best Practices

1. **Use Wrapper Pattern**: Call vendor roles from your overrides when possible
2. **Document Overrides**: Add comments explaining why you override
3. **Test Locally**: Use kind clusters to test before AAP deployment
4. **Version Pin**: Pin collection versions in requirements.yml
5. **Minimal Overrides**: Only override what you need to change
6. **Keep in Sync**: Monitor vendor workflow updates for new override points

## Getting Help

- Check example overrides in the proof of concept: `/aap-override/aap-shadow-customized`
- Review MOC implementation for real-world patterns
- Open issues at https://github.com/osac-project/osac/issues
