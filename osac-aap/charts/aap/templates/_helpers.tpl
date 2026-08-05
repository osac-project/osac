{{/*
Expand the name of the chart.
*/}}
{{- define "osac-aap.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "osac-aap.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "osac-aap.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "osac-aap.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
AAP instance name
*/}}
{{- define "osac-aap.instanceName" -}}
{{- .Values.aap.instance.name }}
{{- end }}

{{/*
AAP gateway hostname (service name)
*/}}
{{- define "osac-aap.gatewayHostname" -}}
{{- include "osac-aap.instanceName" . }}
{{- end }}

{{/*
AAP EDA hostname
*/}}
{{- define "osac-aap.edaHostname" -}}
{{- printf "%s-eda-api" (include "osac-aap.instanceName" .) }}
{{- end }}

{{/*
AAP controller hostname
*/}}
{{- define "osac-aap.controllerHostname" -}}
{{- printf "%s-controller-service" (include "osac-aap.instanceName" .) }}
{{- end }}

{{/*
Shared wait-for-aap readiness check script, consumed via `include` from both
bootstrap-job.yaml and hooks/create-api-token.yaml. Takes no Go-template
parameters — every input comes from the calling container's own env block:
AAP_GATEWAY_HOSTNAME, AAP_CONTROLLER_GATEWAY_HOSTNAME, AAP_PASSWORD, and
AAP_READINESS_CONSECUTIVE_SUCCESSES.

Wall-clock deadline / activeDeadlineSeconds relationship: this script bounds
itself to READINESS_DEADLINE_SECONDS=1500 (25 min) of WALL-CLOCK time via an
absolute epoch deadline checked at the top of each loop iteration -- NOT a
fixed iteration count. A fixed count (e.g. "300 iterations x 5s") is not a
reliable time bound here: each iteration can run up to 4 sequential curls
(controller ping, gateway ping, token POST, token DELETE) at --max-time 15
each, so a single iteration can legitimately take anywhere from ~5s (fast
failures) to ~65s (a degraded-but-technically-up AAP that's slow on every
call). An iteration-count loop under that variance could run for hours;
the epoch-deadline check bounds actual wall-clock time to ~1500s plus at
most one in-flight iteration's overrun (~65s worst case), regardless of how
each iteration resolves. The preceding wait-for-admin-secret init container
adds its own wall-clock-bounded budget (see its docstring below), so the
real per-pod-attempt worst case is the sum of both. This MUST stay well
under the activeDeadlineSeconds set on the calling Job (bootstrap-job.yaml /
hooks/create-api-token.yaml) -- activeDeadlineSeconds is a Job-level
CUMULATIVE deadline across every backoffLimit-driven pod retry, not a
per-pod budget. Sizing activeDeadlineSeconds to comfortably exceed one
combined worst-case attempt ensures a stuck attempt hits this script's own
"Timeout waiting for AAP" branch (clean exit 1, diagnosable logs,
backoffLimit gets to retry) instead of being silently SIGKILLed by
DeadlineExceeded. If you change either deadline, re-check
activeDeadlineSeconds against the new combined total.

Note: osac-installer's `make install-osac` target runs `helm upgrade --install
... --timeout 40m --wait`, which is a SEPARATE, tighter timeout on the overall
Helm operation -- it aborts the whole install/upgrade at 40 minutes regardless
of activeDeadlineSeconds. activeDeadlineSeconds is this Job's own internal
safety net for installs that don't go through that specific Makefile target
(e.g. a longer/no --timeout), not a guarantee of retry budget in CI.
*/}}
{{- define "osac-aap.waitForAapScript" -}}
echo "Controller gateway: ${AAP_CONTROLLER_GATEWAY_HOSTNAME}"
echo "Gateway: ${AAP_GATEWAY_HOSTNAME}"

# Guard against a misconfigured threshold: with a value less than 1, the
# ge-comparison below would be satisfied immediately -- including right after
# an explicit auth failure resets the streak to 0 -- silently reproducing the
# readiness race this script exists to prevent.
case "${AAP_READINESS_CONSECUTIVE_SUCCESSES}" in
  ''|*[!0-9]*)
    echo "ERROR: AAP_READINESS_CONSECUTIVE_SUCCESSES must be a positive integer, got '${AAP_READINESS_CONSECUTIVE_SUCCESSES}'"
    exit 1
    ;;
esac
if [ "${AAP_READINESS_CONSECUTIVE_SUCCESSES}" -lt 1 ]; then
  echo "ERROR: AAP_READINESS_CONSECUTIVE_SUCCESSES must be >= 1, got '${AAP_READINESS_CONSECUTIVE_SUCCESSES}'"
  exit 1
fi

echo "Waiting for AAP to be ready (${AAP_READINESS_CONSECUTIVE_SUCCESSES} consecutive successful checks required)..."

# Credentials go into a netrc file rather than curl's -u flag: -u puts the
# password on the command line, readable by any process in this container's
# PID namespace (/proc/<pid>/cmdline, `ps`). These auth calls stay on the
# internal plaintext Service hostname (not a Route/TLS-terminated hostname) --
# accepted risk: this chart has no Route resource or hostname helper for the
# AAP Gateway (the AAP operator manages that Route, outside this chart), and
# resolving it at runtime would reintroduce the same operator-readiness race
# this script exists to fix. Traffic stays intra-cluster/intra-namespace.
# Created with umask 077 so the file is never world/group-readable (no window
# between creation and a separate chmod), and removed on every exit path via
# the trap below -- it must not outlive this container, since /tmp is a
# shared emptyDir also mounted by later containers in the same pod.
trap 'rm -f /tmp/.netrc' EXIT
( umask 077; printf 'machine %s\nlogin admin\npassword %s\n' "${AAP_GATEWAY_HOSTNAME}" "${AAP_PASSWORD}" > /tmp/.netrc )

POLL_INTERVAL=5
AUTH_FAILURE_LIMIT=5
consecutive_successes=0
auth_failures=0
READINESS_DEADLINE=$(( $(date +%s) + 1500 ))
i=0
while [ "$(date +%s)" -lt "${READINESS_DEADLINE}" ]; do
  i=$((i + 1))
  curl -sf --connect-timeout 5 --max-time 15 \
    "http://${AAP_CONTROLLER_GATEWAY_HOSTNAME}/api/v2/ping/" \
    | grep -q 'version'
  controller_ping="${?}"

  if [ "${controller_ping}" -ne 0 ]; then
    consecutive_successes=0
    echo "Attempt $i: controller_ping=fail gateway_ping=skipped auth=skipped delete=skipped streak: ${consecutive_successes}/${AAP_READINESS_CONSECUTIVE_SUCCESSES}"
    echo "AAP is not ready, waiting ${POLL_INTERVAL} seconds..."
    sleep "${POLL_INTERVAL}"
    continue
  fi

  curl -sf --connect-timeout 5 --max-time 15 \
    "http://${AAP_GATEWAY_HOSTNAME}/api/gateway/v1/ping/" \
    | grep -q 'good'
  gateway_ping="${?}"

  if [ "${gateway_ping}" -ne 0 ]; then
    consecutive_successes=0
    echo "Attempt $i: controller_ping=ok gateway_ping=fail auth=skipped delete=skipped streak: ${consecutive_successes}/${AAP_READINESS_CONSECUTIVE_SUCCESSES}"
    echo "AAP is not ready, waiting ${POLL_INTERVAL} seconds..."
    sleep "${POLL_INTERVAL}"
    continue
  fi

  response=$(curl -s --connect-timeout 5 --max-time 15 -w '\n%{http_code}' -X POST \
    --netrc-file /tmp/.netrc \
    "http://${AAP_GATEWAY_HOSTNAME}/api/gateway/v1/tokens/" \
    -H "Content-Type: application/json" -d '{"description": "readiness-check", "scope": "read"}')
  auth_code=$(echo "${response}" | tail -1)

  if [ "${auth_code}" = "200" ] || [ "${auth_code}" = "201" ]; then
    auth_failures=0
    auth_body=$(echo "${response}" | sed '$d')
    token_id=$(echo "${auth_body}" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])" 2>/dev/null)

    if [ -z "${token_id}" ]; then
      consecutive_successes=0
      delete_code="skipped"
      # Deliberately not logging auth_body here: on a 200/201 it's the token
      # creation response, which can include the live token value itself
      # (per AAP Gateway's token resource schema) -- logging it on a parse
      # failure could leak a real credential into pod logs.
      echo "WARNING: readiness-check token created but its ID could not be parsed from the response"
    else
      delete_code=$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 5 --max-time 15 \
        -X DELETE --netrc-file /tmp/.netrc \
        "http://${AAP_GATEWAY_HOSTNAME}/api/gateway/v1/tokens/${token_id}/")
      case "${delete_code}" in
        2??)
          consecutive_successes=$((consecutive_successes + 1))
          ;;
        *)
          consecutive_successes=0
          echo "WARNING: readiness-check token cleanup failed (token_id='${token_id}' delete_code='${delete_code}')"
          ;;
      esac
    fi
    echo "Attempt $i: controller_ping=ok gateway_ping=ok auth=${auth_code} delete=${delete_code} streak: ${consecutive_successes}/${AAP_READINESS_CONSECUTIVE_SUCCESSES}"
  else
    consecutive_successes=0
    if [ "${auth_code}" = "401" ] || [ "${auth_code}" = "403" ]; then
      auth_failures=$((auth_failures + 1))
      echo "WARNING: AAP authentication rejected (HTTP ${auth_code}), consecutive auth failures: ${auth_failures}/${AUTH_FAILURE_LIMIT}"
      if [ "${auth_failures}" -ge "${AUTH_FAILURE_LIMIT}" ]; then
        echo "ERROR: AAP authentication is failing repeatedly -- check AAP_PASSWORD/admin credentials"
        exit 1
      fi
    else
      auth_failures=0
    fi
    echo "Attempt $i: controller_ping=ok gateway_ping=ok auth=${auth_code} delete=skipped streak: ${consecutive_successes}/${AAP_READINESS_CONSECUTIVE_SUCCESSES}"
  fi

  if [ "${consecutive_successes}" -ge "${AAP_READINESS_CONSECUTIVE_SUCCESSES}" ]; then
    echo "AAP is ready!"
    exit 0
  fi

  echo "AAP is not ready, waiting ${POLL_INTERVAL} seconds..."
  sleep "${POLL_INTERVAL}"
done
echo "Timeout waiting for AAP."
exit 1
{{- end }}

{{/*
Shared wait-for-admin-secret script, consumed via `include` from both
bootstrap-job.yaml and hooks/create-api-token.yaml. Polls for the AAP admin
password Secret's existence before the wait-for-aap init container tries to
mount it via secretKeyRef -- without this, a Secret that doesn't exist yet
when the pod is scheduled (operator hasn't reconciled the
AnsibleAutomationPlatform CR yet) blocks the whole pod in
Init:CreateContainerConfigError with no log output at all. Takes no
Go-template parameters -- inputs come from the calling container's own env
block: NAMESPACE, AAP_ADMIN_SECRET_NAME.

Worst-case wall-clock time: 60 iterations, each bounded to at most
`--request-timeout=10s` (a slow/unresponsive API server) plus a 5s sleep --
~900s (15 min) worst case, not the ~300s a naive "60 x 5s sleep" reading
would suggest. See osac-aap.waitForAapScript's docstring above for why an
explicit per-call timeout matters here.
*/}}
{{- define "osac-aap.waitForAdminSecretScript" -}}
echo "Waiting for AAP admin password Secret '${AAP_ADMIN_SECRET_NAME}' to be created by the operator..."
for i in {1..60}; do
  if oc get secret "${AAP_ADMIN_SECRET_NAME}" -n "${NAMESPACE}" --request-timeout=10s >/dev/null 2>&1; then
    echo "Found Secret ${AAP_ADMIN_SECRET_NAME} in namespace ${NAMESPACE}."
    exit 0
  fi
  echo "Attempt $i: Secret ${AAP_ADMIN_SECRET_NAME} not found yet in namespace ${NAMESPACE}, waiting 5 seconds..."
  sleep 5
done
echo "ERROR: Timed out waiting for Secret ${AAP_ADMIN_SECRET_NAME} in namespace ${NAMESPACE} to be created."
exit 1
{{- end }}

{{/*
Shared wait-for-aap init container env/volumeMounts/resources/securityContext,
consumed via `include` from both bootstrap-job.yaml and
hooks/create-api-token.yaml. The `command:`/script content lives in
osac-aap.waitForAapScript above; this template covers everything else in the
container spec. Takes `.` (the top-level chart context) as its argument.
*/}}
{{- define "osac-aap.waitForAapContainerSpec" -}}
env:
- name: HOME
  value: /tmp
- name: AAP_GATEWAY_HOSTNAME
  value: {{ include "osac-aap.gatewayHostname" . | quote }}
- name: AAP_CONTROLLER_GATEWAY_HOSTNAME
  value: {{ include "osac-aap.controllerHostname" . | quote }}
- name: AAP_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ include "osac-aap.instanceName" . }}-admin-password
      key: password
- name: AAP_READINESS_CONSECUTIVE_SUCCESSES
  value: {{ .Values.bootstrap.readinessConsecutiveSuccesses | quote }}
volumeMounts:
- name: tmp
  mountPath: /tmp
resources:
  requests:
    cpu: 50m
    memory: 128Mi
  limits:
    cpu: 200m
    memory: 256Mi
securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  runAsNonRoot: true
  capabilities:
    drop: ["ALL"]
{{- end }}

{{/*
Shared wait-for-admin-secret init container env/volumeMounts/resources/
securityContext, consumed via `include` from both bootstrap-job.yaml and
hooks/create-api-token.yaml. The `command:`/script content lives in
osac-aap.waitForAdminSecretScript above; this template covers everything else
in the container spec, mirroring osac-aap.waitForAapContainerSpec above for
its sibling container. Takes `.` (the top-level chart context) as its
argument.
*/}}
{{- define "osac-aap.waitForAdminSecretContainerSpec" -}}
env:
- name: HOME
  value: /tmp
- name: NAMESPACE
  valueFrom:
    fieldRef:
      fieldPath: metadata.namespace
- name: AAP_ADMIN_SECRET_NAME
  value: {{ printf "%s-admin-password" (include "osac-aap.instanceName" .) | quote }}
volumeMounts:
- name: tmp
  mountPath: /tmp
resources:
  requests:
    cpu: 50m
    memory: 128Mi
  limits:
    cpu: 200m
    memory: 256Mi
securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  runAsNonRoot: true
  capabilities:
    drop: ["ALL"]
{{- end }}
