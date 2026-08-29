# Cost Management adapter contract

## Purpose

The Cost Management adapter is the only Cost-side Kafka consumer. The
`adapters.Runner` owns consumer-group offsets, retry, ordering checks, and DLQ
publishing. The adapter only validates canonical structured CloudEvents, buffers
them, and sends them to Cost Management.

## Ingress contract

`Flush` POSTs JSON to `COST_MANAGEMENT_API_URL/api/v1/events/batch`:

```json
{"events":[{"specversion":"1.0","id":"...","type":"...","source":"...","data":{...}}]}
```

Events remain canonical CloudEvents; there is no M360-style translation. Each
request contains at most 100 events and is no larger than 1 MiB. Cost returns
`204 No Content` only after the whole batch is durably and atomically processed.
Cost's receipt ledger makes a replay of an accepted batch a no-op.

The adapter needs `COST_MANAGEMENT_API_TOKEN_FILE`, a mounted Secret file. It
sets a bearer authorization header and neither logs the token nor puts it in a
Kubernetes environment value.

## Failure semantics

Before buffering, `Submit` requires CloudEvents 1.0 with `id`, `type`,
`source`, `time`, JSON data, supported VMaaS/CaaS/MaaS resource types, and
matching `osacresourceid`, `osacresourcetype`, and `osactenant` extensions.
These deterministic malformed records return `NonRetryableError`, so the Runner
routes their original Kafka records to DLQ.

Timeouts, 429, 5xx, 401/403, and unexpected 400/413 leave the entire batch in
memory and return an error. The Runner consequently does not commit offsets;
the next delivery is safe because Cost's receipt ledger is idempotent. A batch
response cannot safely attribute a 4xx to one record, so this increment never
DLQs an entire failed batch. Contract-valid input should not receive a 400.

Known framework limitation: the Runner currently adds its short-lived dedup
entry after `Submit`, before a buffered `Flush` reaches durable Cost storage.
This adapter therefore depends on no consumer-session rebalance between a failed
flush and redelivery; the framework must move dedup acknowledgement to durable
flush success before this can be treated as a complete delivery guarantee.
