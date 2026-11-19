# Copyright: (c) 2026, Pure Storage Ansible Team <pure-ansible-team@purestorage.com>
# GNU General Public License v3.0+ (see COPYING.GPLv3 or https://www.gnu.org/licenses/gpl-3.0.txt)

"""Unit tests for purefb_realm module."""

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

from plugins.modules.purefb_realm import (
    main,
    get_realm,
    get_pending_realm,
    get_policy,
)


class TestPurefbRealm:
    """Test cases for purefb_realm module"""

    @patch("plugins.modules.purefb_realm.get_pending_realm")
    @patch("plugins.modules.purefb_realm.get_realm")
    @patch("plugins.modules.purefb_realm.LooseVersion")
    @patch("plugins.modules.purefb_realm.get_system")
    @patch("plugins.modules.purefb_realm.AnsibleModule")
    @patch("plugins.modules.purefb_realm.HAS_PURESTORAGE", True)
    def test_main_create_realm(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_loose_version,
        mock_get_realm,
        mock_get_pending_realm,
    ):
        """Test creating a new realm"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-realm",
            "state": "present",
            "qos_policy": None,
            "eradicate": False,
            "rename": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.19"]
        mock_get_system.return_value = mock_blade

        # Mock API version check
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)

        # Realm doesn't exist
        mock_get_realm.return_value = None
        mock_get_pending_realm.return_value = None

        # Mock successful realm creation
        mock_post_response = Mock()
        mock_post_response.status_code = 200
        mock_blade.post_realms.return_value = mock_post_response

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify post_realms was called
        mock_blade.post_realms.assert_called_once_with(names=["test-realm"])

        # Verify exit_json was called with changed=True
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_realm.get_policy")
    @patch("plugins.modules.purefb_realm.get_pending_realm")
    @patch("plugins.modules.purefb_realm.get_realm")
    @patch("plugins.modules.purefb_realm.LooseVersion")
    @patch("plugins.modules.purefb_realm.get_system")
    @patch("plugins.modules.purefb_realm.AnsibleModule")
    @patch("plugins.modules.purefb_realm.HAS_PURESTORAGE", True)
    def test_main_create_realm_with_qos_policy(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_loose_version,
        mock_get_realm,
        mock_get_pending_realm,
        mock_get_policy,
    ):
        """Test creating a new realm with QoS policy"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-realm",
            "state": "present",
            "qos_policy": "test-qos-policy",
            "eradicate": False,
            "rename": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.19"]
        mock_get_system.return_value = mock_blade

        # Mock API version check
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)

        # Realm doesn't exist
        mock_get_realm.return_value = None
        mock_get_pending_realm.return_value = None

        # QoS policy exists
        mock_get_policy.return_value = True

        # Mock successful realm creation
        mock_post_response = Mock()
        mock_post_response.status_code = 200
        mock_blade.post_realms.return_value = mock_post_response
        mock_blade.post_qos_policies_members.return_value = mock_post_response

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify post_realms was called
        mock_blade.post_realms.assert_called_once_with(names=["test-realm"])

        # Verify QoS policy was assigned
        mock_blade.post_qos_policies_members.assert_called_once_with(
            policy_names=["test-qos-policy"], member_names=["test-realm"]
        )

        # Verify exit_json was called with changed=True
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_realm.get_pending_realm")
    @patch("plugins.modules.purefb_realm.get_realm")
    @patch("plugins.modules.purefb_realm.LooseVersion")
    @patch("plugins.modules.purefb_realm.get_system")
    @patch("plugins.modules.purefb_realm.AnsibleModule")
    @patch("plugins.modules.purefb_realm.HAS_PURESTORAGE", True)
    def test_main_delete_realm(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_loose_version,
        mock_get_realm,
        mock_get_pending_realm,
    ):
        """Test deleting an existing realm"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-realm",
            "state": "absent",
            "qos_policy": None,
            "eradicate": False,
            "rename": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.19"]
        mock_get_system.return_value = mock_blade

        # Mock API version check
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)

        # Realm exists
        mock_get_realm.return_value = True
        mock_get_pending_realm.return_value = None

        # Mock successful realm deletion
        mock_patch_response = Mock()
        mock_patch_response.status_code = 200
        mock_blade.patch_realms.return_value = mock_patch_response

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify patch_realms was called
        mock_blade.patch_realms.assert_called_once()

        # Verify exit_json was called with changed=True
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_realm.get_pending_realm")
    @patch("plugins.modules.purefb_realm.get_realm")
    @patch("plugins.modules.purefb_realm.LooseVersion")
    @patch("plugins.modules.purefb_realm.get_system")
    @patch("plugins.modules.purefb_realm.AnsibleModule")
    @patch("plugins.modules.purefb_realm.HAS_PURESTORAGE", True)
    def test_main_eradicate_realm(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_loose_version,
        mock_get_realm,
        mock_get_pending_realm,
    ):
        """Test deleting and eradicating a realm"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-realm",
            "state": "absent",
            "qos_policy": None,
            "eradicate": True,
            "rename": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.19"]
        mock_get_system.return_value = mock_blade

        # Mock API version check
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)

        # Realm exists
        mock_get_realm.return_value = True
        mock_get_pending_realm.return_value = None

        # Mock successful operations
        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.patch_realms.return_value = mock_response
        mock_blade.delete_realms.return_value = mock_response

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify patch_realms was called (delete)
        mock_blade.patch_realms.assert_called_once()

        # Verify delete_realms was called (eradicate)
        mock_blade.delete_realms.assert_called_once_with(names=["test-realm"])

        # Verify exit_json was called with changed=True
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_realm.get_pending_realm")
    @patch("plugins.modules.purefb_realm.get_realm")
    @patch("plugins.modules.purefb_realm.LooseVersion")
    @patch("plugins.modules.purefb_realm.get_system")
    @patch("plugins.modules.purefb_realm.AnsibleModule")
    @patch("plugins.modules.purefb_realm.HAS_PURESTORAGE", True)
    def test_main_recover_realm(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_loose_version,
        mock_get_realm,
        mock_get_pending_realm,
    ):
        """Test recovering a deleted realm"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-realm",
            "state": "present",
            "qos_policy": None,
            "eradicate": False,
            "rename": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.19"]
        mock_get_system.return_value = mock_blade

        # Mock API version check
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)

        # Realm is in pending deletion
        mock_get_realm.return_value = None
        mock_get_pending_realm.return_value = True

        # Mock successful realm recovery
        mock_patch_response = Mock()
        mock_patch_response.status_code = 200
        mock_blade.patch_realms.return_value = mock_patch_response

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify patch_realms was called
        mock_blade.patch_realms.assert_called_once()

        # Verify exit_json was called with changed=True
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_realm.RealmPatch")
    @patch("plugins.modules.purefb_realm.get_pending_realm")
    @patch("plugins.modules.purefb_realm.get_realm")
    @patch("plugins.modules.purefb_realm.LooseVersion")
    @patch("plugins.modules.purefb_realm.get_system")
    @patch("plugins.modules.purefb_realm.AnsibleModule")
    @patch("plugins.modules.purefb_realm.HAS_PURESTORAGE", True)
    def test_main_rename_realm(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_loose_version,
        mock_get_realm,
        mock_get_pending_realm,
        mock_realm_patch,
    ):
        """Test renaming an existing realm"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-realm",
            "state": "present",
            "qos_policy": None,
            "eradicate": False,
            "rename": "new-realm-name",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.19"]
        mock_get_system.return_value = mock_blade

        # Mock API version check
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)

        # Realm exists
        mock_get_realm.return_value = True
        mock_get_pending_realm.return_value = None

        # Mock successful rename
        mock_patch_response = Mock()
        mock_patch_response.status_code = 200
        mock_blade.patch_realm.return_value = mock_patch_response

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify patch_realm was called
        mock_blade.patch_realm.assert_called_once()

        # Verify exit_json was called with changed=True
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_realm.get_pending_realm")
    @patch("plugins.modules.purefb_realm.get_realm")
    @patch("plugins.modules.purefb_realm.LooseVersion")
    @patch("plugins.modules.purefb_realm.get_system")
    @patch("plugins.modules.purefb_realm.AnsibleModule")
    @patch("plugins.modules.purefb_realm.HAS_PURESTORAGE", True)
    def test_main_update_qos_policy(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_loose_version,
        mock_get_realm,
        mock_get_pending_realm,
    ):
        """Test updating QoS policy for an existing realm"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-realm",
            "state": "present",
            "qos_policy": "new-qos-policy",
            "eradicate": False,
            "rename": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.19"]
        mock_get_system.return_value = mock_blade

        # Mock API version check
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)

        # Realm exists
        mock_get_realm.return_value = True
        mock_get_pending_realm.return_value = None

        # Mock current QoS policy
        mock_policy_member = Mock()
        mock_policy_member.policy = Mock()
        mock_policy_member.policy.name = "old-qos-policy"
        mock_blade.get_qos_policies_members.return_value.items = [mock_policy_member]

        # Mock successful QoS policy update
        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.delete_qos_policies_members.return_value = mock_response
        mock_blade.post_qos_policies_members.return_value = mock_response

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify old policy was removed
        mock_blade.delete_qos_policies_members.assert_called_once()

        # Verify new policy was added
        mock_blade.post_qos_policies_members.assert_called_once()

        # Verify exit_json was called with changed=True
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_realm.get_pending_realm")
    @patch("plugins.modules.purefb_realm.get_realm")
    @patch("plugins.modules.purefb_realm.LooseVersion")
    @patch("plugins.modules.purefb_realm.get_system")
    @patch("plugins.modules.purefb_realm.AnsibleModule")
    @patch("plugins.modules.purefb_realm.HAS_PURESTORAGE", True)
    def test_main_create_realm_check_mode(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_loose_version,
        mock_get_realm,
        mock_get_pending_realm,
    ):
        """Test creating a realm in check mode"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-realm",
            "state": "present",
            "qos_policy": None,
            "eradicate": False,
            "rename": None,
        }
        mock_module.check_mode = True
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.19"]
        mock_get_system.return_value = mock_blade

        # Mock API version check
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)

        # Realm doesn't exist
        mock_get_realm.return_value = None
        mock_get_pending_realm.return_value = None

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify post_realms was NOT called (check mode)
        mock_blade.post_realms.assert_not_called()

        # Verify exit_json was called with changed=True
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_realm.get_pending_realm")
    @patch("plugins.modules.purefb_realm.get_realm")
    @patch("plugins.modules.purefb_realm.LooseVersion")
    @patch("plugins.modules.purefb_realm.get_system")
    @patch("plugins.modules.purefb_realm.AnsibleModule")
    @patch("plugins.modules.purefb_realm.HAS_PURESTORAGE", True)
    def test_main_create_realm_fails(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_loose_version,
        mock_get_realm,
        mock_get_pending_realm,
    ):
        """Test error handling when realm creation fails"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-realm",
            "state": "present",
            "qos_policy": None,
            "eradicate": False,
            "rename": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.19"]
        mock_get_system.return_value = mock_blade

        # Mock API version check
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)

        # Realm doesn't exist
        mock_get_realm.return_value = None
        mock_get_pending_realm.return_value = None

        # Mock failed realm creation
        mock_post_response = Mock()
        mock_post_response.status_code = 400
        mock_error = Mock()
        mock_error.message = "Realm creation failed"
        mock_post_response.errors = [mock_error]
        mock_blade.post_realms.return_value = mock_post_response

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "Creation of realm" in call_args["msg"]

    @patch("plugins.modules.purefb_realm.get_policy")
    @patch("plugins.modules.purefb_realm.get_pending_realm")
    @patch("plugins.modules.purefb_realm.get_realm")
    @patch("plugins.modules.purefb_realm.LooseVersion")
    @patch("plugins.modules.purefb_realm.get_system")
    @patch("plugins.modules.purefb_realm.AnsibleModule")
    @patch("plugins.modules.purefb_realm.HAS_PURESTORAGE", True)
    def test_main_qos_policy_assignment_fails(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_loose_version,
        mock_get_realm,
        mock_get_pending_realm,
        mock_get_policy,
    ):
        """Test error handling when QoS policy assignment fails"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-realm",
            "state": "present",
            "qos_policy": "test-qos-policy",
            "eradicate": False,
            "rename": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.19"]
        mock_get_system.return_value = mock_blade

        # Mock API version check
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)

        # Realm doesn't exist
        mock_get_realm.return_value = None
        mock_get_pending_realm.return_value = None

        # QoS policy exists
        mock_get_policy.return_value = True

        # Mock successful realm creation but failed QoS assignment
        mock_post_response = Mock()
        mock_post_response.status_code = 200
        mock_blade.post_realms.return_value = mock_post_response

        mock_qos_response = Mock()
        mock_qos_response.status_code = 400
        mock_error = Mock()
        mock_error.message = "QoS policy assignment failed"
        mock_qos_response.errors = [mock_error]
        mock_blade.post_qos_policies_members.return_value = mock_qos_response

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "QoS policy assignment failed" in call_args["msg"]

    @patch("plugins.modules.purefb_realm.LooseVersion")
    @patch("plugins.modules.purefb_realm.get_system")
    @patch("plugins.modules.purefb_realm.AnsibleModule")
    @patch("plugins.modules.purefb_realm.HAS_PURESTORAGE", True)
    def test_main_api_version_too_old(
        self, mock_ansible_module, mock_get_system, mock_loose_version
    ):
        """Test failure when API version is too old"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "test-realm",
            "state": "present",
            "qos_policy": None,
            "eradicate": False,
            "rename": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade with old API version
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.10"]
        mock_get_system.return_value = mock_blade

        # Mock API version check to fail
        mock_loose_version.return_value.__gt__ = Mock(return_value=True)

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "Purity//FB 4.6.1, or higher, is required" in call_args["msg"]

    @patch("plugins.modules.purefb_realm.get_pending_realm")
    @patch("plugins.modules.purefb_realm.get_realm")
    @patch("plugins.modules.purefb_realm.LooseVersion")
    @patch("plugins.modules.purefb_realm.get_system")
    @patch("plugins.modules.purefb_realm.AnsibleModule")
    @patch("plugins.modules.purefb_realm.HAS_PURESTORAGE", True)
    def test_main_delete_nonexistent_realm(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_loose_version,
        mock_get_realm,
        mock_get_pending_realm,
    ):
        """Test deleting a nonexistent realm (no-op)"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "name": "nonexistent-realm",
            "state": "absent",
            "qos_policy": None,
            "eradicate": False,
            "rename": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.19"]
        mock_get_system.return_value = mock_blade

        # Mock API version check
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)

        # Realm doesn't exist
        mock_get_realm.return_value = None
        mock_get_pending_realm.return_value = None

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify no API calls were made
        mock_blade.patch_realms.assert_not_called()
        mock_blade.delete_realms.assert_not_called()

        # Verify exit_json was called with changed=False
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is False

    def test_get_realm(self):
        """Test get_realm function"""
        # Setup mock module
        mock_module = Mock()
        mock_module.params = {"name": "test-realm"}

        # Setup mock blade
        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.get_realms.return_value = mock_response

        # Call function
        result = get_realm(mock_module, mock_blade)

        # Verify result
        assert result is True
        mock_blade.get_realms.assert_called_once_with(
            destroyed=False, names=["test-realm"]
        )

    def test_get_realm_not_found(self):
        """Test get_realm when realm doesn't exist"""
        # Setup mock module
        mock_module = Mock()
        mock_module.params = {"name": "nonexistent-realm"}

        # Setup mock blade
        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 400
        mock_blade.get_realms.return_value = mock_response

        # Call function
        result = get_realm(mock_module, mock_blade)

        # Verify result
        assert result is None

    def test_get_pending_realm(self):
        """Test get_pending_realm function"""
        # Setup mock module
        mock_module = Mock()
        mock_module.params = {"name": "test-realm"}

        # Setup mock blade
        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.get_realms.return_value = mock_response

        # Call function
        result = get_pending_realm(mock_module, mock_blade)

        # Verify result
        assert result is True
        mock_blade.get_realms.assert_called_once_with(
            destroyed=True, names=["test-realm"]
        )

    def test_get_policy(self):
        """Test get_policy function"""
        # Setup mock module
        mock_module = Mock()
        mock_module.params = {"qos_policy": "test-policy"}

        # Setup mock blade
        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.get_qos_policies.return_value = mock_response

        # Call function
        result = get_policy(mock_module, mock_blade)

        # Verify result
        assert result is True
        mock_blade.get_qos_policies.assert_called_once_with(names=["test-policy"])
