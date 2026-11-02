#!/usr/bin/env bash
set -euo pipefail

KAFKA_NS=osac-kafka
LISTENER_TLS_SECRET=osac-kafka-listener-tls

wait_for_listener_tls() {
  echo "Waiting for Kafka listener TLS secret ${LISTENER_TLS_SECRET}..."
  local deadline=$((SECONDS + 600))
  until oc get secret "${LISTENER_TLS_SECRET}" -n "${KAFKA_NS}" \
    -o jsonpath='{.data.tls\.crt}' 2>/dev/null | grep -q .; do
    if [ "${SECONDS}" -ge "${deadline}" ]; then
      echo "Timed out waiting for secret ${LISTENER_TLS_SECRET} in ${KAFKA_NS}"
      exit 1
    fi
    sleep 5
  done
  echo "Kafka listener TLS secret is ready."
}

# Always apply the Kafka CR so listener (and other) spec changes take effect on
# upgrade. Skip the operator wait when the cluster already exists.
if oc get kafka/osac-kafka -n "${KAFKA_NS}" &>/dev/null; then
  echo "Kafka cluster already present, skipping operator wait."
elif oc get subscriptions.operators.coreos.com amq-streams -n "${KAFKA_NS}" &>/dev/null; then
  echo "Waiting for AMQ Streams install plan..."
  until INSTALL_PLAN=$(oc get subscriptions.operators.coreos.com amq-streams -n "${KAFKA_NS}" -o jsonpath='{.status.installPlanRef.name}' 2>/dev/null) && [[ -n "${INSTALL_PLAN}" ]]; do
    sleep 10
  done

  echo "Approving install plan ${INSTALL_PLAN}..."
  oc patch installplan "${INSTALL_PLAN}" -n "${KAFKA_NS}" --type merge -p '{"spec":{"approved":true}}'

  echo "Waiting for AMQ Streams Subscription to report installedCSV..."
  until AMQ_CSV=$(oc get subscriptions.operators.coreos.com amq-streams -n "${KAFKA_NS}" -o jsonpath='{.status.installedCSV}' 2>/dev/null) && [[ -n "${AMQ_CSV}" ]]; do
    sleep 10
  done

  echo "Waiting for CSV ${AMQ_CSV} to succeed..."
  until [[ "$(oc get csv "${AMQ_CSV}" -n "${KAFKA_NS}" -o jsonpath='{.status.phase}')" == "Succeeded" ]]; do
    sleep 10
  done

  echo "Waiting for AMQ Streams cluster operator deployment..."
  oc wait --for=condition=Available deploy -l olm.owner="${AMQ_CSV}" -n "${KAFKA_NS}" --timeout=300s
else
  echo "No AMQ Streams subscription found; waiting for Strimzi operator..."
  until oc get kafka -n "${KAFKA_NS}" &>/dev/null && oc get kafkanodepool -n "${KAFKA_NS}" &>/dev/null; do
    sleep 5
  done

  echo "Waiting for Strimzi cluster operator deployment..."
  until oc get deploy/strimzi-cluster-operator -n "${KAFKA_NS}" &>/dev/null; do
    sleep 5
  done
  oc wait --for=condition=Available deploy/strimzi-cluster-operator -n "${KAFKA_NS}" --timeout=300s
fi

echo "Applying Kafka cluster..."
oc apply -f /config/kafka-cluster.yaml

wait_for_listener_tls

echo "Waiting for Kafka cluster to be ready..."
until oc wait kafka/osac-kafka -n "${KAFKA_NS}" --for=condition=Ready --timeout=600s 2>/dev/null; do
  echo "Kafka cluster not yet ready, retrying..."
  sleep 15
done

echo "Kafka configuration complete."
