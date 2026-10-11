# osac.steps

ESI-based step implementations for cluster infrastructure and external access provisioning.

## Roles

- `cluster_infra` — creates and deletes the bare metal pool and cluster network for a hosted cluster
- `external_access` — allocates floating IPs, configures DNS records, and sets up port forwarding for cluster API and ingress endpoints

## Usage

This collection provides the ESI network backend roles `cluster_infra` and `external_access`.

## License

Apache-2.0
