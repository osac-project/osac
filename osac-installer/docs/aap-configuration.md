# AAP Configuration

The OSAC automation backend (AAP) receives its runtime configuration via
Kubernetes ConfigMaps and Secrets that are mounted into AAP instance group
execution pods via `envFrom`. This makes every key available as an environment
variable during playbook execution.

For network backends, prefer the `global.networking` facade — see
[Network Backend Configuration](network-backend.md). Helm derives
`NETWORK_CLASS`, `NETWORK_STEPS_COLLECTION`, and Netris connection fields when
`global.expertOverrides.aap` is false. Set those keys manually only for expert
overrides or CI-specific backends (for example `ci.steps`).

Cluster-fulfillment and network-fulfillment instance groups both mount their
ConfigMaps and Secrets. Network-fulfillment receives the shared Netris
connection fields; cluster-fulfillment also receives cluster-specific Netris
fields and hosted-cluster settings.

## Protected Netris settings

The installer owns the Netris controller URL, certificate-validation mode, and
CA path. Managed workers always receive the canonical environment names
`NETRIS_CONTROLLER_URL`, `NETRIS_VALIDATE_CERTS`, and `NETRIS_CA_PATH`; the CA
path is `/etc/netris/ca/bundle.pem` when a CA ConfigMap is configured and is an
explicit empty value otherwise.

The following launch keys are protected, including case and separator variants:
`controller_url`, `netris_controller_url`, `validate_certs`,
`netris_validate_certs`, `ca_path`, `netris_ca_path`, and the three canonical
environment names. They may not be supplied through AAP `extra_vars` or the
managed instance-group ConfigMap/Secret, even when the value matches the
installation. Ordinary resource variables remain launchable because
`ask_variables_on_launch` stays enabled.

Netris jobs use the managed cluster-fulfillment or networking-operations
instance group with fallback disabled. Their protected strategy validates the
original Ansible Runner `extra_vars` before the first task. A direct AAP launch
that includes a protected key fails before an HTTP request, even when the value
would otherwise be ignored. Use the global networking facade to change these
settings, then reconcile the Helm release.

## How It Works

Instance group ConfigMaps and Secrets are managed by the osac-aap subchart
via Helm values under `aap.instanceGroups.*`. Configure them in your values
file and they are applied during `make install-osac`.

## ConfigMap Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NETWORK_CLASS` | (empty) | AAP network backend (`netris` or `agentless_net`). Derived by the facade for normal installs. |
| `NETWORK_STEPS_COLLECTION` | (empty) | Ansible collection for network steps (`netris.steps`, `ci.steps`, or `agentless_net.steps`). Derived by the facade for normal installs. |
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

For Netris, prefer `global.networking.netris.credentials.externalSecret: true`
when the password is supplied by an externally managed `netris-credentials`
Secret. The Secret must contain the `NETRIS_PASSWORD` key and is mounted into
the AAP cluster-fulfillment and network-fulfillment workers.

> **Note on SSH keys:** SSH private keys are not set via environment variables.
> They must be added directly to the `cluster-fulfillment-ig` Kubernetes Secret.

## Reference

See `../osac-aap/config/base/configmap-cluster-fulfillment-ig-example.yaml` and
`../osac-aap/config/base/secret-cluster-fulfillment-ig-example.yaml` for full
examples.
