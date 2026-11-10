# Template Override Points Summary

This document describes all overrideable tasks in OSAC templates.

## VM Template: osac.templates.ocp_virt_vm

### Create Flow (tasks/create.yaml)

**7 overrideable steps** + 1 mandatory step.

| Step Name | Default Role | Override Variable | Description |
|-----------|-------------|-------------------|-------------|
| Validate | osac.templates.ocp_virt_vm (create_validate.yaml) | `create_step_validate_override` | Validates parameters, VM config, exposed_ports |
| **Build Spec** | osac.templates.ocp_virt_vm (create_build_spec.yaml) | **NOT OVERRIDEABLE** | **Builds VM template spec base** |
| Secrets | osac.templates.ocp_virt_vm (create_secrets.yaml) | `create_step_secrets_override` | Creates user-data and SSH secrets |
| Modify VM Spec Hook | osac.templates.ocp_virt_vm (create_modify_vm_spec.yaml) | `create_step_modify_vm_spec_override` | Hook to customize VM spec (noop by default) |
| Pre-create Hook | osac.templates.ocp_virt_vm (create_pre_create_hook.yaml) | `create_step_pre_create_hook_override` | Hook before resource creation (noop by default) |
| **Create Resources** | osac.templates.ocp_virt_vm (create_resources.yaml) | `create_step_resources_override` | **Creates DataVolumes and VirtualMachine** ⚠️ |
| Post-create Hook | osac.templates.ocp_virt_vm (create_post_create_hook.yaml) | `create_step_post_create_hook_override` | Hook after resource creation (noop by default) |
| Wait/Annotate | osac.templates.ocp_virt_vm (create_wait_annotate.yaml) | `create_step_wait_annotate_override` | Waits for VM and annotates ComputeInstance ⚠️ |

**Note**: Build Spec is not overrideable - use the Modify VM Spec Hook to customize the spec after it's built.

**Critical for Testing**: Override `create_step_resources_override` and `create_step_wait_annotate_override` to skip actual resource creation.

### Delete Flow (tasks/delete.yaml)

**NOT YET OVERRIDEABLE** - delete.yaml directly calls k8s tasks to delete resources.

Tasks performed:
1. Get VirtualMachine info
2. Stop VirtualMachine
3. Delete VirtualMachine
4. Delete Services
5. Delete DataVolumes
6. Delete Secrets

**TODO**: Refactor delete.yaml to add override hooks like create.yaml has.

---

## Cluster Template: osac.templates.ocp_small

### Install Flow (tasks/install.yaml)

**7 overrideable steps**.

| Step Name | Default Role | Override Variable | Description |
|-----------|-------------|-------------------|-------------|
| Pre-install Hook | osac.templates.ocp_small (noop.yaml) | `install_step_pre_install_hook_override` | Hook before cluster creation (noop by default) |
| **Create Hosted Cluster** | osac.service.hosted_cluster | `install_step_hosted_cluster_override` | **Creates HostedCluster and NodePool CRDs** ⚠️ |
| **Configure External Access** | osac.templates.ocp_small (`noop.yaml`) | `install_step_external_access_override` | No provider-specific dispatch; the ClusterOrder controller owns endpoint discovery, ingress reservation, DNS, and attachment ordering |
| Retrieve Kubeconfig | osac.service.retrieve_kubeconfig | `install_step_retrieve_kubeconfig_override` | Retrieves admin kubeconfig from cluster |
| Wait for Nodes | osac.service.wait_for (`wait_for_nodes.yaml`) | `install_step_wait_for_nodes_override` | Waits for all requested cluster nodes to be ready |
| Wait for Cluster Operators | osac.service.wait_for (`wait_for_cluster_operators.yaml`) | `install_step_wait_for_cluster_operators_override` | Waits for cluster operators to become available |
| Post-install Hook | osac.templates.ocp_small (noop.yaml) | `install_step_post_install_hook_override` | Hook after cluster creation (noop by default) |

**Critical for Testing**: Override hosted-cluster and external-access steps to
avoid those AAP steps creating resources or DNS records. The external-access
override replaces only the AAP step; it does not disable the ClusterOrder
controller's endpoint lifecycle. If a test runs the controller with an
eligible HostedCluster and provider networking enabled, the controller may
still discover or reserve ingress, create automatic attachments, and publish
DNS. Control the controller lifecycle separately in those tests.

### Delete Flow (tasks/delete.yaml)

**4 overrideable steps**.

| Step Name | Default Role | Override Variable | Description |
|-----------|-------------|-------------------|-------------|
| Pre-delete Hook | osac.templates.ocp_small (noop.yaml) | `delete_step_pre_delete_hook_override` | Hook before cluster deletion (noop by default) |
| **Destroy Hosted Cluster** | osac.service.hosted_cluster | `delete_step_hosted_cluster_override` | **Deletes HostedCluster and NodePool CRDs** ⚠️ |
| **Remove External Access** | osac.templates.ocp_small (`noop.yaml`) | `delete_step_external_access_override` | No provider-specific dispatch; the ClusterOrder controller removes DNS and automatic endpoint resources in dependency order |
| Post-delete Hook | osac.templates.ocp_small (noop.yaml) | `delete_step_post_delete_hook_override` | Hook after cluster deletion (noop by default) |

**Critical for Testing**: Override hosted-cluster and external-access steps to
avoid those AAP steps deleting resources or DNS records. The external-access
override replaces only the AAP step; it does not disable the ClusterOrder
controller's endpoint lifecycle. If a test runs the controller with an
eligible HostedCluster and provider networking enabled, the controller may
still remove automatic attachments and DNS. Control the controller lifecycle
separately in those tests.

## CI Cluster Template: osac.templates.ocp_ci_small

The CI CaaS template uses the service's dedicated DNS discovery and record
tasks instead of the controller-owned endpoint lifecycle used by `ocp_small`.

### Install and Delete Flows

| Step Name | Default Role | Override Variable | Description |
|-----------|-------------|-------------------|-------------|
| **Configure External Access** | osac.service.external_access (`caas_create.yaml`) | `install_step_external_access_override` | Discovers the CI cluster API IP from the `kube-apiserver` Service and the ingress IP from ClusterOrder-labeled Agent inventory, creates DNS records, and publishes endpoints |
| **Remove External Access** | osac.service.external_access (`caas_delete.yaml`) | `delete_step_external_access_override` | Deletes the CI cluster DNS records and clears endpoint annotations |

### Post-Install Flow (tasks/post_install.yaml)

**NOT OVERRIDEABLE** - operates on the hosted cluster itself (not management cluster).

Tasks performed:
1. Create openshift-operators namespace
2. Install cert-manager Subscription
3. Create ClusterIssuers (Let's Encrypt production/staging)

**Note**: Requires actual cluster with OLM. Tests must use noop template for post_install.

---

## Test Override Patterns

### Baseline Tests (validate template execution without creating resources)

**Compute Instance Create/Delete**:
```yaml
# Use real template from fixture: osac.templates.ocp_virt_vm
create_step_resources_override:
  name: osac.workflows.workflow_helpers
  tasks_from: noop.yml
create_step_wait_annotate_override:
  name: osac.workflows.workflow_helpers
  tasks_from: noop.yml
```

**Cluster Create**:
```yaml
# Use real template from fixture: osac.templates.ocp_small
install_step_hosted_cluster_override:
  name: osac.workflows.workflow_helpers
  tasks_from: noop.yml
install_step_external_access_override:
  name: osac.workflows.workflow_helpers
  tasks_from: noop.yml
install_step_wait_for_nodes_override:
  name: osac.workflows.workflow_helpers
  tasks_from: noop.yml
install_step_wait_for_cluster_operators_override:
  name: osac.workflows.workflow_helpers
  tasks_from: noop.yml
install_step_retrieve_kubeconfig_override:
  name: osac.workflows.workflow_helpers
  tasks_from: noop.yml
```

**Cluster Delete**:
```yaml
# Use real template from fixture: osac.templates.ocp_small
delete_step_hosted_cluster_override:
  name: osac.workflows.workflow_helpers
  tasks_from: noop.yml
delete_step_external_access_override:
  name: osac.workflows.workflow_helpers
  tasks_from: noop.yml
```

**Cluster Post-Install**:
```yaml
# Must use noop template - post_install.yaml operates on hosted cluster
template_id_override: osac.test_overrides.noop_template
```

### Override Tests (validate override mechanism works)

**Compute Instance Create**:
```yaml
# Use real template from fixture: osac.templates.ocp_virt_vm
# Override 7 steps with test hooks that:
# - Log execution to /tmp/osac_test_overrides.log
# - For validate/modify_vm_spec/pre_create/post_create: delegate to real role
# - For secrets/resources/wait_annotate: skip (noop)
# Note: build_spec is not overrideable (critical step)

create_step_validate_override:
  name: osac.test_overrides.vm_create_hooks
  tasks_from: validate.yml

create_step_secrets_override:
  name: osac.test_overrides.vm_create_hooks
  tasks_from: secrets.yml

create_step_modify_vm_spec_override:
  name: osac.test_overrides.vm_create_hooks
  tasks_from: modify_vm_spec.yml

create_step_pre_create_hook_override:
  name: osac.test_overrides.vm_create_hooks
  tasks_from: pre_create_hook.yml

create_step_resources_override:
  name: osac.test_overrides.vm_create_hooks
  tasks_from: resources.yml

create_step_post_create_hook_override:
  name: osac.test_overrides.vm_create_hooks
  tasks_from: post_create_hook.yml

create_step_wait_annotate_override:
  name: osac.test_overrides.vm_create_hooks
  tasks_from: wait_annotate.yml
```

**Compute Instance Delete**:
```yaml
# Must use noop template - delete.yaml not yet overrideable
template_id_override: osac.test_overrides.noop_template

# Override noop template's 3 hooks
delete_step_pre_delete_hook_override:
  name: osac.test_overrides.vm_delete_hooks
  tasks_from: pre_delete_hook.yml

delete_step_resources_override:
  name: osac.test_overrides.vm_delete_hooks
  tasks_from: resources.yml

delete_step_post_delete_hook_override:
  name: osac.test_overrides.vm_delete_hooks
  tasks_from: post_delete_hook.yml
```

**Cluster Create/Delete/Post-Install**:
```yaml
# Use test_template (has more hooks than real template)
template_id_override: osac.test_overrides.test_template
```

---

## Tasks Requiring Real Infrastructure

The following tasks **cannot be tested without real infrastructure**:

### CaaS Networking
- Networking resources and Agent lifecycle are managed through the OSAC
  Networking API and operator.
- Standard CaaS endpoint DNS and status are owned by the ClusterOrder
  controller; CI and NICo external access publish endpoint annotations.

### AAP Resources
- Not currently used by any workflows in the collection

### Hosted Cluster Resources
- Post-install tasks that run on the hosted cluster (not management cluster)
- Requires actual running OpenShift cluster
- Used by: cluster_post_install

---

## Current Test Status

### Using Real Templates
- ✅ cluster_create baseline (overrides all install steps)
- ✅ cluster_delete baseline (overrides all delete steps)
- ✅ compute_instance_create baseline (overrides resources + wait)
- ✅ compute_instance_create override (uses real template with test hooks)
- ✅ compute_instance_delete baseline (TODO: needs delete.yaml refactor)

### Using Noop Templates (TODO: convert to real templates)
- ⚠️ cluster_post_install baseline (post_install.yaml requires real cluster)
- ⚠️ compute_instance_delete override (delete.yaml not yet overrideable)
- ⚠️ cluster_create override (test_template has extra hooks not in real template)
- ⚠️ cluster_delete override (test_template has extra hooks not in real template)
- ⚠️ cluster_post_install override (test_template has extra hooks not in real template)
