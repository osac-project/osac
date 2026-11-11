#!/usr/bin/env bash
# Entrypoint of the OSAC UI E2E image: run run-ui-e2e.sh with the container
# arguments, then copy the Playwright report and test results to
# OSAC_UI_E2E_OUTPUT when that directory is mounted. Exits with the runner's
# exit code.
set -uo pipefail

ui_dir="${OSAC_UI_DIR:-/work/osac-ui}"
output_dir="${OSAC_UI_E2E_OUTPUT:-/tmp/test-output}"

rc=0
bash "${ui_dir}/apps/e2e-tests/run_scripts/run-ui-e2e.sh" \
  --osac-ui-dir="${ui_dir}" "$@" || rc=$?

if [[ -d "${output_dir}" && -w "${output_dir}" ]]; then
  for result in playwright-report test-results; do
    if [[ -d "${ui_dir}/apps/playwright/${result}" ]]; then
      rm -rf -- "${output_dir:?}/${result}"
      cp -r -- "${ui_dir}/apps/playwright/${result}" "${output_dir}/${result}"
    fi
  done
fi

exit "${rc}"
