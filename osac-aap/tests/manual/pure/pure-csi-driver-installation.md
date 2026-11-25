# Pure CSI Driver Installation Guide

Standalone Pure CSI (Portworx `px-pure-csi-driver` 26.2.2) installation for local kind cluster.

**OSAC meta-driver is NOT used.** No `osac-csi-driver` (meta) controller, no
`csi.osac.openshift.io` CSIDriver. The Pure controller runs with the
provisioner/attacher sidecars the meta-driver would otherwise host.

This guide covers CSI driver installation only. For StorageClass and Secret creation per tenant, see the AAP playbooks in [`README.md`](README.md).

## Environment

- kind cluster `osac-test`, node `osac-test-control-plane` (k8s v1.35.0,
  containerd 2.2.0, Debian 12), no taints.

## Manifests

The installation manifests are in `pure-csi-manifests/`:

| File | Contents |
|---|---|
| `00-namespaces.yaml` | `osac-csi`, `osac-csi-backends` |
| `05-purevolumes-crd.yaml` | `purevolumes.storage.purestorage.com` (Namespaced, v1alpha1, preserve-unknown-fields) |
| `06-storagenodeinitiators-crd.yaml` | `storagenodeinitiators.storage.purestorage.com` (**Cluster-scoped**, preserve-unknown-fields) |
| `20-controller.yaml` | SA/ClusterRole/Binding + `pure-csi-controller` Deployment: driver (`--plugin=controller --mode=standalone`, unix socket) + `csi-provisioner` + `csi-attacher` |
| `30-node.yaml` | SA/ClusterRole/Binding + ConfigMaps (`multipath`, `mountshim`) + `pure-csi-node` DaemonSet: driver (`--plugin=node --mode=standalone`) + `csi-node-driver-registrar` |
| `40-csidriver.yaml` | CSIDriver `pxd.portworx.com` registration |

## Installation Steps

```bash
cd tests/manual/pure-csi-manifests

# 1. Create namespaces
kubectl apply -f 00-namespaces.yaml

# 2. Apply CRDs
kubectl apply -f 05-purevolumes-crd.yaml -f 06-storagenodeinitiators-crd.yaml

# 3. Deploy controller
kubectl apply -f 20-controller.yaml

# 4. Deploy node DaemonSet
kubectl apply -f 30-node.yaml

# 5. Register CSIDriver
kubectl apply -f 40-csidriver.yaml
```

## Node Prerequisites (kind clusters only)

For kind nodes, multipath-tools may need to be installed (only needed if `PURE_FLASHARRAY_SAN_TYPE=ISCSI` is set, which is NOT the case for FlashBlade NFS):

```bash
kubectl -n osac-csi run nodeinstall --rm -i --restart=Never \
  --image=debian:bookworm-slim --overrides='{"spec":{"containers":[{"name":"c",
  "image":"debian:bookworm-slim","command":["chroot","/host","/bin/bash","-c",
  "apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq multipath-tools"],
  "volumeMounts":[{"name":"h","mountPath":"/host"}],"securityContext":{"privileged":true}}],
  "volumes":[{"name":"h","hostPath":{"path":"/"}}]}}'
```

The node should already have util-linux `mount` and `mount.nfs` (nfs-common).

## Verification

After installation, verify the driver is running:

```bash
# Check CSI driver registration
kubectl get csidriver | grep pxd.portworx.com

# Check controller pods
kubectl -n osac-csi-backends get pods

# Check node pods
kubectl -n osac-csi get pods

# Verify CSI driver is registered on nodes
kubectl get csinode <node-name> -o jsonpath='{.spec.drivers[*].name}'
```
