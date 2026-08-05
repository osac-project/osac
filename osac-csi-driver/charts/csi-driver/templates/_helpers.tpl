{{/*
Expand the name of the chart.
*/}}
{{- define "csi-driver.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "csi-driver.fullname" -}}
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
Common labels.
*/}}
{{- define "csi-driver.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "csi-driver.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: osac-csi-driver
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "csi-driver.selectorLabels" -}}
app.kubernetes.io/name: {{ include "csi-driver.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Controller service account name.
*/}}
{{- define "csi-driver.controllerServiceAccountName" -}}
{{- if .Values.serviceAccount.controller.name }}
{{- .Values.serviceAccount.controller.name }}
{{- else }}
{{- include "csi-driver.fullname" . }}-controller
{{- end }}
{{- end }}

{{/*
Node service account name.
*/}}
{{- define "csi-driver.nodeServiceAccountName" -}}
{{- if .Values.serviceAccount.node.name }}
{{- .Values.serviceAccount.node.name }}
{{- else }}
{{- include "csi-driver.fullname" . }}-node
{{- end }}
{{- end }}
