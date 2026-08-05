# Bare Metal Fulfillment Operator

Kubernetes operator for managing bare metal host pools in the [Open Sovereign AI
Cloud (OSAC)](https://github.com/osac-project) project.

## Description

Bare Metal Fulfillment Operator is part of the OSAC project. It watches the following custom
resources and reconciles them to their desired state:

- **BareMetalPool** (`bmp`, `bmpool`) — provisions pools of bare metal hosts
  organized by host type (e.g., GPU nodes, worker nodes). Each pool can specify
  the number of replicas needed per host type and apply configuration profiles
  with template parameters.

## Configuration

Configuration is supplied via environment variables (e.g. from a Secret mounted
into the manager deployment) and volume mounts. The following are supported:

### Inventory

The operator reads inventory configuration to gather information from backend
inventory systems.

**Configuration file:** `/etc/osac/inventory/inventory.yaml` (default)

The path can be overridden with the `OSAC_INVENTORY_CONFIG_PATH` environment variable.

**Example:**

```yaml
name: my-inventory
type: openstack
options:
  openstack:
    cloud: osac-project
hostClass: openstack
networkClass: openstack
```

**Fields:**
- `name` — identifier for this inventory backend
- `type` — inventory backend type (e.g., `openstack`)
- `options` — backend-specific configuration options
- `hostClass` — host management class to use
- `networkClass` — network class to use

### Management

The operator reads management configuration to connect to backend management
systems for power control operations (e.g., Ironic for OpenStack bare metal).

**Configuration file:** `/etc/osac/management/management.yaml` (default)

The path can be overridden with the `OSAC_MANAGEMENT_CONFIG_PATH` environment variable.

**Example:**

```yaml
name: my-management
type: openstack
options:
  openstack:
    cloud: osac-project
```

**Fields:**
- `name` — identifier for this management backend
- `type` — management backend type (e.g., `openstack`)
- `options` — backend-specific configuration options
  - For `openstack` type: the `openstack` key contains cloud configuration
    following the [gophercloud clouds.yaml format](https://github.com/gophercloud/utils/blob/main/openstack/clientconfig/requests.go)

### Host Lock

The inventory package provides in-memory locking (`inventory.TryLock()` and
`inventory.Unlock()`) to coordinate host assignments and prevent race conditions
when claiming hosts within a single controller instance. Locks are automatically
released via deferred unlock calls.

### Profile

The operator provides host selection via labels (hostSelector) and additional
configuration to the BareMetalPool and each BareMetalInstance's host. Profiles are a
collection of workflows to be run when BareMetalPool and BareMetalInstance are created
and deleted. The inventory hosts that get selected by the profile will be
marked with profile labels in their backend.

**Configuration file:** `/etc/osac/profile/profile.yaml` (default)

The path can be overridden with the `OSAC_PROFILE_CONFIG_PATH` environment variable.

**Example:**

```yaml
- name: agentProvisioning
  hostSelector:
    managedBy: baremetal
    provisionState: available
  bareMetalPoolTemplate: private_network
  hostTemplate: agent_provision
  labels:
    profileName: agentProvisioning
  persistentLabels:
    managedBy: agent
    provisionState: active
```

**Fields:**
- `name` — name of the profile
- `hostSelector` — map of key/value pairs that determine which hosts this profile applies to
- `expectedTemplateParameters` — list of parameter names to validate input template parameters with
- `bareMetalPoolTemplate` — workflow executed when a BareMetalPool is created and deleted
- `hostTemplate` — workflow executed when a BareMetalInstance is created and deleted
- `labels` — labels to be applied in the inventory that will get deleted when the host's BareMetalInstance gets deleted
- `persistentLabels` — labels to be applied in the inventory that won't get deleted when the host's BareMetalInstance gets deleted

### Environment Variables

The following environment variables can be used to configure controller behavior:

#### Configuration Paths

- **`OSAC_INVENTORY_CONFIG_PATH`** — Path to the inventory configuration file. Default: `/etc/osac/inventory/inventory.yaml`
- **`OSAC_MANAGEMENT_CONFIG_PATH`** — Path to the management configuration file. Default: `/etc/osac/management/management.yaml`
- **`OSAC_PROFILE_CONFIG_PATH`** — Path to the profile configuration file. Default: `/etc/osac/profile/profile.yaml`

#### AAP Provisioning Provider

- **`OSAC_AAP_URL`** — AAP server URL. Optional at deploy time; injected post-install by `prepare-aap.sh`.
- **`OSAC_AAP_TOKEN`** — AAP authentication token. Optional at deploy time; injected post-install by `prepare-aap.sh`.
- **`OSAC_AAP_INSECURE_SKIP_VERIFY`** — skip TLS verification for AAP (default: `false`).
- **`OSAC_AAP_STATUS_POLL_INTERVAL`** — job status polling interval (default: `10s`). Duration string, e.g. `30s`, `1m`.

#### BareMetalPool Controller

- **`OSAC_HOST_DELETION_POLL_INTERVAL`** — Interval for polling host deletion status during BareMetalPool teardown. Default: `5s`

#### BareMetalInstance Controller

- **`OSAC_NO_FREE_HOSTS_POLL_INTERVAL`** — Requeue interval when no free hosts are available in the inventory. Default: `30s`
- **`OSAC_TRY_LOCK_FAIL_POLL_INTERVAL`** — Requeue interval when lock acquisition fails. Default: `1s`
- **`OSAC_BAREMETALINSTANCE_MAX_CONCURRENT_RECONCILES`** — The maximum amount of times the BareMetalInstance reconcile function can run concurrently. Default: `1`

**Example:**
```yaml
env:
  - name: OSAC_INVENTORY_CONFIG_PATH
    value: "/custom/path/inventory.yaml"
  - name: OSAC_HOST_DELETION_POLL_INTERVAL
    value: "10s"
  - name: OSAC_NO_FREE_HOSTS_POLL_INTERVAL
    value: "60s"
  - name: OSAC_TRY_LOCK_FAIL_POLL_INTERVAL
    value: "2s"
```

## Getting Started

### Prerequisites

- go version v1.25.0+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster

**Build and push your image to the location specified by `IMG`:**

``` sh
make image-build image-push IMG=<some-registry>/bare-metal-fulfillment-operator:tag
```

**NOTE:** This image ought to be published in the personal registry you
specified. And it is required to have access to pull the image from the working
environment. Make sure you have the proper permission to the registry if the
above commands don't work.

**Install the CRDs into the cluster:**

``` sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

``` sh
make deploy IMG=<some-registry>/bare-metal-fulfillment-operator:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself
> cluster-admin privileges or be logged in as admin.

**Create instances of your solution**

You can apply the samples (examples) from the config/sample:

``` sh
kubectl apply -k config/samples/
```

> **NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall

**Delete the instances (CRs) from the cluster:**

``` sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

``` sh
make uninstall
```

**UnDeploy the controller from the cluster:**

``` sh
make undeploy
```

## Project Distribution

Following are the steps to build the installer and distribute this project to
users.

1.  Build the installer for the image built and published in the registry:

``` sh
make build-installer IMG=<some-registry>/bare-metal-fulfillment-operator:tag
```

NOTE: The makefile target mentioned above generates an 'install.yaml' file in
the dist directory. This file contains all the resources built with Kustomize,
which are necessary to install this project without its dependencies.

2.  Using the installer

Users can just run kubectl apply -f <URL for YAML BUNDLE> to install the
project, i.e.:

``` sh
kubectl apply -f https://raw.githubusercontent.com/<org>/bare-metal-fulfillment-operator/<tag or branch>/dist/install.yaml
```

## Contributing

// TODO(user): Add detailed information on how you would like others to
contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder
Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use
this file except in compliance with the License. You may obtain a copy of the
License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed
under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
