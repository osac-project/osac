# Copyright: (c) 2026, Everpure Ansible Team <pure-ansible-team@everpuredata.com>
# GNU General Public License v3.0+ (see COPYING.GPLv3 or https://www.gnu.org/licenses/gpl-3.0.txt)

"""Unit tests for purefb_policy rule idempotency and context_names/fleet handling."""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

import sys
from unittest.mock import Mock, MagicMock

# Mock external dependencies before importing module
sys.modules["pypureclient"] = MagicMock()
sys.modules["pypureclient.flashblade"] = MagicMock()
sys.modules["urllib3"] = MagicMock()
sys.modules["distro"] = MagicMock()
sys.modules["grp"] = MagicMock()
sys.modules["fcntl"] = MagicMock()
sys.modules["pwd"] = MagicMock()
sys.modules["syslog"] = MagicMock()
mock_termios = MagicMock()
mock_termios.TCSAFLUSH = 2
sys.modules["termios"] = mock_termios
sys.modules["tty"] = MagicMock()

sys.modules["ansible_collections"] = MagicMock()
sys.modules["ansible_collections.purestorage"] = MagicMock()
sys.modules["ansible_collections.purestorage.flashblade"] = MagicMock()
sys.modules["ansible_collections.purestorage.flashblade.plugins"] = MagicMock()
sys.modules["ansible_collections.purestorage.flashblade.plugins.module_utils"] = (
    MagicMock()
)
sys.modules[
    "ansible_collections.purestorage.flashblade.plugins.module_utils.purefb"
] = MagicMock()
sys.modules[
    "ansible_collections.purestorage.flashblade.plugins.module_utils.common"
] = MagicMock()
sys.modules[
    "ansible_collections.purestorage.flashblade.plugins.module_utils.version"
] = MagicMock()
sys.modules[
    "ansible_collections.purestorage.flashblade.plugins.module_utils.time_utils"
] = MagicMock()

from plugins.modules.purefb_policy import (
    rename_smb_share_policy,
    update_nfs_policy,
    update_snap_policy,
)


def _existing_rule(access="root-squash"):
    """Build a mock SDK NFS export policy rule for client 10.0.10.42."""
    rule = Mock()
    rule.access = access
    rule.anongid = "100021"
    rule.anonuid = "20000021"
    rule.atime = True
    rule.client = "10.0.10.42"
    rule.fileid_32bit = False
    rule.permission = "rw"
    rule.secure = False
    rule.security = ["krb5", "krb5i", "krb5p", "sys"]
    rule.index = 1
    return rule


def _module(access):
    """Build a mock AnsibleModule whose desired rule has the given access."""
    module = Mock()
    module.check_mode = False
    module.params = {
        "name": "project96_nfs_policy",
        "client": "10.0.10.42",
        "permission": "rw",
        "access": access,
        "anonuid": "20000021",
        "anongid": "100021",
        "fileid_32bit": False,
        "atime": True,
        "secure": False,
        "security": ["krb5", "krb5i", "krb5p", "sys"],
        "before_rule": None,
        "context": "",
        "enabled": True,
    }
    module.fail_json = Mock(side_effect=SystemExit("fail_json called"))
    module.exit_json = Mock()
    return module


def _blade(existing_rule):
    """Build a mock blade returning the given existing rule."""
    blade = Mock()
    blade.get_versions.return_value.items = []  # no context API version

    rules_resp = Mock()
    rules_resp.status_code = 200
    rules_resp.total_item_count = 1
    rules_resp.items = [existing_rule]
    blade.get_nfs_export_policies_rules.return_value = rules_resp

    ok = Mock()
    ok.status_code = 200
    blade.patch_nfs_export_policies_rules.return_value = ok

    policy = Mock()
    policy.enabled = True
    policy_resp = Mock()
    policy_resp.items = [policy]
    blade.get_nfs_export_policies.return_value = policy_resp
    return blade


class TestUpdateNfsPolicyRule:
    """Regression tests for NFS export policy rule idempotency."""

    def test_access_change_triggers_patch(self):
        """Changing only access (root-squash -> no-squash) must PATCH the rule."""
        module = _module(access="no-squash")
        blade = _blade(_existing_rule(access="root-squash"))

        update_nfs_policy(module, blade)

        blade.patch_nfs_export_policies_rules.assert_called_once()
        # rule name targeted is "<policy>.<index>"
        call_kwargs = blade.patch_nfs_export_policies_rules.call_args[1]
        assert call_kwargs["names"] == ["project96_nfs_policy.1"]
        module.exit_json.assert_called_once_with(changed=True)

    def test_no_change_is_idempotent(self):
        """Identical desired state (incl. access) must not PATCH the rule."""
        module = _module(access="root-squash")
        blade = _blade(_existing_rule(access="root-squash"))

        update_nfs_policy(module, blade)

        blade.patch_nfs_export_policies_rules.assert_not_called()
        module.exit_json.assert_called_once_with(changed=False)

    def test_access_change_preserves_unspecified_anon(self):
        """An access-only change must not reset anonuid/anongid left unset."""
        import plugins.modules.purefb_policy as pol

        module = _module(access="no-squash")
        # Task changes access only and does not re-specify the anon mappings.
        module.params["anonuid"] = None
        module.params["anongid"] = None
        blade = _blade(_existing_rule(access="root-squash"))

        update_nfs_policy(module, blade)

        blade.patch_nfs_export_policies_rules.assert_called_once()
        # The patched rule must keep the existing anon ids, not null them out.
        rule_kwargs = pol.NfsExportPolicyRule.call_args[1]
        assert rule_kwargs["access"] == "no-squash"
        assert rule_kwargs["anonuid"] == "20000021"
        assert rule_kwargs["anongid"] == "100021"


def _snap_rule(every=86400000, keep_for=86400000, at=None, time_zone=None):
    """Build a mock SDK snapshot policy rule (values in milliseconds)."""
    rule = Mock()
    rule.every = every
    rule.keep_for = keep_for
    rule.at = at
    rule.time_zone = time_zone
    return rule


def _snap_module(keep_for=None, every=None, at=None, timezone=None):
    """Build a mock AnsibleModule for update_snap_policy.

    keep_for/every are in seconds (the task's units), matching the argspec.
    """
    module = Mock()
    module.check_mode = False
    module.params = {
        "name": "daily_snap_policy",
        "keep_for": keep_for,
        "every": every,
        "at": at,
        "timezone": timezone,
        "enabled": True,
        "context": "",
        "state": "present",
        "filesystem": None,
        "replica_link": None,
    }
    module.fail_json = Mock(side_effect=SystemExit("fail_json called"))
    module.exit_json = Mock()
    return module


def _snap_blade(existing_rule):
    """Build a mock blade whose policy has the given single schedule rule."""
    blade = Mock()
    blade.get_versions.return_value.items = []  # no context API version

    policy = Mock()
    policy.rules = [existing_rule]
    policies_resp = Mock()
    policies_resp.items = [policy]
    blade.get_policies.return_value = policies_resp

    ok = Mock()
    ok.status_code = 200
    blade.patch_policies.return_value = ok
    return blade


class TestUpdateSnapPolicy:
    """Regression tests for snapshot policy rule partial updates."""

    def test_retention_change_preserves_at_schedule(self):
        """Updating every/keep_for on an at-scheduled policy must preserve the
        existing ``at`` time and timezone, not drop them to an interval rule."""
        import plugins.modules.purefb_policy as pol

        # Existing rule keeps snapshots taken daily at 10:00 in a named tz.
        # The task changes retention/interval but does not re-specify at/timezone
        # (the guards require every and keep_for to be supplied together).
        module = _snap_module(keep_for=259200, every=86400)
        blade = _snap_blade(
            _snap_rule(
                every=86400000,
                keep_for=86400000,
                at=36000000,
                time_zone="America/New_York",
            )
        )

        update_snap_policy(module, blade)

        blade.patch_policies.assert_called_once()
        module.fail_json.assert_not_called()
        # The re-added rule must keep the at time and timezone, not drop them.
        rule_kwargs = pol.PolicyRule.call_args[1]
        assert rule_kwargs["keep_for"] == 259200 * 1000
        assert rule_kwargs["every"] == 86400 * 1000
        assert rule_kwargs["at"] == 36000000
        assert rule_kwargs["time_zone"] == "America/New_York"
        module.exit_json.assert_called_once_with(changed=True)

    def test_no_change_is_idempotent(self):
        """Identical desired schedule must not PATCH the policy."""
        module = _snap_module(keep_for=86400, every=86400)
        blade = _snap_blade(_snap_rule(every=86400000, keep_for=86400000))

        update_snap_policy(module, blade)

        blade.patch_policies.assert_not_called()
        module.exit_json.assert_called_once_with(changed=False)


def _ctx_module(context):
    module = Mock()
    module.check_mode = False
    module.params = {
        "name": "share_pol",
        "rename": "share_pol2",
        "context": context,
    }
    module.fail_json = Mock(side_effect=SystemExit("fail_json called"))
    module.exit_json = Mock()
    return module


def _ctx_blade():
    blade = Mock()
    # Array supports the context API version (2.17+)
    blade.get_versions.return_value.items = ["2.17"]
    ok = Mock()
    ok.status_code = 200
    blade.patch_smb_share_policies.return_value = ok
    return blade


class TestPolicyContextGating:
    """context_names must only be sent when a context is set."""

    def test_no_context_omits_context_names(self):
        """Standalone array (no context) must not send context_names."""
        module = _ctx_module(context="")
        blade = _ctx_blade()

        rename_smb_share_policy(module, blade)

        blade.patch_smb_share_policies.assert_called_once()
        assert "context_names" not in blade.patch_smb_share_policies.call_args[1]
        module.exit_json.assert_called_once_with(changed=True)

    def test_with_context_sends_context_names(self):
        """When a context is set, context_names must be sent."""
        module = _ctx_module(context="member-array")
        blade = _ctx_blade()

        rename_smb_share_policy(module, blade)

        blade.patch_smb_share_policies.assert_called_once()
        assert blade.patch_smb_share_policies.call_args[1]["context_names"] == [
            "member-array"
        ]
