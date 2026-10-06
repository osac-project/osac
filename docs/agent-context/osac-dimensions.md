# OSAC Feature Dimensions

This file defines the cross-cutting dimensions that OSAC PRD and design
documents should consider, subject to the relevance triage below — not every
dimension applies to every feature. Both the PRD (`/prd`) and design
(`/design`) workflows should consult this file during their ingest phases to
ensure comprehensive coverage of the dimensions that actually apply.

This project reference is maintained with OSAC. Read it during requirements and
design drafting, decomposition, and review, including outside a named workflow.
The bootstrap-managed `.design/context/osac-dimensions.md` path forwards here.

## Applying These Dimensions

Before asking about a dimension during `/clarify`, or before drafting In
Scope/Out of Scope content for it, confirm the feature's Jira issue plausibly
touches that dimension (service, component, or workflow named in the issue).
If a dimension clearly doesn't apply, skip it silently — do not ask about it,
and do not add an "N/A" or "out of scope" line for it. When genuinely unsure
whether a dimension applies, ask one targeted question rather than assuming
either way.

## Services

Every feature applies to one or more OSAC services. The PRD must declare
which services are in scope, and the design must address service-specific
implementation differences.

| Service | Description |
|---------|-------------|
| **BMaaS** | Bare Metal as a Service — provisioning and lifecycle of physical machines |
| **CaaS** | Cluster as a Service — Kubernetes cluster provisioning via Hosted Control Planes |
| **VMaaS** | Virtual Machines as a Service — KubeVirt-based compute instances |
| **MaaS** | Model as a Service — AI model serving and inference platform |
| **Enclave** | Day 1/Day 2 operations, installation monitoring, wizard UI |

## Personas

OSAC has four canonical personas (defined in [personas.md](../personas.md)). Features
must specify what each affected persona can see and do. Use these exact names
in user stories and workflow descriptions.

| Persona | Role | Examples |
|---------|------|----------|
| **Cloud Provider Admin** | Works for the cloud provider. Handles tenant onboarding, sets quotas, manages global catalogs, is a super-user who can see all tenants. | Tenant onboarding, quota management, global template catalogs, resource allocation |
| **Cloud Infrastructure Admin** | Works for the cloud provider. Manages core infrastructure (network, firewall, compute, storage). Integrates control plane with local infrastructure. | Specify inventory backends, network classes, IP pools, storage tiers, DNS, integrate with Netris/VAST/ESI |
| **Tenant Admin** | Works for the tenant organization. Manages their org's config, users, IDP, quotas, and org-specific catalogs. Can only see their own organization. | Create networking objects, manage tenant resources, onboard users, control template visibility |
| **Tenant User** | Works for the tenant organization. Self-service provisions cloud resources, manages full lifecycle. Prefers click-ops but wants API/CLI for automation. | Order machines/clusters/VMs via catalog, manage instance lifecycle, view quota utilization |

## Cross-Cutting Dimensions

For each dimension below that applies (per the triage rule above), the PRD
should state its in-scope content. Add an explicit out-of-scope statement
for that dimension only when a reader might otherwise assume broader scope
than intended — not as a mandatory pairing with every in-scope bullet (see
`.prd/templates/section-guidance.md`'s Out of Scope guidance, which is
optional and prohibits padding with "N/A").

**PRD vs Design:** The PRD states which user-facing behaviors are affected
and why. The design document specifies how (CRD fields, conditions, reconcile
logic, AAP templates, installer changes).

### Tenant Onboarding

How does the feature interact with tenant provisioning?

- RBAC requirements (new roles, permissions, policy changes)
- IDP integration (authentication flows, identity provider considerations)
- Auto-provisioned resources during tenant creation
- Tenant isolation implications

### Inventory

Which inventory backend(s) does the feature use or affect?

- Does the feature add new inventory backends or extend existing ones?
- Which services consume the inventory data?

### Provisioning

What provisioning mechanism does the feature use?

- Which provisioning backend(s) are involved?
- Lifecycle stages affected (create, start, stop, restart, delete)
- Power management considerations (BMaaS)
- Cluster vs. ComputeInstance vs. bare metal provisioning differences

### Networking

Read [networking decisions](networking-decisions.md) and the applicable accepted
designs. Treat their constraints as target behavior and verify implementation
status before describing a capability as available.

Which networking backend(s) are involved?

- Is the integration through the OSAC networking API or a side-channel?
- Does the feature add or modify networking API resources?
- NetworkClass configuration requirements (Cloud Infrastructure Admin)
- ExternalIP/ExternalIPPool management (check the accepted networking migration contract)

### Storage

Does the feature involve persistent storage for tenant workloads?

- Can tenants create persistent volumes on provisioned clusters?
- Can tenants select a storage tier when creating persistent volumes?
- Can tenants and admins see whether storage is ready on a given cluster?
- Does the feature affect how storage is provisioned or configured for tenants during onboarding?
- Are there storage prerequisites that must be in place before clusters are provisioned?

*Design document specifies: storage provider details, API fields, driver
installation, credential management.*

### Installation

Does the feature require new deployment prerequisites or configuration?

- Does an admin need to configure new infrastructure before using the feature (e.g., storage backend reachable from hub cluster)?
- Does the feature add new configuration options for operators or admins?
- Are there CI pipeline changes needed?

*Design document specifies: Helm chart values and `osac-installer/` script changes.*

Features adding or modifying Helm values must consider the Enclave Wizard pipeline — see [Enclave Wizard pipeline](enclave-wizard-pipeline.md) for the schema-to-control mapping and the three-artifact decompose chain.

### E2E Testing

What deployed cross-component coverage does the feature require in `tests/e2e/`?
Use [Integration testing](../INTEGRATION-TESTING.md) to assign the tier and owner
for each case. `osac-test-infra` owns infrastructure backends and reusable
workflows; it is a separate repository and is not bootstrapped.

- Which user-visible flows must work for this milestone (happy path, error paths, edge cases)?
- Which API surfaces and user journeys need coverage (Fulfillment API, CRDs, catalog/templates)?
- Are there cross-service test scenarios (e.g., provisioning + networking)?
- What test infrastructure is required (pytest fixtures, env/config, test tenants/organizations)?

### Documentation

What user-facing documentation does the feature require?

- What user-facing documentation is needed (user guides, API reference, architecture docs)?
- API reference lives in the owning component (e.g. `fulfillment-service/docs/`); cross-component guides live in `docs/`.
- Which persona workflows need documented?
- Are updates needed in `docs/`, component docs, or the separate `enhancement-proposals` repository?
- Is documentation in scope for this milestone or explicitly deferred?
- Does the feature change existing documented workflows that need updating?

### UI

What UI support does the feature require in the tracked `osac-ui/` web console?

- Which persona workflows need UI support? (osac-ui maps Keycloak roles to provider/tenant admin/user — Cloud Infrastructure Admin may have no console today)
- Which UI views/pages are affected (list, detail, create, edit / lifecycle actions)?
- Is UI in scope for this milestone or explicitly deferred (API/CLI-only acceptable)?
- Does the feature require new UI components or extend existing ones in osac-ui?
- Which Fulfillment API resources and catalog entries need console representation? (osac-ui uses the Fulfillment Public API via proxy — not direct CRD access)

Cloud Infrastructure Admin UI work that adds or modifies `osac-installer` Helm values goes through the Enclave Wizard pipeline — see [Enclave Wizard pipeline](enclave-wizard-pipeline.md).

#### Jira component conventions

When a feature requires UI work, add UI tasks to the feature's regular epics
(not a separate UI epic). Each UI task for an affected persona gets an extra
component so it appears in the right Jira filter:

| Persona | Components |
|---------|------------|
| Cloud Infrastructure Admin | Epic's components + `Enclave` |
| Cloud Provider Admin | Epic's components + `UI` |
| Tenant Admin | Epic's components + `UI` |
| Tenant User | Epic's components + `UI` |

Only create UI tasks for personas affected by the feature. The per-component
Jira filters track UI work across services (CaaS, BMaaS, VMaaS, Core,
Enclave, Connectivity&Fabric, Storage) using these component assignments.

## User-Facing Behavior

For each service in scope, identify which user-observable behaviors the feature
affects. The PRD states what users can do or see; the design document specifies
the API surfaces, field names, and resource schemas that implement those
behaviors.

- What new capabilities do users gain? (e.g., tenants can provision clusters with storage, admins can see storage readiness across tenants)
- What existing behaviors change? (e.g., cluster provisioning now includes automatic storage setup)
- Which personas are affected and how?
- Which user-facing resources are affected? (e.g., ClusterOrder, ComputeInstance, Tenant, VirtualNetwork, StorageClass)

## Milestone Scoping

When writing a PRD or design, explicitly declare:

- **Target milestone** (e.g., 0.1, 0.2)
- **What's NOT covered** — dimensions or capabilities deferred to a later milestone (e.g., "No Networking API integration in 0.1", "No Storage API in 0.1")
- **Known risks and gaps** — dependencies, DNS requirements, third-party onboarding, etc.
- **Upgrades** — OSAC does not currently support in-place upgrades. State this limitation, but assess data migration and backward compatibility whenever existing state or independently deployed components are affected.
