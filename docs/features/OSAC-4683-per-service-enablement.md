# Per-service enablement

This guide explains how a Cloud Provider Admin selects the OSAC service tiers
that are deployed, verifies the resulting configuration, and diagnoses requests
to disabled services.

The examples describe the current OSAC implementation. The umbrella chart uses
the values path `global.services.<service>.enabled`. The shorter
`services.*.enabled` wording used in the feature requirements refers to these
values under the chart's global key.

## Service tiers

OSAC has four service-tier switches:

| Service | Value | Default | Scope |
| --- | --- | --- | --- |
| CaaS | `global.services.caas.enabled` | true | Cluster orders, templates, catalog items, and versions |
| VMaaS | `global.services.vmaas.enabled` | true | Compute instances, disk images, instance types, volumes, and console sessions |
| BMaaS | `global.services.bmaas.enabled` | true | Bare-metal instances and bare-metal instance types |
| MaaS | `global.services.maas.enabled` | true | MaaS enablement signal; no MaaS gRPC services or operator controller currently exist |

The default values keep all service tiers enabled. A service is disabled by
setting its enabled value to false in the umbrella chart values:

```yaml
global:
  services:
    caas:
      enabled: true
    vmaas:
      enabled: true
    bmaas:
      enabled: false
    maas:
      enabled: false
```

The service values are consumed by the fulfillment-service chart, the
osac-operator chart, and the umbrella chart's bare-metal operator dependency.

## Service-to-component mapping

### Fulfillment-service

The fulfillment-service gRPC server and REST gateway receive the same boolean
command-line flags:

| Service | Flag |
| --- | --- |
| CaaS | `--enable-caas` |
| VMaaS | `--enable-vmaas` |
| BMaaS | `--enable-bmaas` |
| MaaS | `--enable-maas` |

The chart adds a flag to each container command when the corresponding global
service value is enabled. If the binary is started without any enable flags,
the fulfillment-service enables all four services for backward compatibility.

The REST gateway currently registers its generated handlers for all services.
Calls for a disabled service are rejected by the fulfillment-service gRPC
server and are returned by the gateway as HTTP 503. See Disabled-service
behavior for the verification procedure.

### osac-operator

The operator maps service tiers to controller environment variables:

| Service | Controller | Environment variable |
| --- | --- | --- |
| CaaS | ClusterOrder | `OSAC_ENABLE_CLUSTER_CONTROLLER` |
| VMaaS | ComputeInstance | `OSAC_ENABLE_COMPUTE_INSTANCE_CONTROLLER` |
| BMaaS | BareMetalInstance | `OSAC_ENABLE_BAREMETAL_INSTANCE_CONTROLLER` |

The chart's `controllerEnabled` helper uses an explicit
`operator.controllers.*` value when one is set. Otherwise it falls back to the
matching `global.services.*.enabled` value. Tenant, storage, volume, and
networking controllers are separate shared-infrastructure settings.

MaaS currently has no operator controller. Its value is still propagated to
the fulfillment-service flag set and Capabilities response so clients can
discover the configured tier.

### Bare-metal fulfillment operator

The umbrella chart uses `global.services.bmaas.enabled` as the dependency
condition for the bare-metal fulfillment operator. When BMaaS is false, the
BMF operator deployment is not installed. The separate BMF CRD dependency
remains installed so disabling the operator does not remove its CRDs.

## Selecting services

Use a values file containing the `global.services` settings, then pass it to
the umbrella chart. The dependency rules in the chart must be satisfied before
Helm can install or upgrade the release.

The next sections show the complete installation, upgrade, verification, and
troubleshooting procedures.

## Dependency validation

The umbrella chart validates the service combination before it creates or
updates workloads:

- CaaS can be enabled only when VMaaS or BMaaS is also enabled.
- MaaS can be enabled only when CaaS is enabled.

These rules are expressed in the chart's `values.schema.json`. Helm applies
them to both `helm install` and `helm upgrade`. An invalid combination fails
schema validation before the release is deployed or updated.

For example, this configuration is invalid because CaaS has no compute
backing service:

```bash
helm template osac osac-installer/charts/osac \
  --set global.services.caas.enabled=true \
  --set global.services.vmaas.enabled=false \
  --set global.services.bmaas.enabled=false
```

This configuration is invalid because MaaS requires CaaS:

```bash
helm template osac osac-installer/charts/osac \
  --set global.services.maas.enabled=true \
  --set global.services.caas.enabled=false
```

The fulfillment-service validates the same service dependencies when it starts
outside Helm. Its startup sequence first enables all four services when no
enable flags are set, then validates the resulting flags before creating the
server. The current error messages are:

- invalid service flags: CaaS requires at least one of VMaaS or BMaaS
- invalid service flags: MaaS requires CaaS

The current osac-operator startup validation checks the CaaS dependency on at
least one compute controller. MaaS has no operator controller, so the MaaS
dependency is enforced by the Helm schema and fulfillment-service validation.

## Install with selected services

1. Copy the default values and set the service choices. This example keeps
   CaaS and VMaaS enabled and disables BMaaS and MaaS:

   ```yaml
   global:
     services:
       caas:
         enabled: true
       vmaas:
         enabled: true
       bmaas:
         enabled: false
       maas:
         enabled: false
   ```

2. Install the umbrella chart:

   ```bash
   helm install osac osac-installer/charts/osac \
     --namespace <namespace> \
     --create-namespace \
     --values values.yaml
   ```

3. Confirm the rendered result before applying it in a change-controlled
   environment:

   ```bash
   helm template osac osac-installer/charts/osac \
     --namespace <namespace> \
     --values values.yaml
   ```

The rendered fulfillment-service containers contain only the enable flags for
the selected tiers. The operator receives false for the BMaaS controller, and
the BMF operator deployment is omitted. Shared infrastructure controllers and
the BMF CRD dependency remain available.

## Enable a service after installation

To enable an additional service, update the matching global value and run
`helm upgrade`. For example, to enable BMaaS:

```yaml
global:
  services:
    bmaas:
      enabled: true
```

Then run:

```bash
helm upgrade osac osac-installer/charts/osac \
  --namespace <namespace> \
  --values values.yaml
```

The upgrade rolls the affected workloads. The fulfillment-service starts with
the BMaaS flag, the operator enables its bare-metal controller unless an
explicit controller override says otherwise, and Helm creates the BMF operator
deployment because its dependency condition is now true. Verify the effective
state through the Capabilities endpoint after the rollout completes.

This guide covers initial selective enablement and enabling an additional
service. The lifecycle of resources that already exist when a service is
disabled is not defined by this feature.

## Verify enabled services

The public Capabilities endpoint is available without an authentication token.
Query it after the deployment rollout:

```bash
curl --cacert <ca-bundle.pem> \
  https://<public-api-host>/api/fulfillment/v1/capabilities | jq .
```

The `enabled_services` field is a repeated `ServiceTier` enum. Its REST/JSON
representation uses these generated enum values:

| Enum value | Service tier |
| --- | --- |
| `SERVICE_TIER_CAAS` | CaaS |
| `SERVICE_TIER_VMAAS` | VMaaS |
| `SERVICE_TIER_BMAAS` | BMaaS |
| `SERVICE_TIER_MAAS` | MaaS |

For the partial configuration in this guide, the response includes:

```json
{
  "enabled_services": [
    "SERVICE_TIER_CAAS",
    "SERVICE_TIER_VMAAS"
  ]
}
```

The public and private Capabilities servers use the same service flags. To
check the private API, use an authenticated gRPC request:

```bash
grpcurl \
  -cacert <ca-bundle.pem> \
  -H "authorization: Bearer $OSAC_TOKEN" \
  <private-api-host>:443 \
  osac.private.v1.Capabilities/Get
```

With all four services enabled, `enabled_services` contains
`SERVICE_TIER_CAAS`, `SERVICE_TIER_VMAAS`, `SERVICE_TIER_BMAAS`, and
`SERVICE_TIER_MAAS`. Generated gRPC clients expose the same values as typed
`ServiceTier` constants. The list is generated from the process startup
configuration and does not change until the workload is restarted after a
Helm upgrade.

The public and private Capabilities responses use the same enum values and
ordering. UI behavior based on these generated types is handled by the UI
team in a follow-up change.

The Capabilities endpoint is intentionally anonymous on the public API. This
allows clients to discover the available service tiers before authenticating
for service-specific operations.

## Disabled-service behavior

### gRPC

Known service methods that are not enabled return `codes.Unavailable`. For
example, calling a VMaaS method when VMaaS is disabled returns:

```text
the VMaaS service is not enabled on this server
```

The current service group names in this message are CaaS, VMaaS, and BMaaS.
Calls to a genuinely unknown gRPC method retain the default
`codes.Unimplemented` response.

Disabled service implementations are not registered with the gRPC server, so
they do not appear in gRPC reflection. For example:

```bash
grpcurl -cacert <ca-bundle.pem> <public-api-host>:443 list
```

When BMaaS is disabled, the BMaaS service names should be absent while enabled
CaaS and VMaaS services remain visible.

### REST

The REST gateway currently registers generated handlers for all service
groups. A request to a disabled service is sent to the fulfillment-service,
where the gRPC request is rejected. The gateway returns HTTP 503 and does not
return a valid payload for the disabled service.

For example:

```bash
curl --cacert <ca-bundle.pem> \
  --header "authorization: Bearer $OSAC_TOKEN" \
  --write-out "\n%{http_code}\n" \
  https://<public-api-host>/api/fulfillment/v1/baremetal_instances
```

An enabled service should return its normal API response. A disabled service
should return 503.

### Shared infrastructure

Disabling a service tier does not disable shared infrastructure. Tenant,
networking, storage, Capabilities, and HostTypes services remain registered.
The current checkout does not yet apply service filtering to HostTypes; see
HostTypes behavior for the OSAC-4681 dependency.

## Logs and metrics

At startup, fulfillment-service emits a Service enablement log entry containing
the enabled service list. Use the workload logs to confirm the flags that the
process accepted:

```bash
oc logs deploy/fulfillment-grpc-server -n <namespace> | grep "Service enablement"
```

Requests rejected for a disabled service increment the current Prometheus
counter:

```text
fulfillment_disabled_service_requests_total
```

The current counter has one label, `service`. Its values use the service group
names used by the handler, such as VMaaS or BMaaS. Query the metrics endpoint
with:

```bash
curl --cacert <ca-bundle.pem> \
  https://<metrics-host>/metrics | grep fulfillment_disabled_service_requests_total
```

The current implementation does not expose a method label on this counter.
Do not use a method label when constructing an alert or dashboard until the
deployed implementation changes.

## Controller and deployment checks

When BMaaS is disabled, check both sides of the deployment:

```bash
oc get deployment -n <namespace> \
  -l app.kubernetes.io/name=bare-metal-fulfillment-operator
```

The BMF operator deployment should be absent. Inspect the osac-operator
deployment environment to confirm:

```bash
oc get deployment -n <namespace> \
  -l app.kubernetes.io/name=operator -o yaml
```

The `OSAC_ENABLE_BAREMETAL_INSTANCE_CONTROLLER` value should be false unless
an explicit operator controller override changes it. The BMF CRD dependency
remains installed.

## HostTypes behavior

HostTypes is shared infrastructure and remains registered when VMaaS or BMaaS
is disabled. In the current checkout, the HostTypes server does not yet apply
service enablement flags: its List and Get operations still delegate to the
normal host-type store without filtering by the enabled compute services.
OSAC-4681 is the implementation work that adds this filtering. Do not use a
HostTypes response from this version as proof that disabled-service filtering
is active.

After OSAC-4681 is included in the deployed version, verify the behavior with
the public HostTypes API, preserving any user-supplied filter in the request:

```bash
grpcurl \
  -cacert <ca-bundle.pem> \
  -H "authorization: Bearer $OSAC_TOKEN" \
  -d '{}' \
  <public-api-host>:443 \
  osac.public.v1.HostTypes/List
```

The service-dependent behavior delivered by that change is:

- VMaaS enabled and BMaaS disabled: entries with empty interfaces remain;
  entries with non-empty interfaces are excluded.
- BMaaS enabled and VMaaS disabled: entries with non-empty interfaces remain;
  entries with empty interfaces are excluded.
- Both VMaaS and BMaaS disabled: List returns no host types.
- Get returns NotFound when the requested host type is excluded by the active
  service configuration.

The filtering applies in addition to the caller's normal HostTypes filter. The
HostTypes API itself remains available in every service combination.
