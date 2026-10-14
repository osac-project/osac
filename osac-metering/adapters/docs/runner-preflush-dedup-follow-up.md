# Provider Adapter Runner: pre-flush deduplication follow-up

## Scope

This is a follow-up for the shared Provider Adapter `Runner`, not part of the
Cost Management adapter implementation. Any adapter that buffers events in
`Submit` and acknowledges them later from `Flush` can encounter this issue.

## Current behaviour

For a successfully submitted Kafka message, the Runner currently:

1. adds the CloudEvent ID to its in-memory deduplication cache;
2. records the Kafka offset as eligible to commit; and
3. defers the actual Kafka commit until a later successful `Flush`.

The completed-deduplication state is therefore recorded before the provider
has acknowledged durable receipt.

## Failure sequence

1. Event `E` is accepted by `Submit` and buffered by an adapter.
2. The Runner adds `E` to the completed deduplication cache.
3. The provider call from `Flush` fails, so no offset is committed.
4. A consumer-group rebalance redelivers `E` to the same long-running Runner.
5. The Runner finds `E` in the cache, suppresses it, and records its offset.
6. A later successful flush commits the offset even though `E` was never
   acknowledged by the provider.

This is a potential event loss / under-billing failure. A full process restart
usually masks it because the in-memory cache is cleared; a rebalance without a
process restart does not.

## Required framework change

Model delivery state explicitly:

```text
received -> pending delivery -> provider acknowledged -> deduplicated and offset-committable
```

- Only mark an event completed in the deduplication cache after its provider
  delivery succeeds.
- Retain failed-flush events as pending. A redelivery while an event is pending
  must not be mistaken for a successfully delivered duplicate.
- Track acknowledgement per topic/partition and commit only the contiguous
  prefix of successfully delivered offsets.
- Extend the flush acknowledgement contract if needed so a batching adapter can
  report the exact successfully delivered event set; a multi-chunk flush may
  partially succeed.

## Cost adapter implications

The Cost adapter intentionally retains its unchanged batch for retry after
timeouts, `429`, `5xx`, and unexpected batch-level `4xx` responses. The Cost
receiver's durable receipt ledger makes retry after an ambiguous request safe.
It cannot prevent the Runner from suppressing a Kafka redelivery before any
successful `Flush`, so this shared framework follow-up is required before
production billing use.

## Suggested tests

- Buffered event, failed flush, same-process rebalance/redelivery, later flush:
  verify the original event is delivered and its offset is not skipped.
- Mixed partitions: verify an unresolved earlier offset prevents committing a
  later offset in the same partition.
- Multi-chunk flush with one successful and one failed chunk: verify only
  durably acknowledged events become deduplicated/committable.
