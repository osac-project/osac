# Copyright: (c) 2026, Pure Storage Ansible Team <pure-ansible-team@purestorage.com>
# GNU General Public License v3.0+ (see COPYING.GPLv3 or https://www.gnu.org/licenses/gpl-3.0.txt)

"""Unit tests for purefb_eula module."""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

import sys
from unittest.mock import Mock, patch, MagicMock

# Mock external dependencies before importing module
sys.modules["pypureclient"] = MagicMock()
sys.modules["pypureclient.flashblade"] = MagicMock()
sys.modules["urllib3"] = MagicMock()
sys.modules["distro"] = MagicMock()

from plugins.modules.purefb_eula import main, set_eula


class TestPurefbEula:
    """Test cases for purefb_eula module"""

    @patch("plugins.modules.purefb_eula.EulaSignature")
    @patch("plugins.modules.purefb_eula.Eula")
    @patch("plugins.modules.purefb_eula.get_system")
    @patch("plugins.modules.purefb_eula.AnsibleModule")
    @patch("plugins.modules.purefb_eula.HAS_PYPURECLIENT", True)
    def test_set_eula_success(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_eula_class,
        mock_signature_class,
    ):
        """Test successful EULA signing"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = False
        mock_module.params = {
            "company": "ACME Storage, Inc.",
            "name": "Fred Bloggs",
            "title": "Storage Manager",
        }
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_current_signature = Mock()
        mock_current_signature.accepted = False
        mock_current_signature.company = "Different Company"
        mock_current_signature.name = "Different Name"
        mock_current_signature.title = "Different Title"
        mock_eula_response = Mock()
        mock_eula_response.signature = mock_current_signature
        mock_blade.get_arrays_eula.return_value.items = [mock_eula_response]

        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.patch_arrays_eula.return_value = mock_response
        mock_get_system.return_value = mock_blade

        # Call function
        set_eula(mock_module, mock_blade)

        # Verify
        mock_blade.patch_arrays_eula.assert_called_once()
        mock_signature_class.assert_called_once_with(
            company="ACME Storage, Inc.",
            name="Fred Bloggs",
            title="Storage Manager",
        )
        mock_eula_class.assert_called_once()
        mock_module.exit_json.assert_called_once_with(changed=True)

    @patch("plugins.modules.purefb_eula.get_system")
    @patch("plugins.modules.purefb_eula.AnsibleModule")
    @patch("plugins.modules.purefb_eula.HAS_PYPURECLIENT", True)
    def test_set_eula_check_mode(self, mock_ansible_module, mock_get_system):
        """Test EULA signing in check mode"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = True
        mock_module.params = {
            "company": "ACME Storage, Inc.",
            "name": "Fred Bloggs",
            "title": "Storage Manager",
        }
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_get_system.return_value = mock_blade

        # Call function
        set_eula(mock_module, mock_blade)

        # Verify - should not call get_arrays_eula or patch in check mode
        mock_blade.get_arrays_eula.assert_not_called()
        mock_blade.patch_arrays_eula.assert_not_called()
        mock_module.exit_json.assert_called_once_with(changed=False)

    @patch("plugins.modules.purefb_eula.EulaSignature")
    @patch("plugins.modules.purefb_eula.Eula")
    @patch("plugins.modules.purefb_eula.get_system")
    @patch("plugins.modules.purefb_eula.AnsibleModule")
    @patch("plugins.modules.purefb_eula.HAS_PYPURECLIENT", True)
    def test_set_eula_failure(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_eula_class,
        mock_signature_class,
    ):
        """Test EULA signing failure"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = False
        mock_module.params = {
            "company": "ACME Storage, Inc.",
            "name": "Fred Bloggs",
            "title": "Storage Manager",
        }
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_current_signature = Mock()
        mock_current_signature.accepted = False
        mock_current_signature.company = "Different Company"
        mock_current_signature.name = "Different Name"
        mock_current_signature.title = "Different Title"
        mock_eula_response = Mock()
        mock_eula_response.signature = mock_current_signature
        mock_blade.get_arrays_eula.return_value.items = [mock_eula_response]

        mock_response = Mock()
        mock_response.status_code = 400
        mock_blade.patch_arrays_eula.return_value = mock_response
        mock_get_system.return_value = mock_blade

        # Call function
        set_eula(mock_module, mock_blade)

        # Verify
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "Signing EULA failed" in call_args["msg"]

    @patch("plugins.modules.purefb_eula.get_system")
    @patch("plugins.modules.purefb_eula.AnsibleModule")
    @patch("plugins.modules.purefb_eula.HAS_PYPURECLIENT", True)
    def test_eula_already_accepted(self, mock_ansible_module, mock_get_system):
        """Test when EULA is already accepted"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = False
        mock_module.params = {
            "company": "ACME Storage, Inc.",
            "name": "Fred Bloggs",
            "title": "Storage Manager",
        }
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_current_signature = Mock()
        mock_current_signature.accepted = True  # Already accepted
        mock_eula_response = Mock()
        mock_eula_response.signature = mock_current_signature
        mock_blade.get_arrays_eula.return_value.items = [mock_eula_response]
        mock_get_system.return_value = mock_blade

        # Call function
        set_eula(mock_module, mock_blade)

        # Verify - should not patch if already accepted
        mock_blade.patch_arrays_eula.assert_not_called()
        mock_module.exit_json.assert_called_once_with(changed=False)

    @patch("plugins.modules.purefb_eula.get_system")
    @patch("plugins.modules.purefb_eula.AnsibleModule")
    @patch("plugins.modules.purefb_eula.HAS_PYPURECLIENT", True)
    def test_eula_signature_unchanged(self, mock_ansible_module, mock_get_system):
        """Test when EULA signature matches current values"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = False
        mock_module.params = {
            "company": "ACME Storage, Inc.",
            "name": "Fred Bloggs",
            "title": "Storage Manager",
        }
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_current_signature = Mock()
        mock_current_signature.accepted = False
        mock_current_signature.company = "ACME Storage, Inc."  # Same
        mock_current_signature.name = "Fred Bloggs"  # Same
        mock_current_signature.title = "Storage Manager"  # Same
        mock_eula_response = Mock()
        mock_eula_response.signature = mock_current_signature
        mock_blade.get_arrays_eula.return_value.items = [mock_eula_response]
        mock_get_system.return_value = mock_blade

        # Call function
        set_eula(mock_module, mock_blade)

        # Verify - should not patch if signature is unchanged
        mock_blade.patch_arrays_eula.assert_not_called()
        mock_module.exit_json.assert_called_once_with(changed=False)

    @patch("plugins.modules.purefb_eula.EulaSignature")
    @patch("plugins.modules.purefb_eula.Eula")
    @patch("plugins.modules.purefb_eula.get_system")
    @patch("plugins.modules.purefb_eula.AnsibleModule")
    @patch("plugins.modules.purefb_eula.HAS_PYPURECLIENT", True)
    def test_update_eula_company(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_eula_class,
        mock_signature_class,
    ):
        """Test updating EULA when company changes"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = False
        mock_module.params = {
            "company": "New Company Name",
            "name": "Fred Bloggs",
            "title": "Storage Manager",
        }
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_current_signature = Mock()
        mock_current_signature.accepted = False
        mock_current_signature.company = "Old Company Name"  # Different
        mock_current_signature.name = "Fred Bloggs"
        mock_current_signature.title = "Storage Manager"
        mock_eula_response = Mock()
        mock_eula_response.signature = mock_current_signature
        mock_blade.get_arrays_eula.return_value.items = [mock_eula_response]

        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.patch_arrays_eula.return_value = mock_response
        mock_get_system.return_value = mock_blade

        # Call function
        set_eula(mock_module, mock_blade)

        # Verify - should update when company changes
        mock_blade.patch_arrays_eula.assert_called_once()
        mock_signature_class.assert_called_once_with(
            company="New Company Name",
            name="Fred Bloggs",
            title="Storage Manager",
        )
        mock_module.exit_json.assert_called_once_with(changed=True)

    @patch("plugins.modules.purefb_eula.EulaSignature")
    @patch("plugins.modules.purefb_eula.Eula")
    @patch("plugins.modules.purefb_eula.get_system")
    @patch("plugins.modules.purefb_eula.AnsibleModule")
    @patch("plugins.modules.purefb_eula.HAS_PYPURECLIENT", True)
    def test_update_eula_name(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_eula_class,
        mock_signature_class,
    ):
        """Test updating EULA when name changes"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = False
        mock_module.params = {
            "company": "ACME Storage, Inc.",
            "name": "Jane Smith",
            "title": "Storage Manager",
        }
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_current_signature = Mock()
        mock_current_signature.accepted = False
        mock_current_signature.company = "ACME Storage, Inc."
        mock_current_signature.name = "Fred Bloggs"  # Different
        mock_current_signature.title = "Storage Manager"
        mock_eula_response = Mock()
        mock_eula_response.signature = mock_current_signature
        mock_blade.get_arrays_eula.return_value.items = [mock_eula_response]

        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.patch_arrays_eula.return_value = mock_response
        mock_get_system.return_value = mock_blade

        # Call function
        set_eula(mock_module, mock_blade)

        # Verify - should update when name changes
        mock_blade.patch_arrays_eula.assert_called_once()
        mock_signature_class.assert_called_once_with(
            company="ACME Storage, Inc.",
            name="Jane Smith",
            title="Storage Manager",
        )
        mock_module.exit_json.assert_called_once_with(changed=True)

    @patch("plugins.modules.purefb_eula.EulaSignature")
    @patch("plugins.modules.purefb_eula.Eula")
    @patch("plugins.modules.purefb_eula.get_system")
    @patch("plugins.modules.purefb_eula.AnsibleModule")
    @patch("plugins.modules.purefb_eula.HAS_PYPURECLIENT", True)
    def test_update_eula_title(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_eula_class,
        mock_signature_class,
    ):
        """Test updating EULA when title changes"""
        # Setup mocks
        mock_module = Mock()
        mock_module.check_mode = False
        mock_module.params = {
            "company": "ACME Storage, Inc.",
            "name": "Fred Bloggs",
            "title": "CTO",
        }
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_current_signature = Mock()
        mock_current_signature.accepted = False
        mock_current_signature.company = "ACME Storage, Inc."
        mock_current_signature.name = "Fred Bloggs"
        mock_current_signature.title = "Storage Manager"  # Different
        mock_eula_response = Mock()
        mock_eula_response.signature = mock_current_signature
        mock_blade.get_arrays_eula.return_value.items = [mock_eula_response]

        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.patch_arrays_eula.return_value = mock_response
        mock_get_system.return_value = mock_blade

        # Call function
        set_eula(mock_module, mock_blade)

        # Verify - should update when title changes
        mock_blade.patch_arrays_eula.assert_called_once()
        mock_signature_class.assert_called_once_with(
            company="ACME Storage, Inc.",
            name="Fred Bloggs",
            title="CTO",
        )
        mock_module.exit_json.assert_called_once_with(changed=True)

    @patch("plugins.modules.purefb_eula.get_system")
    @patch("plugins.modules.purefb_eula.AnsibleModule")
    @patch("plugins.modules.purefb_eula.HAS_PYPURECLIENT", False)
    def test_main_missing_sdk(self, mock_ansible_module, mock_get_system):
        """Test main when pypureclient SDK is missing"""
        # Setup mocks
        mock_module = Mock()
        mock_module.params = {
            "company": "ACME Storage, Inc.",
            "name": "Fred Bloggs",
            "title": "Storage Manager",
        }
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

        # Verify - should fail with SDK missing message
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "py-pure-client SDK required" in call_args["msg"]

    @patch("plugins.modules.purefb_eula.set_eula")
    @patch("plugins.modules.purefb_eula.get_system")
    @patch("plugins.modules.purefb_eula.AnsibleModule")
    @patch("plugins.modules.purefb_eula.HAS_PYPURECLIENT", True)
    def test_main_calls_set_eula(
        self, mock_ansible_module, mock_get_system, mock_set_eula
    ):
        """Test main function calls set_eula"""
        # Setup mocks
        mock_module = Mock()
        mock_module.params = {
            "company": "ACME Storage, Inc.",
            "name": "Fred Bloggs",
            "title": "Storage Manager",
        }
        # Make exit_json raise exception to stop execution
        mock_module.exit_json.side_effect = SystemExit("exit_json called")
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_get_system.return_value = mock_blade

        # Call main - should raise SystemExit when set_eula calls exit_json
        try:
            main()
        except SystemExit:
            pass

        # Verify - should call set_eula with module and blade
        mock_set_eula.assert_called_once_with(mock_module, mock_blade)
