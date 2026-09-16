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
AAP connection. global.externalAap from the umbrella chart wins so BMaaS
does not need a duplicated bmf.env.aapUrl / token secret.
*/}}
{{- define "bare-metal-fulfillment-operator.aapUrl" -}}
{{- $ext := dig "externalAap" (dict) (.Values.global | default dict) -}}
{{- if and (index $ext "enabled") (index $ext "url") -}}
{{- index $ext "url" -}}
{{- else -}}
{{- .Values.env.aapUrl | default "" -}}
{{- end -}}
{{- end }}

{{- define "bare-metal-fulfillment-operator.aapTokenSecretName" -}}
{{- $ext := dig "externalAap" (dict) (.Values.global | default dict) -}}
{{- $extSecret := dig "tokenSecret" (dict) $ext -}}
{{- if and (index $ext "enabled") (index $extSecret "name") -}}
{{- index $extSecret "name" -}}
{{- else -}}
{{- .Values.env.aapTokenSecretName | default "" -}}
{{- end -}}
{{- end }}

{{- define "bare-metal-fulfillment-operator.aapTokenSecretKey" -}}
{{- $ext := dig "externalAap" (dict) (.Values.global | default dict) -}}
{{- $extSecret := dig "tokenSecret" (dict) $ext -}}
{{- if and (index $ext "enabled") (index $extSecret "name") -}}
{{- index $extSecret "key" | default "token" -}}
{{- else -}}
{{- .Values.env.aapTokenSecretKey | default "token" -}}
{{- end -}}
{{- end }}

{{/*
Required when pointing at a pre-existing AAP so a missing token Secret
fails the pod at start instead of injecting an empty OSAC_AAP_TOKEN.
*/}}
{{- define "bare-metal-fulfillment-operator.aapTokenSecretOptional" -}}
{{- $ext := dig "externalAap" (dict) (.Values.global | default dict) -}}
{{- $extSecret := dig "tokenSecret" (dict) $ext -}}
{{- if and (index $ext "enabled") (index $extSecret "name") -}}
false
{{- else -}}
true
{{- end -}}
{{- end }}
