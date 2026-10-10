# Configuring an external Kafka cluster

**Audience:** Administrators installing OSAC.

The Fulfillment Service requires Kafka to store and read events, including
when metering is disabled. You can use a Kafka cluster managed separately
from the OSAC installer. Before installing the `osac` Helm chart, prepare
the broker access, credentials, and certificate trust described here, then
continue with the [customer install guide](customer-install-guide.md).

This guide covers the current chart's `service.kafka.connection` settings.
Check that your selected OSAC chart release provides them. The
[Strimzi example](#example-using-strimzi) shows how to prepare an existing
operator-managed Kafka cluster. If you enable metering, also complete
[the metering configuration](#optional-metering).

## Broker requirements

Obtain the following from your Kafka administrator:

| Requirement | Configuration |
|-------------|---------------|
| Bootstrap servers | One or more `host:port` addresses, separated by commas, without a URL scheme. |
| Connectivity | OSAC pods must resolve and reach both the bootstrap servers and every broker address advertised by Kafka. Reaching only the bootstrap address is insufficient. |
| Authentication | A dedicated username and password using SASL SCRAM-SHA-512 over TLS (`SASL_SSL`). The Fulfillment Service does not expose a setting for choosing a different SASL mechanism. |
| Certificate trust | The CA certificate chain for the broker listener, in PEM format. The certificate must match the broker hostnames. TLS 1.2 or later is required. |
| Authorization | The ACLs below for the Fulfillment Service's Kafka user. |
| Topic storage | Persistent storage, replication, and retention appropriate for your availability and event recovery requirements. |

Grant these Kafka permissions:

| Resource type | Resource name | Match | Operations |
|---------------|---------------|-------|------------|
| Topic | `osac.events.` | Prefix | `Create`, `Write`, `Read`, `Describe` |
| Consumer group | `osac.system.` | Prefix | `Read`, `Describe` |
| Cluster | Kafka cluster | Cluster-wide | `IdempotentWrite` |

Use prefix matching with the trailing dot: `osac.events.` includes the event
topics for all tenants, and `osac.system.` includes all OSAC system consumer
groups. Do not use a literal name containing `*`. The group ACL is required
by OSAC builds that use consumer-group subscriptions; it can be provisioned
in advance when preparing a broker for an upgrade.

`IdempotentWrite` is needed in addition to the topic permissions. The
Fulfillment Service does not require a Kafka superuser or permissions to
delete topics. See the [Kafka authorization reference](https://kafka.apache.org/42/security/authorization-and-acls/)
for operation-to-resource mappings.

The current deployment relies on automatic creation of new event topics.
Enable `auto.create.topics.enable` on the broker, or have the Kafka
administrator create each required `osac.events.<tenant-id>` topic before
it is used. Granting `Create` alone does not enable automatic creation.
Configure the broker's default replication factor, partition count, and
retention for these dynamically named topics.

The default topic and group prefixes are shared names. Separate OSAC
installations should use separate Kafka clusters unless their Kafka
configuration explicitly isolates their topics and groups; separate
Kubernetes namespaces alone do not provide Kafka isolation.

## Configure OSAC

The examples use `osac` as the OSAC namespace. Replace example broker names
and file paths with the values provided by your Kafka administrator.

### 1. Disable installer-managed Kafka resources

If you install the infrastructure charts, merge this into the
`my-infra-values.yaml` used for **both** `osac-deps` and `osac-infra`:

```yaml
kafka:
  enabled: false
```

This skips installation of the Kafka Operator and bundled Kafka cluster.
It does not stop OSAC from using Kafka. For the separate `osac` platform
chart, also set `kafka.enabled: false` as shown in step 4. In that chart the
toggle skips the Fulfillment Service's installer-managed `KafkaUser` and
credential-copy job. It does not disable metering's Kafka resources.

### 2. Create the connection ConfigMap and password Secret

Create these resources in the **OSAC namespace**, even if Kafka is hosted
in another namespace or outside Kubernetes. Save the password supplied by
the Kafka administrator in a protected file, here `/secure/kafka-password`.

```bash
export NS=osac
oc create namespace "$NS" --dry-run=client -o yaml | oc apply -f -

oc create configmap external-kafka -n "$NS" \
  --from-literal=brokers='kafka-1.example.com:9093,kafka-2.example.com:9093' \
  --from-literal=user=fulfillment-service \
  --dry-run=client -o yaml | oc apply -f -

oc create secret generic external-kafka-credentials -n "$NS" \
  --from-file=password=/secure/kafka-password \
  --dry-run=client -o yaml | oc apply -f -
```

The password belongs in a Secret, not in Helm values. The Secret contains
the password of the existing Kafka user; creating it does not create the
user or grant Kafka permissions.

### 3. Configure certificate trust

The Fulfillment Service uses `service.certs.caBundle.configMap` for Kafka
and other TLS connections. Preserve the CAs it already needs, including
those used for OSAC services and identity providers, and add the broker CA.

For example, after saving the broker's CA chain as `broker-ca.pem`, create
a combined bundle from the existing `ca-bundle` ConfigMap:

```bash
oc get configmap ca-bundle -n "$NS" \
  -o go-template='{{range .data}}{{printf "%s\n" .}}{{end}}' \
  > existing-ca-bundle.pem
cat existing-ca-bundle.pem broker-ca.pem > osac-kafka-ca-bundle.pem

oc create configmap osac-kafka-ca-bundle -n "$NS" \
  --from-file=bundle.pem=osac-kafka-ca-bundle.pem \
  --dry-run=client -o yaml | oc apply -f -
```

If you use a different existing bundle, substitute its name. This creates
a separate ConfigMap so a trust-manager reconciliation does not overwrite
your additions. Use the `bundle.pem` key so the same ConfigMap also works
with metering. Keep the combined bundle updated when either CA changes.
For automated distribution, add the Kafka CA to your existing trust-manager
bundle sources instead and keep using that managed ConfigMap.

### 4. Set the platform Helm values

Merge the following into your complete `my-values.yaml`. Preserve your
other `service` settings when merging:

```yaml
kafka:
  enabled: false

service:
  kafka:
    connection:
      - configMap:
          name: external-kafka
          items:
            - key: brokers
              param: brokers
            - key: user
              param: user
      - secret:
          name: external-kafka-credentials
          items:
            - key: password
              param: password
  certs:
    caBundle:
      configMap: osac-kafka-ca-bundle

metering:
  enabled: false
```

`brokers`, `user`, and `password` are the three required connection
parameters. The `key` fields identify data in the ConfigMap or Secret;
`param` maps each key to the OSAC parameter. The same connection settings
are supplied to the Fulfillment Service components that need Kafka.

Metering is disabled in this base example, matching the chart default.
To enable it, use the additional settings and prerequisites in
[Optional metering](#optional-metering).

Install or upgrade the complete system using your normal `osac` Helm
command and the updated `my-values.yaml`, as described in the
[customer install guide](customer-install-guide.md). The Kafka resources
and credentials must be ready before the OSAC pods start.

## Example using Strimzi

This example uses an existing Strimzi-managed Kafka cluster named
`external-kafka` in namespace `messaging`, in the same Kubernetes cluster
as OSAC. The Kafka cluster is administered independently of the OSAC Helm
release. It uses the `kafka.strimzi.io/v1` API documented for
[Strimzi 0.50.1](https://strimzi.io/docs/operators/0.50.1/deploying).
Use the API version supported by your installed Operator.

### Configure the listener and User Operator

Have the Kafka administrator merge these settings into the existing `Kafka`
resource. This is a **fragment**, not a complete cluster manifest; preserve
the existing Kafka version, node pools, storage, listeners, and other
configuration:

```yaml
spec:
  kafka:
    listeners:
      - name: tls
        port: 9093
        type: internal
        tls: true
        authentication:
          type: scram-sha-512
    authorization:
      type: simple
    config:
      auto.create.topics.enable: true
    # Preserve your replication, partition, and retention settings.
  entityOperator:
    userOperator: {}
    topicOperator: {}
```

An internal listener works when OSAC can reach the Kafka services inside
the same Kubernetes cluster. For Kafka in another cluster, configure a
Strimzi external listener and use its advertised bootstrap address and CA.

### Create the Fulfillment Service user

Save this as `fulfillment-kafka-user.yaml`:

```yaml
apiVersion: kafka.strimzi.io/v1
kind: KafkaUser
metadata:
  name: fulfillment-service
  namespace: messaging
  labels:
    strimzi.io/cluster: external-kafka
spec:
  authentication:
    type: scram-sha-512
  authorization:
    type: simple
    acls:
      - resource:
          type: topic
          name: osac.events.
          patternType: prefix
        operations: [Create, Write, Read, Describe]
        host: "*"
      - resource:
          type: group
          name: osac.system.
          patternType: prefix
        operations: [Read, Describe]
        host: "*"
      - resource:
          type: cluster
        operations: [IdempotentWrite]
        host: "*"
```

The namespace and `strimzi.io/cluster` label must match the Kafka cluster
watched by the User Operator. See the [Strimzi ACL schema](https://strimzi.io/docs/operators/0.50.1/configuring#type-AclRule-reference)
for the supported resource types and prefix matching. Apply the user and
wait for readiness:

```bash
oc apply -n messaging -f fulfillment-kafka-user.yaml
oc wait -n messaging kafkauser/fulfillment-service \
  --for=condition=Ready --timeout=5m
```

Strimzi creates a Secret named `fulfillment-service` with a `password` key.
Use the following commands **instead of step 2's generic connection
commands** to create the OSAC connection resources. They copy only the
password and do not print it to the terminal. They require `jq`:

```bash
export NS=osac
oc create namespace "$NS" --dry-run=client -o yaml | oc apply -f -

oc create configmap external-kafka -n "$NS" \
  --from-literal=brokers=external-kafka-kafka-bootstrap.messaging.svc:9093 \
  --from-literal=user=fulfillment-service \
  --dry-run=client -o yaml | oc apply -f -

oc get secret fulfillment-service -n messaging -o json \
  | jq --arg ns "$NS" '{apiVersion: "v1", kind: "Secret",
      metadata: {name: "external-kafka-credentials", namespace: $ns},
      type: "Opaque", data: {password: .data.password}}' \
  | oc apply -f -

oc get secret external-kafka-cluster-ca-cert -n messaging \
  -o jsonpath='{.data.ca\.crt}' | base64 -d > broker-ca.pem
```

The CA command assumes Strimzi's default listener certificate. If your
Kafka administrator configured a custom listener certificate, obtain its
CA chain instead. Complete steps 3 and 4 using these resources.

The copied password is not synchronized automatically. After a Strimzi
password rotation, repeat the copy and restart the affected OSAC deployments
as described below. Refresh the combined CA bundle when the listener CA
rotates.

## Optional metering

Metering uses its own Kafka user and `osac.metering.` topics. The Fulfillment
Service credentials above do not configure metering.

The current metering chart creates Strimzi `KafkaUser` and `KafkaTopic`
resources and fetches passwords from Secrets in the Kafka namespace. It
requires Strimzi and the target Kafka cluster's User and Topic Operators
in the same Kubernetes cluster as OSAC. Setting the platform's
`kafka.enabled: false` does **not** suppress those metering resources.
Pointing `metering.kafka.brokers` at an arbitrary external broker is
therefore insufficient; the chart has no values-only path for supplying
independent external metering credentials and skipping those resources.

For the Strimzi cluster in this example, merge these additional values:

```yaml
global:
  osacDeploymentId: example-osac-installation

metering:
  enabled: true
  kafka:
    brokers: external-kafka-kafka-bootstrap.messaging.svc:9093
    clusterName: external-kafka
    clusterNamespace: messaging
  certs:
    caBundle:
      configMap: osac-kafka-ca-bundle
```

Choose a unique, stable `global.osacDeploymentId` before the first install;
keep an existing value unchanged. Also provide metering's database
configuration as required by the [customer install guide](customer-install-guide.md).
The installing account needs permission to create the Strimzi resources
and credential-access RBAC in `messaging`.

The chart provisions the `osac-metering` user with `Write` and `Describe`
on the `osac.metering.` topic prefix and cluster `IdempotentWrite`.
It creates lifecycle, heartbeat, inference, corrections, and DLQ topics
with a replication factor of **3**, so the target cluster needs at least
three brokers. Enabled adapters receive separate users with topic read
access, DLQ write access, and access to their own consumer groups; these
groups are separate from the Fulfillment Service's `osac.system.` groups.

For a broker outside this Strimzi setup, keep metering disabled or configure
metering to use a separate Strimzi-managed Kafka cluster that meets these
requirements. In the latter case, include that broker's CA in
`metering.certs.caBundle.configMap` too.

## Verify and maintain the connection

After installation, check the Fulfillment Service deployments and logs:

```bash
oc rollout status deployment/fulfillment-event-publisher -n "$NS" --timeout=5m
oc rollout status deployment/fulfillment-grpc-server -n "$NS" --timeout=5m
oc rollout status deployment/fulfillment-controller -n "$NS" --timeout=5m
oc logs deployment/fulfillment-event-publisher -n "$NS" --tail=100
oc logs deployment/fulfillment-grpc-server -n "$NS" --tail=100
```

Exercise a normal OSAC resource operation and have the Kafka administrator
confirm that its event topic is created and receives events. For builds
using consumer-group subscriptions, also verify activity in the
`osac.system.` groups. Pod readiness alone does not verify all topic and
group permissions.

| Symptom | Check |
|---------|-------|
| Connection timeouts | Bootstrap and advertised broker addresses, DNS, listener ports, and connectivity from OSAC pods. |
| Certificate errors | Broker hostname matches and broker CA inclusion in the configured CA bundle. |
| SASL authentication failure | Username, password, and SCRAM-SHA-512 on the selected listener. |
| Topic authorization failure | Prefix ACL on `osac.events.` with all four topic operations. |
| Group authorization failure | Prefix ACL on `osac.system.` with `Read` and `Describe`. |
| Cluster authorization failure while publishing | Cluster `IdempotentWrite` permission. |
| New event topics never appear | Automatic topic creation enabled, or topics provisioned in advance; `Create` permission and valid replication defaults. |
| Metering waits for credentials | Correct Kafka namespace and cluster label, User Operator readiness, and permission to read its password Secret. |

After updating credentials or broker settings, restart the Fulfillment
Service deployments so all processes load the new connection configuration:

```bash
oc rollout restart -n "$NS" \
  deployment/fulfillment-event-publisher \
  deployment/fulfillment-grpc-server \
  deployment/fulfillment-controller
```

Wait for each rollout to finish using the commands above. If metering or
adapter credentials change, restart their deployments too: their init
containers copy the password when the pods start.
