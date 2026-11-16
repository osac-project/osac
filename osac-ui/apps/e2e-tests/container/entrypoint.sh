#!/usr/bin/env bash
# Entrypoint of the OSAC UI E2E image: run run-ui-e2e.sh with the container
# arguments, then copy the Playwright report and test results to
# OSAC_UI_E2E_OUTPUT when that directory is mounted. Preserves a runner failure
# or reports an artifact-copy failure after a successful run.
set -uo pipefail

ui_dir="${OSAC_UI_DIR:-/work/osac-ui}"
output_dir="${OSAC_UI_E2E_OUTPUT:-/tmp/test-output}"

export_rc=0
if [[ -d "${output_dir}" ]]; then
  for result in playwright-report test-results; do
    if ! rm -rf -- "${output_dir:?}/${result}"; then
      echo "Failed to clear ${result} artifacts." >&2
      export_rc=1
    fi
  done
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
