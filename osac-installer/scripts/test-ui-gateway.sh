#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

url="${OSAC_UI_URL:-http://ui.osac.localhost:8080}"
status="$(curl --silent --show-error --connect-timeout 5 --max-time 15 --output /dev/null --write-out '%{http_code}' "$url")"

if [[ "$status" != "200" ]]; then
  echo "OSAC UI Gateway returned HTTP ${status}, expected 200" >&2
  exit 1
fi

echo "OSAC UI Gateway is reachable at ${url}"
