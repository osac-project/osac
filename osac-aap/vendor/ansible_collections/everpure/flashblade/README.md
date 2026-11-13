[![Release](https://img.shields.io/github/v/tag/Everpure-Ansible/FlashBlade-Collection?label=release)](https://github.com/Everpure-Ansible/FlashBlade-Collection/releases/latest)
[![License](https://img.shields.io/badge/license-GPL%20v3.0-brightgreen.svg)](https://github.com/Everpure-Ansible/FlashBlade-Collection/blob/master/COPYING.GPLv3)
[![CLA assistant](https://cla-assistant.io/readme/badge/Everpure-Ansible/FlashBlade-Collection)](https://cla-assistant.io/Everpure-Ansible/FlashBlade-Collection)
[![Everpure Ansible CI](https://github.com/Everpure-Ansible/FlashBlade-Collection/workflows/Everpure%20Ansible%20CI/badge.svg)](https://github.com/Everpure-Ansible/FlashBlade-Collection/actions)
[![Code style: black](https://img.shields.io/badge/code%20style-black-000000.svg)](https://github.com/psf/black)
[![Tests](https://img.shields.io/badge/tests-passing-brightgreen.svg)](https://github.com/Everpure-Ansible/FlashBlade-Collection/actions)
    
# Everpure FlashBlade Collection

## Description

The Everpure FlashBlade collection consists of the latest versions of the FlashBlade modules.

## Requirements

- Everpure FlashBlade system running Purity//FB 3.3.3 or later
- py-pure-client >=1.88.0
- python >=3.9
- netaddr
- pytz
- distro
- pycountry
- urllib3

## Installation

Before using this collection, you need to install it with the Ansible Galaxy command-line tool:

```
ansible-galaxy collection install everpure.flashblade
```

You can also include it in a `requirements.yml` file and install it with `ansible-galaxy collection install -r requirements.yml`, using the format:

```
collections:
  - name: everpure.flashblade
```

Note that if you install the collection from Ansible Galaxy, it will not be upgraded automatically when you upgrade the Ansible package. 

To upgrade the collection to the latest available version, run the following command:

```
ansible-galaxy collection install everpure.flashblade --upgrade
```

You can also install a specific version of the collection, for example, if you need to downgrade when something is broken in the latest version (please report an issue in this repository). Use the following syntax to install version 1.0.0:

```
ansible-galaxy collection install everpure.flashblade:==1.0.0
```

See [using Ansible collections](https://docs.ansible.com/ansible/devel/user_guide/collections_using.html) for more details.

## Use Cases

This collection can be used to perform any task that you would normally perform using the command-line or GUI on a FlashBlade. This also includes using the collection in larger playbooks to perform actions with storage-related components, such as:
* Infrastructure Drift Control
* Database Cloning
* Disaster Recovery

## Contributing

There are many ways in which you can participate in the project, for example:

* Submit bugs and feature requests, and help us verify as they are checked in
* Review source code changes
* Review the documentation and make pull requests for anything from typos to new content
* If you are interested in fixing issues and contributing directly to the code base, please see the details below:
    1. Fork this project into your account if you are a first-time contributor.
    2. Create a branch based on the latest `master` branch, commit your changes on this branch.
    3. You may merge the Pull Request in once you have the sign-off of two other developers, or if you do not have permission to do that, you may request the second reviewer to merge it for you.
 
## Support

Only the latest release of this collection is supported.

As Red Hat Ansible Certified Content, this collection is entitled to support through the Ansible Automation Platform (AAP) using the **Create issue** button on the top right corner. This is the Primary Support Route for a Certified Collection.

Community-Only help may be available on the [Ansible Forum](https://forum.ansible.com/) or raise a [GitHub issue](https://github.com/Everpure-Ansible/FlashBlade-Collection/issues) in the Collection repo.

## Release Notes

The full changelog is shipped with the collection as `CHANGELOG.rst` and is also available
[in the repository](https://github.com/Everpure-Ansible/FlashBlade-Collection/blob/master/CHANGELOG.rst).

Per-release notes can be found on the
[releases page](https://github.com/Everpure-Ansible/FlashBlade-Collection/releases).

## Related Information
### Idempotency

All modules are idempotent with the exception of modules that change or set passwords. Due to security requirements exisitng passwords can be validated against and therefore will always be modified, even if there is no change.

### Available Modules

- purefb_ad - manage Active Directory account on FlashBlade
- purefb_admin - configure Everpure FlashBlade Global Admin settings
- purefb_alert - manage alert email settings on a FlashBlade
- purefb_apiclient - manage API clients for FlashBlade
- purefb_banner - manage FlashBlade login banner
- purefb_bladename - manage FlashBlade name
- purefb_bucket - manage S3 buckets on a FlashBlade
- purefb_bucket_access - manage S3 bucket access policies on a FlashBlade
- purefb_bucket_replica - manage bucket replica links on a FlashBlade
- purefb_certgrp - manage FlashBlade certificate groups
- purefb_certs - manage FlashBlade SSL certificates
- purefb_connect - manage connections between FlashBlades
- purefb_dns - manage DNS settings on a FlashBlade
- purefb_ds - manage Directory Services settings on a FlashBlade
- purefb_dsrole - manage Directory Service Roles on a FlashBlade
- purefb_eula - manage EULA on FlashBlade
- purefb_export - manage filesystem exports on FlashBlade
- purefb_fleet - manage Fusion fleet members
- purefb_fs - manage filesystems on a FlashBlade
- purefb_fs_replica - manage filesystem replica links on a FlashBlade
- purefb_groupquota - manage individual group quotas on FlashBlade filesystems
- purefb_hardware - manage hardware LED identifiers and hardware connectors
- purefb_info - get information about the configuration of a FlashBlade
- purefb_inventory - get information about the hardware inventory of a FlashBlade
- purefb_keytabs - manage FlashBlade Kerberos keytabs
- purefb_kmip - manage FlashBlade KMIP servers
- purefb_lag - manage FlashBlade Link Aggregation Groups
- purefb_lifecycle - manage FlashBlade Bucket Lifecycle Rules
- purefb_local_ds - manage FlashBlade Local Directory Services
- purefb_local_group - manage local groups in a FlashBlade Local Directory Service
- purefb_local_user - manage local users in a FlashBlade Local Directory Service
- purefb_messages - list FlashBlade alert messages
- purefb_mgmt_auth_policy - manage FlashBlade management authentication policies and their members
- purefb_mgmt_policy - manage FlashBlade management access policies and their rules
- purefb_mgmt_role - manage FlashBlade custom management roles, permissions, and policy attachment
- purefb_network - manage the network settings for a FlashBlade
- purefb_ntp - manage the NTP settings for a FlashBlade
- purefb_password_policy - manage the FlashBlade management password policy
- purefb_phonehome - manage the phone home settings for a FlashBlade
- purefb_pingtrace - perform FlashBlade network diagnostics
- purefb_policy - manage the filesystem snapshot policies for a FlashBlade
- purefb_proxy - manage the phone home HTTP proxy settings for a FlashBlade
- purefb_ra - manage the Remote Assist connections on a FlashBlade
- purefb_realm - manage the Realms for a FlashBlade
- purefb_remote_cred - manage the Object Store Remote Credentials on a FlashBlade
- purefb_resource_access - manage resource access for a FlashBlade
- purefb_s3_export_policy - manage FlashBlade S3 export policies and their rules
- purefb_s3acc - manage the object store accounts on a FlashBlade
- purefb_s3acc_export - manage FlashBlade object store account exports
- purefb_s3user - manage the object atore users on a FlashBlade
- purefb_saml - manage FlashBlade SAML2 service and identity providers
- purefb_server - manage FlashBlade servers
- purefb_smtp - manage SMTP settings on a FlashBlade
- purefb_snap - manage filesystem snapshots on a FlashBlade
- purefb_snmp_agent - modify the FlashBlade SNMP Agent
- purefb_snmp_mgr - manage SNMP Managers on a FlashBlade
- purefb_subnet - manage network subnets on a FlashBlade
- purefb_syslog - manage FlashBlade syslog server configuration
- purefb_target - manage remote S3-capable targets for a FlashBlade
- purefb_timeout - manage FlashBlade GUI timeout
- purefb_tls_policy - manage FlashBlade TLS policies and their network interface attachments
- purefb_tz - configure Everpure FlashBlade timezone
- purefb_user - manage local *pureuser* account password on a FlashBlade
- purefb_userpolicy - manage FlashBlade Object Store User Access Policies
- purefb_userquota - manage individual user quotas on FlashBlade filesystems
- purefb_virtualhost - manage FlashBlade Object Store Virtual Hosts

## License Information

[BSD-2-Clause](https://directory.fsf.org/wiki?title=License:FreeBSD)

[GPL-3.0-or-later](https://www.gnu.org/licenses/gpl-3.0.en.html)

## Author

This collection was created in 2019 by [Simon Dodsley](https://github.com/sdodsley) for, and on behalf of, the [Everpure Ansible Team](mailto:pure-ansible-team@everpuredata.com)
