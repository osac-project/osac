# osac.steps

ESI-based step implementations for cluster infrastructure and external access provisioning.

## Roles

- `cluster_infra` — creates and deletes the bare metal pool and cluster network for a hosted cluster
- `external_access` — allocates floating IPs, configures DNS records, and sets up port forwarding for cluster API and ingress endpoints

## Usage

These are legacy ESI provider roles. Standard `ocp_small` CaaS endpoint
lifecycle is owned by the ClusterOrder controller and generic networking tasks;
it does not call these roles. CI keeps the service role's `caas_*` DNS tasks,
and NICo keeps its direct provider-specific task overrides. Workflows that
still need an ESI role must call that role directly.

The CI `caas_*` tasks discover the API IP from the `kube-apiserver` Service and
the ingress IP from ClusterOrder-labeled Agent inventory. Agent lifecycle is
owned by the operator and is not implemented in this provider step collection.

## License

Apache-2.0
