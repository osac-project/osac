# Copyright: (c) 2026, Everpure Ansible Team <pure-ansible-team@purestorage.com>
# GNU General Public License v3.0+ (see COPYING.GPLv3 or https://www.gnu.org/licenses/gpl-3.0.txt)

"""Unit tests for purefb_certs module."""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

import sys
import multiprocessing
from unittest.mock import Mock, patch, MagicMock

# Mock multiprocessing context for Windows
if sys.platform == "win32":
    original_get_context = multiprocessing.get_context

    def mock_get_context(method=None):
        if method == "fork":
            return original_get_context("spawn")
        return original_get_context(method)

    multiprocessing.get_context = mock_get_context

# Mock external dependencies before importing module
sys.modules["pypureclient"] = MagicMock()
sys.modules["pypureclient.flashblade"] = MagicMock()
sys.modules["urllib3"] = MagicMock()
sys.modules["distro"] = MagicMock()
sys.modules["pycountry"] = MagicMock()
# Mock Unix-specific modules for Windows compatibility
sys.modules["grp"] = MagicMock()
sys.modules["fcntl"] = MagicMock()
sys.modules["pwd"] = MagicMock()
sys.modules["syslog"] = MagicMock()
# Mock termios with required constants
mock_termios = MagicMock()
mock_termios.TCSAFLUSH = 2
sys.modules["termios"] = mock_termios
# Mock Ansible display module to avoid ctypes issues on Windows
sys.modules["ansible.utils.display"] = MagicMock()
sys.modules["ansible.utils.multiprocessing"] = MagicMock()
# Mock ansible_collections module
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

from plugins.modules.purefb_certs import main


class TestPurefbCerts:
    """Test cases for purefb_certs module"""

    @patch("plugins.modules.purefb_certs.pycountry")
    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_create_cert_with_intermediate(
        self, mock_ansible_module, mock_get_system, mock_pycountry
    ):
        """Test creating a certificate with intermediate certificate"""
        # Setup mock module
        mock_module = Mock()
        # Make exit_json and fail_json raise exceptions to stop execution
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "present",
            "name": "test-cert",
            "common_name": "test.example.com",
            "country": "US",
            "province": "CA",
            "locality": "SF",
            "organization": "Test",
            "org_unit": "IT",
            "email": "test@example.com",
            "key_size": 2048,
            "certificate": None,
            "intermediate_cert": "-----BEGIN CERTIFICATE-----\nINTER\n-----END CERTIFICATE-----",
            "key": None,
            "passphrase": None,
            "generate": False,
            "days": 3650,
            "key_algorithm": None,
            "export_file": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_version = Mock()
        mock_version.version = "2.15"
        mock_blade.get_versions.return_value.items = [mock_version]

        # Mock array name
        mock_array = Mock()
        mock_array.name = "test-array"
        mock_blade.get_arrays.return_value.items = [mock_array]

        # Certificate doesn't exist
        mock_blade.get_certificates.return_value.status_code = 400

        # Mock successful creation
        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.post_certificates.return_value = mock_response

        mock_get_system.return_value = mock_blade

        # Mock pycountry
        mock_country = Mock()
        mock_pycountry.countries.get.return_value = mock_country

        # Call main - expect SystemExit from exit_json
        try:
            main()
        except SystemExit:
            pass

        # Verify certificate was created
        mock_blade.post_certificates.assert_called_once()
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_certs.pycountry")
    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_update_cert_uses_patch(
        self, mock_ansible_module, mock_get_system, mock_pycountry
    ):
        """Test BUG #4 fix: update_cert uses CertificatePatch, not CertificatePost"""
        # Setup mock module
        mock_module = Mock()
        # Make exit_json and fail_json raise exceptions to stop execution
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "present",
            "name": "management",
            "common_name": None,
            "country": None,
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": None,
            "certificate": "-----BEGIN CERTIFICATE-----\nNEW\n-----END CERTIFICATE-----",
            "intermediate_cert": "-----BEGIN CERTIFICATE-----\nINTER\n-----END CERTIFICATE-----",
            "key": "-----BEGIN PRIVATE KEY-----\nKEY\n-----END PRIVATE KEY-----",
            "passphrase": "secret",
            "generate": False,
            "days": 3650,
            "key_algorithm": None,
            "export_file": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_version = Mock()
        mock_version.version = "2.20"
        mock_blade.get_versions.return_value.items = [mock_version]

        # Mock array name
        mock_array = Mock()
        mock_array.name = "test-array"
        mock_blade.get_arrays.return_value.items = [mock_array]

        # Certificate exists
        mock_blade.get_certificates.return_value.status_code = 200

        # Mock existing certificate
        mock_cert = Mock()
        mock_cert.certificate = (
            "-----BEGIN CERTIFICATE-----\nOLD\n-----END CERTIFICATE-----"
        )
        mock_cert.intermediate_certificate = None
        mock_cert.common_name = "old.example.com"
        mock_cert.country = "US"
        mock_cert.email = "old@example.com"
        mock_cert.key_size = 2048
        mock_cert.locality = "SF"
        mock_cert.state = "CA"
        mock_cert.organization = "Old Org"
        mock_cert.organizational_unit = "IT"
        mock_cert.key_algorithm = "rsa"

        # Mock copy method to return a new object
        new_cert = Mock()
        new_cert.certificate = mock_module.params["certificate"]
        new_cert.intermediate_certificate = mock_module.params["intermediate_cert"]
        new_cert.common_name = "old.example.com"
        new_cert.country = "US"
        new_cert.email = "old@example.com"
        new_cert.key_size = 2048
        new_cert.locality = "SF"
        new_cert.state = "CA"
        new_cert.organization = "Old Org"
        new_cert.organizational_unit = "IT"
        new_cert.key_algorithm = "rsa"
        new_cert.__ne__ = Mock(return_value=True)  # new_cert != current_cert

        mock_cert.copy = Mock(return_value=new_cert)
        mock_blade.get_certificates.return_value.items = [mock_cert]

        # Mock successful update
        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.patch_certificates.return_value = mock_response

        mock_get_system.return_value = mock_blade

        # Call main - expect SystemExit from exit_json
        try:
            main()
        except SystemExit:
            pass

        # Verify patch_certificates was called (not post_certificates)
        mock_blade.patch_certificates.assert_called_once()
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_import_cert_check_mode(self, mock_ansible_module, mock_get_system):
        """Test BUG #3 fix: import_cert doesn't raise NameError in check_mode"""
        # Setup mock module
        mock_module = Mock()
        # Make exit_json and fail_json raise exceptions to stop execution
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "import",
            "name": "test-cert",
            "common_name": None,
            "country": None,
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": 2048,
            "certificate": "-----BEGIN CERTIFICATE-----\nCERT\n-----END CERTIFICATE-----",
            "intermediate_cert": None,
            "key": None,
            "passphrase": None,
            "generate": False,
            "days": 3650,
            "key_algorithm": None,
            "export_file": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = True  # This is the key - check_mode enabled
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_version = Mock()
        mock_version.version = "2.10"
        mock_blade.get_versions.return_value.items = [mock_version]

        # Certificate doesn't exist
        mock_blade.get_certificates.return_value.status_code = 400

        mock_get_system.return_value = mock_blade

        # Call main - should NOT raise NameError, expect SystemExit from exit_json
        try:
            main()
        except SystemExit:
            pass

        # Verify post_certificates was NOT called in check mode
        mock_blade.post_certificates.assert_not_called()

        # Verify exit_json was called with changed=True (no error)
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_delete_cert_global_fails(self, mock_ansible_module, mock_get_system):
        """Test delete_cert fails when trying to delete global certificate"""
        # Setup mock module
        mock_module = Mock()
        # Make exit_json and fail_json raise exceptions to stop execution
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "absent",
            "name": "global",
            "common_name": None,
            "country": None,
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": 2048,
            "certificate": None,
            "intermediate_cert": None,
            "key": None,
            "passphrase": None,
            "generate": False,
            "days": 3650,
            "key_algorithm": None,
            "export_file": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_version = Mock()
        mock_version.version = "2.10"
        mock_blade.get_versions.return_value.items = [mock_version]

        # Certificate exists
        mock_blade.get_certificates.return_value.status_code = 200

        mock_get_system.return_value = mock_blade

        # Call main - expect SystemExit from fail_json
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "Global certificate cannot be deleted" in call_args["msg"]

    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_delete_cert_success(self, mock_ansible_module, mock_get_system):
        """Test delete_cert successfully deletes non-management certificate"""
        # Setup mock module
        mock_module = Mock()
        # Make exit_json and fail_json raise exceptions to stop execution
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "absent",
            "name": "test-cert",
            "common_name": None,
            "country": None,
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": 2048,
            "certificate": None,
            "intermediate_cert": None,
            "key": None,
            "passphrase": None,
            "generate": False,
            "days": 3650,
            "key_algorithm": None,
            "export_file": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_version = Mock()
        mock_version.version = "2.10"
        mock_blade.get_versions.return_value.items = [mock_version]

        # Certificate exists
        mock_blade.get_certificates.return_value.status_code = 200

        # Mock successful deletion
        mock_response = Mock()
        mock_response.status_code = 200
        mock_blade.delete_certificates.return_value = mock_response

        mock_get_system.return_value = mock_blade

        # Call main - expect SystemExit from exit_json
        try:
            main()
        except SystemExit:
            pass

        # Verify delete_certificates was called
        mock_blade.delete_certificates.assert_called_once_with(names=["test-cert"])

        # Verify exit_json was called with changed=True
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_certs.pycountry")
    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_export_cert_success(
        self, mock_ansible_module, mock_get_system, mock_pycountry
    ):
        """Test export_cert successfully exports certificate"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "export",
            "name": "test-cert",
            "export_file": "test_cert.pem",
            "common_name": None,
            "country": None,
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": None,
            "certificate": None,
            "intermediate_cert": None,
            "key": None,
            "passphrase": None,
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_version = Mock()
        mock_version.version = "2.15"
        mock_blade.get_versions.return_value.items = [mock_version]

        # Certificate exists
        mock_cert_response = Mock()
        mock_cert_response.status_code = 200
        mock_cert = Mock()
        mock_cert.certificate = (
            "-----BEGIN CERTIFICATE-----\nTEST\n-----END CERTIFICATE-----"
        )
        mock_cert_response.items = [mock_cert]
        mock_blade.get_certificates.return_value = mock_cert_response

        mock_get_system.return_value = mock_blade

        # Mock file operations
        with patch("builtins.open", create=True) as mock_open:
            mock_file = Mock()
            mock_open.return_value.__enter__.return_value = mock_file

            # Call main
            try:
                main()
            except SystemExit:
                pass

            # Verify file was written
            mock_open.assert_called_once_with("test_cert.pem", "w", encoding="utf-8")
            mock_file.write.assert_called_once_with(
                "-----BEGIN CERTIFICATE-----\nTEST\n-----END CERTIFICATE-----"
            )

        # Verify exit_json was called with changed=True
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_certs.pycountry")
    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_export_cert_failure(
        self, mock_ansible_module, mock_get_system, mock_pycountry
    ):
        """Test export_cert fails when certificate doesn't exist"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "export",
            "name": "nonexistent-cert",
            "export_file": "test_cert.pem",
            "common_name": None,
            "country": None,
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": None,
            "certificate": None,
            "intermediate_cert": None,
            "key": None,
            "passphrase": None,
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_version = Mock()
        mock_version.version = "2.15"
        mock_blade.get_versions.return_value.items = [mock_version]

        # Certificate doesn't exist
        mock_cert_response = Mock()
        mock_cert_response.status_code = 400
        mock_error = Mock()
        mock_error.message = "Certificate not found"
        mock_cert_response.errors = [mock_error]
        mock_blade.get_certificates.return_value = mock_cert_response

        mock_get_system.return_value = mock_blade

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "Exporting Certificate failed" in call_args["msg"]

    @patch("plugins.modules.purefb_certs.pycountry")
    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_create_csr_success(
        self, mock_ansible_module, mock_get_system, mock_pycountry
    ):
        """Test create_csr successfully creates CSR"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "sign",
            "name": "test-cert",
            "export_file": "test_csr.pem",
            "common_name": "test.example.com",
            "country": "US",
            "province": "CA",
            "locality": "SF",
            "organization": "Test Org",
            "org_unit": "IT",
            "email": "test@example.com",
            "key_size": None,
            "certificate": None,
            "intermediate_cert": None,
            "key": None,
            "passphrase": None,
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": ["alt.example.com"],
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade with CSR API support
        mock_blade = Mock()
        mock_version = Mock()
        mock_version.version = "2.20"
        mock_blade.get_versions.return_value.items = ["2.20"]

        # Certificate exists
        mock_blade.get_certificates.return_value.status_code = 200

        # Mock CSR response
        mock_csr_item = Mock()
        mock_csr_item.certificate_signing_request = [
            "-----BEGIN CERTIFICATE REQUEST-----\n",
            "CSR_CONTENT\n",
            "-----END CERTIFICATE REQUEST-----\n",
        ]
        mock_csr_response = Mock()
        mock_csr_response.items = [mock_csr_item]
        mock_blade.post_certificates_certificate_signing_requests.return_value = (
            mock_csr_response
        )

        mock_get_system.return_value = mock_blade

        # Mock pycountry
        mock_country = Mock()
        mock_pycountry.countries.get.return_value = mock_country

        # Mock file operations
        with patch("builtins.open", create=True) as mock_open:
            mock_file = Mock()
            mock_open.return_value.__enter__.return_value = mock_file

            # Call main
            try:
                main()
            except SystemExit:
                pass

            # Verify file was written
            mock_open.assert_called_once_with("test_csr.pem", "w", encoding="utf-8")
            mock_file.writelines.assert_called_once()

        # Verify exit_json was called with changed=True
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_create_csr_old_api_fails(self, mock_ansible_module, mock_get_system):
        """Test create_csr fails on old API version"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "sign",
            "name": "test-cert",
            "export_file": "test_csr.pem",
            "common_name": None,
            "country": None,
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": None,
            "certificate": None,
            "intermediate_cert": None,
            "key": None,
            "passphrase": None,
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade with old API version
        mock_blade = Mock()
        mock_version = Mock()
        mock_version.version = "2.15"
        mock_blade.get_versions.return_value.items = ["2.15"]

        mock_get_system.return_value = mock_blade

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "Purity//FB 4.6.3+ is required for CSRs" in call_args["msg"]

    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_invalid_email(self, mock_ansible_module, mock_get_system):
        """Test validation fails for invalid email"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "sign",
            "name": "test-cert",
            "export_file": "test_csr.pem",
            "common_name": "test.example.com",
            "country": None,
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": "invalid-email",  # Invalid email
            "key_size": None,
            "certificate": None,
            "intermediate_cert": None,
            "key": None,
            "passphrase": None,
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        mock_get_system.return_value = mock_blade

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "is not valid" in call_args["msg"]

    @patch("plugins.modules.purefb_certs.pycountry")
    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_invalid_country_length(
        self, mock_ansible_module, mock_get_system, mock_pycountry
    ):
        """Test validation fails for invalid country code length"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "sign",
            "name": "test-cert",
            "export_file": "test_csr.pem",
            "common_name": "test.example.com",
            "country": "USA",  # Invalid - should be 2 letters
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": None,
            "certificate": None,
            "intermediate_cert": None,
            "key": None,
            "passphrase": None,
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        mock_get_system.return_value = mock_blade

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "two-letter country" in call_args["msg"]

    @patch("plugins.modules.purefb_certs.pycountry")
    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_invalid_country_code(
        self, mock_ansible_module, mock_get_system, mock_pycountry
    ):
        """Test validation fails for invalid country code"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "sign",
            "name": "test-cert",
            "export_file": "test_csr.pem",
            "common_name": "test.example.com",
            "country": "XX",  # Invalid ISO code
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": None,
            "certificate": None,
            "intermediate_cert": None,
            "key": None,
            "passphrase": None,
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        mock_get_system.return_value = mock_blade

        # Mock pycountry - return None for invalid code
        mock_pycountry.countries.get.return_value = None

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "ISO 3166-1 code" in call_args["msg"]

    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", False)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_no_pypureclient(self, mock_ansible_module):
        """Test module fails when pypureclient is not installed"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "present",
            "name": "test-cert",
            "export_file": None,
            "common_name": None,
            "country": None,
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": None,
            "certificate": "cert_content",
            "intermediate_cert": None,
            "key": "key_content",
            "passphrase": None,
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "py-pure-client sdk is required" in call_args["msg"]

    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", False)
    def test_main_no_pycountry(self, mock_ansible_module):
        """Test module fails when pycountry is not installed"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "present",
            "name": "test-cert",
            "export_file": None,
            "common_name": None,
            "country": None,
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": None,
            "certificate": "cert_content",
            "intermediate_cert": None,
            "key": "key_content",
            "passphrase": None,
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "pycountry sdk is required" in call_args["msg"]

    @patch("plugins.modules.purefb_certs.pycountry")
    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_update_cert_old_api(
        self, mock_ansible_module, mock_get_system, mock_pycountry
    ):
        """Test update_cert with old API version (< 2.20)"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "present",
            "name": "existing-cert",
            "export_file": None,
            "common_name": None,
            "country": None,
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": None,
            "certificate": "new_cert_content",
            "intermediate_cert": "intermediate_content",
            "key": "new_key_content",
            "passphrase": "secret",
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade with old API version
        mock_blade = Mock()
        mock_version = Mock()
        mock_version.version = "2.15"
        mock_blade.get_versions.return_value.items = ["2.15"]

        # Certificate exists
        mock_blade.get_certificates.return_value.status_code = 200

        # Mock successful patch
        mock_patch_response = Mock()
        mock_patch_response.status_code = 200
        mock_blade.patch_certificates.return_value = mock_patch_response

        mock_get_system.return_value = mock_blade

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify patch_certificates was called (old API path)
        mock_blade.patch_certificates.assert_called_once()
        call_args = mock_blade.patch_certificates.call_args

        # Verify it was called with basic parameters (not extended)
        assert "names" in call_args[1]
        assert "certificate" in call_args[1]
        # Should NOT have generate_new_key (that's only for new API)
        assert "generate_new_key" not in call_args[1]

        # Verify exit_json was called with changed=True
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_certs.pycountry")
    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_update_cert_new_api(
        self, mock_ansible_module, mock_get_system, mock_pycountry
    ):
        """Test update_cert with new API version (>= 2.20)"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "present",
            "name": "existing-cert",
            "export_file": None,
            "common_name": "test.example.com",
            "country": "US",
            "province": "CA",
            "locality": "SF",
            "organization": "Test Org",
            "org_unit": "IT",
            "email": "test@example.com",
            "key_size": 2048,
            "certificate": "new_cert_content",
            "intermediate_cert": "intermediate_content",
            "key": "new_key_content",
            "passphrase": "secret",
            "generate": True,
            "days": 365,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": ["alt.example.com"],
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade with new API version
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]

        # Certificate exists
        mock_blade.get_certificates.return_value.status_code = 200

        # Mock successful patch
        mock_patch_response = Mock()
        mock_patch_response.status_code = 200
        mock_blade.patch_certificates.return_value = mock_patch_response

        mock_get_system.return_value = mock_blade

        # Mock pycountry
        mock_country = Mock()
        mock_pycountry.countries.get.return_value = mock_country

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify patch_certificates was called (new API path)
        mock_blade.patch_certificates.assert_called_once()
        call_args = mock_blade.patch_certificates.call_args

        # Verify it was called with extended parameters
        assert "names" in call_args[1]
        assert "certificate" in call_args[1]
        assert "generate_new_key" in call_args[1]
        assert call_args[1]["generate_new_key"] == "True"

        # Verify exit_json was called with changed=True
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True

    @patch("plugins.modules.purefb_certs.pycountry")
    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_update_cert_new_api_fails(
        self, mock_ansible_module, mock_get_system, mock_pycountry
    ):
        """Test update_cert fails with new API version"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "present",
            "name": "existing-cert",
            "export_file": None,
            "common_name": "test.example.com",
            "country": "US",
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": "test@example.com",
            "key_size": None,
            "certificate": "new_cert_content",
            "intermediate_cert": None,
            "key": "new_key_content",
            "passphrase": None,
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade with new API version
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]

        # Certificate exists
        mock_blade.get_certificates.return_value.status_code = 200

        # Mock failed patch
        mock_patch_response = Mock()
        mock_patch_response.status_code = 400
        mock_error = Mock()
        mock_error.message = "Invalid certificate"
        mock_patch_response.errors = [mock_error]
        mock_blade.patch_certificates.return_value = mock_patch_response

        mock_get_system.return_value = mock_blade

        # Mock pycountry
        mock_country = Mock()
        mock_pycountry.countries.get.return_value = mock_country

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "Updating existing SSL certificate" in call_args["msg"]

    @patch("plugins.modules.purefb_certs.pycountry")
    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_update_cert_old_api_fails(
        self, mock_ansible_module, mock_get_system, mock_pycountry
    ):
        """Test update_cert fails with old API version"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "present",
            "name": "existing-cert",
            "export_file": None,
            "common_name": None,
            "country": None,
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": None,
            "certificate": "new_cert_content",
            "intermediate_cert": None,
            "key": "new_key_content",
            "passphrase": None,
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade with old API version
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.15"]

        # Certificate exists
        mock_blade.get_certificates.return_value.status_code = 200

        # Mock failed patch
        mock_patch_response = Mock()
        mock_patch_response.status_code = 400
        mock_error = Mock()
        mock_error.message = "Invalid certificate"
        mock_patch_response.errors = [mock_error]
        mock_blade.patch_certificates.return_value = mock_patch_response

        mock_get_system.return_value = mock_blade

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "Updating existing SSL certificate" in call_args["msg"]

    @patch("plugins.modules.purefb_certs.pycountry")
    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_create_cert_new_api_fails(
        self, mock_ansible_module, mock_get_system, mock_pycountry
    ):
        """Test create_cert fails with new API version"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "present",
            "name": "new-cert",
            "export_file": None,
            "common_name": "test.example.com",
            "country": "US",
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": "test@example.com",
            "key_size": None,
            "certificate": "cert_content",
            "intermediate_cert": None,
            "key": "key_content",
            "passphrase": None,
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": "self-signed",
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade with new API version
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]

        # Certificate doesn't exist
        mock_blade.get_certificates.return_value.status_code = 400

        # Mock failed post
        mock_post_response = Mock()
        mock_post_response.status_code = 400
        mock_error = Mock()
        mock_error.message = "Invalid certificate"
        mock_post_response.errors = [mock_error]
        mock_blade.post_certificates.return_value = mock_post_response

        mock_get_system.return_value = mock_blade

        # Mock pycountry
        mock_country = Mock()
        mock_pycountry.countries.get.return_value = mock_country

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "Creating SSL certificate" in call_args["msg"]

    @patch("plugins.modules.purefb_certs.pycountry")
    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_delete_cert_fails(
        self, mock_ansible_module, mock_get_system, mock_pycountry
    ):
        """Test delete_cert fails"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "absent",
            "name": "test-cert",
            "export_file": None,
            "common_name": None,
            "country": None,
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": None,
            "certificate": None,
            "intermediate_cert": None,
            "key": None,
            "passphrase": None,
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.15"]

        # Certificate exists
        mock_blade.get_certificates.return_value.status_code = 200

        # Mock failed delete
        mock_delete_response = Mock()
        mock_delete_response.status_code = 400
        mock_error = Mock()
        mock_error.message = "Cannot delete certificate"
        mock_delete_response.errors = [mock_error]
        mock_blade.delete_certificates.return_value = mock_delete_response

        mock_get_system.return_value = mock_blade

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "Failed to delete" in call_args["msg"]

    @patch("plugins.modules.purefb_certs.pycountry")
    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_reimport_external_cert_fails(
        self, mock_ansible_module, mock_get_system, mock_pycountry
    ):
        """Test reimporting external certificate fails"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "import",
            "name": "external-cert",
            "export_file": None,
            "common_name": None,
            "country": None,
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": None,
            "certificate": "cert_content",
            "intermediate_cert": None,
            "key": None,  # No key - will be auto-detected as external
            "passphrase": None,
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.15"]

        # Certificate exists and is external
        mock_cert_response = Mock()
        mock_cert_response.status_code = 200
        mock_cert = Mock()
        mock_cert.certificate_type = "external"
        mock_cert_response.items = [mock_cert]
        mock_blade.get_certificates.return_value = mock_cert_response

        mock_get_system.return_value = mock_blade

        # Call main - expect failure
        try:
            main()
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "External Certificates cannot be reimported" in call_args["msg"]

    @patch("plugins.modules.purefb_certs.pycountry")
    @patch("plugins.modules.purefb_certs.get_system")
    @patch("plugins.modules.purefb_certs.AnsibleModule")
    @patch("plugins.modules.purefb_certs.HAS_PYPURECLIENT", True)
    @patch("plugins.modules.purefb_certs.HAS_PYCOUNTRY", True)
    def test_main_import_existing_cert(
        self, mock_ansible_module, mock_get_system, mock_pycountry
    ):
        """Test importing over existing non-external certificate"""
        # Setup mock module
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "state": "import",
            "name": "existing-cert",
            "export_file": None,
            "common_name": None,
            "country": None,
            "province": None,
            "locality": None,
            "organization": None,
            "org_unit": None,
            "email": None,
            "key_size": None,
            "certificate": "cert_content",
            "intermediate_cert": None,
            "key": "key_content",
            "passphrase": None,
            "generate": False,
            "days": None,
            "key_algorithm": None,
            "certificate_type": None,
            "subject_alternative_names": None,
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        # Mock blade
        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.15"]

        # Certificate exists and is NOT external
        mock_cert_response = Mock()
        mock_cert_response.status_code = 200
        mock_cert = Mock()
        mock_cert.certificate_type = "self-signed"
        mock_cert_response.items = [mock_cert]
        mock_blade.get_certificates.return_value = mock_cert_response

        # Mock successful patch
        mock_patch_response = Mock()
        mock_patch_response.status_code = 200
        mock_blade.patch_certificates.return_value = mock_patch_response

        mock_get_system.return_value = mock_blade

        # Call main
        try:
            main()
        except SystemExit:
            pass

        # Verify patch_certificates was called (update path)
        mock_blade.patch_certificates.assert_called_once()

        # Verify exit_json was called with changed=True
        mock_module.exit_json.assert_called_once()
        call_args = mock_module.exit_json.call_args[1]
        assert call_args["changed"] is True
