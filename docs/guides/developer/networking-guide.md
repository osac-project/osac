# Setting Up Networking with OSAC

This guide walks through creating the networking resources required before provisioning
compute instances: VirtualNetworks and Subnets. It also covers SecurityGroups, which are
optional and only needed when you want to apply firewall rules. It assumes you already
have a Tenant in `Ready` state (see [Tenant Setup Guide](tenant-setup.md)).

Each step shows the `osac` CLI command and the equivalent gRPC / REST API calls so that
the same guide works for both CLI users and application developers integrating with
OSAC's API.

## Contents

- [Prerequisites](#prerequisites)
- [Overview](#overview)
- [Step 1: Identify the NetworkClass](#step-1-identify-the-networkclass)
- [Step 2: Create a VirtualNetwork](#step-2-create-a-virtualnetwork)
- [Step 3: Create a Subnet](#step-3-create-a-subnet)
- [Step 4: Create a SecurityGroup (optional)](#step-4-create-a-securitygroup-optional)
- [Resource lifecycle and updates](#resource-lifecycle-and-updates)
- [Troubleshooting](#troubleshooting)
- [Next steps](#next-steps)

---

## Prerequisites

**CLI users:**

- The `osac` CLI is installed and configured (API endpoint, authentication token).

**API users:**

- `grpcurl` (for gRPC) or `curl` (for REST) is installed.
- Set the following environment variables for the examples in this guide:

  ```bash
  export OSAC_API="<api-endpoint>"    # host:port (e.g., fulfillment-api.osac.svc:443)
  export TOKEN="<authentication-token>"

  # Skip TLS verification in development environments:
  # export GRPCURL_FLAGS="-insecure"
  # export CURL_FLAGS="-k"
  ```

**Both:**

- A Tenant is in `Ready` state (see [Tenant Setup Guide](tenant-setup.md)).

---

## Overview

OSAC networking follows a hierarchical model:

```text
NetworkClass (platform-defined, read-only)
└── VirtualNetwork (tenant L2 network with CIDR)
      ├── Subnet (CIDR range within VirtualNetwork)
      └── SecurityGroup (optional — firewall rules scoped to VirtualNetwork)
```

- A **NetworkClass** is a platform-level resource that defines the type of network
  infrastructure available. It is read-only for tenants.
- A **VirtualNetwork** is a tenant-scoped L2 network associated with a NetworkClass.
  It defines an overall IPv4 CIDR range.
- A **Subnet** is a CIDR range within a VirtualNetwork. Compute instances attach to
  subnets via network attachments.
- A **SecurityGroup** defines ingress and egress firewall rules scoped to a
  VirtualNetwork. Security groups follow a **default-deny** policy — traffic is blocked
  unless a rule explicitly allows it. Rules are stateful, so return traffic for
  established connections is automatically allowed.

---

## Step 1: Identify the NetworkClass

List the available network classes to find the one you will use for your VirtualNetwork:

**CLI:**

```bash
osac get networkclass
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  $OSAC_API osac.public.v1.NetworkClasses/List
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  "https://$OSAC_API/api/fulfillment/v1/network_classes"
```

Note the `ID` of the NetworkClass you want to use — you will need it in the next step.

---

## Step 2: Create a VirtualNetwork

Create a VirtualNetwork associated with the NetworkClass. The `--ipv4-cidr` defines the
overall IP address range for the network:

**CLI:**

```bash
osac create virtualnetwork \
  --name <virtualnetwork_name> \
  --network-class <networkclass-id> \
  --ipv4-cidr 192.168.0.0/16
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" -d '{
  "object": {
    "metadata": {
      "name": "<virtualnetwork_name>"
    },
    "spec": {
      "network_class": "<networkclass-id>",
      "ipv4_cidr": "192.168.0.0/16",
      "capabilities": {
        "enable_ipv4": true
      }
    }
  }
}' $OSAC_API osac.public.v1.VirtualNetworks/Create
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
  "metadata": {
    "name": "<virtualnetwork_name>"
  },
  "spec": {
    "network_class": "<networkclass-id>",
    "ipv4_cidr": "192.168.0.0/16",
    "capabilities": {
      "enable_ipv4": true
    }
  }
}' "https://$OSAC_API/api/fulfillment/v1/virtual_networks"
```

> **Note (API users):** The REST body is the VirtualNetwork object directly.
> The gRPC request wraps it in an `object` field. The `capabilities` field is
> derived automatically by the CLI; when calling the API directly, set
> `enable_ipv4`, `enable_ipv6`, or `enable_dual_stack` to match the CIDRs
> you provide.

Note the VirtualNetwork `ID` from the output — you will need it when creating Subnets
and SecurityGroups.

Wait for the VirtualNetwork to reach `READY` state. Provisioning is
asynchronous — rerun the command until the status shows `READY` before
proceeding:

**CLI:**

```bash
osac get virtualnetwork <virtualnetwork_name>
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  -d '{"id": "<virtualnetwork-id>"}' \
  $OSAC_API osac.public.v1.VirtualNetworks/Get
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  "https://$OSAC_API/api/fulfillment/v1/virtual_networks/<virtualnetwork-id>"
```

---

## Step 3: Create a Subnet

Create a Subnet under the VirtualNetwork. The Subnet CIDR must fall within the
VirtualNetwork's CIDR range (e.g., `192.168.1.0/24` is within `192.168.0.0/16`):

**CLI:**

```bash
osac create subnet \
  --name <subnet_name> \
  --virtual-network <virtualnetwork-id> \
  --ipv4-cidr 192.168.1.0/24
```

> **Note:** The CLI accepts a VirtualNetwork name or ID for the `--virtual-network`
> flag and resolves it to the VirtualNetwork's UUID. When calling the API directly,
> use the VirtualNetwork UUID.

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" -d '{
  "object": {
    "metadata": {
      "name": "<subnet_name>"
    },
    "spec": {
      "virtual_network": "<virtualnetwork-id>",
      "ipv4_cidr": "192.168.1.0/24"
    }
  }
}' $OSAC_API osac.public.v1.Subnets/Create
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
  "metadata": {
    "name": "<subnet_name>"
  },
  "spec": {
    "virtual_network": "<virtualnetwork-id>",
    "ipv4_cidr": "192.168.1.0/24"
  }
}' "https://$OSAC_API/api/fulfillment/v1/subnets"
```

Note the Subnet `ID` from the output — you will need it when creating compute instances.

Wait for the Subnet to reach `READY` state. Provisioning is asynchronous —
rerun the command until the status shows `READY` before proceeding:

**CLI:**

```bash
osac get subnet <subnet_name>
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  -d '{"id": "<subnet-id>"}' \
  $OSAC_API osac.public.v1.Subnets/Get
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  "https://$OSAC_API/api/fulfillment/v1/subnets/<subnet-id>"
```

---

## Step 4: Create a SecurityGroup (optional)

SecurityGroups are optional. Create one only when you need firewall rules to control
traffic to and from compute instances. They are scoped to a VirtualNetwork and follow a
**default-deny** policy: all traffic is blocked unless explicitly allowed by a rule.

### Create a security group with rules

Use `--ingress` and `--egress` flags to define rules. Each rule is a comma-separated
list of `key=value` pairs:

> **Warning — development only:** The examples below use `0.0.0.0/0` (all
> IPv4 addresses) for convenience. This exposes ports to the public
> internet. In production, replace `0.0.0.0/0` with a restricted CIDR
> that limits access to your administrator or application networks (e.g.,
> `10.0.0.0/8` or your corporate VPN range).

**CLI:**

```bash
osac create securitygroup \
  --name <securitygroup_name> \
  --virtual-network <virtualnetwork-id> \
  --ingress protocol=tcp,port-from=22,port-to=22,ipv4-cidr=0.0.0.0/0 \
  --ingress protocol=tcp,port-from=80,port-to=80,ipv4-cidr=0.0.0.0/0 \
  --ingress protocol=tcp,port-from=443,port-to=443,ipv4-cidr=0.0.0.0/0 \
  --egress protocol=all,ipv4-cidr=0.0.0.0/0
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" -d '{
  "object": {
    "metadata": {
      "name": "<securitygroup_name>"
    },
    "spec": {
      "virtual_network": "<virtualnetwork-id>",
      "ingress": [
        {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "0.0.0.0/0"},
        {"protocol": "PROTOCOL_TCP", "port_from": 80, "port_to": 80, "ipv4_cidr": "0.0.0.0/0"},
        {"protocol": "PROTOCOL_TCP", "port_from": 443, "port_to": 443, "ipv4_cidr": "0.0.0.0/0"}
      ],
      "egress": [
        {"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}
      ]
    }
  }
}' $OSAC_API osac.public.v1.SecurityGroups/Create
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
  "metadata": {
    "name": "<securitygroup_name>"
  },
  "spec": {
    "virtual_network": "<virtualnetwork-id>",
    "ingress": [
      {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "0.0.0.0/0"},
      {"protocol": "PROTOCOL_TCP", "port_from": 80, "port_to": 80, "ipv4_cidr": "0.0.0.0/0"},
      {"protocol": "PROTOCOL_TCP", "port_from": 443, "port_to": 443, "ipv4_cidr": "0.0.0.0/0"}
    ],
    "egress": [
      {"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}
    ]
  }
}' "https://$OSAC_API/api/fulfillment/v1/security_groups"
```

> **Note (API users):** Protocol values in the API use the enum names:
> `PROTOCOL_TCP`, `PROTOCOL_UDP`, `PROTOCOL_ICMP`, `PROTOCOL_ALL`. The CLI
> accepts the short forms `tcp`, `udp`, `icmp`, `all`.

The example above allows inbound SSH (port 22), HTTP (port 80), and HTTPS (port 443)
from any IPv4 address, and permits all outbound traffic.

Note the SecurityGroup `ID` from the output — you will need it when creating compute
instances.

Wait for the SecurityGroup to reach `READY` state. Provisioning is
asynchronous — rerun the command until the status shows `READY` before
proceeding:

**CLI:**

```bash
osac get securitygroup <securitygroup_name>
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  -d '{"id": "<securitygroup-id>"}' \
  $OSAC_API osac.public.v1.SecurityGroups/Get
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  "https://$OSAC_API/api/fulfillment/v1/security_groups/<securitygroup-id>"
```

### Rule format

Each `--ingress` or `--egress` flag takes a rule in `key=value,...` format with the
following keys:

| Key | Required | Description | API enum / field |
|-----|----------|-------------|------------------|
| `protocol` | Yes | One of: `tcp`, `udp`, `icmp`, `all` | `PROTOCOL_TCP`, `PROTOCOL_UDP`, `PROTOCOL_ICMP`, `PROTOCOL_ALL` |
| `port-from` | For `tcp`/`udp` | Start of port range (1–65535) | `port_from` |
| `port-to` | For `tcp`/`udp` | End of port range (1–65535), must be >= `port-from` | `port_to` |
| `ipv4-cidr` | No | IPv4 CIDR block (e.g., `0.0.0.0/0` for all, `10.0.0.0/8` for private) | `ipv4_cidr` |
| `ipv6-cidr` | No | IPv6 CIDR block (e.g., `::/0` for all) | `ipv6_cidr` |

- For `icmp` and `all` protocols, port fields are ignored.
- Both `ipv4-cidr` and `ipv6-cidr` can be set on a single rule to create a dual-stack
  rule.
- The `--ingress` and `--egress` flags can each be repeated to add multiple rules.
  In the API, use arrays of rule objects.

### Examples

Allow only SSH from a specific network:

**CLI:**

```bash
osac create securitygroup \
  --name ssh-only \
  --virtual-network <virtualnetwork-id> \
  --ingress protocol=tcp,port-from=22,port-to=22,ipv4-cidr=10.0.0.0/8
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" -d '{
  "object": {
    "metadata": {"name": "ssh-only"},
    "spec": {
      "virtual_network": "<virtualnetwork-id>",
      "ingress": [
        {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "10.0.0.0/8"}
      ]
    }
  }
}' $OSAC_API osac.public.v1.SecurityGroups/Create
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
  "metadata": {"name": "ssh-only"},
  "spec": {
    "virtual_network": "<virtualnetwork-id>",
    "ingress": [
      {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "10.0.0.0/8"}
    ]
  }
}' "https://$OSAC_API/api/fulfillment/v1/security_groups"
```

Allow all traffic (open security group):

> **Warning — development only:** This creates a fully open security
> group. Do not use `0.0.0.0/0` with `PROTOCOL_ALL` in production.

**CLI:**

```bash
osac create securitygroup \
  --name allow-all \
  --virtual-network <virtualnetwork-id> \
  --ingress protocol=all,ipv4-cidr=0.0.0.0/0 \
  --egress protocol=all,ipv4-cidr=0.0.0.0/0
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" -d '{
  "object": {
    "metadata": {"name": "allow-all"},
    "spec": {
      "virtual_network": "<virtualnetwork-id>",
      "ingress": [{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
      "egress": [{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}]
    }
  }
}' $OSAC_API osac.public.v1.SecurityGroups/Create
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
  "metadata": {"name": "allow-all"},
  "spec": {
    "virtual_network": "<virtualnetwork-id>",
    "ingress": [{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}],
    "egress": [{"protocol": "PROTOCOL_ALL", "ipv4_cidr": "0.0.0.0/0"}]
  }
}' "https://$OSAC_API/api/fulfillment/v1/security_groups"
```

Allow a range of UDP ports:

> **Warning — development only:** This example uses `0.0.0.0/0` (all
> IPv4 addresses). In production, replace it with a restricted CIDR
> that limits access to trusted networks.

**CLI:**

```bash
osac create securitygroup \
  --name udp-range \
  --virtual-network <virtualnetwork-id> \
  --ingress protocol=udp,port-from=5000,port-to=5100,ipv4-cidr=0.0.0.0/0
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" -d '{
  "object": {
    "metadata": {"name": "udp-range"},
    "spec": {
      "virtual_network": "<virtualnetwork-id>",
      "ingress": [
        {"protocol": "PROTOCOL_UDP", "port_from": 5000, "port_to": 5100, "ipv4_cidr": "0.0.0.0/0"}
      ]
    }
  }
}' $OSAC_API osac.public.v1.SecurityGroups/Create
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
  "metadata": {"name": "udp-range"},
  "spec": {
    "virtual_network": "<virtualnetwork-id>",
    "ingress": [
      {"protocol": "PROTOCOL_UDP", "port_from": 5000, "port_to": 5100, "ipv4_cidr": "0.0.0.0/0"}
    ]
  }
}' "https://$OSAC_API/api/fulfillment/v1/security_groups"
```

### Inspect a security group

**CLI:**

```bash
osac describe securitygroup <securitygroup-name-or-id>
```

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  -d '{"id": "<securitygroup-id>"}' \
  $OSAC_API osac.public.v1.SecurityGroups/Get
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -H "Authorization: Bearer $TOKEN" \
  "https://$OSAC_API/api/fulfillment/v1/security_groups/<securitygroup-id>"
```

This displays the security group's state and all ingress and egress rules.

---

## Resource lifecycle and updates

Networking resources are immutable after creation. Read means `List` and `Get`;
there is no `Update`/`Patch` operation for a networking resource's specification
or metadata, except for the SecurityGroup rule replacement described below. To
change a VirtualNetwork, Subnet, NATGateway, ExternalIP, or
ExternalIPAttachment, delete it and create a replacement:

- **Deletion is guarded.** A resource can only be deleted when no other active
  resource references it. If a Subnet is attached to compute instances, for
  example, the delete request is rejected with a precondition error until the
  dependents are removed.
- **Replacements observe the same gates.** A replacement can only be created
  when every referenced resource is in its terminal ready state. Remove the
  dependents, delete the old resource, create the replacement, and wait for it
  to reach `READY`, then re-create the dependent workloads with attachments to
  the replacement (attachments are set at workload creation and cannot be
  changed in place). Always preserve tenant
  attribution, and never bypass finalizers or guards.

The one scoped exception is SecurityGroup rules: `SecurityGroups.Update`
replaces the complete ingress and egress rule lists of an existing group. The
group's identity, name, parent VirtualNetwork, metadata, and status are not
changed by this operation
([OSAC-5679](https://redhat.atlassian.net/browse/OSAC-5679)).

**gRPC:**

```bash
grpcurl $GRPCURL_FLAGS -H "Authorization: Bearer $TOKEN" -d '{
  "object": {
    "id": "<securitygroup-id>",
    "spec": {
      "ingress": [
        {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "10.0.0.0/8"}
      ],
      "egress": [
        {"protocol": "PROTOCOL_ALL", "ipv4_cidr": "10.0.0.0/8"}
      ]
    }
  }
}' $OSAC_API osac.public.v1.SecurityGroups/Update
```

**REST:**

```bash
curl -fsS $CURL_FLAGS -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{
  "id": "<securitygroup-id>",
  "spec": {
    "ingress": [
      {"protocol": "PROTOCOL_TCP", "port_from": 22, "port_to": 22, "ipv4_cidr": "10.0.0.0/8"}
    ],
    "egress": [
      {"protocol": "PROTOCOL_ALL", "ipv4_cidr": "10.0.0.0/8"}
    ]
  }
}' "https://$OSAC_API/api/fulfillment/v1/security_groups/<securitygroup-id>"
```

> **Note:** The complete rule lists are applied as supplied — send every rule
> you want to keep, not only the changed ones. The server applies only the
> ingress and egress paths, so no field mask is needed. The CLI `edit`
> command supports only SecurityGroups for the same reason; `osac edit` on any
> other networking resource is refused before any write.

Storing a rule change does not guarantee the fabric manager has reprogrammed
backend ACLs — that reconciliation gap is tracked separately
([OSAC-4888](https://redhat.atlassian.net/browse/OSAC-4888)). Broader selective
updates to other networking resources or fields are future scope
([OSAC-5281](https://redhat.atlassian.net/browse/OSAC-5281)).

---

## Troubleshooting

| Symptom | Cause | What to do |
|---------|-------|------------|
| `osac edit` fails with `object type "osac.public.v1.<Type>" is immutable; updates are not supported` | The CLI refuses to edit immutable networking resources before any write | Delete the resource and create a replacement |
| gRPC `Update` on a networking service reports `Unimplemented` | All networking services except SecurityGroups have no Update RPC | Use delete and recreate |
| `SecurityGroups/Update` rejects with `InvalidArgument` (`object is mandatory`, `object identifier is mandatory`, or `object spec is mandatory`) | The scoped rule update requires the object, its ID, and a spec | Send the object with the group ID and the complete ingress and egress lists |
| Delete rejected with a precondition error naming a blocking resource type | Active dependents still reference the resource | Remove the dependents first, then delete |
| Create rejected with a precondition error | A referenced resource is not ready | Wait for the referenced resource to reach its terminal ready state, then retry |
| Create rejected with an invalid-argument error naming a dependency that has been deleted | A referenced resource has been deleted | Recreate the deleted dependency, wait for it to become `READY`, then retry |

The CLI messages and gRPC codes above reflect implemented behavior. For
operations exposed through REST, REST responses surface the corresponding
rejections with different status codes.

---

## Next steps

With networking in place, you can create compute instances that attach to your Subnets
(and optionally SecurityGroups). See [Creating a VM with OSAC](computeinstance-guide.md).
