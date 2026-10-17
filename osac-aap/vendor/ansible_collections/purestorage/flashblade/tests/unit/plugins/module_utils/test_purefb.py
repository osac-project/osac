# Copyright: (c) 2026, Pure Storage Ansible Team <pure-ansible-team@purestorage.com>
# GNU General Public License v3.0+ (see COPYING.GPLv3 or https://www.gnu.org/licenses/gpl-3.0.txt)

"""Unit tests for purefb module utilities."""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

import sys
from unittest.mock import Mock, patch, MagicMock

# Mock external dependencies before importing purefb
sys.modules["pypureclient"] = MagicMock()
sys.modules["pypureclient.flashblade"] = MagicMock()
sys.modules["urllib3"] = MagicMock()
sys.modules["distro"] = MagicMock()

from plugins.module_utils.purefb import get_system, purefb_argument_spec


class TestPurefbArgumentSpec:
    """Tests for purefb_argument_spec function."""

    def test_returns_dict(self):
        """Test that function returns a dictionary."""
        result = purefb_argument_spec()
        assert isinstance(result, dict)

    def test_contains_fb_url(self):
        """Test that spec contains fb_url."""
        result = purefb_argument_spec()
        assert "fb_url" in result
        assert result["fb_url"] == {}

    def test_contains_api_token(self):
        """Test that spec contains api_token with no_log."""
        result = purefb_argument_spec()
        assert "api_token" in result
        assert result["api_token"]["no_log"] is True

    def test_contains_disable_warnings(self):
        """Test that spec contains disable_warnings with defaults."""
        result = purefb_argument_spec()
        assert "disable_warnings" in result
        assert result["disable_warnings"]["type"] == "bool"
        assert result["disable_warnings"]["default"] is False

    def test_all_expected_keys(self):
        """Test that spec contains exactly the expected keys."""
        result = purefb_argument_spec()
        expected_keys = {
            "fb_url",
            "api_token",
            "id_token",
            "private_key_file",
            "private_key_password",
            "username",
            "client_id",
            "key_id",
            "issuer",
            "disable_warnings",
        }
        assert set(result.keys()) == expected_keys

    def test_token_auth_secrets_no_log(self):
        """Secret token-auth params must be marked no_log."""
        result = purefb_argument_spec()
        assert result["id_token"]["no_log"] is True
        assert result["private_key_password"]["no_log"] is True

    def test_non_secret_params_no_log_false(self):
        """Non-secret params that trip the no_log heuristic are silenced."""
        result = purefb_argument_spec()
        assert result["private_key_file"]["no_log"] is False
        assert result["key_id"]["no_log"] is False


class TestGetSystem:
    """Tests for get_system function."""

    @patch("plugins.module_utils.purefb.flashblade.Client")
    @patch("plugins.module_utils.purefb.HAS_PYPURECLIENT", True)
    def test_with_module_params(self, mock_client_class):
        """Test get_system with module parameters."""
        # Setup mock module
        mock_module = Mock()
        mock_module.params = {
            "fb_url": "https://flashblade.example.com",
            "api_token": "test-token-123",
            "disable_warnings": False,
        }

        # Setup mock FlashBlade client
        mock_client = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_client.get_hardware.return_value = mock_response
        mock_client_class.return_value = mock_client

        # Call function
        result = get_system(mock_module)

        # Verify client was created with correct params
        mock_client_class.assert_called_once()
        call_kwargs = mock_client_class.call_args[1]
        assert call_kwargs["target"] == "https://flashblade.example.com"
        assert call_kwargs["api_token"] == "test-token-123"
        assert "user_agent" in call_kwargs

        # Verify hardware check was called
        mock_client.get_hardware.assert_called_once()

        # Verify result is the client
        assert result == mock_client

    @patch("plugins.module_utils.purefb.flashblade.Client")
    @patch("plugins.module_utils.purefb.HAS_PYPURECLIENT", True)
    @patch("plugins.module_utils.purefb.environ")
    def test_with_environment_vars(self, mock_environ, mock_client_class):
        """Test get_system with environment variables."""
        # Setup mock module without params
        mock_module = Mock()
        mock_module.params = {
            "fb_url": None,
            "api_token": None,
            "disable_warnings": False,
        }

        # Setup environment variables
        env_vars = {
            "PUREFB_URL": "https://env-flashblade.example.com",
            "PUREFB_API": "env-token-456",
        }
        mock_environ.get.side_effect = env_vars.get

        # Setup mock FlashBlade client
        mock_client = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_client.get_hardware.return_value = mock_response
        mock_client_class.return_value = mock_client

        # Call function
        result = get_system(mock_module)

        # Verify client was created with env vars
        mock_client_class.assert_called_once()
        call_kwargs = mock_client_class.call_args[1]
        assert call_kwargs["target"] == "https://env-flashblade.example.com"
        assert call_kwargs["api_token"] == "env-token-456"

        # Verify result is the client
        assert result == mock_client

    @patch("plugins.module_utils.purefb.HAS_PYPURECLIENT", True)
    @patch("plugins.module_utils.purefb.environ")
    def test_missing_credentials(self, mock_environ):
        """Test that missing credentials causes failure."""
        # Setup mock module without params
        mock_module = Mock()
        mock_module.params = {
            "fb_url": None,
            "api_token": None,
            "disable_warnings": False,
        }
        # Make fail_json raise an exception to stop execution
        mock_module.fail_json.side_effect = SystemExit("fail_json called")

        # No environment variables
        mock_environ.get.return_value = None

        # Call function - should fail
        try:
            get_system(mock_module)
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "PUREFB_URL" in call_args["msg"]
        assert "PUREFB_API" in call_args["msg"]

    @patch("plugins.module_utils.purefb.HAS_PYPURECLIENT", False)
    def test_missing_pypureclient(self):
        """Test that missing pypureclient causes failure."""
        mock_module = Mock()
        mock_module.params = {
            "fb_url": "https://flashblade.example.com",
            "api_token": "test-token",
            "disable_warnings": False,
        }
        # Make fail_json raise an exception to stop execution
        mock_module.fail_json.side_effect = SystemExit("fail_json called")

        # Call function - should fail
        try:
            get_system(mock_module)
        except SystemExit:
            pass

        # Verify fail_json was called
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "pypureclient SDK not installed" in call_args["msg"]

    @patch("plugins.module_utils.purefb.flashblade.Client")
    @patch("plugins.module_utils.purefb.HAS_PYPURECLIENT", True)
    def test_authentication_failure(self, mock_client_class):
        """Test that authentication failure is handled."""
        # Setup mock module
        mock_module = Mock()
        mock_module.params = {
            "fb_url": "https://flashblade.example.com",
            "api_token": "invalid-token",
            "disable_warnings": False,
        }
        # Make fail_json raise an exception to stop execution
        mock_module.fail_json.side_effect = SystemExit("fail_json called")

        # Setup mock FlashBlade client with auth failure
        mock_client = Mock()
        mock_response = Mock()
        mock_response.status_code = 401
        mock_error = Mock()
        mock_error.message = "Invalid API token"
        mock_response.errors = [mock_error]
        mock_client.get_hardware.return_value = mock_response
        mock_client_class.return_value = mock_client

        # Call function - should fail
        try:
            get_system(mock_module)
        except SystemExit:
            pass

        # Verify fail_json was called with auth error
        mock_module.fail_json.assert_called_once()
        call_args = mock_module.fail_json.call_args[1]
        assert "authentication failed" in call_args["msg"].lower()

    @patch("plugins.module_utils.purefb.urllib3.disable_warnings")
    @patch("plugins.module_utils.purefb.flashblade.Client")
    @patch("plugins.module_utils.purefb.HAS_PYPURECLIENT", True)
    def test_disable_warnings(self, mock_client_class, mock_disable_warnings):
        """Test that warnings can be disabled."""
        # Setup mock module with disable_warnings=True
        mock_module = Mock()
        mock_module.params = {
            "fb_url": "https://flashblade.example.com",
            "api_token": "test-token",
            "disable_warnings": True,
        }

        # Setup mock FlashBlade client
        mock_client = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_client.get_hardware.return_value = mock_response
        mock_client_class.return_value = mock_client

        # Call function
        get_system(mock_module)

        # Verify urllib3.disable_warnings was called
        mock_disable_warnings.assert_called_once()

    @patch("plugins.module_utils.purefb.distro.name")
    @patch("plugins.module_utils.purefb.flashblade.Client")
    @patch("plugins.module_utils.purefb.HAS_PYPURECLIENT", True)
    @patch("plugins.module_utils.purefb.HAS_DISTRO", True)
    def test_user_agent_with_distro(self, mock_client_class, mock_distro_name):
        """Test user agent string includes distro info when available."""
        # Setup mock module
        mock_module = Mock()
        mock_module.params = {
            "fb_url": "https://flashblade.example.com",
            "api_token": "test-token",
            "disable_warnings": False,
        }

        # Setup mock distro
        mock_distro_name.return_value = "Ubuntu 22.04"

        # Setup mock FlashBlade client
        mock_client = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_client.get_hardware.return_value = mock_response
        mock_client_class.return_value = mock_client

        # Call function
        get_system(mock_module)

        # Verify user_agent contains distro info
        call_kwargs = mock_client_class.call_args[1]
        assert "Ubuntu 22.04" in call_kwargs["user_agent"]
        assert "Ansible" in call_kwargs["user_agent"]

    @patch("plugins.module_utils.purefb.flashblade.Client")
    @patch("plugins.module_utils.purefb.HAS_PYPURECLIENT", True)
    @patch("plugins.module_utils.purefb.environ")
    def test_with_id_token(self, mock_environ, mock_client_class):
        """Test get_system authenticates with a pre-signed id_token."""
        mock_environ.get.return_value = None
        mock_module = Mock()
        mock_module.params = {
            "fb_url": "https://flashblade.example.com",
            "api_token": None,
            "id_token": "eyJhbGciOiJSUzI1NiJ9.payload.sig",
            "disable_warnings": False,
        }
        mock_client = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_client.get_hardware.return_value = mock_response
        mock_client_class.return_value = mock_client

        result = get_system(mock_module)

        call_kwargs = mock_client_class.call_args[1]
        assert call_kwargs["target"] == "https://flashblade.example.com"
        assert call_kwargs["id_token"] == "eyJhbGciOiJSUzI1NiJ9.payload.sig"
        assert "api_token" not in call_kwargs
        assert result == mock_client

    @patch("plugins.module_utils.purefb.flashblade.Client")
    @patch("plugins.module_utils.purefb.HAS_PYPURECLIENT", True)
    @patch("plugins.module_utils.purefb.environ")
    def test_with_private_key(self, mock_environ, mock_client_class):
        """Test get_system authenticates with the private-key app flow."""
        mock_environ.get.return_value = None
        mock_module = Mock()
        mock_module.params = {
            "fb_url": "https://flashblade.example.com",
            "api_token": None,
            "id_token": None,
            "private_key_file": "/run/secrets/aap.pem",
            "private_key_password": "s3cret",
            "username": "automation",
            "client_id": "cid-123",
            "key_id": "kid-456",
            "issuer": "AAP",
            "disable_warnings": False,
        }
        mock_client = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_client.get_hardware.return_value = mock_response
        mock_client_class.return_value = mock_client

        result = get_system(mock_module)

        call_kwargs = mock_client_class.call_args[1]
        assert call_kwargs["target"] == "https://flashblade.example.com"
        assert call_kwargs["private_key_file"] == "/run/secrets/aap.pem"
        assert call_kwargs["private_key_password"] == "s3cret"
        assert call_kwargs["username"] == "automation"
        assert call_kwargs["client_id"] == "cid-123"
        assert call_kwargs["key_id"] == "kid-456"
        assert call_kwargs["issuer"] == "AAP"
        assert "api_token" not in call_kwargs
        assert result == mock_client

    @patch("plugins.module_utils.purefb.flashblade.Client")
    @patch("plugins.module_utils.purefb.HAS_PYPURECLIENT", True)
    @patch("plugins.module_utils.purefb.environ")
    def test_incomplete_private_key_set_fails(self, mock_environ, mock_client_class):
        """An incomplete private-key set (no username) must fail cleanly."""
        mock_module = Mock()
        mock_module.params = {
            "fb_url": "https://flashblade.example.com",
            "api_token": None,
            "id_token": None,
            "private_key_file": "/run/secrets/aap.pem",
            "private_key_password": None,
            "username": None,  # missing -> not a complete set
            "client_id": "cid-123",
            "key_id": "kid-456",
            "issuer": "AAP",
            "disable_warnings": False,
        }
        mock_module.fail_json.side_effect = SystemExit("fail_json called")
        mock_environ.get.return_value = None

        try:
            get_system(mock_module)
        except SystemExit:
            pass

        mock_module.fail_json.assert_called_once()
        mock_client_class.assert_not_called()
