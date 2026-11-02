# osac.steps

ESI-based step implementations for cluster infrastructure and external access provisioning.

## Roles

- `cluster_infra` — creates and deletes the bare metal pool and cluster network for a hosted cluster
- `external_access` — allocates floating IPs, configures DNS records, and sets up port forwarding for cluster API and ingress endpoints

## Usage

This collection can be selected at runtime via the `network_steps_collection`
variable for legacy/provider-driven workflows. The generic
`osac.service.cluster_infra` and `osac.service.external_access` roles dispatch
through that variable.

The CaaS `osac.templates.ocp_ci_small` workflow uses the service role's
`caas_*` external-access entry points. The current CI endpoint contract uses
the inventory IP of a ClusterOrder-labeled Agent for wildcard application DNS;
the service role waits for that endpoint data directly. Agent lifecycle is
owned by the operator and is not implemented in this provider step collection.

## License

Apache-2.0
