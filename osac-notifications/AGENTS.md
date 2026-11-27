# osac-notifications

Kafka consumer that accepts CloudEvents from `osac.notifications.<tenant>`,
dedupes via `notification_intent`, and creates inbox `Notification` rows through
the fulfillment-service private API.

## Build and test

```bash
go build ./cmd/osac-notifications
go test ./...
```

## Scope

- Ingest validation, grant map, intent dedupe, USER recipient fan-out (story 1.02)
- Validation rejects and permanent fan-out failures → Kafka DLQ; permanent failures also `status=failed`
- Kafka topic discovery matches metering adapters: RefreshMetadata + prefix filter, 1m consume sessions
- Webhook delivery is story 1.03; installer chart/ACLs are story 1.04
