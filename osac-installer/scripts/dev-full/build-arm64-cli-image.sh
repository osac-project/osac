#!/usr/bin/env bash
# Build and load the arm64 replacement for the OpenShift CLI image used by the
# installer hooks. The image keeps the upstream tag so no chart values change
# is needed.

set -euo pipefail

cluster_name=${1:?usage: $0 <kind-cluster-name> [image]}
image=${2:-quay.io/openshift/origin-cli:4.20.0}

command -v docker >/dev/null 2>&1 || {
  echo "docker is required to build the Apple Silicon CLI image" >&2
  exit 1
}
command -v kind >/dev/null 2>&1 || {
  echo "kind is required to load the Apple Silicon CLI image" >&2
  exit 1
}

echo "Building ${image} for linux/arm64..."
docker build --platform linux/arm64 -t "${image}" - <<'EOF'
FROM registry.fedoraproject.org/fedora:latest
RUN dnf install -y tar gzip jq curl openssl python3 && \
    curl -L https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable/openshift-client-linux-arm64.tar.gz | tar -xz -C /usr/local/bin/ oc kubectl && \
    chmod +x /usr/local/bin/oc /usr/local/bin/kubectl
EOF

echo "Loading ${image} into kind cluster ${cluster_name}..."
kind load docker-image "${image}" --name "${cluster_name}"
