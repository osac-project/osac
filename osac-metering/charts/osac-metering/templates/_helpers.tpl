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

{{- define "osac-metering.kafkaSaslUsername" -}}
osac-metering
{{- end -}}

{{- define "osac-metering.kafkaSaslSecretName" -}}
osac-metering
{{- end -}}

{{/*
Init container that waits for the Kafka SASL secret in the cluster namespace
and copies its password into the shared kafka-secrets emptyDir.
Args: list of [context, saslSecretName]
*/}}
{{- define "osac-metering.kafkaSecretsInitContainer" -}}
{{- $root := index . 0 -}}
{{- $saslSecret := index . 1 -}}
- name: fetch-kafka-secrets
  image: {{ $root.Values.cliImage | default "quay.io/openshift/origin-cli:4.20.0" }}
  env:
    - name: HOME
      value: /tmp
    - name: SOURCE_NS
      value: {{ include "osac-metering.kafkaClusterNamespace" $root }}
    - name: SASL_SECRET
      value: {{ $saslSecret }}
  volumeMounts:
    - name: kafka-secrets
      mountPath: /etc/kafka-secrets
    - name: tmp
      mountPath: /tmp
  securityContext:
    # The CLI image defaults to root, which kind refuses under the main
    # container's runAsNonRoot. OpenShift's restricted SCC assigns its own UID
    # range and rejects a pinned one, so the init container opts out of the
    # non-root requirement instead: it only reads a Secret and writes to
    # emptyDirs.
    runAsNonRoot: false
    allowPrivilegeEscalation: false
    readOnlyRootFilesystem: true
    capabilities:
      drop:
        - ALL
  resources:
    requests:
      cpu: 50m
      memory: 64Mi
    limits:
      cpu: 100m
      memory: 128Mi
  command:
    - /bin/bash
    - -euo
    - pipefail
    - -c
    - |
      DEADLINE=$((SECONDS + 300))
      wait_for_secret() {
        local name=$1 ns=$2
        echo "Waiting for secret ${name} in ${ns} (timeout 5m)..."
        until oc get secret "${name}" -n "${ns}" > /dev/null 2>&1; do
          if [ $SECONDS -ge $DEADLINE ]; then
            echo "Timed out waiting for secret ${name} in ${ns}"
            exit 1
          fi
          sleep 5
        done
      }

      wait_for_secret "${SASL_SECRET}" "${SOURCE_NS}"

      oc get secret "${SASL_SECRET}" -n "${SOURCE_NS}" -o jsonpath='{.data.password}' | base64 -d > /etc/kafka-secrets/password
      echo "Kafka SASL secret fetched."
{{- end -}}

{{/*
ACLs shared by the adapter KafkaUsers: the adapter reads every tenant's
heartbeat topic and the shared inference topic, writes every tenant's
dead-letter topic plus the inference dead-letter topic, and reads its own
consumer group.
Args: adapter user name (also the consumer group name)
*/}}
{{- define "osac-metering.adapterKafkaUserAcls" -}}
{{- $user := . -}}
- resource:
    type: topic
    name: osac.metering.heartbeat.
    patternType: prefix
  operations:
    - Read
    - Describe
  host: "*"
- resource:
    type: topic
    name: osac.metering.inference
    patternType: literal
  operations:
    - Read
    - Describe
  host: "*"
- resource:
    type: topic
    name: osac.metering.dlq.
    patternType: prefix
  operations:
    - Write
    - Describe
  host: "*"
- resource:
    type: topic
    name: osac.metering.inference.dlq
    patternType: literal
  operations:
    - Write
    - Describe
  host: "*"
- resource:
    type: group
    name: {{ $user }}
    patternType: literal
  operations:
    - Read
  host: "*"
{{- end -}}
