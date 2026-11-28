# Service Helm Chart

This Helm chart deploys the complete fulfillment service.

## Prerequisites

- Kubernetes cluster (_Kind_ or _OpenShift_).
- _cert-manager_ operator installed.
- _trust-manager_ operator installed (optional, but highly recommended).

## Installation

To install the chart with the release name `fulfillment-service`:

```bash
$ helm install fulfillment-service ./charts/service -n osac --create-namespace -f values.yaml
```

## Configuration

The following table lists the configurable parameters of the chart and their default values:

| Parameter                  | Description                                                                       | Default                                                        |
|----------------------------|-----------------------------------------------------------------------------------|----------------------------------------------------------------|
| `variant`                  | Deployment variant (`kind` or `openshift`)                                        | `kind`                                                         |
| `certs.issuerRef.kind`     | Kind of _cert-manager_ issuer                                                     | `ClusterIssuer`                                                |
| `certs.issuerRef.name`     | Name of _cert-manager_ issuer                                                     | None                                                           |
| `certs.caBundle.configMap` | Name of configmap containing trusted CA certificates in PEM format                | Required                                                       |
| `externalHostname`         | Hostname used to access the public API via the external network                   | Required                                                       |
| `internalHostname`         | Hostname used to access both the public and private APIs via the internal network | Required                                                       |
| `auth.issuerUrl`           | OAuth issuer URL for authentication                                               | `https://keycloak.keycloak.svc.cluster.local:8000/realms/osac` |
| `global.mcp.enabled`       | Deploy the supported MCP endpoint                                                 | `false`                                                        |
| `global.mcp.publicURL`     | Canonical external HTTPS MCP URL, including the trailing `/`                     | Empty; required when MCP is enabled                            |
| `global.mcp.issuerURL`     | OAuth issuer URL for MCP caller tokens                                           | Empty; required when MCP is enabled                            |
| `global.mcp.audience`      | OAuth audience required by exchanged Fulfillment API tokens                      | `osac-api`                                                     |
| `global.mcp.exchangeTokenEndpoint` | OAuth token exchange endpoint                                             | Empty; required when MCP is enabled                            |
| `global.mcp.exchangeClientID` | Confidential OAuth exchange client ID                                          | Empty; required when MCP is enabled                            |
| `global.mcp.exchangeClientSecret` | Existing Secret reference for the exchange client credential; the value is never in Helm values | Empty; required when MCP is enabled |
| `global.mcp.trust`         | MCP TLS trust mode and optional PEM ConfigMap source                             | `configMap`; `configMap` is required when enabled              |
| `global.mcp.replicas`      | MCP server replica count                                                         | `2`                                                            |
| `global.mcp.resources`     | Per-pod CPU and memory requests and limits                                       | `50m`/`128Mi`, `500m`/`512Mi`                                 |
| `global.mcp.limits`        | Request size, concurrency, and timeout settings                                  | `1Mi`, `100`, `30s`                                            |
| `log.level`                | Log level for all components (debug, info, warn, error)                           | `info`                                                         |
| `log.headers`              | Enable logging of HTTP/gRPC headers                                               | `false`                                                        |
| `log.bodies`               | Enable logging of HTTP/gRPC request and response bodies                           | `false`                                                        |
| `images.service.repository` | Fulfillment service container image repository                                   | `ghcr.io/osac-project/fulfillment-service`                     |
| `images.service.tag`       | Fulfillment service container image tag                                            | `latest`                                                       |
| `images.service.pullPolicy` | Fulfillment service image pull policy                                             | `Always`                                                       |
| `images.envoy.repository`  | Envoy proxy container image repository                                             | `docker.io/envoyproxy/envoy`                                   |
| `images.envoy.tag`         | Envoy proxy container image tag                                                    | `v1.37.1`                                                      |
| `images.envoy.pullPolicy`  | Envoy proxy image pull policy                                                      | `IfNotPresent`                                                 |
| `eventPublisher.enabled`   | Deploy the Kafka-backed event publisher                                           | `true`                                                         |
| `eventPublisher.topicRetention` | How long a tenant's event topic keeps records: `-1s` indefinitely, or a duration like `168h` | `-1s`                                         |
| `database.connection`      | List of sources for database connection parameters (see below)                    | `[]` (must be configured)                                      |
| `kafka.connection`         | List of sources for Kafka connection parameters (see below)                       | `[]` (must be configured when the event publisher is enabled)  |
| `global.networking.provisioningEnabled` | Provider networking (ExternalIPs, NATGateways, automatic external access); fixed at installation | `true` |
| `global.networking.excludedIpv4Cidrs` | IPv4 ranges that no VirtualNetwork, ExternalIPPool, Cluster network or NetworkClass default may overlap; the service refuses to start when an existing resource overlaps one | `[]` |

Connection details are provided via `kafka.connection`, a list of ConfigMap and Secret sources that
provide the connection parameters. When `eventPublisher.enabled` is true, the sources must provide
`brokers`, which is required for the publisher to start. Set `eventPublisher.enabled: false` for an
API-only deployment that deliberately omits Kafka. Each entry maps keys from a ConfigMap or Secret to Kafka configuration properties
named `brokers`, `user` and `password`. SASL is optional: omit `user` and `password` when the cluster
does not require it. TLS trust uses `certs.caBundle`; include the broker CA in that ConfigMap when it
is not already trusted.

When MCP is enabled, register a dedicated confidential OAuth token-exchange
client with the authorization server. The MCP ingress accepts tokens addressed
to `global.mcp.publicURL`. Use that exact URL, including the slash, for the
exchange client ID and the authorization server's resource URI. The
exchange client must be allowed to receive those tokens and mint
a separate `osac-api` access token for the same user. Its default scopes must
include OSAC's identity claims (`username`, `groups`, `organization`, and
`roles`) and the `osac-api` audience. Put only its client secret in the
configured Kubernetes Secret under the `client-secret` key. The MCP deployment
uses the issuer's Keycloak token endpoint over the configured CA trust bundle.

**Note on hostnames:** Both `externalHostname` and `internalHostname` are required because TLS
certificates must be generated with the correct host names.

The recommended deployment uses two networks. The external network is intended for regular users and
only gives access to the public API via `externalHostname`. The internal network gives access to
both the public and private APIs via `internalHostname`. The internal network should be restricted
so that only the administrators of the system can access it. In a typical environment this would be
a network confined to the physical infrastructure of the cloud provider, while the external network
would be publicly reachable. Restricting access to the internal network is not strictly required
because the private API is protected by authentication and authorization, but it is good practice to
add network-level isolation as an additional layer of defense.

### Example custom values

To customize the deployment, create a `values.yaml` file:

```yaml
variant: openshift

certs:
  issuerRef:
    kind: Issuer
    name: my-issuer
  caBundle:
    configMap: my-ca-bundle

externalHostname: fulfillment-service.example.com
internalHostname: fulfillment-internal.example.com

auth:
  issuerUrl: https://keycloak.example.com/realms/osac

log:
  level: debug
  headers: true
  bodies: true

images:
  service:
    repository: ghcr.io/osac-project/fulfillment-service
    tag: v1.0.0
    pullPolicy: Always

database:
  connection:
  - configMap:
      name: fulfillment-database-config
      items:
      - key: url
        param: url
  - secret:
      name: fulfillment-database-client-cert
      items:
      - key: tls.crt
        param: sslcert
      - key: tls.key
        param: sslkey
      - key: ca.crt
        param: sslrootcert

kafka:
  connection:
  - configMap:
      name: fulfillment-service-kafka-config
      items:
      - key: bootstrap.servers
        param: brokers
      - key: username
        param: user
  - secret:
      name: fulfillment-service-kafka
      items:
      - key: password
        param: password
```

Then install with:

```bash
$ helm install fulfillment-service ./charts/service -n osac -f values.yaml
```

## Variants

### Kind variant

When `variant: kind` is set:

- The API service uses NodePort type with port 30000
- Suitable for development and testing

### OpenShift variant

When `variant: openshift` is set:

- The API service uses ClusterIP type
- An OpenShift Route is created for external access
- Suitable for production deployments on OpenShift

## Uninstallation

To uninstall the chart:

```bash
helm uninstall fulfillment-service -n osac
```
