# netris.controller

Ansible collection for managing Netris controller resources via the REST API (V2).

## Requirements

- Netris controller deployed and reachable
- Valid Netris credentials (username/password)
- `ansible.builtin.uri` for HTTP calls

## CRUD Task Pattern

Each role follows a consistent CRUD pattern with task files:

- `create.yaml` — Create the resource
- `read.yaml` — Query / read the resource
- `delete.yaml` — Delete the resource
- `main.yaml` — Dispatcher that includes the correct task file based on a `<role>_state` variable

## Roles

### auth

Obtain a session cookie for the Netris API (`POST /api/auth`).

**Variables:**
- `netris_controller_url` (required) — Base URL of the Netris controller
- `netris_username` (required) — API username
- `netris_password` (required) — API password (use `no_log`)
- `netris_validate_certs` (default: `true`) — Whether to validate SSL certificates

**Output:** `netris_session_cookie`

### server_cluster

Create or delete a server cluster with attached servers.

**State variable:** `server_cluster_state` (`present` | `absent`)

**Variables:**
- `server_cluster_name` (required) — Name of the server cluster
- `server_cluster_site_id` (required) — Netris site ID
- `server_cluster_servers` — List of server names (strings) or objects with `id`/`name`
- `server_cluster_template_id` — Server cluster template ID
- `server_cluster_tags` — List of tags
- `server_cluster_admin_id` / `server_cluster_admin_name` — Admin tenant

**Output:** `server_cluster_id`, `server_cluster_vpc_id`, `server_cluster_vpc_name`

### server_cluster_template

Read server cluster template details.

**State variable:** `server_cluster_template_state` (default: `read`)

**Variables:**
- `server_cluster_template_id` (required) — ID of the server cluster template

**Output:** `server_cluster_template_data` (full template object), `server_cluster_template_server_nics` (deduplicated list of server NIC names across all vnets)

### nat

Create or delete DNAT/SNAT rules.

**State variable:** `nat_state` (`present` | `absent`)

**Variables:**
- `nat_name` (required) — Name of the NAT rule
- `nat_site_id` (required) — Netris site ID
- `nat_action` — `dnat` or `snat` (default: `dnat`)
- `nat_vpc_id` / `nat_vpc_name` — VPC for the rule
- `nat_protocol` — Protocol (default: `tcp`)
- `nat_source_address`, `nat_destination_address` — Addresses
- `nat_source_port`, `nat_destination_port` — Ports
- DNAT-specific: `nat_dnat_to_ip`, `nat_dnat_to_port`
- SNAT-specific: `nat_snat_to_ip`, `nat_snat_to_pool`

**Output:** `nat_id`

### l4lb

Create or delete L4 load balancers.

**State variable:** `l4lb_state` (`present` | `absent`)

See `meta/argument_specs.yaml` for the full variable list.

### vpc

Create or delete a Netris VPC (Virtual Private Cloud / VRF).

**State variable:** `vpc_state` (`present` | `absent`)

**Variables:**
- `vpc_name` (required) — Name of the VPC
- `vpc_tenant_id` (required for create) — Admin tenant ID

**Output:** `vpc_id`, `vpc_already_existed`

**API:** `POST /api/v2/vpc` (create), `DELETE /api/v2/vpc/{id}` (delete)

### vnet

Create or delete a Netris V-Net (virtual network segment within a VPC).

**State variable:** `vnet_state` (`present` | `absent`)

**Variables:**
- `vnet_name` (required) — Name of the V-Net
- `vnet_vpc_id` (required for create) — Parent VPC ID
- `vnet_site_id` (required for create) — Netris site ID
- `vnet_tenant_id` — Owner tenant ID
- `vnet_gateway` — Gateway CIDR (e.g. `10.0.1.1/24`), enables L3 routing
- `vnet_vlan_id` — VLAN ID (integer, required by API)
- `vnet_ports` — Switch ports to attach

**Output:** `vnet_id`, `vnet_already_existed`

**API:** `POST /api/v2/vnet` (create), `DELETE /api/v2/vnet/{id}` (delete). Uses `tenant` (singular object), `vlan` (integer).

### acl

Create or delete Netris ACL (Access Control List) firewall rules.

**State variable:** `acl_state` (`present` | `absent`)

**Variables:**
- `acl_name` (required) — Name of the ACL rule
- `acl_action` (default: `permit`) — `permit` or `deny`
- `acl_proto` (default: `all`) — Protocol: `all`, `tcp`, `udp`, `icmp`, `ip`, `icmpv6`
- `acl_vpc_id` (default: `1`) — VPC ID for the rule
- `acl_src_prefix` — Source prefix in CIDR notation
- `acl_dst_prefix` — Destination prefix in CIDR notation
- `acl_src_port_from` / `acl_src_port_to` — Source port range
- `acl_dst_port_from` / `acl_dst_port_to` — Destination port range
- `acl_src_port_group` / `acl_dst_port_group` — Port group IDs (0 for none)
- `acl_established` — Match established connections (0 or 1)
- `acl_reverse` — Generate reverse rule (`yes` / `no`)
- `acl_comment` — Comment

**Output:** `acl_id`, `acl_already_existed`

**API:** `POST /api/acl` (create, v1 API), `DELETE /api/acl` with `{"id": [<id>]}` (delete). Field names use snake_case: `proto`, `src_prefix`, `dst_prefix`, `src_port_from`, etc.

### ipam

Query, create, or delete Netris IPAM allocations and subnets.

**State variable:** `ipam_state` (`read` | `create` | `delete`, default: `read`)

**Read variables:**
- `ipam_site_id` — Netris site ID to filter subnets by
- `ipam_purpose` (default: `nat`) — Subnet purpose to filter by (e.g. `nat`, `load-balancer`)
- `ipam_count` (default: `1`) — Number of IPs to allocate

**Read output:** `ipam_allocated_ips` (list of allocated IP addresses)

**Create variables:**
- `ipam_name` (required) — Name for the IPAM subnet
- `ipam_cidr` (required) — CIDR block
- `ipam_purpose` (required) — Subnet purpose: `common`, `load-balancer`, `nat`, `inactive`
- `ipam_site_id` (required) — Netris site ID
- `ipam_tenant_id` — Owner tenant ID
- `ipam_vpc_id` — VPC to assign the subnet to

**Create output:** `ipam_subnet_id`, `ipam_alloc_id`, `ipam_already_existed`

**Delete variables:**
- `ipam_name` (required) — Name of the IPAM subnet to delete (also deletes the `<name>-alloc` allocation)

**API:** Create is two-step: `POST /api/v2/ipam/allocation` (parent), then `POST /api/v2/ipam/subnet` (child). Delete: `DELETE /api/v2/ipam/subnet/{id}`, then `DELETE /api/v2/ipam/allocation/{id}`.

## Common Variables

Set in group_vars or extra vars:

- `netris_controller_url` — Base URL of the Netris controller (e.g. `https://netris.example.com`)
- `netris_username` — Netris API username
- `netris_password` — Netris API password (use `no_log`)
- `netris_validate_certs` — Set to `false` to skip SSL verification (dev only)

## License

Apache-2.0
