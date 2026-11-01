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
Admin password Secret name. The in-cluster instance creates
<instance name>-admin-password; an external AAP (aap.instance.enabled=false)
uses bootstrap.adminPasswordSecret.
*/}}
{{- define "osac-aap.adminPasswordSecretName" -}}
{{- if .Values.aap.instance.enabled -}}
{{- printf "%s-admin-password" (include "osac-aap.instanceName" .) -}}
{{- else -}}
{{- .Values.bootstrap.adminPasswordSecret.name | required "bootstrap.adminPasswordSecret.name is required when aap.instance.enabled is false" -}}
{{- end -}}
{{- end }}

{{/*
Admin password Secret key. Same two cases as osac-aap.adminPasswordSecretName.
*/}}
{{- define "osac-aap.adminPasswordSecretKey" -}}
{{- if .Values.aap.instance.enabled -}}
{{- "password" -}}
{{- else -}}
{{- .Values.bootstrap.adminPasswordSecret.key | required "bootstrap.adminPasswordSecret.key is required when aap.instance.enabled is false" -}}
{{- end -}}
{{- end }}

{{/*
External AAP base URL (the umbrella chart's aap.controllerUrl). Empty is
allowed and fails loudly in the job's wait-for-aap script; a non-empty value
must be an http(s) URL without a path, because callers append
/api/controller/v2 themselves.
*/}}
{{- define "osac-aap.controllerUrl" -}}
{{- if and (not .Values.aap.instance.enabled) .Values.controllerUrl }}
{{- if not (regexMatch "^https?://[^/\\s]+$" .Values.controllerUrl) }}
{{- fail (printf "controllerUrl must be an http(s) AAP base URL without a path, for example https://aap.example.com (got %q)" .Values.controllerUrl) }}
{{- end -}}
{{- end -}}
{{- .Values.controllerUrl -}}
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
Shared wait-for-aap readiness script (bootstrap-job.yaml, create-api-token.yaml).
Wall-clock-bounded (1500s epoch deadline, not iteration count) since each
iteration can take up to ~65s worst case -- keep activeDeadlineSeconds above this.

With aap.instance.enabled=true the script polls the in-cluster services by
hostname over http. With aap.instance.enabled=false it polls the external AAP
at AAP_URL (the umbrella chart's aap.controllerUrl) and fails immediately when
that value is empty.
*/}}
{{- define "osac-aap.waitForAapScript" -}}
{{- if not .Values.aap.instance.enabled }}
if [ -z "${AAP_URL}" ]; then
  echo "ERROR: aap.controllerUrl is empty, but aap.instance.enabled is false. Set aap.controllerUrl to the external AAP base URL, for example https://aap.example.com."
  exit 1
fi
{{ end -}}
echo "Checking AAP controller and gateway readiness..."

# A threshold < 1 would satisfy the streak check immediately, reintroducing the race.
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

# netrc avoids leaking the password via curl -u's process args; cleaned up via
# trap since /tmp is a shared emptyDir mounted by later containers too.
trap 'rm -f /tmp/.netrc' EXIT
{{- if .Values.aap.instance.enabled }}
( umask 077; printf 'machine %s\nlogin admin\npassword %s\n' "${AAP_GATEWAY_HOSTNAME}" "${AAP_PASSWORD}" > /tmp/.netrc )
{{- else }}
# curl matches netrc entries by hostname only, so drop any port from the URL.
AAP_NETRC_HOST="${AAP_URL#*://}"
( umask 077; printf 'machine %s\nlogin admin\npassword %s\n' "${AAP_NETRC_HOST%%:*}" "${AAP_PASSWORD}" > /tmp/.netrc )
{{- end }}

POLL_INTERVAL=5
AUTH_FAILURE_LIMIT=5
consecutive_successes=0
auth_failures=0
READINESS_DEADLINE=$(( $(date +%s) + 1500 ))
i=0
while [ "$(date +%s)" -lt "${READINESS_DEADLINE}" ]; do
  i=$((i + 1))
  curl -sf --connect-timeout 5 --max-time 15 \
    "{{ if .Values.aap.instance.enabled }}http://${AAP_CONTROLLER_GATEWAY_HOSTNAME}/api/v2/ping/{{ else }}${AAP_URL}/api/controller/v2/ping/{{ end }}" \
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
    "{{ if .Values.aap.instance.enabled }}http://${AAP_GATEWAY_HOSTNAME}/api/gateway/v1/ping/{{ else }}${AAP_URL}/api/gateway/v1/ping/{{ end }}" \
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
    "{{ if .Values.aap.instance.enabled }}http://${AAP_GATEWAY_HOSTNAME}/api/gateway/v1/tokens/{{ else }}${AAP_URL}/api/gateway/v1/tokens/{{ end }}" \
    -H "Content-Type: application/json" -d '{"description": "readiness-check", "scope": "read"}')
  auth_code=$(echo "${response}" | tail -1)

  if [ "${auth_code}" = "200" ] || [ "${auth_code}" = "201" ]; then
    auth_failures=0
    auth_body=$(echo "${response}" | sed '$d')
    token_id=$(echo "${auth_body}" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])" 2>/dev/null)

    if [ -z "${token_id}" ]; then
      consecutive_successes=0
      delete_code="skipped"
      # Not logging auth_body: it may contain the live token value.
      echo "WARNING: readiness-check token created but its ID could not be parsed from the response"
    else
      delete_code=$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 5 --max-time 15 \
        -X DELETE --netrc-file /tmp/.netrc \
        "{{ if .Values.aap.instance.enabled }}http://${AAP_GATEWAY_HOSTNAME}/api/gateway/v1/tokens/${token_id}/{{ else }}${AAP_URL}/api/gateway/v1/tokens/${token_id}/{{ end }}")
      case "${delete_code}" in
        2??)
          consecutive_successes=$((consecutive_successes + 1))
          ;;
        *)
          consecutive_successes=0
          echo "WARNING: readiness-check token cleanup failed (delete_code='${delete_code}')"
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
Polls for the AAP admin-password Secret before wait-for-aap mounts it via
secretKeyRef -- otherwise a missing Secret at pod-schedule time blocks the
whole pod in Init:CreateContainerConfigError with no log output.
*/}}
{{- define "osac-aap.waitForAdminSecretScript" -}}
{{- if .Values.aap.instance.enabled -}}
echo "Waiting for AAP admin password Secret to be created by the operator..."
{{- else -}}
echo "Waiting for AAP admin password Secret ${AAP_ADMIN_SECRET_NAME}; no AAP operator creates it in external mode, it must exist before install"
{{- end }}
for i in {1..60}; do
  if oc get secret "${AAP_ADMIN_SECRET_NAME}" -n "${NAMESPACE}" --request-timeout=10s >/dev/null 2>&1; then
    echo "Found admin password Secret."
    exit 0
  fi
  echo "Attempt $i: admin password Secret not found yet, waiting 5 seconds..."
  sleep 5
done
{{- if .Values.aap.instance.enabled }}
echo "ERROR: Timed out waiting for admin password Secret to be created."
{{- else }}
echo "ERROR: Timed out waiting for admin password Secret ${AAP_ADMIN_SECRET_NAME} in namespace ${NAMESPACE}."
{{- end }}
exit 1
{{- end }}

{{/* Shared wait-for-aap init container env/volumeMounts/resources/securityContext. */}}
{{- define "osac-aap.waitForAapContainerSpec" -}}
env:
- name: HOME
  value: /tmp
{{- if .Values.aap.instance.enabled }}
- name: AAP_GATEWAY_HOSTNAME
  value: {{ include "osac-aap.gatewayHostname" . | quote }}
- name: AAP_CONTROLLER_GATEWAY_HOSTNAME
  value: {{ include "osac-aap.controllerHostname" . | quote }}
{{- else }}
- name: AAP_URL
  value: {{ include "osac-aap.controllerUrl" . | quote }}
{{- end }}
- name: AAP_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ include "osac-aap.adminPasswordSecretName" . }}
      key: {{ include "osac-aap.adminPasswordSecretKey" . }}
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

{{/* Shared wait-for-admin-secret init container env/volumeMounts/resources/securityContext. */}}
{{- define "osac-aap.waitForAdminSecretContainerSpec" -}}
env:
- name: HOME
  value: /tmp
- name: NAMESPACE
  valueFrom:
    fieldRef:
      fieldPath: metadata.namespace
- name: AAP_ADMIN_SECRET_NAME
  value: {{ include "osac-aap.adminPasswordSecretName" . | quote }}
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
