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
Fail helm template when values break a cross-field rule that JSON Schema
cannot express.
*/}}
{{- define "osac.validateValues" -}}
{{- include "osac.networking.validate" . -}}
{{- end -}}

{{/*
Name of the customer-managed Netris CA ConfigMap, or "" when no CA bundle is
configured. aap.instanceGroups.networkFulfillment.config.NETRIS_CA_CONFIGMAP is
the single source of truth: AAP config-as-code mounts bundle.pem from the same
ConfigMap, so validation and protection must use the same name.
*/}}
{{- define "osac.netrisCaConfigMapName" -}}
{{- $aap := default dict .Values.aap -}}
{{- $config := dig "instanceGroups" "networkFulfillment" "config" dict $aap -}}
{{- if hasKey $config "NETRIS_CA_CONFIGMAP" -}}
{{- index $config "NETRIS_CA_CONFIGMAP" | toString -}}
{{- end -}}
{{- end -}}
