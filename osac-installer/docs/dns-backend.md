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
| `dns.rfc2136.dns` | `dns.rfc2136` | RFC 2136 dynamic updates signed with a TSIG key (BIND, PowerDNS, ...) |
| `dns.noop.dns` | `dns.noop` | No-op — skips DNS record management |

The **noop** backend performs no DNS create or delete operations. It
logs a generic guidance message without exposing record names or
values, and returns success. Use it in lab or CI environments where
DNS is handled externally or where cluster domains are resolved
through other means (e.g., wildcard records, `/etc/hosts`, or a
local DNS server). Clusters provisioned with the noop backend are
not reachable by domain name unless the required records are created
out of band.

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

## RFC 2136 Configuration

The `dns.rfc2136.dns` backend sends RFC 2136 dynamic updates, signed with a
TSIG key, to the primary DNS server of the zone. Use it where Route 53 is not
available, for example on bare-metal hubs with a lab or corporate DNS server.

For each cluster it creates `api.<cluster>.<base>`, `api-int.<cluster>.<base>`
(the `kube-apiserver` LoadBalancer address) and `*.apps.<cluster>.<base>` (the
ingress address), and deletes them when the cluster is deleted. A rerun with
unchanged values sends no update; a changed address or TTL replaces the record.

### ConfigMap Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DNS_CLASS` | — | `dns.rfc2136.dns` |
| `DNS_ZONE` | `EXTERNAL_ACCESS_BASE_DOMAIN` | The zone configured on the DNS server (its SOA owner name). It must contain the cluster base domain |
| `DNS_RFC2136_SERVER` | — (required) | IPv4 or IPv6 address of the primary server that accepts updates. Host names are not supported |
| `DNS_RFC2136_PORT` | `53` | Server port |
| `DNS_RFC2136_PROTOCOL` | `tcp` | `tcp` or `udp` |
| `DNS_RFC2136_KEY_NAME` | — (required) | Name of the TSIG key |
| `DNS_RFC2136_KEY_ALGORITHM` | `hmac-sha256` | `hmac-md5`, `hmac-sha1`, `hmac-sha224`, `hmac-sha256`, `hmac-sha384` or `hmac-sha512` |
| `DNS_SERVER` | — | Optional resolver for the readiness wait after the records are created. Set it to the update server when the zone is not delegated from the resolvers the execution environment uses |

### Secret Variables

| Variable | Description |
|----------|-------------|
| `DNS_RFC2136_KEY_SECRET` | Base64-encoded TSIG secret (required) |

```yaml
aap:
  instanceGroups:
    clusterFulfillment:
      config:
        DNS_CLASS: "dns.rfc2136.dns"
        DNS_ZONE: "clusters.example.com"
        EXTERNAL_ACCESS_BASE_DOMAIN: "clusters.example.com"
        EXTERNAL_ACCESS_SUPPORTED_BASE_DOMAINS: "clusters.example.com"
        HOSTED_CLUSTER_BASE_DOMAIN: "clusters.example.com"
        DNS_RFC2136_SERVER: "192.0.2.53"
        DNS_RFC2136_KEY_NAME: "osac-update"
      secret:
        DNS_RFC2136_KEY_SECRET: "<base64 TSIG secret>"
```

The driver checks these settings before it contacts the server and fails with
a message that names the variable to fix. It also fails when the server
answers an update with an error (for example `REFUSED` or `NOTAUTH`), on
deletes too, so a cluster deletion is never reported complete while its
records remain in the zone. The failed job is retried. The TSIG secret never
appears in job output, logs, error messages or events, and the driver's own
job output and messages do not include record names or addresses. Cluster
failure events from the existing CaaS diagnostics can still name the
affected record.

The driver contacts only `DNS_RFC2136_SERVER`; managing the records needs no
internet access or external DNS provider.

### DNS server requirements

- The server is authoritative (primary) for `DNS_ZONE` and allows the TSIG
  key to update names below it. For BIND, generate the key with
  `tsig-keygen -a hmac-sha256 osac-update > /etc/named/osac-update.key`
  (its `secret` is the `DNS_RFC2136_KEY_SECRET` value) and add:

  ```text
  include "/etc/named/osac-update.key";
  zone "clusters.example.com" {
      type primary;
      file "dynamic/clusters.example.com.db";
      update-policy { grant osac-update subdomain clusters.example.com. A AAAA; };
  };
  ```

- The AAP job pods on the hub can reach `DNS_RFC2136_SERVER` on the configured
  port and protocol.
- The managed clusters' workers, the hub, and the clients of the clusters
  resolve the zone: delegate it from the parent zone with NS records, or
  forward it to this server from the resolvers they use.
- Keep the SOA negative-caching TTL low (for example 60 seconds). Resolvers
  can look up `api.<cluster>` before its record exists and cache the
  negative answer for that long.

### Verifying the records

Query the update server directly. Other resolvers can keep returning cached
answers, including after a deletion, until the record TTL (1800 seconds by
default) expires. `<cluster>` is the hosted cluster name and `<base>` the
cluster base domain, as shown in the cluster's API URL
(`https://api.<cluster>.<base>`):

```bash
dig +short @192.0.2.53 api.<cluster>.<base> A
dig +short @192.0.2.53 api-int.<cluster>.<base> A
dig +short @192.0.2.53 console-openshift-console.apps.<cluster>.<base> A
```

While the cluster exists, the first two return the API address and the third
the ingress address. After the cluster is deleted, all three return nothing.

### Troubleshooting

The driver's failure messages name the setting to check:

| Message | Fix |
|---------|-----|
| `DNS_RFC2136_<SETTING> must …` | The setting is missing or invalid. Correct it in the installation values; `DNS_RFC2136_SERVER` must be an IP address, not a host name |
| `The DNS record name is not inside DNS_ZONE` | Set `DNS_ZONE` to the zone configured on the server, and make sure it contains the cluster base domain |
| `RFC 2136 DNS update failed: DNS_RFC2136_KEY_SECRET is not a valid base64 TSIG secret` | Copy the `secret` value from the BIND key file exactly |
| `… the server rejected the TSIG signature` | The key name, algorithm or secret does not match a key the server knows |
| `… no answer from the server` | Check the server address, port and protocol, and that the AAP job pods can reach the server |
| `RFC 2136 DNS update failed (REFUSED)` | The key may not change these records. Check the zone's `update-policy` (or `allow-update`) for this key |
| `RFC 2136 DNS update failed (NOTAUTH)` | The server is not the primary for `DNS_ZONE`, or it did not accept the key |
| `RFC 2136 DNS update failed (NOTZONE)` | The record is outside the zone configured on the server; check `DNS_ZONE` |
| `A DNS record already exists and dns_record_overwrite is false` | Only callers that disable overwriting see this (the CaaS callers always overwrite). Remove the conflicting record |
| `… unexpected nsupdate result` | Check the DNS server logs for the update |

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
   `osac-aap` component. Read provider settings from environment variables
   prefixed `DNS_<PROVIDER>_`, and keep credentials out of task output
   (`no_log: true`).
2. Implement `tasks/create.yaml` and `tasks/delete.yaml` using the interface
   above.
3. In the `osac-installer` chart, add `dns.<provider>.dns` to the `DNS_CLASS`
   enum in `charts/osac/values.schema.json`, and add each provider setting to
   the schema and to `charts/osac/values.yaml` (under
   `aap.instanceGroups.clusterFulfillment.config`, or `.secret` for
   credentials). The schema rejects unknown keys, so a setting missing from it
   cannot be configured through Helm. The Enclave Wizard renders its controls
   from this schema.
4. Set `DNS_CLASS: dns.<provider>.dns` in the installation values.

The dispatcher (`dns.api`) and the callers need no changes.
