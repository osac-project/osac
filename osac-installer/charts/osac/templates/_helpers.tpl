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

{{/*
Fail helm template when networking values are inconsistent. Schema validates
individual fields; this enforces cross-field invariants that JSON Schema
cannot express (duplicated Netris config, inverted port ranges, networking
facade vs low-level surface mismatches).
*/}}
{{- define "osac.validateValues" -}}
{{- $fm := .Values.global.fabricManager | default dict -}}
{{- $km := .Values.global.k8sManager | default dict -}}
{{- $expert := .Values.global.expertOverrides | default dict -}}
{{- $netris := $fm.netris | default dict -}}
{{- $agentless := $km.agentlessNet | default dict -}}
{{- $netExpertAap := $expert.aap | default false -}}
{{- $netExpertNetworkClass := $expert.networkClass | default false -}}
{{- $netExpertNetworkManagers := $expert.networkManagers | default false -}}
{{- $cf := .Values.aap.instanceGroups.clusterFulfillment | default dict -}}
{{- $nf := .Values.aap.instanceGroups.networkFulfillment | default dict -}}
{{- $cfCfg := $cf.config | default dict -}}
{{- $nfCfg := $nf.config | default dict -}}
{{- $cfSec := $cf.secret | default dict -}}
{{- $nfSec := $nf.secret | default dict -}}
{{- if not $netExpertAap -}}
{{- if $netris.enabled -}}
{{- $creds := $netris.credentials | default dict -}}
{{- $derived := dict
  "NETWORK_CLASS" "netris"
  "NETWORK_STEPS_COLLECTION" "netris.steps"
  "NETRIS_CONTROLLER_URL" ($netris.controllerUrl | default "")
  "NETRIS_USERNAME" ($creds.username | default "")
  "NETRIS_SITE_ID" ($netris.siteId | default "" | toString)
  "NETRIS_TENANT_ID" ($netris.tenantId | default "" | toString)
  "NETRIS_TENANT_NAME" ($netris.tenantName | default "")
  "NETRIS_MGMT_VPC_ID" ($netris.mgmtVpcId | default "" | toString)
  "NETRIS_MGMT_VPC_NAME" ($netris.mgmtVpcName | default "")
  "NETRIS_RESOURCE_CLASS_MAP" ($netris.resourceClassMap | default "")
-}}
{{- $cfCfg = merge $derived $cfCfg -}}
{{- $nfDerived := dict
  "NETRIS_CONTROLLER_URL" ($netris.controllerUrl | default "")
  "NETRIS_USERNAME" ($creds.username | default "")
  "NETRIS_SITE_ID" ($netris.siteId | default "" | toString)
  "NETRIS_TENANT_ID" ($netris.tenantId | default "" | toString)
  "NETRIS_TENANT_NAME" ($netris.tenantName | default "")
-}}
{{- $nfCfg = merge $nfDerived $nfCfg -}}
{{- if $creds.password }}
{{- $cfSec = merge (dict "NETRIS_PASSWORD" $creds.password) $cfSec -}}
{{- $nfSec = merge (dict "NETRIS_PASSWORD" $creds.password) $nfSec -}}
{{- end }}
{{- else if $agentless.enabled -}}
{{- $derived := dict "NETWORK_CLASS" "agentless_net" "NETWORK_STEPS_COLLECTION" "agentless_net.steps" -}}
{{- $cfCfg = merge $derived $cfCfg -}}
{{- end }}
{{- end }}
{{- if and $netris.enabled $agentless.enabled }}
  {{- fail "global.fabricManager.netris.enabled and global.k8sManager.agentlessNet.enabled cannot both be true" }}
{{- end }}
{{- $netrisConfigFields := list
  "NETRIS_CONTROLLER_URL"
  "NETRIS_USERNAME"
  "NETRIS_SITE_ID"
  "NETRIS_TENANT_ID"
  "NETRIS_TENANT_NAME"
-}}
{{- range $netrisConfigFields }}
  {{- $cfVal := index $cfCfg . | default "" | toString -}}
  {{- $nfVal := index $nfCfg . | default "" | toString -}}
  {{- if ne $cfVal $nfVal }}
    {{- fail (printf "aap.instanceGroups.clusterFulfillment.config.%s and networkFulfillment.config.%s must match (cluster=%q network=%q)" . . $cfVal $nfVal) }}
  {{- end }}
{{- end }}
{{- $cfPwd := $cfSec.NETRIS_PASSWORD | default "" | toString -}}
{{- $nfPwd := $nfSec.NETRIS_PASSWORD | default "" | toString -}}
{{- if ne $cfPwd $nfPwd }}
  {{- fail "aap.instanceGroups.clusterFulfillment.secret.NETRIS_PASSWORD and networkFulfillment.secret.NETRIS_PASSWORD must match" }}
{{- end }}
{{- $networkClass := .Values.networkClass | default dict -}}
{{- $fabricManager := $networkClass.fabricManager | default "" -}}
{{- $k8sManager := $networkClass.k8sManager | default "" -}}
{{- if not $netExpertNetworkClass -}}
{{- if $netris.enabled -}}
{{- $fabricManager = "netris" -}}
{{- $k8sManager = "" -}}
{{- else if $agentless.enabled -}}
{{- $fabricManager = "" -}}
{{- $k8sManager = "k8s_only" -}}
{{- end }}
{{- end }}
{{- if $networkClass.enabled -}}
  {{- range $networkClass.defaults.egressRules | default list }}
    {{- if and .portFrom .portTo (gt (int .portFrom) (int .portTo)) }}
      {{- fail (printf "networkClass.defaults.egressRules: portFrom (%v) must be <= portTo (%v)" .portFrom .portTo) }}
    {{- end }}
  {{- end }}
{{- end }}
{{- $nm := .Values.operator.networkManagers | default dict -}}
{{- $fabricManagers := $nm.fabricManagers | default dict -}}
{{- $k8sManagers := $nm.k8sManagers | default dict -}}
{{- $netClass := index $cfCfg "NETWORK_CLASS" | default "" | toString -}}
{{- $netSteps := index $cfCfg "NETWORK_STEPS_COLLECTION" | default "" | toString -}}
{{- if eq $netClass "netris" -}}
{{- if ne $netSteps "netris.steps" }}
  {{- fail (printf "NETWORK_CLASS=netris requires NETWORK_STEPS_COLLECTION=netris.steps (got %q)" $netSteps) }}
{{- end }}
{{- $netrisMgr := index $fabricManagers "netris" | default dict -}}
{{- $netrisEnabled := $netrisMgr.enabled | default false -}}
{{- if and (not $netExpertNetworkManagers) $netris.enabled }}
{{- $netrisEnabled = true -}}
{{- end }}
{{- if not $netrisEnabled }}
  {{- fail "NETWORK_CLASS=netris requires operator.networkManagers.fabricManagers.netris.enabled=true (or global.fabricManager.netris.enabled=true)" }}
{{- end }}
{{- if eq (index $cfCfg "NETRIS_CONTROLLER_URL" | default "" | toString) "" }}
  {{- fail "NETWORK_CLASS=netris requires NETRIS_CONTROLLER_URL on clusterFulfillment" }}
{{- end }}
{{- if and $fabricManager (ne $fabricManager "netris") }}
  {{- fail (printf "NETWORK_CLASS=netris conflicts with networkClass.fabricManager=%q" $fabricManager) }}
{{- end }}
{{- end }}
{{- if eq $netClass "agentless_net" -}}
{{- if ne $netSteps "agentless_net.steps" }}
  {{- fail (printf "NETWORK_CLASS=agentless_net requires NETWORK_STEPS_COLLECTION=agentless_net.steps (got %q)" $netSteps) }}
{{- end }}
{{- if and $k8sManager (ne $k8sManager "k8s_only") (ne $fabricManager "") }}
  {{- fail "NETWORK_CLASS=agentless_net conflicts with a fabric-backed networkClass manager" }}
{{- end }}
{{- end }}
{{- if and $networkClass.enabled $fabricManager -}}
{{- $mgr := index $fabricManagers $fabricManager | default dict -}}
{{- $mgrEnabled := $mgr.enabled | default false -}}
{{- if and (not $netExpertNetworkManagers) $netris.enabled (eq $fabricManager "netris") }}
{{- $mgrEnabled = true -}}
{{- end }}
{{- if not $mgrEnabled }}
  {{- fail (printf "networkClass.fabricManager=%q requires operator.networkManagers.fabricManagers.%s.enabled=true" $fabricManager $fabricManager) }}
{{- end }}
{{- end }}
{{- if and $networkClass.enabled $k8sManager -}}
{{- $mgr := index $k8sManagers $k8sManager | default dict -}}
{{- $mgrEnabled := $mgr.enabled | default false -}}
{{- if and (not $netExpertNetworkManagers) $agentless.enabled (eq $k8sManager "k8s_only") }}
{{- $mgrEnabled = true -}}
{{- end }}
{{- if not $mgrEnabled }}
  {{- fail (printf "networkClass.k8sManager=%q requires operator.networkManagers.k8sManagers.%s.enabled=true" $k8sManager $k8sManager) }}
{{- end }}
{{- end }}
{{- if and $networkClass.enabled (eq $k8sManager "k8s_only") (ne $fabricManager "") }}
  {{- fail "networkClass.k8sManager=k8s_only requires networkClass.fabricManager to be empty" }}
{{- end }}
{{- $syncInterval := $nm.capabilitiesSyncInterval | default "5m" -}}
{{- if not (regexMatch "^([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$" ($syncInterval | toString)) }}
  {{- fail (printf "operator.networkManagers.capabilitiesSyncInterval must be a valid Go duration (got %q)" $syncInterval) }}
{{- end }}
{{- end -}}
