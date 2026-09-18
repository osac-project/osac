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

{{/*
Customer-managed AAP from the umbrella chart (global.existingAap).
*/}}
{{- define "bare-metal-fulfillment-operator.existingAapUrl" -}}
{{- dig "existingAap" "url" "" (.Values.global | default dict) | trim -}}
{{- end }}

{{- define "bare-metal-fulfillment-operator.aapUrl" -}}
{{- $existing := include "bare-metal-fulfillment-operator.existingAapUrl" . -}}
{{- if $existing -}}
{{- $existing -}}
{{- else -}}
{{- .Values.env.aapUrl | default "" -}}
{{- end -}}
{{- end }}

{{- define "bare-metal-fulfillment-operator.aapTokenSecretName" -}}
{{- $existing := include "bare-metal-fulfillment-operator.existingAapUrl" . -}}
{{- $sec := dig "existingAap" "tokenSecret" (dict) (.Values.global | default dict) -}}
{{- if $existing -}}
{{- required "global.existingAap.tokenSecret.name is required when global.existingAap.url is set" (index $sec "name" | default "") -}}
{{- else -}}
{{- .Values.env.aapTokenSecretName | default "" -}}
{{- end -}}
{{- end }}

{{- define "bare-metal-fulfillment-operator.aapTokenSecretKey" -}}
{{- $existing := include "bare-metal-fulfillment-operator.existingAapUrl" . -}}
{{- $sec := dig "existingAap" "tokenSecret" (dict) (.Values.global | default dict) -}}
{{- if $existing -}}
{{- index $sec "key" | default "token" -}}
{{- else -}}
{{- .Values.env.aapTokenSecretKey | default "token" -}}
{{- end -}}
{{- end }}

{{- define "bare-metal-fulfillment-operator.aapTokenSecretOptional" -}}
{{- if include "bare-metal-fulfillment-operator.existingAapUrl" . -}}
false
{{- else -}}
true
{{- end -}}
{{- end }}
