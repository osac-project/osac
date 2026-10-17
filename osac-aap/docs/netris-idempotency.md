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
4. **Default protection**: delete playbooks refuse resources labeled
   `osac.openshift.io/default=true` unless
   `osac_allow_default_resource_delete=true` (privileged tenant teardown).
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

Move-network-attachment is the supported mutable operation (port membership).
SecurityGroup ingress/egress desired-state prune remains deferred.

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

## Failure message shape

```text
Netris <object-type> '<name>' already exists with immutable field mismatch:
  field=<path> expected=<value> actual=<value>.
Refusing delete-and-recreate. Correct Netris state or recreate the OSAC
resource with a new name after cleanup.
```

## Testing

- Unit: `uv run pytest tests/unit` (idempotency filters).
- Component: `tests/integration/targets/netris_idempotency/` (default refuse
  for VirtualNetwork / Subnet / SecurityGroup / NATGateway, mismatch fail
  messaging, move post-condition helpers). Live Netris ACL prune coverage
  belongs to OSAC-4888 / provider suites.
