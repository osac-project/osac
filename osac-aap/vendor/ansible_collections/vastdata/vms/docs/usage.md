# Using the vastdata.vms collection

This guide covers common usage patterns. For the full per-module reference use
`ansible-doc`, e.g. `ansible-doc vastdata.vms.views`.

## Connection

Every module accepts a `vms:` connection dict (documented once in the
`vastdata.vms.conn` doc fragment). Authenticate with either an API token
(VAST 5.3+) or a username/password pair (mutually exclusive).

```yaml
vms:
  host: vms.example.com        # required
  token: "{{ vast_api_token }}"  # OR username + password
  validate_certs: true
  timeout: 60                  # optional, seconds
  tenant: my-tenant            # optional
  api_version: latest          # optional
  debug: false                 # optional, emits sanitized HTTP traces on failure
```

### Set the connection once

`meta/runtime.yml` declares the `vastdata.vms.all` action group, so the
connection can be defined a single time for a whole play:

```yaml
module_defaults:
  group/vastdata.vms.all:
    vms:
      host: vms.example.com
      token: "{{ vast_api_token }}"
```

## Managed resources vs. info modules

- Managed modules (e.g. `views`, `tenants`, `quotas`) implement an idempotent
  `state: present|absent` lifecycle with check mode and diff mode.
- Read-only `*_info` modules (e.g. `views_info`) list resources and never report
  `changed`. Optional schema fields become server-side query filters.

```yaml
- name: Ensure a view exists
  vastdata.vms.views:
    path: /ansible-demo
    policy_id: 1
    protocols: [NFS, SMB]
    state: present

- name: Look up tenants
  vastdata.vms.tenants_info:
  register: tenants
```

## Idempotency notes

- A resource is identified by its `lookup_field` (often `name` or `path`) or by
  `id`. For `state: present`, one of these is required.
- Updates send a minimal PATCH computed by diffing desired vs. current state.
  Read-only and immutable fields are never included in updates.
- Some fields can be explicitly reset to null using the `clear_fields` parameter
  on resources that declare `nullable_fields`.

## Check mode and diff

All managed modules support `--check` and `--diff`. In check mode no API write
is performed; the module reports whether a change would occur and (with
`--diff`) the before/after payload.
