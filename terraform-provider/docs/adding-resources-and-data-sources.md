# Adding a resource or data source

Copy an existing one and change the names. The schema and models are
generated; only the CRUD glue is hand-written.

| Adding | Copy this |
|--------|-----------|
| A resource | `internal/provider/resource_secret.go` — simple. `resource_bare_metal_instance.go` — with references, waiters, import |
| A data source | `internal/provider/data_source_secret.go` |

About 30 minutes for a data source, half a day for a resource with
references and waiters — plus live validation.

## 1. Generate the schema

1. Add `tools/codegen/configs/<service>.yml` if the service isn't covered
   yet. Copy `secrets_service.yml` — the smallest one — and change the
   resource name and the REST paths. Resource entries need
   `create`/`read`/`update`/`delete`, data sources only `read`. The paths
   are the ones of the REST gateway (`/api/fulfillment/v1/...`).
2. Generate (tool requirements are listed in `generate.sh`):

   ```bash
   cd tools/codegen
   ./generate.sh <service>
   ```

3. Check `internal/gen/`: a `resource_<name>/` and/or `datasource_<name>/`
   package appeared, with schema functions, models and value types.
   Never hand-edit `internal/gen/` — regeneration overwrites it.

Why these tools: see [adr/0002](adr/0002-generate-schemas-from-protos.md).

## 2. Write the glue

Create `internal/provider/resource_<name>.go` or
`data_source_<name>.go` from the copy, rename the types and the
constructor, and wire it to the client.

**Data source** (see `data_source_secret.go`):

- Schema: wrap the generated schema with `withLookup(...)` so the
  object can be resolved by `name`, `id`, or `labels`.
- `Read`: `lookupID` to resolve the name, `get` to fetch, then copy the
  response into the state: plain fields directly, nested objects through
  `valueOfJSON`. The generated models can't be `json.Unmarshal` targets —
  their fields are framework value types with `tfsdk` tags — so each Read
  decodes into a small local struct and reaches the generated types through
  `valueOfJSON`.

**Resource** (see `resource_secret.go`):

- Schema: start from the generated schema, then add what it can't express
  (see [adr/0003](adr/0003-keep-schema-glue-hand-written.md)): 
  `UseStateForUnknown` on `id`, `RequiresReplace` on fields the server
  rejects changing, `markSensitive(...)` on secret material.
- `Create`: build the request body (metadata name + tenant), `create`,
  store the returned `id`, set the state.
- `Read`: `get`; on `isNotFound`, `RemoveResource` (handles deletions
  outside Terraform).
- `Update`: send only the updatable fields, with an explicit update mask —
  `update(ctx, path, id, body, []string{"field"})`.
- `Delete`: `delete`, tolerate `isNotFound`.
- `ImportState`: `resource.ImportStatePassthroughID` on `id`.
- References to other objects (id + name): resolve with `resolveReference`,
  which returns the `shared` flag for cross-tenant references.
- Asynchronous operations: add a waiter like `waitForReady` /
  `waitForDeleted` in the resource file, and call it before setting the
  state — a resource isn't "created" until the backend is done with it.

## 3. Register it

Add the constructor to the `Resources` or `DataSources` list in
`internal/provider/provider.go`, and the REST path constant to
`internal/provider/paths.go` — all API paths live there.

## 4. Build

From `terraform-provider/` (or anywhere in the mono-repo):

```bash
go build .
```

## 5. Validate live

There are no unit tests yet; validation runs against a deployed cluster
through development overrides (setup in the
[README](../README.md#using-with-opentofu-or-terraform)):

```bash
go build -o ~/bin/terraform-provider-osac/terraform-provider-osac .
export TF_CLI_CONFIG_FILE=~/.osac-tofu.tfrc
cd examples/<example>
tofu plan && tofu apply
tofu import osac_<resource>.<name> <id>
tofu destroy
```

Check: plan is clean on rerun (refresh consistency), `tofu import` works,
destroy completes. Don't wrap `tofu apply` in a short `timeout` — the
SIGTERM looks like a provider crash and wastes an afternoon.

## Gotchas

- Tenant: set `metadata.tenant` on the resource, or the provider falls
  back to `client.DefaultTenant` (`osac tenant use`).
- Cross-tenant references need `shared: true` — `resolveReference`
  returns the flag, don't hardcode it.
- `Sensitive` is a display hint: the value is still recorded in the state.
- The REST paths aren't consistent between services (`baremetal` vs
  `bare_metal`, OSAC-7379): copy the path from the gateway, not from the
  resource name.
