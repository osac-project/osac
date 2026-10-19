{{/*
Expand the name of the chart.
*/}}
{{- define "osac-devstack.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "osac-devstack.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "osac-devstack.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "osac-devstack.labels" -}}
helm.sh/chart: {{ include "osac-devstack.chart" . }}
{{ include "osac-devstack.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "osac-devstack.selectorLabels" -}}
app.kubernetes.io/name: {{ include "osac-devstack.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "osac-devstack.serviceAccountName" -}}
{{- if .Values.rbac.create }}
{{- default (include "osac-devstack.fullname" .) .Values.rbac.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.rbac.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "osac-devstack.fulfillmentAuthCredential" -}}
{{- $root := index . 0 -}}
{{- $param := index . 1 -}}
{{- $credential := dict -}}
{{- range ($root.Values.service.auth.controllerCredentials | default list) -}}
  {{- $secret := .secret | default dict -}}
  {{- range ($secret.items | default list) -}}
    {{- if eq (.param | default "") $param -}}
      {{- $_ := set $credential "name" ($secret.name | default "") -}}
      {{- $_ := set $credential "key" (.key | default "") -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- $secretName := required (printf "service.auth.controllerCredentials must include a secret-backed %s parameter" $param) ($credential.name | default "") -}}
{{- $secretKey := required (printf "service.auth.controllerCredentials must include a secret key for the %s parameter" $param) ($credential.key | default "") -}}
{{- toJson (dict "name" $secretName "key" $secretKey) -}}
{{- end -}}

{{- define "osac-devstack.fulfillmentAuthEnv" -}}
{{- $clientID := include "osac-devstack.fulfillmentAuthCredential" (list . "client-id") | fromJson -}}
{{- $issuerURL := .Values.service.auth.issuerUrl | default .Values.ui.auth.oidcIssuerURL -}}
- name: FULFILLMENT_ISSUER_URL
  value: {{ tpl $issuerURL . | quote }}
- name: FULFILLMENT_CLIENT_ID
  valueFrom:
    secretKeyRef:
      name: {{ $clientID.name | quote }}
      key: {{ $clientID.key | quote }}
- name: FULFILLMENT_CLIENT_SECRET_FILE
  value: /var/run/secrets/fulfillment-client-secret/client-secret
{{- end -}}

{{- define "osac-devstack.fulfillmentAuthVolumeMount" -}}
- name: fulfillment-controller-credentials
  mountPath: /var/run/secrets/fulfillment-client-secret
  readOnly: true
{{- end -}}

{{- define "osac-devstack.fulfillmentAuthVolume" -}}
{{- $clientSecret := include "osac-devstack.fulfillmentAuthCredential" (list . "client-secret") | fromJson -}}
- name: fulfillment-controller-credentials
  secret:
    secretName: {{ $clientSecret.name | quote }}
    defaultMode: 0440
    items:
    - key: {{ $clientSecret.key | quote }}
      path: client-secret
{{- end -}}
