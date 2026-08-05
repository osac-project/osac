# Keycloak Helm chart

This Keycloak Helm chart is intended exclusively for development and testing environments. It
deploys a single Keycloak pod with a pre-configured realm and client setup for testing
authentication and authorization workflows.

**Do not use this chart in production.** It uses hardcoded admin credentials, has no high
availability, and is not designed for production workloads.

## Installation

Before installing this chart you will need a working installation of _cert-manager_ and at least one
issuer defined.

The following table lists the configurable parameters of the Keycloak chart:

| Parameter                      | Description                                                    | Required | Default         |
|--------------------------------|----------------------------------------------------------------|----------|-----------------|
| `hostname`                     | The hostname that Keycloak uses to refer to itself             | **Yes**  | None            |
| `certs.issuerRef.kind`         | The kind of cert-manager issuer (`ClusterIssuer` or `Issuer`)  | No       | `ClusterIssuer` |
| `certs.issuerRef.name`         | The name of the cert-manager issuer for TLS certificates       | **Yes**  | None            |
| `certs.caBundle.configMap`     | ConfigMap with trusted CA certificates in PEM format           | No       | None            |
| `admin.username`               | Bootstrap admin username for the initial Keycloak account      | No       | `admin`         |
| `admin.password`               | Bootstrap admin password for the initial Keycloak account      | No       | `admin`         |
| `images.keycloak`              | The Keycloak container image                                   | No       | `26.3`          |
| `database.connection`          | List of sources for database connection parameters (see below) | No       | `[]`            |
| `groups`                       | List of groups to create in the Keycloak realm                 | No       | `[]`            |
| `users`                        | List of users to create in the Keycloak realm                  | No       | `[]`            |
| `clients`                      | List of clients to create in the Keycloak realm                | No       | `[]`            |

This chart expects an external PostgreSQL database to be available before installation. The database
connection details are provided via `database.connection`, a list of ConfigMap and Secret sources
that provide the required connection settings. Each entry maps keys from a ConfigMap or Secret to
named files. The mapped files must include at least `url`, `user`, and `password`. TLS certificate
files can also be included and referenced from the JDBC URL.

For example, to install the chart using a values file:

```bash
$ helm install keycloak it/charts/keycloak \
--namespace keycloak \
--create-namespace \
--values values.yaml \
--wait
```

To uninstall it:

```bash
$ helm uninstall keycloak --namespace keycloak
```

Here's an example `values.yaml` file for installing the chart:

```yaml
hostname: keycloak.osac

certs:
  issuerRef:
    kind: ClusterIssuer
    name: default-ca
  caBundle:
    configMap: ca-bundle

database:
  connection:
  - configMap:
      name: keycloak-database-config
      items:
      - key: url
        param: url
      - key: user
        param: user
      - key: password
        param: password
  - secret:
      name: keycloak-database-client-cert
      items:
      - key: tls.crt
        param: sslcert
      - key: key.der
        param: sslkey
      - key: ca.crt
        param: sslrootcert
```

Install using a values file:

```bash
$ helm install keycloak it/charts/keycloak \
--namespace keycloak \
--create-namespace \
--values values.yaml \
--wait
```

## Configuring groups and users

The chart allows you to create groups and users in the Keycloak realm by specifying them in the
`values.yaml` file. These are merged into the base realm configuration during deployment.

### Groups

Groups are specified as a list with at least the `name` and `path` fields:

```yaml
groups:

- name: admins
  path: /admins

- name: developers
  path: /developers
```

### Users

Users require more fields to be functional. The most important fields are:

- `username`: The login name for the user.
- `enabled`: Must be `true` for the user to log in.
- `credentials`: List of credentials with `type`, `value`, and `temporary` fields.
- `temporary`: Must be `false` in credentials, otherwise password grant won't work.

Example with users and groups:

```yaml
groups:

- name: admins
  path: /admins

users:

- username: alice
  enabled: true
  emailVerified: true
  credentials:
  - type: password
    value: alice123
    temporary: false
  firstName: Alice
  lastName: Smith
  email: alice@example.com
  groups:
  - /admins

- username: bob
  enabled: true
  emailVerified: true
  credentials:
  - type: password
    value: bob456
    temporary: false
  firstName: Bob
  lastName: Jones
  email: bob@example.com
```

Refer to the [Keycloak documentation](https://www.keycloak.org/docs/latest/server_admin/) for more
details about the available fields for groups and users.

### Clients

Clients are appended to the existing clients defined in the base realm JSON file. This is useful
for defining OAuth service accounts that can authenticate using the client credentials flow.

To create a service account client you need to add both a client entry and a corresponding user
entry. The username must follow the Keycloak convention `service-account-<clientId>`:

```yaml
clients:

- clientId: my-service
  name: My Service
  description: Service account for my service
  enabled: true
  clientAuthenticatorType: client-secret
  secret: my-secret
  serviceAccountsEnabled: true
  publicClient: false
  standardFlowEnabled: false
  implicitFlowEnabled: false
  directAccessGrantsEnabled: false
  protocol: openid-connect
  fullScopeAllowed: true

users:

- username: service-account-my-service
  enabled: true
  serviceAccountClientId: my-service
```

The key fields for service account clients are:

- `clientId`: The identifier used to authenticate.
- `secret`: The client secret.
- `serviceAccountsEnabled`: Must be `true` to enable the client credentials flow.
- `publicClient`: Must be `false` for confidential clients.

Refer to the [Keycloak documentation](https://www.keycloak.org/docs/latest/server_admin/) for more
details about the available fields for clients.

### Required clients for the fulfillment service

When using this Keycloak chart together with the fulfillment service, you must create at least the
following two service account clients:

- `osac-admin` - Used by the administrator tooling to manage the service.

- `osac-controller` - Used by the controller to authenticate to the fulfillment API using the OAuth
  client credentials flow. This service account requires the following roles from the
  `realm-management` client:

  - `manage-realm` - Manage the realm configuration, including organizations.
  - `manage-users` - Create, update and delete users.
  - `view-realm` - View the realm configuration.
  - `view-users` - View users.

For example:

```yaml
clients:

- clientId: osac-admin
  name: OSAC administrator
  description: Service account for the OSAC administrator
  enabled: true
  clientAuthenticatorType: client-secret
  secret: <your-secret>
  serviceAccountsEnabled: true
  publicClient: false
  standardFlowEnabled: false
  implicitFlowEnabled: false
  directAccessGrantsEnabled: false
  protocol: openid-connect
  fullScopeAllowed: true

- clientId: osac-controller
  name: OSAC controller
  description: Service account for the OSAC controller
  enabled: true
  clientAuthenticatorType: client-secret
  secret: <your-secret>
  serviceAccountsEnabled: true
  publicClient: false
  standardFlowEnabled: false
  implicitFlowEnabled: false
  directAccessGrantsEnabled: false
  protocol: openid-connect
  fullScopeAllowed: true

users:

- username: service-account-osac-admin
  enabled: true
  serviceAccountClientId: osac-admin

- username: service-account-osac-controller
  enabled: true
  serviceAccountClientId: osac-controller
  clientRoles:
    realm-management:
    - manage-realm
    - manage-users
    - view-realm
    - view-users
```

The `secret` for the `osac-controller` client must match the value configured in the fulfillment
service Helm chart via `auth.controllerCredentials`.

## Exporting the realm

To export the realm configuration to a JSON file, you need to find the Keycloak pod and execute the
`export` command inside it. The exported data can be written to a local JSON file using the
following steps:

1. First, find the name of the Keycloak pod:

    ```bash
    $ pod=$(kubectl get pods -n keycloak -l app=keycloak-service -o json | jq -r '.items[].metadata.name')
    ```

2. Run the `export` command inside the pod to write the realm to a temporary file:

    ```bash
    $ kubectl exec -n keycloak "${pod}" -- /opt/keycloak/bin/kc.sh export --realm osac --file /tmp/realm.json
    ```

3. Copy the temporary file to a local file:

    ```bash
    $ kubectl exec -n keycloak "${pod}" -- cat /tmp/realm.json > realm.json
    ```

4. Optionally, if you want to replace the realm used by the chart, overwrite the `realm.json` file:

   ```bash
   $ cp realm.json it/charts/keycloak/files/realm.json
   ```
