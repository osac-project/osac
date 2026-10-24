#!/usr/bin/env bash
set -euo pipefail

# Activate Ironic for the BMF Metal3 inventory backend. Mirrors the E2E
# harness's setup-virtual-bmh.sh: the Provisioning CR turns on the cluster
# bare-metal operator with no provisioning network (virtual and CI clusters
# have none) and lets it discover BareMetalHosts in every namespace.
echo "Activating Ironic (Provisioning CR)..."
oc apply -f - <<'EOF'
apiVersion: metal3.io/v1alpha1
kind: Provisioning
metadata:
  name: provisioning-configuration
spec:
  provisioningNetwork: "Disabled"
  watchAllNamespaces: true
EOF

echo "Waiting for metal3 pods to appear..."
oc wait --for=create pods \
  -l baremetal.openshift.io/cluster-baremetal-operator=metal3-state \
  -n openshift-machine-api --timeout=300s

echo "Waiting for metal3 pods to be ready..."
oc wait --for=condition=Ready pods \
  -l baremetal.openshift.io/cluster-baremetal-operator=metal3-state \
  -n openshift-machine-api --timeout=600s

echo "Metal3 (Ironic) is active."
