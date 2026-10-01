# Manage Ethernet FabricDomains

Phase 1 provisions administrator-selected physical servers through Netris Server
Clusters. An administrator supplies exact Netris inventory hostnames and one
VirtualNetwork. OSAC resolves each host's BareMetalInstanceType, selects its
private Ethernet template binding, and provisions the domain in the VN's VPC.

This guide describes the development API. InfiniBand, NVLink, automatic workload
membership, VM device attachment, and automatic template generation are not
implemented by this phase.

## Ownership and prerequisites

FabricDomain creation, resize, and deletion are infrastructure-admin operations.
Tenant users can inspect objects visible to their tenant; they cannot enroll
arbitrary physical hosts. The onboarding inventory identifies hardware, not
tenant allocation or entitlement. Administrators must verify that the servers
are assigned to the intended tenant and available for this domain.

The deployment needs:

- A registered Netris fabric manager advertising Ethernet east-west support.
- The fulfillment controller, FabricDomain operator controller, and networking
  provisioning enabled, with private API and AAP access configured.
- AAP job templates `osac-create-fabric-domain` and
  `osac-delete-fabric-domain`, registered by config-as-code. Their current
  Netris implementation creates/deletes Server Clusters.
- A Ready VirtualNetwork with a positive numeric Netris VPC ID, in the same
  tenant as the FabricDomain.
- Netris inventory hosts and an administrator-maintained Server Cluster
  template. Template NIC names must match the participating hosts.

NetworkClass supplies fabric capability and network policy. It no longer stores
`east_west_config.ethernet_ew.template_id`. The hardware binding belongs to the
BareMetalInstanceType and is private API configuration.

The template determines which networks and ports Netris programs. When ordinary
Subnet attachments already own north-south ports, select a template that leaves
those ports and networks under their existing owner. A shared VPC does not make
a template-created V-Net identical to an existing OSAC Subnet's V-Net.

Netris permits a server to be dedicated to only one Server Cluster. Its shared
endpoint mode has different behavior, including ignoring `l3vpn`; this Phase 1
path uses dedicated servers. Also, separate FabricDomain objects in one VPC do
not by themselves establish separate routed isolation boundaries. Validate the
VPC routing policy against the required isolation. See the Netris
[Server Cluster](https://www.netris.ai/docs/en/latest/server-cluster.html) and
[V-Net](https://www.netris.ai/docs/en/latest/vnet.html) documentation.

## Configure hardware bindings

Create the normal BareMetalInstanceType hardware catalog entries through the
private API, or edit existing entries using a CLI session logged into the
private API. Add this fragment to each applicable type's `spec`:

```yaml
fabric_bindings:
  ethernet_ew:
    netris:
      network_class: "<network-class-id>"
      template_id: "42"
```

Use the actual NetworkClass ID and a canonical positive decimal template ID.
The NetworkClass ID scopes the backend identifier to this deployment's class.
Different instance types may use the same template. All members of one
FabricDomain must resolve to the same scoped template.

No `instance_type` or template ID is added to FabricDomain. Tenant-facing catalog
responses do not expose the private hardware bindings. NIC roles are hardware
metadata; Phase 1 does not generate or edit Netris templates from those roles.

## Register exact host identities

Maintain the following map in the installation's Helm values:

```yaml
operator:
  fabricDomainInventory:
    gpu-01.example.com: "gpu-type-id"
    gpu-02.example.com: "gpu-type-id"
    gpu-03.example.com: "gpu-type-id"
```

Keys must exactly match the Netris inventory hostnames. Values are shared
BareMetalInstanceType IDs, not labels or instance IDs. A nonempty map renders
`ConfigMap/osac-fabric-domain-inventory` in the operator's networking namespace.
Only infrastructure administrators and onboarding automation should be able to
write it. Keep using the existing installation values and upgrade procedure;
see [installer onboarding](../../../osac-installer/README.md#fabricdomain-admin-onboarding-phase-1).

The operator reads this map on reconciliation and watches it for changes. It
does not reverse-match `host_label_selector`, infer a type from a hostname, or
fall back to a NetworkClass template. Missing inventory, missing/deleting types,
wrong NetworkClass bindings, and incompatible templates block provisioning.
The AAP role independently verifies that every requested server resolves
unambiguously in the Netris inventory before changing membership.

This explicit map is the Phase 1 onboarding bridge. It can exist before an OS
is installed or a BareMetalInstance has been allocated. Automatic allocation
identity and tenant-controlled membership are later work.

## Create and inspect a domain

Use an administrator's authenticated public API CLI session for these commands.
Select the tenant explicitly and use the VN ID when names could be ambiguous:

```bash
osac --tenant tenant-a create fabricdomain \
  --name training-ew \
  --type ethernet_ew \
  --virtual-network <virtual-network-id> \
  --servers gpu-01.example.com,gpu-02.example.com

osac --tenant tenant-a get fabricdomains
osac --tenant tenant-a describe fabricdomain training-ew
```

Fulfillment persists the API object and its hub assignment, then creates a
FabricDomain CR on the VN's hub. The operator waits for VN readiness, resolves
hardware bindings, records the selected template/VPC/region, and launches AAP.
Feedback copies the conditions and backend/member status to the API.

`Ready=True` means the requested Server Cluster job succeeded with valid backend
identity. Phase 1 member states follow the whole job; they do not independently
verify NIC connectivity, RoCE performance, or per-server attachment health.
Disabled networking provisioning is not reported as a provisioned domain.

## Observe provisioning

The operator exports these Prometheus metrics:

| Metric | Labels | Meaning |
|---|---|---|
| `osac_fabric_domains_total` | `type`, `tenant` | Current non-deleting FabricDomain objects in the configured networking namespace, including unready or unmanaged objects. |
| `osac_fabric_domain_provisioning_duration_seconds` | `type` | Time from CR creation to its first persisted Ready state. Resize operations do not add samples. |
| `osac_fabric_domain_provisioning_failures_total` | `type`, `reason` | Persisted transitions into Failed or to a different failure reason; repeated reconciles do not increment it. |

Kubernetes events report `FabricDomainProvisioned` (Normal) on the first Ready
transition, `FabricDomainProvisioningFailed` (Warning) when a persisted failure
reason changes, and `FabricDomainDeleted` (Normal) after successful cleanup.
The metrics and events describe control-plane state; they do not measure
per-server attachment health or dataplane reachability.

## Resize and delete

Edit only the server list when changing membership:

```bash
osac --tenant tenant-a edit fabricdomain training-ew
```

For example, change `spec.servers` from two hosts to three after onboarding
`gpu-03.example.com`. The operator updates the existing Server Cluster and
waits for the new job. An older job completing does not make newly requested
members Active. Type and VirtualNetwork are immutable, and the server list
must remain nonempty in Phase 1.

The selected template, NetworkClass, VPC, and region are recorded before the
first backend launch. Editing the catalog or inventory to select a different
binding blocks further provisioning. Restore the compatible configuration or
plan deletion and recreation; a catalog edit is not an in-place fabric migration.

```bash
osac --tenant tenant-a delete fabricdomain training-ew
```

Wait for deletion to finish before reusing the servers or deleting the VN.
Cleanup uses recorded backend identity and retains finalizers until the backend
operation completes. The VN is protected while domains still reference it.
Hardware inventory and instance type lookups are not prerequisites for cleanup.

## Diagnose failures

Inspect API conditions first. For additional job and binding details, inspect
the matching hub CR by its `osac.openshift.io/fabricdomain-uuid` label, using the
configured networking namespace:

```bash
kubectl -n osac get fabricdomains \
  -l osac.openshift.io/fabricdomain-uuid=<fabric-domain-id> -o yaml
kubectl -n osac get events --field-selector involvedObject.kind=FabricDomain
```

| Condition or symptom | Action |
|---|---|
| No hub CR yet | Check the fulfillment controller, VN hub assignment, and API tenant metadata. |
| `VirtualNetworkNotReady` | Restore the VN's provisioning and numeric VPC identity. |
| `UnsupportedNetworkClass` | Check Netris manager registration and Ethernet EW capability. |
| `InvalidHardwareBinding` | Correct the exact hostname map, shared instance type, class scope, or template compatibility. |
| `BackendBindingChanged` | Restore the recorded binding or plan domain recreation. |
| `TenantMismatch` | Correct the administrative request; the domain and VN must belong to the same tenant. |
| `ProvisioningDisabled` | Enable networking provisioning and configure AAP access. |
| AAP failure | Inspect the recorded job and Netris result; correct the cause and allow reconciliation to retry. |
| `UnresolvedProvisioningIntent` | Investigate AAP before changing status: a create may have launched without its job ID being persisted. Do not remove the finalizer merely to unblock deletion. |
| `UnresolvedBackendBinding` | Verify the existing backend's template/VPC/site before restoring missing administrative status. |

Provisioning conditions describe control-plane progress. Validate actual EW
connectivity, north-south preservation, and cross-tenant isolation in the target
environment. The deployed lifecycle, coexistence, and failure-recovery checks
are tracked by [OSAC-4786](https://redhat.atlassian.net/browse/OSAC-4786),
[OSAC-4788](https://redhat.atlassian.net/browse/OSAC-4788), and
[OSAC-4789](https://redhat.atlassian.net/browse/OSAC-4789).
