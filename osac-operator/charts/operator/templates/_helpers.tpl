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
Customer-managed AAP URL from the umbrella chart (global.existingAap).
Empty when OSAC should install and use osac-aap.
*/}}
{{- define "osac-operator.existingAapUrl" -}}
{{- dig "existingAap" "url" "" (.Values.global | default dict) | trim -}}
{{- end }}

{{- define "osac-operator.aapUrl" -}}
{{- $existing := include "osac-operator.existingAapUrl" . -}}
{{- if $existing -}}
{{- $existing -}}
{{- else -}}
{{- .Values.aap.url | default "http://osac-aap/api/controller" -}}
{{- end -}}
{{- end }}

{{- define "osac-operator.aapTokenSecretName" -}}
{{- $existing := include "osac-operator.existingAapUrl" . -}}
{{- $sec := dig "existingAap" "tokenSecret" (dict) (.Values.global | default dict) -}}
{{- if $existing -}}
{{- required "global.existingAap.tokenSecret.name is required when global.existingAap.url is set" (index $sec "name" | default "") -}}
{{- else -}}
{{- dig "tokenSecret" "name" "" (.Values.aap | default dict) -}}
{{- end -}}
{{- end }}

{{- define "osac-operator.aapTokenSecretKey" -}}
{{- $existing := include "osac-operator.existingAapUrl" . -}}
{{- $sec := dig "existingAap" "tokenSecret" (dict) (.Values.global | default dict) -}}
{{- if $existing -}}
{{- index $sec "key" | default "token" -}}
{{- else -}}
{{- dig "tokenSecret" "key" "token" (.Values.aap | default dict) -}}
{{- end -}}
{{- end }}

{{- define "osac-operator.aapTokenSecretOptional" -}}
{{- if include "osac-operator.existingAapUrl" . -}}
false
{{- else -}}
true
{{- end -}}
{{- end }}
