#!/bin/bash
set -e

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=== Setting up test environment ==="

# 0. Delete existing cluster if it exists
echo "Cleaning up any existing test cluster..."
kind delete cluster --name osac-test 2>/dev/null || true

# 0.5. Install required Python libraries
echo "Installing required Python libraries..."
pip install kubernetes openstacksdk 2>/dev/null || uv pip install kubernetes openstacksdk 2>/dev/null || uv pip install --system kubernetes openstacksdk

# 1. Create kind cluster
echo "Creating kind cluster..."
kind create cluster --name osac-test --wait 5m

# 1.5. Export kubeconfig to dedicated file
echo "Exporting kubeconfig to dedicated file..."
kind export kubeconfig --name osac-test --kubeconfig "${SCRIPT_DIR}/kubeconfig-osac-test"
echo "Kubeconfig exported to: ${SCRIPT_DIR}/kubeconfig-osac-test"

# 2. Clone osac-operator for CRDs
echo "Cloning osac-operator for CRDs..."
if [ -d "/tmp/osac-operator" ]; then
  rm -rf /tmp/osac-operator
fi
git clone https://github.com/osac-project/osac-operator.git /tmp/osac-operator

# 3. Install OSAC CRDs
echo "Installing OSAC CRDs..."
kubectl apply -f /tmp/osac-operator/config/crd/bases/

# 3.5. Install external CRDs needed by workflows
echo "Installing KubeVirt operator..."
kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/v1.1.0/kubevirt-operator.yaml

echo "Waiting for KubeVirt operator to be ready..."
kubectl wait --for=condition=Available --timeout=120s -n kubevirt deployment/virt-operator || echo "KubeVirt operator not ready yet"

echo "Installing KubeVirt CR to trigger CRD creation..."
kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/v1.1.0/kubevirt-cr.yaml

echo "Waiting for VirtualMachine CRD to be created..."
timeout 60 bash -c 'until kubectl get crd virtualmachines.kubevirt.io 2>/dev/null; do echo "Waiting for VirtualMachine CRD..."; sleep 2; done' || echo "Timeout waiting for VirtualMachine CRD"

echo "Installing CDI operator for DataVolume support..."
kubectl apply -f https://github.com/kubevirt/containerized-data-importer/releases/download/v1.58.0/cdi-operator.yaml

echo "Waiting for CDI operator to be ready..."
kubectl wait --for=condition=Available --timeout=120s -n cdi deployment/cdi-operator || echo "CDI operator not ready yet"

echo "Installing CDI CR to trigger DataVolume CRD creation..."
kubectl apply -f https://github.com/kubevirt/containerized-data-importer/releases/download/v1.58.0/cdi-cr.yaml

echo "Waiting for DataVolume CRD to be created..."
timeout 60 bash -c 'until kubectl get crd datavolumes.cdi.kubevirt.io 2>/dev/null; do echo "Waiting for DataVolume CRD..."; sleep 2; done' || echo "Timeout waiting for DataVolume CRD"

# 3.6. Scale down all deployments (keep CRs and CRDs)
echo "Scaling down all KubeVirt and CDI deployments to save resources..."
kubectl scale deployment -n kubevirt --all --replicas=0 || echo "Could not scale kubevirt deployments"
kubectl scale deployment -n cdi --all --replicas=0 || echo "Could not scale cdi deployments"

echo "Waiting for pods to terminate..."
kubectl wait --for=delete pod --all -n kubevirt --timeout=60s 2>/dev/null || echo "Some kubevirt pods still terminating"
kubectl wait --for=delete pod --all -n cdi --timeout=60s 2>/dev/null || echo "Some cdi pods still terminating"

echo "KubeVirt and CDI deployments scaled down. CRs and CRDs remain available for testing."

echo "Installing OLM CRDs..."
kubectl apply -f https://github.com/operator-framework/operator-lifecycle-manager/releases/download/v0.25.0/crds.yaml 2>/dev/null || echo "OLM CRDs may already exist or URL changed"

echo "Installing RHACM CRDs (ManagedCluster)..."
kubectl apply -f https://raw.githubusercontent.com/stolostron/managedcluster-import-controller/main/deploy/crds/cluster.open-cluster-management.io_managedclusters.yaml 2>/dev/null || echo "RHACM CRDs may already exist or URL changed"

echo "Installing HyperConverged CRD (OpenShift Virtualization / CNV)..."
kubectl apply --server-side -f https://raw.githubusercontent.com/kubevirt/hyperconverged-cluster-operator/main/deploy/crds/hco00.crd.yaml 2>/dev/null || echo "HyperConverged CRD may already exist or URL changed"

echo "Waiting for HyperConverged CRD to be established..."
kubectl wait --for=condition=Established crd/hyperconvergeds.hco.kubevirt.io --timeout=60s || echo "Timeout waiting for HyperConverged CRD"

# 4. Create test namespaces
echo "Creating test namespaces..."
kubectl create namespace osac-system || true
kubectl create namespace osac-workflows-test || true
kubectl create namespace cluster-test-cluster-work || true
kubectl create namespace computeinstance-test-vm-work || true
kubectl create namespace computeinstance-test-vm-gpu-work || true
kubectl create namespace openshift-cnv || true

# 5. Create minimal HyperConverged CR for GPU passthrough tests
echo "Creating HyperConverged CR for GPU passthrough tests..."
# Created via v1, matching the version the ocp_virt_vm role's GPU passthrough
# task prefers (and falls back from) and the version the test itself reads
# through in baseline.yml. Creating through v1beta1 while reading/patching
# through v1 lets the CRD's structural defaulting populate the object under
# one version's schema and validate it under the other's on the next write.
kubectl apply -f - <<HCEOF
apiVersion: hco.kubevirt.io/v1
kind: HyperConverged
metadata:
  name: kubevirt-hyperconverged
  namespace: openshift-cnv
spec: {}
HCEOF

# 6. Apply test fixtures
# Note: computeinstance-defaults-test.yaml is intentionally omitted — it has no required
# spec fields (tests defaults merging) and is read from file by tests via lookup().
echo "Applying test fixtures..."
kubectl apply -f "${SCRIPT_DIR}/fixtures/clusterorder-test.yaml"
kubectl apply -f "${SCRIPT_DIR}/fixtures/computeinstance-test.yaml"
kubectl apply -f "${SCRIPT_DIR}/fixtures/computeinstance-with-gpu-test.yaml"

# 6. Apply storage test fixtures and CRDs (conditional)
if [ "${STORAGE_TESTS_ENABLED:-}" = "true" ]; then
  echo "Installing VolumeSnapshot CRDs for storage tests..."
  kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/v8.5.0/client/config/crd/snapshot.storage.k8s.io_volumesnapshotclasses.yaml 2>/dev/null || echo "VolumeSnapshotClass CRD may already exist"
  kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/v8.5.0/client/config/crd/snapshot.storage.k8s.io_volumesnapshots.yaml 2>/dev/null || echo "VolumeSnapshot CRD may already exist"
  kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/v8.5.0/client/config/crd/snapshot.storage.k8s.io_volumesnapshotcontents.yaml 2>/dev/null || echo "VolumeSnapshotContent CRD may already exist"

  echo "Applying storage test fixtures..."
  kubectl apply -f "${SCRIPT_DIR}/fixtures/storage/" || true

  echo "Creating fake VAST CSIDrivers (short-circuits OLM installation in tests)..."
  kubectl apply -f - <<CSIEOF
apiVersion: storage.k8s.io/v1
kind: CSIDriver
metadata:
  name: csi.vastdata.com
spec:
  attachRequired: false
---
apiVersion: storage.k8s.io/v1
kind: CSIDriver
metadata:
  name: block.csi.vastdata.com
spec:
  attachRequired: true
CSIEOF

  # Generate self-signed TLS cert for mock VMS server
  echo "Generating TLS certificate for mock VMS server..."
  mkdir -p "${SCRIPT_DIR}/certs"
  openssl req -x509 -newkey rsa:2048 \
    -keyout "${SCRIPT_DIR}/certs/mock.key" \
    -out "${SCRIPT_DIR}/certs/mock.pem" \
    -days 1 -nodes -subj "/CN=127.0.0.1" 2>/dev/null

  # Start mock VMS server for storage integration tests (TLS required by vastdata.vms modules)
  echo "Starting mock VMS server (TLS)..."
  python3 "${SCRIPT_DIR}/mock_vms_server.py" 18443 \
    --tls --cert "${SCRIPT_DIR}/certs/mock.pem" --key "${SCRIPT_DIR}/certs/mock.key" &
  MOCK_VMS_PID=$!
  echo "${MOCK_VMS_PID}" > "${SCRIPT_DIR}/.mock_vms_pid"

  # Wait for mock server to be ready
  for i in $(seq 1 10); do
    if curl -sk https://127.0.0.1:18443/api > /dev/null 2>&1; then
      echo "Mock VMS server ready on port 18443 (PID: ${MOCK_VMS_PID})"
      break
    fi
    sleep 1
  done

  # Create storage test namespace and ConfigMap
  kubectl create namespace test-tenant-ns || true

  # Write VAST env vars to file for run_tests.sh (Make runs each recipe line in a separate shell)
  cat > "${SCRIPT_DIR}/.storage_env" <<'ENVEOF'
export VAST_ENDPOINT="127.0.0.1:18443"
export VAST_USERNAME="admin"
export VAST_PASSWORD="admin"
export VAST_VIP_POOL_NAME="osac-test-pool"
export VAST_VIP_POOL_IP_RANGES='[["10.0.0.10","10.0.0.50"]]'
export VAST_VIP_POOL_SUBNET_CIDR="24"
export VAST_VALIDATE_CERTS="false"
export STORAGE_TIERS='[{"name":"default","protocol":"nfs","provider":"vast","qos_policy":"test-qos","qos_limits":{"static_limits":{"max_reads_bw_mbps":100,"max_writes_bw_mbps":100}}}]'
ENVEOF
fi

echo "=== Test environment ready ==="
