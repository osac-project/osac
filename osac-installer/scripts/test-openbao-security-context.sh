#!/usr/bin/env bash
# Verify that the OpenBao pod securityContext renders correctly for each
# platform target:
#   - Kind profiles must include a numeric runAsUser (the OpenBao image
#     declares USER openbao, a non-numeric name; Kubernetes cannot verify
#     runAsNonRoot without a numeric UID).
#   - OpenShift CI profiles must omit runAsUser so the restricted-v2 SCC
#     can allocate from the namespace UID range.
#   - Both must include seccompProfile.type RuntimeDefault.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
INFRA_CHART=$(cd -- "${SCRIPT_DIR}/../charts/osac-infra" && pwd)
HELM_BIN=${HELM_BIN:-helm}
TMP_DIR=$(mktemp -d)
trap 'rm -rf "${TMP_DIR}"' EXIT

# Render the osac-infra chart and extract the OpenBao Deployment's pod
# securityContext block.
render_openbao_security_context() {
  local values_file=$1
  "${HELM_BIN}" template osac-infra "${INFRA_CHART}" --values "${values_file}" \
    | awk '/kind: Deployment/{found=0} /app: openbao/{found=1} found && /securityContext:/{sc=1; indent=0; next} sc{
        if(indent==0){match($0,/^[[:space:]]*/); indent=RLENGTH}
        if(NR>1 && match($0,/^[[:space:]]*/)==0){sc=0; next}
        if(RLENGTH<indent && !/^[[:space:]]*$/){sc=0; next}
        print
      }' \
    | head -20
}

errors=0

# --- Kind profile ---
echo "Checking Kind profile (values/dev/kind-infra.yaml)..."
kind_sc=$(render_openbao_security_context "${SCRIPT_DIR}/../values/dev/kind-infra.yaml")

if ! grep -Fq 'runAsUser:' <<<"${kind_sc}"; then
  echo "ERROR: Kind profile must include a numeric runAsUser for OpenBao" >&2
  echo "Rendered securityContext:" >&2
  echo "${kind_sc}" >&2
  errors=$((errors + 1))
fi

if ! grep -Fq 'runAsNonRoot: true' <<<"${kind_sc}"; then
  echo "ERROR: Kind profile must include runAsNonRoot: true" >&2
  errors=$((errors + 1))
fi

if ! grep -Fq 'RuntimeDefault' <<<"${kind_sc}"; then
  echo "ERROR: Kind profile must include seccompProfile type RuntimeDefault" >&2
  errors=$((errors + 1))
fi

# --- OpenShift CI profile (vmaas-ci as representative) ---
echo "Checking OpenShift CI profile (values/vmaas-ci/infra.yaml)..."
ocp_sc=$(render_openbao_security_context "${SCRIPT_DIR}/../values/vmaas-ci/infra.yaml")

if grep -Fq 'runAsUser:' <<<"${ocp_sc}"; then
  echo "ERROR: OpenShift CI profile must NOT include runAsUser (SCC allocates UIDs)" >&2
  echo "Rendered securityContext:" >&2
  echo "${ocp_sc}" >&2
  errors=$((errors + 1))
fi

if ! grep -Fq 'runAsNonRoot: true' <<<"${ocp_sc}"; then
  echo "ERROR: OpenShift CI profile must include runAsNonRoot: true" >&2
  errors=$((errors + 1))
fi

if ! grep -Fq 'RuntimeDefault' <<<"${ocp_sc}"; then
  echo "ERROR: OpenShift CI profile must include seccompProfile type RuntimeDefault" >&2
  errors=$((errors + 1))
fi

if [[ ${errors} -gt 0 ]]; then
  echo "FAILED: ${errors} error(s) in OpenBao securityContext render checks" >&2
  exit 1
fi

echo "OpenBao securityContext render checks passed."
