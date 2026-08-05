# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
