{{/*
Host that the db-init hook and the database client Certificate connect to. The
install preflight resolves the same value, so a blank dbInit.host cannot make
the checked host and the used host differ.
*/}}
{{- define "osac.dbInit.host" -}}
{{- .Values.dbInit.host | default "postgres.osac-infra.svc.cluster.local" -}}
{{- end }}

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
True when this release should create a Strimzi KafkaUser for the fulfillment
service. That is only when the in-cluster Kafka named by kafka.clusterName is
enabled. An external cluster requires kafka.enabled=false and is configured
through service.kafka.connection.
*/}}
{{- define "osac.provisionFulfillmentKafkaUser" -}}
{{- if and .Values.kafka.enabled .Values.service.enabled }}
true
{{- end }}
{{- end }}

{{/*
Name of the Strimzi KafkaUser (and of the Secret it creates in the Kafka
namespace) used by the fulfillment service.
*/}}
{{- define "osac.fulfillmentKafkaUserName" -}}
fulfillment-service
{{- end }}

{{/*
Name of the Secret in the release namespace that holds the fulfillment Kafka
connection properties (brokers, user, and password).
*/}}
{{- define "osac.fulfillmentKafkaSecretName" -}}
fulfillment-service-kafka
{{- end }}

{{/*
Bootstrap servers for the in-cluster Kafka named by kafka.clusterName in
kafka.clusterNamespace.
*/}}
{{- define "osac.fulfillmentKafkaBrokers" -}}
{{- $ns := .Values.kafka.clusterNamespace | default "osac-kafka" }}
{{- $name := .Values.kafka.clusterName | default "osac-kafka" }}
{{- printf "%s-kafka-bootstrap.%s.svc:9093" $name $ns }}
{{- end }}

{{/*
Wait-for-fulfillment init container.
Uses .Values.cliImage for the container image.
*/}}
{{- define "osac.fulfillmentCurlTLS" -}}
--cacert /etc/ca-bundle/bundle.pem
{{- end -}}

{{- define "osac.fulfillmentAuthCredential" -}}
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

{{- define "osac.fulfillmentAuthEnv" -}}
{{- $clientID := include "osac.fulfillmentAuthCredential" (list . "client-id") | fromJson -}}
- name: FULFILLMENT_ISSUER_URL
  value: {{ tpl .Values.service.auth.issuerUrl . | quote }}
- name: FULFILLMENT_CLIENT_ID
  valueFrom:
    secretKeyRef:
      name: {{ $clientID.name | quote }}
      key: {{ $clientID.key | quote }}
- name: FULFILLMENT_CLIENT_SECRET_FILE
  value: /var/run/secrets/fulfillment-client-secret/client-secret
{{- end -}}

{{- define "osac.fulfillmentAuthVolumeMount" -}}
- name: fulfillment-client-secret
  mountPath: /var/run/secrets/fulfillment-client-secret
  readOnly: true
{{- end -}}

{{- define "osac.fulfillmentAuthVolume" -}}
{{- $clientSecret := include "osac.fulfillmentAuthCredential" (list . "client-secret") | fromJson -}}
- name: fulfillment-client-secret
  secret:
    secretName: {{ $clientSecret.name | quote }}
    defaultMode: 0440
    items:
    - key: {{ $clientSecret.key | quote }}
      path: client-secret
{{- end -}}

{{- define "osac.fulfillmentAccessTokenFunction" -}}
get_fulfillment_access_token() {
  local issuer_url="${FULFILLMENT_ISSUER_URL:-}"
  local client_id="${FULFILLMENT_CLIENT_ID:-}"
  local client_secret_file="${FULFILLMENT_CLIENT_SECRET_FILE:-}"
  if [[ "${issuer_url}" != https://* || -z "${client_id}" || -z "${client_secret_file}" || ! -s "${client_secret_file}" ]]; then
    echo "ERROR: Fulfillment service-account credentials are unavailable." >&2
    return 1
  fi

  local token_url="${issuer_url%/}/protocol/openid-connect/token"
  local response
  if ! response=$(curl --silent --show-error --fail {{ include "osac.fulfillmentCurlTLS" . }} \
    --connect-timeout 5 --max-time 30 \
    --request POST \
    --data-urlencode 'grant_type=client_credentials' \
    --data-urlencode "client_id=${client_id}" \
    --data-urlencode "client_secret@${client_secret_file}" \
    "${token_url}"); then
    echo "ERROR: Fulfillment token request failed." >&2
    return 1
  fi

  local access_token
  if ! access_token=$(printf '%s' "${response}" | python3 -c '
import json
import sys

try:
    token = json.load(sys.stdin).get("access_token")
except (json.JSONDecodeError, AttributeError):
    sys.exit(1)

if not isinstance(token, str) or not token:
    sys.exit(1)

sys.stdout.write(token)
'); then
    echo "ERROR: Fulfillment token response was invalid." >&2
    return 1
  fi

  printf '%s\n' "${access_token}"
}
{{- end -}}

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
        if curl -sf {{ include "osac.fulfillmentCurlTLS" . }} --connect-timeout 5 --max-time 30 {{ $url }}; then
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
  - name: ca-bundle
    mountPath: /etc/ca-bundle
    readOnly: true
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
The umbrella chart validates the values before rendering the AAP subchart.
Keep this chart-local adapter because Helm subcharts cannot call templates
defined by their parent chart; the AAP chart has the corresponding helper for
its two instance-group manifests.
*/}}
{{- define "osac.netrisConfig" -}}
{{- $netris := .netris | default dict -}}
{{- $creds := $netris.credentials | default dict -}}
{{- $validateCerts := true -}}
{{- if hasKey $netris "validateCerts" -}}
{{- $validateCerts = $netris.validateCerts -}}
{{- end -}}
{{- $derived := dict
  "NETRIS_CONTROLLER_URL" ($netris.controllerUrl | default "")
  "NETRIS_VALIDATE_CERTS" ($validateCerts | toString)
  "NETRIS_USERNAME" ($creds.username | default "")
  "NETRIS_SITE_ID" ($netris.siteId | default "" | toString)
  "NETRIS_TENANT_ID" ($netris.tenantId | default "" | toString)
  "NETRIS_TENANT_NAME" ($netris.tenantName | default "")
-}}
{{- if .cluster -}}
{{- $_ := set $derived "NETWORK_CLASS" "netris" -}}
{{- $_ := set $derived "NETWORK_STEPS_COLLECTION" "netris.steps" -}}
{{- $_ := set $derived "NETRIS_MGMT_VPC_ID" ($netris.mgmtVpcId | default "" | toString) -}}
{{- $_ := set $derived "NETRIS_MGMT_VPC_NAME" ($netris.mgmtVpcName | default "") -}}
{{- $_ := set $derived "NETRIS_RESOURCE_CLASS_MAP" ($netris.resourceClassMap | default "") -}}
{{- end -}}
{{- $derived | toYaml -}}
{{- end }}

{{/* Return true when a top-level AAP config source attempts a protected key. */}}
{{- define "osac.isProtectedNetrisKey" -}}
{{- $normalized := .key | lower | replace "_" "" | replace "-" "" | replace "." "" -}}
{{- $contract := .root.Files.Get "files/netris-protected-inputs.json" | fromJson -}}
{{- $matched := false -}}
{{- range $input := $contract.protected_inputs -}}
  {{- range $alias := $input.aliases -}}
    {{- if eq $normalized ($alias | lower | replace "_" "" | replace "-" "" | replace "." "") }}{{- $matched = true -}}{{ end -}}
  {{- end -}}
  {{- if eq $normalized ($input.canonical_environment | lower | replace "_" "" | replace "-" "" | replace "." "") }}{{- $matched = true -}}{{ end -}}
{{- end -}}
{{- if $matched }}true{{ end -}}
{{- end }}

{{/*
Fail helm template when networking values are inconsistent. Schema validates
individual fields; this enforces cross-field invariants that JSON Schema
cannot express (duplicated Netris config, inverted port ranges, networking
facade vs low-level surface mismatches).
*/}}
{{- define "osac.validateValues" -}}
{{- $networking := include "osac.networking.effective" . | fromYaml -}}
{{- $expert := .Values.global.expertOverrides | default dict -}}
{{- $netris := $networking.netris | default dict -}}
{{- $netrisEnabled := eq $networking.fabricManager "netris" -}}
{{- $agentlessEnabled := or (eq $networking.k8sManager "k8s_only") (eq $networking.fabricManager "agentless_net") -}}
{{- $agentlessStubEnabled := eq $networking.fabricManager "agentless_net" -}}
{{- $cudnEnabled := eq $networking.fabricManager "cudn_net" -}}
{{- $netExpertAap := $expert.aap | default false -}}
{{- $netExpertNetworkClass := $expert.networkClass | default false -}}
{{- $netExpertNetworkManagers := $expert.networkManagers | default false -}}
{{- $cf := .Values.aap.instanceGroups.clusterFulfillment | default dict -}}
{{- $nf := .Values.aap.instanceGroups.networkFulfillment | default dict -}}
{{- $cfCfg := $cf.config | default dict -}}
{{- $nfCfg := $nf.config | default dict -}}
{{- $cfSec := $cf.secret | default dict -}}
{{- $nfSec := $nf.secret | default dict -}}
{{- range $key, $value := $cfCfg -}}
{{- if eq (include "osac.isProtectedNetrisKey" (dict "root" $ "key" $key)) "true" -}}
{{- fail (printf "aap.instanceGroups.clusterFulfillment.config.%s is managed by global.networking.netris" $key) -}}
{{- end -}}
{{- end -}}
{{- range $key, $value := $nfCfg -}}
{{- if eq (include "osac.isProtectedNetrisKey" (dict "root" $ "key" $key)) "true" -}}
{{- fail (printf "aap.instanceGroups.networkFulfillment.config.%s is managed by global.networking.netris" $key) -}}
{{- end -}}
{{- end -}}
{{- range $key, $value := $cfSec -}}
{{- if eq (include "osac.isProtectedNetrisKey" (dict "root" $ "key" $key)) "true" -}}
{{- fail (printf "aap.instanceGroups.clusterFulfillment.secret.%s is managed by global.networking.netris" $key) -}}
{{- end -}}
{{- end -}}
{{- range $key, $value := $nfSec -}}
{{- if eq (include "osac.isProtectedNetrisKey" (dict "root" $ "key" $key)) "true" -}}
{{- fail (printf "aap.instanceGroups.networkFulfillment.secret.%s is managed by global.networking.netris" $key) -}}
{{- end -}}
{{- end -}}
{{- if not $netExpertAap -}}
{{- if $netrisEnabled -}}
{{- $creds := $netris.credentials | default dict -}}
{{- $derived := include "osac.netrisConfig" (dict "netris" $netris "cluster" true) | fromYaml -}}
{{/* Match the AAP chart: facade values win unless expertOverrides.aap is enabled. */}}
{{- $cfCfg = merge $derived $cfCfg -}}
{{- $nfDerived := include "osac.netrisConfig" (dict "netris" $netris "cluster" false) | fromYaml -}}
{{- $nfCfg = merge $nfDerived $nfCfg -}}
{{- if $creds.password }}
{{- $cfSec = merge (dict "NETRIS_PASSWORD" $creds.password) $cfSec -}}
{{- $nfSec = merge (dict "NETRIS_PASSWORD" $creds.password) $nfSec -}}
{{- end }}
{{- else if $agentlessEnabled -}}
{{- $derived := dict "NETWORK_CLASS" "agentless_net" "NETWORK_STEPS_COLLECTION" "agentless_net.steps" -}}
{{- $cfCfg = merge $derived $cfCfg -}}
{{- end }}
{{- end }}
{{- $netrisConfigFields := list
  "NETRIS_CONTROLLER_URL"
  "NETRIS_VALIDATE_CERTS"
  "NETRIS_USERNAME"
  "NETRIS_SITE_ID"
  "NETRIS_TENANT_ID"
  "NETRIS_TENANT_NAME"
-}}
{{- range $netrisConfigFields }}
  {{- $cfVal := index $cfCfg . | default "" | toString -}}
  {{- $nfVal := index $nfCfg . | default "" | toString -}}
  {{- if ne $cfVal $nfVal }}
    {{- fail (printf "aap.instanceGroups.clusterFulfillment.config.%s and networkFulfillment.config.%s must match" . .) }}
  {{- end }}
{{- end }}
{{- $cfPwd := $cfSec.NETRIS_PASSWORD | default "" | toString -}}
{{- $nfPwd := $nfSec.NETRIS_PASSWORD | default "" | toString -}}
{{- if ne $cfPwd $nfPwd }}
  {{- fail "aap.instanceGroups.clusterFulfillment.secret.NETRIS_PASSWORD and networkFulfillment.secret.NETRIS_PASSWORD must match" }}
{{- end }}
{{- $networkClass := $networking.networkClass | default dict -}}
{{- if $netExpertNetworkClass -}}
{{- $networkClass = .Values.networkClass | default dict -}}
{{- end }}
{{- $fabricManager := $networkClass.fabricManager | default "" -}}
{{- $k8sManager := $networkClass.k8sManager | default "" -}}
{{- if $networkClass.enabled -}}
  {{- range $networkClass.defaults.egressRules | default list }}
    {{- if and .portFrom .portTo (gt (int .portFrom) (int .portTo)) }}
      {{- fail (printf "networkClass.defaults.egressRules: portFrom (%v) must be <= portTo (%v)" .portFrom .portTo) }}
    {{- end }}
  {{- end }}
{{- end }}
{{- $nm := .Values.operator.networkManagers | default dict -}}
{{- $networkManagersEnabled := $nm.enabled | default false -}}
{{- $fabricManagers := $nm.fabricManagers | default dict -}}
{{- $k8sManagers := $nm.k8sManagers | default dict -}}
{{- if and (not $netExpertNetworkManagers) (or $netrisEnabled $agentlessEnabled $cudnEnabled) (not $networkManagersEnabled) }}
  {{- fail "global.networking requires operator.networkManagers.enabled=true" }}
{{- end }}
{{- if $cudnEnabled -}}
  {{- if not (and .Values.global.services.caas.enabled .Values.global.services.bmaas.enabled) -}}
    {{- fail "fabricManager=cudn_net requires CaaS and BMaaS services" -}}
  {{- end -}}
  {{- if or (ne $fabricManager "cudn_net") (ne $k8sManager "") (not $networkClass.enabled) (not $networkClass.isDefault) -}}
    {{- fail "fabricManager=cudn_net requires an enabled default CUDN-only NetworkClass" -}}
  {{- end -}}
  {{- $cudnMgr := index $fabricManagers "cudn_net" | default dict -}}
  {{- if not ($cudnMgr.enabled | default false) -}}
    {{- fail "fabricManager=cudn_net requires registered operator.networkManagers.fabricManagers.cudn_net.enabled=true" -}}
  {{- end -}}
  {{- if .Values.operator.enabled -}}
    {{- if not (and $netExpertAap .Values.aap.aap.instance.enabled .Values.aap.bootstrap.enabled $cf.enabled $nf.enabled) -}}
      {{- fail "fabricManager=cudn_net requires expert ci.steps AAP with both instance groups" -}}
    {{- end -}}
    {{- if or (ne (index $cfCfg "NETWORK_CLASS" | default "") "ci") (ne (index $cfCfg "NETWORK_STEPS_COLLECTION" | default "") "ci.steps") -}}
      {{- fail "fabricManager=cudn_net requires NETWORK_CLASS=ci and NETWORK_STEPS_COLLECTION=ci.steps" -}}
    {{- end -}}
  {{- else -}}
    {{- fail "fabricManager=cudn_net requires an enabled operator" -}}
  {{- end -}}
{{- end -}}
{{- $netClass := index $cfCfg "NETWORK_CLASS" | default "" | toString -}}
{{- $netSteps := index $cfCfg "NETWORK_STEPS_COLLECTION" | default "" | toString -}}
{{- if eq $netClass "netris" -}}
{{- if ne $netSteps "netris.steps" }}
  {{- fail (printf "NETWORK_CLASS=netris requires NETWORK_STEPS_COLLECTION=netris.steps (got %q)" $netSteps) }}
{{- end }}
{{- $netrisMgr := index $fabricManagers "netris" | default dict -}}
{{- $netrisRegistered := $netrisMgr.enabled | default false -}}
{{- if and (not $netExpertNetworkManagers) $netrisEnabled }}
{{- $netrisRegistered = true -}}
{{- end }}
{{- if not $netrisRegistered }}
  {{- fail "NETWORK_CLASS=netris requires operator.networkManagers.fabricManagers.netris.enabled=true" }}
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
{{- if and $agentlessStubEnabled (ne $fabricManager "agentless_net") }}
  {{- fail "AgentlessNet stub requires NetworkClass fabricManager=agentless_net" }}
{{- else if and (not $agentlessStubEnabled) (ne $fabricManager "") }}
  {{- fail "agentless_net.steps with the k8s_only manager requires networkClass.fabricManager to be empty" }}
{{- end }}
{{- end }}
{{- if and $networkClass.enabled $fabricManager -}}
{{- $mgr := index $fabricManagers $fabricManager | default dict -}}
{{- $mgrEnabled := $mgr.enabled | default false -}}
{{- if and (not $netExpertNetworkManagers) $netrisEnabled (eq $fabricManager "netris") }}
{{- $mgrEnabled = true -}}
{{- end }}
{{- if and (not $netExpertNetworkManagers) $agentlessStubEnabled (eq $fabricManager "agentless_net") }}
{{- $mgrEnabled = true -}}
{{- end }}
{{- if not $mgrEnabled }}
  {{- fail (printf "networkClass.fabricManager=%q requires operator.networkManagers.fabricManagers.%s.enabled=true" $fabricManager $fabricManager) }}
{{- end }}
{{- end }}
{{- if and $networkClass.enabled $k8sManager -}}
{{- $mgr := index $k8sManagers $k8sManager | default dict -}}
{{- $mgrEnabled := $mgr.enabled | default false -}}
{{- if and (not $netExpertNetworkManagers) $agentlessEnabled (eq $k8sManager "k8s_only") }}
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
