# VMaaS Dedicated Cluster

The OSAC operator can manage KubeVirt VMs on a **remote cluster** that is
separate from the cluster where the operator itself runs. This allows all VMs
to be deployed to a dedicated VMaaS cluster while the operator, fulfillment
service, and AAP remain on the management cluster.

## Motivation

This feature was originally designed for the
[Enclave](https://github.com/rh-ecosystem-edge/enclave) use case, where the
control plane (fulfillment service, osac-operator, and AAP) and the VMaaS
cluster are managed by different personas.

## Architecture

| Controller | Management cluster | VMaaS cluster (remote) |
|---|---|---|
| ComputeInstance controller | reconciles ComputeInstance CRs | watch KubeVirt VirtualMachine/VirtualMachineInstance |
| Tenant controller | reconciles Tenant CRs | manage Tenant namespace, UDN resources |
| AAP | executes playbooks | manage KubeVirt VirtualMachine/VirtualMachineInstance |
| Console Proxy | -- | connects to KubeVirt VM subresources (console, VNC) via WebSocket |

All OSAC custom resources remain on the management cluster. The
`ComputeInstance` and `Tenant` controllers watch the downstream Kubernetes
objects (VMs, namespaces, UDN) on the remote cluster via a dedicated
kubeconfig.

osac-operator relies on
[multicluster-runtime](https://github.com/kubernetes-sigs/multicluster-runtime)
to watch and manage resources across both clusters. AAP relies on the
standard [kubernetes.core](https://github.com/ansible-collections/kubernetes.core)
Ansible collection, passing a kubeconfig to each task to target the remote
cluster.

The `cluster` controller is incompatible with the remote cluster option
(see [Constraints](#constraints)).

## Configuration

### osac-operator

The operator accepts a kubeconfig path that points to the remote cluster. When
set, the `ComputeInstance` and `Tenant` controllers target the remote cluster
instead of the local one.

| Flag | Environment variable | Default | Description |
|---|---|---|---|
| `--remote-cluster-kubeconfig` | `OSAC_REMOTE_CLUSTER_KUBECONFIG` | _(unset)_ | Path to a kubeconfig file for the remote VMaaS cluster. When unset the operator behaves in single-cluster mode. |
| `--enable-tenant-controller` | `OSAC_ENABLE_TENANT_CONTROLLER` | `false` | Enable the Tenant controller. |
| `--enable-compute-instance-controller` | `OSAC_ENABLE_COMPUTE_INSTANCE_CONTROLLER` | `false` | Enable the ComputeInstance controller. |

> **Note:** When no `--enable-*-controller` flag is set, the operator enables
> all controllers by default. When using a remote cluster, enable only the
> compatible controllers (`tenant`, `compute-instance`, and/or `networking`)
> explicitly to avoid also enabling the `cluster` controller, which is
> incompatible with the remote cluster option.

The kubeconfig is typically provided to the operator pod as a mounted
Kubernetes Secret volume.

#### Example: operator deployment with remote cluster

```yaml
env:
  - name: OSAC_REMOTE_CLUSTER_KUBECONFIG
    value: /etc/osac/remote-cluster-kubeconfig
  - name: OSAC_ENABLE_TENANT_CONTROLLER
    value: "true"
  - name: OSAC_ENABLE_COMPUTE_INSTANCE_CONTROLLER
    value: "true"
volumeMounts:
  - name: remote-cluster-kubeconfig
    mountPath: /etc/osac
    readOnly: true
volumes:
  - name: remote-cluster-kubeconfig
    secret:
      secretName: <secret-name>
      items:
        - key: kubeconfig
          path: remote-cluster-kubeconfig
```

### AAP (osac-aap)

The `compute-instance-operations-ig` instance group is configured via two
variables that control whether the Ansible execution environment gets access
to the remote cluster kubeconfig.

| Variable | Environment variable | Default | Description |
|---|---|---|---|
| `remote_cluster_kubeconfig_secret_name` | `REMOTE_CLUSTER_KUBECONFIG_SECRET_NAME` | _(unset)_ | Name of the Kubernetes Secret holding the remote cluster kubeconfig. When unset, no remote cluster is configured for AAP jobs. |
| `remote_cluster_kubeconfig_secret_key` | `REMOTE_CLUSTER_KUBECONFIG_SECRET_KEY` | `kubeconfig` | Key within the Secret that contains the kubeconfig data. |

When `remote_cluster_kubeconfig_secret_name` is set:

1. The Secret is mounted into the `compute-instance-operations-ig` worker
   pod at `/etc/osac`.
2. `OSAC_REMOTE_CLUSTER_KUBECONFIG=/etc/osac/remote-cluster-kubeconfig` is
   injected as an environment variable.
3. The `ocp_virt_vm` Ansible role reads that variable at task start (via
   `get_remote_cluster_kubeconfig.yaml`) and passes `kubeconfig: <path>` to
   every `kubernetes.core.k8s` and `kubernetes.core.k8s_info` task,
   directing all VM operations to the remote cluster.

## Setup

### 1. Create the kubeconfig Secret

On the management cluster, create a Secret containing the remote cluster
kubeconfig:

```bash
kubectl create secret generic <secret-name> \
  --from-file=kubeconfig=/path/to/remote-cluster.kubeconfig \
  -n <osac-namespace>
kubectl label secret <secret-name> \
  osac.openshift.io/remote-cluster-kubeconfig=true \
  -n <osac-namespace>
```

The label is required by the [console proxy](../../README.md#console-proxy),
which discovers the remote kubeconfig by listing labeled Secrets in the
ComputeInstance's namespace at request time. The operator and AAP use a
mounted file path instead and do not need the label.

### 2. Configure the operator

Set `OSAC_REMOTE_CLUSTER_KUBECONFIG` and mount the Secret (see example
above). Enable only the compatible controllers (`tenant`, `compute-instance`,
and/or `networking`). The `cluster` controller, if needed, is incompatible with the
remote kubeconfig and must run in a separate operator instance without it.

### 3. Configure AAP

Pass `REMOTE_CLUSTER_KUBECONFIG_SECRET_NAME=<secret-name>` to the
config-as-code job (or set it in the relevant config vars). The Secret must
be readable by the `osac-sa` service account in the AAP namespace.

## References

- [MGMT-23102](https://redhat.atlassian.net/browse/MGMT-23102) — Add the
  ability to manage VMs remotely
- [osac-project/osac-operator#119](https://github.com/osac-project/osac-operator/pull/119)
  — Multicluster support and controller refactor
- [osac-project/osac-aap#209](https://github.com/osac-project/osac-aap/pull/209)
  — Add optional keys to specify a remote cluster
- [osac-project/osac-aap#213](https://github.com/osac-project/osac-aap/pull/213)
  — Re-vendor to get compute instance remote cluster support

## Constraints

- `--remote-cluster-kubeconfig` is compatible with the `tenant`,
  `compute-instance`, and `networking` controllers. The operator will exit
  with an error if the `cluster` controller is enabled alongside it. Run
  the `cluster` controller in a separate operator instance without the
  remote kubeconfig.
- When no remote kubeconfig is configured, the operator operates in
  single-cluster mode with no behaviour change.
