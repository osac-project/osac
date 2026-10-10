#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
CHART_DIR="${SCRIPT_DIR}/../charts/osac"
RENDERED=$(mktemp)
trap 'rm -f "${RENDERED}"' EXIT

helm template osac "${CHART_DIR}" \
  --namespace osac \
  --values "${CHART_DIR}/ci/default-values.yaml" \
  --set hubAccess.enabled=true \
  >"${RENDERED}"

python3 - "${RENDERED}" <<'PY'
import re
import sys
from pathlib import Path

rendered = Path(sys.argv[1]).read_text()
cluster_role = next(
    (
        document
        for document in rendered.split("\n---\n")
        if "kind: ClusterRole\n" in document
        and "name: osac-hub-access\n" in document
    ),
    None,
)
if cluster_role is None:
    raise SystemExit("hub-access ClusterRole was not rendered")

secret_rule = re.search(
    r'(?ms)^  - apiGroups:\n      - ""\n    resources:\n      - secrets\n    verbs:\n((?:      - .+(?:\n|$))+)',
    cluster_role,
)
if secret_rule is None:
    raise SystemExit("hub-access Secret RBAC rule was not rendered")

verbs = set(re.findall(r"^      - (.+)$", secret_rule.group(1), re.MULTILINE))
missing = {"create", "patch"} - verbs
if missing:
    raise SystemExit(f"hub-access Secret RBAC rule is missing verbs: {sorted(missing)}")
PY
