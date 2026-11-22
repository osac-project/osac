# Copyright: (c) 2026, Pure Storage Ansible Team <pure-ansible-team@purestorage.com>
# GNU General Public License v3.0+ (see COPYING.GPLv3 or https://www.gnu.org/licenses/gpl-3.0.txt)

"""Shared pytest fixtures for FlashBlade Collection tests."""

from __future__ import absolute_import, division, print_function

__metaclass__ = type

import pytest
from unittest.mock import Mock, MagicMock


@pytest.fixture
def mock_module():
    """Create a mock Ansible module with common parameters.

    Returns:
        Mock: Mock AnsibleModule instance with default parameters
    """
    module = Mock()
    module.params = {
        "fb_url": "flashblade.example.com",
        "api_token": "T-test-token-12345",
        "disable_warnings": True,
        "state": "present",
    }
    module.check_mode = False
    module.fail_json = Mock(side_effect=Exception("fail_json called"))
    module.exit_json = Mock(side_effect=Exception("exit_json called"))
    return module


@pytest.fixture
def mock_blade():
    """Create a mock FlashBlade client.

    Returns:
        Mock: Mock FlashBlade client with common API methods
    """
    blade = MagicMock()

    # Mock API version
    blade.get_versions.return_value.items = ["2.10"]

    # Mock successful responses by default
    mock_response = Mock()
    mock_response.status_code = 200
    mock_response.errors = []

    # Set default return values for common API calls
    blade.get_file_systems.return_value = mock_response
    blade.post_file_systems.return_value = mock_response
    blade.patch_file_systems.return_value = mock_response
    blade.delete_file_systems.return_value = mock_response

    blade.get_buckets.return_value = mock_response
    blade.post_buckets.return_value = mock_response
    blade.patch_buckets.return_value = mock_response
    blade.delete_buckets.return_value = mock_response

    return blade


@pytest.fixture
def mock_error_response():
    """Create a mock error response from FlashBlade API.

    Returns:
        Mock: Mock response with error
    """
    response = Mock()
    response.status_code = 400
    error = Mock()
    error.message = "Test error message"
    response.errors = [error]
    return response


@pytest.fixture
def mock_empty_error_response():
    """Create a mock error response with empty errors list.

    Returns:
        Mock: Mock response with empty errors
    """
    response = Mock()
    response.status_code = 400
    response.errors = []
    return response


@pytest.fixture
def mock_filesystem():
    """Create a mock filesystem object.

    Returns:
        Mock: Mock filesystem with common attributes
    """
    fs = Mock()
    fs.name = "test-fs"
    fs.provisioned = 107374182400  # 100GB in bytes
    fs.destroyed = False
    fs.nfs = Mock()
    fs.nfs.v3_enabled = True
    fs.nfs.v4_1_enabled = True
    fs.smb = Mock()
    fs.smb.enabled = False
    fs.http = Mock()
    fs.http.enabled = False
    fs.snapshot_directory_enabled = False
    return fs


@pytest.fixture
def mock_bucket():
    """Create a mock S3 bucket object.

    Returns:
        Mock: Mock bucket with common attributes
    """
    bucket = Mock()
    bucket.name = "test-bucket"
    bucket.account = Mock()
    bucket.account.name = "test-account"
    bucket.destroyed = False
    bucket.versioning = "none"
    bucket.object_lock_config = Mock()
    bucket.object_lock_config.enabled = False
    return bucket


@pytest.fixture
def mock_api_exception():
    """Create a mock API exception.

    Returns:
        Exception: Mock exception for API errors
    """
    exception = Exception("API call failed")
    exception.status = 400
    exception.reason = "Bad Request"
    return exception
