#!/usr/bin/env bash
# Retry Go fetch commands only for transient proxy/sumdb transport failures.
# Real module resolution or checksum problems still fail on the first attempt.
set -euo pipefail

attempts="${GO_NETWORK_RETRY_ATTEMPTS:-3}"
base_delay_seconds="${GO_NETWORK_RETRY_BASE_DELAY_SECONDS:-5}"

if ! [[ "${attempts}" =~ ^[1-9][0-9]*$ ]]; then
  echo "go-network-retry: GO_NETWORK_RETRY_ATTEMPTS must be a positive integer" >&2
  exit 2
fi

if ! [[ "${base_delay_seconds}" =~ ^[0-9]+$ ]]; then
  echo "go-network-retry: GO_NETWORK_RETRY_BASE_DELAY_SECONDS must be a non-negative integer" >&2
  exit 2
fi

if (( $# == 0 )); then
  echo "usage: $0 <go arguments...>" >&2
  exit 2
fi

readonly go_proxy_log_re='https://(proxy\.golang\.org|sum\.golang\.org)/'
readonly transient_transport_re='stream error: stream ID [0-9]+; INTERNAL_ERROR|unexpected EOF|TLS handshake timeout|i/o timeout|connection reset by peer|context deadline exceeded|EOF|502 Bad Gateway|503 Service Unavailable|504 Gateway Timeout|proxyconnect tcp|dial tcp|temporary failure in name resolution|no such host|server misbehaving'

run_go_command() {
  local logfile="$1"
  shift
  local rc=0

  set +e
  go "$@" >"${logfile}" 2>&1
  rc=$?
  set -e

  cat "${logfile}"
  return "${rc}"
}

is_transient_go_fetch_failure() {
  local logfile="$1"

  grep -Eq "${go_proxy_log_re}" "${logfile}" && grep -Eq "${transient_transport_re}" "${logfile}"
}

for (( attempt = 1; attempt <= attempts; attempt++ )); do
  logfile="$(mktemp)"

  echo "go-network-retry: attempt ${attempt}/${attempts}: go $*" >&2

  if run_go_command "${logfile}" "$@"; then
    if (( attempt > 1 )); then
      echo "go-network-retry: succeeded on attempt ${attempt}" >&2
    fi
    rm -f "${logfile}"
    exit 0
  else
    rc=$?
  fi

  if ! is_transient_go_fetch_failure "${logfile}"; then
    echo "go-network-retry: non-transient failure, not retrying" >&2
    rm -f "${logfile}"
    exit "${rc}"
  fi

  rm -f "${logfile}"

  if (( attempt == attempts )); then
    echo "go-network-retry: transient proxy or sumdb failure persisted after ${attempts} attempts" >&2
    exit "${rc}"
  fi

  sleep_seconds=$(( base_delay_seconds * attempt ))
  echo "go-network-retry: transient proxy or sumdb failure, retrying in ${sleep_seconds}s" >&2
  sleep "${sleep_seconds}"
done
