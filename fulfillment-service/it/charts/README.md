# Integration test Helm charts

Charts in this directory are for **integration testing only**. They are deployed
by the fulfillment-service `it/` Ginkgo suite and must not be used for production
installations.

For production PostgreSQL, deploy an in-cluster database via an operator
(CloudNativePG, Crunchy PGO, or Zalando) as documented in
[`docs/INSTALL.md`](../../docs/INSTALL.md).

| Chart | Purpose |
|-------|---------|
| `postgres/` | Single-replica PostgreSQL with TLS for integration tests |
| `keycloak/` | Keycloak instance for integration tests |
| `prometheus/` | Prometheus monitoring stack for integration tests |
