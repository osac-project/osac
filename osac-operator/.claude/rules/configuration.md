# Configuration Reference

Config via environment variables from a Secret (see `config/samples/osac-config-secret.yaml`).

## AAP Provisioning

- `OSAC_AAP_URL` — AAP server URL (required)
- `OSAC_AAP_TOKEN` — authentication token (required)
- `OSAC_AAP_TEMPLATE_PREFIX` — template name prefix (default: `osac`)
- `OSAC_AAP_STATUS_POLL_INTERVAL` — job polling interval (default: 30s)
- `OSAC_AAP_INSECURE_SKIP_VERIFY` — skip TLS verification (default: false)

## Fulfillment Service gRPC

- `OSAC_FULFILLMENT_SERVER_ADDRESS` — gRPC server address
- `OSAC_FULFILLMENT_TOKEN_FILE` — path to auth token file

## Namespaces

- `OSAC_CLUSTER_ORDER_NAMESPACE`, `OSAC_COMPUTE_INSTANCE_NAMESPACE`
- `OSAC_TENANT_NAMESPACE`, `OSAC_NETWORKING_NAMESPACE`

## Controller Enable Flags

- `OSAC_ENABLE_CLUSTER_CONTROLLER` / `--enable-cluster-controller`
- `OSAC_ENABLE_COMPUTE_INSTANCE_CONTROLLER` / `--enable-compute-instance-controller`
- `OSAC_ENABLE_TENANT_CONTROLLER` / `--enable-tenant-controller`
- `OSAC_ENABLE_NETWORKING_CONTROLLER` / `--enable-networking-controller`

If none set, all controllers run. If any set, only flagged controllers run.
