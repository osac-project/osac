# Netris Idempotency and Update-Safety

Contract for OSAC tenant networking AAP playbooks that talk to Netris
(`osac.templates.netris` + `netris.controller`).

See also: [inventory / operation matrix](netris-idempotency-inventory.md)
(OSAC-4923).

## Goals (OSAC-4923)

1. **Safe rerun**: a second identical create or delete converges without
   duplicates or hard failures when the target is already gone.
2. **Partial recovery**: after a failure mid-create or mid-delete, a rerun
   finishes remaining Netris mutations by reading live controller state.
3. **Immutable mismatch fails clearly**: if a named object exists with a
   different immutable field (CIDR, VPC id, SNAT/DNAT bindings, etc.), the
   playbook fails with kind/name/field/expected/actual — it does **not**
   delete and recreate.
4. **Default protection**: VirtualNetwork, Subnet, and NATGateway delete
   playbooks refuse resources labeled `osac.openshift.io/default=true`, even
   when a job extra variable tries to enable deletion.
5. **Move attachment**: detach/attach is idempotent; an interrupted move
   completes on rerun; post-conditions are checked.

## Deferred

| Topic | Ticket |
|-------|--------|
| SecurityGroup ACL stale-rule prune / desired-state reconcile | [OSAC-4888](https://redhat.atlassian.net/browse/OSAC-4888) |
| ACL fan-out to `status.attachedSubnetCIDRs` | [OSAC-4925](https://redhat.atlassian.net/browse/OSAC-4925) |
| Fulfillment FieldMask immutability | [OSAC-4936](https://redhat.atlassian.net/browse/OSAC-4936) |

Until OSAC-4888, SecurityGroup create remains **get-or-create by ACL name**.
Rule content changes that leave orphans are a known gap.

## Mutable vs create-only (AC4)

For VPC, V-Net, IPAM, and NAT objects in scope of this ticket, supported
fields are treated as **create-only**. When the named object already exists:

- matching immutable fields → no-op success
- mismatched immutable fields → fail with an actionable message (no
  update-in-place, no delete-and-recreate)
- fields absent from a list summary trigger a detail read where the provider
  endpoint is available; still-missing fields fail as unverifiable rather than
  being treated as a match

Move-network-attachment is the supported mutable operation (port membership).
SecurityGroup ingress/egress desired-state prune remains deferred.

Delete operations verify the provider object against the OSAC resource before
deleting it. Checks include tenant, prefix, site, purpose, VPC, and NAT binding
fields when those values are available from the resource. A missing provider
object is a successful no-op. A found object without a stable ID or with
mismatched/unverifiable ownership fields is left in place and the job fails.
VirtualNetwork deletion still checks for its IPAM allocation by name if the VPC
has already disappeared, so a retry can finish cleanup after a partial delete.

## Assumptions

- Netris object names are unique within the relevant API list.
- OSAC CR `metadata.name` maps to Netris VPC / V-Net / IPAM / ACL / NAT names
  (NAT uses `{name}-snat` or `{name}-dnat`).
- Auth uses `netris_session_cookie` from `netris.controller.auth`.
- Operator re-launches the **create** job when desired config version changes;
  there are no separate Update playbooks for tenant networking.

## Filter helpers

`netris.controller` filter plugin `idempotency`:

- `netris_immutable_mismatch(existing, expected_fields)` — returns a list of
  `{field, expected, actual}` dicts for differing fields (empty = match).
- `osac_is_default_networking_resource(resource)` — true when label
  `osac.openshift.io/default` is `"true"`.
- `netris_port_on_vnet(vnet_detail, port_id)` — true when port id is in
  V-Net ports (int/string tolerant).
- `netris_ports_excluding(ports, port_id)` — ports list without the matching
  id (int/string tolerant; used by detach).

## NAT provider value normalization

Immutable NAT comparisons accept equivalent provider representations for host
addresses: a bare IPv4 address matches the same `/32` in `snatToIP` or
`destinationAddress`. Other destination prefixes remain exact. For DNAT,
an empty `dnatToPort` matches the provider's `0` representation for an unset
port; configured port values remain exact.

## Failure message shape

```text
Netris <object-type> '<name>' already exists with immutable field mismatch:
  field=<path> expected=<value> actual=<value>.
Refusing delete-and-recreate. Correct Netris state or recreate the OSAC
resource with a new name after cleanup.
```

The top-level Netris AAP playbooks rescue provider failures and report the
operation, OSAC resource kind/name, failed task, HTTP status when present, and
a bounded provider error summary. Common cookie, authorization, token,
password, and secret values are redacted. This is AAP job output only; this
change does not alter operator status or Fulfillment error propagation.

## Testing

- Unit: `uv run pytest tests/unit` (idempotency, ownership, diagnostic, and
  pool-membership filters).
- Component: `tests/integration/targets/netris_idempotency/` runs top-level
  playbooks against Kind and a stateful local Netris HTTP fixture. It covers
  create/replay, immutable drift, detail reads, partial create/delete recovery,
  default-resource refusal, delete ownership, ExternalIP pool membership,
  move retries, post-conditions, and sanitized AAP diagnostics. It does not
  prove response shapes against the pinned Netris 2.8.1 lab. Live Netris ACL
  prune coverage belongs to OSAC-4888 / provider suites.
