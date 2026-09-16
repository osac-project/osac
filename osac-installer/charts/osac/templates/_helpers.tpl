{{/*
Expand the name of the chart.
*/}}
{{- define "osac.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "osac.fullname" -}}
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
{{- define "osac.labels" -}}
helm.sh/chart: {{ include "osac.name" . }}
app.kubernetes.io/part-of: osac
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
PostgreSQL identifier prefix. All database identifiers derive from this
single template.
*/}}
{{- define "osac.pgPrefix" -}}
osac
{{- end }}

{{- define "osac.dbNameService" -}}
{{ include "osac.pgPrefix" . }}_service
{{- end }}

{{- define "osac.dbNameMetering" -}}
{{ include "osac.pgPrefix" . }}_metering
{{- end }}

{{/*
Stable identity for the OSAC/fulfillment-service deployment. The configured
value is required on every render; the retained ConfigMap detects accidental
identity changes across upgrades and reinstall attempts.
*/}}
{{- define "osac.osacDeploymentIdentityName" -}}
{{- $release := .Release.Name | trunc 49 | trimSuffix "-" -}}
{{- $hash := sha256sum .Release.Name | trunc 8 -}}
{{- printf "%s-osac-%s" $release $hash -}}
{{- end -}}

{{- define "osac.osacDeploymentId" -}}
{{- $configured := required "global.osacDeploymentId is required" .Values.global.osacDeploymentId -}}
{{- $identity := lookup "v1" "ConfigMap" .Release.Namespace (include "osac.osacDeploymentIdentityName" .) -}}
{{- if $identity -}}
{{- $stored := required "OSAC deployment identity ConfigMap is missing osacDeploymentId" (index $identity.data "osacDeploymentId") -}}
{{- if ne $configured $stored -}}
{{- fail (printf "global.osacDeploymentId cannot change from %q to %q" $stored $configured) -}}
{{- end -}}
{{- end -}}
{{- $configured -}}
{{- end -}}

{{/*
External (brownfield) AAP. User overlay may set aap.externalAap and/or
global.externalAap; Helm only propagates global.* into subcharts, so the
operator and BMF auto-wire from global.externalAap. Umbrella templates
honor either source.
*/}}
{{- define "osac.externalAap.cfg" -}}
{{- if dig "externalAap" "enabled" false .Values.aap -}}
{{- .Values.aap.externalAap | toYaml -}}
{{- else -}}
{{- dig "externalAap" (dict "enabled" false "url" "" "tokenSecret" (dict "name" "" "key" "token")) .Values.global | toYaml -}}
{{- end -}}
{{- end -}}

{{/*
Wait-for-fulfillment init container.
Uses .Values.cliImage for the container image.
*/}}
{{- define "osac.waitForFulfillment" -}}
{{- $url := "https://fulfillment-rest-gateway:8000/healthz" -}}
- name: wait-for-fulfillment
  image: {{ .Values.cliImage }}
  command:
    - /bin/bash
    - -euo
    - pipefail
    - -c
    - |
      echo "Waiting for fulfillment REST gateway..."
      for i in $(seq 1 60); do
        echo "Attempt ${i}: checking {{ $url }}"
        if curl -skf --connect-timeout 5 --max-time 30 {{ $url }}; then
          echo ""
          echo "Fulfillment service is ready."
          exit 0
        fi
        sleep 10
      done
      echo "ERROR: Fulfillment service not ready after 600s"
      exit 1
  env:
  - name: HOME
    value: /tmp
  volumeMounts:
  - name: tmp
    mountPath: /tmp
  resources:
    requests:
      cpu: 50m
      memory: 128Mi
    limits:
      cpu: 200m
      memory: 256Mi
  securityContext:
    allowPrivilegeEscalation: false
    readOnlyRootFilesystem: true
    capabilities:
      drop: ["ALL"]
{{- end }}
