# AAP Configuration

## Block StorageClass filesystem type

Configure the filesystem type used for new AAP-managed VAST block and LVMS
StorageClasses with:

```yaml
aap:
  instanceGroups:
    storageFulfillment:
      enabled: true
      config:
        OSAC_STORAGE_BLOCK_FSTYPE: "ext4"
```

The default is `ext4`. The existing `storage-operations-ig` ConfigMap supplies
the value to storage workers. Overrides are passed unchanged and must be
nonempty when creating block classes; they are not restricted to an allowlist
or normalized. NFS classes and teardown operations are unaffected.

As with other instance-group ConfigMap entries, an empty Helm value is omitted,
leaving the worker's `ext4` default in effect. An explicitly empty worker
environment variable or Ansible override is rejected when a block class needs
to be created.

The setting applies to newly created classes, not existing classes or volumes.
VAST creates only missing block classes, including on partial requests; an
existing block class does not require a filesystem setting to create a missing
NFS class. No existing classes or volumes are migrated.
For example, after changing `ext4` to `xfs`, an existing class retains `ext4`;
new PVCs naming that same class still use its old parameters, not the new
worker setting. Only newly created class objects receive `xfs`, including
missing block classes in a partial request. Existing classes without an
`fstype` parameter are not backfilled, and bound PVCs, PVs, and their data
are left unchanged.

Plan a separately named or versioned StorageClass for future claims and a
separate workload and data migration plan for existing workloads. This
setting does not automate either rollout or migration. Do not assume that
same-name class deletion and recreation is safe for all volume lifecycle
operations.

See the [AAP storage configuration](../../osac-aap/docs/aap-configuration.md#block-storageclass-filesystem-type)
for existing-class selection and immutable-parameter limitations.

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

Netris manager settings and credentials are read directly from
`aap.instanceGroups.networkFulfillment.config` and `.secret` by generic
networking jobs. They are not selected through a `NETWORK_CLASS` environment
variable. See [shared CaaS endpoint lifecycle](network-backend.md#shared-caas-endpoint-lifecycle)
for the endpoint flow and upgrade guidance.

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
| `HOSTED_CLUSTER_BASE_DOMAIN` | `hosted.` plus the hub's apps domain without `apps.` | Base domain for hosted clusters. The chart sets this default from the hub `Ingress`; `box.massopen.cloud` is only the playbook fallback when the variable is absent |
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
