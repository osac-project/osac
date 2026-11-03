# Bare Metal Server Fulfillment

This page describes the current BareMetalInstance fulfillment flow, with
emphasis on networking.

## Components

| Component | Role |
|---|---|
| fulfillment-service | Validates and stores BMI network attachments and reports network conditions through the API. |
| bare-metal-fulfillment-operator (BMF) | Assigns inventory hosts, provisions and powers them, runs network and DHCP lease jobs, coordinates handoff, records network status, and performs host cleanup. |
| osac-aap | Runs the provisioning, network, and lease-query jobs configured for BMF. |
| Fabric Manager | Applies provider-side port and policy changes, assigns tenant addresses through DHCP, looks up leases, and restores ports during network cleanup. |
| osac-operator | Manages automatic ExternalIP resources and coordinates their cleanup before BMF releases a host. |

Shared VirtualNetwork, Subnet, SecurityGroup, ExternalIP, and NATGateway
behavior is described in [Setting Up Networking with OSAC](../guides/developer/networking-guide.md).

## Lifecycle

1. A tenant creates a BMI with at most one network attachment or uses tenant
   defaults. Fulfillment validates and stores the selected network.
2. BMF assigns an inventory host and provisions the operating system on the
   provider-managed provisioning network.
3. After OS provisioning, BMF starts the configured network job. The Fabric
   Manager moves the selected port to the tenant Subnet and applies policy.
4. After network provisioning succeeds, BMF coordinates handoff through the
   management backend. An online host receives one reboot so its operating
   system requests DHCP on the tenant network. A powered-off host completes
   handoff and receives its tenant address on the next power-on.
5. BMF starts DHCP lease discovery after handoff. When inventory provides a
   logical-port MAC mapping, BMF passes the subnet-to-MAC mapping to the query
   job. The query also supports server-name matching when a host has no complete
   mapping. BMF records the discovered tenant address in network status.
6. During deletion, BMF waits for OSAC network cleanup. It powers off the host
   while its port remains on the tenant network, returns the port to the
   provisioning network, deprovisions the host, and releases it to inventory.

When the networking provider is not configured, BMF marks network provisioning
and handoff as skipped.

## Guides

- [Provisioning a Bare Metal Instance with a DiskImage](../guides/developer/baremetalinstance-guide.md)
- [BMF networking and MAC inventory mappings](../../bare-metal-fulfillment-operator/README.md)
- [Integration testing for BMF](../INTEGRATION-TESTING.md#bare-metal-fulfillment-operator)
