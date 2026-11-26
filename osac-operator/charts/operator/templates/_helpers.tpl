{{/*
Expand the name of the chart.
*/}}
{{- define "osac-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "osac-operator.fullname" -}}
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
{{- define "osac-operator.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{ include "osac-operator.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "osac-operator.selectorLabels" -}}
control-plane: controller-manager
app.kubernetes.io/name: {{ include "osac-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Console proxy labels
*/}}
{{- define "osac-operator.consoleProxy.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{ include "osac-operator.consoleProxy.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Console proxy selector labels
*/}}
{{- define "osac-operator.consoleProxy.selectorLabels" -}}
app: {{ printf "%s-console-proxy" (include "osac-operator.fullname" .) | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ include "osac-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: console-proxy
{{- end }}

{{/*
Service account name
*/}}
{{- define "osac-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.name }}
{{- .Values.serviceAccount.name }}
{{- else }}
{{- include "osac-operator.fullname" . }}
{{- end }}
{{- end }}

{{/*
Resolve a service-tier controller flag from global.services or a local override.
Args: list of [context, controllerKey, serviceKey]
  - controllerKey: key in .Values.controllers (e.g. "clusterOrder")
  - serviceKey: key in .Values.global.services (e.g. "caas")
If controllers.<controllerKey> is explicitly set, it wins.
Otherwise, falls back to global.services.<serviceKey>.enabled (default true).
*/}}
{{- define "osac-operator.controllerEnabled" -}}
{{- $ctx := index . 0 -}}
{{- $ctrlKey := index . 1 -}}
{{- $svcKey := index . 2 -}}
{{- $ctrlVal := index $ctx.Values.controllers $ctrlKey -}}
{{- if $ctrlVal | kindIs "invalid" | not -}}
{{- $ctrlVal -}}
{{- else -}}
{{- $enabled := true -}}
{{- if $ctx.Values.global -}}
{{- if $ctx.Values.global.services -}}
{{- $svc := index $ctx.Values.global.services $svcKey -}}
{{- if $svc -}}
{{- if hasKey $svc "enabled" -}}
{{- $enabled = $svc.enabled -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- $enabled -}}
{{- end -}}
{{- end }}

{{/*
The ServiceMonitor renders when the standalone override is set or the umbrella
chart enabled observability. Keep in sync with the identical helper in the
other OSAC component charts; subcharts cannot call sibling templates.
*/}}
{{- define "osac-operator.serviceMonitorEnabled" -}}
{{- $serviceMonitor := .Values.serviceMonitor | default dict -}}
{{- $globalObservability := (.Values.global | default dict).observability | default dict -}}
{{- if or $serviceMonitor.enabled $globalObservability.enabled -}}true{{- end -}}
{{- end }}

{{/*
Resolve the scrape interval. Fails rather than hardcoding a default.
*/}}
{{- define "osac-operator.serviceMonitorInterval" -}}
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
