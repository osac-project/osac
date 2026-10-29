---
marp: true
theme: redhat
paginate: true
title: "OSAC Taxonomy & Architecture"
description: "Aligning Technical Definitions"
---

<!-- _class: title -->

# OSAC Taxonomy & Architecture

### Aligning Technical Definitions

---

## What is OSAC?

**Open Sovereign AI Cloud** — a multi-tenant cloud platform for provisioning Kubernetes clusters, virtual machines, bare metal servers, and AI model serving on sovereign infrastructure.

- **Declarative API** (gRPC + REST) following Kubernetes conventions
- **Catalog-driven** provisioning — extensible templates adapt to each customer's environment
- **Four service domains:** CaaS, VMaaS, BMaaS, MaaS
- Cross-cutting capabilities: networking, storage, auth, observability, metering/quota, and more

---

## Four Personas

| Persona | Scope | Key Actions |
|---------|-------|-------------|
| **Cloud Infrastructure Admin** | Platform-wide | Deploy and operate OSAC; manage servers, network, storage via OpenShift/ACM and infrastructure APIs |
| **Cloud Provider Admin** | Platform-wide | Publish CatalogItems, manage Templates, NetworkClasses, InstanceTypes, ExternalIPPools |
| **Tenant Admin** | Within a Tenant | Manage Users, Roles, Projects, IdentityProviders, networking |
| **Tenant User** | Within a Project | Create Clusters, VMs, BareMetalInstances from catalog; manage ExternalIPs |

Infrastructure admins **bring up and operate** the platform.
Provider admins define **what** can be provisioned (templates, catalogs).
Tenant users **consume** those offerings.

---

<!-- _class: divider -->

# System Architecture

---

## Component Overview

| Component | Role |
|-----------|------|
| **fulfillment-service** | gRPC/REST API server + controller, PostgreSQL |
| **osac-metering** | Watches resource lifecycle events, publishes to Kafka |
| **osac-operator** | K8s operator on each management cluster |
| **osac-aap** | Ansible roles for infrastructure provisioning |
| **osac-installer** | Kustomize/Helm deployment manifests |
| **osac-ui** | React + PatternFly 6 web console |

---

## Request Flow

```
  Client (CLI / UI / API)
           │
           ▼
  ┌──────────────────────┐
  │  fulfillment-service │  gRPC + REST Gateway
  │  + PostgreSQL        │  JWT Auth, OPA, Metrics
  └─────────┬────────────┘
            │ creates CRs
            ▼
  ┌─────────────────────┐        ┌───────────┐
  │   osac-operator     │───────►│   AAP     │
  │   (mgmt cluster)    │◄───────│ (Ansible) │
  └─────────┬───────────┘        └───────────┘
            │ Signal RPC                │
            │ (status feedback)         ▼
            └──────────────►  Infrastructure
                              (HyperShift, KubeVirt,
                               OVN, MetalLB, DNS)
```

---

## Deployment Topology

```
  ┌─────────────────────────┐
  │   Fulfillment Service   │
  │   gRPC + REST + Ctrl    │
  │   + PostgreSQL          │
  └──┬──────────────────┬───┘
     │                  │ Watch stream
     │           ┌──────▼──────┐   ┌───────────┐
     │           │  Metering   │──►│   Kafka   │
     │           │  Service    │   │ (AMQ Str) │
     │           └─────────────┘   └───────────┘
     └─────┬────────────────────────┐
           │                        │
  ┌────────▼─────────┐    ┌─────────▼────────┐
  │ Mgmt Cluster 1   │    │ Mgmt Cluster 2   │
  │  RHACM           │    │  RHACM           │
  │  KubeVirt        │    │  KubeVirt        │
  │  AAP             │    │  AAP             │
  │  osac-operator   │    │  osac-operator   │
  │  MetalLB         │    │  MetalLB         │
  └───────┬──────────┘    └────────┬─────────┘
          │                        │
    Workload Clusters         Workload Clusters
    + VMs + BM                + VMs + BM
```

---

## Authentication & Authorization

```
  Client ──► Keycloak (OIDC) ──► JWT token
                                     │
                                     ▼
  fulfillment-service ◄── JWT verified per request
       │
       ├── Tenant extracted from token claims
       │
       ├── OPA policy engine ──► authorization decision
       │     • Role-based access (Role / RoleBinding)
       │     • Tenant isolation enforced
       │     • Project-scoped permissions
       │
       └── Database queries auto-filtered by tenant ID
```

Each tenant can configure its own **IdentityProvider** for Keycloak.
Tenant isolation is enforced at **every layer** — API, policy, and database.

---

## Technology Stack

| Layer | Technology |
|-------|-----------|
| **API** | gRPC + grpc-gateway, Buf v2 |
| **Database** | PostgreSQL (pgx/v5), Kafka (AMQ Streams) |
| **Auth** | Keycloak (OIDC/JWT), OPA |
| **K8s** | controller-runtime, HyperShift, KubeVirt |
| **Provisioning** | Ansible Automation Platform |
| **UI** | React 19, PatternFly 6 |
| **Deploy** | Helm 3, Kustomize |

---

<!-- _class: divider -->

# Resource Taxonomy

---

## The Template / Catalog Pattern

A **Template** is an Ansible role — it contains the actual provisioning logic. Different infrastructure backends or provisioning strategies require **different templates** (different roles, different code).

A **Catalog Item** wraps a template with field definitions, defaults, and visibility controls. Multiple catalog items can reference the **same template** with different configurations.

```
Template  = HOW to provision  (Ansible role, distinct logic)
Catalog   = WHAT to offer     (field defaults, tenant scoping)
```

---

## Customizing Templates

OSAC ships built-in templates, but providers can **extend them** to match their environment:

- **Modification hooks** — transform resource YAML before it's applied (e.g., add custom KubeVirt settings OSAC doesn't expose yet)
- **Phase overrides** — replace entire provisioning steps with provider-specific logic
- **Generic hooks** — inject custom validation, notifications, or metrics at workflow start/end

When overrides become complex or the provider needs fundamentally different provisioning paths, they create a **new template** (a separate Ansible role) rather than adding difficult-to-maintain conditionals to one role.

---

## Catalog Items

Catalog items are the **user-facing offerings** built on top of templates. They package a template with pre-set defaults, field visibility, and tenant scoping to create application-oriented experiences:

| Catalog Item | What it delivers |
|--------------|-----------------|
| "Slurm Cluster" | Pre-configured cluster with Slurm scheduler, job queues, shared storage |
| "AI Training Environment" | Cluster with RHOAI, GPU node pools, model storage pre-wired |
| "Web Application Stack" | Cluster with ingress, TLS, monitoring defaults |

Multiple catalog items can reference the **same template** — each tuned for a different use case.

---

## Resource Hierarchy — Overview

```
Tenant                              ← isolation boundary
  ├── Project                       ← hierarchical org unit
  │     └── ProjectMembership       ← user-to-project binding
  ├── User / Role / RoleBinding     ← identity & RBAC
  ├── IdentityProvider / Secret     ← auth & credentials
  ├── Cluster                       ← OpenShift cluster
  ├── ComputeInstance               ← KubeVirt VM
  ├── BareMetalInstance             ← physical server
  ├── VirtualNetwork                ← L2 network
  │     ├── Subnet                  ← CIDR range
  │     └── SecurityGroup           ← firewall rules
  ├── ExternalIP                    ← routable address
  │     └── ExternalIPAttachment    ← IP ↔ VM binding
  └── NATGateway                    ← outbound connectivity
```

---

## Tenant & Identity Resources

| Resource | Description |
|----------|-------------|
| **Tenant** | Top-level isolation boundary (namespace + network) |
| **Project** | Hierarchical org unit within a tenant (`spec.parent` → Project) |
| **ProjectMembership** | Binds a User to a Project |
| **User** | Identity within a tenant |
| **Role** | Permission definition |
| **RoleBinding** | Binds a Role to a User |
| **IdentityProvider** | IDP configuration per tenant |
| **Secret** | Credential storage |

---

## Compute Resources

| Resource | Description |
|----------|-------------|
| **ComputeInstance** | KubeVirt VM attached to Subnets + SecurityGroups |
| **BareMetalInstance** | Physical bare metal server |
| **Cluster** | OpenShift cluster via Hosted Control Planes |
| **InstanceType** | Hardware flavor (vCPU, memory, disk) |
| **HostType** | Physical hardware class for bare metal |

All three compute types follow the **Template → Catalog Item** pattern for provisioning.

---

## Networking Resources

| Resource | Description |
|----------|-------------|
| **NetworkClass** | Platform-defined network backend (read-only for tenants) |
| **VirtualNetwork** | Tenant-scoped L2 network with CIDR (child of NetworkClass) |
| **Subnet** | CIDR range within a VirtualNetwork |
| **SecurityGroup** | Firewall rules scoped to a VirtualNetwork |
| **NATGateway** | Outbound NAT connectivity |

```
NetworkClass (provider-defined)
  └── VirtualNetwork (tenant-scoped)
        ├── Subnet
        └── SecurityGroup
```

---

## Public IP Resources

| Resource | Description |
|----------|-------------|
| **ExternalIPPool** | Provider-defined IP address ranges |
| **ExternalIP** | Single routable address allocated by tenants |
| **ExternalIPAttachment** | One-to-one binding: ExternalIP ↔ ComputeInstance |

Attachment is a **separate resource** (not a sub-resource action) — consistent with the declarative, intent-based API pattern.

Uses MetalLB L2 mode for IP advertisement.

---

## Platform / Private Resources

These resources are managed by provider admins, not tenants:

| Resource | Scope | Description |
|----------|-------|-------------|
| **Hub** | Private | Management Cluster registration |
| **StorageBackend** | Private | Storage provider configuration |
| **StorageTier** | Private | Storage class definition |
| **Event** | Both | Audit and activity events |

---

<!-- _class: divider -->

# API Design

---

## Object Structure

Every OSAC resource follows a consistent four-part structure, adapted from Kubernetes API conventions:

```protobuf
message VirtualNetwork {
  string id = 1;
  ObjectMetadata metadata = 2;
  VirtualNetworkSpec spec = 3;
  VirtualNetworkStatus status = 4;
}
```

- **spec** — user-controlled desired state (system never modifies)
- **status** — system-controlled observed state (users cannot modify)
- **metadata** — shared fields: name, tenant, labels, annotations, timestamps, version

---

## Dual API Surface

| | Public API | Private API |
|---|---|---|
| **Audience** | Tenant users | Provider admins, controllers |
| **Proto path** | `proto/public/osac/public/v1/` | `proto/private/osac/private/v1/` |
| **REST prefix** | `/api/fulfillment/v1/` | `/api/private/v1/` |
| **Methods** | Create, List, Get, Update, Delete | Same + **Signal** |

- Public is always a **strict subset** of private
- No cross-imports between public and private protos
- Signal RPC: notifies controller of reconciliation need

---

## API Conventions

- **Flat URL space** — `/api/fulfillment/v1/subnets` not `.../virtual_networks/123/subnets`
- **Plural service names**, no "Service" suffix — `Clusters`, `VirtualNetworks`
- **Declarative design** — no imperative methods (Start/Stop); use spec fields like `power_state`
- **Object references** — plain string fields named after the relationship (no `_id` suffix)
- **Enums** — `UPPER_SNAKE_CASE`, prefixed with type, start with `_UNSPECIFIED = 0`
- **Validation** — `buf.validate` annotations (protovalidate), CEL for complex rules

---

<!-- _class: title -->

# Summary

OSAC provides a **declarative, catalog-driven** cloud platform built on OpenShift.

**32 public API services** across compute, networking, identity, and platform domains.

Resources flow from **provider-defined templates** through **catalog items** to **tenant-created instances**, with extensible provisioning that adapts to each customer's environment.
