=================================
Everpure.Flashblade Release Notes
=================================

.. contents:: Topics

v1.29.0
=======

Minor Changes
-------------

- CI - The artifact job now fails on any symlink in the build, on unreleased fragments or test files being shipped, and when ``CHANGELOG.rst`` has no section for the version in ``galaxy.yml``. It also runs ansible-lint against the artifact, not only ansible-test sanity.
- CI - The certification workflow now also builds the collection and runs sanity against the built artifact, not only the source tree, and fails if a non user-facing file is shipped.
- README - Converted the badge block from unbalanced raw HTML to markdown image links, and reconciled the Available Modules list against the collection - it listed 64 of 66 modules, omitting ``purefb_admin`` and ``purefb_tz``.
- README - Fixed a typo in the CI badge organisation path, pointed the Release Notes section at the changelog shipped with the collection, dropped the redundant ``ansible-core`` requirement which is already declared by ``requires_ansible`` in ``meta/runtime.yml``, and changed the author line to a ``mailto:`` URL rather than a bare address used as a link target.
- README.rst - Replaced the repository-relative license link with a full URL, so that it resolves when the file is rendered outside the repository.
- collection - Added ``tests/config.yml`` declaring ``python_requires`` of ``>=3.9``, matching the Python version stated in the README. The 81 entries in ``tests/sanity/ignore-2.16.txt`` existed only to suppress the Python 2.7 compile and import tests one file at a time, so the ignore file is no longer needed and has been removed.
- collection - Added a ``build_ignore`` section to ``galaxy.yml`` so that the published tarball holds only user-facing content. ``settings.json``, ``.github``, ``pytest.ini``, ``.yamllint``, ``.gitignore``, ``.git-blame-ignore-revs``, ``changelogs/.plugin-cache.yaml``, ``README.rst`` and the empty ``.keep`` placeholders are no longer installed onto customer systems or pulled into execution environment images. ``changelogs/changelog.yaml`` still ships alongside the rendered ``CHANGELOG.rst``.
- collection - Added an ``.ansible-lint`` file setting the production profile, excluding only non-content directories, and adding ``no-log-password`` to the warning list.
- collection - Completed the rebranding from Pure Storage to Everpure. ``README.rst`` badge URLs, the ``.github/CONTRIBUTING.md`` title and issue link, and a copyright line in the ``purefb_tls_policy`` unit test all still referred to the previous identity.
- collection - Fixed the YAML indentation that ansible-lint reports on the built collection. These were suppressed while .yamllint shipped inside the tarball; it is development tooling and no longer ships, so the sequences in meta/runtime.yml and nine module documentation blocks are indented conformantly instead. A documentation formatting change only - every DOCUMENTATION, EXAMPLES and RETURN block parses to exactly the same content as before.
- collection - Removed the ``playbooks/roles`` symlink, stopped shipping ``tests/README.md`` which documents the excluded ``tests/unit`` directory, and narrowed the ``changelogs`` exclusion so that only ``changelog.yaml`` ships. A symlink is recorded in ``FILES.json`` as an ordinary directory with a null checksum, so nothing behind it is verified, and content scanners routinely skip symlinks.
- purefb_info - added ``smb_share_policies``, ``network_access_policies``, ``worm_data_policies``, ``tls_policies`` and ``s3_export_policies`` (with their rules or members) to the ``policies`` subset, ``object_store_virtual_hosts`` to ``buckets``, ``object_store_account_exports`` to ``accounts``, ``config.kmip`` to ``config``, and a new ``realms`` subset reporting ``realms`` and ``resource_accesses``; each is gated on the REST API version that introduced it.
- purefb_info - the ``default`` section reports the login ``banner`` so that purefb_banner changes can be read back and the previous text restored.
- purefb_remote_cred - new ``force`` option to re-send a secret under an unchanged access key ID, because the secret cannot be read back from the array.

Bugfixes
--------

- purefb_ad - an omitted ``join_ou`` no longer forces a change on every run; the OU-with-encryption guard now compares the lists.
- purefb_admin - omitted options leave the setting unchanged instead of resetting ``min_password`` to 1, crashing on ``lockout`` or reporting a change for ``max_login``.
- purefb_apiclient - hyphens are accepted in client names, the naming error names the API client, and a missing ``role`` at creation fails with a clear message.
- purefb_bucket - ``default_retention``, ``retention_mode``, ``object_lock_enabled`` and ``freeze_locked_objects`` are applied to existing buckets idempotently instead of being ignored; fixed a ``TypeError`` when updating a bucket that has a default retention without passing ``default_retention``. ``hard_limit=false`` is honoured on create and update.
- purefb_bucket - a bucket created without public-access options no longer gets ``block_public_access`` and ``block_new_public_policies`` forced to false or a world-readable ``default`` access policy rule; array defaults are kept and only requested settings are applied.
- purefb_bucket - eradication config changes were sent in the wrong request field; ``eradication_mode`` is now applied on create and update. ``eradication_delay`` and ``manual_eradication`` are read-only on a bucket at every REST API version (the array rejects them with "Invalid body parameter") and are inherited from the array-wide eradication config, so the module now fails early with a message naming where they are set instead of sending them. Destroy and eradicate failures fail the task instead of only warning. Documented that ``freeze_locked_objects=true`` cannot be combined with ``versioning=enabled`` and that ``default_retention`` is in days.
- purefb_bucket_access - ``effect=deny`` is rejected with an error because the FlashBlade REST API only supports ``allow`` and silently stored such rules as ``allow``. Changing an existing access or CORS rule in place is applied (the rule is replaced) instead of reporting no change, and deleting a CORS rule by name no longer has its existence check inverted. After creating or replacing a rule the module compares what the array stored with the request and fails, naming the stored rule, when the array kept something else (for example an action other than ``s3:GetObject``); previously such a rule was replaced again on every run.
- purefb_bucket_replica - modifying a replica link without ``target`` no longer raises ``TypeError``; creating a link without ``credential`` fails with a clear message.
- purefb_certgrp - fixed the ``status_code`` typo that broke every group creation, corrected the certificate add/remove calls to the ``certificate_groups_certificates`` endpoints, and made adding and removing certificates idempotent.
- purefb_certs - ``state=present`` on an existing certificate compares the requested attributes with the certificate on the array and only re-issues it when something differs or ``generate`` is true; ``key_size`` and ``key_algorithm`` are only sent together with ``generate_new_key``. ``key_algorithm`` is honoured at creation and the CSR file is written in one piece.
- purefb_connect - ``state=absent`` no longer crashes with ``TypeError`` (the SDK item iterator was indexed directly), so an array connection can be removed again.
- purefb_connect - fixed a ``NameError`` when setting a throttle on an unthrottled connection; the bandwidth range check now rejects out-of-range values.
- purefb_dns - updating a DNS configuration without ``source`` no longer sends an empty source reference.
- purefb_ds - ``state=present`` no longer crashes for an LDAP-based nfs service, ``enable=false`` is applied, ``force_bind_password`` without a ``bind_password`` is a no-op, ``bind_password`` is no longer mandatory alongside ``uri``, and ``state=test`` with ``nfs_server`` works.
- purefb_dsrole - ``state=absent`` no longer reports a change for a role with no group mapping.
- purefb_export - fixed a ``NameError`` when creating an export in check mode.
- purefb_fleet - adding a member fails with a clear message when only one of ``member_url`` and ``member_api`` is supplied instead of crashing.
- purefb_fs - ``hard_limit``, ``fastremove`` and ``snapshot`` are now applied when modifying an existing filesystem and are idempotent in both directions. ``smb``, ``http``, ``snapshot``, ``fastremove``, ``hard_limit`` and ``safeguard_acls`` no longer carry a default, so an omitted flag leaves the filesystem alone and an explicit ``false`` turns the setting off.
- purefb_fs - ``policy_state=absent`` is idempotent; detaching a policy that is not attached no longer fails on the second run. ``group_ownership`` and ``storage_class`` are applied at creation for filesystems without SMB, and eradicating a realm filesystem uses the qualified name.
- purefb_fs_replica - the module now uses the py-pure-client API (it failed on every run); ``state=absent`` with ``policy`` removes the policy and without ``policy`` deletes the link.
- purefb_hardware - fixed an ``UnboundLocalError`` when a connector is named without ``speed``, and omitted ``enabled`` or ``ports`` no longer report a change.
- purefb_info - Fixed a traceback when gathering the ``lags`` or ``subnets`` subsets, or ``all``, on a FlashBlade running in Layer 3 (BGP) mode, where the link aggregation groups endpoint returns an error and the data subnets have no LAG. The ``lag`` subset is now empty and such subnets report a null ``lag``.
- purefb_info - The ``drives`` and ``servers`` subsets now apply their API-version check when requested explicitly, not only as part of ``all``.
- purefb_info - ``gather_subset=fleet`` now returns the ``fleet`` section; previously the data was only produced as part of the ``policies`` subset.
- purefb_info - ``gather_subset=policies`` (and ``all``) no longer crashes with ``AttributeError`` when any bucket has a CORS policy; CORS policies report their rules.
- purefb_info - ``gather_subset=replication`` (and ``all``) no longer crashes with ``AttributeError`` when a filesystem replica link exists; the link status detail is read from the SDK's ``status_details`` attribute.
- purefb_info - ``network.<interface>.server`` and ``.subnet`` were always null; ``server`` now reports the attached server and a new ``attached_servers`` list is included. ``config.dns.<name>.source`` and the certificate ``certificate_type`` and ``intermediate_certificate`` values were always null due to misspelt attribute reads.
- purefb_info - ``share_policies`` actually contains SMB client policies; it is kept and also available as ``smb_client_policies``, with real SMB share policies under ``smb_share_policies``. ``multi_protocol.safeguard_acls`` is added alongside the misspelt ``safegaurd_acls`` key.
- purefb_keytabs - ``state=export`` reports no change; ``state=rotate`` fails cleanly when no AD account exists.
- purefb_lag - LAG creation no longer fails with an ``AttributeError`` whenever an existing LAG has ports, the in-use check tests every requested port, debug warnings are gone, and fully qualified ``CHx.FMy.ETHz`` port names are accepted.
- purefb_mgmt_policy - policies can be created with ``rules`` on REST API 2.19 to 2.25 (Purity//FB 4.6), where the rule list travels inline with the policy; the ``management`` policy is treated as built-in. Comparing the inline rule list no longer raises ``AttributeError`` for a rule whose role or scope reference is null.
- purefb_ntp - ``state=absent`` no longer reports a change when no NTP servers are configured.
- purefb_password_policy - ``min_character_groups`` and ``min_characters_per_group`` can be cleared with 0 and compare idempotently when disabled.
- purefb_policy - ``anonuid`` and ``anongid`` are integer options and are sent in the form the array accepts (strings up to REST 2.21, integers from 2.22); NFS export rules that set them no longer fail SDK validation or the array's "invalid value for anonuid" check. Object store access rule ``effect`` changes now reach the array (the SDK response model dropped ``effect`` from the PATCH body), and SMB share rules are addressed by principal within the policy with a PATCH body limited to the changed permissions; ``before_rule`` is rejected for share policies.
- purefb_policy - ``enabled`` now works for existing snapshot policies, a snapshot rule is only added when no existing rule matches, and a rule can be added to an empty policy. ``rename`` of a snapshot policy is verified after the PATCH and fails with a clear message where Purity//FB ignores it (REST 2.21 has no snapshot policy rename) instead of reporting a change every run.
- purefb_policy - ``policy_type=network`` failed on every array that is a member of a fleet with ``TypeError ... unexpected keyword argument 'context_names'``, including ``state=absent`` on a policy that does not exist. The network-access-policies endpoints take no fleet context, so the context defaulted for fleet members is no longer sent to them.
- purefb_policy - ``policy_type=network`` no longer raises ``AttributeError``; the module reports that network access policies cannot be created or deleted by the REST API and manages the rules of the existing policy, with interfaces compared order-insensitively and no fleet context sent.
- purefb_policy - ``state=copy`` copies snapshot, NFS, SMB and WORM policies (settings plus rules) to ``target`` and fails clearly where a copy is impossible instead of silently doing nothing; copied object store access rules keep their ``effect``.
- purefb_policy - a repeated ``state=present`` on an SMB share policy rule no longer crashes with ``TypeError``; permissions are compared as strings and ``""`` clears a permission idempotently. Share permissions are compared as the array stores them (a single effect over the ladder ``read`` < ``change`` < ``full_control``), so ``full_control=deny`` next to ``read``/``change`` ``allow`` is "not granted" and repeating the creation task is a no-op instead of turning the rule into deny-all. Deleting an SMB share principal rule that is already gone no longer raises ``IndexError``.
- purefb_policy - an update to an NFS export rule that left ``security`` unset sent the stored list back sorted, which can reorder a client's ``sec=`` preference. The stored order is now kept.
- purefb_policy - changing ``effect`` on an existing object store access policy rule is applied, and rule lists are compared order-insensitively.
- purefb_policy - rule fields not set in a task (``permission``, ``access``, ``atime``, ``fileid_32bit``, ``secure``, ``security``, ``smb_encryption``, ``effect``) keep their current value on update; defaults apply only when a rule is created. Renaming a WORM data policy no longer raises ``AttributeError``.
- purefb_policy - updating an NFS export rule that has ``anonuid`` or ``anongid`` failed with ``invalid value for anonuid``, even for a change that only touched another field such as ``atime``. The array accepts the ids as integers on create but only as strings on update, so an update now sends them as strings on every REST version.
- purefb_proxy, purefb_eula - check mode reports whether the setting would change.
- purefb_realm - ``without_default_access_list`` now defaults to ``true`` because Purity//FB requires it for realm creation; the failure message explains this when ``false`` is rejected. Fixed ``rename`` (``AttributeError`` on ``patch_realm``) and a crash in the QoS policy update for realms without a QoS policy; repeating a completed rename is a no-op.
- purefb_remote_cred - updating an existing credential is idempotent; the array is only patched when the access key ID differs or the new ``force`` option is set.
- purefb_resource_access - documentation lists the resource types a realm scope accepts (``subnets``, ``dns``) instead of the rejected ``policies/nfs`` example.
- purefb_s3_export_policy, purefb_s3acc_export - no longer send a fleet context on arrays that are not in a fleet ("Cannot find array in fleet"); rename is idempotent and rules are reconciled before a rename.
- purefb_s3acc - fixed the user-existence check on delete (stray apostrophe in the user glob).
- purefb_s3user - Added an explicit ``no_log=False`` to the ``access_key`` and ``enable_key`` parameters. Both are switches rather than credentials, but ``validate-modules`` treats any option whose name ends in ``key`` as a potential secret and raises ``no-log-needed`` unless it is marked. Their neighbours ``imported_key`` and ``key_name`` were already marked.
- purefb_s3user - an ``imported_key`` that already exists is no longer re-imported on every run; a rejected key import during user creation now fails the task with the array's error instead of silently deleting the new user and reporting ``changed``; invalid entries in ``policy`` no longer cause the following policy to be skipped; documentation no longer claims that ``pure:policy/full-access`` is added when ``policy`` is omitted.
- purefb_saml - the signing credential is sent as the signing credential on update; ``state=test`` reports no change.
- purefb_smtp - ``encryption=""`` is idempotent against an array with no encryption mode, and ``host=""`` now clears the relay host.
- purefb_snap - ``state=absent`` on an already destroyed snapshot no longer eradicates it unless ``eradicate=true`` is set. ``state=restore`` honours ``suffix``; without one the latest snapshot is used (fixed a crash when sorting snapshots). Fixed the inverted ``target`` connection check.
- purefb_snmp_agent, purefb_snmp_mgr - comparison is limited to the readable attributes of the selected SNMP version, so v2c and v3 updates are idempotent; the v2c community string and the v3 passphrases, which the array returns masked, are sent only when the version or another setting changes, and an omitted parameter keeps the current value. Passphrase length validation now works and a failed manager lookup fails cleanly instead of raising ``IndexError``.
- purefb_subnet - ``mtu`` and ``vlan`` no longer default on modification, so an omitted value leaves the subnet alone and a VLAN ID of 0 can be set; creation still defaults to 1500 and 0.
- purefb_syslog - ``state=test`` reports ``changed=false`` and returns only the results for the named server.
- purefb_target - changing a target address uses ``patch_targets`` instead of a removed 1.x SDK attribute.
- purefb_tls_policy - documented that ``appliance_certificate`` is required at creation and that ``client_certificates_required`` needs ``verify_client_certificate_trust``; creation without an appliance certificate fails early with a clear message.
- purefb_tz - fixed the ``HAS_PYTZ`` typo that hid a missing pytz.
- purefb_user - ``api=true`` no longer fails on a user without an existing token, the default ``timeout`` of ``0`` creates a non-expiring token instead of raising ``Invalid time format``, setting ``public_key`` at creation no longer calls a non-existent SDK method, ``state=absent`` on a missing local user is a no-op, ``clear_lock`` unlocks the user, and an omitted ``role`` no longer demotes an existing user to ``readonly``.
- purefb_user - ``public_key`` is documented and validated as an OpenSSH ``authorized_keys`` entry (``ssh-rsa``/``ssh-ed25519``, at most two, one per line); a PEM block is rejected with a clear message instead of the array's "At most 2 public keys can be uploaded for a user". Keys are compared by type and key material so re-applying the same key (or a different comment) is not a change, ``public_key=""`` only clears a key that is set, and a key the array masks as ``****`` is treated as present.
- purefb_userpolicy - ``state=show`` reports ``changed=false``; array-wide show without ``account`` and ``name`` no longer raises ``TypeError``.
- purefb_userquota, purefb_groupquota - uid/gid ``0`` is accepted; one of uid/uname (gid/gname) is now required instead of crashing when both are omitted.
- purefb_virtualhost - deleting a virtual host, or re-asserting an existing one, is no longer blocked once 10 hosts exist; hitting the cap on add is now an error.

v1.28.0
=======

Major Changes
-------------

- This collection has been renamed from purestorage.flashblade to everpure.flashblade. The purestorage.flashblade namespace is now a redirect-only deprecation stub.

Minor Changes
-------------

- purefb_ad - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_admin - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_bucket - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_bucket - The ``versioning`` parameter no longer defaults to ``absent``. Omitting it now leaves the bucket's versioning state unchanged. Set ``versioning`` to ``absent`` explicitly to suspend versioning on a bucket.
- purefb_bucket_access - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_bucket_replica - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_certs - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_connect - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_dns - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_ds - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_export - Add examples documenting the migration path from ``purefb_fs``'s ``export_policy``, ``share_policy`` and ``client_policy`` parameters
- purefb_export - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_export - Warn when creating an export at a (name, filesystem, server) address where an export of the other protocol already exists; almost always a mistyped ``type:`` parameter
- purefb_export - Warn when the filesystem still carries a legacy ``nfs.export_policy``, ``smb.share_policy`` or ``smb.client_policy``. For SMB the check fires even without the matching field
- purefb_fs - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_fs_replica - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_groupquota - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_hardware - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_info - Add ``password_policies`` section to the ``policies`` subset reporting the management password policy with durations in seconds (REST 2.16 and higher)
- purefb_info - Add per-filesystem ``file_system_exports`` list under ``filesystems`` and a top-level ``file_system_exports`` view; use instead of the legacy filesystem-level policy keys
- purefb_info - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_inventory - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_kmip - Add ``ca_certificate_group`` to reference a certificate group rather than a single certificate.
- purefb_kmip - Only the settings that differ are now sent on update, so an unrelated change no longer resends the CA reference.
- purefb_lifecycle - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_pingtrace - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_policy - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_ra - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_remote_cred - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_s3acc - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_s3user - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_saml - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_server - Add ``local_directory_service`` option to manage the server's local directory service reference on both create and update. Requires REST API 2.24 or higher.
- purefb_server - Switch REST API version gating from membership-style ``not in api_version`` to the standard ``LooseVersion`` comparison used by the rest of the collection.
- purefb_smtp - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_snap - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_syslog - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_user - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_userpolicy - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_userquota - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons
- purefb_virtualhost - Migrate REST API version checks to get_rest_api_version and LooseVersion comparisons

Deprecated Features
-------------------

- purefb_fs - Deprecate ``client_policy`` in favour of ``purefb_export`` (uses File System Exports); Purity//FB marks the underlying ``smb.client_policy`` as deprecated. Removed in 2.0.0
- purefb_fs - Deprecate ``export_policy`` in favour of ``purefb_export`` (uses File System Exports); Purity//FB marks the underlying ``nfs.export_policy`` as deprecated. Removed in 2.0.0
- purefb_fs - Deprecate ``share_policy`` in favour of ``purefb_export`` (uses File System Exports); Purity//FB marks the underlying ``smb.share_policy`` as deprecated. Removed in 2.0.0
- purefb_info - Deprecate ``filesystems`` subset keys ``nfs_rules``, ``export_policy``, ``smb_client_policy`` and ``smb_share_policy``; use ``file_system_exports`` instead. Removed in 2.0.0

Bugfixes
--------

- purefb_bucket - Fixed a task that does not specify ``versioning`` silently suspending versioning on an existing versioned bucket, because the parameter defaulted to ``absent``.
- purefb_bucket - Fixed idempotency error when ``versioning`` is ``absent`` and the bucket's versioning is already ``suspended``. The module sent the literal string ``absent`` to the API, which rejected it with "Versioning can only be set to 'enabled' or 'suspended'".
- purefb_info - Fix AttributeError in `policies` subset when any policy exists; `snapshot_policies` now reports snapshot policies with rules and `policies` reports a cross-type policy summary
- purefb_info - Fix AttributeError in `snapshot_policies` subset when a policy has no rules, or when an older py-pure-client returns a policy object without a `rules` attribute
- purefb_info - Fix default subset version gating so encryption, network access protocol and remote assist duration details are reported when the array supports them
- purefb_info - Fixed the ``arrays`` subset raising ``AttributeError`` on any array with a replication connection. The loop variable was rebound to the remote array name before every field was read from it, so ``gather_subset=all`` failed too.
- purefb_info - Fixed the ``policies`` subset raising ``AttributeError`` when a bucket access policy exists. ``BucketAccessPolicy`` has no ``description`` field, unlike the object store access policy reported alongside it; the key is now reported as null.
- purefb_info - The ``default`` subset's ``api_versions`` value now reports the highest REST API version the array supports, rather than the lowest. It previously read the first entry of the supported-versions list.
- purefb_kmip - Fixed ``state=test`` reporting ``changed=true`` for what is a read-only connectivity check.
- purefb_kmip - Fixed the 3000 character limit being applied to ``certificate``, a name, rather than to the certificate text it was documented against.
- purefb_kmip - Fixed the module being entirely non-functional. ``ca_certificate`` was sent as certificate text where the API expects a reference to a certificate already on the array, raising ``ValidationError``; ``certificate`` was sent as a field that does not exist on any REST version, so it was silently discarded; and reading it back raised ``AttributeError``, so updates could not complete at all.
- purefb_network - Added ``attached_server`` parameter so a network interface can be attached to a specific server rather than always landing on the default server, and so an existing interface can be moved to another server. Requires REST API 2.16 or higher.
- purefb_realm - Pass the new optional ``without_default_access_list`` parameter (default false) to post_realms, as required by py-pure-client when creating a realm
- purefb_server - Correct the ``directory_service`` update path which wrote to ``Server.dns`` instead of ``Server.directory_services`` and could silently replace the DNS list.
- purefb_server - Fix ``ValidationError`` on ``state=present`` when ``dns`` or ``directory_service`` is set — the update path passed the whole list to ``Reference(name=...)`` instead of iterating.
- purefb_server - Fixed ``dns`` and ``directory_service`` updates being applied to the array even when running in check mode.
- purefb_server - When a server existed and it should update the existing server. This will use the dns variable from the loop instead of referencing it.
- purefb_userpolicy - Fix adding a policy to an existing user silently failing to persist on REST 2.17+ while still reporting changed, due to a stray second non-context POST

New Modules
-----------

- everpure.flashblade.purefb_local_ds - Manage FlashBlade Local Directory Services
- everpure.flashblade.purefb_local_group - Manage FlashBlade Local Directory Service groups
- everpure.flashblade.purefb_local_user - Manage FlashBlade Local Directory Service users
- everpure.flashblade.purefb_mgmt_auth_policy - Manage FlashBlade Management Authentication Policies and their members
- everpure.flashblade.purefb_mgmt_policy - Manage FlashBlade Management Access Policies and their rules
- everpure.flashblade.purefb_mgmt_role - Manage FlashBlade custom management roles, permissions, and policy attachment
- everpure.flashblade.purefb_password_policy - Manage the FlashBlade management password policy
- everpure.flashblade.purefb_resource_access - Manage Resource Access on Everpure FlashBlades
- everpure.flashblade.purefb_tls_policy - Manage FlashBlade TLS policies

v1.27.0
=======

Minor Changes
-------------

- purefb - Add ``get_rest_api_version`` helper that returns the highest REST API version supported by the FlashBlade as a string, mirroring FlashArray's ``array.get_rest_version()``.
- purefb_fleet - Use the highest supported REST version when comparing against the minimum required API version.
- purefb_realm - Use the highest supported REST version when comparing against the minimum required API version.
- purefb_s3_export_policy - Use the highest supported REST version when comparing against the minimum required API version.
- purefb_s3acc_export - Use the highest supported REST version when comparing against the minimum required API version.

Bugfixes
--------

- purefb_dns - Fix incorrect ``DnsPatch`` class reference (now ``DnsPost``) that made the module wrongly report py-pure-client as not installed
- purefb_policy - Fixed a regression where updating an existing NFS export, SMB share, SMB client or network access policy rule failed with a pydantic ``str type expected`` error.
- purefb_policy - Rule updates now send the original string values to the API rather than the sorted values used only for the idempotency comparison.
- purefb_realm - Fixed incorrect documentation for ``purefb_realm`` module that was causing confusion about the required parameters. Changed from fa_url to fb_url
- purefb_remote_cred - Fix reference to non-existent ``ObjectStoreRemoteCredentialsPatch`` class in py-pure-client, which caused the module to incorrectly report that the SDK was not installed

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

- everpure.flashblade.purefb_s3_export_policy - Manage FlashBlade S3 Export Policies
- everpure.flashblade.purefb_s3acc_export - Manage FlashBlade Object Store Account exports

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

- everpure.flashblade.purefb_export - Manage filesystem exports on Everpure FlashBlade`
- everpure.flashblade.purefb_realm - Manage realms on Everpure FlashBlades

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

- everpure.flashblade.purefb_kmip - Manage FlashBlade KMIP server objects

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

- everpure.flashblade.purefb_bucket_access - Manage FlashBlade bucket access policies
- everpure.flashblade.purefb_fleet - Manage Fusion Fleet
- everpure.flashblade.purefb_server - Manage FlashBlade servers

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

- everpure.flashblade.purefb_saml - Manage FlashBlade SAML2 service and identity providers

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

- everpure.flashblade.purefb_hardware - Manage FlashBlade Hardware

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

- everpure.flashblade.purefb_pingtrace - Employ the internal FlashBlade ping and trace mechanisms

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

- everpure.flashblade.purefb_messages - List FlashBlade Alert Messages
- everpure.flashblade.purefb_tz - Configure Pure Storage FlashBlade timezone

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

- everpure.flashblade.purefb_groupquota - Manage filesystem group quotas
- everpure.flashblade.purefb_lag - Manage FlashBlade Link Aggregation Groups
- everpure.flashblade.purefb_userquota - Manage filesystem user quotas

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

- everpure.flashblade.purefb_ad - Manage FlashBlade Active Directory Account
- everpure.flashblade.purefb_apiclient - Manage FlashBlade API Clients
- everpure.flashblade.purefb_eula - Sign Pure Storage FlashBlade EULA
- everpure.flashblade.purefb_keytabs - Manage FlashBlade Kerberos Keytabs
- everpure.flashblade.purefb_timeout - Configure Pure Storage FlashBlade GUI idle timeout
- everpure.flashblade.purefb_userpolicy - Manage FlashBlade Object Store User Access Policies
- everpure.flashblade.purefb_virtualhost - Manage FlashBlade Object Store Virtual Hosts

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

- everpure.flashblade.purefb_banner - Configure Pure Storage FlashBlade GUI and SSH MOTD message
- everpure.flashblade.purefb_certgrp - Manage FlashBlade Certifcate Groups
- everpure.flashblade.purefb_certs - Manage FlashBlade SSL Certifcates
- everpure.flashblade.purefb_lifecycle - Manage FlashBlade object lifecycles
- everpure.flashblade.purefb_syslog - Configure Pure Storage FlashBlade syslog settings

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
