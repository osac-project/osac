# Copyright: (c) 2026, Pure Storage Ansible Team <pure-ansible-team@purestorage.com>
# GNU General Public License v3.0+ (see COPYING.GPLv3 or https://www.gnu.org/licenses/gpl-3.0.txt)

"""Unit tests for purefb_bladename module."""

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

from plugins.modules.purefb_bladename import main, update_name


class TestPurefbBladename:
    """Test cases for purefb_bladename module"""

    @patch("plugins.modules.purefb_bladename.get_system")
    @patch("plugins.modules.purefb_bladename.AnsibleModule")
    @patch("plugins.modules.purefb_bladename.HAS_PYPURECLIENT", True)
    def test_update_name_success(self, mock_ansible_module, mock_get_system):
        """Test successful blade name update"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = False
        mock_module.params = {"name": "new-blade-name"}
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.patch_arrays.return_value = mock_response
        mock_get_system.return_value = mock_blade

        # Call function
        update_name(mock_module, mock_blade)

        # Verify
        mock_blade.patch_arrays.assert_called_once()
        mock_module.exit_json.assert_called_once_with(changed=True)

    @patch("plugins.modules.purefb_bladename.get_system")
    @patch("plugins.modules.purefb_bladename.AnsibleModule")
    @patch("plugins.modules.purefb_bladename.HAS_PYPURECLIENT", True)
    def test_update_name_check_mode(self, mock_ansible_module, mock_get_system):
        """Test blade name update in check mode"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = True
        mock_module.params = {"name": "new-blade-name"}
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_get_system.return_value = mock_blade

        # Call function
        update_name(mock_module, mock_blade)

        # Verify - should not call patch_arrays in check mode
        mock_blade.patch_arrays.assert_not_called()
        mock_module.exit_json.assert_called_once_with(changed=True)

    @patch("plugins.modules.purefb_bladename.get_system")
    @patch("plugins.modules.purefb_bladename.AnsibleModule")
    @patch("plugins.modules.purefb_bladename.HAS_PYPURECLIENT", True)
    def test_update_name_failure(self, mock_ansible_module, mock_get_system):
        """Test blade name update failure"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = False
        mock_module.params = {"name": "new-blade-name"}
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 400
        mock_response.errors = [Mock(message="Invalid name")]
        mock_blade.patch_arrays.return_value = mock_response
        mock_get_system.return_value = mock_blade

        # Call function
        update_name(mock_module, mock_blade)

        # Verify
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "Failed to change array name" in call_args["msg"]

    @patch("plugins.modules.purefb_bladename.get_system")
    @patch("plugins.modules.purefb_bladename.AnsibleModule")
    @patch("plugins.modules.purefb_bladename.HAS_PYPURECLIENT", True)
    def test_main_name_unchanged(self, mock_ansible_module, mock_get_system):
        """Test main when name is already set correctly"""
        # Setup mocks
        mock_module = Mock()
        mock_module.params = {"name": "current-blade-name", "state": "present"}
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_array = Mock()
        mock_array.name = "current-blade-name"
        mock_blade.get_arrays.return_value.items = [mock_array]
        mock_get_system.return_value = mock_blade

        # Call main
        main()

        # Verify - should not update if name is already correct
        mock_blade.patch_arrays.assert_not_called()
        mock_module.exit_json.assert_called_once_with(changed=False)

    @patch("plugins.modules.purefb_bladename.get_system")
    @patch("plugins.modules.purefb_bladename.AnsibleModule")
    @patch("plugins.modules.purefb_bladename.HAS_PYPURECLIENT", True)
    def test_main_invalid_name_format(self, mock_ansible_module, mock_get_system):
        """Test main with invalid blade name format"""
        # Setup mocks
        mock_module = Mock()
        mock_module.params = {"name": "invalid_name!", "state": "present"}
        mock_module.check_mode = False
        # Make fail_json raise exception to stop execution
        mock_module.fail_json.side_effect = SystemExit("fail_json called")
        mock_ansible_module.return_value = mock_module

        # Mock blade - get_system is called before validation
        mock_blade = Mock()
        mock_get_system.return_value = mock_blade

        # Call main - should raise SystemExit
        try:
            main()
        except SystemExit:
            pass

        # Verify - should fail with invalid name
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "does not conform to array name rules" in call_args["msg"]

    @patch("plugins.modules.purefb_bladename.get_system")
    @patch("plugins.modules.purefb_bladename.AnsibleModule")
    @patch("plugins.modules.purefb_bladename.HAS_PYPURECLIENT", False)
    def test_main_missing_sdk(self, mock_ansible_module, mock_get_system):
        """Test main when pypureclient SDK is missing"""
        # Setup mocks
        mock_module = Mock()
        mock_module.params = {"name": "blade-name", "state": "present"}
        mock_module.check_mode = False
        # Make fail_json raise exception to stop execution
        mock_module.fail_json.side_effect = SystemExit("fail_json called")
        mock_ansible_module.return_value = mock_module

        # Call main - should raise SystemExit
        try:
            main()
        except SystemExit:
            pass

        # Verify - should fail with SDK missing message (before calling get_system)
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "py-pure-client sdk is required" in call_args["msg"]
        # Should not call get_system since SDK check fails first
        mock_get_system.assert_not_called()

    @patch("plugins.modules.purefb_bladename.get_system")
    @patch("plugins.modules.purefb_bladename.AnsibleModule")
    @patch("plugins.modules.purefb_bladename.HAS_PYPURECLIENT", True)
    def test_main_valid_name_formats(self, mock_ansible_module, mock_get_system):
        """Test main with various valid blade name formats"""
        valid_names = [
            "blade1",
            "blade-1",
            "my-blade-123",
            "a",
            "a" * 56,  # Max length (56 chars)
            "blade-name-with-numbers-123",
        ]

        for name in valid_names:
            # Setup mocks
            mock_module = Mock()
            mock_module.params = {"name": name, "state": "present"}
            mock_ansible_module.return_value = mock_module

            mock_blade = Mock()
            mock_array = Mock()
            mock_array.name = "different-name"
            mock_blade.get_arrays.return_value.items = [mock_array]
            mock_response = Mock()
            mock_response.status_code = 200
            mock_blade.patch_arrays.return_value = mock_response
            mock_get_system.return_value = mock_blade

            # Call main
            main()

            # Verify - should not fail validation
            assert not any(
                "does not conform" in str(call)
                for call in mock_module.fail_json.call_args_list
            )

    @patch("plugins.modules.purefb_bladename.get_system")
    @patch("plugins.modules.purefb_bladename.AnsibleModule")
    @patch("plugins.modules.purefb_bladename.HAS_PYPURECLIENT", True)
    def test_main_invalid_name_formats(self, mock_ansible_module, mock_get_system):
        """Test main with various invalid blade name formats"""
        invalid_names = [
            "blade_name",  # Underscore not allowed
            "-blade",  # Cannot start with hyphen
            "blade-",  # Cannot end with hyphen
            "blade name",  # Space not allowed
            "blade@name",  # Special char not allowed
            "a" * 57,  # Too long (>56 chars)
        ]

        for name in invalid_names:
            # Setup mocks
            mock_module = Mock()
            mock_module.params = {"name": name, "state": "present"}
            mock_module.check_mode = False
            # Make fail_json raise exception to stop execution
            mock_module.fail_json.side_effect = SystemExit("fail_json called")
            mock_ansible_module.return_value = mock_module

            mock_blade = Mock()
            mock_get_system.return_value = mock_blade

            # Call main - should raise SystemExit
            try:
                main()
            except SystemExit:
                pass

            # Verify - should fail validation
            mock_module.fail_json.assert_called()
            call_args = mock_module.fail_json.call_args[1]
            assert "does not conform to array name rules" in call_args["msg"]

            # Reset mocks for next iteration
            mock_module.reset_mock()
            mock_blade.reset_mock()
            mock_ansible_module.reset_mock()
            mock_get_system.reset_mock()
