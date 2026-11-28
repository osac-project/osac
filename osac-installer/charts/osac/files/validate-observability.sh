#!/usr/bin/env bash

# Pre-install gate for the observability stack. The hook Job runs this before
# Helm creates any chart resource, so a failure aborts the install with the
# missing prerequisite named and nothing deployed.
#
# The gate owns the osac-observability namespace: the chart must not render a
# Namespace object, because Helm would delete it, the admin's Secret and the
# Receive and Compactor PVCs on the upgrade that sets enabled: false. This
# script creates the namespace when it does not exist, labels it for platform
# monitoring on every run, and then requires the object-storage Secret. The
# Secret check runs on the first install too: skipping it there let an enabled
# install come up with no object storage at all.

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
  :
elif printf '%s\n' "${namespace_output}" | grep -q "NotFound"; then
  if ! create_output=$(oc create namespace "${namespace}" 2>&1); then
    # A concurrent create can win the race; only a real failure aborts.
    if ! printf '%s\n' "${create_output}" | grep -q "AlreadyExists"; then
      printf '%s\n' "${create_output}" >&2
      fail "could not create namespace \"${namespace}\""
    fi
  else
    echo "created namespace \"${namespace}\""
  fi
else
  # Any other error is unexpected; fail closed rather than skipping the check.
  printf '%s\n' "${namespace_output}" >&2
  fail "could not read namespace \"${namespace}\""
fi

if ! label_output=$(oc label namespace "${namespace}" openshift.io/cluster-monitoring=true --overwrite 2>&1); then
  printf '%s\n' "${label_output}" >&2
  fail "could not label namespace \"${namespace}\" for platform monitoring"
fi

if ! oc get secret "${secret_name}" -n "${namespace}" >/dev/null 2>&1; then
  fail "observability object-storage Secret \"${secret_name}\" was not found in namespace \"${namespace}\""
fi

# aws_sdk_auth reads the standard AWS environment, which the pods take from
# this Secret. A Secret that exists but omits a key passes the check above and
# then fails every bucket call, so require both keys here. Only the key name is
# reported; the value is never printed.
for key in AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY; do
  if ! key_output=$(oc get secret "${secret_name}" -n "${namespace}" \
      -o "jsonpath={.data.${key}}" 2>&1); then
    printf '%s\n' "${key_output}" >&2
    fail "could not read key ${key} from observability object-storage Secret \"${secret_name}\" in namespace \"${namespace}\""
  fi
  if [ -z "${key_output}" ]; then
    fail "observability object-storage Secret \"${secret_name}\" has no ${key} key in namespace \"${namespace}\""
  fi
done

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
