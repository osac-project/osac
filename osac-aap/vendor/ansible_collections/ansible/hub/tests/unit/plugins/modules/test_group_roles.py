import importlib.util
import pathlib
import sys
from unittest.mock import MagicMock


for mock_module in [
    "ansible",
    "ansible.module_utils",
    "plugins",
    "plugins.modules",
    "plugins.module_utils",
    "plugins.module_utils.ah_api_module",
    "plugins.module_utils.ah_module",
    "plugins.module_utils.ah_ui_object",
    "plugins.module_utils.ah_pulp_object",
]:
    sys.modules.setdefault(mock_module, MagicMock())

_module_path = pathlib.Path(__file__).resolve().parents[4] / "plugins" / "modules" / "group_roles.py"
spec = importlib.util.spec_from_file_location("group_roles", str(_module_path), submodule_search_locations=[])
mod = importlib.util.module_from_spec(spec)
mod.__package__ = "plugins.modules"
spec.loader.exec_module(mod)


def test_organization_is_opt_in():
    module = MagicMock()
    groups = ["team1", "org1::team2"]

    assert mod.qualify_group_names(module, groups, None) is groups
    module.fail_json.assert_not_called()


def test_organization_qualifies_unqualified_group_names():
    module = MagicMock()

    assert mod.qualify_group_names(module, ["team1", "org1::team2"], "org1") == [
        "org1::team1",
        "org1::team2",
    ]


def test_organization_rejects_conflicting_qualified_group_name():
    module = MagicMock()

    assert mod.qualify_group_names(module, ["org2::team1"], "org1") == []
    module.fail_json.assert_called_once_with(
        msg=(
            "Group `org2::team1` is already organization-qualified for a "
            "different organization; do not combine it with organization `org1`."
        )
    )


def test_group_roles_rejects_hub_4_11_and_later():
    module = MagicMock()

    mod.validate_server_version(module, "4.11")

    module.fail_json.assert_called_once_with(
        msg=(
            "The group_roles module is supported through private automation hub 4.10 "
            "(AAP 2.5). Use the team_roles module with private automation hub 4.11 or later."
        )
    )
