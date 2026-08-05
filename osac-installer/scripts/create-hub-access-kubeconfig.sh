#!/usr/bin/env bash

set -e

server_url=$(
  oc config view --minify --output jsonpath="{.clusters[*].cluster.server}"
)

server_name=${server_url#*.}
server_name=${server_name%%.*}

ca_data=$(
  oc config view --minify --raw --output jsonpath="{.clusters[*].cluster.certificate-authority-data}"
)

namespace=$(
  oc config view --minify --output jsonpath="{.contexts[*].context.namespace}"
)

token=$(oc -n "$namespace" extract secret/hub-access --keys token --to - 2>/dev/null)

echo "generating a kubeconfig for hub-access serviceaccount in $namespace namespace on $server_url"

cat <<EOF >/tmp/kubeconfig.hub-access.${server_name}
apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: $ca_data
    server: "$server_url"
  name: "$server_name"
contexts:
- context:
    cluster: "$server_name"
    namespace: "$namespace"
    user: "system:serviceaccount:$namespace:hub-access"
  name: "$server_name"
current-context: "$server_name"
kind: Config
preferences: {}
users:
- name: "system:serviceaccount:$namespace:hub-access"
  user:
    token: "$token"
EOF
