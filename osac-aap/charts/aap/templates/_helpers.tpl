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
