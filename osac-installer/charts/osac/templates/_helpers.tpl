{{/*
Expand the name of the chart.
*/}}
{{- define "osac.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "osac.fullname" -}}
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
{{- define "osac.labels" -}}
helm.sh/chart: {{ include "osac.name" . }}
app.kubernetes.io/part-of: osac
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
PostgreSQL identifier prefix. All database identifiers derive from this
single template.
*/}}
{{- define "osac.pgPrefix" -}}
osac
{{- end }}

{{- define "osac.dbNameService" -}}
{{ include "osac.pgPrefix" . }}_service
{{- end }}

{{- define "osac.dbNameMetering" -}}
{{ include "osac.pgPrefix" . }}_metering
{{- end }}

{{/*
Wait-for-fulfillment init container.
Uses .Values.cliImage for the container image.
*/}}
{{- define "osac.waitForFulfillment" -}}
{{- $url := "https://fulfillment-rest-gateway:8000/healthz" -}}
- name: wait-for-fulfillment
  image: {{ .Values.cliImage }}
  command:
    - /bin/bash
    - -euo
    - pipefail
    - -c
    - |
      echo "Waiting for fulfillment REST gateway..."
      for i in $(seq 1 60); do
        echo "Attempt ${i}: checking {{ $url }}"
        if curl -skf --connect-timeout 5 --max-time 30 {{ $url }}; then
          echo ""
          echo "Fulfillment service is ready."
          exit 0
        fi
        sleep 10
      done
      echo "ERROR: Fulfillment service not ready after 600s"
      exit 1
  env:
  - name: HOME
    value: /tmp
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
    capabilities:
      drop: ["ALL"]
{{- end }}

{{/*
Fail helm template when networking values are inconsistent. Schema validates
individual fields; this enforces cross-field invariants that JSON Schema
cannot express (duplicated Netris config, inverted port ranges).
*/}}
{{- define "osac.validateValues" -}}
{{- $cf := .Values.aap.instanceGroups.clusterFulfillment | default dict -}}
{{- $nf := .Values.aap.instanceGroups.networkFulfillment | default dict -}}
{{- $cfCfg := $cf.config | default dict -}}
{{- $nfCfg := $nf.config | default dict -}}
{{- $cfSec := $cf.secret | default dict -}}
{{- $nfSec := $nf.secret | default dict -}}
{{- $netrisConfigFields := list
  "NETRIS_CONTROLLER_URL"
  "NETRIS_USERNAME"
  "NETRIS_SITE_ID"
  "NETRIS_TENANT_ID"
  "NETRIS_TENANT_NAME"
-}}
{{- range $netrisConfigFields }}
  {{- $cfVal := index $cfCfg . | default "" | toString -}}
  {{- $nfVal := index $nfCfg . | default "" | toString -}}
  {{- if ne $cfVal $nfVal }}
    {{- fail (printf "aap.instanceGroups.clusterFulfillment.config.%s and networkFulfillment.config.%s must match (cluster=%q network=%q)" . . $cfVal $nfVal) }}
  {{- end }}
{{- end }}
{{- $cfPwd := $cfSec.NETRIS_PASSWORD | default "" | toString -}}
{{- $nfPwd := $nfSec.NETRIS_PASSWORD | default "" | toString -}}
{{- if ne $cfPwd $nfPwd }}
  {{- fail "aap.instanceGroups.clusterFulfillment.secret.NETRIS_PASSWORD and networkFulfillment.secret.NETRIS_PASSWORD must match" }}
{{- end }}
{{- if .Values.networkClass.enabled }}
  {{- range .Values.networkClass.defaults.egressRules | default list }}
    {{- if and .portFrom .portTo (gt (int .portFrom) (int .portTo)) }}
      {{- fail (printf "networkClass.defaults.egressRules: portFrom (%v) must be <= portTo (%v)" .portFrom .portTo) }}
    {{- end }}
  {{- end }}
{{- end }}
{{- end -}}
