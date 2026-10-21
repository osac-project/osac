#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

infra_render=$(helm template test charts/osac-infra --namespace osac-infra \
  --values values/dev/kind-infra.yaml \
  --values values/dev/kind-infra-devfull.yaml \
  --show-only templates/bundled-openbao.yaml)
if ! grep -Fq 'value: "https://keycloak.osac.localhost:8443/realms/osac"' <<<"$infra_render"; then
  echo "dev-full OpenBao must use the advertised Keycloak issuer" >&2
  exit 1
fi

profile_values=(
  --values values/dev/kind-instance.yaml
  --values values/dev/kind-instance-devfull.yaml
)
chart_render=$(helm template test charts/osac-devstack --namespace osac \
  "${profile_values[@]}" --set ui.enabled=true)
osac_render=$(helm template osac charts/osac --namespace osac \
  --values values/dev/kind-instance.yaml \
  --values values/dev/kind-instance-devfull.yaml \
  --set-string global.osacDeploymentId=osac/osac)
if awk 'BEGIN { RS = "---" } /kind: Deployment/ && /name: osac-ui/ { found = 1 } END { exit found ? 0 : 1 }' \
  <<<"$osac_render"; then
  echo "dev-full must keep the umbrella chart's UI disabled to avoid a second release owner" >&2
  exit 1
fi

if ! grep -Fq 'FULFILLMENT_TLS_CA_FILE: "/etc/osac-ui/tls/api/bundle.pem"' <<<"$chart_render" \
  || ! grep -Fq 'OIDC_TLS_CA_FILE: "/etc/osac-ui/tls/auth/bundle.pem"' <<<"$chart_render" \
  || ! grep -Fq 'FULFILLMENT_API_URL: "https://fulfillment-api.osac.localhost:8000"' <<<"$chart_render" \
  || ! grep -Fq 'OIDC_ISSUER_URL: "https://keycloak.osac.localhost:8443/realms/osac"' <<<"$chart_render" \
  || ! grep -Fq 'imagePullPolicy: IfNotPresent' <<<"$chart_render" \
  || ! grep -Fq 'mountPath: /etc/osac-ui/tls/api' <<<"$chart_render" \
  || ! grep -Fq 'mountPath: /etc/osac-ui/tls/auth' <<<"$chart_render" \
  || ! grep -Fq 'automountServiceAccountToken: false' <<<"$chart_render"; then
  echo "dev-full must deploy the shared osac-ui chart with verified TLS and Kind image settings" >&2
  exit 1
fi

if ! awk 'BEGIN { RS = "---" } /kind: Deployment/ && /name: osac-ui/ { count++ } END { exit count == 1 ? 0 : 1 }' \
  <<<"$chart_render"; then
  echo "osac-devstack must deploy exactly one UI Deployment through its chart dependency" >&2
  exit 1
fi
if awk 'BEGIN { RS = "---" } /kind: Route/ && /name: osac-ui/ { found = 1 } END { exit found ? 0 : 1 }' \
  <<<"$chart_render"; then
  echo "dev-full on Kind must disable the OpenShift UI Route" >&2
  exit 1
fi

ui_route_enabled_render=$(helm template test charts/osac-devstack --namespace osac \
  --set ui.route.enabled=true)
if ! awk 'BEGIN { RS = "---" } /kind: Route/ && /name: osac-ui/ { found = 1 } END { exit !found }' \
  <<<"$ui_route_enabled_render"; then
  echo "the shared UI chart must retain its configurable OpenShift Route" >&2
  exit 1
fi

ui_route_render=$(helm template test charts/osac-devstack --namespace osac --show-only templates/ui.yaml)
if ! grep -Fq 'kind: HTTPRoute' <<<"$ui_route_render" \
  || ! grep -Fq 'ui.osac.localhost' <<<"$ui_route_render" \
  || ! grep -Fq 'name: osac-ui' <<<"$ui_route_render"; then
  echo "dev-full must route the shared osac-ui Service through Gateway API" >&2
  exit 1
fi

for template in \
  templates/hooks/provision-tenant.yaml \
  templates/hooks/seed-catalog.yaml; do
  rendered=$(helm template test charts/osac-devstack --namespace osac "${profile_values[@]}" --show-only "$template")
  if ! grep -Fq 'name: ca-bundle' <<<"$rendered" \
    || ! grep -Fq 'key: bundle.pem' <<<"$rendered" \
    || ! grep -Fq 'mountPath: /etc/ca-bundle' <<<"$rendered"; then
    echo "$template must mount the trusted CA bundle" >&2
    exit 1
  fi
done

for template in \
  templates/hooks/seed-catalog.yaml \
  templates/hooks/provision-tenant.yaml; do
  rendered=$(helm template test charts/osac-devstack --namespace osac "${profile_values[@]}" --show-only "$template")
  if ! grep -Fq 'name: FULFILLMENT_ISSUER_URL' <<<"$rendered" \
    || ! grep -Fq 'name: FULFILLMENT_CLIENT_ID' <<<"$rendered" \
    || ! grep -Fq 'secretName: "fulfillment-controller-credentials"' <<<"$rendered" \
    || ! grep -Fq 'key: "client-id"' <<<"$rendered" \
    || ! grep -Fq 'key: "client-secret"' <<<"$rendered" \
    || ! grep -Fq 'mountPath: /var/run/secrets/fulfillment-client-secret' <<<"$rendered" \
    || ! grep -Fq 'value: "https://keycloak.osac.localhost:8443/realms/osac"' <<<"$rendered"; then
    echo "$template must provide service-account credentials by Secret reference" >&2
    exit 1
  fi
done

custom_auth_values=(
  --set service.auth.controllerCredentials[0].secret.name=custom-fulfillment-credentials
  --set service.auth.controllerCredentials[0].secret.items[0].key=custom-client-id
  --set service.auth.controllerCredentials[0].secret.items[1].key=custom-client-secret
)
for template in \
  templates/hooks/seed-catalog.yaml \
  templates/hooks/provision-tenant.yaml; do
  rendered=$(helm template test charts/osac-devstack --namespace osac \
    "${profile_values[@]}" "${custom_auth_values[@]}" --show-only "$template")
  if ! grep -Fq 'name: "custom-fulfillment-credentials"' <<<"$rendered" \
    || ! grep -Fq 'key: "custom-client-id"' <<<"$rendered" \
    || ! grep -Fq 'secretName: "custom-fulfillment-credentials"' <<<"$rendered" \
    || ! grep -Fq 'key: "custom-client-secret"' <<<"$rendered"; then
    echo "$template must use custom Fulfillment credential Secret references" >&2
    exit 1
  fi
done

for template in \
  templates/hooks/seed-catalog.yaml \
  templates/hooks/provision-tenant.yaml; do
  rendered=$(helm template test charts/osac-devstack --namespace osac \
    "${profile_values[@]}" --show-only "$template")
  if ! grep -Fq 'fsGroup: 1001' <<<"$rendered"; then
    echo "$template must make the mounted client-secret file readable by the CLI image" >&2
    exit 1
  fi
done

if ! grep -Fq 'fulfillment-auth.sh: |' <<<"$chart_render" \
  || ! grep -Fq 'get_fulfillment_access_token()' <<<"$chart_render" \
  || grep -Fq 'kubectl -n "${NS}" create token admin' <<<"$chart_render"; then
  echo "dev-full Fulfillment callers must exchange service-account credentials for Keycloak tokens" >&2
  exit 1
fi

rendered_script() {
  local script_name="$1"
  awk -v script_name="${script_name}" \
    '$0 == "  " script_name ": |" { in_script = 1; next }
     in_script && /^  [^ ]/ { exit }
     in_script { sub(/^    /, ""); print }' <<<"$chart_render"
}

seed_catalog_script=$(rendered_script "seed-catalog-simple.sh")
if ! grep -Fq 'AUTH_TOKEN="$(get_fulfillment_access_token)"' <<<"$seed_catalog_script" \
  || ! grep -Fq 'Authorization: Bearer ${AUTH_TOKEN}' <<<"$seed_catalog_script"; then
  echo "dev-full catalog seeding must authenticate Fulfillment requests with a Keycloak service-account token" >&2
  exit 1
fi

provision_tenant_script=$(rendered_script "provision-tenant.sh")
if ! grep -Fq 'fulfillment_token="$(get_fulfillment_access_token)"' <<<"$provision_tenant_script" \
  || ! grep -Fq '"authorization: Bearer ${FULFILLMENT_TOKEN}"' <<<"$provision_tenant_script" \
  || ! grep -Fq -- '--from-file=token=/dev/stdin' <<<"$provision_tenant_script"; then
  echo "dev-full tenant provisioning must pass its Keycloak token to grpcurl through the temporary Secret" >&2
  exit 1
fi

if ! grep -Fq -- '"-expand-headers"' <<<"$chart_render" \
  || ! grep -Fq 'authorization: Bearer ${FULFILLMENT_TOKEN}' <<<"$chart_render" \
  || ! grep -Fq 'kubectl -n "${NS}" delete secret "${TOKEN_SECRET_NAME}"' <<<"$chart_render"; then
  echo "dev-full tenant gRPC call must read and clean up its temporary Fulfillment-token Secret" >&2
  exit 1
fi

if ! grep -Fq -- '-cacert' <<<"$chart_render" \
  || ! grep -Fq -- '--cacert' <<<"$chart_render" \
  || grep -Eq 'curl.*[[:space:]](-k|--insecure)([[:space:]]|$)|grpcurl.*[[:space:]]-insecure' <<<"$chart_render"; then
  echo "dev-full setup hooks must verify TLS without insecure flags" >&2
  exit 1
fi

awx_render=$(helm template test charts/osac-devstack --namespace osac "${profile_values[@]}" --show-only templates/awx-instance.yaml)
if ! awk 'BEGIN { RS = "---" } /kind: AWX/ { found = 1; if ($0 !~ /app\.kubernetes\.io\/name: osac-devstack/ || $0 ~ /app\.kubernetes\.io\/managed-by/) bad = 1 } END { exit !(found && !bad) }' <<<"$awx_render"; then
  echo "AWX resource must use chart selector labels without the operator-owned managed-by label" >&2
  exit 1
fi

proxy_render=$(helm template test charts/osac-devstack --namespace osac "${profile_values[@]}" \
  --show-only charts/awx-operator/templates/deployment-awx-operator-controller-manager.yaml \
  | scripts/dev-full/helm-post-renderer/helm-post-render.sh)
if ! grep -Fq 'image: ghcr.io/kube-rbac-proxy/kube-rbac-proxy:v0.22.1' <<<"$proxy_render" \
  || grep -Fq 'image: gcr.io/kubebuilder/kube-rbac-proxy:v0.15.0' <<<"$proxy_render"; then
  echo "AWX operator post-renderer must replace the unavailable proxy image" >&2
  exit 1
fi

echo "Dev-full shared UI TLS and Helm post-render checks passed."
