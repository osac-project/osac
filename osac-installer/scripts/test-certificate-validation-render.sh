#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

# Assert the rendered storage instance-group ConfigMap has the expected value.
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

tmpdir=$(mktemp -d)
trap 'rm -rf "${tmpdir}"' EXIT

devstack_chart="${tmpdir}/osac-devstack"
mkdir -p "${devstack_chart}/templates/hooks"
cp charts/osac-devstack/Chart.yaml "${devstack_chart}/Chart.yaml"
cp charts/osac-devstack/values.yaml "${devstack_chart}/values.yaml"
cp charts/osac-devstack/templates/_helpers.tpl "${devstack_chart}/templates/_helpers.tpl"
cp charts/osac-devstack/templates/hooks/configure-awx.yaml \
  "${devstack_chart}/templates/hooks/configure-awx.yaml"
cp charts/osac-devstack/templates/scripts-configmap.yaml \
  "${devstack_chart}/templates/scripts-configmap.yaml"
cp -R charts/osac-devstack/files "${devstack_chart}/files"
python3 - "${devstack_chart}/Chart.yaml" <<'PY'
import sys

import yaml

chart_path = sys.argv[1]
with open(chart_path, encoding="utf-8") as chart_file:
    chart = yaml.safe_load(chart_file)
chart.pop("dependencies", None)
with open(chart_path, "w", encoding="utf-8") as chart_file:
    yaml.safe_dump(chart, chart_file, sort_keys=False)
PY

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

extract_aap_values values/vmaas-ci/instance.yaml "${tmpdir}/vmaas-ci-aap.yaml"
main_vmaas_render=$(helm template aap "${aap_chart}" \
  --values "${tmpdir}/vmaas-ci-aap.yaml" \
  --set instanceGroups.storageFulfillment.enabled=true)
assert_storage_config_value "${main_vmaas_render}" false

extract_aap_values values/dev/instance.yaml "${tmpdir}/dev-aap.yaml"
main_dev_render=$(helm template aap "${aap_chart}" \
  --values "${tmpdir}/dev-aap.yaml" \
  --set instanceGroups.storageFulfillment.enabled=true)
assert_storage_config_value "${main_dev_render}" false

python3 - <<'PY'
import json
import yaml

with open("charts/osac/values.yaml", encoding="utf-8") as values_file:
    default = yaml.safe_load(values_file)["aap"]["instanceGroups"]["storageFulfillment"]
assert default["enable_certificate_validation"] is True

with open("charts/osac/values.schema.json", encoding="utf-8") as schema_file:
    schema = json.load(schema_file)
setting = schema["properties"]["aap"]["properties"]["instanceGroups"]["properties"]["storageFulfillment"]["properties"]["enable_certificate_validation"]
assert setting["type"] == "boolean" and setting["default"] is True
PY

devstack_default_render=$(helm template test "${devstack_chart}" --namespace osac \
  --show-only templates/hooks/configure-awx.yaml)
if ! grep -Fq 'name: ENABLE_CERTIFICATE_VALIDATION' <<<"${devstack_default_render}" \
  || ! grep -Fq 'value: "true"' <<<"${devstack_default_render}"; then
  echo "devstack AWX configuration must default certificate validation to enabled" >&2
  exit 1
fi

devstack_profile_render=$(helm template test "${devstack_chart}" --namespace osac \
  --values values/dev/kind-instance.yaml \
  --values values/dev/kind-instance-devfull.yaml \
  --show-only templates/hooks/configure-awx.yaml)
if ! grep -Fq 'name: ENABLE_CERTIFICATE_VALIDATION' <<<"${devstack_profile_render}" \
  || ! grep -Fq 'value: "false"' <<<"${devstack_profile_render}"; then
  echo "dev-full AWX configuration must disable validation for its development profile" >&2
  exit 1
fi

devstack_render=$(helm template test "${devstack_chart}" --namespace osac)
rendered_script=$(awk '
  $0 == "  configure-awx.sh: |" { in_script = 1; next }
  in_script && /^  [^ ]/ { exit }
  in_script { sub(/^    /, ""); print }
' <<<"${devstack_render}")
if ! grep -Fq 'settings.get("AWX_TASK_ENV") or {}' <<<"${rendered_script}" \
  || ! grep -Fq '"ENABLE_CERTIFICATE_VALIDATION": sys.argv[3]' <<<"${rendered_script}"; then
  echo "AWX configuration must preserve existing task environment and apply the storage TLS setting" >&2
  exit 1
fi
bash -n <<<"${rendered_script}"
if ! grep -Fq '"ENABLE_CERTIFICATE_VALIDATION": os.environ["ENABLE_CERTIFICATE_VALIDATION"]' \
  scripts/dev-full/install-awx.sh; then
  echo "the direct dev-full AWX installer must also pass its optional TLS setting" >&2
  exit 1
fi
bash -n scripts/dev-full/install-awx.sh

echo "Certificate-validation Helm values rendered with secure defaults and dev overrides."
