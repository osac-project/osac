#!/usr/bin/env bash

# Pre-install gate for the observability stack. The hook Job runs this before
# Helm creates any chart resource, so a failure aborts the install with the
# missing prerequisite named and nothing deployed.
#
# The object-storage Secret is read only when its namespace already exists.
# On a first install this chart creates osac-observability after pre-install
# hooks run, so that run validates everything else and the pre-upgrade run
# repeats the Secret check before any upgrade changes the stack.

set -euo pipefail

secret_name="${OBS_SECRET_NAME:-}"
namespace="${OBS_SECRET_NAMESPACE:-osac-observability}"
endpoint="${OBS_ENDPOINT:-}"
enabled="${OBSERVABILITY_ENABLED:-true}"

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

if [ "${enabled}" != "true" ]; then
  echo "observability is disabled; skipping prerequisites"
  exit 0
fi

if [ -z "${secret_name}" ]; then
  fail "global.observability.objectStorage.secretName must name an existing Secret when observability is enabled"
fi

if [ -z "${endpoint}" ]; then
  fail "global.observability.objectStorage.endpoint must be set when observability is enabled"
fi

if namespace_output=$(oc get namespace "${namespace}" -o name 2>&1); then
  if ! oc get secret "${secret_name}" -n "${namespace}" >/dev/null 2>&1; then
    fail "observability object-storage Secret \"${secret_name}\" was not found in namespace \"${namespace}\""
  fi
elif printf '%s\n' "${namespace_output}" | grep -q "NotFound"; then
  echo "namespace \"${namespace}\" is created by this release; the object-storage Secret check runs on pre-upgrade"
else
  # Any other error is unexpected; fail closed rather than skipping the check.
  printf '%s\n' "${namespace_output}" >&2
  fail "could not read namespace \"${namespace}\""
fi

host="${endpoint#*://}"
host="${host%%/*}"
host="${host%%:*}"
if [ -z "${host}" ]; then
  fail "global.observability.objectStorage.endpoint must include a host when observability is enabled"
fi
if ! getent hosts "${host}" >/dev/null 2>&1; then
  fail "observability object-storage endpoint host \"${host}\" does not resolve"
fi

if ! oc get --raw /apis/cert-manager.io/v1 >/dev/null 2>&1; then
  fail "the cert-manager.io/v1 API is not served; install cert-manager before enabling observability"
fi

echo "observability prerequisites validated"
