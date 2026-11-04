# NICo BMaaS tenant and networking plan

Status: working implementation plan for [OSAC-7195](https://redhat.atlassian.net/browse/OSAC-7195),
not an accepted networking design. It extends the
[NICo pilot record](nico-bmaas-pilot.md) and must be reviewed against the
[unified networking design](../../../enhancement-proposals/enhancements/OSAC-1433-unified-networking/design.md)
and [BMaaS networking design](../../../enhancement-proposals/enhancements/OSAC-1437-bmaas-networking/design.md)
before general tenant use.

## Decisions for the next pilot

- Keep the public OSAC Tenant, network, and BareMetalInstance APIs unchanged.
  A tenant requests an OSAC Subnet and an OSAC catalog item; no NICo ID or
  credential belongs in that request.
- Keep the current administrator-owned binding file for the first hardware
  test. Its key is `(OSAC tenant ID, OSAC Subnet ID)` and its value is the
  complete AWX credential ID set. The NICo credential holds the NICo tenant
  org/ID, VPC, Instance Type or target Machine, boot source, and scoped REST
  identities. The AAP role checks the OSAC IDs again before NICo calls. This
  is *manual* onboarding, not automatic Tenant creation in NICo.
- Make NICo's network virtualization type explicit in the credential. The
  standalone BMI module accepts `FLAT`, `ETHERNET_VIRTUALIZER`, or `FNN`.
  Delete and cleanup-to-Ready remain the same for all three types.
- For an optional Flat pilot, an admin must confirm that the OSAC Subnet
  CIDR represents the site's HostInband range and set
  `NICO_BMAAS_FLAT_IPV4_CIDR` to that exact CIDR. The module requires one
  unique assigned IPv4 address in it. This is a guard against reporting an
  address from another HostInband segment; it is not proof of packet delivery
  or tenant isolation.

## Onboarding: where the NICo details live

| Stage | Owner | Input and persisted result |
| --- | --- | --- |
| Site preparation | NICo/site operator | Expected Machines, DPU policy, HostInband or DPU fabric, IP/VNI pools, eligible Instance Types; Machines reach `Ready`. |
| Provider onboarding | Cloud Provider Admin | OSAC Tenant identity; NICo org/Tenant Account; compute and any required network allocations; approved NICo Site, VPC, and boot artifact. NICo Tenant Account acceptance and IdP membership may require coordination. |
| Pilot binding | Cloud Infrastructure Admin | The BMF ConfigMap maps OSAC tenant + OSAC Subnet IDs to AWX credential IDs. AWX stores NICo IDs, network mode, and credentials. The binding must survive until all associated BMIs are deleted. |
| Proposed automated binding | Provider-only reconciler | A durable `NicoTenantBinding` admin resource, keyed by OSAC Tenant and network identity, references a secret/credential by name and records the approved NICo Site, Tenant, VPC, network mode, network resource, and Instance Type or target Machine mapping. It validates readiness and publishes a condition before a BMI can use it. Exact CRD versus private API placement needs design review. |

The proposed binding is an **administrative** input. The OSAC public tenant
API cannot infer a NICo IdP org, Site, capacity allocation, VPC, or permitted
hardware pool from a Tenant name. An authorized administrator or deployment
inventory must provide that policy. Automation may create or link NICo
resources and synchronize AWX credentials after the policy is supplied; it
must not let a tenant choose another tenant's NICo IDs or pass provider tokens
in BMI fields. Prefer a stable credential name and resolved AWX ID over the
current deployment-specific numeric ID. Rotation must preserve the remote
tenant identity used for existing BMIs and keep deletion possible.

## One BMI lifecycle shared by Flat and DPU sites

1. Fulfillment authenticates the OSAC tenant and validates its requested
   catalog item, Subnet, and any SecurityGroup reference. The BMI CR keeps the
   `osac.openshift.io/tenant` and `osac.openshift.io/owner-reference`
   annotations.
2. BMF selects the exact administrator binding, installs its finalizer and
   durable one-create-attempt marker, then launches AAP with that binding's
   credentials. Missing or mismatched bindings fail before a NICo POST.
3. AAP checks the configured NICo VPC belongs to the expected NICo Tenant and
   has the configured network virtualization type. It creates or adopts one
   Instance identified by the BMI UID label. In a pool, NICo selects an eligible
   Machine from the approved Instance Type allocation. For a one-tray pilot,
   AAP requests the administrator-approved target Machine ID; NICo requires
   effective targeted Instance creation capability for that Site.
4. AAP observes the Instance, reports its Instance ID, Machine ID, and one
   unambiguous assigned IP. BMF persists the IDs and reports BMI progress.
   NICo `Ready` alone does not prove that a guest OS installed or that packets
   reach it; a production readiness contract needs a boot/health signal.
5. On OSAC delete, AAP checks the saved IDs and ownership label, requests
   deletion, and waits for Instance absence and the original Machine to be
   unassigned `Ready` without blocking health reports. BMF retains its
   finalizer on an uncertain result or cleanup failure.

### Flat, zero-DPU site

The site operator declares HostInband segments and binds them to the approved
Flat VPC. OSAC's BMI adapter sends `autoNetwork: true` and **no** `interfaces`.
NICo resolves the host's HostInband interfaces and allocates addresses. The
pilot picks one IPv4 address inside the bound OSAC Subnet CIDR. If there is
none or more than one, BMI provisioning fails closed and keeps the NICo IDs
for investigation.

NICo does not program isolation, routing, or NSG enforcement for Flat VPCs.
The site's physical/SDN fabric manager must own those functions. The first
Launchpad proof must be labeled as an allocation/boot/cleanup pilot until
reachability and cross-tenant packet isolation are independently verified.
An OSAC SecurityGroup must not be advertised as enforced merely because the
resource exists or NICo stores an NSG association.

### DPU-backed site

The provider allocates a NICo Tenant Account, Instance Type capacity, and a
tenant IP Block. The tenant has an FNN VPC with a VPC Prefix, or an ETV VPC
with an IPv4 Subnet. AAP sends one explicit physical Instance interface using
the corresponding NICo resource ID. NICo applies the per-VPC DPU VRF and
reports network convergence before the Instance is `Ready`. The current
standalone BMI role can consume a **pre-created** NICo VPC/Prefix or Subnet;
the real DPU path remains unproven in OSAC.

To make OSAC's own networking API create those resources, implement a NICo
fabric manager registration and AAP roles through the existing NetworkClass
dispatcher. Map an OSAC VirtualNetwork to a NICo VPC, an OSAC Subnet to a
NICo FNN VPC Prefix or ETV Subnet, and an OSAC SecurityGroup to a NICo NSG
only after checking rule semantics and propagation. Persist OSAC UID to NICo
ID ownership in private provider state, reconcile idempotently, and block
parent deletion while dependent BMIs exist. Design and test ExternalIP,
ExternalIPAttachment, and NATGateway separately; their current OSAC semantics
do not automatically map to NICo APIs.

## What is shared, and what is separate

The tenant-facing BMI API, authorization, binding lookup, AAP launch,
Instance/Machine tracking, and cleanup gate are shared. The Instance network
request differs by NICo VPC type. The **fabric manager differs by data-plane
owner**: NICo drives the DPU overlay; a Flat site requires the operator's
physical/SDN fabric. The accepted OSAC design currently permits one
NetworkClass/fabric manager per deployment, so offering Flat and DPU
networking in one deployment needs an explicit design extension or separate
deployment profiles. A single `fabricManager: nico` must not claim to enforce
Flat VPC isolation.

NICo-managed BMI is also an intentional exception to the generic BMaaS
networking design's switch-port move and DHCP-lease discovery path: NICo owns
the host's network transition and reports its assigned address. This exception
needs design review before declaring general BMaaS networking parity.

## Delivery order and verification

1. Flat request contract: add `FLAT` mode to the BMI module and AAP role,
   reject NICo Subnet/Prefix IDs, use `autoNetwork`, and select one address
   from the configured OSAC Subnet CIDR. Unit-test Flat, ETV, and FNN bodies
   and fail-closed cases. Machine-a-Tron has DPU-backed hosts and cannot
   validate the zero-DPU data path.
2. Launchpad FNN preflight: confirm the approved DPU-backed Machine is
   unassigned `Ready`, the NICo Tenant Account permits targeted creation at
   the Site, the compute and IP Block allocations are registered, and the
   FNN VPC and VPC Prefix are Ready. Verify the OSAC Subnet represents the
   same IPv4 range, REST credentials/CA work, and a real ARM64 boot artifact
   is available before an OSAC BMI create.
3. Run one real BMI create/status/delete. Verify installed-OS/packet behavior
   separately from NICo Instance `Ready`; verify the exact original Machine
   returns to unassigned `Ready` before OSAC removes the BMI.
4. Replace manual onboarding with a provider-only binding reconciler and
   scoped credential lifecycle. This is where an admin API or CRD is needed;
   no NICo-specific public BMI fields are required.
5. Build the DPU NICo fabric manager against a pre-agreed FNN or ETV site,
   then test OSAC VN/Subnet/SG create and delete, DPU convergence, isolation,
   and cleanup. Reuse the BMI backend for CaaS workers after BMaaS is proven.

NICo reference: [Flat VPCs](https://docs.nvidia.com/infra-controller/documentation/operations-day-2/managing-vp-cs/flat-vp-cs-and-zero-dpu-hosts),
[REST flow by VPC type](https://docs.nvidia.com/infra-controller/rest-api-reference/getting-started),
and [DPU VPC virtualization](https://docs.nvidia.com/infra-controller/documentation/operations-day-2/managing-vp-cs/vpc-network-virtualization).
