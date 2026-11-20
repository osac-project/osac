#!/usr/bin/env bash
# Entrypoint of the OSAC UI E2E image: run run-ui-e2e.sh with the container
# arguments, then copy the Playwright report and test results to
# OSAC_UI_E2E_OUTPUT when that directory is mounted. Preserves a runner failure
# or reports an artifact-copy failure after a successful run.
set -uo pipefail

ui_dir="${OSAC_UI_DIR:-/work/osac-ui}"
output_dir="${OSAC_UI_E2E_OUTPUT:-/tmp/test-output}"
export OSAC_E2E_CREDENTIALS_DIR="${OSAC_E2E_CREDENTIALS_DIR:-/work/tenant-credentials}"

export_rc=0
if [[ -d "${output_dir}" ]]; then
  for result in playwright-report test-results; do
    if ! rm -rf -- "${output_dir:?}/${result}"; then
      echo "Failed to clear ${result} artifacts." >&2
      export_rc=1
    fi
  done
fi

# Tenant recovery credentials must outlive a removed container. Listing and
# help do not authenticate or create tenants, so they need no mount.
needs_credentials_mount=true
for arg in "$@"; do
  case "$arg" in
    --help|-h|--list) needs_credentials_mount=false ;;
  esac
done
if [[ "$needs_credentials_mount" == true ]]; then
  if ! mountpoint -q -- "$OSAC_E2E_CREDENTIALS_DIR" ||
    [[ ! -w "$OSAC_E2E_CREDENTIALS_DIR" ]]; then
    echo 'Mount a private writable tenant credentials directory at /work/tenant-credentials.' >&2
    exit 1
  fi
  mode=$(stat -c '%a' -- "$OSAC_E2E_CREDENTIALS_DIR") || exit 1
  if [[ ! "$mode" =~ ^[0-7]{3,4}$ ]] || (( (8#$mode & 077) != 0 )); then
    echo 'The tenant credentials directory must not be accessible by group or others.' >&2
    exit 1
  fi
fi

rc=0
bash "${ui_dir}/apps/e2e-tests/run_scripts/run-ui-e2e.sh" \
  --osac-ui-dir="${ui_dir}" "$@" || rc=$?

if [[ -d "${output_dir}" ]]; then
  for result in playwright-report test-results; do
    if [[ -d "${ui_dir}/apps/playwright/${result}" ]]; then
      if [[ -e "${output_dir}/${result}" || -L "${output_dir}/${result}" ]] ||
        ! cp -r -- "${ui_dir}/apps/playwright/${result}" "${output_dir}/${result}"; then
        echo "Failed to copy ${result} artifacts." >&2
        export_rc=1
      fi
    fi
  done
fi

if [[ "$rc" -eq 0 && "$export_rc" -ne 0 ]]; then
  rc="$export_rc"
fi
exit "${rc}"
