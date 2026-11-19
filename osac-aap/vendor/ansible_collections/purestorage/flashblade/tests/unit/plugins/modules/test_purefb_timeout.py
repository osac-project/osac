# Copyright: (c) 2026, Pure Storage Ansible Team <pure-ansible-team@purestorage.com>
# GNU General Public License v3.0+ (see COPYING.GPLv3 or https://www.gnu.org/licenses/gpl-3.0.txt)

"""Unit tests for purefb_timeout module."""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

import sys
from unittest.mock import Mock, patch, MagicMock

# Mock external dependencies before importing module
sys.modules["pypureclient"] = MagicMock()
sys.modules["pypureclient.flashblade"] = MagicMock()
sys.modules["urllib3"] = MagicMock()
sys.modules["distro"] = MagicMock()

from plugins.modules.purefb_timeout import main, set_timeout, disable_timeout


class TestPurefbTimeout:
    """Test cases for purefb_timeout module"""

    @patch("plugins.modules.purefb_timeout.flashblade.Array")
    @patch("plugins.modules.purefb_timeout.get_system")
    @patch("plugins.modules.purefb_timeout.AnsibleModule")
    @patch("plugins.modules.purefb_timeout.HAS_PYPURECLIENT", True)
    def test_set_timeout_success(
        self, mock_ansible_module, mock_get_system, mock_array_class
    ):
        """Test successful timeout set"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = False
        mock_module.params = {"timeout": 30}
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.patch_arrays.return_value = mock_response
        mock_get_system.return_value = mock_blade

        # Call function
        set_timeout(mock_module, mock_blade)

        # Verify
        mock_blade.patch_arrays.assert_called_once()
        mock_array_class.assert_called_once_with(idle_timeout=30 * 60000)
        mock_module.exit_json.assert_called_once_with(changed=True)

    @patch("plugins.modules.purefb_timeout.get_system")
    @patch("plugins.modules.purefb_timeout.AnsibleModule")
    @patch("plugins.modules.purefb_timeout.HAS_PYPURECLIENT", True)
    def test_set_timeout_check_mode(self, mock_ansible_module, mock_get_system):
        """Test timeout set in check mode"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = True
        mock_module.params = {"timeout": 30}
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_get_system.return_value = mock_blade

        # Call function
        set_timeout(mock_module, mock_blade)

        # Verify - should not call patch_arrays in check mode
        mock_blade.patch_arrays.assert_not_called()
        mock_module.exit_json.assert_called_once_with(changed=True)

    @patch("plugins.modules.purefb_timeout.get_system")
    @patch("plugins.modules.purefb_timeout.AnsibleModule")
    @patch("plugins.modules.purefb_timeout.HAS_PYPURECLIENT", True)
    def test_set_timeout_failure(self, mock_ansible_module, mock_get_system):
        """Test timeout set failure"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = False
        mock_module.params = {"timeout": 30}
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 400
        mock_blade.patch_arrays.return_value = mock_response
        mock_get_system.return_value = mock_blade

        # Call function
        set_timeout(mock_module, mock_blade)

        # Verify
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "Failed to set GUI idle timeout" in call_args["msg"]

    @patch("plugins.modules.purefb_timeout.flashblade.Array")
    @patch("plugins.modules.purefb_timeout.get_system")
    @patch("plugins.modules.purefb_timeout.AnsibleModule")
    @patch("plugins.modules.purefb_timeout.HAS_PYPURECLIENT", True)
    def test_disable_timeout_success(
        self, mock_ansible_module, mock_get_system, mock_array_class
    ):
        """Test successful timeout disable"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.patch_arrays.return_value = mock_response
        mock_get_system.return_value = mock_blade

        # Call function
        disable_timeout(mock_module, mock_blade)

        # Verify
        mock_blade.patch_arrays.assert_called_once()
        mock_array_class.assert_called_once_with(idle_timeout=0)
        mock_module.exit_json.assert_called_once_with(changed=True)

    @patch("plugins.modules.purefb_timeout.get_system")
    @patch("plugins.modules.purefb_timeout.AnsibleModule")
    @patch("plugins.modules.purefb_timeout.HAS_PYPURECLIENT", True)
    def test_disable_timeout_check_mode(self, mock_ansible_module, mock_get_system):
        """Test timeout disable in check mode"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = True
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_get_system.return_value = mock_blade

        # Call function
        disable_timeout(mock_module, mock_blade)

        # Verify - should not call patch_arrays in check mode
        mock_blade.patch_arrays.assert_not_called()
        mock_module.exit_json.assert_called_once_with(changed=True)

    @patch("plugins.modules.purefb_timeout.get_system")
    @patch("plugins.modules.purefb_timeout.AnsibleModule")
    @patch("plugins.modules.purefb_timeout.HAS_PYPURECLIENT", True)
    def test_disable_timeout_failure(self, mock_ansible_module, mock_get_system):
        """Test timeout disable failure"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 400
        mock_blade.patch_arrays.return_value = mock_response
        mock_get_system.return_value = mock_blade

        # Call function
        disable_timeout(mock_module, mock_blade)

        # Verify
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "Failed to disable GUI idle timeout" in call_args["msg"]

    @patch("plugins.modules.purefb_timeout.get_system")
    @patch("plugins.modules.purefb_timeout.AnsibleModule")
    @patch("plugins.modules.purefb_timeout.HAS_PYPURECLIENT", True)
    def test_main_timeout_unchanged(self, mock_ansible_module, mock_get_system):
        """Test main when timeout is already set correctly"""
        # Setup mocks
        mock_module = Mock()
        mock_module.params = {"timeout": 30, "state": "present"}
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_array = Mock()
        mock_array.idle_timeout = 30 * 60000  # Already set to 30 minutes
        mock_blade.get_arrays.return_value.items = [mock_array]
        mock_get_system.return_value = mock_blade

        # Call main
        main()

        # Verify - should not update if timeout is already correct
        mock_blade.patch_arrays.assert_not_called()
        mock_module.exit_json.assert_called_once_with(changed=False)

    @patch("plugins.modules.purefb_timeout.get_system")
    @patch("plugins.modules.purefb_timeout.AnsibleModule")
    @patch("plugins.modules.purefb_timeout.HAS_PYPURECLIENT", True)
    def test_main_timeout_already_disabled(self, mock_ansible_module, mock_get_system):
        """Test main when timeout is already disabled"""
        # Setup mocks
        mock_module = Mock()
        mock_module.params = {"timeout": 30, "state": "absent"}
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_array = Mock()
        mock_array.idle_timeout = 0  # Already disabled
        mock_blade.get_arrays.return_value.items = [mock_array]
        mock_get_system.return_value = mock_blade

        # Call main
        main()

        # Verify - should not update if timeout is already disabled
        mock_blade.patch_arrays.assert_not_called()
        mock_module.exit_json.assert_called_once_with(changed=False)

    @patch("plugins.modules.purefb_timeout.get_system")
    @patch("plugins.modules.purefb_timeout.AnsibleModule")
    @patch("plugins.modules.purefb_timeout.HAS_PYPURECLIENT", True)
    def test_main_invalid_timeout_too_low(self, mock_ansible_module, mock_get_system):
        """Test main with timeout value too low"""
        # Setup mocks
        mock_module = Mock()
        mock_module.params = {"timeout": 3, "state": "present"}
        mock_module.check_mode = False
        # Make fail_json raise exception to stop execution
        mock_module.fail_json.side_effect = SystemExit("fail_json called")
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_array = Mock()
        mock_array.idle_timeout = 0
        mock_blade.get_arrays.return_value.items = [mock_array]
        mock_get_system.return_value = mock_blade

        # Call main - should raise SystemExit
        try:
            main()
        except SystemExit:
            pass

        # Verify - should fail with invalid timeout message
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "Timeout value must be between 5 and 180 minutes" in call_args["msg"]

    @patch("plugins.modules.purefb_timeout.get_system")
    @patch("plugins.modules.purefb_timeout.AnsibleModule")
    @patch("plugins.modules.purefb_timeout.HAS_PYPURECLIENT", True)
    def test_main_invalid_timeout_too_high(self, mock_ansible_module, mock_get_system):
        """Test main with timeout value too high"""
        # Setup mocks
        mock_module = Mock()
        mock_module.params = {"timeout": 200, "state": "present"}
        mock_module.check_mode = False
        # Make fail_json raise exception to stop execution
        mock_module.fail_json.side_effect = SystemExit("fail_json called")
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_array = Mock()
        mock_array.idle_timeout = 0
        mock_blade.get_arrays.return_value.items = [mock_array]
        mock_get_system.return_value = mock_blade

        # Call main - should raise SystemExit
        try:
            main()
        except SystemExit:
            pass

        # Verify - should fail with invalid timeout message
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "Timeout value must be between 5 and 180 minutes" in call_args["msg"]

    @patch("plugins.modules.purefb_timeout.get_system")
    @patch("plugins.modules.purefb_timeout.AnsibleModule")
    @patch("plugins.modules.purefb_timeout.HAS_PYPURECLIENT", False)
    def test_main_missing_sdk(self, mock_ansible_module, mock_get_system):
        """Test main when pypureclient SDK is missing"""
        # Setup mocks
        mock_module = Mock()
        mock_module.params = {"timeout": 30, "state": "present"}
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

    @patch("plugins.modules.purefb_timeout.get_system")
    @patch("plugins.modules.purefb_timeout.AnsibleModule")
    @patch("plugins.modules.purefb_timeout.HAS_PYPURECLIENT", True)
    def test_main_valid_timeout_boundaries(self, mock_ansible_module, mock_get_system):
        """Test main with valid timeout boundary values"""
        valid_timeouts = [5, 30, 180]  # Min, middle, max

        for timeout in valid_timeouts:
            # Setup mocks
            mock_module = Mock()
            mock_module.params = {"timeout": timeout, "state": "present"}
            mock_ansible_module.return_value = mock_module

            mock_blade = Mock()
            mock_array = Mock()
            mock_array.idle_timeout = 0  # Different from target
            mock_blade.get_arrays.return_value.items = [mock_array]
            mock_response = Mock()
            mock_response.status_code = 200
            mock_blade.patch_arrays.return_value = mock_response
            mock_get_system.return_value = mock_blade

            # Call main
            main()

            # Verify - should not fail validation
            assert not any(
                "Timeout value must be between 5 and 180 minutes" in str(call)
                for call in mock_module.fail_json.call_args_list
            )

            # Reset mocks for next iteration
            mock_module.reset_mock()
            mock_blade.reset_mock()
            mock_ansible_module.reset_mock()
            mock_get_system.reset_mock()

    @patch("plugins.modules.purefb_timeout.flashblade.Array")
    @patch("plugins.modules.purefb_timeout.get_system")
    @patch("plugins.modules.purefb_timeout.AnsibleModule")
    @patch("plugins.modules.purefb_timeout.HAS_PYPURECLIENT", True)
    def test_main_set_timeout_when_different(
        self, mock_ansible_module, mock_get_system, mock_array_class
    ):
        """Test main sets timeout when current value is different"""
        # Setup mocks
        mock_module = Mock()
        mock_module.params = {"timeout": 60, "state": "present"}
        mock_module.check_mode = False
        # Make exit_json raise SystemExit to simulate actual module behavior
        mock_module.exit_json.side_effect = SystemExit("exit_json called")
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_array = Mock()
        mock_array.idle_timeout = 30 * 60000  # Currently 30 minutes
        mock_blade.get_arrays.return_value.items = [mock_array]
        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.patch_arrays.return_value = mock_response
        mock_get_system.return_value = mock_blade

        # Call main - set_timeout will call exit_json and raise SystemExit
        try:
            main()
        except SystemExit:
            pass

        # Verify - should call patch_arrays to update timeout
        mock_blade.patch_arrays.assert_called_once()
        mock_array_class.assert_called_once_with(idle_timeout=60 * 60000)
        # set_timeout calls exit_json(changed=True), which exits the module
        mock_module.exit_json.assert_called_once_with(changed=True)

    @patch("plugins.modules.purefb_timeout.flashblade.Array")
    @patch("plugins.modules.purefb_timeout.get_system")
    @patch("plugins.modules.purefb_timeout.AnsibleModule")
    @patch("plugins.modules.purefb_timeout.HAS_PYPURECLIENT", True)
    def test_main_disable_timeout_when_enabled(
        self, mock_ansible_module, mock_get_system, mock_array_class
    ):
        """Test main disables timeout when currently enabled"""
        # Setup mocks
        mock_module = Mock()
        mock_module.params = {"timeout": 30, "state": "absent"}
        mock_module.check_mode = False
        # Make exit_json raise SystemExit to simulate actual module behavior
        mock_module.exit_json.side_effect = SystemExit("exit_json called")
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_array = Mock()
        mock_array.idle_timeout = 30 * 60000  # Currently enabled
        mock_blade.get_arrays.return_value.items = [mock_array]
        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.patch_arrays.return_value = mock_response
        mock_get_system.return_value = mock_blade

        # Call main - disable_timeout will call exit_json and raise SystemExit
        try:
            main()
        except SystemExit:
            pass

        # Verify - should call patch_arrays to disable timeout
        mock_blade.patch_arrays.assert_called_once()
        mock_array_class.assert_called_once_with(idle_timeout=0)
        # disable_timeout calls exit_json(changed=True), which exits the module
        mock_module.exit_json.assert_called_once_with(changed=True)
