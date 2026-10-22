# Service Helm Chart

This Helm chart deploys the OSAC UI.

## Prerequisites

- **fulfillment-service** deployed and reachable from the cluster.
- **Keycloak** deployed with an OIDC realm and the `osac-ui` client registered.

Platform integration (Keycloak `KC_HOSTNAME`, OIDC client, fulfillment trusted issuer, TLS troubleshooting) is documented in [`../../docs/deployment-openshift-guide.md`](../../docs/deployment-openshift-guide.md).

## Installation

`helm install` deploys only the UI; complete Keycloak and fulfillment integration per the [OpenShift deployment guide](../../docs/deployment-openshift-guide.md) before expecting a working login flow.

To install the chart with the release name `osac-ui`:

```bash
$ helm install osac-ui ./deploy/chart -n osac --create-namespace
```

## Configuration

The following table lists the configurable parameters of the chart and their default values:

| Parameter                                 | Description                                                                                                                                      | Default                        | Required |
|-------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------|--------------------------------|----------|
| `externalHostname`                        | External hostname for the OpenShift Route. When not set, the hostname is automatically assigned by OpenShift.                                    | `""`                           | No       |
| `route.enabled`                           | Create the OpenShift Route. Disable when exposing the Service through another ingress, such as a Gateway API HTTPRoute.                         | `true`                          | No       |
| `api.fulfillment.url`                     | URL of the Fulfillment API backend.                                                                                                              | `https://fulfillment-api:8000` | Yes      |
| `api.fulfillment.certs.caBundle.configMap`| Name of a ConfigMap in the release namespace that contains trusted CA certificates in PEM format for the Fulfillment API TLS connection.         | `""`                           | Yes      |
| `auth.oidcClientId`                       | OIDC client ID registered in Keycloak (or another OIDC provider) for the UI.                                                                    | `osac-ui`                      | Yes      |
| `auth.certs.caBundle.configMap`           | Name of a ConfigMap in the release namespace that contains trusted CA certificates in PEM format for the OIDC/Auth TLS connection.               | `""`                           | Yes      |
| `log.level`                               | Log level for all components. Valid values: `debug`, `info`, `warn`, `error`. Setting `debug` increases log volume and may impact performance.   | `info`                         | No       |
| `images.ui`                               | Container image for the UI.                                                                                                                      | `ghcr.io/osac-project/osac-ui:main`    | No       |
| `images.pullPolicy`                       | Kubernetes image pull policy, or `auto` to leave it to Kubernetes' tag-based default.                                                            | `auto`                           | No       |
