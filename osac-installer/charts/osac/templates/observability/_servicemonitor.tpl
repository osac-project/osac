{{/*
Render one plain-HTTP ServiceMonitor for the OSAC control plane.

The monitors live in the umbrella chart so that a single
observability.enabled switch and a single scrape interval drive every target;
Helm cannot propagate either value into a subchart's own serviceMonitor
settings without duplicating the switch.

Arguments (dict):
  root      chart context
  name      ServiceMonitor name
  jobLabel  Service label whose value becomes the fixed Prometheus job label
  selector  Service label selector (map)
  port      Service port name to scrape

Callers must guard on observability.enabled, the owning component being
enabled, and the ServiceMonitor CRD being present.
*/}}
{{- define "osac.serviceMonitor" -}}
{{- $root := .root -}}
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ .name }}
  namespace: {{ $root.Release.Namespace }}
  labels:
    {{- include "osac.labels" $root | nindent 4 }}
spec:
  jobLabel: {{ .jobLabel }}
  namespaceSelector:
    matchNames:
    - {{ $root.Release.Namespace }}
  selector:
    matchLabels:
      {{- toYaml .selector | nindent 6 }}
  endpoints:
  - port: {{ .port }}
    scheme: http
    path: /metrics
    interval: {{ $root.Values.observability.collection.osacControlPlane | quote }}
{{- end }}
