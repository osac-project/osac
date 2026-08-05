{{/*
bare-metal-fulfillment-operator chart helpers
*/}}
{{- define "bare-metal-fulfillment-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "bare-metal-fulfillment-operator.fullname" -}}
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
{{- define "bare-metal-fulfillment-operator.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{ include "bare-metal-fulfillment-operator.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "bare-metal-fulfillment-operator.selectorLabels" -}}
control-plane: controller-manager
app.kubernetes.io/name: {{ include "bare-metal-fulfillment-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name
*/}}
{{- define "bare-metal-fulfillment-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "bare-metal-fulfillment-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- required "serviceAccount.name must be set when serviceAccount.create=false" .Values.serviceAccount.name }}
{{- end }}
{{- end }}
