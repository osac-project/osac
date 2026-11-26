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

{{/* Image reference helper. An empty tag renders "registry/repository:", which
     is not a valid reference, so validate.yaml fails the render when
     observability is enabled instead of falling back to "latest". */}}
{{- define "osac-observability.image" -}}
{{- $image := . -}}
{{- printf "%s/%s:%s" $image.registry $image.repository $image.tag -}}
{{- end -}}

{{- define "osac-observability.serviceAccountName" -}}
{{- printf "%s-thanos" .Release.Name -}}
{{- end -}}

{{/* One component label per workload so NetworkPolicies and Services can
     select a single role and the label name matches the contract in AGENTS.md. */}}
{{- define "osac-observability.componentLabels" -}}
{{ include "osac-observability.labels" .root }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{- define "osac-observability.componentSelectorLabels" -}}
{{ include "osac-observability.selectorLabels" .root }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/* The two admin proxies must not select each other's pods, so they carry a
     per-workload instance label instead of the release-wide one. */}}
{{- define "osac-observability.adminProxyLabels" -}}
helm.sh/chart: {{ printf "%s-%s" .root.Chart.Name .root.Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: thanos
app.kubernetes.io/instance: {{ .instance }}
app.kubernetes.io/component: query-auth-proxy
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
{{- end -}}

{{- define "osac-observability.podSecurityContext" -}}
runAsNonRoot: true
seccompProfile:
  type: RuntimeDefault
{{- end -}}

{{- define "osac-observability.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop:
    - ALL
{{- end -}}

{{/* Scrape labels the ServiceMonitors and the scraped Services share. The job
     value becomes the series' job label: Prometheus Operator reads jobLabel
     from the Service, not from the pod. */}}
{{- define "osac-observability.scrapeLabels" -}}
osac.openshift.io/metrics-scrape: "true"
osac.openshift.io/metrics-job: {{ .job }}
{{- end -}}

{{/* Convert a Prometheus duration ("30s", "1m30s", "2h") to whole seconds. */}}
{{- define "osac-observability.durationSeconds" -}}
{{- $total := 0 -}}
{{- range $part := regexFindAll "[0-9]+(ms|s|m|h|d|w|y)" (. | toString) -1 -}}
  {{- $number := regexFind "^[0-9]+" $part | int -}}
  {{- $unit := regexFind "(ms|s|m|h|d|w|y)$" $part -}}
  {{- if eq $unit "ms" -}}{{- $total = add $total (div $number 1000) -}}
  {{- else if eq $unit "s" -}}{{- $total = add $total $number -}}
  {{- else if eq $unit "m" -}}{{- $total = add $total (mul $number 60) -}}
  {{- else if eq $unit "h" -}}{{- $total = add $total (mul $number 3600) -}}
  {{- else if eq $unit "d" -}}{{- $total = add $total (mul $number 86400) -}}
  {{- else if eq $unit "w" -}}{{- $total = add $total (mul $number 604800) -}}
  {{- else if eq $unit "y" -}}{{- $total = add $total (mul $number 31536000) -}}
  {{- end -}}
{{- end -}}
{{- $total -}}
{{- end -}}

{{/* A cluster counts as stopped when no series with its cluster label arrived
     for longer than one scrape interval of either collector plus one interval
     of tolerance. The stopped-cluster query looks back over the Thanos
     retention window, so the threshold only decides how old the newest sample
     must be; it is not bounded by the instant-query lookback. */}}
{{- define "osac-observability.stoppedClusterThresholdSeconds" -}}
{{- $collection := (.Values.global | default dict).observability.collection | default dict -}}
{{- $controlPlane := include "osac-observability.durationSeconds" ($collection.osacControlPlane | default "30s") | int -}}
{{- $infrastructure := include "osac-observability.durationSeconds" ($collection.infrastructure | default "30s") | int -}}
{{- add (max $controlPlane $infrastructure) 30 -}}
{{- end -}}
