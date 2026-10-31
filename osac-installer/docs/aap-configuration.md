# AAP Configuration

The OSAC automation backend (AAP) receives its runtime configuration via
Kubernetes ConfigMaps and Secrets that are mounted into AAP instance group
execution pods via `envFrom`. This makes every key available as an environment
variable during playbook execution.

Two instance groups carry networking and CaaS settings:

- `cluster-fulfillment-ig` (`aap.instanceGroups.clusterFulfillment`): CaaS
  settings such as the hosted-cluster and external-access DNS settings below.
- `network-fulfillment-ig` (`aap.instanceGroups.networkFulfillment`): the
  network managers' settings (`config`, a ConfigMap) and credentials
  (`secret`, a Secret), written verbatim. Each key starts with the manager's
  registration name in upper case, for example `NETRIS_CONTROLLER_URL`,
  `NETRIS_PASSWORD` or `NETRIS_SITE_ID`. See
  [Networking configuration](network-backend.md).

The CaaS Netris steps (`NETWORK_CLASS=netris`) take the Netris controller URL,
certificate validation and CA ConfigMap from the Netris settings in
`aap.instanceGroups.networkFulfillment.config`; see
[CaaS on Netris](network-backend.md#caas-on-netris).

## How It Works

Instance group ConfigMaps and Secrets are managed by the osac-aap subchart
via Helm values under `aap.instanceGroups.*`. Configure them in your values
file and they are applied during `make install-osac`.

## ConfigMap Variables

| Variable | Default | Description |
|----------|---------|-------------|
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

Network manager credentials go in `aap.instanceGroups.networkFulfillment.secret`
(Secret `network-fulfillment-ig`).

> **Note on SSH keys:** SSH private keys are not set via environment variables.
> They must be added directly to the `cluster-fulfillment-ig` Kubernetes Secret.

## Reference

See `../osac-aap/config/base/configmap-cluster-fulfillment-ig-example.yaml` and
`../osac-aap/config/base/secret-cluster-fulfillment-ig-example.yaml` for full
examples.
