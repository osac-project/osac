# Copyright: (c) 2026, Pure Storage Ansible Team <pure-ansible-team@purestorage.com>
# GNU General Public License v3.0+ (see COPYING.GPLv3 or https://www.gnu.org/licenses/gpl-3.0.txt)

"""Unit tests for purefb_s3acc_export module."""

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

from plugins.modules.purefb_s3acc_export import (
    main,
    _get_export,
)


class TestPurefbS3accExport:
    """Test cases for purefb_s3acc_export module"""

    # ---------------- _get_export tests ----------------

    def test_get_export_returns_first_item(self):
        """Test _get_export returns the first export when present"""
        mock_module = Mock()
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "context": "",
        }

        existing_export = Mock()
        existing_export.name = "acme/fb-01"

        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_response.items = [existing_export]
        mock_blade.get_object_store_account_exports.return_value = mock_response

        out = _get_export(mock_module, mock_blade)

        assert out is not None
        mock_blade.get_object_store_account_exports.assert_called_once_with(
            filter="member.name='acme' and server.name='fb-01'"
        )

    def test_get_export_passes_context_when_set(self):
        """Test _get_export forwards context_names when context is set"""
        mock_module = Mock()
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "context": "fleet1",
        }

        existing_export = Mock()
        existing_export.name = "acme/fb-01"

        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_response.items = [existing_export]
        mock_blade.get_object_store_account_exports.return_value = mock_response

        _get_export(mock_module, mock_blade)

        mock_blade.get_object_store_account_exports.assert_called_once_with(
            filter="member.name='acme' and server.name='fb-01'",
            context_names=["fleet1"],
        )

    def test_get_export_returns_none_on_error(self):
        """Test _get_export returns None when the SDK reports an error"""
        mock_module = Mock()
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "context": "",
        }

        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 400
        mock_response.items = []
        mock_blade.get_object_store_account_exports.return_value = mock_response

        out = _get_export(mock_module, mock_blade)

        assert out is None

    def test_get_export_returns_none_when_no_items(self):
        """Test _get_export returns None when no export matches the filter"""
        mock_module = Mock()
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "context": "",
        }

        mock_blade = Mock()
        mock_response = Mock()
        mock_response.status_code = 200
        mock_response.items = []
        mock_blade.get_object_store_account_exports.return_value = mock_response

        out = _get_export(mock_module, mock_blade)

        assert out is None

    # ---------------- main() create flows ----------------

    @patch("plugins.modules.purefb_s3acc_export.LooseVersion")
    @patch("plugins.modules.purefb_s3acc_export.Reference")
    @patch("plugins.modules.purefb_s3acc_export.ObjectStoreAccountExportPost")
    @patch("plugins.modules.purefb_s3acc_export.get_system")
    @patch("plugins.modules.purefb_s3acc_export.AnsibleModule")
    @patch("plugins.modules.purefb_s3acc_export.HAS_PYPURECLIENT", True)
    def test_main_creates_export(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_post_cls,
        mock_reference_cls,
        mock_loose_version,
    ):
        """Test creating a new account export"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "policy": "acme-export",
            "enabled": True,
            "state": "present",
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        # Export does not exist
        get_response = Mock()
        get_response.status_code = 200
        get_response.items = []
        mock_blade.get_object_store_account_exports.return_value = get_response

        # Mock successful create
        post_response = Mock()
        post_response.status_code = 200
        mock_blade.post_object_store_account_exports.return_value = post_response

        try:
            main()
        except SystemExit:
            pass

        mock_blade.post_object_store_account_exports.assert_called_once()
        call_kwargs = mock_blade.post_object_store_account_exports.call_args[1]
        assert call_kwargs["member_names"] == ["acme"]
        assert call_kwargs["policy_names"] == ["acme-export"]

        mock_module.exit_json.assert_called_once()
        assert mock_module.exit_json.call_args[1]["changed"] is True

    @patch("plugins.modules.purefb_s3acc_export.LooseVersion")
    @patch("plugins.modules.purefb_s3acc_export.Reference")
    @patch("plugins.modules.purefb_s3acc_export.ObjectStoreAccountExportPost")
    @patch("plugins.modules.purefb_s3acc_export.get_system")
    @patch("plugins.modules.purefb_s3acc_export.AnsibleModule")
    @patch("plugins.modules.purefb_s3acc_export.HAS_PYPURECLIENT", True)
    def test_main_create_check_mode_skips_post(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_post_cls,
        mock_reference_cls,
        mock_loose_version,
    ):
        """Test create reports changed but skips POST in check mode"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "policy": "acme-export",
            "enabled": True,
            "state": "present",
            "context": "",
        }
        mock_module.check_mode = True
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        get_response = Mock()
        get_response.status_code = 200
        get_response.items = []
        mock_blade.get_object_store_account_exports.return_value = get_response

        try:
            main()
        except SystemExit:
            pass

        mock_blade.post_object_store_account_exports.assert_not_called()
        assert mock_module.exit_json.call_args[1]["changed"] is True

    @patch("plugins.modules.purefb_s3acc_export.LooseVersion")
    @patch("plugins.modules.purefb_s3acc_export.get_system")
    @patch("plugins.modules.purefb_s3acc_export.AnsibleModule")
    @patch("plugins.modules.purefb_s3acc_export.HAS_PYPURECLIENT", True)
    def test_main_create_without_policy_fails(
        self, mock_ansible_module, mock_get_system, mock_loose_version
    ):
        """Test create fails when policy is not supplied"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "policy": None,
            "enabled": None,
            "state": "present",
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        get_response = Mock()
        get_response.status_code = 200
        get_response.items = []
        mock_blade.get_object_store_account_exports.return_value = get_response

        try:
            main()
        except SystemExit:
            pass

        mock_module.fail_json.assert_called_once()
        assert "policy is required" in mock_module.fail_json.call_args[1]["msg"]

    @patch("plugins.modules.purefb_s3acc_export.LooseVersion")
    @patch("plugins.modules.purefb_s3acc_export.Reference")
    @patch("plugins.modules.purefb_s3acc_export.ObjectStoreAccountExportPost")
    @patch("plugins.modules.purefb_s3acc_export.get_system")
    @patch("plugins.modules.purefb_s3acc_export.AnsibleModule")
    @patch("plugins.modules.purefb_s3acc_export.HAS_PYPURECLIENT", True)
    def test_main_create_failure_calls_fail_json(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_post_cls,
        mock_reference_cls,
        mock_loose_version,
    ):
        """Test SDK error on create is surfaced via fail_json"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "policy": "acme-export",
            "enabled": True,
            "state": "present",
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        get_response = Mock()
        get_response.status_code = 200
        get_response.items = []
        mock_blade.get_object_store_account_exports.return_value = get_response

        post_response = Mock()
        post_response.status_code = 400
        post_err = Mock()
        post_err.message = "nope"
        post_response.errors = [post_err]
        mock_blade.post_object_store_account_exports.return_value = post_response

        try:
            main()
        except SystemExit:
            pass

        mock_module.fail_json.assert_called_once()
        assert (
            "Failed to create account export"
            in mock_module.fail_json.call_args[1]["msg"]
        )

    # ---------------- main() update flows ----------------

    @patch("plugins.modules.purefb_s3acc_export.LooseVersion")
    @patch("plugins.modules.purefb_s3acc_export.Reference")
    @patch("plugins.modules.purefb_s3acc_export.ObjectStoreAccountExportPatch")
    @patch("plugins.modules.purefb_s3acc_export.get_system")
    @patch("plugins.modules.purefb_s3acc_export.AnsibleModule")
    @patch("plugins.modules.purefb_s3acc_export.HAS_PYPURECLIENT", True)
    def test_main_updates_enabled_flag(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_patch_cls,
        mock_reference_cls,
        mock_loose_version,
    ):
        """Test updating the enabled flag on an existing export"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "policy": None,
            "enabled": False,
            "state": "present",
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        existing_export = Mock()
        existing_export.name = "acme/fb-01"
        existing_export.enabled = True
        existing_export.policy = Mock()
        existing_export.policy.name = "acme-export"

        get_response = Mock()
        get_response.status_code = 200
        get_response.items = [existing_export]
        mock_blade.get_object_store_account_exports.return_value = get_response

        patch_response = Mock()
        patch_response.status_code = 200
        mock_blade.patch_object_store_account_exports.return_value = patch_response

        try:
            main()
        except SystemExit:
            pass

        mock_blade.patch_object_store_account_exports.assert_called_once()
        call_kwargs = mock_blade.patch_object_store_account_exports.call_args[1]
        assert call_kwargs["names"] == ["acme/fb-01"]
        assert mock_module.exit_json.call_args[1]["changed"] is True

    @patch("plugins.modules.purefb_s3acc_export.LooseVersion")
    @patch("plugins.modules.purefb_s3acc_export.Reference")
    @patch("plugins.modules.purefb_s3acc_export.ObjectStoreAccountExportPatch")
    @patch("plugins.modules.purefb_s3acc_export.get_system")
    @patch("plugins.modules.purefb_s3acc_export.AnsibleModule")
    @patch("plugins.modules.purefb_s3acc_export.HAS_PYPURECLIENT", True)
    def test_main_updates_policy(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_patch_cls,
        mock_reference_cls,
        mock_loose_version,
    ):
        """Test re-pointing an export at a different policy"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "policy": "acme-export-v2",
            "enabled": None,
            "state": "present",
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        existing_export = Mock()
        existing_export.name = "acme/fb-01"
        existing_export.enabled = True
        existing_export.policy = Mock()
        existing_export.policy.name = "acme-export"

        get_response = Mock()
        get_response.status_code = 200
        get_response.items = [existing_export]
        mock_blade.get_object_store_account_exports.return_value = get_response

        patch_response = Mock()
        patch_response.status_code = 200
        mock_blade.patch_object_store_account_exports.return_value = patch_response

        try:
            main()
        except SystemExit:
            pass

        mock_blade.patch_object_store_account_exports.assert_called_once()
        assert mock_module.exit_json.call_args[1]["changed"] is True

    @patch("plugins.modules.purefb_s3acc_export.LooseVersion")
    @patch("plugins.modules.purefb_s3acc_export.get_system")
    @patch("plugins.modules.purefb_s3acc_export.AnsibleModule")
    @patch("plugins.modules.purefb_s3acc_export.HAS_PYPURECLIENT", True)
    def test_main_update_idempotent_when_matches(
        self, mock_ansible_module, mock_get_system, mock_loose_version
    ):
        """Test update is a no-op when desired state already matches"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "policy": "acme-export",
            "enabled": True,
            "state": "present",
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        existing_export = Mock()
        existing_export.name = "acme/fb-01"
        existing_export.enabled = True
        existing_export.policy = Mock()
        existing_export.policy.name = "acme-export"

        get_response = Mock()
        get_response.status_code = 200
        get_response.items = [existing_export]
        mock_blade.get_object_store_account_exports.return_value = get_response

        try:
            main()
        except SystemExit:
            pass

        mock_blade.patch_object_store_account_exports.assert_not_called()
        assert mock_module.exit_json.call_args[1]["changed"] is False

    @patch("plugins.modules.purefb_s3acc_export.LooseVersion")
    @patch("plugins.modules.purefb_s3acc_export.Reference")
    @patch("plugins.modules.purefb_s3acc_export.ObjectStoreAccountExportPatch")
    @patch("plugins.modules.purefb_s3acc_export.get_system")
    @patch("plugins.modules.purefb_s3acc_export.AnsibleModule")
    @patch("plugins.modules.purefb_s3acc_export.HAS_PYPURECLIENT", True)
    def test_main_update_failure_calls_fail_json(
        self,
        mock_ansible_module,
        mock_get_system,
        mock_patch_cls,
        mock_reference_cls,
        mock_loose_version,
    ):
        """Test SDK error on update is surfaced via fail_json"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "policy": None,
            "enabled": False,
            "state": "present",
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        existing_export = Mock()
        existing_export.name = "acme/fb-01"
        existing_export.enabled = True
        existing_export.policy = Mock()
        existing_export.policy.name = "acme-export"

        get_response = Mock()
        get_response.status_code = 200
        get_response.items = [existing_export]
        mock_blade.get_object_store_account_exports.return_value = get_response

        patch_response = Mock()
        patch_response.status_code = 400
        patch_err = Mock()
        patch_err.message = "nope"
        patch_response.errors = [patch_err]
        mock_blade.patch_object_store_account_exports.return_value = patch_response

        try:
            main()
        except SystemExit:
            pass

        mock_module.fail_json.assert_called_once()
        assert (
            "Failed to update account export"
            in mock_module.fail_json.call_args[1]["msg"]
        )

    # ---------------- main() delete flows ----------------

    @patch("plugins.modules.purefb_s3acc_export.LooseVersion")
    @patch("plugins.modules.purefb_s3acc_export.get_system")
    @patch("plugins.modules.purefb_s3acc_export.AnsibleModule")
    @patch("plugins.modules.purefb_s3acc_export.HAS_PYPURECLIENT", True)
    def test_main_deletes_export(
        self, mock_ansible_module, mock_get_system, mock_loose_version
    ):
        """Test deleting an existing export"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "policy": None,
            "enabled": None,
            "state": "absent",
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        existing_export = Mock()
        existing_export.name = "acme/fb-01"

        get_response = Mock()
        get_response.status_code = 200
        get_response.items = [existing_export]
        mock_blade.get_object_store_account_exports.return_value = get_response

        delete_response = Mock()
        delete_response.status_code = 200
        mock_blade.delete_object_store_account_exports.return_value = delete_response

        try:
            main()
        except SystemExit:
            pass

        mock_blade.delete_object_store_account_exports.assert_called_once()
        call_kwargs = mock_blade.delete_object_store_account_exports.call_args[1]
        assert call_kwargs["names"] == ["acme/fb-01"]
        assert mock_module.exit_json.call_args[1]["changed"] is True

    @patch("plugins.modules.purefb_s3acc_export.LooseVersion")
    @patch("plugins.modules.purefb_s3acc_export.get_system")
    @patch("plugins.modules.purefb_s3acc_export.AnsibleModule")
    @patch("plugins.modules.purefb_s3acc_export.HAS_PYPURECLIENT", True)
    def test_main_delete_idempotent_when_absent(
        self, mock_ansible_module, mock_get_system, mock_loose_version
    ):
        """Test delete is a no-op when the export does not exist"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "policy": None,
            "enabled": None,
            "state": "absent",
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        get_response = Mock()
        get_response.status_code = 200
        get_response.items = []
        mock_blade.get_object_store_account_exports.return_value = get_response

        try:
            main()
        except SystemExit:
            pass

        mock_blade.delete_object_store_account_exports.assert_not_called()
        assert mock_module.exit_json.call_args[1]["changed"] is False

    @patch("plugins.modules.purefb_s3acc_export.LooseVersion")
    @patch("plugins.modules.purefb_s3acc_export.get_system")
    @patch("plugins.modules.purefb_s3acc_export.AnsibleModule")
    @patch("plugins.modules.purefb_s3acc_export.HAS_PYPURECLIENT", True)
    def test_main_delete_check_mode_skips_delete(
        self, mock_ansible_module, mock_get_system, mock_loose_version
    ):
        """Test delete reports changed but skips DELETE in check mode"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "policy": None,
            "enabled": None,
            "state": "absent",
            "context": "",
        }
        mock_module.check_mode = True
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        existing_export = Mock()
        existing_export.name = "acme/fb-01"

        get_response = Mock()
        get_response.status_code = 200
        get_response.items = [existing_export]
        mock_blade.get_object_store_account_exports.return_value = get_response

        try:
            main()
        except SystemExit:
            pass

        mock_blade.delete_object_store_account_exports.assert_not_called()
        assert mock_module.exit_json.call_args[1]["changed"] is True

    @patch("plugins.modules.purefb_s3acc_export.LooseVersion")
    @patch("plugins.modules.purefb_s3acc_export.get_system")
    @patch("plugins.modules.purefb_s3acc_export.AnsibleModule")
    @patch("plugins.modules.purefb_s3acc_export.HAS_PYPURECLIENT", True)
    def test_main_delete_failure_calls_fail_json(
        self, mock_ansible_module, mock_get_system, mock_loose_version
    ):
        """Test SDK error on delete is surfaced via fail_json"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=False)
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "policy": None,
            "enabled": None,
            "state": "absent",
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.20"]
        local_array = Mock()
        local_array.name = "local-array"
        mock_blade.get_arrays.return_value.items = [local_array]
        mock_get_system.return_value = mock_blade

        existing_export = Mock()
        existing_export.name = "acme/fb-01"

        get_response = Mock()
        get_response.status_code = 200
        get_response.items = [existing_export]
        mock_blade.get_object_store_account_exports.return_value = get_response

        delete_response = Mock()
        delete_response.status_code = 400
        delete_err = Mock()
        delete_err.message = "nope"
        delete_response.errors = [delete_err]
        mock_blade.delete_object_store_account_exports.return_value = delete_response

        try:
            main()
        except SystemExit:
            pass

        mock_module.fail_json.assert_called_once()
        assert (
            "Failed to delete account export"
            in mock_module.fail_json.call_args[1]["msg"]
        )

    # ---------------- main() version gating ----------------

    @patch("plugins.modules.purefb_s3acc_export.LooseVersion")
    @patch("plugins.modules.purefb_s3acc_export.get_system")
    @patch("plugins.modules.purefb_s3acc_export.AnsibleModule")
    @patch("plugins.modules.purefb_s3acc_export.HAS_PYPURECLIENT", True)
    def test_main_fails_when_api_version_too_old(
        self, mock_ansible_module, mock_get_system, mock_loose_version
    ):
        """Test failure when API version is too old"""
        mock_loose_version.return_value.__gt__ = Mock(return_value=True)
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "policy": None,
            "enabled": None,
            "state": "present",
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        mock_blade = Mock()
        mock_blade.get_versions.return_value.items = ["2.10", "2.15", "2.19"]
        mock_get_system.return_value = mock_blade

        try:
            main()
        except SystemExit:
            pass

        mock_module.fail_json.assert_called_once()
        assert (
            "FlashBlade REST version not supported"
            in mock_module.fail_json.call_args[1]["msg"]
        )

    @patch("plugins.modules.purefb_s3acc_export.AnsibleModule")
    @patch("plugins.modules.purefb_s3acc_export.HAS_PYPURECLIENT", False)
    def test_main_fails_when_sdk_missing(self, mock_ansible_module):
        """Test failure when py-pure-client is not installed"""
        mock_module = Mock()
        mock_module.exit_json = Mock(side_effect=SystemExit)
        mock_module.fail_json = Mock(side_effect=SystemExit)
        mock_module.params = {
            "account": "acme",
            "server": "fb-01",
            "policy": None,
            "enabled": None,
            "state": "present",
            "context": "",
        }
        mock_module.check_mode = False
        mock_ansible_module.return_value = mock_module

        try:
            main()
        except SystemExit:
            pass

        mock_module.fail_json.assert_called_once()
        assert (
            "py-pure-client sdk is required"
            in mock_module.fail_json.call_args[1]["msg"]
        )
