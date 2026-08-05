# Prometheus Helm chart

This Prometheus Helm chart is intended for use in the development and integration testing of the
fulfillment service. It provides a simple Prometheus instance configured to scrape metrics from
Kubernetes services using service discovery. The deployment uses ephemeral storage, so metrics
data will be lost when the pod is restarted.

## Installation

Before installing this chart you will need a working installation of _cert-manager_ and at least one
issuer defined.

The following table lists the configurable parameters of the Prometheus chart:

| Parameter                 | Description                                                   | Required | Default                                |
|---------------------------|---------------------------------------------------------------|----------|----------------------------------------|
| `hostname`                | The hostname that Prometheus uses                             | No       | `prometheus.<namespace>.svc...`        |
| `certs.issuerRef.kind`    | The kind of cert-manager issuer (`ClusterIssuer` or `Issuer`) | No       | `ClusterIssuer`                        |
| `certs.issuerRef.name`    | The name of the cert-manager issuer for TLS certificates      | No       | `default-ca`                           |
| `certs.caBundle.configMap`| The name of a ConfigMap containing trusted CA certificates    | No       | `ca-bundle`                            |
| `certs.caBundle.bundleKey`| The key in the ConfigMap that contains the CA bundle          | No       | `bundle.pem`                           |
| `images.prometheus`       | The Prometheus container image                                | No       | `quay.io/prometheus/prometheus:v3.8.1` |

For example, in the integration tests environment the chart can be installed like this:

```bash
$ helm install prometheus it/charts/prometheus \
--namespace prometheus \
--create-namespace \
--wait
```

To install the chart with a custom hostname:

```bash
$ helm install prometheus it/charts/prometheus \
--namespace prometheus \
--create-namespace \
--set hostname=prometheus.osac \
--wait
```

To uninstall it:

```bash
$ helm uninstall prometheus --namespace prometheus
```

Here's an example `values.yaml` file for installing the chart:

```yaml
hostname: prometheus.osac
```

Install using a values file:

```bash
$ helm install prometheus it/charts/prometheus \
--namespace prometheus \
--create-namespace \
--values values.yaml \
--wait
```

## Accessing the Prometheus UI

To access the Prometheus UI, you can use port-forwarding:

```bash
$ kubectl port-forward -n prometheus svc/prometheus 9090:9090
```

Then open your browser and navigate to [https://localhost:9090](https://localhost:9090).

Note that the UI is served over HTTPS. You may need to accept the self-signed certificate in your
browser.

## Service Discovery

This chart uses Kubernetes service discovery to automatically find and scrape services. To enable
scraping for a service, add the following annotations to the Service resource:

| Annotation                | Description                              | Required | Default    |
|---------------------------|------------------------------------------|----------|------------|
| `prometheus.io/scrape`    | Set to `"true"` to enable scraping       | **Yes**  | -          |
| `prometheus.io/port`      | The port to scrape metrics from          | **Yes**  | -          |
| `prometheus.io/path`      | The path to scrape metrics from          | No       | `/metrics` |
| `prometheus.io/scheme`    | The scheme to use (`http` or `https`)    | No       | `http`     |

Example service with Prometheus annotations:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-service
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "8002"
    prometheus.io/scheme: "https"
spec:
  selector:
    app: my-app
  ports:
  - name: metrics
    port: 8002
    targetPort: 8002
```

Prometheus will automatically discover and scrape any service with the `prometheus.io/scrape: "true"`
annotation across all namespaces.

## TLS Configuration

This chart uses cert-manager to generate TLS certificates for the Prometheus server. The TLS
certificate is issued by the configured cert-manager issuer (ClusterIssuer by default) and stored
in a Kubernetes secret named `prometheus-tls`.

The chart also creates a TLSRoute to expose the Prometheus UI externally with TLS passthrough.

When scraping services over HTTPS (using `prometheus.io/scheme: "https"`), Prometheus uses the CA
certificates from the ConfigMap specified in `certs.caBundle.configMap` to verify the service
certificates. The `certs.caBundle.bundleKey` parameter specifies which key in the ConfigMap contains
the CA certificate (defaults to `bundle.pem`). This is the same mechanism used by the fulfillment
service components.

## RBAC

This chart creates a `ServiceAccount`, `ClusterRole`, and `ClusterRoleBinding` to allow Prometheus
to discover and scrape services across all namespaces. The role grants read access to services,
endpoint slices and pods.

## Storage

This chart uses ephemeral storage (`emptyDir` volume) for simplicity. This means:

- Metrics data will be lost when the pod is restarted or rescheduled.
- No persistent volume claims are required.
- This is suitable for development and testing environments.
- Metrics are retained for 24 hours.

For production use cases, consider using a Prometheus deployment with persistent storage.
