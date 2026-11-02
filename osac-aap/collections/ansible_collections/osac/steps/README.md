# osac.steps

ESI-based step implementations for cluster infrastructure and external access provisioning.

## Roles

- `cluster_infra` — creates and deletes the bare metal pool and cluster network for a hosted cluster
- `external_access` — allocates floating IPs, configures DNS records, and sets up port forwarding for cluster API and ingress endpoints

## Usage

These are legacy ESI provider roles. CaaS templates use the OSAC Networking API
and the service-owned external-access entry points; the generic service roles
no longer select provider collections at runtime. Workflows that still need an
ESI role must call that role directly.

The service role's `caas_*` entry points use the inventory IP of a
ClusterOrder-labeled Agent for wildcard application DNS. Agent lifecycle is
owned by the operator and is not implemented in this provider step collection.

## License

Apache-2.0
