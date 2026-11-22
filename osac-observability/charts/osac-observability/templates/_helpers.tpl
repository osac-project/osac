{{/* Common naming for the observability tier. The umbrella chart owns the
     single switch; this chart never creates its namespace. */}}

{{- define "osac-observability.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end -}}

{{- define "osac-observability.namespace" -}}
osac-observability
{{- end -}}

{{- define "osac-observability.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: thanos
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "osac-observability.selectorLabels" -}}
app.kubernetes.io/name: thanos
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* Image reference helper. An empty tag means "latest" so the values file
     never carries the floating tag the repository's tag check rejects. */}}
{{- define "osac-observability.image" -}}
{{- $image := . -}}
{{- printf "%s/%s:%s" $image.registry $image.repository ($image.tag | default "latest") -}}
{{- end -}}

{{- define "osac-observability.serviceAccountName" -}}
{{- printf "%s-thanos" .Release.Name -}}
{{- end -}}
