# AAP Configuration

The OSAC automation backend (AAP) receives its runtime configuration via
Kubernetes ConfigMaps and Secrets that are mounted into AAP instance group
execution pods via `envFrom`. This makes every key available as an environment
variable during playbook execution.

Currently only the **cluster-fulfillment-ig** instance group mounts these
variables, as it is the only instance group that consumes them (for cluster provisioning workflows).
This will be expanded to additional instance groups in the future as more automation workflows are added.

## How It Works

Instance group ConfigMaps and Secrets are managed by the osac-aap subchart
via Helm values under `aap.instanceGroups.*`. Configure them in your values
file and they are applied during `make install-osac`.

## ConfigMap Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NETWORK_CLASS` | `esi` | Network backend (`netris` or `esi`) |
| `NETWORK_STEPS_COLLECTION` | `osac.steps` | Ansible collection for network steps |
| `EXTERNAL_ACCESS_BASE_DOMAIN` | `box.massopen.cloud` | Base domain for cluster DNS records |
| `EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS` | `box.massopen.cloud` | Comma-separated list of allowed domains |
| `EXTERNAL_ACCESS_API_INTERNAL_NETWORK` | `hypershift` | Internal network for API access |
| `HOSTED_CLUSTER_BASE_DOMAIN` | `box.massopen.cloud` | Base domain for hosted clusters |
| `HOSTED_CLUSTER_CONTROLLER_AVAILABILITY_POLICY` | `HighlyAvailable` | Control plane HA policy |
| `HOSTED_CLUSTER_INFRASTRUCTURE_AVAILABILITY_POLICY` | `HighlyAvailable` | Infrastructure HA policy |
| `DNS_CLASS` | `dns.route53.dns` | DNS backend driver role (see [DNS Backend](dns-backend.md)) |
| `DNS_ZONE` | `EXTERNAL_ACCESS_BASE_DOMAIN` | DNS zone for record management |

## Secret Variables

Values must be **plaintext** — the script base64-encodes them when patching the
Kubernetes Secret. Do not pre-encode them.

| Variable | Description |
|----------|-------------|
| `AWS_ACCESS_KEY_ID` | AWS credentials for Route53 DNS |
| `AWS_SECRET_ACCESS_KEY` | AWS credentials for Route53 DNS |

Additional variables are added by specific network backends — see
[Network Backend Configuration](network-backend.md).

> **Note on SSH keys:** SSH private keys are not set via environment variables.
> They must be added directly to the `cluster-fulfillment-ig` Kubernetes Secret.

## Reference

See `../osac-aap/config/base/configmap-cluster-fulfillment-ig-example.yaml` and
`../osac-aap/config/base/secret-cluster-fulfillment-ig-example.yaml` for full
examples.
