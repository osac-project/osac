# Cluster-as-a-Service (CaaS) User Guide

This guide describes how tenants can create, manage, and delete OpenShift clusters through the
OSAC CLI and API.

## Prerequisites

- `osac` installed and authenticated (`osac login`)
- A cluster catalog item published by your provider

## Workflow Overview

1. Browse available cluster catalog items
2. Create a cluster from a catalog item
3. Wait for the cluster to become ready
4. Access the cluster (kubeconfig, console, admin password)
5. Scale nodes as needed
6. Delete the cluster when done

## Browse Cluster Catalog Items

List published catalog items available for provisioning:

```bash
osac get clustercatalogitems --filter 'this.published'
```

Inspect a specific catalog item to see its fields and defaults:

```bash
osac get clustercatalogitems <catalog-item-id> -o yaml
```

Key fields in a catalog item:

- **`title`**: Human-friendly short description of the offering.
- **`description`**: Detailed Markdown description of what the catalog item provides.
- **`fields`**: Typed locked or editable policies for cluster fields, with optional defaults.
- **`template_parameters`**: Typed policies for parameters declared by the referenced Template.

## Manage Cluster Versions

Cluster versions define which OpenShift releases are available for provisioning. Each cluster
version maps a version string (e.g., `4.17.0`) to an OCI release image. Cluster versions are
managed by platform admins through the private API.

Create a cluster version:

```bash
osac create clusterversion \
  --version "4.17.0" \
  --image "quay.io/openshift-release-dev/ocp-release:4.17.0-multi" \
  --name "4-17-0" \
  --default
```

The `--default` flag marks this version as the system default — at most one version can be the
default at a time.

List available versions:

```bash
osac get clusterversions
```

Cluster versions follow a lifecycle: **Active** (available for new clusters), **Deprecated**
(a newer version should be preferred), and **Obsolete** (blocked for new clusters). The `version`
and `image` fields are immutable after creation.

## Create a Cluster

Create a cluster using a catalog item:

```bash
osac create cluster \
  --catalog-item hosted_cluster_offering
```

To specify an OpenShift version explicitly, the catalog's version policy must be editable or absent:

```bash
osac create cluster \
  --catalog-item hosted_cluster_offering \
  --version "4-17-0"
```

If `fields.version` is locked, omit `--version`: the catalog supplies its fixed version and rejects
any explicitly supplied version, including the same one.

Optional flags:

- `-n, --name <name>` - Human-readable name for the cluster
- `--version <version>` - ClusterVersion `metadata.name` or `spec.version` string. For an editable
  or ungoverned version, values are resolved with the following precedence:
  1. Explicit `--version` provided by the user.
  2. Catalog item editable default (`fields.version.editable.default_value`).
  3. Template default (`spec_defaults.version`).
  4. System default ClusterVersion (`is_default = true`).

The command outputs the cluster ID upon successful creation.

## Check Cluster Status

List all your clusters:

```bash
osac get clusters
```

The table shows ID, name, template, state, API URL, and console URL.

Get detailed information about a specific cluster:

```bash
osac describe cluster <cluster-id>
```

Or in full YAML format:

```bash
osac get clusters <cluster-id> -o yaml
```

### Cluster States

| State | Meaning |
|-------|---------|
| `CLUSTER_STATE_PROGRESSING` | Cluster is being created or updated |
| `CLUSTER_STATE_READY` | Cluster is operational and accessible |
| `CLUSTER_STATE_FAILED` | Cluster creation or update failed |

Deletion is indicated by a `deletion_timestamp` in the cluster metadata rather than a separate
state.

### Conditions

A cluster in `READY` state may have additional conditions:

- **`DEGRADED`** = `TRUE`: Cluster is operational but not at full capacity (e.g., some requested
  worker nodes could not be allocated). The control plane is functional.

## Access the Cluster

Once the cluster is in `READY` state, inspect its secret references and retrieve the secrets through
the Secrets API:

**Kubeconfig** (for `oc` / `kubectl`):

```bash
cluster=$(osac get cluster <cluster-id-or-name> -o json)
kubeconfig_secret=$(printf '%s' "$cluster" | jq -r '.status.kubeconfig_secret.name')
osac get secret "$kubeconfig_secret" -o json > kubeconfig-secret.json
jq -r '.data.kubeconfig' kubeconfig-secret.json | base64 --decode > kubeconfig.yaml
export KUBECONFIG=kubeconfig.yaml
oc get nodes
```

**Admin password** (for the web console):

```bash
password_secret=$(printf '%s' "$cluster" | jq -r '.status.password_secret.name')
osac get secret "$password_secret" -o yaml
osac get secret "$password_secret" -o json | jq -r '.data.password' | base64 --decode
```

The console URL is shown in `get clusters` output or in the cluster's `status.console_url` field.

## Scale Nodes

Node set sizes can be changed with either the dedicated `osac scale` command or
the generic `osac edit` command.

### Using osac scale (recommended)

`osac scale cluster` sets the absolute target size of a node set non-interactively:

```bash
osac scale cluster <cluster-id-or-name> --node-set workers --size 3
```

To scale relative to the current size, read the current value first:

```bash
current=$(osac get clusters <cluster-id-or-name> -o yaml | yq '.spec.node_sets.workers.size')
osac scale cluster <cluster-id-or-name> --node-set workers --size $((current + 1))
```

### Using osac edit (interactive)

`osac edit clusters` opens the full cluster resource in your `$EDITOR`. Modify the
`size` field under `spec.node_sets.<node-set-name>` and save:

```bash
osac edit clusters <cluster-id>
```

Use this when you want to review or change multiple fields at once.

### Constraints

- The `host_type` of an existing node set cannot be changed (immutable after creation)
- At least one node set must remain; node sets can be scaled to zero (the control
  plane continues to run on the hub)

After either command, the cluster transitions to `PROGRESSING` until the new node
configuration is applied. Monitor progress with:

```bash
osac describe cluster <cluster-id>
```

## Delete a Cluster

```bash
osac delete clusters <cluster-id>
```

Deletion triggers cleanup of all associated resources (HostedCluster, HostPool, bare-metal host
allocations). The cluster remains visible with a `deletion_timestamp` set until cleanup completes.

Verify deletion:

```bash
osac get clusters
```

## API Access

All CLI operations correspond to REST API endpoints:

| Operation | Method | Endpoint |
|-----------|--------|----------|
| List catalog items | `GET` | `/api/fulfillment/v1/cluster_catalog_items` |
| Get catalog item | `GET` | `/api/fulfillment/v1/cluster_catalog_items/{id}` |
| List clusters | `GET` | `/api/fulfillment/v1/clusters` |
| Get cluster | `GET` | `/api/fulfillment/v1/clusters/{id}` |
| Create cluster | `POST` | `/api/fulfillment/v1/clusters` |
| Update cluster | `PATCH` | `/api/fulfillment/v1/clusters/{id}` |
| Delete cluster | `DELETE` | `/api/fulfillment/v1/clusters/{id}` |
| Get secret | `GET` | `/api/fulfillment/v1/secrets/{id}` |

See [Filter expressions](FILTER.md) for filtering and ordering list results.
