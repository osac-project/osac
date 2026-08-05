# PostgreSQL Helm chart

This PostgreSQL Helm chart is intended exclusively for development and testing environments. It
deploys a single PostgreSQL pod with TLS enabled via cert-manager and ephemeral storage backed by
`emptyDir`. Data does not persist across pod restarts.

**Do not use this chart in production.** It disables `fsync`, `synchronous_commit`, and
`full_page_writes` to improve performance at the cost of durability. These settings, combined with
the lack of persistent storage, mean that data will be lost if the pod is restarted.

## Installation

Before installing this chart you will need a working installation of cert-manager and at least one
issuer defined.

The following table lists the configurable parameters of the PostgreSQL chart:

| Parameter                  | Description                                                   | Required | Default                                      |
|----------------------------|---------------------------------------------------------------|----------|----------------------------------------------|
| `certs.issuerRef.kind`     | The kind of cert-manager issuer (`ClusterIssuer` or `Issuer`) | No       | `ClusterIssuer`                              |
| `certs.issuerRef.name`     | The name of the cert-manager issuer for TLS certificates      | **Yes**  | None                                         |
| `certs.caBundle.configMap` | ConfigMap with trusted CA certificates for client auth        | No       | Uses the server certificate CA               |
| `images.postgres`          | The PostgreSQL container image                                | No       | `quay.io/sclorg/postgresql-18-c10s:latest`    |
| `databases`                | List of databases and users to create (see below)             | No       | `[]`                                         |

Note that the `certs.issuerRef.name` parameter is required. For example, to install the chart in a
Kind cluster:

```bash
$ helm install postgres it/charts/postgres \
--namespace postgres \
--create-namespace \
--set certs.issuerRef.name=default-ca \
--wait
```

To uninstall it:

```bash
$ helm uninstall postgres --namespace postgres
```

Here's an example `values.yaml` file for installing the chart:

```yaml
certs:
  issuerRef:
    kind: ClusterIssuer
    name: default-ca
  caBundle:
    configMap: ca-bundle

databases:
- name: keycloak
  user: keycloak
- name: service
  user: service
```

Install using a values file:

```bash
$ helm install postgres it/charts/postgres \
--namespace postgres \
--create-namespace \
--values values.yaml \
--wait
```

## Configuring databases and users

The chart supports creating multiple databases and users in the same PostgreSQL instance. Each entry
in the `databases` list must have a `name` (the database name) and a `user` (the PostgreSQL
user that will own the database).

Each entry may optionally include a `password` field. If omitted or empty, the user will only be
able to authenticate using client certificates. If set, password-based authentication (over TLS
using SCRAM-SHA-256) will also be available.

Here's an example with multiple databases, one using certificate-only auth and one with a password:

```yaml
databases:
- name: service
  user: service
- name: analytics
  user: analyst
  password: analyst123
```

This creates two databases (`service` and `analytics`) owned by two users (`service` and `analyst`),
along with two client certificate secrets (`postgres-client-cert-service` and
`postgres-client-cert-analyst`). The `analyst` user can also authenticate with the password
`analyst123`.

## Connecting to the database

The chart creates a Kubernetes service named `postgres` that exposes port 5432. To connect from
another pod in the same namespace, use a connection string like:

```text
postgres://service@postgres:5432/service?sslmode=verify-full&sslrootcert=/path/to/ca.crt&sslcert=/path/to/tls.crt&sslkey=/path/to/tls.key
```

Replace the user and database name with the appropriate values from your `databases` list.

To connect from a different namespace, use the fully qualified service name:

```text
postgres://service@postgres.<namespace>.svc.cluster.local:5432/service?...
```
