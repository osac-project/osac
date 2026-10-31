# Bare Metal Server Fulfillment

This page summarizes how OSAC fulfills a BareMetalInstance (BMI), with emphasis
on networking after [enhancement-proposals PR #357](https://github.com/osac-project/enhancement-proposals/pull/357).
The normative design is
[BMaaS networking](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1437-bmaas-networking/design.md).
Tier and owner classification for tests is in the design
[testplan](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1437-bmaas-networking/testplan.md).

**Accepted target vs shipping code.** The design describes the accepted contract.
It does not assert that every constraint is already enforced. Completed network
attachment baseline work is under closed
[OSAC-1448](https://redhat.atlassian.net/browse/OSAC-1448) and
[OSAC-1510](https://redhat.atlassian.net/browse/OSAC-1510). The remaining PR #357
BMaaS delta is tracked by
[OSAC-6818](https://redhat.atlassian.net/browse/OSAC-6818) (implementation) and
[OSAC-6820](https://redhat.atlassian.net/browse/OSAC-6820) (deployed QE). Do not
treat remaining-delta behavior as currently supported product behavior until
those land with evidence.

## Components

| Component | Role |
|---|---|
| fulfillment-service | Validates and stores the BMI network attachment; maps BMI network conditions to API `NETWORK_READY`; admits create/delete guards. |
| bare-metal-fulfillment-operator (BMF) | Inventory, OS provisioning, power, MAC resolution, NetworkAttachment create/delete, handoff reboot, offboard power-off, repair mark, status mirroring. **Never** calls a Fabric Manager, switch, or DHCP API. |
| osac-operator networking controller | Runs every BMaaS Fabric task on the NetworkAttachment (`apply_network_attachment`, `delete_network_attachment`, `query_dhcp_lease`). |
| Fabric Manager (AAP) | Binds ports to SecurityGroup policy, moves ports between provisioning and tenant segments, serves DHCP, returns the selected MAC's lease; restores ports on delete. Never reboots the host. |
| osac-operator BMI cleanup / feedback | Automatic ExternalIPAttachment lifecycle and status feedback to fulfillment. |

Shared VirtualNetwork, Subnet, SecurityGroup, ExternalIP, and NATGateway behavior
belongs to Unified / Default Networking. Do not duplicate those contracts here;
see [Setting Up Networking with OSAC](../guides/developer/networking-guide.md)
and [networking decisions](../agent-context/networking-decisions.md).

## Lifecycle (provider networking enabled)

1. Tenant creates a BMI with at most one network attachment (or omits it for
   tenant defaults). Interface selection uses BareMetalInstanceType fabric ports.
2. BMF assigns a host and records the selected port's MAC, then provisions the OS
   on the provider-managed **provisioning network** (not an OSAC Subnet).
3. BMF creates a hub NetworkAttachment. The networking controller runs Fabric
   `apply_network_attachment` (port move and policy). BMF does not launch that job.
4. After the NetworkAttachment is Ready, BMF requests one **handoff reboot**.
5. The networking controller runs `query_dhcp_lease` for the recorded MAC. BMF
   mirrors the tenant address into status. A provisioning-network address is never
   presented as the tenant IP.
6. On delete, BMF powers the host off while still on the tenant network (when a
   NetworkAttachment exists), deletes the NetworkAttachment (Fabric restore to
   provisioning), deprovisions, and may write a **repair mark** when a
   FabricDomain still lists the host.

With **provider networking disabled**, host provisioning continues on the
provisioning network; OSAC skips NetworkAttachment, handoff, and lease discovery.
Readiness then attests OS provisioning only.

## Operator and user guides

- Tenant BMI create (DiskImage + networking):
  [Provisioning a Bare Metal Instance with a DiskImage](../guides/developer/baremetalinstance-guide.md)
- BMF networking and MAC inventory mapping:
  [bare-metal-fulfillment-operator README](../../bare-metal-fulfillment-operator/README.md)
- Integration test tiers for BMF:
  [INTEGRATION-TESTING — bare-metal-fulfillment-operator](../INTEGRATION-TESTING.md#bare-metal-fulfillment-operator)
