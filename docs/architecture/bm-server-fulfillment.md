# Bare Metal Server Fulfillment

This page summarizes how OSAC fulfills a BareMetalInstance (BMI), with emphasis
on networking.
The normative design is
[BMaaS networking](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1437-bmaas-networking/design.md).
Tier and owner classification for tests is in the design
[testplan](https://github.com/osac-project/enhancement-proposals/blob/main/enhancements/OSAC-1437-bmaas-networking/testplan.md).

## Components

| Component | Role |
|---|---|
| fulfillment-service | Validates and stores the BMI network attachment; maps BMI network conditions to API `NETWORK_READY`; admits create/delete guards. |
| bare-metal-fulfillment-operator (BMF) | Manages inventory, host provisioning and power, records the selected MAC, creates and deletes NetworkAttachments, coordinates handoff and repair marks, and mirrors network status. |
| osac-aap | Executes the `{{ aap_prefix }}-move-network-attachment` and `{{ aap_prefix }}-query-dhcp-lease` AAP job templates using `playbook_osac_move_network_attachment.yml` and `playbook_osac_query_dhcp_lease.yml`. The playbooks invoke the selected networking role. |
| Fabric Manager | Performs provider-side Fabric operations invoked by the AAP playbooks: port and policy changes, DHCP, selected-MAC lease lookup, and port restoration on delete. |
| osac-operator BMI cleanup / feedback | Manages automatic ExternalIPAttachment lifecycle and reports status changes to fulfillment. |

Shared VirtualNetwork, Subnet, SecurityGroup, ExternalIP, and NATGateway behavior
belongs to Unified / Default Networking. Do not duplicate those contracts here;
see [Setting Up Networking with OSAC](../guides/developer/networking-guide.md)
and [networking decisions](../agent-context/networking-decisions.md).

## Lifecycle (provider networking enabled)

1. Tenant creates a BMI with at most one network attachment (or omits it for
   tenant defaults). Interface selection uses BareMetalInstanceType fabric ports.
2. BMF assigns a host and records the selected port's MAC, then provisions the OS
   on the provider-managed **provisioning network** (not an OSAC Subnet).
3. BMF creates a hub NetworkAttachment. The configured
   `{{ aap_prefix }}-move-network-attachment` AAP job template runs the apply
   playbook, which moves the port and applies policy through the selected
   networking role.
4. After the NetworkAttachment is Ready, BMF coordinates the handoff through the
   management backend. Ironic skips an extra power-off when the host is already
   off and proceeds to power it on; Metal3 uses its handoff annotation. BMF marks
   the handoff complete only after the host is on.
5. After handoff, the configured `{{ aap_prefix }}-query-dhcp-lease` AAP job
   template runs the lease-query playbook for the recorded MAC. BMF mirrors the
   accepted lease. A provisioning-network address is never presented as the
   tenant IP.
6. On delete, BMF powers the host off while still on the tenant network (when a
   NetworkAttachment exists). The configured
   `{{ aap_prefix }}-move-network-attachment` AAP job template runs the delete
   playbook in offboard mode to return the port to the provisioning network. BMF
   then deprovisions. Before releasing the host, BMF writes a **repair mark** only when provider networking is enabled and a FabricDomain
   retains a `status.jobs` entry showing that its `create_fabric_domain` job
   started while `spec.servers` lists the host. If the mark cannot be persisted,
   BMF keeps the host from being released.

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
