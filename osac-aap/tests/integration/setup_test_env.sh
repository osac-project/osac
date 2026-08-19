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

# 2. Install OSAC CRDs
echo "Installing OSAC CRDs..."
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
kubectl apply -f "${REPO_ROOT}/osac-operator/config/crd/bases/"

# 2.1. Install external CRDs needed by workflows
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

# 2.2. Scale down all deployments (keep CRs and CRDs)
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

# 3. Create test namespaces
echo "Creating test namespaces..."
kubectl create namespace osac-system || true
kubectl create namespace osac-workflows-test || true
kubectl create namespace cluster-test-cluster-work || true
kubectl create namespace computeinstance-test-vm-work || true
kubectl create namespace computeinstance-test-vm-gpu-work || true
kubectl create namespace openshift-cnv || true

# 4. Create minimal HyperConverged CR for GPU passthrough tests
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

# 5. Apply test fixtures
# Note: computeinstance-defaults-test.yaml is intentionally omitted — it has no required
# spec fields (tests defaults merging) and is read from file by tests via lookup().
echo "Applying test fixtures..."
kubectl apply -f "${SCRIPT_DIR}/fixtures/clusterorder-test.yaml"
kubectl apply -f "${SCRIPT_DIR}/fixtures/computeinstance-test.yaml"
kubectl apply -f "${SCRIPT_DIR}/fixtures/computeinstance-with-gpu-test.yaml"

# 5.1. Apply storage test fixtures and CRDs (conditional)
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
  # VAST_ENDPOINT/VAST_USERNAME/VAST_PASSWORD/STORAGE_TIERS are no longer read by any
  # playbook or role (OSAC-1992 moved credential/tier input to
  # storage_provider_backend_connections/storage_provider_tiers extra_vars, set directly
  # by each test target) -- only VIP pool and TLS settings remain relevant here.
  # Resolve the csi-backends chart path from the repo root — integration tests
  # run playbooks from test subdirectories, not the top-level osac-aap/ dir,
  # so the playbook_dir-based default doesn't resolve correctly.
  REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
  cat > "${SCRIPT_DIR}/.storage_env" <<ENVEOF
export VAST_VIP_POOL_SUPERNET="10.0.0.0/24"
export VAST_VALIDATE_CERTS="false"
export OSAC_CSI_BACKENDS_CHART_REF="${REPO_ROOT}/../osac-csi-driver/charts/csi-backends"
ENVEOF

  # 5.2. Set up a local, TLS-trusted OCI registry hosting the osac-csi-driver charts at
  # two versions, so the csi_driver_install role-level test (CSI_DRIVER_TESTS_ENABLED) can
  # run in CI without a real oci://ghcr.io/osac-project/charts release tag -- no such tag
  # has been cut yet (OSAC-3290 Risk Assessment item 1). Plain HTTP is not viable: the
  # vendored kubernetes.core.helm module (pinned 5.2.0) has no plain_http or
  # insecure-skip-tls-verify parameter at all, and Helm's own OCI client special-cases
  # localhost/127.0.0.1 to force plain HTTP regardless of TLS config -- so a genuinely
  # system-trusted TLS cert on a non-loopback hostname is required. localtest.me is a
  # public DNS name that resolves to 127.0.0.1, sidestepping both problems with no
  # /etc/hosts changes.
  echo "Setting up local TLS OCI registry for csi_driver_install tests..."

  CSI_DRIVER_TEST_REGISTRY_HOST="localtest.me"
  CSI_DRIVER_TEST_REGISTRY_PORT="5500"
  CSI_DRIVER_TEST_REGISTRY_REPO="oci://${CSI_DRIVER_TEST_REGISTRY_HOST}:${CSI_DRIVER_TEST_REGISTRY_PORT}/csi-driver-test"
  # Prefer the same container tool kind itself would use (KIND_EXPERIMENTAL_PROVIDER),
  # falling back to whichever of docker/podman is actually installed.
  if [ -n "${KIND_EXPERIMENTAL_PROVIDER:-}" ]; then
    CSI_DRIVER_TEST_CONTAINER_TOOL="${KIND_EXPERIMENTAL_PROVIDER}"
  elif command -v docker > /dev/null 2>&1; then
    CSI_DRIVER_TEST_CONTAINER_TOOL="docker"
  else
    CSI_DRIVER_TEST_CONTAINER_TOOL="podman"
  fi

  # This whole section is best-effort: it must never abort the rest of setup (the storage
  # tests it shares STORAGE_TESTS_ENABLED with, e.g. the playbook wiring tests, don't need
  # it at all). Only Debian/Ubuntu's system trust store is supported today -- that's what
  # CI (ubuntu-latest) actually runs on. On any other OS, skip straight to leaving
  # CSI_DRIVER_INSTALL_TEST_REGISTRY unset: csi_driver_install's own role-level test then
  # falls back to the real (not-yet-published) oci://ghcr.io/osac-project/charts and fails
  # with a clear registry-not-found error, rather than every other storage test failing to
  # even start because this section couldn't get a trusted TLS chain.
  if [ -d /usr/local/share/ca-certificates ]; then
    echo "Generating a local CA and server certificate for ${CSI_DRIVER_TEST_REGISTRY_HOST}..."
    openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
      -keyout "${SCRIPT_DIR}/certs/registry-ca.key" \
      -out "${SCRIPT_DIR}/certs/registry-ca.pem" \
      -subj "/CN=osac-test-registry-ca" 2>/dev/null

    openssl req -newkey rsa:2048 -nodes \
      -keyout "${SCRIPT_DIR}/certs/registry.key" \
      -out "${SCRIPT_DIR}/certs/registry.csr" \
      -subj "/CN=${CSI_DRIVER_TEST_REGISTRY_HOST}" 2>/dev/null

    echo "subjectAltName=DNS:${CSI_DRIVER_TEST_REGISTRY_HOST}" > "${SCRIPT_DIR}/certs/registry.ext"

    openssl x509 -req -in "${SCRIPT_DIR}/certs/registry.csr" \
      -CA "${SCRIPT_DIR}/certs/registry-ca.pem" -CAkey "${SCRIPT_DIR}/certs/registry-ca.key" \
      -CAcreateserial -out "${SCRIPT_DIR}/certs/registry.pem" \
      -days 1 -extfile "${SCRIPT_DIR}/certs/registry.ext" 2>/dev/null

    echo "Installing the local CA into the system trust store..."
    sudo cp "${SCRIPT_DIR}/certs/registry-ca.pem" /usr/local/share/ca-certificates/osac-test-registry-ca.crt
    sudo update-ca-certificates > /dev/null

    echo "Starting local OCI registry (TLS) on port ${CSI_DRIVER_TEST_REGISTRY_PORT}..."
    "${CSI_DRIVER_TEST_CONTAINER_TOOL}" rm -f osac-test-csi-registry > /dev/null 2>&1 || true
    "${CSI_DRIVER_TEST_CONTAINER_TOOL}" run -d --name osac-test-csi-registry \
      -p "${CSI_DRIVER_TEST_REGISTRY_PORT}:5000" \
      -v "${SCRIPT_DIR}/certs:/certs:ro" \
      -e REGISTRY_HTTP_TLS_CERTIFICATE=/certs/registry.pem \
      -e REGISTRY_HTTP_TLS_KEY=/certs/registry.key \
      registry:2 > /dev/null

    echo "Waiting for local OCI registry to be ready..."
    for i in $(seq 1 10); do
      if curl -sf "https://${CSI_DRIVER_TEST_REGISTRY_HOST}:${CSI_DRIVER_TEST_REGISTRY_PORT}/v2/" > /dev/null 2>&1; then
        echo "Local OCI registry ready on ${CSI_DRIVER_TEST_REGISTRY_HOST}:${CSI_DRIVER_TEST_REGISTRY_PORT}"
        break
      fi
      sleep 1
    done

    echo "Packaging and pushing osac-csi-driver charts (csi-driver, csi-backends) at versions 0.1.0 and 0.1.1..."
    CSI_DRIVER_CHARTS_DIR="${REPO_ROOT}/osac-csi-driver/charts"
    CSI_DRIVER_CHART_PKG_DIR="${SCRIPT_DIR}/.csi_driver_chart_pkgs"
    rm -rf "${CSI_DRIVER_CHART_PKG_DIR}"
    mkdir -p "${CSI_DRIVER_CHART_PKG_DIR}"

    for chart in csi-driver csi-backends; do
      for version in 0.1.0 0.1.1; do
        helm package "${CSI_DRIVER_CHARTS_DIR}/${chart}" --version "${version}" --app-version "${version}" \
          -d "${CSI_DRIVER_CHART_PKG_DIR}"
        helm push "${CSI_DRIVER_CHART_PKG_DIR}/${chart}-${version}.tgz" "${CSI_DRIVER_TEST_REGISTRY_REPO}"
      done
    done

    # Append to the same env file consumed by run_tests.sh (Make runs each recipe line in
    # a separate shell) -- csi_driver_install's role-level test overrides
    # csi_driver_install_chart_registry to this value instead of the real, not-yet-published
    # oci://ghcr.io/osac-project/charts.
    cat >> "${SCRIPT_DIR}/.storage_env" <<ENVEOF
export CSI_DRIVER_INSTALL_TEST_REGISTRY="${CSI_DRIVER_TEST_REGISTRY_REPO}"
ENVEOF
  else
    echo "No supported CA trust store found (expected /usr/local/share/ca-certificates on Debian/Ubuntu, which is what CI runs on) -- skipping local OCI registry setup. csi_driver_install's own role-level test will fail against the real, not-yet-published oci://ghcr.io/osac-project/charts; every other STORAGE_TESTS_ENABLED test is unaffected."
  fi
fi

echo "=== Test environment ready ==="
