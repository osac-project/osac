#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

assert_storage_config_value() {
  local rendered="$1" expected="$2"
  if ! grep -Fq 'name: storage-operations-ig' <<<"${rendered}" \
    || ! grep -Fq "ENABLE_CERTIFICATE_VALIDATION: \"${expected}\"" <<<"${rendered}"; then
    echo "storage-operations-ig must receive ENABLE_CERTIFICATE_VALIDATION=${expected}" >&2
    return 1
  fi
}

aap_chart=../osac-aap/charts/aap
default_aap_render=$(helm template aap "${aap_chart}" \
  --set instanceGroups.storageFulfillment.enabled=true)
assert_storage_config_value "${default_aap_render}" true

override_render=$(helm template aap "${aap_chart}" \
  --set instanceGroups.storageFulfillment.enabled=true \
  --set instanceGroups.storageFulfillment.config.ENABLE_CERTIFICATE_VALIDATION=true \
  --set instanceGroups.storageFulfillment.enable_certificate_validation=false)
assert_storage_config_value "${override_render}" false
if [[ "$(grep -Fc 'ENABLE_CERTIFICATE_VALIDATION:' <<<"${override_render}")" -ne 1 ]]; then
  echo "the storage-group setting must render once and remain authoritative over config-map overrides" >&2
  exit 1
fi

render_main_chart() {
  local values_file="$1"
  helm template osac charts/osac \
    --values charts/osac/ci/default-values.yaml \
    --values "${values_file}" \
    --set service.externalHostname=fulfillment-api.example.com \
    --set service.internalHostname=fulfillment-internal-api.example.com \
    --set global.clusterDomain=example.com \
    --set aap.instanceGroups.storageFulfillment.enabled=true \
    --set ui.auth.oidcIssuerURL=https://keycloak-keycloak.example.com/realms/osac
}

main_default_render=$(helm template osac charts/osac \
  --values charts/osac/ci/default-values.yaml \
  --set aap.instanceGroups.storageFulfillment.enabled=true)
assert_storage_config_value "${main_default_render}" true

main_vmaas_render=$(render_main_chart values/vmaas-ci/instance.yaml)
assert_storage_config_value "${main_vmaas_render}" false

main_dev_render=$(render_main_chart values/dev/instance.yaml)
assert_storage_config_value "${main_dev_render}" false

echo "Certificate-validation Helm values rendered with secure defaults and profile overrides."
