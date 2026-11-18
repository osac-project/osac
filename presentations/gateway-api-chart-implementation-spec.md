# OSAC-7866 — Chart implementation spec: remove bundled Envoy, add Gateway API

Scope: make `fulfillment-service` installable with the OpenShift CIO-managed
Gateway API instead of the bundled `fulfillment-ingress-proxy` Envoy, behind a
toggle, with the bundled Envoy not deployed in gateway mode. Derived from the
validated POC (OCP 4.22.14, see `gateway-api-poc-final-report.md`). Epic
OSAC-7243, task OSAC-7866.

Default stays `envoy` (fully backward compatible). `gatewayapi` is opt-in.

## Resolved design decisions

- **Cert into `openshift-ingress`:** `certs.issuerRef.kind` is `ClusterIssuer`, so
  issue a second cert-manager `Certificate` directly into the gateway namespace.
  No secret copy / reflector needed.
- **Backend CA:** backends (grpc-server/rest-gateway/console-proxy) each hold a
  `Certificate` from the same ClusterIssuer. BackendTLSPolicy needs a ConfigMap
  keyed `ca.crt` with that CA. The shared `ca-bundle` is keyed `bundle.pem`, so a
  dedicated `osac-backend-ca` (key `ca.crt`) is required, materialized via a
  trust-manager `Bundle` (trust-manager is already a dependency).
- **In-cluster bridge:** target the gateway **Service ClusterIP** (stable across
  pod replacement, no controller) rather than pinning the gateway pod IP. Kept as
  a value (`bridge: service|pod`) so the pod-IP + operator-controller path remains
  an option.
- **GatewayClass:** cluster-scoped and triggers the CIO istiod install, so it is a
  prerequisite installed by `osac-installer`, not the per-release service chart.
- **D5 / ISSUE-2:** both are route-config only. No `fulfillment-service` Go change.
  gRPC routes use `request: 0s`; REST keeps `300s`; `Connect` is routed by `Exact`
  match (not regex) so it is never shadowed by the broad gRPC rule.

## What is already in place (no change)

- `ingress-proxy/certificate.yaml` (`fulfillment-api-tls`) already carries SANs for
  the apps hostnames AND the bare `fulfillment-api` / `fulfillment-internal-api`
  names — covers all four gateway listeners.
- `httpCorsAllowedOrigins` already provides app-side CORS. In gateway mode the
  gateway has no CORS filter, so the UI origin (and any cross-origin caller) MUST
  be listed here or browser calls regress. No new key; required configuration.

## 1. `values.yaml` additions (`charts/service/values.yaml`)

```yaml
# Ingress driver: how the public and internal APIs are exposed.
#   envoy      - bundled fulfillment-ingress-proxy Envoy (default; current behavior)
#   gatewayapi - OpenShift CIO-managed Gateway API; the bundled Envoy is not deployed
ingress:
  driver: envoy

  gatewayapi:
    # Namespace of the CIO-managed Gateway (where istiod/Envoy run).
    gatewayNamespace: openshift-ingress
    # GatewayClass installed as a cluster prerequisite by osac-installer.
    gatewayClassName: openshift-default
    gatewayName: osac-gateway
    # How in-cluster callers reach the gateway:
    #   service - selectorless Services -> gateway Service ClusterIP (stable; recommended)
    #   pod     - managed EndpointSlice -> gateway pod IP (needs osac-operator controller)
    bridge: service
    # Request timeouts. gRPC stream routes MUST be 0s (uncapped); REST stays bounded.
    grpcRequestTimeout: "0s"
    restRequestTimeout: "300s"
    jwksRequestTimeout: "10s"
    # ConfigMap (keyed ca.crt) for BackendTLSPolicy upstream validation.
    backendCaConfigMap: osac-backend-ca
```

## 2. `values.schema.json` additions (closed enums per the chart invariant)

```json
"ingress": {
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "driver": { "enum": ["envoy", "gatewayapi"] },
    "gatewayapi": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "gatewayNamespace":   { "type": "string" },
        "gatewayClassName":   { "type": "string" },
        "gatewayName":        { "type": "string" },
        "bridge":             { "enum": ["service", "pod"] },
        "grpcRequestTimeout": { "type": "string" },
        "restRequestTimeout": { "type": "string" },
        "jwksRequestTimeout": { "type": "string" },
        "backendCaConfigMap": { "type": "string" }
      }
    }
  }
}
```

## 3. Template changes (`charts/service/templates/`)

### Gate the bundled Envoy off (wrap with `{{- if eq .Values.ingress.driver "envoy" }}`)
- `ingress-proxy/deployment.yaml`
- `ingress-proxy/configmap.yaml` (+ `_config.yaml.tpl`)
- the **metrics** Service block in `ingress-proxy/service.yaml`

### Keep, but adapt
- `ingress-proxy/certificate.yaml` — **not gated**; both modes need `fulfillment-api-tls`.
- `ingress-proxy/service.yaml` — `fulfillment-api` and `fulfillment-internal-api`
  render in both modes; drop the `selector` when `gatewayapi`:
  ```yaml
  spec:
  {{- if eq .Values.ingress.driver "envoy" }}
    selector:
      app: fulfillment-ingress-proxy
  {{- end }}
    ports:
    - name: external-api   # / internal-api
      port: 8000           # / 8001
      targetPort: external-api
  ```

### New `templates/gateway-api/` (all wrapped `{{- if eq .Values.ingress.driver "gatewayapi" }}`)

| File | Renders |
|---|---|
| `gateway.yaml` | `Gateway` in `ingress.gatewayapi.gatewayNamespace`, class `gatewayClassName`, name `gatewayName`. 4 HTTPS/443 Terminate listeners: `external`->`externalHostname`, `internal`->`internalHostname`, `ext-svc`->`fulfillment-api`, `int-svc`->`fulfillment-internal-api`. certRef `fulfillment-api-tls`. `allowedRoutes.namespaces` Selector `kubernetes.io/metadata.name={{ .Release.Namespace }}`. |
| `certificate-gateway.yaml` | second `Certificate` (same SANs + issuer as `ingress-proxy/certificate.yaml`), `secretName: fulfillment-api-tls`, in the gateway namespace. |
| `httproute-external.yaml` | hostnames `externalHostname` + `fulfillment-api`; attached to `external` + `ext-svc`. Rules: console-ws `Exact /api/fulfillment/v1/console_sessions/connect` -> console-proxy:8090 (`0s`); jwks `Exact /.well-known/jwks.json` -> rest-gateway:8000 (`jwksRequestTimeout`); REST `PathPrefix /api/fulfillment` + `/api/events` -> rest-gateway:8000 (`restRequestTimeout`); **Connect `Exact /osac.public.v1.ConsoleProxy/Connect` -> console-proxy:8000 (`grpcRequestTimeout`)**; unary `RegularExpression /(osac\.public\.|grpc\.(reflection|health)\.).*` -> grpc-server:8000 (`grpcRequestTimeout`). |
| `httproute-internal.yaml` | hostnames `internalHostname` + `fulfillment-internal-api`; attached to `internal` + `int-svc`. Same console-ws/jwks; REST `PathPrefix /api`; Connect `Exact` as above; unary `PathPrefix /` -> grpc-server:8000 (`grpcRequestTimeout`). |
| `backendtlspolicy.yaml` | ×3 (grpc-server / rest-gateway / console-proxy) -> `caCertificateRefs` = `backendCaConfigMap`, `validation.hostname = fulfillment-<svc>.{{ .Release.Namespace }}.svc.cluster.local`. |
| `backend-ca-bundle.yaml` | trust-manager `Bundle` -> target ConfigMap `backendCaConfigMap` with key `ca.crt`, sourced from the ClusterIssuer CA. |
| `bridge.yaml` | `bridge=service`: selectorless Services wired (EndpointSlice) to the gateway **Service ClusterIP**:443 with port names `external-api` / `internal-api`. `bridge=pod`: `EndpointSlice` -> gateway pod IP:443 (requires the osac-operator reconciler). Delete-legacy-Endpoints concern disappears because the Services are chart-managed selectorless from the start. |

Note: no separate `Watch` route is needed — same backend and `0s` timeout as the
unary catch-all. Routing is specificity-driven (Exact/Prefix), never dependent on
RegEx ordering.

## 4. `osac-installer` wiring

- **GatewayClass** prerequisite (cluster-scoped) in the deps/prereq layer, rendered
  when gateway mode is enabled, with the `Accepted` / `ControllerInstalled` /
  `CRDsReady` readiness wait and a check that no `servicemesh` Subscription appears.
- **Enable per profile** via a CI overlay / profile values passthrough:
  ```yaml
  fulfillment-service:
    ingress:
      driver: gatewayapi
    httpCorsAllowedOrigins:
      - "https://osac-ui-osac.apps.<cluster>"
  ```
- Mirror the new keys in the umbrella `values.schema.json` if it duplicates them.

## 5. Greenfield doc fixes (`gateway-api-greenfield-install.md`)

- `Validation`: drop the "only after the heartbeat ships" caveat on
  "Watch survives >5 min".
- `Known gaps`: remove "D5 idle timeout — needs the Watch heartbeat"; D5 is closed
  by the `0s` route config alone (no code change).

## 6. Validation / dry-run plan

1. `helm lint charts/service -f charts/service/ci-values.yaml` (envoy mode, default).
2. `helm template t charts/service -f ci-values.yaml` — baseline renders unchanged.
3. `helm template t charts/service -f ci-values.yaml --set ingress.driver=gatewayapi`
   and confirm: no ingress-proxy Deployment/ConfigMap, Services selectorless,
   Gateway + 2 HTTPRoutes + 3 BackendTLSPolicy + backend-ca Bundle + gateway cert
   present, Connect rule is `Exact`, gRPC rules `0s`, REST `300s`.
4. `helm lint` in gateway mode; schema accepts the new keys and rejects bad enums.
5. Functional: deploy on a real OpenShift 4.22 cluster (CIO Gateway API). Kind
   cannot provide the CIO-managed gateway (see Testing environments below).

## 7. Testing environments

- **helm dry-run (template/lint):** no cluster. Validates both modes render and the
  schema. This is the immediate gate for the chart PR.
- **Kind (`PROFILE=kind`/`dev`):** runs OSAC locally; good for Envoy-mode, general
  chart validation, and the `it`/e2e suites. It CANNOT simulate the OpenShift
  CIO-managed Gateway API (`openshift.io/gateway-controller/v1` + embedded Sail)
  — that is OpenShift-specific. Upstream Istio + Gateway API CRDs could approximate
  it, but RegEx precedence is implementation-specific, so it would not faithfully
  reproduce the CIO behavior the POC validated.
- **Laptop OpenShift (CRC 4.22) or a lab SNO:** the only way to exercise the real
  gateway path end-to-end. CRC is resource-heavy (~9+ GB RAM) but is a real
  OpenShift with Gateway API GA on 4.19+.
