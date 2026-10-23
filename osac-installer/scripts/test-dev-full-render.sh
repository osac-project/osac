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

infra_cudn_crd_render=$(helm template test charts/osac-infra --namespace osac-infra \
  --values values/dev/kind-infra.yaml \
  --values values/dev/kind-infra-devfull.yaml \
  --show-only templates/fake-crds.yaml)
if ! grep -Fq 'name: clusteruserdefinednetworks.k8s.ovn.org' <<<"$infra_cudn_crd_render" \
  || ! grep -Fq 'group: k8s.ovn.org' <<<"$infra_cudn_crd_render" \
  || ! grep -Fq 'kind: ClusterUserDefinedNetwork' <<<"$infra_cudn_crd_render" \
  || ! grep -Fq 'name: v1' <<<"$infra_cudn_crd_render" \
  || ! grep -Fq 'x-kubernetes-preserve-unknown-fields: true' <<<"$infra_cudn_crd_render" \
  || ! grep -Fq 'scope: Cluster' <<<"$infra_cudn_crd_render" \
  || grep -Fq 'finalizers:' <<<"$infra_cudn_crd_render"; then
  echo "dev-full must render the cluster-scoped, non-finalized Kind CUDN API placeholder in osac-infra" >&2
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
if ! awk '/name: OSAC_ENABLE_NETWORKING_PROVISIONING/ { getline; if ($0 ~ /value: "true"/) found = 1 } END { exit !found }' \
  <<<"$osac_render"; then
  echo "dev-full must enable Subnet AAP provisioning so every tenant gets a target namespace" >&2
  exit 1
fi
if awk 'BEGIN { RS = "---" } /kind: Deployment/ && /name: osac-ui/ { found = 1 } END { exit found ? 0 : 1 }' \
  <<<"$osac_render"; then
  echo "dev-full must keep the umbrella chart's UI disabled to avoid a second release owner" >&2
  exit 1
fi

if grep -Eq '^kind: MutatingAdmissionPolicy(Binding)?$' <<<"$chart_render"; then
  echo "the ARM64 VM compatibility policy must be disabled by default and for Linux dev-full" >&2
  exit 1
fi

arm64_compat_render=$(helm template test charts/osac-devstack --namespace osac \
  --set arm64VmCompatibility.enabled=true)
policy_count=$(grep -c '^kind: MutatingAdmissionPolicy$' <<<"$arm64_compat_render" || true)
binding_count=$(grep -c '^kind: MutatingAdmissionPolicyBinding$' <<<"$arm64_compat_render" || true)
if [[ "$policy_count" != 1 || "$binding_count" != 1 ]]; then
  echo "Apple Silicon dev-full must render exactly one VM compatibility policy and binding" >&2
  exit 1
fi
if ! grep -Fq 'apiVersion: admissionregistration.k8s.io/v1' <<<"$arm64_compat_render" \
  || ! grep -Fq 'operations: ["CREATE", "UPDATE"]' <<<"$arm64_compat_render" \
  || ! grep -Fq 'resources: ["virtualmachines"]' <<<"$arm64_compat_render" \
  || ! grep -Fq 'failurePolicy: Fail' <<<"$arm64_compat_render" \
  || ! grep -Fq 'operator: Exists' <<<"$arm64_compat_render" \
  || ! grep -Fq 'key: osac.openshift.io/tenant' <<<"$arm64_compat_render" \
  || ! grep -Fq '/spec/template/spec/domain/features/smm' <<<"$arm64_compat_render" \
  || ! grep -Fq '/spec/template/spec/domain/features/hyperv' <<<"$arm64_compat_render" \
  || ! grep -Fq '/spec/template/spec/domain/clock/timer/hyperv' <<<"$arm64_compat_render"; then
  echo "the ARM64 VM compatibility resources must scope and remove only the planned KubeVirt fields" >&2
  exit 1
fi

mac_arm_make_output=$(make --no-print-directory --dry-run install-devstack \
  PLATFORM=kind PROFILE=dev-full NS=osac HOST_IS_MAC_ARM=true)
if ! grep -Fq -- '--set arm64VmCompatibility.enabled=true' <<<"$mac_arm_make_output" \
  || ! grep -Eq '^[[:space:]]*scripts/dev-full/check-cluster-compatibility\.sh[[:space:]]*$' <<<"$mac_arm_make_output"; then
  echo "Apple Silicon dev-full must check node architecture and enable the compatibility policy" >&2
  exit 1
fi

linux_make_output=$(make --no-print-directory --dry-run install-devstack \
  PLATFORM=kind PROFILE=dev-full NS=osac HOST_IS_MAC_ARM=false)
if grep -Fq 'arm64VmCompatibility.enabled=true' <<<"$linux_make_output" \
  || grep -Fq -- 'check-cluster-compatibility.sh' <<<"$linux_make_output"; then
  echo "Linux dev-full must not enable the ARM64 VM compatibility policy or run its compatibility check" >&2
  exit 1
fi

dev_make_output=$(make --no-print-directory --dry-run install \
  PLATFORM=kind PROFILE=dev NS=osac HOST_IS_MAC_ARM=true)
if grep -Fq 'osac-devstack' <<<"$dev_make_output" \
  || grep -Fq 'arm64VmCompatibility.enabled=true' <<<"$dev_make_output"; then
  echo "PROFILE=dev must not install the dev-full ARM64 VM compatibility policy" >&2
  exit 1
fi

preflight_stub_dir=$(mktemp -d)
trap 'rm -rf "$preflight_stub_dir"' EXIT
cat >"$preflight_stub_dir/kubectl" <<'KUBECTL'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  version)
    if [[ "${2:-}" != "-o" || "${3:-}" != "json" ]]; then
      echo "kubectl version must request JSON: $*" >&2
      exit 2
    fi
    printf '{"serverVersion":{"gitVersion":"%s"}}\n' "${MOCK_SERVER_VERSION:-}"
    ;;
  get)
    printf '%s\n' "${MOCK_NODE_ARCHITECTURES:-}" | tr ' ' '\n'
    ;;
  *)
    echo "unexpected kubectl invocation: $*" >&2
    exit 2
    ;;
esac
KUBECTL
chmod +x "$preflight_stub_dir/kubectl"

compatibility_check=scripts/dev-full/check-cluster-compatibility.sh
if ! PATH="$preflight_stub_dir:$PATH" MOCK_SERVER_VERSION=v1.36.0 \
  MOCK_NODE_ARCHITECTURES='arm64 arm64' "$compatibility_check" >/dev/null; then
  echo "the installer must accept a Kubernetes 1.36 API server with arm64 nodes" >&2
  exit 1
fi
if ! PATH="$preflight_stub_dir:$PATH" MOCK_SERVER_VERSION=v1.37.1 \
  MOCK_NODE_ARCHITECTURES='arm64' "$compatibility_check" >/dev/null; then
  echo "the installer must accept Kubernetes API servers newer than 1.36" >&2
  exit 1
fi
if output=$(PATH="$preflight_stub_dir:$PATH" MOCK_SERVER_VERSION=v1.35.9 \
  MOCK_NODE_ARCHITECTURES='arm64' "$compatibility_check" 2>&1); then
  echo "the installer must reject Kubernetes API servers older than 1.36" >&2
  exit 1
elif ! grep -Fq 'requires a Kubernetes API server v1.36 or later' <<<"$output" \
  || ! grep -Fq 'Upgrade this Kind cluster or recreate it' <<<"$output"; then
  echo "an older Kubernetes API server must produce actionable remediation" >&2
  exit 1
fi
if output=$(PATH="$preflight_stub_dir:$PATH" MOCK_SERVER_VERSION=v1.36.0 \
  MOCK_NODE_ARCHITECTURES='arm64 amd64' "$compatibility_check" 2>&1); then
  echo "Apple Silicon dev-full must reject non-arm64 Kind nodes" >&2
  exit 1
elif ! grep -Fq 'requires arm64 Kind nodes' <<<"$output"; then
  echo "a non-arm64 Kind cluster must produce an architecture error" >&2
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
if ! grep -Fq 'kubectl get clusteruserdefinednetwork' <<<"$provision_tenant_script" \
  || ! grep -Fq 'timed out waiting for Subnet AAP' <<<"$provision_tenant_script" \
  || grep -Fq 'kubectl create namespace' <<<"$provision_tenant_script"; then
  echo "dev-full tenant hook must verify AAP-created namespace/CUDN resources instead of masking failures" >&2
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

echo "Dev-full render, ARM64 admission, cluster-version, TLS, and Helm post-render checks passed."
