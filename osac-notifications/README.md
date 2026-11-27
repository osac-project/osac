# osac-notifications

Consumes CloudEvents from `osac.notifications.<tenant>`, validates them, stores
a durable `notification_intent` row for dedupe, and creates inbox
`Notification` objects through the fulfillment-service private API.

Validation rejects and permanent fan-out failures are written to
`osac.notifications.dlq.<tenant>`. Permanent fan-out failures also set intent
`status=failed`.

`--fulfillment-token` / `FULFILLMENT_TOKEN` is required.

## Run

```bash
go run ./cmd/osac-notifications \
  --kafka-brokers localhost:9092 \
  --db-url postgres://... \
  --fulfillment-grpc localhost:8000 \
  --fulfillment-grpc-plaintext \
  --fulfillment-token "$TOKEN"
```
