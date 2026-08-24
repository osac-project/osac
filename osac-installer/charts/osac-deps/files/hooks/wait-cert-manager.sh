#!/usr/bin/env bash
set -euo pipefail

echo "Waiting for cert-manager CRD to exist..."
until oc get crd certmanagers.operator.openshift.io; do
  sleep 10
done

echo "Re-applying CertManager CR..."
oc apply -f - <<'EOF'
apiVersion: operator.openshift.io/v1alpha1
kind: CertManager
metadata:
  name: cluster
spec:
  managementState: "Managed"
EOF

echo "Waiting for cert-manager ServiceAccounts..."
for sa in cert-manager cert-manager-webhook cert-manager-cainjector; do
  echo "  Waiting for ServiceAccount ${sa}..."
  until oc get sa "${sa}" -n cert-manager &>/dev/null; do
    sleep 5
  done
  echo "  ServiceAccount ${sa} exists."
done

echo "Waiting for cert-manager deployment..."
oc wait --for=condition=Available deploy/cert-manager -n cert-manager --timeout=300s

echo "Waiting for cert-manager-webhook deployment..."
oc wait --for=condition=Available deploy/cert-manager-webhook -n cert-manager --timeout=300s

echo "Waiting for cert-manager-cainjector deployment..."
oc wait --for=condition=Available deploy/cert-manager-cainjector -n cert-manager --timeout=300s

echo "cert-manager is ready."
