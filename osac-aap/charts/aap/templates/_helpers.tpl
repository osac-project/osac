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
*/}}
{{- define "osac-aap.waitForAapScript" -}}
echo "Controller gateway: ${AAP_CONTROLLER_GATEWAY_HOSTNAME}"
echo "Gateway: ${AAP_GATEWAY_HOSTNAME}"
echo "Waiting for AAP to be ready (${AAP_READINESS_CONSECUTIVE_SUCCESSES} consecutive successful checks required)..."
POLL_INTERVAL=5
consecutive=0
for i in {1..720}; do
  curl -sf --connect-timeout 5 --max-time 15 \
    http://${AAP_CONTROLLER_GATEWAY_HOSTNAME}/api/v2/ping/ \
    | grep -q 'version';
  controller_ping=$?

  if [ $controller_ping -ne 0 ]; then
    consecutive=0
    echo "Attempt $i: controller_ping=fail auth=skipped delete=skipped streak: ${consecutive}/${AAP_READINESS_CONSECUTIVE_SUCCESSES}"
    echo "AAP is not ready, waiting ${POLL_INTERVAL} seconds..."
    sleep ${POLL_INTERVAL}
    continue
  fi

  response=$(curl -s --connect-timeout 5 --max-time 15 -w '\n%{http_code}' -X POST \
    http://${AAP_GATEWAY_HOSTNAME}/api/gateway/v1/tokens/ -u "admin:${AAP_PASSWORD}" \
    -H "Content-Type: application/json" -d '{"description": "readiness-check"}')
  auth_code=$(echo "$response" | tail -1)
  auth_body=$(echo "$response" | sed '$d')

  if [ "$auth_code" = "200" ] || [ "$auth_code" = "201" ]; then
    token_id=$(echo "$auth_body" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
    delete_code=$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 5 --max-time 15 \
      -X DELETE -u "admin:${AAP_PASSWORD}" \
      http://${AAP_GATEWAY_HOSTNAME}/api/gateway/v1/tokens/${token_id}/)
    consecutive=$((consecutive + 1))
    echo "Attempt $i: controller_ping=ok auth=${auth_code} delete=${delete_code} streak: ${consecutive}/${AAP_READINESS_CONSECUTIVE_SUCCESSES}"
  else
    consecutive=0
    echo "Attempt $i: controller_ping=ok auth=${auth_code} delete=skipped streak: ${consecutive}/${AAP_READINESS_CONSECUTIVE_SUCCESSES}"
  fi

  if [ "$consecutive" -ge "${AAP_READINESS_CONSECUTIVE_SUCCESSES}" ]; then
    echo "AAP is ready!"
    exit 0
  fi

  echo "AAP is not ready, waiting ${POLL_INTERVAL} seconds..."
  sleep ${POLL_INTERVAL}
done
echo "Timeout waiting for AAP."
exit 1
{{- end }}
