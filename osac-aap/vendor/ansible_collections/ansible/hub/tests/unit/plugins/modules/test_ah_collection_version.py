import importlib.util
import pathlib
import sys
from unittest.mock import MagicMock

import pytest

# ah_collection.py imports ansible-core modules (AnsibleModule, etc.) at the top
# level. ansible-core is not installed in the unit test environment, so we mock
# those modules in sys.modules before loading the file. This lets us import and
# test pure helper functions like extract_version_from_path without needing a
# full Ansible installation.
for mock_module in [
    "ansible",
    "ansible.module_utils",
    "ansible.module_utils._text",
    "ansible.module_utils.basic",
    "plugins",
    "plugins.modules",
    "plugins.module_utils",
    "plugins.module_utils.ah_module",
]:
    sys.modules.setdefault(mock_module, MagicMock())

_module_path = pathlib.Path(__file__).resolve().parents[4] / "plugins" / "modules" / "ah_collection.py"
spec = importlib.util.spec_from_file_location(
    "ah_collection", str(_module_path), submodule_search_locations=[]
)
mod = importlib.util.module_from_spec(spec)
mod.__package__ = "plugins.modules"
spec.loader.exec_module(mod)

extract_version_from_path = mod.extract_version_from_path
main = mod.main
AHModule = sys.modules["plugins.module_utils.ah_module"].AHModule


def _run_main(params):
    """Run main() with mocked AHModule and return the endpoint passed to get_endpoint."""
    mock_module = MagicMock()
    mock_module.params = {
        "namespace": "my_ns",
        "name": "my_collection",
        "path": None,
        "repository": "staging",
        "wait": True,
        "interval": 10.0,
        "timeout": None,
        "overwrite_existing": False,
        "auto_approve": True,
        "version": None,
        "state": "present",
    }
    mock_module.params.update(params)
    mock_module.json_output = {}
    mock_module.get_endpoint.return_value = None
    AHModule.return_value = mock_module
    return mock_module


@pytest.mark.parametrize(
    "path, expected",
    [
        ("my_ns-my_collection-1.0.0.tar.gz", "1.0.0"),
        ("/tmp/my_ns-my_collection-1.0.0.tar.gz", "1.0.0"),
        ("/home/my-user/collections/my_ns-my_collection-1.0.0.tar.gz", "1.0.0"),
        ("/opt/ansible-automation/build-output/my_ns-my_collection-1.0.0.tar.gz", "1.0.0"),
        ("./my_ns-my_collection-1.0.0.tar.gz", "1.0.0"),
        ("/tmp/my_ns-my_collection-2.1.0-beta.1.tar.gz", "2.1.0-beta.1"),
        ("/path/to/sample-sample-1.0.0.tar.gz", "1.0.0"),
        ("/no-hyphens/path/ns-name-0.0.1.tar.gz", "0.0.1"),
    ],
    ids=[
        "bare_filename",
        "simple_absolute_path",
        "directory_with_hyphens",
        "multiple_directory_hyphens",
        "relative_path",
        "prerelease_version",
        "sample_collection",
        "single_directory_hyphen",
    ],
)
def test_version_extraction(path, expected):
    assert extract_version_from_path(path) == expected


@pytest.mark.parametrize(
    "path",
    [
        "mycollection.tar.gz",
        "name-1.0.0.tar.gz",
        "",
    ],
    ids=[
        "no_hyphens",
        "one_hyphen_missing_namespace",
        "empty_string",
    ],
)
def test_version_extraction_malformed_raises(path):
    with pytest.raises(ValueError, match="Could not extract version"):
        extract_version_from_path(path)


@pytest.mark.parametrize(
    "path, expected",
    [
        ("ns-name-1.0.0.tgz", "1.0.0.tgz"),
        ("ns-name-1.0.0", "1.0.0"),
    ],
    ids=[
        "wrong_extension_preserved",
        "no_extension",
    ],
)
def test_version_extraction_non_standard_extension(path, expected):
    assert extract_version_from_path(path) == expected


class TestMainVersionResolution:
    """Test that main() resolves the version correctly before calling the API."""

    def test_extracts_version_from_path_when_not_supplied(self, tmp_path):
        tarball = tmp_path / "my_ns-my_collection-1.0.0.tar.gz"
        tarball.touch()
        mock_module = _run_main({"path": str(tarball), "auto_approve": True, "version": None})
        main()
        endpoint = mock_module.get_endpoint.call_args[0][0]
        assert endpoint == "collections/my_ns/my_collection/versions/1.0.0"

    def test_preserves_user_supplied_version(self, tmp_path):
        tarball = tmp_path / "my_ns-my_collection-1.0.0.tar.gz"
        tarball.touch()
        mock_module = _run_main({"path": str(tarball), "auto_approve": True, "version": "2.0.0"})
        main()
        endpoint = mock_module.get_endpoint.call_args[0][0]
        assert endpoint == "collections/my_ns/my_collection/versions/2.0.0"

    def test_no_extraction_when_auto_approve_false(self, tmp_path):
        tarball = tmp_path / "my_ns-my_collection-1.0.0.tar.gz"
        tarball.touch()
        mock_module = _run_main({"path": str(tarball), "auto_approve": False, "version": None})
        main()
        endpoint = mock_module.get_endpoint.call_args[0][0]
        assert endpoint == "collections/my_ns/my_collection"
