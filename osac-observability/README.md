# osac-observability

The shared metrics store and query tier for OSAC. It deploys Thanos Receive,
Store Gateway, Compactor and Query, plus the authenticated admin query Routes.

Enable it with the single installer switch:

```bash
helm upgrade --install osac osac-installer/charts/osac \
  --set global.observability.enabled=true \
  --set global.observability.objectStorage.secretName=thanos-object-storage \
  --set global.observability.objectStorage.endpoint=https://s3.example.com \
  --set global.observability.objectStorage.bucket=thanos
```

The object-storage Secret lives in `osac-observability` and must contain
`AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`. The bucket, the Secret and
cert-manager must exist before the install; a pre-install hook checks them.

`charts/osac-observability` renders the store. `cmd/remote-write-config`
configures the management cluster's `cluster-monitoring-config`, and
`cmd/query-allowlist` restricts the admin Routes to five Prometheus endpoints.
Both ship in the `osac-observability` image built from `Containerfile`.

For development, see `AGENTS.md` and run `make test`.
