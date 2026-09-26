# Network Backend Configuration

The network backend controls how hosted clusters get their networking
infrastructure (server clusters, NAT, DNS, MetalLB). Configure it under
`global.networking` in your environment values file. Helm derives operator
manager ConfigMaps, AAP instance-group environment variables, and the default
NetworkClass from that single block.

For general AAP configuration see [AAP Configuration](aap-configuration.md).

## Fabric vs Kubernetes managers

`fabricManager` and `k8sManager` are the facade inputs. They match the operator /
NetworkClass manager names and select the AAP backend:

| `fabricManager` | `k8sManager` | Derived AAP backend | Status |
|-----------------|--------------|---------------------|--------|
| `netris` | `""` | `netris` / `netris.steps` | Supported |
| `""` | `k8s_only` | `agentless_net` / `agentless_net.steps` | Supported (default) |
| `""` | `""` | Must set managers via `global.networking.networkClass` or expert overrides | Expert only |
| `cudn_net` | `""` | `ci` / `ci.steps` (explicit AAP override) | Virtual-BMH CaaS only |
| `vlan` | * | — | Reserved; Helm render fails |

Setting both managers non-empty fails during render. The removed
`provider` / `overlay` keys also fail with a migration message.

## What Helm derives

When `global.networking.fabricManager` is `netris`, Helm automatically:

- Enables `operator.networkManagers.fabricManagers.netris`
- Sets `NETWORK_CLASS`, `NETWORK_STEPS_COLLECTION`, and shared `NETRIS_*` fields on
  both AAP instance groups when they are enabled (no manual duplication)
- Points the generated NetworkClass at `fabricManager: netris`

When `fabricManager` is empty and `k8sManager` is `k8s_only`, Helm enables
`operator.networkManagers.k8sManagers.k8s_only`, sets the agentless AAP backend,
and points the NetworkClass at `k8sManager: k8s_only`.

For virtual-BMH CaaS, `values/caas-ci/instance.yaml` selects `cudn_net`, no
k8s manager, explicitly registers the existing operator CUDN fabric-manager
ConfigMap, and retains `global.expertOverrides.aap: true` with
`NETWORK_CLASS=ci` / `NETWORK_STEPS_COLLECTION=ci.steps`. Rendering fails if
CaaS/BMaaS is disabled, the default NetworkClass conflicts, the CUDN manager
is not registered, or an enabled operator lacks the two enabled AAP groups and
matching `ci.steps` keys. AAP-disabled Kind sim is permitted only with the
operator disabled; it manually advances tenant default VN/Subnet/SG readiness.
The sim validates the real fulfillment API and BMI worker contract, **not**
OpenShift ClusterUserDefinedNetwork provisioning.

The CUDN operator manager handles VN/Subnet overlay provisioning on OpenShift;
Kind simulates their readiness. The `ci.steps` cluster roles still wait for
operator-bound Agents and read Agent IPs for external-access ingress DNS. They
have not been disabled or replaced: verify these steps in fresh full-install CI
before choosing any new ingress address source. This path does not claim
physical fabric provisioning by Netris.

The facade does **not** enable the AAP instance groups themselves. Set both
`aap.instanceGroups.clusterFulfillment.enabled` and
`aap.instanceGroups.networkFulfillment.enabled` to `true` for Netris-backed
provisioning. Cluster fulfillment receives `NETWORK_CLASS` /
`NETWORK_STEPS_COLLECTION` plus cluster-specific Netris fields; network
fulfillment receives the shared Netris connection fields only.

## Netris example

```yaml
global:
  networking:
    fabricManager: netris
    k8sManager: ""
    netris:
      controllerUrl: "https://redhat-ctl.netris.io"
      credentials:
        username: "netris"
        externalSecret: true
      siteId: "5"
      tenantId: "1"
      tenantName: "Admin"

aap:
  instanceGroups:
    clusterFulfillment:
      enabled: true
    networkFulfillment:
      enabled: true
```

When Netris is selected, the schema requires `controllerUrl` (HTTPS), credentials,
`siteId`, `tenantId`, and `tenantName`. Credentials may contain either a direct
password or `externalSecret: true` when the `netris-credentials` Secret is
managed outside Helm.

## Agentless example

```yaml
global:
  networking:
    fabricManager: ""
    k8sManager: k8s_only
```

## Expert overrides

Set `global.expertOverrides.aap`, `global.expertOverrides.networkClass`, or
`global.expertOverrides.networkManagers` to keep the corresponding low-level
values authoritative instead of the facade:

| Override | Low-level block |
|----------|-----------------|
| `expertOverrides.aap` | `aap.instanceGroups.clusterFulfillment` / `networkFulfillment` |
| `expertOverrides.networkClass` | legacy top-level `networkClass` (disabled by default) |
| `expertOverrides.networkManagers` | `operator.networkManagers` |

Normal deployments should leave these overrides `false` and configure
`global.networking` only.

## Advanced / manual configuration

Prefer the facade above. When not using it, set variables on
`aap.instanceGroups` directly and set `global.expertOverrides.aap: true`.

### Derived AAP backends

| `NETWORK_CLASS` | `NETWORK_STEPS_COLLECTION` | Description |
|-----------------|---------------------------|-------------|
| `netris` | `netris.steps` | Netris controller API |
| `agentless_net` | `agentless_net.steps` | Agentless network backend (no physical fabric) |
| (empty) | (empty) | No AAP network backend selected |

### ConfigMap variables

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

### Secret variables

Values must be plaintext — Helm base64-encodes them when rendering the
Kubernetes Secret. Do not pre-encode them.

| Variable | Description |
|----------|-------------|
| `NETRIS_PASSWORD` | Netris API password |

Prefer `global.networking.netris.credentials.externalSecret: true` with the
fixed `netris-credentials` Secret when the password is managed outside Helm.

### SSH keys

SSH private keys must be added directly to the `cluster-fulfillment-ig`
Kubernetes Secret:

| Key | Description |
|-----|-------------|
| `SERVER_SSH_KEY` | Private key for SSH to bare-metal servers |
| `SERVER_SSH_BASTION_KEY` | Private key for SSH to the bastion host |

### `NETRIS_RESOURCE_CLASS_MAP` format

```json
{
  "fc430": {
    "server_cluster_template_id": 89,
    "mgmt_interface": "ens4",
    "vpc_interfaces": ["ens13"]
  }
}
```

Each key is a resource class name. `server_cluster_template_id` is the Netris
server cluster template ID, `mgmt_interface` is the management NIC name, and
`vpc_interfaces` lists the data-plane NIC names.

### Expert Helm example

```yaml
global:
  expertOverrides:
    aap: true

aap:
  instanceGroups:
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
      secret:
        NETRIS_PASSWORD: "<netris-password>"
    networkFulfillment:
      enabled: true
      config:
        NETRIS_CONTROLLER_URL: "https://redhat-ctl.netris.io"
        NETRIS_USERNAME: "netris"
        NETRIS_SITE_ID: "5"
        NETRIS_TENANT_ID: "1"
        NETRIS_TENANT_NAME: "Admin"
      secret:
        NETRIS_PASSWORD: "<netris-password>"
```

Only non-empty values are rendered into the ConfigMap. Keep secrets in a
gitignored `.local.yaml` file or pass them with
`--set-string aap.instanceGroups.clusterFulfillment.secret.NETRIS_PASSWORD=...`.
