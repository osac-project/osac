# CaaS over BMaaS Demo Runbook

This runbook demonstrates creating an OpenShift cluster whose worker is
provisioned as a BareMetalInstance (BMI), watching that worker join the
cluster, and retrieving a kubeconfig for the finished cluster.

The provider backend is assumed to have one or more available physical hosts.
Those hosts do **not** need pre-existing OSAC BMI resources: the CaaS worker
controller creates the BMI after the cluster request is submitted.

## What must already be in place

- The OSAC CLI is installed and logged in as a user allowed to create clusters
  in the selected tenant.
- A cluster template and an active ClusterVersion are visible. The chosen
  ClusterVersion must reference a usable RHCOS disk image; the CaaS worker
  controller waits for this image before creating BMIs.
- A BareMetalInstanceType (BMIT) is visible to the cluster's scope, and its
  backend selector matches at least one available host.
- A pull-secret file is available locally. The selected template/catalog item
  can provide the pull secret, or it can be supplied using the commands below.
- A management-cluster kubeconfig is available for the inspection portion of
  the demo.
- For Metal3-backed inventory inspection, the demo identity can read the
  relevant BareMetalHost (BMH) namespace.

Set values for the demo. `CLUSTER_VERSION` should be the ClusterVersion's
`metadata.name` (or its version string), and `BMIT` should be the matching
BareMetalInstanceType name.

```bash
export OSAC_ADDRESS='https://<fulfillment-api-address>'
export TENANT='<tenant-name>'
export CLUSTER_NAME="bm-demo-$(date -u +%H%M%S)"
export CLUSTER_TEMPLATE='ocp-ci-small'
export CLUSTER_VERSION='<active-version-with-rhcos-disk-image>'
export BMIT='<baremetal-instance-type-name>'
export PULL_SECRET_FILE='<path-to-pull-secret.json>'
export SSH_PUBLIC_KEY_FILE="$HOME/.ssh/id_ed25519.pub"
export HUB_KUBECONFIG='<path-to-management-cluster-kubeconfig>'
```

For the catalog-item path only, also set:

```bash
export CLUSTER_CATALOG_ITEM='<published-cluster-catalog-item>'
export PULL_SECRET_NAME='demo-pull-secret'
```

Authenticate and select the tenant if the CLI is not already configured:

```bash
osac login "$OSAC_ADDRESS" --flow device
osac tenant "$TENANT"
osac whoami
```

If the API uses a private CA, add `--ca-file <ca-bundle.pem>` to `osac login`.

Check the values before creating the cluster:

```bash
osac get clustertemplates "$CLUSTER_TEMPLATE" -o json \
  | jq '{name: .metadata.name,
         spec_defaults: {version: .spec_defaults.version},
         parameters: [.parameters[]? | {name, title, required, type}]}'
osac get clusterversions -o yaml
osac get baremetalinstancetypes -o yaml
```

For a catalog-item run, inspect its policies too:

```bash
osac get clustercatalogitems --filter 'this.published'
osac get clustercatalogitems "$CLUSTER_CATALOG_ITEM" -o json \
  | jq '{name: .metadata.name,
         published: .published,
         fields: {version: .fields.version,
                  node_sets: .fields.node_sets,
                  pull_secret_secret: .fields.pull_secret_secret,
                  ssh_public_key: .fields.ssh_public_key}}'
```

Confirm the selected version's `spec.disk_image` is set and that the BMIT
matches available backend inventory. These projections omit template-parameter
values, which may contain pull-secret data. Do not create a BMI manually; that
is the behavior this demo is showing.

## 1. Commands to provision the cluster

Choose **one** of the following create paths. Both request one worker in the
`workers` node set.

### A. Create directly from the `ocp-ci-small` template

This follows the branch's CaaS E2E path. The CLI currently warns that
`--template` is deprecated in favor of `--catalog-item`; this path remains
supported and is useful when the demo needs to choose the BMIT directly.

```bash
osac create cluster \
  --name "$CLUSTER_NAME" \
  --template "$CLUSTER_TEMPLATE" \
  --version "$CLUSTER_VERSION" \
  --node-set "name=workers,size=1,baremetal-instance-type=$BMIT" \
  --template-parameter-file "pull_secret=$PULL_SECRET_FILE" \
  --template-parameter-file "ssh_public_key=$SSH_PUBLIC_KEY_FILE"
```

The `--template-parameter-file` flags read the pull-secret file and SSH public
key without putting their contents on the command line. `--version` explicitly
selects the active ClusterVersion that has the RHCOS disk image.

### B. Create from a published Cluster Catalog Item

Use this path when the provider has published an offering whose `fields.node_sets`
policy allows the requested node-set configuration. If the node-set map is
locked, the catalog item must already specify the desired BMIT and worker count;
omit `--node-set` in that case.

The catalog-item CLI path does not accept `--template-parameter-file`. If the
catalog item/template does not provide a default pull secret, create an OSAC
pull-secret from the local file once. Choose a name that is not already in use:

```bash
osac create secret \
  --name "$PULL_SECRET_NAME" \
  --type pull-secret \
  --from-file=.dockerconfigjson="$PULL_SECRET_FILE"
```

Then create the cluster:

```bash
osac create cluster \
  --name "$CLUSTER_NAME" \
  --catalog-item "$CLUSTER_CATALOG_ITEM" \
  --node-set "name=workers,size=1,baremetal-instance-type=$BMIT" \
  --pull-secret "$PULL_SECRET_NAME" \
  --ssh-public-key-file "$SSH_PUBLIC_KEY_FILE"
```

Only include `--pull-secret` when the catalog item's pull-secret policy allows
the override; otherwise rely on its configured default. Likewise, add
`--version "$CLUSTER_VERSION"` only if the catalog item's version policy is
editable or absent. If the version is locked, use the catalog's version and
confirm that it references the required disk image. Supply
`--ssh-public-key-file` only if the catalog item's SSH-key policy allows the
override; otherwise rely on the offering's configured default or omit it.

### Wait for readiness and retrieve the cluster kubeconfig

Watch the tenant-facing cluster state. Stop the watch once the state is
`CLUSTER_STATE_READY`:

```bash
osac get cluster "$CLUSTER_NAME" --watch
```

Retrieve the kubeconfig from the secret referenced by the cluster. The restrictive
umask keeps the kubeconfig private on disk:

```bash
umask 077
KUBECONFIG_SECRET=$(osac get cluster "$CLUSTER_NAME" -o json \
  | jq -er '.status.kubeconfig_secret.name')
osac get secret "$KUBECONFIG_SECRET" -o json \
  | jq -er '.data.kubeconfig' \
  | base64 --decode > "${CLUSTER_NAME}.kubeconfig"
chmod 600 "${CLUSTER_NAME}.kubeconfig"
```

Use the new kubeconfig to verify that the worker is available:

```bash
oc --kubeconfig "${CLUSTER_NAME}.kubeconfig" get nodes
```

## 2. What to inspect and explain during the demo

Use the management-cluster kubeconfig for these commands; it is separate from
the target cluster kubeconfig retrieved above. The ClusterOrder namespace is
deployment-configured, so discover the order across namespaces by its cluster
UUID label.

### A. Cluster request becomes a ClusterOrder

Get the cluster ID, then find the ClusterOrder created by fulfillment-service:

```bash
CLUSTER_ID=$(osac get cluster "$CLUSTER_NAME" -o json | jq -er '.id')
CO_SELECTOR="osac.openshift.io/clusterorder-uuid=$CLUSTER_ID"
CO_NAME=$(kubectl --kubeconfig "$HUB_KUBECONFIG" get clusterorders -A \
  -l "$CO_SELECTOR" -o jsonpath='{.items[0].metadata.name}')
CO_NAMESPACE=$(kubectl --kubeconfig "$HUB_KUBECONFIG" get clusterorders -A \
  -l "$CO_SELECTOR" -o jsonpath='{.items[0].metadata.namespace}')

show_clusterorder() {
  kubectl --kubeconfig "$HUB_KUBECONFIG" get clusterorder "$CO_NAME" \
    -n "$CO_NAMESPACE" -o json \
    | jq '{name: .metadata.name,
           templateID: .spec.templateID,
           nodeRequests: [(.spec.nodeRequests // [])[]
             | {resourceClass: .resourceClass,
                numberOfNodes: .numberOfNodes,
                bareMetal: .bareMetal}],
           phase: .status.phase,
           conditions: [(.status.conditions // [])[]
             | {type, status, reason, message}],
           desiredWorkers: .status.desiredWorkers,
           currentWorkers: .status.currentWorkers,
           readyWorkers: .status.readyWorkers,
           workers: [(.status.workers // [])[]
             | {name, nodeSet, instanceType, phase}],
           clusterReference: .status.clusterReference}'
}

show_clusterorder
```

**Explain:** the CLI creates a tenant-facing Cluster. Fulfillment-service
projects that request into a ClusterOrder. Its `spec.nodeRequests` carries the
worker count and BMIT; the OSAC operator reconciles the order on the management
cluster. The summary intentionally omits `spec.pullSecret` and
`spec.templateParameters`, which can contain credentials; avoid showing the
unfiltered ClusterOrder YAML during the demo.

### B. The operator prepares assisted discovery

```bash
kubectl --kubeconfig "$HUB_KUBECONFIG" \
  get infraenvs.agent-install.openshift.io "$CO_NAME-infraenv" \
  -n "$CO_NAMESPACE" -o json \
  | jq '{name: .metadata.name,
         discoveryIgnitionReady: (.status.bootArtifacts.discoveryIgnitionURL != null)}'
show_clusterorder | jq '(.conditions // []) | map(select(.type == "InfraEnvReady"))'
```

**Explain:** the bare-metal worker controller creates a cluster-specific
InfraEnv and waits for its discovery ignition to become available. It resolves
the RHCOS disk image through the selected ClusterVersion. It does not create a
BMI until both the ignition and disk image are ready.

Once `InfraEnvReady` is `True` and `discoveryIgnitionReady` is `true`, discovery
ignition has been fetched and the controller can create the BMI. The command
prints only whether the ignition URL is present; do not display or copy the
ignition payload or URL.

### C. The BMI is created and the backend provisions a host

On the management cluster, watch the BMI created for this ClusterOrder:

```bash
kubectl --kubeconfig "$HUB_KUBECONFIG" get bmi -A \
  -l "osac.openshift.io/cluster-order=$CO_NAME" -o wide --watch
```

The BMF BMI CR exposes its lifecycle phase in the table. Expect it to move
through `Allocating` and `Progressing` toward `Ready` (or `Failed` if backend
provisioning fails). Stop the watch with Ctrl-C after the transition you want
to show.

If the OSAC CLI identity has platform-wide visibility, the same BMI can be
shown through the system tenant:

```bash
osac --tenant system get baremetalinstances --watch
```

The OSAC CLI object name is `baremetalinstance` / `baremetalinstances`; it does
not define a `bmi` alias. A tenant-scoped identity may not be allowed to see
system-tenant BMIs, so the management-cluster `kubectl get bmi` command is the
reliable inspection path for this resource.

**Explain:** the controller creates one BMI per requested bare-metal worker,
using the shared `osac.templates.bm_host_provisioning` template, the selected BMIT,
the RHCOS disk image, and discovery ignition as BMI user data. The BMF operator matches
the BMIT selector against backend inventory and provisions an available host.
The physical host was available before the demo; its OSAC BMI is created by
this CaaS flow.

For a Metal3-backed deployment, optionally show the corresponding backend
BareMetalHosts and their provisioning states. Use the configured inventory
namespace if you know it; otherwise list across namespaces:

```bash
kubectl --kubeconfig "$HUB_KUBECONFIG" get bmh -A -o wide
```

### D. Agent registration is correlated to the BMI

```bash
kubectl --kubeconfig "$HUB_KUBECONFIG" get agents.agent-install.openshift.io \
  -n "$CO_NAMESPACE" \
  -l "infraenvs.agent-install.openshift.io=$CO_NAME-infraenv" -o wide --watch
```

For details on an Agent, inspect its inventory interfaces, approval/binding
fields, and OSAC worker label using this targeted JSON view:

```bash
kubectl --kubeconfig "$HUB_KUBECONFIG" get agents.agent-install.openshift.io \
  -n "$CO_NAMESPACE" \
  -l "infraenvs.agent-install.openshift.io=$CO_NAME-infraenv" -o json \
  | jq '[.items[] | {name: .metadata.name,
                     workerName: .metadata.labels["osac.openshift.io/worker-name"],
                     approved: .spec.approved,
                     clusterDeploymentName: .spec.clusterDeploymentName,
                     interfaces: [.status.inventory.interfaces[]?
                       | {name, macAddress}]}]'
```

**Explain:** Agents initially register through the InfraEnv without a cluster
binding. The controller matches an Agent's inventory MAC address to the NIC MAC
reported by the BMI, then approves and binds that Agent to the HostedCluster.
The ClusterOrder worker status progresses through `WaitingForAgent`, `Binding`,
and `Ready`.

### E. NodePool and cluster become ready

Get the HostedCluster namespace and name from the ClusterOrder, then inspect
the HostedCluster and its NodePools:

```bash
HC_NAMESPACE=$(kubectl --kubeconfig "$HUB_KUBECONFIG" \
  get clusterorder "$CO_NAME" -n "$CO_NAMESPACE" \
  -o jsonpath='{.status.clusterReference.namespace}')
HC_NAME=$(kubectl --kubeconfig "$HUB_KUBECONFIG" \
  get clusterorder "$CO_NAME" -n "$CO_NAMESPACE" \
  -o jsonpath='{.status.clusterReference.hostedClusterName}')

kubectl --kubeconfig "$HUB_KUBECONFIG" get hostedcluster "$HC_NAME" \
  -n "$HC_NAMESPACE" -o wide
kubectl --kubeconfig "$HUB_KUBECONFIG" get nodepools \
  -n "$HC_NAMESPACE" -l "osac.openshift.io/clusterorder=$CO_NAME" -o wide
```

Recheck the ClusterOrder worker phases and aggregate counts:

```bash
show_clusterorder
```

**Explain:** after the Agent is installed and bound, the operator converges the
NodePool replicas to the requested bare-metal worker count. The ClusterOrder
reports the worker as `Ready`; fulfillment-service then reports the cluster as
`CLUSTER_STATE_READY` and exposes its API URL and kubeconfig-secret reference.
The final `oc get nodes` command demonstrates access through that kubeconfig.

## Suggested demo narration

> “I requested one worker using a BareMetalInstanceType. The ClusterOrder
> controller prepared discovery and then created the BMI automatically. The
> backend matched that request to an available physical host and provisioned
> it. Once the host registered an Agent, OSAC correlated it to the BMI by MAC,
> bound it to the HostedCluster, and waited for the NodePool worker to become
> ready. The cluster is now ready, and this kubeconfig reaches it.”
