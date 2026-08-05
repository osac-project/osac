{{/*
bare-metal-fulfillment-operator-crds chart helpers
*/}}
{{- define "bare-metal-fulfillment-operator-crds.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "bare-metal-fulfillment-operator-crds.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}
