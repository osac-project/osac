=========================
ansible.hub Release Notes
=========================

.. contents:: Topics

v1.1.2
======

Bugfixes
--------

- group_roles - Add an opt-in organization parameter for qualifying AAP 2.5 team names while preserving legacy group behavior on supported Hub versions when organization is omitted (https://redhat.atlassian.net/browse/AAP-42096).
- group_roles - Reject Hub 4.11 and later, where team_roles is the supported role-assignment module (https://redhat.atlassian.net/browse/AAP-42096).
- team_roles - Add organization-aware team lookup and fail instead of selecting an arbitrary team when names are ambiguous (https://redhat.atlassian.net/browse/AAP-42096).

v1.1.1
======

Bugfixes
--------

- Prevent credentials marked ``no_log`` from appearing in unrecognized API error responses (https://redhat.atlassian.net/browse/AAP-90033).
- Surface the real API error message when an HTTP error response body does not match one of the previously recognized shapes, instead of a generic and unhelpful error (https://redhat.atlassian.net/browse/AAP-90033).
- ah_collection - Fix version extraction from collection tarball paths containing hyphens in directory components (https://github.com/ansible-collections/ansible_hub/issues/59).
- ah_ee_repository - Fix ``Update error: Failed to read response body: 'dict' object has no attribute 'read'`` when updating an existing execution environment repository, by sending updates as a partial update (PATCH) instead of a full replace (PUT) (https://redhat.atlassian.net/browse/AAP-90033).
- ah_ee_repository - Setting ``description`` to an empty string to clear it no longer fails with ``This field may not be blank.``; it is now sent to the API as a null value, which the server accepts (https://redhat.atlassian.net/browse/AAP-90033).

v1.1.0
======

Minor Changes
-------------

- The minimum supported ansible-core version is now 2.16. ansible-core 2.15 is no longer supported (https://forum.ansible.com/t/red-hat-ansible-automation-platform-is-ending-support-for-ansible-core-2-15-and-python-3-11/45574).

v1.0.7
======

Bugfixes
--------

- ah_token - Send authentication headers in check_deprecation() version-check requests so deprecation warnings and version gates work correctly when galaxy_ng strips version fields from unauthenticated API root responses (https://redhat.atlassian.net/browse/AAP-68691).

v1.0.6
======

Deprecated Features
-------------------

- ah_token - module now emits a deprecation warning when used with AAP 2.5 or 2.6 (Hub 4.10/4.11) and fails with a clear error on AAP 2.7+ (Hub 4.12+). This module is not compatible with Gateway-only authentication in AAP 2.7. Use the ansible.platform collection for token management instead.
- ah_user - module now emits a deprecation warning when used with AAP 2.5 or 2.6 (Hub 4.10/4.11) and fails with a clear error on AAP 2.7+ (Hub 4.12+). This module is not compatible with Gateway-only user management in AAP 2.7. Use the ansible.platform collection or AAP Gateway UI to manage users instead.

v1.0.5
======

Bugfixes
--------

- Fixed group_roles module failing with HTTP 400 when assigning namespace-scoped execution environment roles such as galaxy.execution_environment_collaborator.

v1.0.4
======

Major Changes
-------------

- Added ah_team and team_roles modules to manage assigning roles to teams in AAP 2.6. The team_roles module will only work in galaxy_ng 4.11+ and AAP 2.6+. The ah_team module will only work in galaxy_ng 4.11+ and added to support testing the team_roles module.

v1.0.3
======

Bugfixes
--------

- Fixed an issue where an incompatibility message was too restrictive.

v1.0.2
======

Bugfixes
--------

- Fixed an issue where an incompatibility message was indicating the incorrect version.

v1.0.1
======

Minor Changes
-------------

- added additional options for authentication to match controller credential type updates.

v1.0.0
======

Release Summary
---------------

This is the major release of the ``ansible.hub`` collection.
