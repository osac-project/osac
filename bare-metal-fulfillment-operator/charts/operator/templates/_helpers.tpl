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
Whether the metrics ServiceMonitor and its Prometheus ClusterRoleBinding render.
Standalone serviceMonitor.enabled overrides; otherwise global.observability
drives it. Identical helpers exist in sibling charts because Helm subcharts
cannot call each other's templates.
*/}}
{{- define "bare-metal-fulfillment-operator.serviceMonitorEnabled" -}}
{{- $serviceMonitor := .Values.serviceMonitor | default dict -}}
{{- $globalObservability := (.Values.global | default dict).observability | default dict -}}
{{- if or $serviceMonitor.enabled $globalObservability.enabled -}}true{{- end -}}
{{- end }}

{{/*
Scrape interval: serviceMonitor.interval, else the control-plane collection
interval. Failing here keeps a missing interval from rendering an invalid
ServiceMonitor.
*/}}
{{- define "bare-metal-fulfillment-operator.serviceMonitorInterval" -}}
{{- $serviceMonitor := .Values.serviceMonitor | default dict -}}
{{- $globalObservability := (.Values.global | default dict).observability | default dict -}}
{{- $collection := $globalObservability.collection | default dict -}}
{{- $interval := $serviceMonitor.interval | default "" | toString -}}
{{- if not $interval -}}
{{- $interval = $collection.osacControlPlane | default "" | toString -}}
{{- end -}}
{{- if not $interval -}}
{{- fail "serviceMonitor.interval or global.observability.collection.osacControlPlane must be set" -}}
{{- end -}}
{{- $interval -}}
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
