#!/usr/bin/env bash
# Helm-contract test for the storage certificate-validation control
# (aap.instanceGroups.storageFulfillment.enable_certificate_validation):
#   - charts/osac must default the value to true in values.yaml and
#     values.schema.json so wizard and UI consumers surface a secure default.
#   - The AAP chart renders the storage instance-group ConfigMap with the
#     provider-scoped VAST_VALIDATE_CERTS worker variable, and the explicit
#     Helm setting stays authoritative over low-level config-map overrides.
#   - Development profiles (dev, kind dev-full, vmaas-ci) disable validation
#     for their self-signed provider certificates.
#   - The devstack AWX hook propagates the setting into its pod environment
#     and merges it into AWX_TASK_ENV without clobbering unrelated entries,
#     and the direct dev-full installer passes the same variable through.
#
# Charts are rendered directly (the devstack chart is flattened to drop its
# packaged dependencies), so this suite needs no `helm dep build` run.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
OSAC_CHART_DIR=$(cd -- "${SCRIPT_DIR}/../charts/osac" && pwd)
DEVSTACK_CHART_DIR=$(cd -- "${SCRIPT_DIR}/../charts/osac-devstack" && pwd)
AAP_CHART_DIR=$(cd -- "${SCRIPT_DIR}/../../osac-aap/charts/aap" && pwd)
VALUES_DIR=$(cd -- "${SCRIPT_DIR}/../values" && pwd)
HELM_BIN=${HELM_BIN:-helm}
TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT

echo "checking umbrella chart certificate-validation default and schema"
python3 - "${OSAC_CHART_DIR}/values.yaml" "${OSAC_CHART_DIR}/values.schema.json" <<'PY'
import json
import sys

import yaml

with open(sys.argv[1], encoding="utf-8") as values_file:
    values = yaml.safe_load(values_file)
storage_tls = values["aap"]["instanceGroups"]["storageFulfillment"]["enable_certificate_validation"]
assert storage_tls is True

with open(sys.argv[2], encoding="utf-8") as schema_file:
    schema = json.load(schema_file)
setting = (
    schema["properties"]["aap"]["properties"]["instanceGroups"]["properties"][
        "storageFulfillment"
    ]["properties"]["enable_certificate_validation"]
)
assert setting["type"] == "boolean" and setting["default"] is True
PY

# Assert a rendered AAP chart exposes the storage TLS policy through exactly
# one provider-scoped worker variable in the storage instance-group ConfigMap.
assert_storage_validate_certs() {
  local rendered="$1" expected="$2"
  python3 - "${rendered}" "${expected}" <<'PY'
import sys

import yaml

with open(sys.argv[1], encoding="utf-8") as rendered_file:
    resources = [item for item in yaml.safe_load_all(rendered_file) if isinstance(item, dict)]
matches = [
    item for item in resources
    if item.get("kind") == "ConfigMap"
    and item.get("metadata", {}).get("name") == "storage-operations-ig"
]
assert len(matches) == 1, (
    f"expected one storage-operations-ig ConfigMap, found {len(matches)}"
)
data = matches[0].get("data", {})
assert data.get("VAST_VALIDATE_CERTS") == sys.argv[2], (
    f"expected VAST_VALIDATE_CERTS={sys.argv[2]!r}, got {data.get('VAST_VALIDATE_CERTS')!r}"
)
assert "ENABLE_CERTIFICATE_VALIDATION" not in data, "generic worker variable must not be rendered"
PY
  if [[ $(grep -Fc 'VAST_VALIDATE_CERTS:' "${rendered}") -ne 1 ]]; then
    echo "ERROR: expected exactly one VAST_VALIDATE_CERTS setting in the AAP render" >&2
    exit 1
  fi
}

render_aap_storage() {
  local name="$1" expected="$2"
  shift 2
  local rendered="${TMP_DIR}/${name}.yaml"
  echo "checking ${name} (expected VAST TLS validation=${expected})"
  "${HELM_BIN}" template aap "${AAP_CHART_DIR}" "$@" >"${rendered}"
  assert_storage_validate_certs "${rendered}" "${expected}"
}

render_aap_storage storage-validate-certs-default true \
  --set instanceGroups.storageFulfillment.enabled=true

# The storage policy owns this worker variable; a low-level config value
# cannot override the explicit Helm setting.
render_aap_storage storage-validate-certs-override false \
  --set instanceGroups.storageFulfillment.enabled=true \
  --set instanceGroups.storageFulfillment.enable_certificate_validation=false \
  --set instanceGroups.storageFulfillment.config.VAST_VALIDATE_CERTS=true

# Flatten an umbrella-chart profile for direct rendering of the AAP subchart.
extract_aap_values() {
  local profile="$1" destination="$2"
  python3 - "${profile}" >"${destination}" <<'PY'
import sys

import yaml

with open(sys.argv[1], encoding="utf-8") as profile_file:
    profile = yaml.safe_load(profile_file) or {}
print(yaml.safe_dump(profile.get("aap", {}), sort_keys=False), end="")
PY
}

extract_aap_values "${VALUES_DIR}/dev/instance.yaml" "${TMP_DIR}/dev-aap-values.yaml"
render_aap_storage storage-validate-certs-dev-profile false \
  --values "${TMP_DIR}/dev-aap-values.yaml" \
  --set instanceGroups.storageFulfillment.enabled=true

extract_aap_values "${VALUES_DIR}/vmaas-ci/instance.yaml" "${TMP_DIR}/vmaas-ci-aap-values.yaml"
render_aap_storage storage-validate-certs-vmaas-ci-profile false \
  --values "${TMP_DIR}/vmaas-ci-aap-values.yaml" \
  --set instanceGroups.storageFulfillment.enabled=true

# Render the devstack AWX hook without its packaged chart dependencies: copy
# the hook, its script ConfigMap, and their inputs into a flattened chart.
devstack_flat_chart="${TMP_DIR}/osac-devstack"
mkdir -p "${devstack_flat_chart}/templates/hooks"
cp "${DEVSTACK_CHART_DIR}/Chart.yaml" "${devstack_flat_chart}/Chart.yaml"
cp "${DEVSTACK_CHART_DIR}/values.yaml" "${devstack_flat_chart}/values.yaml"
cp "${DEVSTACK_CHART_DIR}/templates/_helpers.tpl" "${devstack_flat_chart}/templates/_helpers.tpl"
cp "${DEVSTACK_CHART_DIR}/templates/scripts-configmap.yaml" \
  "${devstack_flat_chart}/templates/scripts-configmap.yaml"
cp "${DEVSTACK_CHART_DIR}/templates/hooks/configure-awx.yaml" \
  "${devstack_flat_chart}/templates/hooks/configure-awx.yaml"
cp -R "${DEVSTACK_CHART_DIR}/files" "${devstack_flat_chart}/files"
python3 - "${devstack_flat_chart}/Chart.yaml" <<'PY'
import sys

import yaml

chart_path = sys.argv[1]
with open(chart_path, encoding="utf-8") as chart_file:
    chart = yaml.safe_load(chart_file)
chart.pop("dependencies", None)
with open(chart_path, "w", encoding="utf-8") as chart_file:
    yaml.safe_dump(chart, chart_file, sort_keys=False)
PY

echo "checking devstack AWX hook default"
awx_hook_default_render=$("${HELM_BIN}" template test "${devstack_flat_chart}" --namespace osac \
  --show-only templates/hooks/configure-awx.yaml)
if ! grep -Fq 'name: VAST_VALIDATE_CERTS' <<<"${awx_hook_default_render}" \
  || ! grep -Fq 'value: "true"' <<<"${awx_hook_default_render}"; then
  echo "ERROR: the devstack AWX hook must default VAST certificate validation to enabled" >&2
  exit 1
fi

echo "checking devstack AWX hook dev-full profile override"
awx_hook_devfull_render=$("${HELM_BIN}" template test "${devstack_flat_chart}" --namespace osac \
  --values "${VALUES_DIR}/dev/kind-instance.yaml" \
  --values "${VALUES_DIR}/dev/kind-instance-devfull.yaml" \
  --show-only templates/hooks/configure-awx.yaml)
if ! grep -Fq 'name: VAST_VALIDATE_CERTS' <<<"${awx_hook_devfull_render}" \
  || ! grep -Fq 'value: "false"' <<<"${awx_hook_devfull_render}"; then
  echo "ERROR: the dev-full AWX hook must honor its development VAST certificate override" >&2
  exit 1
fi
if grep -Fq 'name: ENABLE_CERTIFICATE_VALIDATION' <<<"${awx_hook_devfull_render}"; then
  echo "ERROR: the AWX hook must use the provider-scoped VAST worker variable" >&2
  exit 1
fi

echo "checking the rendered AWX hook script"
devstack_render=$("${HELM_BIN}" template test "${devstack_flat_chart}" --namespace osac)
rendered_script=$(awk '
  $0 == "  configure-awx.sh: |" { in_script = 1; next }
  in_script && /^  [^ ]/ { exit }
  in_script { sub(/^    /, ""); print }
' <<<"${devstack_render}")
if ! grep -Fq 'build_awx_settings_payload()' <<<"${rendered_script}" \
  || ! grep -Fq 'settings_payload=$(build_awx_settings_payload' <<<"${rendered_script}" \
  || ! grep -Fq '"VAST_VALIDATE_CERTS": sys.argv[3]' <<<"${rendered_script}"; then
  echo "ERROR: the AWX hook must merge its task environment through the payload helper" >&2
  exit 1
fi
bash -n <<<"${rendered_script}"

# Exercise the same AWX settings transformation the hook renders, with fixture
# input. Unrelated global task environment entries must survive while the
# OSAC VAST policy remains authoritative.
echo "checking AWX_TASK_ENV merge with fixture input"
source "${DEVSTACK_CHART_DIR}/files/configure-awx.sh"
awx_settings_payload=$(build_awx_settings_payload \
  '{"AWX_TASK_ENV":{"EXISTING_SETTING":"preserve-me","VAST_VALIDATE_CERTS":"true"}}' \
  osac false)
python3 - "${awx_settings_payload}" <<'PY'
import json
import sys

settings = json.loads(sys.argv[1])
task_env = settings["AWX_TASK_ENV"]
assert task_env["EXISTING_SETTING"] == "preserve-me"
assert task_env["ANSIBLE_JINJA2_NATIVE"] == "true"
assert task_env["OSAC_STORAGE_CONFIG_NAMESPACE"] == "osac"
assert task_env["VAST_VALIDATE_CERTS"] == "false"
assert settings["AWX_COLLECTIONS_ENABLED"] is False
assert settings["AWX_ROLES_ENABLED"] is False
PY
if build_awx_settings_payload '{"AWX_TASK_ENV":"not-an-object"}' osac false >/dev/null 2>&1; then
  echo "ERROR: the AWX task environment merge must reject non-object fixture input" >&2
  exit 1
fi

if ! grep -Fq '"VAST_VALIDATE_CERTS": os.environ["VAST_VALIDATE_CERTS"]' \
  "${SCRIPT_DIR}/dev-full/install-awx.sh"; then
  echo "ERROR: the direct dev-full AWX installer must also pass the provider-scoped VAST setting" >&2
  exit 1
fi
bash -n "${SCRIPT_DIR}/dev-full/install-awx.sh"

echo "Certificate-validation render checks passed: secure default, dev overrides, and AWX propagation."
