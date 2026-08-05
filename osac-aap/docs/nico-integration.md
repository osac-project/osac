# OSAC NICo Integration

## Overview

The OSAC NICo integration enables cluster-as-a-service provisioning on bare metal infrastructure managed by NVIDIA NICo (Bare Metal Manager). NICo is a pluggable backend — no separate template is required. Existing cluster templates work with NICo by setting `NETWORK_STEPS_COLLECTION=nico.steps` in the AAP environment.

The integration uses the pluggable backend architecture via the `network_steps_collection` variable, which routes `cluster_infra` and `external_access` operations to the NICo backend collection (`nico.steps`).

## Environment Variables

All NICo configuration is provided via environment variables injected into AAP from the `cluster-fulfillment-ig` Kubernetes Secret.

### Required

| Variable | Description |
|----------|-------------|
| `NETWORK_STEPS_COLLECTION` | Set to `nico.steps` to select the NICo backend |
| `NVIDIA_BMM_API_URL` | NICo API endpoint (e.g., `https://nico.example.com`) |
| `NVIDIA_BMM_CLIENT_ID` | OAuth2 client ID for authentication |
| `NVIDIA_BMM_CLIENT_SECRET` | OAuth2 client secret for authentication |
| `NVIDIA_BMM_SSA_TOKEN_URL` | OAuth2 token exchange endpoint (must use HTTPS) |
| `NVIDIA_BMM_ORG` | Organization identifier |
| `NVIDIA_BMM_SITE_ID` | Site identifier (datacenter location) |
| `NVIDIA_BMM_TENANT_ID` | Tenant identifier for multi-tenancy scoping |
| `NVIDIA_BMM_MGMT_VPC_ID` | Management cluster VPC ID for VPC peering |
| `NVIDIA_BMM_INGRESS_CIDR` | CIDR range for ingress IP allocation (e.g., `10.0.100.0/24`) |

### Optional

| Variable | Default | Description |
|----------|---------|-------------|
| `NVIDIA_BMM_DEFAULT_IP_BLOCK_ID` | `""` | Default IP block for VPC prefix allocation |
| `NVIDIA_BMM_DEFAULT_SSH_KEY_GROUP_ID` | `""` | Default SSH key group for instance access |
| `NVIDIA_BMM_DEFAULT_OS_ID` | `""` | Default operating system ID for instances |
| `NVIDIA_BMM_DEFAULT_API_PATH_PREFIX` | `forge` | API path prefix |
| `NVIDIA_BMM_DEFAULT_OAUTH_SCOPE` | `""` | OAuth2 scope |
| `NVIDIA_BMM_IPXE_DNS_SERVER` | `""` | DNS server to inject into iPXE boot script |
| `NVIDIA_BMM_CREATE_TIMEOUT` | `600` | Instance creation wait timeout in seconds |
| `NVIDIA_BMM_DELETE_TIMEOUT` | `180` | Instance deletion wait timeout in seconds |
| `NVIDIA_BMM_VALIDATE_CERTS` | `false` | TLS certificate validation for iPXE script fetch |

### Example Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cluster-fulfillment-ig
  namespace: aap
type: Opaque
stringData:
  NETWORK_STEPS_COLLECTION: "nico.steps"
  NVIDIA_BMM_API_URL: "https://nico.example.com"
  NVIDIA_BMM_CLIENT_ID: "my-client-id"
  NVIDIA_BMM_CLIENT_SECRET: "my-client-secret"
  NVIDIA_BMM_SSA_TOKEN_URL: "https://auth.example.com/oauth/token"
  NVIDIA_BMM_ORG: "org-id-here"
  NVIDIA_BMM_SITE_ID: "site-uuid-here"
  NVIDIA_BMM_TENANT_ID: "tenant-uuid-here"
  NVIDIA_BMM_MGMT_VPC_ID: "mgmt-vpc-uuid-here"
  NVIDIA_BMM_INGRESS_CIDR: "10.0.100.0/24"
  NVIDIA_BMM_DEFAULT_IP_BLOCK_ID: "ip-block-uuid-here"
  NVIDIA_BMM_DEFAULT_SSH_KEY_GROUP_ID: "ssh-key-group-uuid-here"
  NVIDIA_BMM_DEFAULT_OAUTH_SCOPE: "my-scope"
```

## Template Parameters

ClusterOrder resources provide these in `spec.templateParameters`. The same template works for both ESI and NICo — the backend is selected by the environment, not the template.

| Parameter | Required | Description |
|-----------|----------|-------------|
| `pull_secret` | Yes | OpenShift pull secret (JSON object, not a string) |
| `ssh_public_key` | Yes | SSH public key for cluster node access |
| `ip_block_id` | Yes | NICo IP block ID for VPC prefix allocation |
| `ocp_release_image` | No | Override OCP release image |
| `vpc_id` | No | Use an existing VPC instead of creating a new one |
| `vpc_prefix_id` | No | Use an existing VPC prefix instead of creating a new one |
| `ssh_key_group_id` | No | Override SSH key group for this cluster |

### Prerequisites

Before submitting a ClusterOrder, ensure:

1. `NETWORK_STEPS_COLLECTION=nico.steps` is set in the AAP environment
2. A **Tenant CR** exists with the `osac.openshift.io/bgp-asn` annotation set to the local BGP ASN
3. The ClusterOrder has the `osac.openshift.io/tenant` annotation pointing to the Tenant name
4. An **InfraEnv** resource named `hardware-inventory` exists in the `hardware-inventory` namespace

### Example ClusterOrder

```yaml
apiVersion: osac.openshift.io/v1alpha1
kind: ClusterOrder
metadata:
  name: my-cluster
  namespace: osac-devel
  annotations:
    osac.openshift.io/tenant: "osac-devel"
spec:
  templateName: ocp_4_20_small_nico
  nodeRequests:
    - resourceClass: my-instance-type
      numberOfNodes: 2
  templateParameters:
    pull_secret:
      auths:
        quay.io:
          auth: "<base64-encoded-credentials>"
          email: "user@example.com"
        registry.redhat.io:
          auth: "<base64-encoded-credentials>"
          email: "user@example.com"
    ssh_public_key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI..."
    ip_block_id: "ip-block-uuid-here"
```

Note: `resourceClass` values must match NICo instance type names at the configured site exactly. No manual mapping is needed.

## Cluster Lifecycle Flows

### Cluster Creation

```
ClusterOrder submitted
  |
  v
1. Template creates HostedCluster and NodePools
   - Creates HyperShift HostedCluster with OCP release image
   - Creates NodePool per resource class with agent label selectors
  |
  v
2. Create cluster infrastructure (nico.steps.cluster_infra)
   a. Validate credentials and HTTPS token URL
   b. Authenticate with NICo API (OAuth2 client credentials -> JWT)
   c. Snapshot pre-existing agents (for scale-down protection)
   d. Set NICo infrastructure variables from cluster context
   e. Create/reuse VPC (osac-vpc-{cluster_name})
   f. Create/reuse VPC prefix (prefix-{cluster_name})
   g. Peer cluster VPC with management VPC (skipped if same VPC)
   h. Write ConfigMap with VPC/prefix state (crash safety)
   i. Provision instances per resource class:
      - Resolve instance type by name via NICo API
      - Count existing instances via NICo API (excludes Terminating)
      - Only create the delta needed
      - Each instance gets a UUID-based unique name
      - Boot via iPXE from InfraEnv
   j. Update ConfigMap with instance IDs
   k. Wait for agents to register (MAC address matching)
   l. Label agents with cluster, resource class, and nico-instance-id
   m. Select, label, and approve agents for NodePool attachment
  |
  v
3. Configure external access (nico.steps.external_access)
   a. Wait for kube-apiserver LoadBalancer IP
   b. Allocate ingress IP from CIDR range (tracked in nico-ip-registry ConfigMap)
   c. Create DNS records (api, api-int, *.apps) via Route53
   d. Wait for DNS propagation
   e. Retrieve admin kubeconfig from HostedCluster
   f. Wait for cluster readiness
   g. Look up tenant BGP ASN from ClusterOrder/Tenant CR annotations
   h. Compute expected worker count
   i. Wait for all workers to have OVN gateway annotation
   j. Discover BGP parameters per worker:
      - Gateway IP from k8s.ovn.org/l3-gateway-config annotation
      - Peer ASN from NICo metadata service (169.254.169.254:7777)
   k. Install MetalLB operator and create IPAddressPool
   l. Create BGPAdvertisement and BGPPeer per worker
   m. Create external-ingress LoadBalancer Service
  |
  v
4. Retrieve and store kubeconfig
```

### Cluster Scale-Up (adding nodes)

```
ClusterOrder updated with higher numberOfNodes
  |
  v
1. Template updates NodePool replicas
  |
  v
2. cluster_infra runs:
   a. Authenticate with NICo API
   b. Snapshot pre-existing agents
   c. VPC/prefix/peering already exist (idempotent)
   d. Read existing ConfigMap to preserve instance IDs
   e. Count existing instances via NICo API
   f. Create only the NEW instances needed (delta)
   g. Wait for new agents to register
   h. Label and approve new agents
   i. Scale-down block skipped (labeled count <= desired count)
  |
  v
3. external_access runs:
   a. DNS records already exist (idempotent)
   b. Kubeconfig retrieved
   c. Look up tenant BGP ASN
   d. Wait for all worker Nodes to have OVN annotations
   e. Discover BGP params for ALL workers (including new ones)
   f. Create BGPPeers for new workers (existing ones are idempotent)
```

### Cluster Scale-Down (removing nodes)

```
ClusterOrder updated with lower numberOfNodes
  |
  v
1. Template updates NodePool replicas
   - HyperShift detaches an agent (removes clusterdeployment-namespace)
  |
  v
2. cluster_infra runs:
   a. Authenticate with NICo API
   b. Snapshot pre-existing agents
   c. VPC/prefix/peering unchanged
   d. Count existing instances, create delta if needed
   e. Compute protected agents (all matched to desired instances)
   f. Scale-down block runs (labeled agents > desired count):
      - Wait for HyperShift to detach excess agent(s)
      - Capture removed agents (without clusterdeployment-namespace,
        NOT in protected list)
      - Extract instance IDs and hostnames from removed agents
      - Detach: remove cluster_order_label (protected agents excluded)
      - Delete removed Agent CRs (NICo agents are ephemeral)
      - Delete removed NICo instances (with fresh token)
      - Update nico-infra ConfigMap with remaining instance IDs
  |
  v
3. external_access runs:
   a. Kubeconfig retrieved
   b. Delete BGPPeers for removed agent hostnames
   c. Look up tenant BGP ASN
   d. Rediscover BGP params for remaining workers
```

### Cluster Deletion

```
ClusterOrder deleted
  |
  v
1. Template deletes HostedCluster
   - Removes HostedCluster, NodePools, and associated resources
  |
  v
2. Delete external access (nico.steps.external_access)
   a. Remove ingress IP from nico-ip-registry ConfigMap
   b. Delete DNS records
  |
  v
3. Delete cluster infrastructure (nico.steps.cluster_infra)
   a. Authenticate with NICo API
   b. List and detach all agents for this cluster
   c. Delete all Agent CRs
   d. Refresh token before deletion
   e. Read nico-infra ConfigMap for instance IDs and VPC state
   f. Delete all instances by ID (3 min timeout per instance)
   g. Delete VPC peering
   h. Delete VPC prefix (only if created by this cluster)
   i. Delete VPC (only if created by this cluster)
   j. Delete nico-infra ConfigMap
```

## State Storage

| ConfigMap | Namespace | Purpose |
|-----------|-----------|---------|
| `nico-infra-{cluster}` | `hardware-inventory` | VPC ID, prefix ID, instance IDs, peering ID, creation flags |
| `nico-ip-registry` | `hardware-inventory` | Ingress IP allocations (IP -> cluster name mapping) |

The `nico-infra` ConfigMap is written immediately after VPC/prefix creation (before instance provisioning) to ensure cleanup can proceed even if instance creation or agent registration fails. Instance IDs are updated in the ConfigMap after provisioning, preserving any existing IDs from prior runs.

## Architecture

### Backend Routing

The NICo backend is selected by setting the `NETWORK_STEPS_COLLECTION` environment variable to `nico.steps`. No template changes are needed. The shared orchestration layer delegates to the backend via:

```yaml
ansible.builtin.include_role:
  name: "{{ network_steps_collection }}.cluster_infra"
  tasks_from: create.yaml
```

This pattern allows multiple backends to coexist (ESI via `osac.steps`, NICo via `nico.steps`, Netris via `netris.steps`).

### Collection Structure

```
nico/steps/roles/
  cluster_infra/
    tasks/
      create.yaml               # Orchestration: auth, snapshot, infra, scale-down, agents
      create_nico_infra.yaml    # VPC, prefix, peering creation + ConfigMap persistence
      create_instances.yaml     # Instance provisioning and agent matching
      create_instances_for_resource_class.yaml  # Per-class instance creation
      delete.yaml               # Orchestration: auth, detach, delete infra
      delete_nico_infra.yaml    # Reverse deletion of all resources
      scale_down_cleanup.yaml   # Token refresh, instance deletion, ConfigMap update
  external_access/
    tasks/
      create.yaml               # DNS, IP allocation, tenant/BGP lookup, MetalLB/BGP
      delete.yaml               # IP deallocation, DNS cleanup
      allocate_ip.yml           # CIDR IP registry management
      discover_bgp_params.yml   # OVN annotation + metadata service BGP discovery
```

### Agent Lifecycle

NICo agents are **ephemeral** — they are created when instances boot and deleted when instances are destroyed. This differs from ESI where agents persist and are reused.

- **Creation**: Instance boots via iPXE -> agent registers -> matched by MAC -> labeled
- **Attachment**: HyperShift NodePool selects agent by label -> sets `clusterdeployment-namespace`
- **Detachment**: NodePool scaled down -> HyperShift removes `clusterdeployment-namespace`
- **Deletion**: Agent CR is deleted after detachment to prevent stale agent conflicts

See [CaaS Agent Binding Labels](agent-labels.md) for the full label
reference and common mistakes to avoid.

### Scale-Down Protection

During scale-down, agents without `clusterdeployment-namespace` could be either:
- **Detached by HyperShift** (should be cleaned up)
- **Newly provisioned** for scale-up (should be kept)

Protection mechanisms:
1. **Matched agent protection**: All agents matched to desired instances are protected from removal
2. **Count guard**: Scale-down capture only runs when labeled agent count exceeds desired node count
3. **NodePool existence check**: Scale-down block only runs when NodePools already exist (skipped during initial creation)

## Troubleshooting

### Instance Type Not Found
```
NICo instance type 'X' not found at site 'Y'
```
The `resourceClass` in `nodeRequests` must exactly match a NICo instance type name at the configured site.

### Agent Registration Timeout
Agents not registering after instance boot:
- Check InfraEnv exists: `oc get infraenv -n hardware-inventory`
- Verify iPXE script URL is accessible from the instance network
- Check DNS if `NVIDIA_BMM_IPXE_DNS_SERVER` is set
- Review instance status in NICo UI

### BGP Discovery Failure
- Gateway is from node annotation `k8s.ovn.org/l3-gateway-config` — the backend waits for all workers to have this annotation before proceeding
- ASN is from NICo metadata service — test: `oc debug node/<name> -- chroot /host curl -s http://169.254.169.254:7777/latest/meta-data/asn`
- Ensure `oc` CLI is available in the execution environment

### 401 Unauthorized on Instance Deletion
The NICo API token expired during a long operation. Both `scale_down_cleanup.yaml` and `delete_nico_infra.yaml` automatically re-exchange credentials for a fresh token before deleting instances.

### Duplicate Instances on Re-run
Instance count is verified against the NICo API (not agent count) before creating new instances. Terminating instances are excluded from the count. If instances were created but agents never registered, re-runs will not create duplicates.

### Debugging Commands

```bash
# View infrastructure state
oc get cm -n hardware-inventory nico-infra-<cluster> -o yaml

# Check IP allocations
oc get cm -n hardware-inventory nico-ip-registry -o yaml

# List agents for a cluster
oc get agent -n hardware-inventory -l osac.openshift.io/resource_class -o wide

# Check agent binding status
oc get agent -n hardware-inventory <agent-name> -o json | \
  jq '{bound: .status.conditions[] | select(.type=="Bound"), clusterdeployment: .metadata.labels["agent-install.openshift.io/clusterdeployment-namespace"]}'

# Check NodePool status
oc get nodepool -A

# Check MetalLB in hosted cluster
oc --kubeconfig=<kubeconfig> get ipaddresspool,bgpadvertisement,bgppeer -n metallb-system

# Check ingress service
oc --kubeconfig=<kubeconfig> get svc -n openshift-ingress external-ingress

# List NICo instances for a cluster (via API)
# Instances are labeled with cluster=<name> and resource_class=<class>
```
