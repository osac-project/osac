# DNS Backend Configuration

The DNS backend controls how hosted clusters get their DNS records created and
deleted during provisioning. A pluggable dispatcher (`dns.api.dns`) delegates
to a configurable backend driver selected by the `DNS_CLASS` environment
variable.

For general AAP configuration see [AAP Configuration](aap-configuration.md).

## Supported Backends

| `DNS_CLASS` | Collection | Description |
|-------------|------------|-------------|
| `dns.route53.dns` (default) | `dns.route53` | AWS Route 53 |

## ConfigMap Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DNS_CLASS` | `dns.route53.dns` | Fully-qualified Ansible role name of the DNS driver |
| `DNS_ZONE` | Value of `EXTERNAL_ACCESS_BASE_DOMAIN` | DNS zone to operate in (e.g., `box.massopen.cloud`) |

## Route 53 Configuration

The default Route 53 backend requires AWS credentials to manage DNS records.

### Secret Variables

Values must be plaintext — the script base64-encodes them automatically.

| Variable | Description |
|----------|-------------|
| `AWS_ACCESS_KEY_ID` | AWS access key with Route 53 permissions |
| `AWS_SECRET_ACCESS_KEY` | AWS secret access key |

The AWS IAM user or role must have permissions to create, modify, and delete
records in the target Route 53 hosted zone.

## DNS Interface

Each backend driver must implement two entry points:

- **`tasks/create.yaml`** — create or update a DNS record
- **`tasks/delete.yaml`** — delete a DNS record

Both receive the following variables:

| Variable | Type | Required | Default | Description |
|----------|------|----------|---------|-------------|
| `dns_zone` | str | yes | — | DNS zone to operate in |
| `dns_record_name` | str | yes | — | Fully qualified domain name |
| `dns_record_type` | str | no | `A` | Record type (`A` or `AAAA`) |
| `dns_record_value` | str | yes | — | Record value (IP address) |
| `dns_record_ttl` | int | no | `1800` | TTL in seconds |
| `dns_record_overwrite` | bool | no | `true` | Whether to overwrite an existing record |

The `delete` entry point only requires `dns_zone`, `dns_record_name`, and
`dns_record_type`.

## Adding a New DNS Backend

To add support for a new provider (e.g., Cloudflare):

1. Create a new collection at
   `collections/ansible_collections/dns/<provider>/roles/dns/` in the
   `osac-aap` repository.
2. Implement `tasks/create.yaml` and `tasks/delete.yaml` using the interface
   above.
3. Set `DNS_CLASS=dns.<provider>.dns` as an environment variable.

No changes to `dns.api` or any existing code are required.
