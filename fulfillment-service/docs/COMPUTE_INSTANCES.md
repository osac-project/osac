# ComputeInstances API: resize with Update

Use `osac.public.v1.ComputeInstances/Update` to select a different
InstanceType for an existing ComputeInstance. The public REST route is
`PATCH /api/fulfillment/v1/compute_instances/{object.id}`. The request carries
the ComputeInstance in `object`. An Update can include `update_mask` to select
paths, or omit it to replace the full object.

For a masked resize, set `object.id`, set `object.spec.instance_type` to a
target `InstanceTypeReference`, and include `spec.instance_type` in
`update_mask.paths`. The `osac edit` CLI submits a full-object Update without
a mask. The target may have more or fewer vCPUs and more or less memory than
the current type. A stopped VM is eligible; it uses the selected type when it
next starts. For a running VM, the selected type may require a restart before
its CPU and memory configuration takes effect.

The Update response contains the updated `object` and, on gRPC, a repeated
`warnings` field. Selecting a DEPRECATED target succeeds and returns a
deprecation warning. The warning may include an obsolescence date or a
suggested replacement when the InstanceType provides them. The `osac edit`
CLI prints each warning to standard error. The REST annotation uses
`response_body: "object"`, so the REST response body contains the updated
ComputeInstance without the sibling `warnings` field.

| Target or change | Update outcome |
|---|---|
| Existing ACTIVE type with the same GPU configuration | Accepted |
| Existing DEPRECATED type with the same GPU configuration | Accepted; gRPC response includes a warning |
| Current type selected again | Successful no-op for an instance-type-only update, even if its lifecycle state has changed |
| Missing target type | `InvalidArgument` |
| OBSOLETE target type | `FailedPrecondition` |
| Target with a different GPU configuration | `FailedPrecondition` |

GPU configuration must match exactly: a resize cannot add, remove, or change
GPU devices. The same-type no-op is checked before target lifecycle and GPU
validation. Other fields included in the same update are still processed; the
no-op guarantee applies to an update whose mask contains only
`spec.instance_type`.

After an accepted resize, inspect the returned ComputeInstance or fetch it
again. `status.conditions` includes `ConfigurationApplied` and
`RestartRequired`. `ConfigurationApplied=True` reports that the desired VM
configuration has been applied by the provisioning provider;
`RestartRequired=True` means a running VM still needs a restart for the new
resources to take effect. Request that restart separately by updating
`spec.restart_requested_at` to a UTC timestamp later than
`status.last_restarted_at`. This timestamp is a declarative restart signal,
not a scheduled time.
