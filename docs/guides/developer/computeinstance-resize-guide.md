# Resizing a VM with an InstanceType

This guide shows Tenant Admins and Tenant Users how to change a
ComputeInstance's vCPU and memory configuration with the `osac` CLI. On the
restart-required path, a running VM needs a separate restart request before
the new resources take effect. Plan for an interruption to workloads running
inside the guest.

For the Update API contract, including gRPC response warnings and error codes,
see the [ComputeInstances API reference](../../../fulfillment-service/docs/COMPUTE_INSTANCES.md).

## Prerequisites

- Install and log in to the `osac` CLI, with access to the VM and its tenant.
- Choose an existing InstanceType available to your tenant. It may have more
  or fewer vCPUs or memory than the current type. Its GPU configuration must
  match the current type exactly; adding, removing, or changing GPUs through
  resize is rejected.
- Arrange a maintenance window for a running VM. A restart interrupts guest
  workloads; applications may need their own checks after the VM returns.

## 1. Inspect the VM and target type

```bash
osac get computeinstance <vm-name-or-id> -o yaml
osac get instancetype
osac get instancetype <target-type-name-or-id> -o yaml
```

Note the current `spec.instance_type`, the VM's `status.state`, and the target
type's `spec.vcpus`, `spec.memory_gib`, `spec.gpu`, and `spec.state`. Choose an
ACTIVE type when possible. A DEPRECATED type is accepted with a warning;
an OBSOLETE type is rejected. For details about InstanceType lifecycle states,
see [Managing Instance Types](instancetype-guide.md).

## 2. Select the new InstanceType

```bash
osac edit computeinstance <vm-name-or-id>
```

The command opens the current ComputeInstance as YAML in your editor. Replace
the `spec.instance_type` reference with the target, for example:

```yaml
spec:
  instance_type:
    name: standard-8
```

This is an excerpt of the object in the editor. Replace the entire
`instance_type` mapping: remove the **old** `id`, `name`, `project`, and
`shared` values, then add the target's name. If both an old ID and a new
name remain, the ID selects the old type and the mismatched name can cause
the update to fail. Add `shared` or `project` only when they identify the
target type's scope. Leave the rest of the VM unchanged, then save and close
the editor. The CLI sends the edited ComputeInstance through
`ComputeInstances.Update` as a full-object update.

An ACTIVE target succeeds without a lifecycle warning. A DEPRECATED target
succeeds and the CLI prints `Warning: ... is deprecated` to standard error;
the notice may also include an obsolescence date or replacement. A missing
target returns `InvalidArgument`; an OBSOLETE target or GPU change returns
`FailedPrecondition`.

## 3. Check whether a restart is required

```bash
osac get computeinstance <vm-name-or-id> -o yaml
```

Check `status.conditions` for `CONFIGURATION_APPLIED` and
`RESTART_REQUIRED`. The enum values in YAML have the
`COMPUTE_INSTANCE_CONDITION_TYPE_` prefix; their statuses use
`CONDITION_STATUS_TRUE` or `CONDITION_STATUS_FALSE`.
`ConfigurationApplied=True` means the provisioning provider applied the
desired VM configuration. `RestartRequired=True` means a running VM still
needs a restart for the new resources to take effect. Allow reconciliation
to finish before deciding whether to restart. If configuration application
fails, inspect the condition message and contact your platform administrator.

If the VM is stopped, the selected type takes effect when it next starts.
The stopped VM does not need the manual restart request in step 4.

## 4. Restart a running VM when required

When `RestartRequired=True`, generate a current UTC timestamp:

```bash
date -u +%Y-%m-%dT%H:%M:%SZ
osac edit computeinstance <vm-name-or-id>
```

In the editor, set `spec.restart_requested_at` to the timestamp from the
first command, for example:

```yaml
spec:
  restart_requested_at: "2026-10-01T14:30:00Z"
```

Use a value later than `status.last_restarted_at`; if the timestamps would
fall in the same second, wait and generate a new one. Save and close the
editor. The timestamp requests a restart immediately; it does not schedule
one for the specified time. Keep the selected `instance_type` unchanged.

## 5. Confirm the result

```bash
osac get computeinstance <vm-name-or-id> -o yaml
```

Repeat the command while the restart completes. Check that the VM returns to
`COMPUTE_INSTANCE_STATE_RUNNING`, `ConfigurationApplied=True`, and
`RestartRequired=False`. `status.last_restarted_at` should reflect the
requested restart. If `RestartFailed=True` or `ConfigurationApplied=False`
persists, inspect the condition messages and contact your platform
administrator. After the restart, check CPU, memory, and application health
inside the guest; guest operating systems and workloads may need their own
adjustments to use the new resources.
