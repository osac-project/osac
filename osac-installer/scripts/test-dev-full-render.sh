#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

chart_render=$(helm template test charts/osac-devstack --namespace osac)
ui_render=$(helm template test charts/osac-devstack --namespace osac --show-only templates/ui.yaml)
if ! grep -Fq 'FULFILLMENT_TLS_INSECURE: "0"' <<<"$ui_render" \
  || ! grep -Fq 'OIDC_TLS_INSECURE: "0"' <<<"$ui_render" \
  || ! grep -Fq 'FULFILLMENT_TLS_CA_FILE: "/etc/ca-bundle/bundle.pem"' <<<"$ui_render" \
  || ! grep -Fq 'OIDC_TLS_CA_FILE: "/etc/ca-bundle/bundle.pem"' <<<"$ui_render"; then
  echo "dev-full UI must verify fulfillment and OIDC TLS with the CA bundle" >&2
  exit 1
fi

for template in \
  templates/ui.yaml \
  templates/hooks/provision-tenant.yaml \
  templates/hooks/seed-catalog.yaml; do
  rendered=$(helm template test charts/osac-devstack --namespace osac --show-only "$template")
  if ! grep -Fq 'name: ca-bundle' <<<"$rendered" \
    || ! grep -Fq 'key: bundle.pem' <<<"$rendered" \
    || ! grep -Fq 'mountPath: /etc/ca-bundle' <<<"$rendered"; then
    echo "$template must mount the trusted CA bundle" >&2
    exit 1
  fi
done

if ! grep -Fq -- '-cacert' <<<"$chart_render" \
  || ! grep -Fq -- '--cacert' <<<"$chart_render" \
  || grep -Eq 'curl.*[[:space:]](-k|--insecure)([[:space:]]|$)|grpcurl.*[[:space:]]-insecure' <<<"$chart_render"; then
  echo "dev-full setup hooks must verify TLS without insecure flags" >&2
  exit 1
fi

awx_render=$(helm template test charts/osac-devstack --namespace osac --show-only templates/awx-instance.yaml)
if ! awk 'BEGIN { RS = "---" } /kind: AWX/ { found = 1; if ($0 !~ /app\.kubernetes\.io\/name: osac-devstack/ || $0 ~ /app\.kubernetes\.io\/managed-by/) bad = 1 } END { exit !(found && !bad) }' <<<"$awx_render"; then
  echo "AWX resource must use chart selector labels without the operator-owned managed-by label" >&2
  exit 1
fi

proxy_render=$(helm template test charts/osac-devstack --namespace osac \
  --show-only charts/awx-operator/templates/deployment-awx-operator-controller-manager.yaml \
  | scripts/dev-full/helm-post-renderer/helm-post-render.sh)
if ! grep -Fq 'image: ghcr.io/kube-rbac-proxy/kube-rbac-proxy:v0.22.1' <<<"$proxy_render" \
  || grep -Fq 'image: gcr.io/kubebuilder/kube-rbac-proxy:v0.15.0' <<<"$proxy_render"; then
  echo "AWX operator post-renderer must replace the unavailable proxy image" >&2
  exit 1
fi

echo "Dev-full verified TLS and Helm post-render checks passed."
