{{/*
Expand the name of the chart.
*/}}
{{- define "osac-workload.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "osac-workload.fullname" -}}
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
{{- define "osac-workload.labels" -}}
helm.sh/chart: {{ include "osac-workload.name" . }}
app.kubernetes.io/part-of: osac
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Derive namespace-prefixed database name (replace hyphens with underscores).
*/}}
{{- define "osac-workload.dbPrefix" -}}
{{- .Release.Namespace | replace "-" "_" }}
{{- end }}

{{/*
Wait-for-fulfillment init container.
*/}}
{{- define "osac-workload.waitForFulfillment" -}}
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
