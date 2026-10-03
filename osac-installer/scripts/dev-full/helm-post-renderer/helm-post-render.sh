#!/usr/bin/env bash
set -euo pipefail

# The pinned AWX operator chart uses an unavailable gcr.io image. Rewrite only
# that exact upstream reference so Helm creates and owns the working GHCR image.
sed 's|gcr.io/kubebuilder/kube-rbac-proxy:v0.15.0|ghcr.io/kube-rbac-proxy/kube-rbac-proxy:v0.22.1|g'
