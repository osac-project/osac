# Cluster Fulfillment

Cluster fulfillment is the process of provisioning OpenShift clusters on-demand
for tenants. OSAC uses a template-based approach that enables Cloud Service
Providers (CSPs) to offer standardized cluster configurations while maintaining
flexibility to customize the underlying infrastructure and deployment process.

## Overview

Cluster fulfillment in OSAC leverages Red Hat Advanced Cluster Management
(RHACM) to deploy OpenShift clusters using the Hosted Control Planes
architecture. This approach allows control plane components to run as pods on a
Management Cluster while worker nodes can run on bare metal servers or virtual
machines. By "hosting" control planes as pods on a management cluster, more
control planes can run on a given set of hardware, achiving better density and
resource utilization.

The cluster fulfillment workflow follows the general pattern described in the
[main architecture document](README.md#workflow), with specific implementations
for cluster provisioning:

1. A cluster request is submitted to the Fulfillment Service API
2. The Fulfillment Service schedules the request to a Management Cluster
3. The OSAC Controller on the Management Cluster processes the request and launches AAP job templates
4. Ansible Automation Platform executes the cluster provisioning workflow using template-based automation
5. Status is continuously synchronized back through the controller to the Fulfillment Service

## Components

### Fulfillment Service - Cluster API

The Fulfillment Service provides gRPC and REST APIs for managing cluster
lifecycle operations:

**Private API Operations** (`proto/private/osac/private/v1/clusters_service.proto`):
- `Create`: Request a new cluster deployment
- `Get`: Retrieve cluster details and status
- `List`: List all clusters for a tenant
- `Update`: Modify cluster configuration
- `Delete`: Request cluster deletion
- `Signal`: Indicate that something changed and may require reconciliation (gRPC only, no HTTP endpoint)

**Cluster Request Model** (`proto/private/osac/private/v1/cluster_type.proto`):

A cluster request includes:
- `template` or `catalog_item`: The provisioning template or published catalog offering
- `template_parameters`: A map of parameters specific to the selected template
- `node_sets`: A required effective map of worker groups, keyed by node set identifier, each containing:
  - `baremetal_instance_type`: Reference to a BareMetalInstanceType hardware profile
  - `size`: Positive number of nodes in the set

The caller supplies node sets directly or a catalog item supplies the whole map through a locked or
editable default `fields.node_sets` policy. Templates never supply hardware selections. Fulfillment
validates and canonicalizes each effective reference in the cluster's scope after catalog policy
application; an absent map or invalid type fails before provisioning. Catalog policy hardware
references are validated in the catalog item's own scope. A concrete network attachment requires
each selected type to have a fabric port.

**Public API Operations** (`proto/public/osac/public/v1/clusters_service.proto`):

The public API exposes tenant-facing operations at `/api/fulfillment/v1/clusters`:
- `List`: List clusters visible to the current tenant
- `Get`: Retrieve cluster details and status
- `Create`: Create a new cluster
- `Update`: Modify cluster configuration (partial updates via `FieldMask`)
- `Delete`: Delete a cluster
- `GetKubeconfig` / `GetKubeconfigViaHttp`: Retrieve the admin kubeconfig for a provisioned cluster (gRPC returns a string field; HTTP variant at `/api/fulfillment/v1/clusters/{id}/kubeconfig` returns YAML directly with `application/yaml` content type)
- `GetPassword` / `GetPasswordViaHttp`: Retrieve the admin password for a provisioned cluster (gRPC returns a string field; HTTP variant at `/api/fulfillment/v1/clusters/{id}/password` returns plain text)

The public API uses CEL-based filtering for List operations and supports optimistic locking on Update via the `lock` field.

**Cluster Status**:

The cluster status provides real-time information about the deployment:
- `state`: Overall cluster state (PROGRESSING, READY, FAILED)
- `conditions`: Detailed conditions tracking specific aspects of the cluster (PROGRESSING, READY, FAILED, DEGRADED)
- `api_url`: Kubernetes API endpoint for the provisioned cluster
- `console_url`: OpenShift web console URL
- `node_sets`: Current status of node sets (may differ from spec when changes are in progress)
- `hub`: The Management Cluster ID where the cluster is hosted (private API only)

### Management Cluster Scheduling

The Fulfillment Service implements a scheduling algorithm to select an
appropriate Management Cluster for each request
(`fulfillment-service/internal/controllers/cluster/cluster_reconciler_function.go`):

1. **Hub Selection**: Currently uses random selection from available Management Clusters. Future enhancements may consider:
   - Capacity and resource availability
   - Geographic or availability zone preferences
   - Load balancing across hubs
   - Tenant affinity rules

2. **ClusterOrder Creation**: Once a Management Cluster is selected, the Fulfillment Service creates a `ClusterOrder` custom resource in a tenant-specific namespace. This object contains:
   - The selected template ID
   - Template parameters
   - Node requests translated from the resolved node sets: `numberOfNodes` and
     `bareMetal.instanceType` (the selected BareMetalInstanceType name)

The ClusterOrder serves as the bridge between the Fulfillment Service and the
OSAC Controller running on the Management Cluster.

### OSAC Controller - ClusterOrder Processing

The OSAC Controller is a Kubernetes controller running on each Management
Cluster that reconciles ClusterOrder resources
(`osac-operator/internal/controller/clusterorder_controller.go`).

**ClusterOrder Custom Resource** (`osac-operator/api/v1alpha1/clusterorder_types.go`):

The ClusterOrder CRD spec defines:
- `templateID`: Identifies which cluster template to use
- `templateParameters`: JSON-encoded string of template-specific parameters
- `nodeRequests`: Array of node request objects, each with `bareMetal.instanceType` (selected
  BareMetalInstanceType name) and `numberOfNodes` (count)
- `pullSecret`: Credentials for container image repositories (optional, defaults to provider's)
- `sshPublicKey`: SSH public key installed on worker nodes (optional, defaults to provider's)
- `releaseImage`: OCP release image URL controlling the OpenShift version (optional, defaults to template's)
- `network`: Cluster networking configuration with `podCIDR` and `serviceCIDR` (optional)

The ClusterOrder CRD status tracks:
- `phase`: Overall state (Progressing, Failed, Ready, Deleting)
- `conditions`: Detailed conditions (Accepted, Progressing, ControlPlaneAvailable, Available)
- `clusterReference`: References to the created namespace, HostedCluster, ServiceAccount, and RoleBinding
- `jobs`: Chronological history of provision and deprovision operations with job state, timestamps, and error details
- `desiredConfigVersion`: Hash of the current spec used to detect changes that require re-provisioning

**Reconciliation Loop**:

When a ClusterOrder is created or updated, the controller performs these steps:

1. **Status Initialization**: Sets the phase to "Progressing" and initializes status conditions

2. **Infrastructure Preparation**: Creates prerequisite Kubernetes objects:
   - **Namespace**: A dedicated namespace for the cluster (named after the cluster ID)
   - **ServiceAccount**: Identity for cluster-related automation
   - **RoleBindings**: RBAC permissions for the service account to manage cluster resources

3. **AAP Job Launch**: Launches the cluster provisioning workflow template via the AAP REST API:
   - The template receives the complete ClusterOrder specification in `ansible_eda.event.payload` format.
   - AAP runs a playbook that creates or updates all relevant k8s resources, including the HostedCluster and NodePool. See below for details.
   - The operator stores the AAP job ID in ClusterOrder status and polls until the job completes.

4. **HostedCluster Monitoring**: While the AAP job runs, the controller watches for a HostedCluster resource (created by the Ansible automation) and monitors its conditions:
   - Waits for the control plane to become available
   - Monitors that the cluster is not degraded
   - Tracks NodePool size

5. **Status Synchronization**: Continuously updates the ClusterOrder status based on the HostedCluster state, including:
   - Phase transitions (Progressing → Ready or Failed)
   - Condition updates
   - Cluster reference information (namespace, resource names)

6. **Deletion Handling**: When a ClusterOrder is deleted:
   - Launches the cluster deletion workflow template via the AAP REST API
   - Ensures all associated cluster resources are cleaned up
   - Uses finalizers to prevent premature deletion

### Ansible Automation Platform - Cluster Provisioning

AAP orchestrates cluster provisioning when the OSAC operator launches workflow templates.

**Cluster Templates in AAP:**

Templates are named with the configured prefix (for example, `osac-create-hosted-cluster` and `osac-delete-hosted-cluster`). The operator resolves template names from resource type and operation.

When launched, AAP runs the appropriate workflow template for cluster creation or deletion.

**Cluster Creation Playbook** (`osac-aap/playbook_osac_create_hosted_cluster.yml`):

The main cluster creation workflow consists of these phases:

1. **Infrastructure Preparation**:
   - Extracts template information from the ClusterOrder
   - Determines the working namespace
   - Acquires a cluster lock to ensure safe concurrent operations
   - Adds an infrastructure finalizer to the ClusterOrder to track cleanup

2. **Template Execution**:
   - Dynamically includes the selected cluster template's `install` tasks
   - Templates are Ansible roles located at a file path specified in the `osac_template_collections` variable
   - Each template provides a standardized interface while allowing customization of the underlying implementation

3. **Cluster Infrastructure Creation**:
   - Creates or updates a HostedCluster resource
   - Provisions worker nodes according to each `nodeRequests[].bareMetal.instanceType`
   - Configures networking, ingress, and external access

## Cluster Templates

Cluster templates are implemented as Ansible roles that define how clusters are
provisioned. Each template:

- Accepts standardized parameters (defined via role argument validation)
- Can customize cluster configuration, pre-installed software, and infrastructure details
- Implements an `install.yaml` tasks file that performs the provisioning
- Implements a `postinstall.yaml` tasks file that performs tasks such as cluster configuration or software installation
- Implements a `delete.yaml` tasks file that deletes resources associated with the cluster

**Template Metadata**:

Each template defines metadata that helps users understand the template and its
requirements:
- `title`: Human-readable name
- `description`: Explanation of what the template provides
- `parameters`: Provisioning inputs and defaults, without node sets or hardware selections

CSPs can create custom templates to offer differentiated cluster configurations, such as:
- Clusters with specific software pre-installed (monitoring, security tools, etc.)
- Different versions of OpenShift
- Specialized hardware configurations
- Compliance-specific settings

## Cluster Catalog Items

Cluster catalog items are the primary user-facing abstraction for ordering
clusters. A catalog item wraps an underlying cluster template with additional
controls that determine what end users see and can configure.

**Definition** (`proto/private/osac/private/v1/cluster_catalog_item_type.proto`):

A cluster catalog item includes:
- `title`: Human-friendly short description suitable for display in a UI or CLI
- `description`: Longer description in Markdown format
- `template`: Reference to the underlying cluster template ID
- `published`: Whether this item is visible in the public API (only published items appear in public List/Get responses)
- `metadata.tenant` and `metadata.project`: Ownership and visibility scope
- `fields`: Typed locked/editable policies, including a complete `node_sets` map with BMIT references

**Field Policies**:

Each typed policy controls a supported cluster field. A locked node-set map forbids a user-supplied
map; an editable default supplies a complete map only when the caller omits one. There is no merging
with template hardware. See [Catalog Items](../../fulfillment-service/docs/CATALOG_ITEMS.md#list-and-node-set-policies)
for policy semantics and reference scopes.

**APIs:**

Catalog items are managed through both private and public APIs:
- **Private API** (`fulfillment-service/proto/private/osac/private/v1/cluster_catalog_items_service.proto`): Full CRUD operations for admins, including tenant scoping and publication controls
- **Public API** (`fulfillment-service/proto/public/osac/public/v1/cluster_catalog_items_service.proto`): Read-only access for end users (List, Get) filtered to published items

**Relationship to Templates:**

Cluster templates define the infrastructure provisioning logic (Ansible roles and
parameters). Catalog items sit above templates in the
abstraction layer: they reference a template and add field-level access
controls, defaults, and validation that shape the end-user experience. Multiple
catalog items can reference the same underlying template with different field
configurations - for example, one catalog item might expose all parameters for
advanced users while another locks most fields to sensible defaults for
simplified ordering.

## Worker Node Provisioning

Worker nodes for hosted clusters can be provisioned in multiple ways depending
on the CSP's infrastructure:

**Bare Metal Workers**:
When worker nodes are provisioned on bare metal:
1. AAP consumes the selected `bareMetal.instanceType` from each ClusterOrder node request
   to select Agents and NodePools by `osac.openshift.io/instance_type` (value: BMIT name).
   Imported Agents must advertise that same BMIT name; provider-native `resource_class`
   values must be configured to match before import.
2. The template includes tasks to allocate physical servers from inventory
3. Network isolation is applied (L2/L3 networking, VLANs, etc.)
4. Nodes are joined to the hosted control plane

The independent BareMetalPool API still uses `hostSets[].hostType`; only the AAP pool adapter
translates the selected instance type into that field.

**Virtual Machine Workers**:
This workflow is still being created.

## Infrastructure Components

The cluster provisioning workflow integrates with several infrastructure components:

**Hosted Control Planes (Hypershift)**:
- Control plane components (API server, controller manager, scheduler, etcd) run as pods on the Management Cluster
- This architecture enables:
  - Faster cluster provisioning (no need to boot control plane nodes)
  - Lower resource overhead per cluster
  - Easier/faster upgrades and maintenance

**Networking**:
- Each cluster is deployed on an isolated Layer 2 network
- Ingress is configured on the tenant cluster worker nodes using MetalLB (`osac.service.metallb_ingress` role)
- External access is configured to expose the API and console endpoints
- Worker nodes are connected to the appropriate networks based on template configuration

**Storage**:
- At time of writing, OSAC code and default templates have no direct integration with storage providers. But the intent is for CSPs to interact with a storage provider of their choice from their templates
- Templates can be used to specify storage classes for persistent volumes
- Both local (software-defined storage) and remote storage backends are possible
- CSPs will be able to customize storage options per template
- The project will offer a more structured approach to storage integration in the future

**Identity and Access**:
- Each cluster can be configured with an identity provider
- RBAC configurations can be pre-applied by templates
- A default "kubeadmin" user is available for the end user to configure their cluster
- Default roles [are available](https://docs.redhat.com/en/documentation/openshift_container_platform/4.20/html/authentication_and_authorization/using-rbac#default-roles_using-rbac) to delegate cluster administration

## Status Tracking and Reporting

Cluster status flows through multiple levels:

1. **Hypershift HostedCluster Status**: The source of truth for control plane state
   - Tracks control plane availability
   - Reports degraded conditions
   - Provides version information

2. **ClusterOrder Status**: Maintained by the OSAC Controller
   - Aggregates HostedCluster and NodePool status
   - Tracks provisioning phase (Progressing/Ready/Failed/Deleting)
   - Includes detailed conditions for troubleshooting

3. **Fulfillment Service Cluster Status**: Synchronized from ClusterOrder
   - Provides tenant-facing status information
   - Includes API and console URLs when ready
   - Maintains conditions visible through the API

**ClusterOrder Conditions** (Kubernetes CRD):
- `Accepted`: The order has been accepted but work has not yet started
- `Progressing`: An update is in progress
- `ControlPlaneAvailable`: The cluster control plane is ready
- `Available`: The cluster is available

**Fulfillment Service Condition Types** (gRPC/REST API):
- `PROGRESSING`: The cluster is not completely ready yet
- `READY`: The cluster is ready to use
- `FAILED`: The cluster is unusable
- `DEGRADED`: The cluster is degraded (e.g., unable to allocate requested nodes)

## Cluster Deletion

When a cluster deletion is requested:

1. **Fulfillment Service**: Updates the ClusterOrder to trigger deletion
2. **OSAC Controller**: Detects deletion and:
   - Launches the cluster deletion workflow template via the AAP REST API
   - Sets ClusterOrder phase to "Deleting"
3. **Ansible Automation**: Executes the deletion playbook which:
   - Removes the HostedCluster resource
   - Deallocates worker nodes (shuts down VMs or releases bare metal)
   - Cleans up networking and storage resources
   - Removes the cluster namespace
4. **OSAC Controller**: Finalizes ClusterOrder deletion after all resources are cleaned up

## Scalability and Performance

The cluster fulfillment system is designed for scale:

**Management Cluster Capacity**:
- Each Management Cluster can host a large number of hosted control planes
- Sizing depends on worker node density and control plane resource requirements

**Provisioning Throughput**:
- Multiple clusters can be provisioned concurrently on a single Management Cluster
- The cluster lock mechanism prevents resource conflicts
- AAP job polling provides visibility into long-running provisioning operations

**Horizontal Scaling**:
- Additional Management Clusters can be added to increase total capacity
- The Fulfillment Service load balances across available Management Clusters
- Each Management Cluster operates independently

## Customization and Extension

CSPs can customize cluster fulfillment in several ways:

**Custom Templates**: Create new Ansible role-based templates that:
- Pre-install required software or operators
- Apply specific security policies
- Configure clusters for specialized workloads (AI/ML, edge, etc.)
- Integrate with CSP-specific infrastructure

**Infrastructure Adapters**: Customize how worker nodes are provisioned:
- Integrate with different inventory systems
- Adapt to proprietary bare metal provisioning systems

**Workflow Extensions**: Add pre- or post-provisioning steps:
- Backup and disaster recovery setup
- Monitoring and observability configuration
- Compliance scanning and hardening

**Template Parameters**: Expose configuration options to end users:
- OpenShift version selection
- Add-on operator selection
- Resource quota settings
- Backup and retention policies

By leveraging the flexibility of Ansible and the declarative nature of
Kubernetes operators, OSAC's cluster fulfillment provides a powerful foundation
for CSPs to deliver OpenShift clusters as a service.
