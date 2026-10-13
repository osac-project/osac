====================================
Purestorage.Flashblade Release Notes
====================================

.. contents:: Topics

v1.26.0
=======

Minor Changes
-------------

- purefb - Add API-client token authentication as an alternative to ``api_token``, via a pre-signed ``id_token`` or a ``private_key_file`` (with ``client_id``, ``key_id``, ``issuer``, ``username``).

Deprecated Features
-------------------

- purefb_fs - The ``nfs_rules`` parameter is deprecated in favour of ``export_policy`` and will be removed in 2.0.0. A deprecation notice is emitted when it is used.

Bugfixes
--------

- purefb_bucket - Default context to the local array name to avoid errors from an empty context_names on newer Purity//FB releases
- purefb_bucket - Only send context_names when a context is set, and only default it for fleet members, fixing "not in a fleet" errors on standalone arrays
- purefb_bucket - Stop sending context_names when destroying a bucket without a context, which leaked an empty/invalid fleet context
- purefb_bucket_access - Default context to the local array name to avoid errors from an empty context_names on newer Purity//FB releases
- purefb_bucket_access - Only send context_names when a context is set, and only default it for fleet members, fixing "not in a fleet" errors on standalone arrays
- purefb_bucket_replica - Allow a replica link to be removed when its local bucket no longer exists (https://github.com/Everpure-Ansible/FlashBlade-Collection/issues/545)
- purefb_bucket_replica - Default context to the local array name to avoid errors from an empty context_names on newer Purity//FB releases
- purefb_bucket_replica - Only send context_names when a context is set, and only default it for fleet members, fixing "not in a fleet" errors on standalone arrays
- purefb_connect - Default context to the local array name to avoid errors from an empty context_names on newer Purity//FB releases
- purefb_connect - Only send context_names when a context is set, and only default it for fleet members, fixing "not in a fleet" errors on standalone arrays
- purefb_export - Default context to the local array name to avoid errors from an empty context_names on newer Purity//FB releases
- purefb_export - Only send context_names when a context is set, and only default it for fleet members, fixing "not in a fleet" errors on standalone arrays
- purefb_fs - Default context to the local array name to avoid errors from an empty context_names on newer Purity//FB releases
- purefb_fs - Fixed ``not in a fleet`` errors on standalone arrays by only sending ``context_names`` when a context is set, and only defaulting the context for arrays that are fleet members.
- purefb_fs - Restore the ``nfs_rules`` parameter, silently ignored since v1.25.0. Inline NFS export rules are applied again on create and update for non-realm filesystems.
- purefb_groupquota - Default context to the local array name to avoid errors from an empty context_names on newer Purity//FB releases
- purefb_groupquota - Only send context_names when a context is set, and only default it for fleet members, fixing "not in a fleet" errors on standalone arrays
- purefb_info - Fix AttributeError where the policy loop variable was overwritten with `policy.name`, breaking `gather_subset=policies` (#547)
- purefb_lifecycle - Default context to the local array name to avoid errors from an empty context_names on newer Purity//FB releases
- purefb_lifecycle - Only send context_names when a context is set, and only default it for fleet members, fixing "not in a fleet" errors on standalone arrays
- purefb_policy - Create a policy rule when the client lookup returns no match or an error, fixing failures adding the first rule for a client while keeping the task idempotent
- purefb_policy - Fixed NFS export policy rules not updating when only the ``access`` value (e.g. root-squash to no-squash) changed; ``access`` was missing from the idempotency comparison.
- purefb_policy - Fixed an object store access policy rule update referencing a non-existent key when preserving existing source IPs.
- purefb_policy - Fixed policy rule updates (NFS export, SMB share, SMB client, network access) resetting unspecified fields to null; patches now use merged values so omitted fields are kept.
- purefb_policy - Fixed snapshot policy updates dropping the ``at`` time and timezone (reverting to an interval-only rule) when ``every`` or ``keep_for`` were changed without re-specifying ``at``.
- purefb_policy - Only send context_names when a context is set, and only default it for fleet members, avoiding empty-context errors and "not in a fleet" errors on standalone arrays
- purefb_remote_cred - Default context to the local array name to avoid errors from an empty context_names on newer Purity//FB releases
- purefb_remote_cred - Only send context_names when a context is set, and only default it for fleet members, fixing "not in a fleet" errors on standalone arrays
- purefb_s3acc - Default context to the local array name to avoid errors from an empty context_names on newer Purity//FB releases
- purefb_s3acc - Only send context_names when a context is set, and only default it for fleet members, fixing "not in a fleet" errors on standalone arrays
- purefb_s3user - Default context to the local array name to avoid errors from an empty context_names on newer Purity//FB releases
- purefb_s3user - Only send context_names when a context is set, and only default it for fleet members, fixing "not in a fleet" errors on standalone arrays
- purefb_snap - Default context to the local array name to avoid errors from an empty context_names on newer Purity//FB releases
- purefb_snap - Only send context_names when a context is set, and only default it for fleet members, fixing "not in a fleet" errors on standalone arrays
- purefb_userpolicy - Default context to the local array name to avoid errors from an empty context_names on newer Purity//FB releases
- purefb_userpolicy - Only send context_names when a context is set, and only default it for fleet members, fixing "not in a fleet" errors on standalone arrays
- purefb_userquota - Default context to the local array name to avoid errors from an empty context_names on newer Purity//FB releases
- purefb_userquota - Only send context_names when a context is set, and only default it for fleet members, fixing "not in a fleet" errors on standalone arrays
- purefb_virtualhost - Default context to the local array name to avoid errors from an empty context_names on newer Purity//FB releases
- purefb_virtualhost - Only send context_names when a context is set, and only default it for fleet members, fixing "not in a fleet" errors on standalone arrays

New Modules
-----------

- purestorage.flashblade.purefb_s3_export_policy - Manage FlashBlade S3 Export Policies
- purestorage.flashblade.purefb_s3acc_export - Manage FlashBlade Object Store Account exports

v1.25.1
=======

Bugfixes
--------

- purefb_info - Fixed bucket AttributeError

v1.25.0
=======

Minor Changes
-------------

- Add comprehensive testing infrastructure with pytest framework (#502)
- Add comprehensive unit tests for purefb.py utilities (get_system, purefb_argument_spec)
- Add coverage reporting with HTML and XML artifacts (#503)
- Add coverage summary to GitHub Actions job output (#503)
- Add pytest configuration and shared test fixtures (#502)
- Add test requirements and directory structure (#502)
- Add unit test execution to GitHub Actions CI workflow (#503)
- Add unit tests for common module utilities (#502)
- Add unit tests for purefb_eula module
- Add unit tests for purefb_info module
- Add unit tests for purefb_timeout module
- Add unit tests for simple modules (purefb_bladename, purefb_timeout, purefb_eula)
- Add unit tests for time_utils module with 100% coverage (#502)
- Expand test coverage from 15% to improve code quality and prevent regressions
- Removed multiple un-pythonic range iterations
- Standardize all import checks of Pure SDK to use the same variable name
- common - Add comprehensive docstrings to human_to_bytes, human_to_real, and get_local_tz functions (#494)
- common - Add get_error_message() utility function for safe API error extraction (#496)
- purefb - Add comprehensive docstrings to get_system and purefb_argument_spec functions (#494)
- purefb_fs - Added ``realm`` parameter to support creating filesystems in realms (requires Purity//FB 4.6.1+)
- purefb_info - Updated to use get_policies_all() method and added policy type breakdown to default output

Bugfixes
--------

- common - Add remove_duplicates() utility function for list deduplication
- common - Consolidate duplicate _findstr implementations to prevent UnboundLocalError
- common - Remove deprecated time conversion functions (replaced by time_utils)
- purefb - Fix unsafe res.errors[0].message access that could cause IndexError (#496)
- purefb_ad - Correct encryption type from ``arcfour-hma`` to ``arcfour-hmac``
- purefb_ad - Fix unsafe res.errors[0].message access that could cause IndexError (#497)
- purefb_admin - Fix unsafe res.errors[0].message access that could cause IndexError (#497)
- purefb_alert - Fix unsafe res.errors[0].message access that could cause IndexError (#497)
- purefb_apiclient - Fix unsafe res.errors[0].message access that could cause IndexError (#497)
- purefb_banner - Fix unsafe res.errors[0].message access that could cause IndexError (#497)
- purefb_bladename - Fix unsafe res.errors[0].message access that could cause IndexError (#497)
- purefb_bucket - Fix unsafe res.errors[0].message access that could cause IndexError (#497)
- purefb_bucket - Fixed issue creating bucket with no versioning incorrectly failing
- purefb_bucket - Fixed module.warn() calls to be compatible with Ansible 2.15+ by removing msg= parameter
- purefb_bucket_access - Fix unsafe res.errors[0].message access that could cause IndexError (#497)
- purefb_bucket_replica - Added safety checks for empty lists before accessing [0] index
- purefb_bucket_replica - Added validation for missing target parameter when creating new replica links
- purefb_bucket_replica - Check actual list length instead of unreliable total_item_count
- purefb_bucket_replica - Fix unsafe res.errors[0].message access that could cause IndexError (#497)
- purefb_bucket_replica - Fixed IndexError 'list index out of range' in get_connected() function
- purefb_bucket_replica - Fixed iteration anti-pattern using range(len()) that could cause IndexError
- purefb_certgrp - Fix unsafe res.errors[0].message access that could cause IndexError (#497)
- purefb_certs - Corrects typos in the parameter name.
- purefb_certs - Fix unsafe res.errors[0].message access that could cause IndexError (#498)
- purefb_certs - Fixes certificate_type name from array to appliance
- purefb_certs - Fixes issue where intermediate_certificate was not be applied to certificates.
- purefb_certs - Removes immutable field certificate_type for the patch operation.
- purefb_connect - Fix unsafe res.errors[0].message access that could cause IndexError (#498)
- purefb_connect - Use unified time conversion from time_utils module
- purefb_dns - Fix unsafe res.errors[0].message access that could cause IndexError (#498)
- purefb_dns - Use remove_duplicates() from common instead of local remove() function
- purefb_ds - Fix unsafe res.errors[0].message access that could cause IndexError (#498)
- purefb_dsrole - Fix unsafe res.errors[0].message access that could cause IndexError (#498)
- purefb_export - Fix unsafe res.errors[0].message access that could cause IndexError (#498)
- purefb_fleet - Fix unsafe res.errors[0].message access that could cause IndexError (#498)
- purefb_fs - Consolidated duplicate get_fs() function and fixed unsafe list access (#493)
- purefb_fs - Fix unsafe res.errors[0].message access that could cause IndexError (#498)
- purefb_fs - Fixed failure to apply policies to existing filesystems due to incorrect API check and non-existent patch method
- purefb_fs - Fixed issue where NFS export policies were applied even when both nfsv3 and nfsv4 were disabled
- purefb_fs - Fixed issue where SMB policies were applied even when smb parameter was set to false
- purefb_fs_replica - Fix unsafe res.errors[0].message access that could cause IndexError (#498)
- purefb_groupquota - Consolidated duplicate get_fs() function and fixed unsafe list access (#493)
- purefb_groupquota - Fix unsafe res.errors[0].message access that could cause IndexError (#498)
- purefb_hardware - Fix unsafe res.errors[0].message access that could cause IndexError (#499)
- purefb_info - Use unified time conversion from time_utils module
- purefb_keytabs - Fix unsafe res.errors[0].message access that could cause IndexError (#499)
- purefb_kmip - Fix unsafe res.errors[0].message access that could cause IndexError (#499)
- purefb_lag - Fix unsafe res.errors[0].message access that could cause IndexError (#499)
- purefb_lifecycle - Fix unsafe res.errors[0].message access that could cause IndexError (#499)
- purefb_lifecycle - Use unified time conversion from time_utils module with proper None handling
- purefb_messages - Fix unsafe res.errors[0].message access that could cause IndexError (#499)
- purefb_network - Fix unsafe res.errors[0].message access that could cause IndexError (#499)
- purefb_ntp - Fix unsafe res.errors[0].message access that could cause IndexError (#499)
- purefb_ntp - Use remove_duplicates() from common instead of local remove() function
- purefb_phonehome - Fix unsafe res.errors[0].message access that could cause IndexError (#499)
- purefb_pingtrace - Fix unsafe res.errors[0].message access that could cause IndexError (#499)
- purefb_policy - Fix UnboundLocalError when policy string is not found
- purefb_policy - Fix unsafe res.errors[0].message access that could cause IndexError (#499)
- purefb_policy - Fixed AttributeError with empty policy rules
- purefb_policy - Use unified time conversion from time_utils module
- purefb_proxy - Fix unsafe res.errors[0].message access that could cause IndexError (#500)
- purefb_ra - Fix unsafe res.errors[0].message access that could cause IndexError (#500)
- purefb_remote_cred - Fix unsafe res.errors[0].message access that could cause IndexError (#500)
- purefb_s3acc - Fix unsafe res.errors[0].message access that could cause IndexError (#500)
- purefb_s3user - Fix unsafe res.errors[0].message access that could cause IndexError (#500)
- purefb_saml - Fix unsafe res.errors[0].message access that could cause IndexError (#500)
- purefb_saml - Fixed typo in model name
- purefb_server - Fix unsafe res.errors[0].message access that could cause IndexError (#500)
- purefb_smtp - Fix unsafe res.errors[0].message access that could cause IndexError (#500)
- purefb_snap - Consolidated duplicate get_fs() function and fixed unsafe list access (#493)
- purefb_snap - Fix unsafe res.errors[0].message access that could cause IndexError (#500)
- purefb_snmp_agent - Fix unsafe res.errors[0].message access that could cause IndexError (#500)
- purefb_snmp_mgr - Fix unsafe res.errors[0].message access that could cause IndexError (#500)
- purefb_subnet - Fix unsafe res.errors[0].message access that could cause IndexError (#500)
- purefb_syslog - Fix unsafe res.errors[0].message access that could cause IndexError (#501)
- purefb_target - Fix unsafe res.errors[0].message access that could cause IndexError (#501)
- purefb_tz - Fix UnboundLocalError when timezone string is not found
- purefb_tz - Fix unsafe res.errors[0].message access that could cause IndexError (#501)
- purefb_user - Fix unsafe res.errors[0].message access that could cause IndexError (#501)
- purefb_user - Fixed NameError by replacing deprecated AdminRole with ReferenceWritable when changing user roles
- purefb_user - Use unified time conversion from time_utils module
- purefb_userpolicy - Fix unsafe res.errors[0].message access that could cause IndexError (#501)
- purefb_userquota - Consolidated duplicate get_fs() function and fixed unsafe list access (#493)
- purefb_userquota - Fix unsafe res.errors[0].message access that could cause IndexError (#501)
- time_utils - Add unified time conversion module with proper error handling and input validation

New Modules
-----------

- purestorage.flashblade.purefb_export - Manage filesystem exports on Everpure FlashBlade`
- purestorage.flashblade.purefb_realm - Manage realms on Everpure FlashBlades

v1.24.0
=======

Bugfixes
--------

- purefb_ad - Ensure encryption algorithms used match the GUI values
- purefb_certs - Fix the syntax to generate a CSR
- purefb_ds - Fixed issue when creating pre-enabled management directory service

v1.23.1
=======

Minor Changes
-------------

- purefb_info - Added MAC address information for LAGs

Bugfixes
--------

- purefb_bucket_replica - Fixed IndexError crash in check loop
- purefb_pingtrace - Fiexed issue with XFM module when state is ping

v1.23.0
=======

Minor Changes
-------------

- purefa_ad - Added support for local servers using the ``server`` parameter.
- purefb_ad - Added test and rotate states
- purefb_ad - Remove doc references to FQDNs as SPNs are the required method.
- purefb_ad - Updated encryption algorithms to use correct values
- purefb_ds - Allow directory services to be modified for internal NFS servers
- purefb_ds - Update test state to allow specific tests to be run
- purefb_info - Added MAC address information for LAGs

Bugfixes
--------

- purefb_alert - Fixed issue with syntax error in update function
- purefb_bucket_replica - Fixed issue with ItemIterator error

v1.22.0
=======

Minor Changes
-------------

- module_utils/purefb - Remove `get_blade()` function as not required for REST v2
- purefb_admin - Remove references to unsupported API versions
- purefb_alert - Add new ``state`` of ``test`` to check alert manager configuration
- purefb_alert - Upgraded to REST v2
- purefb_banner - Upgraded to REST v2
- purefb_bladename - Upgraded to REST v2
- purefb_bucket - Added Fusion support
- purefb_bucket - Updated to REST v2
- purefb_bucket_access - Fusion support added
- purefb_bucket_replica - Add Fusion support
- purefb_bucket_replica - Upgraded to REST v2
- purefb_certgrp - Upgraded to REST v2
- purefb_connect - Added Fusion support
- purefb_connect - Remove references to unsupported API versions
- purefb_connect - Upgraded to REST v2
- purefb_ds - Added new state of ``test`` to enable directory services to run diagnostics test
- purefb_ds - Updated to REST v2
- purefb_dsrole - Upgraded to REST v2
- purefb_eula - Converted to REST v2
- purefb_fs - Added support for Fusion
- purefb_fs - Upgraded to use REST 2
- purefb_fs_replica - Upgraded to REST v2
- purefb_groupquota - Fusion support added
- purefb_groupquota - Upgraded to REST v2
- purefb_info - Upgraded to REST v2
- purefb_inventory - Upgraded to REST v2
- purefb_lifecycle - Fusion support added
- purefb_lifecycle - Upgraded to REST v2
- purefb_network - Upgraded to REST v2
- purefb_ntp - Upgraded to REST v2
- purefb_phonehome - Add new ``state`` of ``test`` to check phonehome configuration
- purefb_phonehome - Upgrwded to REST v2
- purefb_pingtrace - Ehanced JSON response for ping
- purefb_policy - Add Fusion support
- purefb_policy - Remove references to unsupported API versions
- purefb_policy - Upgraded to REST v2
- purefb_ra - Add new ``state`` of ``test`` to check remote support configuration
- purefb_remote_cred - Fusion support added
- purefb_remote_cred - Upgraded to REST v2
- purefb_s3acc - Fusion support added
- purefb_s3acc - Remove references to unsupported API versions
- purefb_s3user - Fusion support added
- purefb_snamp_agent - Upgraded to REST v2
- purefb_snap - Fusion support added
- purefb_snap - Upgraded to REST v2
- purefb_snmp_mgr - Add new ``state`` of ``test`` to check SNMP manager configuration
- purefb_snmp_mgr - Upgraded to REST v2
- purefb_subnet - Upgraded to REST v2
- purefb_syslog - Converted to REST v2
- purefb_target - Upgraded to REST v2
- purefb_userpolicy - Fusion support added
- purefb_userquota - Added Fusion support
- purefb_userquota - Upgraded to REST v2
- purefb_virtualhost - Fusion support added

New Modules
-----------

- purestorage.flashblade.purefb_kmip - Manage FlashBlade KMIP server objects

v1.21.2
=======

v1.21.1
=======

Minor Changes
-------------

- purefb_ad - Revert removal of ``service`` parameter (breaking change). Added more logic to use of ``service`` parameter and recommend use of ``service_principals`` with service incorporated.

v1.21.0
=======

Minor Changes
-------------

- purefb_ad - ``service`` parameter removed to comply with underlying API structure. ``service`` should be included in the ``service_principals`` strings as shown in the documentation.
- purefb_saml - Added ``entity_id`` parameter
- purefb_snap - Add support to delete/eradicate remote snapshots, including the latest replica
- purefb_user - All AD users to have SSH keys and/or API tokens assigned, even if they have never accessed the FlashArray before. AD users must have ``ad_user`` set as ``true``.

Bugfixes
--------

- purefb_ad - Fixed issue where updating an AD account required unnecessary parameters.
- purefb_bucket - Fix versioning control and access rules for public buckets
- purefb_bucket - Fixed issue where a bucket with no versioning defined was incorrectly created.
- purefb_bucket - Fixed issue with default retention parameter
- purefb_bucket_access - Fixed typo in CORS rule definition
- purefb_certs - Fixed issues with importing external certificates
- purefb_certs - Updated email regex pattern to fix ``re`` failures
- purefb_dns - Fixed multiple issues for data DNS configuration
- purefb_fs - Ensured that NFS rules are emprty if requested filesystem is SMB only
- purefb_info - Fixed error when ``default`` subset fails if SMD has been disabled on the FLashBlade
- purefb_policy - Fixed typo when calling object store policy rule deletion
- purefb_s3user - Fixed typo in imported keys code
- purefb_subnet - Ensured prefix is required for subnet creation or update

v1.20.0
=======

Minor Changes
-------------

- purefb_ad - Add support for Global Catalog Servers
- purefb_dns - Added support for multiple DNS configurations.
- purefb_ds - SMB directory services deprecated from Purity//FB 4.5.2
- purefb_info - Add support for Active Directory Global Catalog Servers
- purefb_info - Added snapshot creation date-time and time_remaining, if snapshot is not deleted, to the ``snapshots`` response.
- purefb_info - Added support for multiple DNS configurations.
- purefb_policy - Snapshot policies can now have specific filesystems and/or replica links added or deletred from the policy
- purefb_proxy - Added support to update existing proxy
- purefb_proxy - Updated to REST v2
- purefb_s3user - Changed ``key_state`` state to be ``keystate`` as ``key_state`` is reserved.
- purefb_s3user - Changed ``remove_key`` parameter to ``key_name`` and add new ``state`` of ``key_state`` to allow a specificed key to be enabled/disabled using the new parameter ``enable_key``.
- purefb_s3user - Updated failure messages for applying policies to an object user account.
- purefb_subnet - ``prefix`` removed as a required parameter for updating an existing subnet

Bugfixes
--------

- purefb_bucket - Resolved issue with removing bucket quota
- purefb_info - Fixed issue after SMD Directory Services no longer avaible from REST 2.16
- purefb_policy - Fixed creation of snapshot policies with assigned filesystems and/or replica links
- purefb_s3acc - Fixed issue with public access config settings not being correctly for an account

New Modules
-----------

- purestorage.flashblade.purefb_bucket_access - Manage FlashBlade bucket access policies
- purestorage.flashblade.purefb_fleet - Manage Fusion Fleet
- purestorage.flashblade.purefb_server - Manage FlashBlade servers

v1.19.2
=======

Bugfixes
--------

- purefb_bucket - Fixed issue with idempotency reported when ``hard_limit`` not provided.
- purefb_info - Fixed ``AttributeError`` for ``snapshot`` subset when snapshot had been created manually, rather than using a snapshot policy
- purefb_info - Fixed issue with admin token creation time and bucket policies
- purefb_policy - Fixed syntax error is account name.
- purefb_smtp - Fix errors that occurred after adding support for smtp encrpytion and using the module on older FlashBlades.
- purefb_snap - Fixed issue where ``target`` incorrectly required for a regular snapshot

v1.19.1
=======

Minor Changes
-------------

- purefb_bucket - Allow bucket quotas to be modified.

v1.19.0
=======

Minor Changes
-------------

- multiple - YAML lint fixes based on updated ``ansible-lint`` version
- purefb_info - Add ``time_remaining_status`` to bucket information from REST 2.14
- purefb_info - Expose SMTP encryption mode
- purefb_policy - Add new policy type of ``worm`` which is availble from Purity//FB 4.5.0
- purefb_smtp - Add encryption mode support from Purity//FB 4.5.0
- purefb_snap - Change ``targets`` to ``target` and from ``list`` to ``str``. ``targets`` added as alias and code to ensure existing list in playbooks is translated as a string.
- purefb_syslog - Enable ``services`` parameter and also the ability update existing syslog servers from REST 2.14

Bugfixes
--------

- purefb_certs - Fix issue with importing certificates
- purefb_certs - Fix parameter mispelling of ``intermeadiate_cert`` to ``intermediate_cert``. Keep original mispelling as an alias.
- purefb_ds - Initialize variable correctly
- purefb_policy - Initialize variable correctly
- purefb_ra - Fix incorrect import statement
- purefb_snap - Fix issue with immeadiate remote snapshots not executing

New Modules
-----------

- purestorage.flashblade.purefb_saml - Manage FlashBlade SAML2 service and identity providers

v1.18.0
=======

Minor Changes
-------------

- all - add ``disable_warnings`` parameters
- purefb_bucket - Add ``safemode`` option for ``retention_mode``
- purefb_certs - Update module to use REST v2 code. This brings in new parameters for certificate management.
- purefb_fs - Set default for group_ownership to be creator
- purefb_ra - Add ``duration`` option from REST 2.14
- purefb_ra - Update to REST2

Bugfixes
--------

- purefb_fs - Fix conflict with SMB mode and ACL safeguarding
- purefb_fs - Fix error checking for SMB parameter in non-SMB filesystem
- purefb_info - Fix space reporting issue

v1.17.0
=======

Minor Changes
-------------

- purefb_bucket - Add support for strict 17a-4 WORM compliance.
- purefb_connect - Increase Fan-In and Fan-Out maximums
- purefb_fs - Add ``group_ownership`` parameter from Purity//FB 4.4.0.
- purefb_info - Show array network access policy from Purity//FB 4.4.0
- purefb_policy - Add support for network access policies from Purity//FB 4.4.0

v1.16.0
=======

Minor Changes
-------------

- purefb_ds - Add `force_bind_password` parameter to allow module to be idempotent.

Bugfixes
--------

- purefb_bucket - Changed logic to allow complex buckets to be created in a single call, rather than having to split into two tasks.
- purefb_lag - Enable LAG port configuration with multi-chassis
- purefb_timeout - Fixed arithmetic error that resulted in module incorrectly reporting changed when no change was required.

v1.15.0
=======

Minor Changes
-------------

- purefb_bucket - Add support for public buckets
- purefb_bucket - From REST 2.12 the `mode` parameter default changes to `multi-site-writable`.
- purefb_fs - Added SMB Continuous Availability parameter. Requires REST 2.12 or higher.
- purefb_info - Added enhanced information for buckets, filesystems and snapshots, based on new features in REST 2.12
- purefb_s3acc - Add support for public buckets
- purefb_s3acc - Remove default requirements for ``hard_limit`` and ``default_hard_limit``

Bugfixes
--------

- purefb_info - Added missing object lock retention details if enabledd

New Modules
-----------

- purestorage.flashblade.purefb_hardware - Manage FlashBlade Hardware

v1.14.0
=======

Minor Changes
-------------

- purefb_bucket_replica - Added support for cascading replica links
- purefb_info - New fields to display free space (remaining quota) for Accounts and Buckets. Space used by destroyed buckets is split out from virtual field to new destroyed_virtual field
- purefb_info - Report encryption state in SMB client policy rules
- purefb_info - Report more detailed space data from Purity//FB 4.3.0
- purefb_policy - Add deny effect for object store policy rules. Requires Purity//FB 4.3.0+
- purefb_policy - Added parameter to define object store policy description

Bugfixes
--------

- purefb_userpolicy - Fixed `show` state for all user policies

v1.13.1
=======

Minor Changes
-------------

- purefb_policy - Add new and updated policy access rights

Bugfixes
--------

- purefb_info - Fixed missing atributes for SMB client policy rules

v1.13.0
=======

v1.12.0
=======

Minor Changes
-------------

- purefb_fs - Added support for SMB client and share policies
- purefb_fs_replica - Added support to delete filesystem replica links from REST 2.10
- purefb_info - Add drive type in drives subset for //S and //E platforms. Only available from REST 2.9.
- purefb_info - Added support for SMB client and share policies
- purefb_policy - Added support for SMB client and share policies
- purefb_s3acc - Allow human readable quota sizes; eg. 1T, 230K, etc
- purefb_s3user - Add new boolean parameter I(multiple_keys) to limit access keys for a user to a single key.

Bugfixes
--------

- purefb_bucket - Fixed bucket type mode name typo
- purefb_fs - Fixed issue with incorrect promotion state setting

v1.11.0
=======

Minor Changes
-------------

- purefb_info - Added `encryption` and `support_keys` information.
- purefb_info - Added bucket quota and safemode information per bucket
- purefb_info - Added security update version for Purity//FB 4.0.2, or higher
- purefb_info - Updated object store account information
- purefb_inventory - Added `part_number` to hardware item information.
- purefb_policy - Added support for multiple rules in snapshot policies
- purefb_proxy - Added new boolean parameter `secure`. Default of true (for backwards compatability) sets the protocol to be `https://`. False sets `http://`
- purefb_s3acc - Added support for default bucket quotas and hard limits
- purefb_s3acc - Added support for object account quota and hard limit

Bugfixes
--------

- purefb_info - Fixed issue when more than 10 buckets have lifecycle rules.
- purefb_s3user - Fix incorrect response when bad key/secret pair provided for new user

New Modules
-----------

- purestorage.flashblade.purefb_pingtrace - Employ the internal FlashBlade ping and trace mechanisms

v1.10.0
=======

Minor Changes
-------------

- All - Update documentation examples with FQCNs
- purefb_ad - Allow service to be a list
- purefb_bucket - Allow setting of bucket type to support VSO - requires Purity//FB 3.3.3 or higher
- purefb_certs - Fix several misspellings of certificate
- purefb_info - Added filesystem default, user and group quotas where available
- purefb_info - Expose object store bucket type from Purity//FB 3.3.3
- purefb_info - Show information for current timezone
- purefb_policy - Allow rename of NFS Export Policies from Purity//FB 3.3.3
- purefb_tz - Add support for FlashBlade timezone management

Bugfixes
--------

- purefb_connect - Resolve connection issues between two FBs that are throttling capable
- purefb_policy - Fix incorrect API call for NFS export policy rule creation

New Modules
-----------

- purestorage.flashblade.purefb_messages - List FlashBlade Alert Messages
- purestorage.flashblade.purefb_tz - Configure Pure Storage FlashBlade timezone

v1.9.0
======

Minor Changes
-------------

- purefb_admin - New module to manage global admin settings
- purefb_connect - Add support for array connections to have bandwidth throttling defined
- purefb_fs - Add support for NFS export policies
- purefb_info - Add NFS export policies and rules
- purefb_info - Show array connections bandwidth throttle information
- purefb_policy - Add NFS export policies, with rules, as a new policy type
- purefb_policy - Add support for Object Store Access Policies, associated rules and user grants
- purefb_policy - New parameter `policy_type` added. For backwards compatability, default to `snapshot` if not provided.

v1.8.1
======

Minor Changes
-------------

- purefb.py - Use latest `pypureclient` SDK with fix for "best fit". No longer requires double login to negotiate best API version.

v1.8.0
======

Minor Changes
-------------

- purefb.py - Add check to ensure FlashBlade uses the latest REST version possible for Purity version installed
- purefb_info - Add object lifecycles rules to bucket subset
- purefb_lifecycle - Add support for updated object lifecycle rules. See documentation for details of new parameters.
- purefb_lifecycle - Change `keep_for` parameter to be `keep_previous_for`. `keep_for` is deprecated and will be removed in a later version.
- purefb_user - Add support for managing user public key and user unlock

Known Issues
------------

- purefb_lag - The mac_address field in the response is not populated. This will be fixed in a future FlashBlade update.

v1.7.0
======

Minor Changes
-------------

- purefb_groupquota - New module for manage individual filesystem group quotas
- purefb_lag - Add support for LAG management
- purefb_snap - Add support for immeadiate snapshot to remote connected FlashBlade
- purefb_subnet - Add support for multiple LAGs.
- purefb_userquota - New module for manage individual filesystem user quotas

Bugfixes
--------

- purefb_fs - Fix bug where changing the state of both NFS v3 and v4.1 at the same time ignored one of these.
- purefb_s3acc - Ensure S3 Account Name is always lowercase
- purefb_s3user - Ensure S3 Account Name is always lowercase
- purefb_subnet - Allow subnet creation with no gateway

New Modules
-----------

- purestorage.flashblade.purefb_groupquota - Manage filesystem group quotas
- purestorage.flashblade.purefb_lag - Manage FlashBlade Link Aggregation Groups
- purestorage.flashblade.purefb_userquota - Manage filesystem user quotas

v1.6.0
======

Minor Changes
-------------

- purefb_ad - New module to manage Active Directory Account
- purefb_eula - New module to sign EULA
- purefb_info - Add Active Directory, Kerberos and Object Store Account information
- purefb_info - Add extra info for Purity//FB 3.2+ systems
- purefb_keytabs - New module to manage Kerberos Keytabs
- purefb_s3user - Add access policy option to user creation
- purefb_timeout - Add module to set GUI idle timeout
- purefb_userpolicy - New module to manage object store user access policies
- purefb_virtualhost - New module to manage API Clients
- purefb_virtualhost - New module to manage Object Store Virtual Hosts

New Modules
-----------

- purestorage.flashblade.purefb_ad - Manage FlashBlade Active Directory Account
- purestorage.flashblade.purefb_apiclient - Manage FlashBlade API Clients
- purestorage.flashblade.purefb_eula - Sign Pure Storage FlashBlade EULA
- purestorage.flashblade.purefb_keytabs - Manage FlashBlade Kerberos Keytabs
- purestorage.flashblade.purefb_timeout - Configure Pure Storage FlashBlade GUI idle timeout
- purestorage.flashblade.purefb_userpolicy - Manage FlashBlade Object Store User Access Policies
- purestorage.flashblade.purefb_virtualhost - Manage FlashBlade Object Store Virtual Hosts

v1.5.0
======

Minor Changes
-------------

- purefb_certs - Add update functionality for array cert
- purefb_fs - Add multiprotocol ACL support
- purefb_info - Add information regarding filesystem multiprotocol (where available)
- purefb_info - Add new parameter to provide details on admin users
- purefb_info - Add replication performace statistics
- purefb_s3user - Add ability to remove an S3 users existing access key

Bugfixes
--------

- purefb_* - Return a correct value for `changed` in all modules when in check mode
- purefb_dns - Deprecate search paramerter
- purefb_dsrole - Resolve idempotency issue
- purefb_lifecycle - Fix error when creating new bucket lifecycle rule.
- purefb_policy - Ensure undeclared variables are set correctly
- purefb_s3user - Fix maximum access_key count logic

v1.4.0
======

Minor Changes
-------------

- purefb_banner - Module to manage the GUI and SSH login message
- purefb_certgrp - Module to manage FlashBlade Certificate Groups
- purefb_certs - Module to create and delete SSL certificates
- purefb_connect - Support idempotency when exisitng connection is incoming
- purefb_fs - Add new options for filesystem control (https://github.com/Pure-Storage-Ansible/FlashBlade-Collection/pull/81)
- purefb_fs - Default filesystem size on creation changes from 32G to ``unlimited``
- purefb_fs - Fix error in deletion and eradication of filesystem
- purefb_fs_replica - Remove condition to attach/detach policies on unhealthy replica-link
- purefb_info - Add support to list filesystem policies
- purefb_lifecycle - Module to manage FlashBlade Bucket Lifecycle Rules
- purefb_s3user - Add support for imported user access keys
- purefb_syslog - Module to manage syslog server configuration

Bugfixes
--------

- purefb_connect - Ensure changing encryption status on array connection is performed correctly
- purefb_connect - Fix breaking change created in purity_fb SDK 1.9.2 for deletion of array connections
- purefb_connect - Hide target array API token
- purefb_ds - Ensure updating directory service configurations completes correctly
- purefb_info - Fix issue getting array info when encrypted connection exists
- purefb_policy - Resolve multiple issues related to incorrect use of timezones

New Modules
-----------

- purestorage.flashblade.purefb_banner - Configure Pure Storage FlashBlade GUI and SSH MOTD message
- purestorage.flashblade.purefb_certgrp - Manage FlashBlade Certifcate Groups
- purestorage.flashblade.purefb_certs - Manage FlashBlade SSL Certifcates
- purestorage.flashblade.purefb_lifecycle - Manage FlashBlade object lifecycles
- purestorage.flashblade.purefb_syslog - Configure Pure Storage FlashBlade syslog settings

v1.3.0
======

Release Summary
---------------

| Release Date: 2020-08-08
| This changlelog describes all changes made to the modules and plugins included in this collection since Ansible 2.9.0

Major Changes
-------------

- purefb_alert - manage alert email settings on a FlashBlade
- purefb_bladename - manage FlashBlade name
- purefb_bucket_replica - manage bucket replica links on a FlashBlade
- purefb_connect - manage connections between FlashBlades
- purefb_dns - manage DNS settings on a FlashBlade
- purefb_fs_replica - manage filesystem replica links on a FlashBlade
- purefb_inventory - get information about the hardware inventory of a FlashBlade
- purefb_ntp - manage the NTP settings for a FlashBlade
- purefb_phonehome - manage the phone home settings for a FlashBlade
- purefb_policy - manage the filesystem snapshot policies for a FlashBlade
- purefb_proxy - manage the phone home HTTP proxy settings for a FlashBlade
- purefb_remote_cred - manage the Object Store Remote Credentials on a FlashBlade
- purefb_snmp_agent - modify the FlashBlade SNMP Agent
- purefb_snmp_mgr - manage SNMP Managers on a FlashBlade
- purefb_target - manage remote S3-capable targets for a FlashBlade
- purefb_user - manage local ``pureuser`` account password on a FlashBlade

Minor Changes
-------------

- purefb_bucket - Versioning support added
- purefb_info - new options added for information collection
- purefb_network - Add replication service type
- purefb_s3user - Limit ``access_key`` recreation to 3 times
- purefb_s3user - return dict changed from ``ansible_facts`` to ``s3user_info``

Bugfixes
--------

- purefb_bucket - Add warning message if ``state`` is ``absent`` without ``eradicate:``
- purefb_fs - Add graceful exist when ``state`` is ``absent`` and filesystem not eradicated
- purefb_fs - Add warning message if ``state`` is ``absent`` without ``eradicate``
