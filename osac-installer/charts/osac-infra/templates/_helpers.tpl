{{- define "osac-infra.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "osac-infra.fullname" -}}
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

{{- define "osac-infra.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ include "osac-infra.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "osac-infra.hookLabels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ include "osac-infra.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Pod security context shared by the credential-generation Jobs. The explicit
runAsNonRoot requirement applies to both the default OpenShift path and the
numeric security context used by Kind profiles.
*/}}
{{- define "osac-infra.cliJobPodSecurityContext" -}}
runAsNonRoot: true
{{- with .Values.cliJobPodSecurityContext }}
{{- with .runAsUser }}
runAsUser: {{ . }}
{{- end }}
{{- with .runAsGroup }}
runAsGroup: {{ . }}
{{- end }}
{{- with .fsGroup }}
fsGroup: {{ . }}
{{- end }}
{{- end }}
{{- end }}
