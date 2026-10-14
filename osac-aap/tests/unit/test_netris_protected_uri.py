import importlib.util
from types import SimpleNamespace
from pathlib import Path

import pytest
from ansible.errors import AnsibleActionFail
from ansible.plugins.action import ActionBase



PLUGIN = (
    Path(__file__).parents[2]
    / "collections"
    / "ansible_collections"
    / "netris"
    / "controller"
    / "plugins"
    / "action"
    / "protected_uri.py"
)
_SPEC = importlib.util.spec_from_file_location("protected_uri", PLUGIN)
protected_uri = importlib.util.module_from_spec(_SPEC)
assert _SPEC.loader is not None
_SPEC.loader.exec_module(protected_uri)


def _action(monkeypatch, args):
    monkeypatch.setattr(ActionBase, "run", lambda self, **kwargs: {})
    action = object.__new__(protected_uri.ActionModule)
    action._task = SimpleNamespace(args=args)
    action._execute_module = lambda **kwargs: {"changed": False}
    return action


def test_rejects_missing_managed_url(monkeypatch):
    monkeypatch.delenv("NETRIS_CONTROLLER_URL", raising=False)
    monkeypatch.setenv("NETRIS_VALIDATE_CERTS", "true")
    action = _action(
        monkeypatch,
        {
            "url": "https://managed.example/api/auth",
            "method": "GET",
            "validate_certs": True,
        },
    )

    with pytest.raises(AnsibleActionFail, match="controller URL"):
        action.run(task_vars={})


def test_rejects_omitted_validate_certs(monkeypatch):
    monkeypatch.setenv("NETRIS_CONTROLLER_URL", "https://managed.example")
    monkeypatch.setenv("NETRIS_VALIDATE_CERTS", "false")
    action = _action(
        monkeypatch,
        {"url": "https://managed.example/api/auth", "method": "GET"},
    )

    with pytest.raises(AnsibleActionFail, match="certificate validation"):
        action.run(task_vars={})


def test_delegates_matching_managed_settings(monkeypatch):
    monkeypatch.setenv("NETRIS_CONTROLLER_URL", "https://managed.example")
    monkeypatch.setenv("NETRIS_VALIDATE_CERTS", "false")
    monkeypatch.setenv("NETRIS_CA_PATH", "/etc/netris/ca/bundle.pem")
    action = _action(
        monkeypatch,
        {
            "url": "https://managed.example/api/auth",
            "method": "GET",
            "validate_certs": False,
            "ca_path": "/etc/netris/ca/bundle.pem",
        },
    )

    assert action.run(task_vars={}) == {"changed": False}
