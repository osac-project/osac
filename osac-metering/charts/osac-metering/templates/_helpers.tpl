{{- define "osac-metering.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end -}}

{{- define "osac-metering.fullname" -}}
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
{{- end -}}

{{- define "osac-metering.osacDeploymentId" -}}
{{- .Values.global.osacDeploymentId -}}
{{- end -}}

{{- define "osac-metering.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ include "osac-metering.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "osac-metering.selectorLabels" -}}
app.kubernetes.io/name: {{ include "osac-metering.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "osac-metering.kafkaClusterName" -}}
{{- .Values.kafka.clusterName | default "osac-kafka" }}
{{- end -}}

{{- define "osac-metering.kafkaClusterNamespace" -}}
{{- .Values.kafka.clusterNamespace | default "osac-kafka" }}
{{- end -}}

{{- define "osac-metering.kafkaBrokers" -}}
{{- .Values.kafka.brokers | default "osac-kafka-kafka-bootstrap.osac-kafka.svc.cluster.local:9093" }}
{{- end -}}

{{- define "osac-metering.kafkaTopic" -}}
osac.metering.lifecycle
{{- end -}}

{{- define "osac-metering.kafkaSaslUsername" -}}
osac-metering
{{- end -}}

{{- define "osac-metering.kafkaSaslSecretName" -}}
osac-metering
{{- end -}}

{{- define "osac-metering.kafkaReplicas" -}}
3
{{- end -}}

{{/*
Return "true" when this chart should render its ServiceMonitor. A standalone
install sets serviceMonitor.enabled; the umbrella chart enables it through
global.observability, the only value Helm propagates into subcharts.
*/}}
{{- define "osac-metering.serviceMonitorEnabled" -}}
{{- $serviceMonitor := .Values.serviceMonitor | default dict -}}
{{- $globalObservability := (.Values.global | default dict).observability | default dict -}}
{{- if or $serviceMonitor.enabled $globalObservability.enabled -}}true{{- end -}}
{{- end -}}

{{/*
Resolve the scrape interval. The standalone serviceMonitor.interval wins when
set; otherwise the umbrella's global.observability.collection.osacControlPlane
is used. Fails rather than defaulting so no template hardcodes an interval.

Keep this in sync with the identical helper in the fulfillment chart. A
subchart cannot call a parent or sibling template, so the resolution is
duplicated.
*/}}
{{- define "osac-metering.serviceMonitorInterval" -}}
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
{{- end -}}

{{/*
Check if a service tier is enabled via global.services.<key>.enabled.
Args: list of [context, serviceKey]
Falls back to true when global.services is not set.
Returns non-empty string for enabled, empty for disabled (for use in {{- if include ... }}).
*/}}
{{- define "osac-metering.serviceEnabled" -}}
{{- $ctx := index . 0 -}}
{{- $svcKey := index . 1 -}}
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
{{- if $enabled -}}true{{- end -}}
{{- end }}
