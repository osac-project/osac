#!/usr/bin/env bash
set -eu

if [[ "${OSAC_CLI_TEST_FAIL:-0}" == 1 ]]; then
  echo 'simulated CLI failure' >&2
  exit 1
fi
if [[ "$#" != 8 || "$1" != '--tenant' || "$2" != 'test-tenant' || "$3" != 'get' || "$4" != 'computeinstance' || "$5" != '--filter' || "$6" != 'this.metadata.name == "test-vm"' || "$7" != '--output' || "$8" != 'json' ]]; then
  echo 'unexpected CLI arguments' >&2
  exit 1
fi
printf '%s\n' "${OSAC_CLI_TEST_OUTPUT:-[]}"
