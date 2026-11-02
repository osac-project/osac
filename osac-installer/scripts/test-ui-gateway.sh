#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

url="${OSAC_UI_URL:-http://ui.osac.localhost:8080}"
check_ui_route() {
  local status

  status="$(curl --silent --connect-timeout 5 --max-time 15 \
    --output /dev/null --write-out '%{http_code}' "$url")" || return 1
  [[ "${status}" == "200" ]]
}

if ! retry_until 60 5 check_ui_route; then
  echo "OSAC UI Gateway was not reachable at ${url} with HTTP 200." >&2
  exit 1
fi

echo "OSAC UI Gateway is reachable at ${url}"
