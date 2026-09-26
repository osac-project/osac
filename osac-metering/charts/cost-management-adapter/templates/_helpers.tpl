{{- define "cost-management-adapter.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end -}}

{{- define "cost-management-adapter.fullname" -}}
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

{{- define "cost-management-adapter.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: {{ include "cost-management-adapter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "cost-management-adapter.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cost-management-adapter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "cost-management-adapter.kafkaClusterName" -}}
{{- .Values.kafka.clusterName | default "osac-kafka" }}
{{- end -}}

{{- define "cost-management-adapter.kafkaClusterNamespace" -}}
{{- .Values.kafka.clusterNamespace | default "osac-kafka" }}
{{- end -}}

{{- define "cost-management-adapter.kafkaBrokers" -}}
{{- .Values.kafka.brokers | default "osac-kafka-kafka-bootstrap.osac-kafka.svc.cluster.local:9093" }}
{{- end -}}
