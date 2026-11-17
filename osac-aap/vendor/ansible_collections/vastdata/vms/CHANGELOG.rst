======================================
VAST Data VMS Collection Release Notes
======================================

.. contents:: Topics

v2.2.0
======

Release Summary
---------------

Adds VMS TLS/SAML/network/maintenance modules, quotagroups, tenant metric labels, blobexpansions, list-only ``*_info`` companions, and idempotency/filter fixes.

Minor Changes
-------------

- Added ``health_info``, ``issues_info``, and ``issue_pre_install_validations``.
- Added ``quotagroups``, ``quotagroups_info``, and the quotagroup action modules ``quotagroup_assign_quotas``, ``quotagroup_refresh_user_quotas``, and ``quotagroup_reset_grace_period``.
- Added ``supportbundlesqueue_info`` and ``encryptiongroups_info`` companions for the list-only endpoints (see also removed writable modules).
- Added ``tenant_metric_label_values_bulk`` to read or replace the whole tenant metric-label value map (``GET``/``POST`` ``/tenants/{id}/metric_label_values/bulk/``). Omit ``values`` to read; supply a dict to replace. Requires VMS 5.5+.
- Added ``tenant_metric_labels``, ``tenant_metric_labels_info``, ``tenant_metric_label_values``, and ``tenant_metric_label_values_info`` (bulk replace/read remains ``tenant_metric_label_values_bulk``).
- Added ``tlscertificates``, ``tlscertificates_info``, ``tlscertificate_is_operation_healthy``, and ``tlscertificate_crl``.
- Added ``vms_login_banner``, ``vms_pwd_settings``, and ``vms_pwd_settings_info``.
- Added ``vms_set_certificate`` for installing an SSL certificate and private key on VMS (``PATCH /vms/{id}/set_certificate/``). Write-only action; every successful call reports ``changed=true``.
- Added ``vms_set_client_certificate`` and ``vms_remove_client_certificate`` to the published module set, with optional mTLS client authentication via ``VAST_CLIENT_CERT`` / ``VAST_CLIENT_KEY`` environment variables for recovery when a client certificate is installed on VMS.
- Added ``vms_set_ssl_ciphers`` and ``vms_reset_certificate``.
- Id-less keyed resources (``topics``, ``schemas``, ``tables``, ``blobexpansions``) declare ``KeyedShowCrudMixin`` via ``schema_overrides.keyed_show``; the generator emits the mixin base and class attributes instead of runtime ``__bases__`` patching in module customization blocks.
- New ``schema_show`` / ``schema_delete`` and ``table_show`` / ``table_delete`` modules expose the body-keyed show and delete sub-endpoints. ``schemas`` and ``tables`` remain present-only; use ``schema_delete`` / ``table_delete`` to remove them.
- New ``topic_show`` and ``topic_delete`` modules expose ``GET /topics/show/`` and body-keyed ``DELETE /topics/delete/`` (same pattern as blobexpansions).
- Published ``vms_set_ssl_port`` for changing the VMS HTTPS listener port (``PATCH /vms/{id}/set_ssl_port/``). Write-only action; every successful call reports ``changed=true``. Added integration tests covering check mode, out-of-band verification, and restore after port moves.
- Replaced legacy ``saml_config`` / ``saml_config_info`` with ``vms_saml_config`` and ``vms_saml_config_info`` for ``/vms/{id}/saml_config/`` (``idp_name`` query param). Supports GET, POST create/modify, PATCH ``remove_signed_certs``, and DELETE.
- The ``blobexpansions``, ``blobexpansion_show``, ``blobexpansion_show_info``, ``blobexpansion_add_columns``, ``blobexpansion_drop_columns`` and ``blobexpansion_delete`` modules (VMS 5.5+) are now part of the public collection, with an integration target covering the full lifecycle.
- The generator now emits a companion read-only ``*_info`` module for list-only endpoints that own an action sub-endpoint (``supportbundlesqueue``, ``encryptiongroups``, ``issues``), so their collections can finally be listed. The "list returns a top-level array" signal is derived on the resource lifecycle (``is_list_only_action``) instead of a by-id read capability.
- ``*_info`` modules for collection (list) endpoints now expose the union of Swagger ``in: query`` filters and the response-schema fields, so both Swagger-declared filters (e.g. ``name__icontains``, ``severity``, ``ip__icontains``) and response-schema fields the live API honors via django-filter (``id``, ``name``, ``guid``, ``hostname``, ``role``, ``acknowledged`` ...) remain available. Declared query params take precedence on name collisions.
- ``vms_reset_ssl_ciphers`` - publish the ``/vms/{id}/reset_ssl_ciphers/`` sub-endpoint module and add integration coverage (check mode, argument validation, set-then-reset with always cleanup).
- ``vms_toggle_maintenance_mode`` - publish the ``/vms/{id}/toggle_maintenance_mode/`` sub-endpoint module and add integration coverage (check mode, argument validation, optional live PATCH with cluster deactivation cleanup).

Breaking Changes / Porting Guide
--------------------------------

- A small number of ``*_info`` filter arguments were renamed to match the Swagger query parameter names actually honored by the API: ``delta_info`` ``current_generation`` becomes ``generation``; ``blobexpansion_show_info`` ``target_table_name`` becomes ``table_name``; ``nonlocal_user_info`` drops ``name`` (use ``username``). Singleton ``vms_login_banner_info`` / ``vms_pwd_settings_info`` lose their response-schema filter arguments (the endpoints do not accept them). Playbooks passing the old argument names must be updated.
- ``topics`` no longer supports ``state: absent``; the delete-only ``schema_name``, ``is_imports_table`` and ``tenant_id`` parameters were removed with it. Use ``topic_delete`` to remove a topic.

Removed Features (previously deprecated)
----------------------------------------

- Removed ``saml_config`` and ``saml_config_info``. Use ``vms_saml_config`` / ``vms_saml_config_info`` instead.
- Removed the non-functional ``supportbundlesqueue``, ``encryptiongroups``, and ``issues`` modules. These endpoints expose no create/update/delete or by-id read, so the generated writable module was dead code whose every ``state=present`` call failed. Use the new ``*_info`` modules to list them and the existing action sub-endpoints (``supportbundlesqueue_move`` etc.) to act on them.

v2.1.0
======

Release Summary
---------------

Adds OIDC, user quotas, webhooks, Kerberos, and expanded ``*_info`` coverage for remaining listable resources.

Minor Changes
-------------

- Added modules ``challengetokens_info``, ``permissions_info``, ``nonlocal_user_info``, and ``nonlocal_group_info``.
- Added modules ``kerberos``, ``kerberos_keytab``, and ``kerberos_info``.
- Added modules ``oidcs`` / ``oidcs_info``, ``userquotas`` / ``userquotas_info``, ``webhooks`` / ``webhooks_info``.
- Expanded ``*_info`` coverage for every remaining listable resource (views, viewpolicies, vippools, quotas, s3policies, tenants, groups, users, ldaps, dns, eventdefinitionconfigs, nativereplicationremotetargets, snapshots, globalsnapstreams, protectionpolicies, protectedpaths, activedirectory, administrator_role, localproviders, s3lifecyclerules, managers, qospolicies, apitokens, iamroles, realms, callhomeconfigs, vms, nis).

v2.0.0
======

Release Summary
---------------

Adds read-only ``*_info`` modules, multi-version VMS support, Apache-2.0 relicensing, and Red Hat certification hardening.

Major Changes
-------------

- Broadened supported Ansible to ``ansible-core >= 2.15`` (previously pinned to ``>=2.19.0,<2.20.0``).
- Relicensed the collection under Apache-2.0 (added a full ``LICENSE`` at the collection root and aligned all plugin file headers to Apache-2.0 + SPDX).

Minor Changes
-------------

- Added ``module_defaults`` action group ``vastdata.vms.all``, so the ``vms:`` connection can be declared once per play.
- Added a multi-version (5.4 + 5.5) generation target and a swagger-diff gate to the ``Makefile``; refreshed the architecture documentation.
- Added modules ``callhomeconfigs``, ``callhomeconfig_register_cluster``, ``callhomeconfig_send``, ``vms``, ``vms_configured_idps``, ``vms_set_max_api_tokens``, ``nonlocal_user_key``, ``nis``, and ``nis_set_posix_primary``.
- Added read-only ``*_info`` modules for every listable resource (e.g. ``views_info``, ``vippools_info``, ``tenants_info``, ``nics_info``).
- Adopted ``antsibull-changelog`` for changelog generation (``changelogs/`` config, seeded history, and a fragment workflow).
- Sub-endpoint update modules now use the shared ``values_equal`` comparison for more accurate idempotency (parity with the main resource diff engine).
- Unified the resource base-class connection/version setup and the module/lookup connection builders into a single shared implementation.

Bugfixes
--------

- Excluded build artifacts (tarballs) and test/cache directories from the built collection so the published artifact is clean.
- The REST client now raises a typed ``VastNotFoundError`` on HTTP 404 and wraps low-level transport errors as ``VastAPIError`` instead of relying on error-message string matching.

v1.3.1
======

Release Summary
---------------

Adds the ``protectedpath`` role for orchestrating VAST protected paths and their supporting native replication objects.

Minor Changes
-------------

- Added module ``protectedpath_streams`` to manage replication streams attached to a protected path (used by the ``protectedpath`` role).
- Added the ``vastdata.vms.protectedpath`` role to orchestrate VAST protected paths (VIP pools, replication peers, protection policies, protected paths, and replication/standby streams).

v1.3.0
======

Release Summary
---------------

Adds identity, access management, and QoS policy modules.

Minor Changes
-------------

- Added module ``activedirectory`` to manage Active Directory configuration.
- Added module ``administrator_role`` to manage administrator roles.
- Added module ``apitoken_revoke`` to revoke API tokens.
- Added module ``apitokens`` to manage API tokens.
- Added module ``iam_role_credentials`` to manage IAM role credentials.
- Added module ``iamrole_revoke_access_keys`` to revoke IAM role access keys.
- Added module ``iamroles`` to manage IAM roles.
- Added module ``localproviders`` to manage local providers.
- Added module ``manager_authorized_status`` to manage manager authorized status.
- Added module ``manager_password`` to manage manager passwords.
- Added module ``managers`` to manage managers.
- Added module ``qospolicies`` to manage QoS policies.
- Added module ``realms`` to manage realms.
- Added module ``s3lifecyclerules`` to manage S3 lifecycle rules.

v1.2.0
======

Release Summary
---------------

Adds data protection and snapshot management modules.

Minor Changes
-------------

- Added module ``globalsnapstreams`` to manage global snapshot streams.
- Added module ``nativereplicationremotetargets`` to manage native replication remote targets.
- Added module ``protectedpaths`` to manage protected paths.
- Added module ``protectionpolicies`` to manage protection policies.
- Added module ``snapshots`` to manage snapshots.
- Added module ``user_key`` to manage user keys.

v1.1.0
======

Release Summary
---------------

Adds non-local identity provider modules and replaces the vastpy SDK dependency with a self-contained REST client.

Minor Changes
-------------

- Added debug tracing and centralized timeout support for improved troubleshooting.
- Added module ``eventdefinitionconfigs`` to manage event definition configurations.
- Added module ``nonlocal_group`` to manage non-local groups.
- Added module ``nonlocal_user`` to manage non-local users.
- Replaced the ``vastpy`` SDK dependency with a self-contained REST client (``VastClient``).
- Stamped Galaxy version and git commit into the User-Agent header for request traceability.

v1.0.0
======

Release Summary
---------------

First public release of the VAST Data VMS Ansible Collection with 10 core modules for VAST storage management. Supports token (VAST 5.3+) or username/password authentication, full idempotency, check mode, and diff mode. Requires Python 3.9+ and ansible-core 2.14+.

Minor Changes
-------------

- Added module ``dns`` to manage DNS settings.
- Added module ``groups`` to manage user groups.
- Added module ``ldaps`` to configure LDAP authentication.
- Added module ``quotas`` to manage storage quotas.
- Added module ``s3policies`` to manage S3 bucket policies.
- Added module ``tenants`` to manage multi-tenancy configurations.
- Added module ``users`` to manage user accounts.
- Added module ``viewpolicies`` to manage view policies and configurations.
- Added module ``views`` to manage VAST views (file system exports).
- Added module ``vippools`` to manage VIP pools for network configuration.
