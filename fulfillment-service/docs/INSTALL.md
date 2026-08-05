# Installation guide

This document describes how to install the service on OpenShift. It uses the Keycloak operator for
identity management and presents three options for the PostgreSQL database: CloudNativePG (CNPG),
the Crunchy PostgreSQL operator (PGO), and the Zalando PostgreSQL operator. These are just
suggestions. The service will work with any PostgreSQL 18+ database as long as the connection
details are provided correctly. Similarly, the service requires Keycloak specifically, but it will
work with any Keycloak installation regardless of how it was deployed - whether via the operator,
manually, or by other means - as long as it is correctly configured with the required realm,
clients, and roles.

The steps below are meant to be followed in order. Each step assumes the previous ones have been
completed. The commands are designed to be copied and executed directly, and the result should be a
fully working installation.

## Prerequisites

You will need an OpenShift cluster with `cluster-admin` access and a storage class that supports
dynamic provisioning of persistent volumes. The PostgreSQL cluster created during the installation
will request persistent volume claims for its data, so the cluster must have a working storage
backend.

You will also need the following command-line tools installed:

- `helm` To install Helm charts. At least version 3.8 for OCI registry support.
- `jq` - To extract data from JSON documents.
- `oc` - To interact with the OpenShift cluster from the command line.
- `openssl` - To generate random passwords.
- `osac` - To interact with the OSAC system from the command line.

The `osac` CLI can be downloaded from the [releases
page](https://github.com/osac-project/osac/releases) of the project.

Before starting, set the `DOMAIN` environment variable to the OpenShift applications domain of your
cluster. This is the domain suffix used by OpenShift routes to construct hostnames. You can find it
by running `oc get ingresses.config.openshift.io cluster -o json | jq -r '.spec.domain'`, or by
looking at the hostname of any existing Route in your cluster.

```shell
export DOMAIN=$(oc get ingresses.config.openshift.io cluster -o json | jq -r '.spec.domain')
```

All commands in this guide use `${DOMAIN}` to construct hostnames, so you only need to set this
once.

## Enable HTTP/2

The service uses gRPC, which requires HTTP/2. This is not enabled by default in OpenShift, so you
need to enable it first:

```shell
oc annotate ingresses.config.openshift.io cluster ingress.operator.openshift.io/default-enable-http2=true
```

## Install cert-manager

The service uses cert-manager to manage TLS certificates for all components. Install it using Helm:

```shell
helm upgrade cert-manager oci://quay.io/jetstack/charts/cert-manager \
--install \
--version v1.20.0 \
--namespace cert-manager \
--create-namespace \
--set crds.enabled=true \
--wait
```

If you prefer, cert-manager can also be installed via the Operator Lifecycle Manager (OLM) from the
`community-operators` catalog on OpenShift.

## Install trust-manager

The trust-manager operator distributes CA certificates to all namespaces in the cluster. It is used
to make the CA certificate available to the service and its dependencies. Install it in the same
namespace as cert-manager:

```shell
helm upgrade trust-manager oci://quay.io/jetstack/charts/trust-manager \
--install \
--version v0.22.0 \
--namespace cert-manager \
--set defaultPackage.enabled=false \
--wait
```

## Create the certificate authority

The service and its dependencies need a common CA for TLS communication. If your cluster already has
a CA and a cert-manager `ClusterIssuer` configured, you can skip this section and use your existing
issuer in the steps that follow. The commands below show how to create a simple self-signed CA from
scratch. They create a CA certificate, a `ClusterIssuer` that can issue certificates signed by that
CA, and a trust-manager Bundle that distributes the CA certificate to the relevant namespaces.

First, create a namespace-scoped self-signed issuer that will be used only to generate the CA
certificate itself:

```shell
oc apply -f - <<.
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  namespace: cert-manager
  name: osac-ca
spec:
  selfSigned: {}
.
```

Next, create a CA certificate using that self-signed issuer:

```shell
oc apply -f - <<.
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  namespace: cert-manager
  name: osac-ca
spec:
  commonName: OSAC CA
  isCA: true
  issuerRef:
    kind: Issuer
    name: osac-ca
  secretName: osac-ca
  privateKey:
    rotationPolicy: Always
.
```

Now create the `ClusterIssuer` that other components will reference when they need a TLS
certificate. This issuer uses the CA certificate created above:

```shell
oc apply -f - <<.
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: osac-ca
spec:
  ca:
    secretName: osac-ca
.
```

Finally, create a trust-manager `Bundle` that copies the CA certificate into a configmap named
`ca-bundle` in the namespaces that need it. This configmap is used by the service and its
dependencies to verify TLS certificates:

```shell
oc apply -f - <<.
apiVersion: trust.cert-manager.io/v1alpha1
kind: Bundle
metadata:
  name: ca-bundle
spec:
  sources:
  - secret:
      name: osac-ca
      key: tls.crt
  target:
    configMap:
      key: bundle.pem
    namespaceSelector:
      matchExpressions:
      - key: kubernetes.io/metadata.name
        operator: In
        values:
        - keycloak
        - osac
        - postgres
.
```

## Install the PostgreSQL operator

The service requires a PostgreSQL 18+ database. This guide presents three options for deploying
PostgreSQL on Kubernetes: CloudNativePG (CNPG), the Crunchy PostgreSQL operator (PGO), and the
Zalando PostgreSQL operator. Choose one of the options below and follow the corresponding
instructions. The rest of this guide will note where commands differ depending on which operator
you chose.

### Option A: CloudNativePG (CNPG)

[CloudNativePG](https://github.com/cloudnative-pg/cloudnative-pg) (CNPG) is an operator that
manages the full lifecycle of PostgreSQL clusters natively in Kubernetes.

On OpenShift the operator pod needs a security context constraint that allows it to run with its
expected UID. Create the namespace, grant the required SCC, and then install the operator:

```shell
oc new-project postgres || true
oc adm policy add-scc-to-user nonroot-v2 -z cnpg-cloudnative-pg -n postgres

helm upgrade cnpg oci://ghcr.io/cloudnative-pg/charts/cloudnative-pg \
--install \
--version 0.28.0 \
--namespace postgres \
--wait
```

CNPG does not generate database passwords automatically. Create Kubernetes secrets of type
`kubernetes.io/basic-auth` with the credentials for each application user before creating the
cluster:

```shell
oc create secret generic -n postgres osac-keycloak-credentials \
--type=kubernetes.io/basic-auth \
--from-literal=username=keycloak \
--from-literal=password="$(openssl rand -base64 18)"

oc create secret generic -n postgres osac-service-credentials \
--type=kubernetes.io/basic-auth \
--from-literal=username=service \
--from-literal=password="$(openssl rand -base64 18)"
```

Create a TLS certificate for the PostgreSQL server using the CA created earlier. The `dnsNames`
must cover all the service names that CNPG creates for the cluster. Add the `cnpg.io/reload`
label so that CNPG automatically reloads the certificate when it is renewed:

```shell
oc apply -f - <<.
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  namespace: postgres
  name: osac-server-tls
spec:
  issuerRef:
    kind: ClusterIssuer
    name: osac-ca
  dnsNames:
  - osac-rw
  - osac-rw.postgres
  - osac-rw.postgres.svc
  - osac-rw.postgres.svc.cluster.local
  - osac-r
  - osac-r.postgres.svc
  - osac-ro
  - osac-ro.postgres.svc
  secretName: osac-server-tls
  secretTemplate:
    labels:
      cnpg.io/reload: ""
  privateKey:
    rotationPolicy: Always
.
```

Create a CNPG `Cluster`. The `certificates` section references the cert-manager secret for
server TLS. The `bootstrap.initdb` section creates the `keycloak` database with `keycloak` as
its owner. The `postInitSQL` stanza runs additional SQL as superuser against the `postgres`
database to create the `service` role and database. The `managed.roles` section tells CNPG to
reconcile the `service` role's password from the corresponding secret:

```shell
oc apply -f - <<.
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  namespace: postgres
  name: osac
spec:
  instances: 2
  certificates:
    serverTLSSecret: osac-server-tls
    serverCASecret: osac-server-tls
  bootstrap:
    initdb:
      database: keycloak
      owner: keycloak
      secret:
        name: osac-keycloak-credentials
      postInitSQL:
      - create role service login
      - create database service owner service
  managed:
    roles:
    - name: service
      ensure: present
      login: true
      passwordSecret:
        name: osac-service-credentials
  storage:
    size: 10Gi
.
```

Wait for the cluster pods to be ready:

```shell
oc wait pods -n postgres \
--selector cnpg.io/podRole=instance,cnpg.io/cluster=osac \
--for=condition=Ready \
--timeout=300s
```

The credentials for the `keycloak` and `service` users are in the secrets created earlier
(`osac-keycloak-credentials` and `osac-service-credentials`). Each secret contains `username` and
`password` keys.

The primary PostgreSQL instance is reachable at `osac-rw.postgres.svc.cluster.local` on port 5432.

The configuration above is a minimal example. CNPG supports many additional features such as
automated backups, point-in-time recovery, and connection pooling with _PgBouncer_. See the
[CNPG documentation](https://cloudnative-pg.io/documentation/current/) for the full reference.

### Option B: Crunchy PostgreSQL operator (PGO)

The [Crunchy PostgreSQL operator](https://github.com/CrunchyData/postgres-operator) (PGO) is an
alternative operator for managing PostgreSQL clusters on Kubernetes. PGO is designed to work with
OpenShift's restricted security context constraint out of the box, so no special SCC configuration
is needed.

Create the namespace and install the operator from the Crunchy Data OCI registry:

```shell
oc new-project postgres || true

helm upgrade pgo oci://registry.developers.crunchydata.com/crunchydata/pgo \
--install \
--version 6.0.1 \
--namespace postgres \
--wait
```

PGO creates databases owned by the `postgres` superuser. Beginning with PostgreSQL 15, the default
privileges on the `public` schema were tightened, so application users cannot create tables unless
they own the schema. To fix this, create a ConfigMap with initialization SQL that transfers schema
ownership to each application user. PGO will run this SQL automatically as part of cluster creation:

```shell
oc create configmap -n postgres osac-init-sql --from-literal=init.sql='
\c keycloak
alter schema public owner to keycloak;
\c service
alter schema public owner to service;
'
```

Create TLS certificates for the PostgreSQL server and replication using the CA created earlier.
The server certificate must include the primary service name in its `dnsNames`. The replication
certificate must have `_crunchyrepl` as its common name and be valid for client authentication.
Both must share the same CA certificate:

```shell
oc apply -f - <<.
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  namespace: postgres
  name: osac-server-tls
spec:
  issuerRef:
    kind: ClusterIssuer
    name: osac-ca
  dnsNames:
  - osac-primary
  - osac-primary.postgres
  - osac-primary.postgres.svc
  - osac-primary.postgres.svc.cluster.local
  - osac-replicas
  - osac-replicas.postgres.svc
  secretName: osac-server-tls
  privateKey:
    rotationPolicy: Always
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  namespace: postgres
  name: osac-replication-tls
spec:
  issuerRef:
    kind: ClusterIssuer
    name: osac-ca
  commonName: _crunchyrepl
  usages:
  - client auth
  secretName: osac-replication-tls
  privateKey:
    rotationPolicy: Always
.
```

Create a `PostgresCluster` with two users and two databases: one for Keycloak and one for the
service. The `customTLSSecret` and `customReplicationTLSSecret` fields reference the cert-manager
secrets created above. The `databaseInitSQL` field references the ConfigMap created earlier.

```shell
oc apply -f - <<.
apiVersion: postgres-operator.crunchydata.com/v1beta1
kind: PostgresCluster
metadata:
  namespace: postgres
  name: osac
spec:
  postgresVersion: 18
  customTLSSecret:
    name: osac-server-tls
  customReplicationTLSSecret:
    name: osac-replication-tls
  databaseInitSQL:
    key: init.sql
    name: osac-init-sql
  instances:
  - name: osac
    replicas: 2
    dataVolumeClaimSpec:
      accessModes:
      - ReadWriteOnce
      resources:
        requests:
          storage: 10Gi
  users:
  - name: keycloak
    databases:
    - keycloak
  - name: service
    databases:
    - service
.
```

Wait for the cluster pods to be ready:

```shell
oc wait pods -n postgres \
--selector=postgres-operator.crunchydata.com/cluster=osac \
--for=condition=Ready \
--timeout=300s
```

PGO creates a secret for each user with the generated credentials. The secret names follow the
pattern `<cluster>-pguser-<username>`. For the configuration above the secrets are:

- `osac-pguser-keycloak` for the `keycloak` user
- `osac-pguser-service` for the `service` user

Each secret contains `user`, `password`, `host`, `port`, `dbname`, `uri`, and `jdbc-uri` keys.

The primary PostgreSQL instance is reachable at `osac-primary.postgres.svc.cluster.local` on port
5432.

The configuration above is a minimal example. PGO supports many additional features such as
connection pooling with _PgBouncer_, automated backups with _pgBackRest_, and monitoring. See the
[PGO documentation](https://access.crunchydata.com/documentation/postgres-operator/latest/) for the
full reference.

### Option C: Zalando PostgreSQL operator

The [Zalando PostgreSQL operator](https://github.com/zalando/postgres-operator) manages PostgreSQL
clusters on Kubernetes.

On OpenShift the operator and database pods need security context constraints that allow them to
run with their expected UIDs. Create the namespace first and grant the required SCCs before
installing the operator, so that its pod starts without issues:

```shell
oc new-project postgres || true
oc adm policy add-scc-to-user nonroot-v2 -z postgres-operator -n postgres
oc adm policy add-scc-to-user anyuid -z postgres-pod -n postgres
```

Now add the Helm repository and install the operator:

```shell
helm repo add postgres-operator-charts \
https://opensource.zalando.com/postgres-operator/charts/postgres-operator

helm upgrade postgres-operator postgres-operator-charts/postgres-operator \
--install \
--version 1.15.1 \
--namespace postgres \
--set configGeneral.kubernetes_use_configmaps=true \
--wait
```

The `kubernetes_use_configmaps` setting is required on OpenShift because Patroni's default leader
election mechanism uses Kubernetes endpoints, which does not work reliably on recent Kubernetes
versions. Using configmaps instead avoids this issue.

Create a TLS certificate for the PostgreSQL server using the CA created earlier. The `dnsNames`
must cover the service names that the Zalando operator creates for the cluster:

```shell
oc apply -f - <<.
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  namespace: postgres
  name: osac-server-tls
spec:
  issuerRef:
    kind: ClusterIssuer
    name: osac-ca
  dnsNames:
  - osac
  - osac.postgres
  - osac.postgres.svc
  - osac.postgres.svc.cluster.local
  - osac-repl
  - osac-repl.postgres.svc
  secretName: osac-server-tls
  privateKey:
    rotationPolicy: Always
.
```

Create a PostgreSQL cluster with two databases: one for Keycloak and one for the service. The
`spiloFSGroup` field is required so that the postgres process can read the mounted TLS files.
The `tls` section references the cert-manager secret. The Zalando operator will create the
cluster, the databases, and the users, and it will store the generated passwords in Kubernetes
secrets.

```shell
oc apply -f - <<.
apiVersion: acid.zalan.do/v1
kind: postgresql
metadata:
  namespace: postgres
  name: osac
spec:
  teamId: osac
  numberOfInstances: 2
  spiloFSGroup: 103
  tls:
    secretName: osac-server-tls
    caFile: ca.crt
  volume:
    size: 10Gi
  postgresql:
    version: "18"
  users:
    keycloak: []
    service: []
  databases:
    keycloak: keycloak
    service: service
.
```

Wait for the cluster to be ready:

```shell
oc wait postgresql -n postgres osac \
--for=jsonpath='{.status.PostgresClusterStatus}'=Running \
--timeout=300s
```

The operator creates a secret for each user with the generated password. The secret names follow the
pattern `<username>.<cluster>.credentials.postgresql.acid.zalan.do`. For the configuration above the
secrets are:

- `keycloak.osac.credentials.postgresql.acid.zalan.do` for the `keycloak` user
- `service.osac.credentials.postgresql.acid.zalan.do` for the `service` user

Each secret contains `username` and `password` keys.

The primary PostgreSQL instance is reachable at `osac.postgres.svc.cluster.local` on port 5432.

The configuration above is a minimal example. The Zalando operator supports many additional
features such as connection pooling, logical backups, and custom pod configuration. See the
[Zalando PostgreSQL operator documentation](https://postgres-operator.readthedocs.io/) for the
full reference.

## Install the Keycloak operator

Install the Keycloak custom resource definitions:

```shell
oc apply -f https://raw.githubusercontent.com/keycloak/keycloak-k8s-resources/26.6.2/kubernetes/keycloaks.k8s.keycloak.org-v1.yml
oc apply -f https://raw.githubusercontent.com/keycloak/keycloak-k8s-resources/26.6.2/kubernetes/keycloakrealmimports.k8s.keycloak.org-v1.yml
```

Install the operator deployment in the `keycloak` namespace. The operator watches the namespace
where it is installed, so the Keycloak instance will also be created there:

```shell
oc new-project keycloak || true

oc apply -n keycloak \
-f https://raw.githubusercontent.com/keycloak/keycloak-k8s-resources/26.6.2/kubernetes/kubernetes.yml
```

Wait for the operator to be ready:

```shell
oc rollout status deployment keycloak-operator -n keycloak --timeout=120s
```

The Keycloak operator is also available via OLM from the `redhat-operators` catalog on OpenShift.

## Create the Keycloak instance

Create a TLS certificate for Keycloak using the CA created earlier:

```shell
oc apply -f - <<.
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  namespace: keycloak
  name: keycloak-tls
spec:
  issuerRef:
    kind: ClusterIssuer
    name: osac-ca
  dnsNames:
  - keycloak-keycloak.${DOMAIN}
  - keycloak.keycloak.svc.cluster.local
  secretName: keycloak-tls
  privateKey:
    rotationPolicy: Always
.
```

Create a secret with the database credentials for Keycloak. The command to extract the password
depends on which PostgreSQL operator you installed.

If you installed CNPG:

```shell
KEYCLOAK_DB_PASSWORD=$(
  oc get secret -n postgres osac-keycloak-credentials -o json |
  jq -r '.data["password"] | @base64d'
)
```

If you installed the Crunchy operator:

```shell
KEYCLOAK_DB_PASSWORD=$(
  oc get secret -n postgres osac-pguser-keycloak -o json |
  jq -r '.data["password"] | @base64d'
)
```

If you installed the Zalando operator:

```shell
KEYCLOAK_DB_PASSWORD=$(
  oc get secret -n postgres keycloak.osac.credentials.postgresql.acid.zalan.do -o json |
  jq -r '.data["password"] | @base64d'
)
```

Then create the secret in the Keycloak namespace:

```shell
oc create secret generic -n keycloak keycloak-db-secret \
--from-literal=username=keycloak \
--from-literal=password="${KEYCLOAK_DB_PASSWORD}"
```

The `db.host` value depends on which PostgreSQL operator you installed:

- If you installed CNPG:

  ```shell
  KEYCLOAK_DB_HOST="osac-rw.postgres.svc.cluster.local"
  ```

- If you installed the Crunchy operator:

  ```shell
  KEYCLOAK_DB_HOST="osac-primary.postgres.svc.cluster.local"
  ```

- If you installed the Zalando operator:

  ```shell
  KEYCLOAK_DB_HOST="osac.postgres.svc.cluster.local"
  ```

Now create the Keycloak instance:

```shell
oc apply -f - <<.
apiVersion: k8s.keycloak.org/v2beta1
kind: Keycloak
metadata:
  namespace: keycloak
  name: keycloak
spec:
  instances: 2
  db:
    vendor: postgres
    host: ${KEYCLOAK_DB_HOST}
    port: 5432
    database: keycloak
    usernameSecret:
      name: keycloak-db-secret
      key: username
    passwordSecret:
      name: keycloak-db-secret
      key: password
  http:
    tlsSecret: keycloak-tls
  hostname:
    hostname: keycloak-keycloak.${DOMAIN}
  proxy:
    headers: xforwarded
.
```

Wait for Keycloak to be ready:

```shell
oc wait keycloak -n keycloak keycloak --for=condition=Ready --timeout=300s
```

Create an OpenShift route to expose Keycloak outside the cluster:

```shell
oc apply -f - <<.
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  namespace: keycloak
  name: keycloak
spec:
  host: keycloak-keycloak.${DOMAIN}
  to:
    kind: Service
    name: keycloak-service
  port:
    targetPort: https
  tls:
    termination: passthrough
.
```

## Configure the Keycloak realm

The service expects a Keycloak realm named `osac` with specific clients and roles. The full realm
configuration is documented in `charts/keycloak/README.md`. The minimum required configuration
includes the following clients:

- `osac-cli` - A public client with device and password grant flows enabled, used by the CLI.
- `osac-admin` - A confidential service account client used by administrative tooling.
- `osac-controller` - A confidential service account client used by the controller. This client
needs the following roles from the `realm-management` client: `manage-realm`, `manage-users`,
`view-realm`, and `view-users`.

The realm must also be configured so that access tokens include the `username` and `groups` claims.
The service uses these claims to identify the user and determine group membership for authorization
decisions. This is done by defining custom client scopes with the appropriate protocol mappers and
assigning them to all clients.

Generate random secrets for the `osac-admin` and `osac-controller` clients:

```shell
OSAC_ADMIN_CLIENT_SECRET="$(openssl rand -base64 18)"
OSAC_CONTROLLER_CLIENT_SECRET="$(openssl rand -base64 18)"
```

Create the realm using a `KeycloakRealmImport` custom resource. The example below creates the realm
with the required clients, scopes, and protocol mappers:

```shell
oc apply -f - <<.
apiVersion: k8s.keycloak.org/v2beta1
kind: KeycloakRealmImport
metadata:
  namespace: keycloak
  name: osac
spec:
  keycloakCRName: keycloak
  realm:
    realm: osac
    enabled: true
    organizationsEnabled: true

    clientScopes:
    - name: osac-api
      description: OSAC API
      protocol: openid-connect
      attributes:
        include.in.token.scope: "false"
        display.on.consent.screen: "false"
      protocolMappers:
      - name: osac-api-aud
        protocol: openid-connect
        protocolMapper: oidc-audience-mapper
        consentRequired: false
        config:
          id.token.claim: "false"
          lightweight.claim: "false"
          access.token.claim: "true"
          introspection.token.claim: "true"
          included.custom.audience: "osac-api"
    - name: username
      description: Username claim
      protocol: openid-connect
      attributes:
        include.in.token.scope: "false"
        display.on.consent.screen: "true"
      protocolMappers:
      - name: username
        protocol: openid-connect
        protocolMapper: oidc-usermodel-attribute-mapper
        consentRequired: false
        config:
          aggregate.attrs: "false"
          introspection.token.claim: "true"
          multivalued: "false"
          userinfo.token.claim: "true"
          user.attribute: username
          id.token.claim: "true"
          lightweight.claim: "false"
          access.token.claim: "true"
          claim.name: username
          jsonType.label: String

    - name: groups
      description: Group membership
      protocol: openid-connect
      attributes:
        include.in.token.scope: "false"
        display.on.consent.screen: "true"
      protocolMappers:
      - name: groups
        protocol: openid-connect
        protocolMapper: oidc-group-membership-mapper
        consentRequired: false
        config:
          full.path: "false"
          introspection.token.claim: "true"
          multivalued: "true"
          userinfo.token.claim: "true"
          id.token.claim: "true"
          lightweight.claim: "false"
          access.token.claim: "true"
          claim.name: groups

    defaultDefaultClientScopes:
    - basic
    - username
    - groups

    clients:
    - clientId: osac-cli
      name: OSAC CLI
      enabled: true
      publicClient: true
      standardFlowEnabled: true
      directAccessGrantsEnabled: true
      protocol: openid-connect
      attributes:
        oauth2.device.authorization.grant.enabled: "true"
      defaultClientScopes:
      - basic
      - username
      - groups
      - osac-api
      redirectUris:
      - http://localhost

    - clientId: osac-admin
      name: OSAC administrator
      enabled: true
      clientAuthenticatorType: client-secret
      secret: ${OSAC_ADMIN_CLIENT_SECRET}
      serviceAccountsEnabled: true
      publicClient: false
      standardFlowEnabled: false
      implicitFlowEnabled: false
      directAccessGrantsEnabled: false
      protocol: openid-connect
      fullScopeAllowed: true
      defaultClientScopes:
      - basic
      - username
      - groups
      - osac-api

    - clientId: osac-controller
      name: OSAC controller
      enabled: true
      clientAuthenticatorType: client-secret
      secret: ${OSAC_CONTROLLER_CLIENT_SECRET}
      serviceAccountsEnabled: true
      publicClient: false
      standardFlowEnabled: false
      implicitFlowEnabled: false
      directAccessGrantsEnabled: false
      protocol: openid-connect
      fullScopeAllowed: true
      defaultClientScopes:
      - basic
      - username
      - groups
      - osac-api

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
.
```

Wait for the realm import to complete:

```shell
oc wait keycloakrealmimport -n keycloak osac --for=condition=Done --timeout=300s
```

After the import is done you can delete the `KeycloakRealmImport` resource to clean up the
associated Kubernetes resources, as recommended by the Keycloak documentation:

```shell
oc delete keycloakrealmimport -n keycloak osac
```

## Create the service secrets

The service needs two secrets in its namespace: one for the database connection and one
for the controller's OAuth credentials.

Create the namespace first:

```shell
oc new-project osac || true
```

Then extract the database password and create the connection secret. The commands depend on which
PostgreSQL operator you installed.

If you installed CNPG:

```shell
SERVICE_DB_PASSWORD=$(
  oc get secret -n postgres osac-service-credentials -o json |
  jq -r '.data["password"] | @base64d'
)

oc create secret generic -n osac fulfillment-database \
--from-literal=url="postgres://osac-rw.postgres.svc.cluster.local:5432/service?sslmode=verify-full" \
--from-literal=user=service \
--from-literal=password="${SERVICE_DB_PASSWORD}"
```

If you installed the Crunchy operator:

```shell
SERVICE_DB_PASSWORD=$(
  oc get secret -n postgres osac-pguser-service -o json |
  jq -r '.data["password"] | @base64d'
)

oc create secret generic -n osac fulfillment-database \
--from-literal=url="postgres://osac-primary.postgres.svc.cluster.local:5432/service?sslmode=verify-full" \
--from-literal=user=service \
--from-literal=password="${SERVICE_DB_PASSWORD}"
```

If you installed the Zalando operator:

```shell
SERVICE_DB_PASSWORD=$(
  oc get secret -n postgres service.osac.credentials.postgresql.acid.zalan.do -o json |
  jq -r '.data["password"] | @base64d'
)

oc create secret generic -n osac fulfillment-database \
--from-literal=url="postgres://osac.postgres.svc.cluster.local:5432/service?sslmode=verify-full" \
--from-literal=user=service \
--from-literal=password="${SERVICE_DB_PASSWORD}"
```

Create the secret containing the controller's OAuth client credentials. The client secret must match
the one you used for the `osac-controller` client in the Keycloak realm import above:

```shell
oc create secret generic -n osac fulfillment-controller-credentials \
--from-literal=client-id=osac-controller \
--from-literal=client-secret="${OSAC_CONTROLLER_CLIENT_SECRET}"
```

## Install the service

Create a values file for the service chart. This configures the service for OpenShift, points it at
the Keycloak issuer, and wires up the database and controller credential secrets:

```shell
cat > service-values.yaml <<.
variant: openshift

externalHostname: fulfillment-api-osac.${DOMAIN}
internalHostname: fulfillment-internal-api-osac.${DOMAIN}

certs:
  issuerRef:
    name: osac-ca
  caBundle:
    configMap: ca-bundle

auth:
  issuerUrl: https://keycloak-keycloak.${DOMAIN}/realms/osac
  controllerCredentials:
  - secret:
      name: fulfillment-controller-credentials
      items:
      - key: client-id
        param: client-id
      - key: client-secret
        param: client-secret

database:
  connection:
  - secret:
      name: fulfillment-database
      items:
      - key: url
        param: url
      - key: user
        param: user
      - key: password
        param: password
  - configMap:
      name: ca-bundle
      items:
      - key: bundle.pem
        param: sslrootcert

idp:
  provider: keycloak
  url: https://keycloak-keycloak.${DOMAIN}
  credentials:
  - secret:
      name: fulfillment-controller-credentials
      items:
      - key: client-id
        param: client-id
      - key: client-secret
        param: client-secret
.
```

Install the service using Helm:

```shell
helm upgrade fulfillment-service oci://ghcr.io/osac-project/charts/fulfillment-service \
--install \
--namespace osac \
--values service-values.yaml \
--wait
```

You can add `--version <version>` to pin a specific chart version if needed. See the
[releases page](https://github.com/osac-project/osac/releases) for available
versions.

## Verify the installation

To verify that the installation is working, use the `osac` CLI to log in and run a simple command.

First, extract the CA bundle so the CLI can verify TLS certificates:

```shell
oc get configmap -n osac ca-bundle -o json | jq -r '.data["bundle.pem"]' > bundle.pem
```

Log in using the `osac-admin` service account credentials via the internal API:

```shell
osac login \
--ca-file bundle.pem \
--flow credentials \
--client-id osac-admin \
--client-secret "${OSAC_ADMIN_CLIENT_SECRET}" \
--private \
https://fulfillment-internal-api-osac.${DOMAIN}
```

If the login succeeds without errors, the service is running and authentication is working
correctly. You can inspect the access token to verify that it contains the expected `username`
claim:

```shell
osac get token --payload
```

The output should be a JSON object containing at least a `username` field with the value
`service-account-osac-admin`. If this claim is missing, the realm configuration is incorrect and
authorization will fail.

You can then try listing resources to confirm the API is responding:

```shell
osac get clusters
```

This should return an empty list or a list of clusters, depending on the state of the service.

You can clean up the `bundle.pem` file and the `service-values.yaml` file if you no longer need
them, but keep the values file if you plan to upgrade the service later.
