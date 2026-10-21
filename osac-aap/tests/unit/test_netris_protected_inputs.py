import sys
from pathlib import Path

import pytest


MODULE_UTILS = (
    Path(__file__).resolve().parents[2]
    / "collections"
    / "ansible_collections"
    / "netris"
    / "controller"
    / "plugins"
    / "module_utils"
)
sys.path.insert(0, str(MODULE_UTILS))

from protected_connection import (  # noqa: E402
    ProtectedInputError,
    strip_protected_env,
    validate_extra_vars,
)


PROTECTED_NAMES = (
    "controller_url",
    "netris_controller_url",
    "NETRIS_CONTROLLER_URL",
    "validate_certs",
    "netris_validate_certs",
    "NETRIS_VALIDATE_CERTS",
    "ca_path",
    "netris_ca_path",
    "NETRIS_CA_PATH",
)


@pytest.mark.parametrize("name", PROTECTED_NAMES)
def test_validate_extra_vars_rejects_protected_names_at_any_depth(name):
    with pytest.raises(ProtectedInputError, match=name):
        validate_extra_vars({"osac_job_vars": {"resource": {name: "value"}}})


@pytest.mark.parametrize(
    "name",
    [
        "NetrisControllerURL",
        "NETRIS.controller_url",
        "netris-controller-url",
        "ValidateCerts",
        "Netris_CA_Path",
    ],
)
def test_validate_extra_vars_rejects_case_and_separator_variants(name):
    with pytest.raises(ProtectedInputError):
        validate_extra_vars({"nested": [{name: False}]})


@pytest.mark.parametrize("value", [None, "", False, 0, "https://managed.example"])
def test_validate_extra_vars_rejects_protected_values_regardless_of_value(value):
    with pytest.raises(ProtectedInputError):
        validate_extra_vars({"netris_validate_certs": value})


def test_validate_extra_vars_preserves_non_protected_payload():
    payload = {
        "osac_job_vars": {
            "resource": {"name": "network-1", "enabled": False},
            "items": ["a", 3, None],
        },
        "empty": "",
    }

    assert validate_extra_vars(payload) is None


def test_strip_protected_env_removes_aliases_but_preserves_other_values():
    environment = {
        "NETRIS_CONTROLLER_URL": "https://managed.example",
        "netris_controller_url": "https://override.example",
        "NETRIS_VALIDATE_CERTS": "false",
        "unrelated": "kept",
    }

    assert strip_protected_env({**environment, 1: "non-string key"}) == {
        "unrelated": "kept",
        1: "non-string key",
    }
