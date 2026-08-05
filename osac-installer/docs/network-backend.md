# Network Backend Configuration

The network backend controls how hosted clusters get their networking
infrastructure (server clusters, NAT, DNS, MetalLB). The backend is selected by
the `NETWORK_CLASS` environment variable.

For general AAP configuration see [AAP Configuration](aap-configuration.md).

## Supported Backends

| `NETWORK_CLASS` | `NETWORK_STEPS_COLLECTION` | Description |
|-----------------|---------------------------|-------------|
| `esi` (default) | `osac.steps` | ESI (Elastic System Infrastructure) |
| `netris` | `netris.steps` | Netris controller API |

## Netris Configuration

When using `NETWORK_CLASS=netris`, the following additional variables must be set.

### ConfigMap Variables

| Variable | Description |
|----------|-------------|
| `NETRIS_CONTROLLER_URL` | Netris controller API URL |
| `NETRIS_USERNAME` | Netris API username |
| `NETRIS_SITE_ID` | Netris site ID (integer) |
| `NETRIS_TENANT_ID` | Netris tenant ID (integer) |
| `NETRIS_TENANT_NAME` | Netris tenant name |
| `NETRIS_MGMT_VPC_ID` | Management VPC ID |
| `NETRIS_MGMT_VPC_NAME` | Management VPC name |
| `NETRIS_RESOURCE_CLASS_MAP` | JSON dict mapping resource classes to config (see below) |
| `SERVER_SSH_BASTION_HOST` | Bastion hostname/IP for SSH to bare-metal servers |
| `SERVER_SSH_BASTION_USER` | Bastion SSH username |
| `SERVER_SSH_USER` | Server SSH username |
| `SERVER_MGMT_ROUTE_DESTINATION` | Management route destination CIDR |
| `SERVER_MGMT_ROUTE_GATEWAY` | Management route gateway IP |

### Secret Variables

Values must be plaintext — the script base64-encodes them automatically.

| Variable | Description |
|----------|-------------|
| `NETRIS_PASSWORD` | Netris API password |

### SSH Keys

SSH private keys must be added directly to the `cluster-fulfillment-ig`
Kubernetes Secret:

| Key | Description |
|-----|-------------|
| `SERVER_SSH_KEY` | Private key for SSH to bare-metal servers |
| `SERVER_SSH_BASTION_KEY` | Private key for SSH to the bastion host |

### `NETRIS_RESOURCE_CLASS_MAP` Format

```json
{
  "fc430": {
    "server_cluster_template_id": 89,
    "mgmt_interface": "ens4",
    "vpc_interfaces": ["ens13"]
  }
}
```

Each key is a resource class name. `server_cluster_template_id` is the Netris server
cluster template ID, `mgmt_interface` is the management NIC name, and `vpc_interfaces`
lists the data-plane NIC names.

## Helm Chart Configuration

Netris configuration is provided via Helm values. Two values sections control
the fulfillment instance groups:

### Enabling the Instance Groups

In your environment values file (e.g., `values/development/values.yaml`):

```yaml
clusterFulfillment:
  enabled: true
  config:
    NETWORK_CLASS: "netris"
    NETWORK_STEPS_COLLECTION: "netris.steps"
    NETRIS_CONTROLLER_URL: "https://redhat-ctl.netris.io"
    NETRIS_USERNAME: "netris"
    NETRIS_SITE_ID: "5"
    NETRIS_TENANT_ID: "1"
    NETRIS_TENANT_NAME: "Admin"
    NETRIS_MGMT_VPC_ID: "4"
    NETRIS_MGMT_VPC_NAME: "RH-Infra"
    NETRIS_RESOURCE_CLASS_MAP: '{"fc430": {"server_cluster_template_id": 89, "mgmt_interface": "ens4", "vpc_interfaces": ["ens13"]}}'
    SERVER_SSH_BASTION_HOST: "redhat-ctl.netris.io"
    SERVER_SSH_BASTION_USER: "ubuntu"
    SERVER_SSH_USER: "core"
    SERVER_MGMT_ROUTE_DESTINATION: "10.8.0.0/30"
    SERVER_MGMT_ROUTE_GATEWAY: "192.168.16.1"
    EXTERNAL_ACCESS_BASE_DOMAIN: "box.massopen.cloud"
    EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS: "box.massopen.cloud"
    EXTERNAL_ACCESS_API_INTERNAL_NETWORK: "hypershift"
    HOSTED_CLUSTER_BASE_DOMAIN: "box.massopen.cloud"
    HOSTED_CLUSTER_CONTROLLER_AVAILABILITY_POLICY: "HighlyAvailable"
    HOSTED_CLUSTER_INFRASTRUCTURE_AVAILABILITY_POLICY: "HighlyAvailable"

networkFulfillment:
  enabled: true
  config:
    NETRIS_CONTROLLER_URL: "https://redhat-ctl.netris.io"
    NETRIS_USERNAME: "netris"
    NETRIS_SITE_ID: "5"
    NETRIS_TENANT_ID: "1"
    NETRIS_TENANT_NAME: "Admin"
```

Only non-empty values are rendered into the ConfigMap. Keys left as `""` are
omitted, so you only need to set the variables relevant to your network backend.

### Setting Secret Values

Create a separate secrets values file that is **not committed to git**
(`.local.yaml` files are already gitignored):

```yaml
# values/development-secrets.local.yaml
clusterFulfillment:
  secret:
    NETRIS_PASSWORD: "my-netris-password"
    AWS_ACCESS_KEY_ID: "AKIA..."
    AWS_SECRET_ACCESS_KEY: "..."
    SERVER_SSH_KEY: "<contents of ~/.ssh/id_rsa>"
    SERVER_SSH_BASTION_KEY: "<contents of ~/.ssh/id_ed25519>"

networkFulfillment:
  secret:
    NETRIS_PASSWORD: "my-netris-password"
```

Pass both files when deploying — Helm deep-merges them:

```bash
helm install osac charts/osac \
  -f values/development/values.yaml \
  -f values/development-secrets.local.yaml \
  -n <namespace>
```

Alternatively, pass secrets directly on the command line:

```bash
helm install osac charts/osac -f values/development/values.yaml \
  --set clusterFulfillment.secret.NETRIS_PASSWORD=mypass \
  --set networkFulfillment.secret.NETRIS_PASSWORD=mypass
```
