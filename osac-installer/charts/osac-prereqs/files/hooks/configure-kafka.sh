#!/usr/bin/env bash
set -euo pipefail

KAFKA_NS=osac-kafka

# Idempotency guard: skip all work if Kafka is already healthy.
if oc wait kafka/osac-kafka -n "${KAFKA_NS}" --for=condition=Ready --timeout=5s 2>/dev/null; then
  echo "Kafka cluster already ready, nothing to do."
  exit 0
fi

echo "Waiting for AMQ Streams install plan..."
until INSTALL_PLAN=$(oc get subscription amq-streams -n "${KAFKA_NS}" -o jsonpath='{.status.installPlanRef.name}' 2>/dev/null) && [[ -n "${INSTALL_PLAN}" ]]; do
  sleep 10
done

echo "Approving install plan ${INSTALL_PLAN}..."
oc patch installplan "${INSTALL_PLAN}" -n "${KAFKA_NS}" --type merge -p '{"spec":{"approved":true}}'

echo "Waiting for AMQ Streams Subscription to report installedCSV..."
until AMQ_CSV=$(oc get subscription amq-streams -n "${KAFKA_NS}" -o jsonpath='{.status.installedCSV}' 2>/dev/null) && [[ -n "${AMQ_CSV}" ]]; do
  sleep 10
done

echo "Waiting for CSV ${AMQ_CSV} to succeed..."
until [[ "$(oc get csv "${AMQ_CSV}" -n "${KAFKA_NS}" -o jsonpath='{.status.phase}')" == "Succeeded" ]]; do
  sleep 10
done

echo "Waiting for AMQ Streams cluster operator deployment..."
oc wait --for=condition=Available deploy -l olm.owner="${AMQ_CSV}" -n "${KAFKA_NS}" --timeout=300s

echo "Applying Kafka cluster..."
oc apply -f /config/kafka-cluster.yaml

echo "Waiting for Kafka cluster to be ready..."
until oc wait kafka/osac-kafka -n "${KAFKA_NS}" --for=condition=Ready --timeout=600s 2>/dev/null; do
  echo "Kafka cluster not yet ready, retrying..."
  sleep 15
done

echo "Kafka configuration complete."
