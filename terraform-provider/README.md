# terraform-provider-osac

Proof of concept Terraform/OpenTofu provider for [OSAC](https://github.com/osac-project/osac).
It manages **BareMetalInstance** resources through the fulfillment service public API.

The provider does not have any credentials of its own. It reuses the session
created by the `osac login` command of the OSAC CLI: the server address, the CA
certificates, and the OAuth credentials stored in `~/.config/osac` (with the
secrets in the operating system keyring, or in `secrets.json` when no keyring
is available). Tokens are renewed with the same non-interactive mechanisms the
CLI uses after login — a stored access token, a stored refresh token, the
client credentials and password flows, or the token generation script — and
renewed tokens are written back to the same store, so the CLI and the provider
share the session.

## Status

This is a POC:

- Only `osac_bare_metal_instance` is managed. Creating, reading, updating the
  in-place updatable fields (`run_strategy`, `restart_trigger`), and deleting
  are implemented and were verified against a live cluster. Everything else in
  the spec is immutable and forces replacement, which matches the API.
- Creation waits for the instance to be ready: `Create` polls until the
  instance reaches `RUNNING` (or `STOPPED` with `run_strategy` `HALTED`), with
  a 30 minute timeout, and fails the apply when provisioning fails. Updates
  don't wait for restarts to complete.
- `template_parameters` values are always sent as strings (wrapped in
  `google.protobuf.StringValue`).
- The secret data source exposes the secret entries as a sensitive
  attribute: hidden from plan and apply output, but recorded in the state
  like any sensitive Terraform value.

## Building

From this directory (or anywhere in the `osac` mono-repo, thanks to
`go.work`):

```bash
go build -o terraform-provider-osac .
```

## Using with OpenTofu or Terraform

Providers are normally distributed through registries; this POC is not
published, so use development overrides.

1. Build the provider and put the binary in a directory, for example
   `~/bin/terraform-provider-osac/terraform-provider-osac`.

2. Create a CLI configuration file, for example `~/.osac-tofu.tfrc`:

   ```hcl
   provider_installation {
     dev_overrides {
       "registry.opentofu.org/osac-project/osac" = "/home/you/bin/terraform-provider-osac"
     }
     direct {}
   }
   ```

   Use `registry.terraform.io/osac-project/osac` instead if you run Terraform.

3. Point the CLI at it and run plan/apply directly (`tofu init` is not needed
   with development overrides):

   ```bash
   export TF_CLI_CONFIG_FILE=~/.osac-tofu.tfrc   # works for tofu and terraform
   tofu plan
   ```

## Authentication

Log in once with the CLI, exactly as you would for normal OSAC work:

```bash
osac login --ca-file ~/.config/osac/bundle.pem \
  --flow credentials \
  --client-id "${OSAC_ADMIN_CLIENT_ID}" \
  --client-secret "${OSAC_ADMIN_CLIENT_SECRET}" \
  https://fulfillment-internal-api-osac.example.com
```

Notes:

- Sessions that use an interactive flow (authorization code or device) work
  while the stored access or refresh token is valid, but the provider can't
  run those flows when the session finally expires; run `osac login` again.
- If the session can access multiple tenants (for example an admin session),
  set `tenant` on the resource, or select a default with
  `osac tenant use <name>`.
- The provider configuration accepts a `config_dir` attribute (or the
  `OSAC_CONFIG_DIR` environment variable) to use a configuration directory
  other than the default `~/.config/osac`.

## Example

```hcl
terraform {
  required_providers {
    osac = {
      source = "registry.opentofu.org/osac-project/osac"
    }
  }
}

data "osac_bare_metal_instance_catalog_item" "standard" {
  name = "standard-bare-metal"
}

data "osac_disk_image" "rhel" {
  name = "rhel-9-4-amd64"
}

data "osac_secret" "userdata" {
  name = "nginx-userdata"
}

resource "osac_bare_metal_instance" "worker" {
  name            = "worker-0"
  tenant          = "tenant1"
  catalog_item    = data.osac_bare_metal_instance_catalog_item.standard.name
  disk_image      = data.osac_disk_image.rhel.name
  user_data_secret = data.osac_secret.userdata.name
  ssh_public_key  = file("~/.ssh/id_ed25519.pub")
  run_strategy    = "ALWAYS"
}
```

See [`examples/`](examples/) for a complete, runnable example.

## Resources

### `osac_bare_metal_instance`

The schema is generated from the API definitions by `tools/codegen`, so it
mirrors the API object: a `metadata` object (`name`, `tenant`, `labels`,
`annotations`, ...), a `spec` object and a computed `status` object. The spec
attributes are the API fields, with snake_case names:

```hcl
resource "osac_bare_metal_instance" "worker" {
  metadata = {
    name   = "worker-0"
    tenant = "tenant1"
  }
  spec = {
    catalog_item     = { name = "standard-bare-metal" }
    disk_image       = { name = "rhel-9-4-amd64" }
    instance_type    = { name = "standard-bare-metal-type" }
    user_data_source = { secret = { name = "nginx-userdata" } }
    ssh_public_key   = file("~/.ssh/id_ed25519.pub")
    run_strategy     = "BARE_METAL_INSTANCE_RUN_STRATEGY_ALWAYS"
  }
}
```

Reference attributes (`catalog_item`, `template`, `instance_type`,
`disk_image`, `user_data_source.secret`, network attachment `subnet` and
`security_groups`) are objects with `id` and `name`: set one of them and the
provider resolves the reference, marking shared-tenant objects correctly.
`run_strategy` accepts the enum values listed in the plan output, validated by
a generated `OneOf` validator.

Only `spec.run_strategy` and `spec.restart_trigger` can be updated in place;
everything else forces the replacement of the instance, which matches the
immutability of the API. The resource supports `tofu import` with the
instance ID.

## Data sources

All data sources accept exactly one of `name` or `id` and return the object
with the schema generated from the API definitions — a nested `metadata`
object (so the name is `metadata.name`), plus the type-specific attributes:

- `osac_bare_metal_instance_type` — the `spec` hardware profile (CPU, memory,
  disks, network ports) and `status`.
- `osac_disk_image` — the `spec` source, architectures, lifecycle and guest
  OS family.
- `osac_bare_metal_instance_catalog_item` — `title`, `description`,
  `published` and `template`. The field and template parameter policies are
  not exposed: the generator can't map their repeated attribute names to
  unique Go types, and they aren't needed to reference the item.
- `osac_secret` — `metadata`, `type`, and the `data` entries (base64
  encoded, like in the REST API). The `data` attribute is marked sensitive:
  it is hidden from plan and apply output, but note that sensitive is a
  display hint — the values are still recorded in the Terraform state.

## Layout

```text
terraform-provider/
├── main.go                       Plugin entrypoint
├── internal/osaccli/             Reads what `osac login` wrote: config.json,
│                                 keyring/secrets.json, CA pool, and the OAuth
│                                 token renewal used by the CLI
└── internal/provider/            Provider, gRPC client, resource, data sources
```

The API calls use the generated gRPC clients from the
[`proto`](../proto) module, so the provider tracks the public API definitions
in the mono-repo. The `internal/osaccli` package mirrors the formats written
by `fulfillment-service/internal/config` (which can't be imported directly
because it is internal to that module): the keyring key is
`secrets:<config-dir>`, with the older plain `secrets` key used as a fallback.
