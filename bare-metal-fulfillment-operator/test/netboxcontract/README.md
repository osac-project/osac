# NetBox mock API contract tests

Run from `bare-metal-fulfillment-operator/`:

```bash
make netbox-contract-test
```

These tests send the production NetBox client and inventory adapter through a
stateful HTTPS mock. They cover:

- Managed-pool selection, owner values, typed and escaped filters, and
  capabilities added after startup.
- Conditional claims and releases, ownership races, and preservation of tags
  and unrelated fields.
- Stable numeric device IDs and safe handling of missing or replaced devices.

A passing mock test does not verify compatibility with a real NetBox server.
