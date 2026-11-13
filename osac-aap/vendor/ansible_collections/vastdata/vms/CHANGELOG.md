# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [2.2.0] - 2026-09-17

### Added

- New modules:
  - `vastdata.vms.tenant_metric_labels`, `vastdata.vms.tenant_metric_labels_info`, `vastdata.vms.tenant_metric_label_values`, `vastdata.vms.tenant_metric_label_values_info`, `vastdata.vms.tenant_metric_label_values_bulk`
  - `vastdata.vms.vms_saml_config`
  - `vastdata.vms.vms_login_banner`, `vastdata.vms.vms_pwd_settings`, `vastdata.vms.vms_pwd_settings_info`
  - `vastdata.vms.vms_network_settings`, `vastdata.vms.vms_network_settings_info`, `vastdata.vms.vms_network_settings_summary`
  - `vastdata.vms.vms_set_certificate`, `vastdata.vms.vms_reset_certificate`
  - `vastdata.vms.vms_set_ssl_ciphers`, `vastdata.vms.vms_reset_ssl_ciphers`, `vastdata.vms.vms_set_ssl_port`
  - `vastdata.vms.vms_set_client_certificate`, `vastdata.vms.vms_remove_client_certificate` (optional mTLS via `VAST_CLIENT_CERT` / `VAST_CLIENT_KEY`)
  - `vastdata.vms.vms_toggle_maintenance_mode`
  - `vastdata.vms.issue_pre_install_validations`
  - `vastdata.vms.health_info`, `vastdata.vms.issues_info`
  - `vastdata.vms.tlscertificates`, `vastdata.vms.tlscertificates_info`, `vastdata.vms.tlscertificate_is_operation_healthy`, `vastdata.vms.tlscertificate_crl`
  - `vastdata.vms.quotagroups`, `vastdata.vms.quotagroups_info`, `vastdata.vms.quotagroup_assign_quotas`, `vastdata.vms.quotagroup_refresh_user_quotas`, `vastdata.vms.quotagroup_reset_grace_period`
  - `vastdata.vms.supportbundlesqueue_info`, `vastdata.vms.supportbundlesqueue_move`
  - `vastdata.vms.encryptiongroups_info`
  - `vastdata.vms.blobexpansions`, `vastdata.vms.blobexpansion_show`, `vastdata.vms.blobexpansion_show_info`, `vastdata.vms.blobexpansion_add_columns`, `vastdata.vms.blobexpansion_drop_columns`, `vastdata.vms.blobexpansion_delete` (VMS 5.5+)
- Collection `*_info` modules now expose Swagger-declared query filters plus response-schema fields the live API honors.

### Changed

- `nonlocal_user_info`: drop `name` (use `username`)

## [2.1.0] - 2026-07-28

### Added

- New modules:
  - `vastdata.vms.oidcs` and `vastdata.vms.oidcs_info`
  - `vastdata.vms.userquotas` and `vastdata.vms.userquotas_info`
  - `vastdata.vms.webhooks` and `vastdata.vms.webhooks_info`
  - `vastdata.vms.kerberos`, `vastdata.vms.kerberos_keytab`, and `vastdata.vms.kerberos_info`
  - `vastdata.vms.challengetokens_info`
  - `vastdata.vms.permissions_info`
  - `vastdata.vms.nonlocal_user_info` and `vastdata.vms.nonlocal_group_info`
- Expanded `*_info` coverage for every remaining listable resource (views, viewpolicies, vippools, quotas, s3policies, tenants, groups, users, ldaps, dns, eventdefinitionconfigs, nativereplicationremotetargets, snapshots, globalsnapstreams, protectionpolicies, protectedpaths, activedirectory, administrator_role, localproviders, s3lifecyclerules, managers, qospolicies, apitokens, iamroles, realms, callhomeconfigs, vms, nis).

## [2.0.0] - 2026-07-01

### Added

- Read-only `*_info` modules for every listable resource (e.g. `views_info`, `vippools_info`, `tenants_info`, `nics_info`).
- Multi-version cluster support: a single collection auto-adapts to VMS 5.4.x and 5.5.x at runtime.
- `module_defaults` action group `vastdata.vms.all`, so the `vms:` connection can be declared once per play.
- New modules:
  - `vastdata.vms.callhomeconfigs`
  - `vastdata.vms.callhomeconfig_register_cluster`
  - `vastdata.vms.callhomeconfig_send`
  - `vastdata.vms.vms`
  - `vastdata.vms.vms_configured_idps`
  - `vastdata.vms.vms_set_max_api_tokens`
  - `vastdata.vms.nonlocal_user_key`
  - `vastdata.vms.nis`
  - `vastdata.vms.nis_set_posix_primary`

### Changed

- Relicensed the collection under Apache-2.0 (added a full `LICENSE` at the collection root and aligned all plugin file headers to Apache-2.0 + SPDX).
- Broadened supported Ansible to `ansible-core >= 2.15` (previously pinned to `>=2.19.0,<2.20.0`).
- HTTP 404s now raise a typed not-found error and other transport failures are wrapped as a typed API error, giving callers consistent, actionable failures.
- Hardened `vastdata.vms` for Red Hat certification.

## [1.3.1] - 2026-06-18

### Added

- Role `vastdata.vms.protectedpath` - orchestrate VAST protected paths (VIP pools, replication peers, protection policies, protected paths, and replication/standby streams).
- Module `vastdata.vms.protectedpath_streams` - manage replication streams attached to a protected path (used by the `protectedpath` role).


## [1.3.0] - 2026-06-02

### Added

- New modules:
  - `vastdata.vms.activedirectory`
  - `vastdata.vms.administrator_role`
  - `vastdata.vms.localproviders`
  - `vastdata.vms.s3lifecyclerules`
  - `vastdata.vms.managers`
  - `vastdata.vms.manager_password`
  - `vastdata.vms.manager_authorized_status`
  - `vastdata.vms.qospolicies`
  - `vastdata.vms.apitokens`
  - `vastdata.vms.apitoken_revoke`
  - `vastdata.vms.iamroles`
  - `vastdata.vms.iam_role_credentials`
  - `vastdata.vms.iamrole_revoke_access_keys`
  - `vastdata.vms.realms`


## [1.2.0] - 2026-05-05

### Added

- New modules:
  - `vastdata.vms.protectionpolicies`
  - `vastdata.vms.protectedpaths`
  - `vastdata.vms.globalsnapstreams`
  - `vastdata.vms.snapshots`
  - `vastdata.vms.nativereplicationremotetargets`
  - `vastdata.vms.user_key`


## [1.1.0] - 2026-03-29

### Added

- New modules for managing non-local identity providers:
  - `vastdata.vms.nonlocal_group` - Manage non-local groups
  - `vastdata.vms.nonlocal_user` - Manage non-local users
- New module `vastdata.vms.eventdefinitionconfigs` - Manage event definition configurations
- Debug tracing and centralized timeout support for improved troubleshooting
- Galaxy version and git commit stamped into User-Agent header for request traceability

### Changed

- Replaced `vastpy` SDK dependency with a self-contained REST client (`VastClient`)


## [1.0.0] - 2025-02-26

### Added - First Public Release

- Initial public release of VAST Ansible Collection
- 10 core modules for VAST storage management:
  - `vastdata.vms.views` - Manage VAST views (file system exports)
  - `vastdata.vms.viewpolicies` - Manage view policies and configurations
  - `vastdata.vms.vippools` - Manage VIP pools for network configuration
  - `vastdata.vms.quotas` - Manage storage quotas
  - `vastdata.vms.s3policies` - Manage S3 bucket policies
  - `vastdata.vms.tenants` - Manage multi-tenancy configurations
  - `vastdata.vms.groups` - Manage user groups
  - `vastdata.vms.users` - Manage user accounts
  - `vastdata.vms.ldaps` - Configure LDAP authentication
  - `vastdata.vms.dns` - Manage DNS settings
- Authentication via token (VAST 5.3+) or username/password
- Full idempotency and check mode support
- Diff mode for change preview
- Comprehensive module documentation
- Unit and sanity test coverage
- Python 3.9+ and ansible-core 2.14+ support
