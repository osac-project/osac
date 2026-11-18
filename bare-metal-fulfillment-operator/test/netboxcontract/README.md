# NetBox mock API contract tests

Run from `bare-metal-fulfillment-operator/`:

```bash
make netbox-contract-test
```

These tests send the production NetBox client and inventory adapter through a
stateful HTTPS mock. They check request filters, response decoding, ownership,
conditional updates, and numeric device identity without a cluster or NetBox
installation. The mock defines its own API behavior; a passing run does not
verify that NetBox Community behaves identically. No schema validation is
implemented or tested.

| Case | Behavior |
|---|---|
| TC-INT-015 | Paginated metadata decoding and the explicit managed-pool flag |
| TC-INT-016 | Combined capability, status, pool, and owner filters |
| TC-INT-017 | Empty and populated owner representations |
| TC-INT-019 | Exact typed scalar and stored choice-value filters |
| TC-INT-020 | Tags remain independent; claim and release preserve other fields |
| TC-INT-021 | Conditional claim/release, stale ETags, and ownership races |
| TC-INT-022 | Escaped capability values stay in one filter parameter |
| TC-INT-023 | A capability added after adapter construction can be selected |
| TC-INT-029 | Host IDs use numeric device IDs; missing devices do not release replacements |

TC-INT-018 is omitted because the adapter does not validate custom-field
definitions. TC-INT-015 covers metadata *decoding* only. The mock is never
presented as evidence for NetBox 4.6.10 or 4.7.1 server compatibility.
