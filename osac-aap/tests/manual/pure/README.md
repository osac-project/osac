# Manual Tests for Pure Storage FlashBlade Integration

This directory contains manual test playbooks for Pure Storage FlashBlade integration with OSAC.

## Overview

The Pure Storage integration follows a **Realm pool** model where:
- Realms are pre-provisioned on FlashBlade and registered in a pool
- Tenants check out available Realms on setup
- Realms are released back to the pool on teardown
- The CSI driver dynamically creates filesystems on-demand when PVCs are provisioned

## Prerequisites

1. **Environment Variables:**
   ```bash
   export OSAC_STORAGE_CONFIG_NAMESPACE="osac-system"
   export KUBECONFIG="${HOME}/.kube/config"
   ```

2. **Kubernetes Cluster:**
   - Cluster must be accessible (e.g., kind cluster `osac-test`)
   - Namespace `osac-system` must exist

3. **Pure CSI Driver:**
   - Standalone Pure CSI driver installed (`pxd.portworx.com` provisioner)
   - See [`pure-csi-driver-installation.md`](pure-csi-driver-installation.md) for installation steps
   - Manifests are in `pure-csi-manifests/`

4. **FlashBlade Access Tokens:**
   - **Storage Admin Token** - for Realm provisioning (array-wide admin with realm creation permissions)
   - **Realm Admin Token** - for tenant operations (realm-scoped token with NFS export/filesystem permissions)

## Test Playbooks

### 1. Realm Provisioning Tests (Storage Admin Token Required)

These tests require a **storage admin token** with array-wide permissions to create Realms.

#### `test_flashblade_auth.yml`
Tests FlashBlade API authentication and basic connectivity.

```bash
# Set storage admin token for Realm provisioning
export PUREFB_API="T-your-storage-admin-token"
export PUREFB_URL="https://10.3.11.50"

ansible-playbook tests/manual/test_flashblade_auth.yml
```

**Tests:**
- FlashBlade API authentication
- Token validation
- Network connectivity

---

#### `test_pure_realm_role.yml`
Tests the `osac.service.pure_realm` role for Realm provisioning.

```bash
# Edit realms.yaml with your configuration first
vi tests/manual/realms.yaml

# Set storage admin token
export PUREFB_API="T-your-storage-admin-token"
export PUREFB_URL="https://10.3.11.50"

ansible-playbook tests/manual/test_pure_realm_role.yml
```

**Tests:**
- Realm provisioning on FlashBlade (requires storage admin permissions)
- NFS server and export policy creation
- Kubernetes Secret creation with realm-scoped token
- ConfigMap `pure-realm-pool` creation
- Idempotency

**Output:**
- Realm credential Secret: `pure-realm-<realm-name>` (contains realm admin token)
- ConfigMap: `pure-realm-pool` (Realm pool registry)

---

### 2. Tenant Workflow Tests (Realm Admin Token)

These tests use the **realm admin token** stored in the Realm credential Secret (created by Realm provisioning). No environment variables needed - credentials are read from Kubernetes Secrets.

#### `test_pure_setup.yml`
Tests tenant setup: Realm checkout and hub Secret creation.

```bash
ansible-playbook tests/manual/test_pure_setup.yml
```

**Tests:**
- Realm pool validation
- Available Realm selection (first unclaimed Realm)
- Hub Secret creation with tenant-to-Realm allocation
- Idempotency (reuses same Realm on re-run)

**Prerequisites:**
- Realm pool ConfigMap exists: `pure-realm-pool`
- At least one Realm credential Secret exists

**Output:**
- Hub Secret: `pure-tenant-config-test-pure-tenant-be-pure-test`
- Labels: `osac.openshift.io/pure-realm`, `osac.openshift.io/tenant`

---

#### `test_pure_ensure_sc.yml`
Tests StorageClass and CSI Secret creation.

```bash
ansible-playbook tests/manual/test_pure_ensure_sc.yml
```

**Tests:**
- CSI Secret creation with FlashBlade credentials
- StorageClass creation per tier (default, fast)
- Correct provisioner (`pxd.portworx.com`)
- Realm-scoped resource names (`<realm>::nfs-server`, `<realm>::export-policy`)

**Prerequisites:**
- `test_pure_setup.yml` completed successfully
- Hub Secret exists

**Output:**
- StorageClasses: `pure-nfs-test-pure-tenant-default`, `pure-nfs-test-pure-tenant-fast`
- CSI Secret: `pure-csi-secret-be-pure-test`

---

### 3. Teardown Tests

#### `test_pure_teardown_cluster_storage.yml`
Tests cluster-side resource cleanup.

```bash
ansible-playbook tests/manual/test_pure_teardown_cluster_storage.yml
```

**Tests:**
- StorageClass deletion (by label selector)
- CSI Secret deletion
- Idempotency
- Hub Secret preservation (backend resources untouched)

**What it deletes:**
- StorageClasses for the tenant
- CSI Secret in tenant namespace

**What it does NOT delete:**
- Hub Secret (Realm allocation)
- PVCs (user responsibility)
- FlashBlade filesystems (deleted when PVCs are deleted)

---

#### `test_pure_teardown_backend.yml`
Tests backend resource cleanup and Realm release.

```bash
ansible-playbook tests/manual/test_pure_teardown_backend.yml
```

**Tests:**
- Hub Secret deletion
- Realm release back to pool
- Idempotency

**What it deletes:**
- Hub Secret (`pure-tenant-config-<tenant>-<backend-id>`)

**What it does NOT delete:**
- Realm itself (stays in pool for reuse)
- Realm credential Secret
- FlashBlade filesystems
- Cluster-side resources (StorageClasses, CSI Secret)

---

## Complete Test Workflow

### Setup Flow

```bash
# 1. Provision Realm (one-time setup)
ansible-playbook tests/manual/test_pure_realm_role.yml

# 2. Tenant onboarding
ansible-playbook tests/manual/test_pure_setup.yml

# 3. Create StorageClasses
ansible-playbook tests/manual/test_pure_ensure_sc.yml

# 4. Create test PVC and pod
kubectl apply -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: pure-test-pvc
  namespace: osac-system
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: pure-nfs-test-pure-tenant-default
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: Pod
metadata:
  name: pure-test-pod
  namespace: osac-system
spec:
  containers:
  - name: busybox
    image: busybox:latest
    command: ["sh", "-c", "echo 'hello' > /data/test.txt && sleep 3600"]
    volumeMounts:
    - name: data
      mountPath: /data
  volumes:
  - name: data
    persistentVolumeClaim:
      claimName: pure-test-pvc
EOF
```

### Teardown Flow

```bash
# 1. Delete PVCs and pods (triggers CSI driver filesystem deletion)
kubectl -n osac-system delete pod pure-test-pod
kubectl -n osac-system delete pvc pure-test-pvc

# 2. Remove cluster-side resources
ansible-playbook tests/manual/test_pure_teardown_cluster_storage.yml

# 3. Release Realm back to pool
ansible-playbook tests/manual/test_pure_teardown_backend.yml
```

**Important:** Always delete pods before PVCs. PVCs stuck in `Terminating` state are waiting for pods to be deleted due to the `kubernetes.io/pvc-protection` finalizer.

---

## Verification

### Check Realm Pool

```bash
kubectl get configmap pure-realm-pool -n osac-system -o jsonpath='{.data.PURE_REALM_POOL}' | jq
```

### Check Realm Credential Secret

```bash
kubectl get secret pure-realm-ps-realm -n osac-system -o yaml
```

### Check Tenant Allocation

```bash
# Hub Secret (after setup)
kubectl get secret pure-tenant-config-test-pure-tenant-be-pure-test -n osac-system -o yaml

# StorageClasses (after ensure_sc)
kubectl get storageclass -l osac.openshift.io/tenant=test-pure-tenant

# CSI Secret (after ensure_sc)
kubectl get secret pure-csi-secret-be-pure-test -n osac-system
```

### Verify PVC Provisioning

```bash
# Check PVC status
kubectl -n osac-system get pvc pure-test-pvc

# Check PV details
kubectl get pv

# Check CSI driver logs
kubectl -n osac-csi-backends logs deploy/pure-csi-controller -c pure-csi-driver --tail=50

# Verify data in pod
kubectl -n osac-system exec pure-test-pod -- cat /data/test.txt
kubectl -n osac-system exec pure-test-pod -- df -hT /data
```

---

## Configuration Files

### `realms.yaml`
Defines Realm configuration for provisioning tests. Edit this file with your FlashBlade parameters:

```yaml
pure_realm_name: "danni-test-1"
pure_realm_config:
  api_token: "{{ lookup('env', 'PUREFB_API') }}"
  mgmt_endpoint: "10.3.11.50"
  nfs_endpoint: "10.7.0.11"
```

---

## Clean Up

**Warning:** These operations delete resources. Only run them if you understand the impact.

```bash
# Delete all tenant resources
kubectl delete storageclass -l osac.openshift.io/tenant=test-pure-tenant
kubectl delete secret -n osac-system -l osac.openshift.io/tenant=test-pure-tenant

# Delete Realm pool (affects all tenants!)
# kubectl delete configmap pure-realm-pool -n osac-system

# Delete Realm credential Secret (breaks tenant workflows!)
# kubectl delete secret pure-realm-ps-realm -n osac-system
```

**Note:** Do NOT delete Realms from the FlashBlade array manually. Use proper teardown procedures to release them back to the pool.
