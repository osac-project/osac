import importlib.util
import pathlib
import sys
from unittest.mock import MagicMock

# ah_ee_repository.py imports ansible-core and sibling module_utils files at the
# top level. Neither ansible-core nor those files' real dependencies are
# available in the unit test environment, so mock them in sys.modules before
# loading the file, matching the approach used by test_ah_collection_version.py.
for mock_module in [
    "ansible",
    "ansible.module_utils",
    "ansible.module_utils._text",
    "ansible.module_utils.basic",
    "plugins",
    "plugins.modules",
    "plugins.module_utils",
    "plugins.module_utils.ah_api_module",
    "plugins.module_utils.ah_ui_object",
    "plugins.module_utils.ah_pulp_object",
]:
    sys.modules.setdefault(mock_module, MagicMock())

_module_path = pathlib.Path(__file__).resolve().parents[2] / "plugins" / "modules" / "ah_ee_repository.py"
spec = importlib.util.spec_from_file_location(
    "ah_ee_repository", str(_module_path), submodule_search_locations=[]
)
mod = importlib.util.module_from_spec(spec)
mod.__package__ = "plugins.modules"
spec.loader.exec_module(mod)

main = mod.main
AHAPIModule = sys.modules["plugins.module_utils.ah_api_module"].AHAPIModule
AHPulpEERepository = sys.modules["plugins.module_utils.ah_pulp_object"].AHPulpEERepository
AHUIEERepository = sys.modules["plugins.module_utils.ah_ui_object"].AHUIEERepository


def _run_main(overrides):
    """Run main() against a repository that already exists (update path, no
    registry/remote, no README) and return the mocked AHPulpEERepository
    instance so the test can inspect what update() was called with.
    """
    mock_module = MagicMock()
    params = {
        "name": "my_repo",
        "description": None,
        "registry": None,
        "upstream_name": None,
        "include_tags": [],
        "exclude_tags": [],
        "readme": None,
        "readme_file": None,
        "state": "present",
    }
    params.update(overrides)
    mock_module.params = params
    mock_module.get_server_version.return_value = "4.9.0"
    mock_module.check_mode = False
    AHAPIModule.return_value = mock_module

    repo_pulp = MagicMock()
    repo_pulp.exists = True
    repo_pulp.update.return_value = True
    AHPulpEERepository.return_value = repo_pulp

    AHUIEERepository.return_value = MagicMock()

    main()
    return repo_pulp


import pytest


@pytest.mark.parametrize(
    "input_desc, expected_sent",
    [
        ("", None),
        ("a real description", "a real description"),
    ],
    ids=[
        "empty_string_clears_to_null",
        "real_text_sent_unchanged",
    ],
)
def test_description_value_sent_to_api(input_desc, expected_sent):
    """Empty string means "clear it" and must be translated to null, since
    the API rejects blank strings. Real text passes through unchanged.
    """
    repo_pulp = _run_main({"description": input_desc})

    sent = repo_pulp.update.call_args[0][0]
    assert sent["description"] == expected_sent


def test_omitted_description_does_not_call_update():
    repo_pulp = _run_main({"description": None})

    repo_pulp.update.assert_not_called()
