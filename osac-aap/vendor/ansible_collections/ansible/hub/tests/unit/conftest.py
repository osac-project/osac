import sys
import types
from unittest.mock import MagicMock

# AHAPIModule subclasses ansible.module_utils.basic.AnsibleModule. ansible-core
# is not installed in the unit test environment, and a MagicMock cannot stand
# in as a base class: subclassing a MagicMock instance doesn't raise, it just
# silently makes the resulting "class" itself a MagicMock instance instead of
# a real class (confirmed: `class Foo(MagicMock().attr): pass` gives
# `isinstance(Foo, type) == False`). Any test that then instantiates or
# type-checks against that "class" gets nonsense instead of a clear failure.
# Give AnsibleModule a real, empty stand-in class instead.
_fake_basic = types.ModuleType("ansible.module_utils.basic")


class _FakeAnsibleModule:
    pass


_fake_basic.AnsibleModule = _FakeAnsibleModule
_fake_basic.env_fallback = lambda *args, **kwargs: None
sys.modules.setdefault("ansible.module_utils.basic", _fake_basic)


def _remove_values(value, no_log_values):
    """Minimal test double for Ansible's recursive no_log redaction."""
    if isinstance(value, dict):
        return {key: _remove_values(item, no_log_values) for key, item in value.items()}
    if isinstance(value, list):
        return [_remove_values(item, no_log_values) for item in value]
    if isinstance(value, str):
        for secret in no_log_values:
            value = value.replace(secret, "********")
    return value


_fake_parameters = types.ModuleType("ansible.module_utils.common.parameters")
_fake_parameters.remove_values = _remove_values
sys.modules.setdefault("ansible.module_utils.common.parameters", _fake_parameters)

for _mock_module in [
    "ansible",
    "ansible.module_utils",
    "ansible.module_utils._text",
    "ansible.module_utils.common",
    "ansible.module_utils.compat",
    "ansible.module_utils.compat.version",
    "ansible.module_utils.urls",
]:
    sys.modules.setdefault(_mock_module, MagicMock())
