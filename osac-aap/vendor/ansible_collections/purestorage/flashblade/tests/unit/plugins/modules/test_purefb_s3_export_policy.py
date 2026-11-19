# Copyright: (c) 2026, Pure Storage Ansible Team <pure-ansible-team@purestorage.com>
# GNU General Public License v3.0+ (see COPYING.GPLv3 or https://www.gnu.org/licenses/gpl-3.0.txt)

"""Unit tests for purefb_s3_export_policy module."""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

import sys
import multiprocessing
import ctypes
from unittest.mock import Mock, patch, MagicMock

# Mock multiprocessing context for Windows
if sys.platform == "win32":
    original_get_context = multiprocessing.get_context

    def mock_get_context(method=None):
        if method == "fork":
            return original_get_context("spawn")
        return original_get_context(method)

    multiprocessing.get_context = mock_get_context

    # Mock ctypes LoadLibrary to prevent Windows errors
    original_load_library = ctypes.cdll.LoadLibrary

    def mock_load_library(name):
        if name is None or not isinstance(name, str):
            return MagicMock()
        try:
            return original_load_library(name)
        except (OSError, TypeError):
            return MagicMock()

    ctypes.cdll.LoadLibrary = mock_load_library

# Mock external dependencies before importing module
sys.modules["pypureclient"] = MagicMock()
sys.modules["pypureclient.flashblade"] = MagicMock()
sys.modules["urllib3"] = MagicMock()
sys.modules["distro"] = MagicMock()
# Mock Unix-specific modules for Windows compatibility
sys.modules["grp"] = MagicMock()
sys.modules["fcntl"] = MagicMock()
sys.modules["pwd"] = MagicMock()
sys.modules["syslog"] = MagicMock()
# Mock termios with required constants
mock_termios = MagicMock()
mock_termios.TCSAFLUSH = 2
sys.modules["termios"] = mock_termios
sys.modules["tty"] = MagicMock()

# Mock ansible_collections module structure
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

from plugins.modules.purefb_s3_export_policy import (
    main,
    _normalize_rule,
    _normalize_existing_rule,
    _get_policy,
    _get_policy_rules,
    _reconcile_rules,
)


class TestPurefbS3ExportPolicy:
    """Test cases for purefb_s3_export_policy module"""

    # ---------------- helper-function unit tests ----------------

    def test_normalize_rule_sorts_lists(self):
        """Test _normalize_rule sorts actions and resources for stable comparison"""
        out = _normalize_rule(
            {
                "name": "r1",
                "actions": ["b", "a"],
                "effect": "allow",
                "resources": ["z", "a"],
            }
        )
        assert out == {
            "name": "r1",
            "actions": ["a", "b"],
            "effect": "allow",
            "resources": ["a", "z"],
        }

    def test_normalize_rule_handles_none_lists(self):
        """Test _normalize_rule normalizes missing lists to empty lists"""
        out = _normalize_rule({"name": "r1", "effect": "allow"})
        assert out == {
            "name": "r1",
            "actions": [],
            "effect": "allow",
            "resources": [],
        }

    def test_normalize_existing_rule_uses_attr_access(self):
        """Test _normalize_existing_rule reads rule object attributes"""
        rule = Mock()
        rule.name = "r1"
        rule.actions = ["b", "a"]
        rule.effect = "allow"
        rule.resources = ["*"]

        out = _normalize_existing_rule(rule)
        assert out == {
            "name": "r1",
            "actions": ["a", "b"],
            "effect": "allow",
            "resources": ["*"],
        }

    def test_get_policy_returns_first_item(self):
        """Test _get_policy returns the first policy when present"""
        mock_module = Mock()
        mock_module.params = {"name": "test-policy", "context": ""}

        existing_policy = Mock()
        existing_policy.name = "test-policy"
        existing_policy.enabled = True

        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_response.items = [existing_policy]
        mock_blade.get_s3_export_policies.return_value = mock_response

        out = _get_policy(mock_module, mock_blade)

        assert out is not None
        mock_blade.get_s3_export_policies.assert_called_once_with(names=["test-policy"])

    def test_get_policy_passes_context_when_set(self):
        """Test _get_policy forwards context_names when context is set"""
        mock_module = Mock()
        mock_module.params = {"name": "test-policy", "context": "fleet1"}

        existing_policy = Mock()
        existing_policy.name = "test-policy"

        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_response.items = [existing_policy]
        mock_blade.get_s3_export_policies.return_value = mock_response

        _get_policy(mock_module, mock_blade)

        mock_blade.get_s3_export_policies.assert_called_once_with(
            names=["test-policy"], context_names=["fleet1"]
        )

    def test_get_policy_returns_none_on_error(self):
        """Test _get_policy returns None when the SDK reports an error"""
        mock_module = Mock()
        mock_module.params = {"name": "test-policy", "context": ""}

        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 400
        mock_response.items = []
        mock_blade.get_s3_export_policies.return_value = mock_response

        out = _get_policy(mock_module, mock_blade)

        assert out is None

    def test_get_policy_rules_returns_items(self):
        """Test _get_policy_rules returns all rule objects for the policy"""
        mock_module = Mock()
        mock_module.params = {"name": "test-policy", "context": ""}

        rule = Mock()
        rule.name = "r1"
        rule.actions = ["pure:S3Access"]
        rule.effect = "allow"
        rule.resources = ["*"]

        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_response.items = [rule]
        mock_blade.get_s3_export_policies_rules.return_value = mock_response

        out = _get_policy_rules(mock_module, mock_blade)

        assert len(out) == 1
        mock_blade.get_s3_export_policies_rules.assert_called_once_with(
            policy_names=["test-policy"]
        )

    # ---------------- _reconcile_rules tests ----------------

    @patch("plugins.modules.purefb_s3_export_policy.S3ExportPolicyRulePost")
    def test_reconcile_adds_missing_rules(self, mock_rule_post):
        """Test _reconcile_rules POSTs rules that are missing on the blade"""
        mock_module = Mock()
        mock_module.params = {"name": "test-policy", "context": ""}
        mock_module.check_mode = False
        mock_module.fail_json = Mock(side_effect=SystemExit)

        mock_blade = Mock()
        get_rules_response = Mock()
        get_rules_response.status_code = 200
        get_rules_response.items = []
        mock_blade.get_s3_export_policies_rules.return_value = get_rules_response

        post_response = Mock()
        post_response.status_code = 200
        mock_blade.post_s3_export_policies_rules.return_value = post_response

        desired = [
            {
                "name": "new_rule",
                "actions": ["pure:S3Access"],
                "effect": "allow",
                "resources": ["*"],
            }
        ]

        changed = _reconcile_rules(mock_module, mock_blade, desired)

        assert changed is True
        mock_blade.post_s3_export_policies_rules.assert_called_once()
        mock_blade.patch_s3_export_policies_rules.assert_not_called()
        mock_blade.delete_s3_export_policies_rules.assert_not_called()

    @patch("plugins.modules.purefb_s3_export_policy.S3ExportPolicyRulePost")
    def test_reconcile_modifies_divergent_rules(self, mock_rule_post):
        """Test _reconcile_rules PATCHes rules whose content has drifted"""
        mock_module = Mock()
        mock_module.params = {"name": "test-policy", "context": ""}
        mock_module.check_mode = False
        mock_module.fail_json = Mock(side_effect=SystemExit)

        existing_rule = Mock()
        existing_rule.name = "r1"
        existing_rule.actions = ["pure:S3Access"]
        existing_rule.effect = "allow"
        existing_rule.resources = ["old-*"]

        mock_blade = Mock()
        get_rules_response = Mock()
        get_rules_response.status_code = 200
        get_rules_response.items = [existing_rule]
        mock_blade.get_s3_export_policies_rules.return_value = get_rules_response

        patch_response = Mock()
        patch_response.status_code = 200
        mock_blade.patch_s3_export_policies_rules.return_value = patch_response

        desired = [
            {
                "name": "r1",
                "actions": ["pure:S3Access"],
                "effect": "allow",
                "resources": ["new-*"],
            }
        ]

        changed = _reconcile_rules(mock_module, mock_blade, desired)

        assert changed is True
        mock_blade.post_s3_export_policies_rules.assert_not_called()
        mock_blade.patch_s3_export_policies_rules.assert_called_once()
        mock_blade.delete_s3_export_policies_rules.assert_not_called()

    def test_reconcile_removes_orphan_rules(self):
        """Test _reconcile_rules DELETEs rules that are absent from desired"""
        mock_module = Mock()
        mock_module.params = {"name": "test-policy", "context": ""}
        mock_module.check_mode = False
        mock_module.fail_json = Mock(side_effect=SystemExit)

        keep_rule = Mock()
        keep_rule.name = "keep"
        keep_rule.actions = ["pure:S3Access"]
        keep_rule.effect = "allow"
        keep_rule.resources = ["*"]

        drop_rule = Mock()
        drop_rule.name = "drop"
        drop_rule.actions = ["pure:S3Access"]
        drop_rule.effect = "allow"
        drop_rule.resources = ["*"]

        mock_blade = Mock()
        get_rules_response = Mock()
        get_rules_response.status_code = 200
        get_rules_response.items = [keep_rule, drop_rule]
        mock_blade.get_s3_export_policies_rules.return_value = get_rules_response

        delete_response = Mock()
        delete_response.status_code = 200
        mock_blade.delete_s3_export_policies_rules.return_value = delete_response

        desired = [
            {
                "name": "keep",
                "actions": ["pure:S3Access"],
                "effect": "allow",
                "resources": ["*"],
            }
        ]

        changed = _reconcile_rules(mock_module, mock_blade, desired)

        assert changed is True
        mock_blade.delete_s3_export_policies_rules.assert_called_once()
        call_kwargs = mock_blade.delete_s3_export_policies_rules.call_args[1]
        assert call_kwargs["names"] == ["drop"]
        mock_blade.post_s3_export_policies_rules.assert_not_called()
        mock_blade.patch_s3_export_policies_rules.assert_not_called()

    def test_reconcile_idempotent_when_match(self):
        """Test _reconcile_rules makes no calls when desired matches current"""
        mock_module = Mock()
        mock_module.params = {"name": "test-policy", "context": ""}
        mock_module.check_mode = False
        mock_module.fail_json = Mock(side_effect=SystemExit)

        existing_rule = Mock()
        existing_rule.name = "r1"
        existing_rule.actions = ["pure:S3Access"]
        existing_rule.effect = "allow"
        existing_rule.resources = ["*"]

        mock_blade = Mock()
        get_rules_response = Mock()
        get_rules_response.status_code = 200
        get_rules_response.items = [existing_rule]
        mock_blade.get_s3_export_policies_rules.return_value = get_rules_response

        desired = [
            {
                "name": "r1",
                "actions": ["pure:S3Access"],
                "effect": "allow",
                "resources": ["*"],
            }
        ]

        changed = _reconcile_rules(mock_module, mock_blade, desired)

        assert changed is False
        mock_blade.post_s3_export_policies_rules.assert_not_called()
        mock_blade.patch_s3_export_policies_rules.assert_not_called()
        mock_blade.delete_s3_export_policies_rules.assert_not_called()

    def test_reconcile_check_mode_skips_writes(self):
        """Test _reconcile_rules reports changed but skips writes in check mode"""
        mock_module = Mock()
        mock_module.params = {"name": "test-policy", "context": ""}
        mock_module.check_mode = True
        mock_module.fail_json = Mock(side_effect=SystemExit)

        mock_blade = Mock()
        get_rules_response = Mock()
        get_rules_response.status_code = 200
        get_rules_response.items = []
        mock_blade.get_s3_export_policies_rules.return_value = get_rules_response

        desired = [
            {
                "name": "new_rule",
                "actions": ["pure:S3Access"],
                "effect": "allow",
                "resources": ["*"],
            }
        ]

        changed = _reconcile_rules(mock_module, mock_blade, desired)

        assert changed is True
        mock_blade.post_s3_export_policies_rules.assert_not_called()

    # ---------------- main() flows ----------------

    @patch("plugins.modules.purefb_s3_export_policy.LooseVersion")
    @patch("plugins.modules.purefb_s3_export_policy.S3ExportPolicyPost")
    @patch("plugins.modules.purefb_s3_export_policy.get_system")
    @patch("plugins.modules.purefb_s3_export_policy.AnsibleModule")
    @patch("plugins.modules.purefb_s3_export_policy.HAS_PYPURECLIENT", True)
    def test_main_creates_policy(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_policy_post,
        mock_loose_version,
    ):
        """Test creating a new S3 export policy"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-policy",
            "state": "present",
            "enabled": True,
            "rename": None,
            "rules": None,
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        # Policy does not exist
        get_response = Mock()
        get_response.status_code = 200
        get_response.items = []
        mock_blade.get_s3_export_policies.return_value = get_response

        # Mock successful policy creation
        post_response = Mock()
        post_response.status_code = 200
        mock_blade.post_s3_export_policies.return_value = post_response

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify post_s3_export_policies was called
        mock_blade.post_s3_export_policies.assert_called_once()
        call_kwargs = mock_blade.post_s3_export_policies.call_args[1]
        assert call_kwargs["names"] == ["test-policy"]

        # Verify exit_json was called with changed=True
        mock_module.exit_json.assert_called_once()
        assert mock_module.exit_json.call_args[1]["changed"] is True

    @patch("plugins.modules.purefb_s3_export_policy.LooseVersion")
    @patch("plugins.modules.purefb_s3_export_policy.S3ExportPolicyPost")
    @patch("plugins.modules.purefb_s3_export_policy.get_system")
    @patch("plugins.modules.purefb_s3_export_policy.AnsibleModule")
    @patch("plugins.modules.purefb_s3_export_policy.HAS_PYPURECLIENT", True)
    def test_main_create_check_mode_skips_post(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_policy_post,
        mock_loose_version,
    ):
        """Test create reports changed but skips POST in check mode"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-policy",
            "state": "present",
            "enabled": True,
            "rename": None,
            "rules": None,
            "context": "",
        }
        mock_module.check_mode = True
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        # Policy does not exist
        get_response = Mock()
        get_response.status_code = 200
        get_response.items = []
        mock_blade.get_s3_export_policies.return_value = get_response

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify no POST was made
        mock_blade.post_s3_export_policies.assert_not_called()

        # Verify exit_json was called with changed=True
        assert mock_module.exit_json.call_args[1]["changed"] is True

    @patch("plugins.modules.purefb_s3_export_policy.LooseVersion")
    @patch("plugins.modules.purefb_s3_export_policy.S3ExportPolicyPatch")
    @patch("plugins.modules.purefb_s3_export_policy.get_system")
    @patch("plugins.modules.purefb_s3_export_policy.AnsibleModule")
    @patch("plugins.modules.purefb_s3_export_policy.HAS_PYPURECLIENT", True)
    def test_main_updates_enabled_flag(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_patch_cls,
        mock_loose_version,
    ):
        """Test updating the enabled flag on an existing policy"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-policy",
            "state": "present",
            "enabled": False,
            "rename": None,
            "rules": None,
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        # Existing policy with enabled=True
        existing_policy = Mock()
        existing_policy.name = "test-policy"
        existing_policy.enabled = True

        get_response = Mock()
        get_response.status_code = 200
        get_response.items = [existing_policy]
        mock_blade.get_s3_export_policies.return_value = get_response

        # Mock successful patch
        patch_response = Mock()
        patch_response.status_code = 200
        mock_blade.patch_s3_export_policies.return_value = patch_response

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify patch_s3_export_policies was called
        mock_blade.patch_s3_export_policies.assert_called_once()

        # Verify exit_json was called with changed=True
        assert mock_module.exit_json.call_args[1]["changed"] is True

    @patch("plugins.modules.purefb_s3_export_policy.LooseVersion")
    @patch("plugins.modules.purefb_s3_export_policy.get_system")
    @patch("plugins.modules.purefb_s3_export_policy.AnsibleModule")
    @patch("plugins.modules.purefb_s3_export_policy.HAS_PYPURECLIENT", True)
    def test_main_update_idempotent_when_enabled_matches(
        self, mock_ansible_module, mock_get_system, mock_loose_version
    ):
        """Test update is a no-op when desired enabled already matches"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-policy",
            "state": "present",
            "enabled": True,
            "rename": None,
            "rules": None,
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        # Existing policy already has enabled=True
        existing_policy = Mock()
        existing_policy.name = "test-policy"
        existing_policy.enabled = True

        get_response = Mock()
        get_response.status_code = 200
        get_response.items = [existing_policy]
        mock_blade.get_s3_export_policies.return_value = get_response

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify no patch was made
        mock_blade.patch_s3_export_policies.assert_not_called()

        # Verify exit_json was called with changed=False
        assert mock_module.exit_json.call_args[1]["changed"] is False

    @patch("plugins.modules.purefb_s3_export_policy.LooseVersion")
    @patch("plugins.modules.purefb_s3_export_policy.S3ExportPolicyPatch")
    @patch("plugins.modules.purefb_s3_export_policy.get_system")
    @patch("plugins.modules.purefb_s3_export_policy.AnsibleModule")
    @patch("plugins.modules.purefb_s3_export_policy.HAS_PYPURECLIENT", True)
    def test_main_renames_policy(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_patch_cls,
        mock_loose_version,
    ):
        """Test renaming an existing policy"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-policy",
            "state": "present",
            "enabled": None,
            "rename": "renamed-policy",
            "rules": None,
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        # Existing policy
        existing_policy = Mock()
        existing_policy.name = "test-policy"
        existing_policy.enabled = True

        get_response = Mock()
        get_response.status_code = 200
        get_response.items = [existing_policy]
        mock_blade.get_s3_export_policies.return_value = get_response

        # Mock successful patch
        patch_response = Mock()
        patch_response.status_code = 200
        mock_blade.patch_s3_export_policies.return_value = patch_response

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify patch_s3_export_policies was called
        mock_blade.patch_s3_export_policies.assert_called_once()

        # Verify exit_json was called with changed=True
        assert mock_module.exit_json.call_args[1]["changed"] is True

    @patch("plugins.modules.purefb_s3_export_policy.LooseVersion")
    @patch("plugins.modules.purefb_s3_export_policy.get_system")
    @patch("plugins.modules.purefb_s3_export_policy.AnsibleModule")
    @patch("plugins.modules.purefb_s3_export_policy.HAS_PYPURECLIENT", True)
    def test_main_deletes_policy(
        self, mock_ansible_module, mock_get_system, mock_loose_version
    ):
        """Test deleting an existing policy"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-policy",
            "state": "absent",
            "enabled": None,
            "rename": None,
            "rules": None,
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        # Existing policy
        existing_policy = Mock()
        existing_policy.name = "test-policy"
        existing_policy.enabled = True

        get_response = Mock()
        get_response.status_code = 200
        get_response.items = [existing_policy]
        mock_blade.get_s3_export_policies.return_value = get_response

        # Mock successful delete
        delete_response = Mock()
        delete_response.status_code = 200
        mock_blade.delete_s3_export_policies.return_value = delete_response

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify delete_s3_export_policies was called
        mock_blade.delete_s3_export_policies.assert_called_once()
        call_kwargs = mock_blade.delete_s3_export_policies.call_args[1]
        assert call_kwargs["names"] == ["test-policy"]

        # Verify exit_json was called with changed=True
        assert mock_module.exit_json.call_args[1]["changed"] is True

    @patch("plugins.modules.purefb_s3_export_policy.LooseVersion")
    @patch("plugins.modules.purefb_s3_export_policy.get_system")
    @patch("plugins.modules.purefb_s3_export_policy.AnsibleModule")
    @patch("plugins.modules.purefb_s3_export_policy.HAS_PYPURECLIENT", True)
    def test_main_delete_idempotent_when_absent(
        self, mock_ansible_module, mock_get_system, mock_loose_version
    ):
        """Test delete is a no-op when the policy does not exist"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-policy",
            "state": "absent",
            "enabled": None,
            "rename": None,
            "rules": None,
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        # Policy does not exist
        get_response = Mock()
        get_response.status_code = 200
        get_response.items = []
        mock_blade.get_s3_export_policies.return_value = get_response

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify no delete was made
        mock_blade.delete_s3_export_policies.assert_not_called()

        # Verify exit_json was called with changed=False
        assert mock_module.exit_json.call_args[1]["changed"] is False

    @patch("plugins.modules.purefb_s3_export_policy.LooseVersion")
    @patch("plugins.modules.purefb_s3_export_policy.get_system")
    @patch("plugins.modules.purefb_s3_export_policy.AnsibleModule")
    @patch("plugins.modules.purefb_s3_export_policy.HAS_PYPURECLIENT", True)
    def test_main_fails_when_api_version_too_old(
        self, mock_ansible_module, mock_get_system, mock_loose_version
    ):
        """Test failure when API version is too old"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=True)
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-policy",
            "state": "present",
            "enabled": None,
            "rename": None,
            "rules": None,
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade with too-old API versions
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.10", "2.15", "2.19"]
        mock_get_system.return_value = mock_blade

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        assert (
            "FlashBlade REST version not supported"
            in mock_module.fail_json.call_args[1]["msg"]
        )

    @patch("plugins.modules.purefb_s3_export_policy.LooseVersion")
    @patch("plugins.modules.purefb_s3_export_policy.S3ExportPolicyPost")
    @patch("plugins.modules.purefb_s3_export_policy.get_system")
    @patch("plugins.modules.purefb_s3_export_policy.AnsibleModule")
    @patch("plugins.modules.purefb_s3_export_policy.HAS_PYPURECLIENT", True)
    def test_main_create_failure_calls_fail_json(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_post_cls,
        mock_loose_version,
    ):
        """Test SDK error on create is surfaced via fail_json"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-policy",
            "state": "present",
            "enabled": True,
            "rename": None,
            "rules": None,
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        # Policy does not exist
        get_response = Mock()
        get_response.status_code = 200
        get_response.items = []
        mock_blade.get_s3_export_policies.return_value = get_response

        # Mock failed policy creation
        post_response = Mock()
        post_response.status_code = 400
        post_err = Mock()
        post_err.message = "nope"
        post_response.errors = [post_err]
        mock_blade.post_s3_export_policies.return_value = post_response

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        assert (
            "Failed to create S3 export policy"
            in mock_module.fail_json.call_args[1]["msg"]
        )
