---
marp: true
theme: redhat
paginate: true
title: "OSAC Storage Framework"
description: "StorageBackends, StorageTiers, Tenant and CaaS Storage Provisioning"
---

<style>
section.compact { font-size: 22px; }
section.compact li { margin-bottom: 2px; }
section.compact h2 { margin-bottom: 10px; }
section.detail { font-size: 22px; }
section.detail li { margin-bottom: 2px; }
</style>

<!-- _class: title -->
<!-- _paginate: false -->

# OSAC Storage Framework

### StorageBackends, StorageTiers, Tenant and CaaS Storage Provisioning

OSAC-917 | August 2026

---

## Agenda

| Part | Topic | Demo? |
|------|-------|-------|
| 1 | StorageBackend and StorageTier Registration | Yes |
| 2 | Tenant Onboarding: Storage Setup | Yes |
| 3 | Cluster Provisioning: Cluster Storage Setup | Slides |
| 4 | Destroying a Cluster | Slides |
| 5 | Destroying a Tenant | Slides |
| 6 | Deleting Tiers and Backend | Yes |

---

## The Problem

Datacenters have different storage equipment from different vendors.
Each vendor supports different protocols and performance characteristics.

```text
  Datacenter A              Datacenter B              Datacenter C
  +------------------+     +------------------+     +------------------+
  | VAST Data        |     | Pure Storage     |     | Ceph / ODF       |
  | - NFS (file)     |     | FlashBlade:      |     | - RBD (block)    |
  | - NVMe (block)   |     |   NFS, S3        |     | - CephFS (file)  |
  | - S3 (object)    |     | FlashArray:      |     | - RGW (object)   |
  +------------------+     |   FC, iSCSI, NFS |     +------------------+
                            +------------------+
```

OSAC needs a unified way to:
- Register and manage storage vendors and their connection details
- Define tiers with different protocols and QoS characteristics
- Automatically provision per-tenant networked storage on clusters

---

## The Solution: Storage Framework

Three steps, each with a clear owner:

```text
  +------------------------------------------------------------------+
  |  Step 1: Backend + Tier Registration      (Cloud Provider Admin)  |
  |  Private API: register storage vendors, define storage tiers     |
  +------------------------------------------------------------------+
                              |
                     operator queries APIs
                              |
  +------------------------------------------------------------------+
  |  Step 2: Tenant Storage Provisioning      (Storage Controller)    |
  |  Operator + AAP, two stages:                                     |
  |    a. Backend Setup (once per tenant)                             |
  |       Create tenant identity, quotas, views on the vendor        |
  |    b. Cluster Storage Setup (once per cluster)                   |
  |       Install CSI driver + per-tenant StorageClasses             |
  +------------------------------------------------------------------+
                              |
                     tenant sees StorageClasses
                              |
  +------------------------------------------------------------------+
  |  Step 3: Tenant Consumption               (Tenant User)          |
  |  Create PVCs using tenant-specific StorageClasses                |
  +------------------------------------------------------------------+
```

---

## Personas and API Access

```text
  Cloud Provider Admin                 Tenant Admin / User
  +----------------------------------+ +----------------------------------+
  |                                  | |                                  |
  |  StorageBackend (Private API)    | |  StorageBackend                  |
  |    Create, Get, List,            | |    No access                     |
  |    Update, Delete                | |                                  |
  |                                  | |                                  |
  |  StorageTier (Private API)       | |  StorageTier (Public API)        |
  |    Create, Get, List,            | |    Get, List (read-only)         |
  |    Update, Delete                | |    Planned: OSAC-3014            |
  |                                  | |                                  |
  +----------------------------------+ +----------------------------------+
```

Tenants should not be able to access vendor credentials.
Tenants need to query available tiers when creating ComputeInstances.

---

<!-- _class: divider -->

# Part 1: StorageBackend and StorageTier

---

## StorageBackend

A registered storage system with connection details.

```yaml
StorageBackend:
  metadata:
    name: vast-demo
    tenant: shared              # platform-scoped, set by server
  spec:
    provider: vast              # immutable after creation
    endpoint: 192.0.2.10:8443
    description: "VAST demonstration appliance"
    credentials:
      username: admin
      password: ****
  status:
    state: READY                # set immediately on create
```

**Private API only.** Create and describe via `osac` CLI,
list and delete via REST API.

Provider field determines which AAP role handles provisioning.

---

## StorageTier

A storage offering definition linked to a backend: protocol, capacity, and performance.

```yaml
StorageTier (standard):              StorageTier (fast):
  spec:                                spec:
    backends:                            backends:
      - backend_id: <vast-uuid>            - backend_id: <vast-uuid>
        protocol: NFS                        protocol: BLOCK
        quota_gib: 100                       quota_gib: 50
        max_read_bandwidth_mbs: 200
        max_write_bandwidth_mbs: 100
  status:                              status:
    state: ACTIVE                        state: ACTIVE
```

A single backend can serve multiple tiers with different protocols and characteristics.

Tier names are defined by the CSP admin and vary per deployment
(e.g., `standard`/`fast`, `gold`/`silver`, `nfs-general`/`block-perf`).

**Current limitation:** each tier links to a single backend.

---

## Backend and Tier Relationship

StorageBackend and StorageTier are linked with enforced ordering:

```text
  StorageBackend (vast-demo)          StorageTier (default)
  +-------------------------+         +-------------------------+
  | id: abc-123             |<--------| backends[0].backend_id  |
  | state: READY            |         | state: ACTIVE           |
  +-------------------------+         +-------------------------+

  Cannot delete a backend that has active tiers referencing it.
  Must delete all tiers first, then delete the backend.

  Create order:  Backend first, then Tier (backend_id validated)
  Delete order:  Tier first, then Backend (enforced by DB trigger)
```

---

<!-- _class: compact -->

## CLI Commands

**Create:**
```bash
osac create storagebackend --name vast-demo --provider vast \
  --endpoint 192.0.2.10:8443 \
  --username "$OSAC_STORAGE_USERNAME" --password "$OSAC_STORAGE_PASSWORD"

osac create storagetier --name standard --backend-id <id> \
  --protocol NFS --quota-gib 100 --max-read-bandwidth-mbs 200

osac create storagetier --name fast --backend-id <id> \
  --protocol BLOCK --quota-gib 50
```

**Inspect:**
```bash
osac describe storagebackend vast-demo
osac describe storagetier standard
```

**Delete (order matters):**
```bash
osac delete storagetier standard         # must delete tiers first
osac delete storagetier fast
osac delete storagebackend vast-demo     # then backend
```

---

## Demo: Backend and Tier Registration

1. Show current state: no backends, no tiers
2. Register VAST as a StorageBackend
3. Create a StorageTier linked to the backend
4. Inspect both resources

<!--
[Play recording: ./demos/storage/play-demo.sh 01 02 03]
-->

---

<!-- _class: divider -->

# Part 2: Tenant Onboarding

---

## What Happens When a Tenant Becomes Ready

The Storage Controller **waits until Tenant Phase=Ready** before acting.

```text
  Tenant Phase=Ready --> add finalizer osac.openshift.io/storage
       |
  Stage 1: Backend Setup          (osac-create-tenant-storage-backend)
       +-- Hub Secret exists? YES --> StorageBackendReady=True
       |   NO --> Query Backend API
       |           Backend + AAP --> trigger AAP job
       |           Backend, no AAP --> False
       |           No backend --> False
       v
  Stage 2: Cluster Storage Setup  (osac-create-tenant-cluster-storage)
       +-- Per-tenant SCs exist? YES --> ClusterStorageReady=True
       |   NO + AAP --> trigger AAP job
       v
  All storage conditions True
```

Backend Setup runs once per tenant. Cluster Storage Setup runs once per cluster.

---

## Stage 1: Backend Setup

AAP job template: `osac-create-tenant-storage-backend`

What gets created is vendor-specific. Each provider role implements `setup.yaml`.

```text
  Operator passes to AAP:             AAP creates per tenant on vendor:
  +--------------------------------+ +----------------------------------+
  | Tenant payload                 | | Tenant identity + credentials    |
  | storage_tier_definitions       | | Per-tier paths (same tier name,  |
  |   [{name, protocol, quota,     | |   different path per tenant):    |
  |     bandwidth, backend_id}]    | |   /osac-<tenant>-<hash>/standard |
  | storage_backend_connections    | |   /osac-<tenant>-<hash>/fast     |
  |   [{endpoint, username, pwd}]  | | Per-tier: view, quota, QoS       |
  +--------------------------------+ +----------------------------------+
                                                    |
                                     AAP writes to hub cluster:
                                     +----------------------------------+
                                     | K8s Secret with tenant creds     |
                                     +----------------------------------+
                                          StorageBackendReady=True
```

---

## Stage 2: Cluster Storage Setup

AAP job template: `osac-create-tenant-cluster-storage`

```text
  Operator passes to AAP:              AAP creates on target cluster:
  +----------------------------------+ +-------------------------------------+
  | Tenant or ClusterOrder payload   | | CSI Operator (via OLM)              |
  | admin_kubeconfig (for CaaS)      | | CSI Secret (per-tenant creds        |
  | storage_tier_definitions         | |   from hub Secret, NOT admin)       |
  | storage_backend_connections      | | Per-tier StorageClasses:            |
  +----------------------------------+ |   e.g. vast-nfs-acme-standard       |
                                       |   e.g. vast-block-acme-fast         |
  CSI Secret uses per-tenant creds     |   labels: tenant, tier, managed     |
  from hub Secret (Backend Setup),     +-------------------------------------+
  never admin credentials.                  ClusterStorageReady=True
```

---

<!-- _class: detail -->

## How the Operator Finds StorageClasses

The operator discovers tenant StorageClasses purely by labels:

```yaml
  StorageClass: vast-nfs-mycompany-standard
    labels:
      osac.openshift.io/tenant: mycompany          # ownership
      osac.openshift.io/storage-tier: standard     # tier association
      app.kubernetes.io/managed-by: osac-aap       # set by AAP playbook

  StorageClass: vast-block-mycompany-fast
    labels:
      osac.openshift.io/tenant: mycompany
      osac.openshift.io/storage-tier: fast
      app.kubernetes.io/managed-by: osac-aap
```

The `managed-by` label is set by the AAP playbook when creating
StorageClasses. The operator discovers tenant SCs by these labels.

Every tenant must have its own per-tenant StorageClasses.
The shared `tenant=Default` fallback has been removed.

---

## Default StorageClass Removed

The storage controller previously fell back to a shared StorageClass
labeled `osac.openshift.io/tenant=Default` when no tenant-specific
StorageClass was found.

```text
  Before (removed):                    Now:
  +--------------------------------+   +--------------------------------+
  | 1. Look for SC labeled         |   | Look for SC labeled            |
  |    tenant=<tenantName>         |   |   tenant=<tenantName>          |
  |                                |   |   storage-tier=<tierName>      |
  | 2. Not found? Fall back to SC  |   |                                |
  |    labeled tenant=Default      |   | Not found?                     |
  |    (shared across all tenants) |   |   ClusterStorageReady=False    |
  +--------------------------------+   +--------------------------------+
```

**What this means:**
- Each tenant must have its own labeled StorageClasses
- No shared StorageClass is resolved across tenants
- Environments that relied on a `tenant=Default` SC need
  per-tenant SCs provisioned via AAP or labeled manually

---

## Demo: Tenant Onboarding

1. Create a Tenant
2. Watch StorageBackendReady and ClusterStorageReady conditions progress
3. See per-tenant StorageClass appear
4. Create a PVC using the tenant StorageClass
5. Verify volume is created and bound

---

<!-- _class: divider -->

# Part 3: Cluster Provisioning

---

## CaaS Clusters: Cluster Storage Setup Only

When a ClusterOrder becomes Ready, the Storage Controller runs **only
Cluster Storage Setup**. Backend resources already exist from tenant onboarding.

```text
  Tenant Onboarding                    Cluster Provisioning
  (runs both stages)                   (Cluster Storage Setup only)

  Stage 1: Backend Setup               (skipped: hub Secret
    - vendor tenant                     already exists)
    - views, quotas
    - Hub Secret

  Stage 2: Cluster Storage Setup       Stage 2: Cluster Storage Setup
    - CSI driver on hub cluster          - CSI driver on GUEST cluster
    - Per-tenant StorageClasses          - Per-tenant StorageClasses
    - Target: VMaaS hub cluster          - Target: CaaS guest cluster
                                         - Namespace: openshift-osac-storage
```

The ClusterOrder gets its own `ClusterStorageReady` condition.

---

<!-- _class: detail -->

## How CaaS Cluster Storage is Provisioned

```text
  ClusterOrder becomes Ready
       |
  Storage Controller
       |
       v
  For each Ready ClusterOrder without ClusterStorageReady=True:
       |
       +-- Add finalizer: osac.openshift.io/cluster-storage
       |
       +-- Get guest cluster kubeconfig from HostedControlPlane
       |     (HCP -> kubeConfig Secret)
       |
       +-- Trigger osac-create-tenant-cluster-storage
       |     payload: ClusterOrder (not Tenant)
       |     extra_vars includes admin_kubeconfig
       |
       +-- AAP detects payload.kind == ClusterOrder
       |     targets namespace: openshift-osac-storage
       |     on the GUEST cluster
       |
       v
  ClusterStorageReady=True on ClusterOrder
```

---

<!-- _class: divider -->

# Part 4: Destroying a Cluster

---

## Cluster Deletion: Storage Cleanup

When a ClusterOrder is deleted, the storage finalizer ensures cleanup:

```text
  ClusterOrder deletion triggered
       |
       +-- Has finalizer                        Only cluster-side resources
       |   osac.openshift.io/cluster-storage?   are removed:
       |     NO --> nothing to do                 - StorageClasses
       |     YES:                                 - CSI Secret (on that cluster)
       +-- Is HostedControlPlane still alive?
       |     NO --> skip cleanup                Backend resources stay intact
       |            (cluster already gone)      (shared by other clusters
       |     YES:                                and the VMaaS hub).
       |
       +-- Get guest cluster kubeconfig
       |
       +-- Trigger osac-delete-tenant-cluster-storage
       |
       +-- Remove finalizer
       |
       v
  ClusterOrder deletion proceeds
```

---

<!-- _class: divider -->

# Part 5: Destroying a Tenant

---

## Tenant Deletion: Full Storage Cleanup

Tenant deletion triggers a three-phase cleanup in strict order:

```text
  Tenant deletion triggered
       |
  Phase 1: CaaS Cluster Cleanup (osac-delete-tenant-cluster-storage)
       |   For EACH ClusterOrder with storage finalizer:
       |     skip if HCP gone, else get kubeconfig + trigger AAP
       |     Remove SCs + CSI Secret from CaaS cluster
       |     Remove cluster-storage finalizer from ClusterOrder
       |
  Phase 2: Hub Cluster Cleanup (osac-delete-tenant-cluster-storage)
       |   Remove SCs + CSI Secret from hub cluster
       |
  Phase 3: Backend Teardown (osac-delete-tenant-storage-backend)
       |   Remove vendor resources (tenant, views, quotas) + hub Secret
       |
  Remove osac.openshift.io/storage finalizer from Tenant
       |
       v
  Tenant deletion proceeds
```

---

<!-- _class: detail -->

## What Prevents Incomplete Cleanup

```text
  Finalizer: osac.openshift.io/storage (on Tenant)
    Prevents tenant deletion until ALL storage cleanup completes.
    Removed only after Phase 3 backend teardown.

  Finalizer: osac.openshift.io/cluster-storage (on ClusterOrder)
    Prevents cluster deletion until cluster-side resources are removed.
    Removed after each individual cluster cleanup.

  Failed cleanup jobs block deletion until an admin investigates.
  This prevents orphaned resources on the storage vendor.
```

---

<!-- _class: divider -->

# Part 6: Deleting Tiers and Backend

---

## Demo: Tier and Backend Deletion

Reverse of Part 1. Enforced ordering:

1. Attempt to delete backend (fails: active tier references it)
2. Delete the StorageTier
3. Delete the StorageBackend
4. Verify both are gone

<!--
[Play recording: ./demos/storage/play-demo.sh 04 05 06]
-->

---

<!-- _class: detail -->

## What Happens in Different Configurations

How the storage controller behaves:

| Backend registered? | AAP configured? | StorageBackendReady | ClusterStorageReady |
|---|---|---|---|
| Yes | Yes | True (after AAP job) | True (per-tenant SCs created) |
| Yes | No | False: "no AAP configured" | Not evaluated |
| No | N/A | False: "no backend registered" | Resolves manually labeled SCs |

---

## Storage Controller: Enabled vs Disabled

Helm value: `operator.controllers.storage` (default: enabled in chart).

```text
  Enabled (storage: true)              Disabled (storage: false)
  +----------------------------------+ +----------------------------------+
  | Waits for Tenant/ClusterOrder    | | No storage reconciliation        |
  |   to become Ready, then acts     | |                                  |
  | Sets StorageBackendReady,        | | Tenants and clusters provision   |
  |   ClusterStorageReady conditions | |   normally, but without          |
  | Triggers AAP storage jobs        | |   tenant-scoped StorageClasses   |
  | Enforces cleanup on deletion     | |                                  |
  +----------------------------------+ +----------------------------------+
```

Storage conditions are **additive**: they do not block Tenant or
ClusterOrder from becoming Ready.

**VMaaS requires it**: ComputeInstance disks need PVCs, which need
tenant-scoped StorageClasses. Without storage controller, no SCs.
CaaS clusters provision without it, but PVC workloads will fail.

---

<!-- _class: detail -->

## Manual StorageClasses for Development

When no backends or tiers are registered, developers can configure
StorageClasses manually. The storage controller resolves them by labels.

```yaml
  Required labels on your StorageClass:
  +---------------------------------------------------+
  | osac.openshift.io/tenant: <tenantName>             |
  | osac.openshift.io/storage-tier: <tierName>         |
  +---------------------------------------------------+

  Example:
  apiVersion: storage.k8s.io/v1
  kind: StorageClass
  metadata:
    name: lvms-mycompany-standard
    labels:
      osac.openshift.io/tenant: mycompany
      osac.openshift.io/storage-tier: standard
  provisioner: topolvm.io
  ...
```

The controller discovers these labeled StorageClasses and sets
`ClusterStorageReady=True` without AAP. Useful for local dev, CI,
or environments running OSAC without a production storage backend.

---

## What's Changing and What's Next

### Recently completed

- **Default SC fallback removed**: every tenant must have its own
  per-tenant StorageClasses (no more shared `tenant=Default`)
- **AAP runtime configuration**: operator passes tier and backend
  info to AAP at runtime (no static Secrets)
- **Local storage for dev/CI**: LVMS-backed provider role creates
  per-tenant StorageClasses so dev/CI works without VAST

### Coming up

- **Public StorageTier API**: read-only Get/List for tenants
  to query available tiers
- **Storage Control Plane**: vendor-agnostic OSAC CSI driver
  with volume management via fulfillment-service

---

<!-- _class: detail -->

## Adding a New Storage Provider

One AAP template role with four task files:

```text
  roles/<provider>_storage/
    meta/
      osac.yaml                       # provider metadata + capabilities
    tasks/
      setup.yaml                      # create vendor resources + hub Secret
      ensure_storage_class.yaml       # install CSI + create StorageClasses
      teardown_cluster_storage.yaml   # remove SCs + CSI Secret from cluster
      teardown_backend.yaml           # remove vendor resources + hub Secret
    defaults/
      main.yaml                       # provider-specific defaults
```

The dispatcher resolves the provider name from tier definitions and
routes to the matching role automatically.

Reference implementations:
[vast_storage](https://github.com/osac-project/osac/tree/main/osac-aap/collections/ansible_collections/osac/templates/roles/vast_storage) |
[lvms_storage](https://github.com/osac-project/osac/tree/main/osac-aap/collections/ansible_collections/osac/templates/roles/lvms_storage)

---

<!-- _class: detail -->

## How Provider Routing Works

```text
  Playbook: osac-create-tenant-storage-backend
       |
       v
  osac.service.storage_provider (dispatcher)
       |
       +-- Extract providers from tier_definitions
       |     [{provider: "vast", ...}, {provider: "lvms", ...}]
       |
       +-- For each unique provider:
       |
       +-- include_role: osac.templates.{{ provider }}_storage
       |     tasks_from: setup
       |
       v
  vast_storage/tasks/setup.yaml     (VAST appliance)
    or
  lvms_storage/tasks/setup.yaml     (local LVMS, dev/CI)
```

**Current providers:** `vast_storage` (production), `lvms_storage` (dev/CI)

---

<!-- _class: detail -->

## Local Storage for Dev/CI (LVMS)

Dev/CI environments don't have a production storage backend like VAST.
LVMS (Logical Volume Manager Storage) provides local block storage
using LVM on the node's disks.

```text
  At install time (Helm hook, gated by lvms.enabled):
  +---------------------------------------------------+
  | Auto-registers:                                    |
  |   StorageBackend "local" (provider: lvms)          |
  |   StorageTier "local" (block protocol)             |
  +---------------------------------------------------+

  When a Tenant becomes Ready:
  +---------------------------------------------------+
  | LVMS role creates:                                 |
  |   Lifecycle marker Secret (no real credentials)    |
  |   Per-tenant StorageClass: osac-<tenant>-local     |
  |     provisioner: topolvm.io                        |
  +---------------------------------------------------+
```

Block protocol only, VMaaS targets only. No real vendor credentials.
Unlike VAST, backend + tier are auto-registered at install time.

---

## Full Picture

```text
  Step 1: Registration              Step 2: Provisioning
  (Cloud Provider Admin)            (automatic)

  osac CLI                          Storage Controller
     |                                  |
     v                                  v
  fulfillment-service  <----------  Tenant Ready: both stages
  (StorageBackend DB)               ClusterOrder Ready: Cluster Storage only
  (StorageTier DB)                      |
                                        v
                                    AAP (provider role)
                                        |
                        +---------------+---------------+
                        |                               |
                        v                               v
                   Storage Vendor               Target Cluster
                   (tenant, views,              (CSI driver,
                    quotas, QoS)                 CSI Secret,
                                                 StorageClasses)

  Step 3: Consumption
  (Tenant User)

  kubectl / osac CLI --> PVC --> StorageClass --> CSI --> Vendor
```

---

<!-- _class: dark -->

# Thank You

Questions?

[OSAC-917: Storage Framework](https://redhat.atlassian.net/browse/OSAC-917)

---

<!-- _class: compact -->

## Appendix: AAP Job Templates

| Template | Triggered by | What it does |
|---|---|---|
| `osac-create-tenant-storage-backend` | Tenant Phase=Ready | Backend Setup: vendor resources + hub Secret |
| `osac-delete-tenant-storage-backend` | Tenant delete | Reverse of above + delete hub Secret |
| `osac-create-tenant-cluster-storage` | Tenant Phase=Ready or ClusterOrder Ready | Cluster Storage Setup: CSI + StorageClasses |
| `osac-delete-tenant-cluster-storage` | Tenant or ClusterOrder delete | Remove cluster-side K8s resources |

`osac-create-tenant-cluster-storage` handles both VMaaS (Tenant payload)
and CaaS (ClusterOrder payload) by detecting the payload kind.
