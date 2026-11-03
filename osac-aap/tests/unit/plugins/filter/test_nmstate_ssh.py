"""Unit tests for shell-safe nmstate_config SSH and SCP arguments."""

from __future__ import annotations

import shlex
from pathlib import Path

import pytest
import yaml
from ansible.errors import AnsibleFilterError

from nmstate_ssh import FilterModule


AAP_ROOT = Path(__file__).resolve().parents[4]
TASK_FILE = (
    AAP_ROOT
    / "collections/ansible_collections/osac/service/roles/nmstate_config/tasks/apply_single.yaml"
)
FILTERS = FilterModule().filters()
SSH_OPTIONS = [
    "-o",
    "StrictHostKeyChecking=no",
    "-o",
    "UserKnownHostsFile=/dev/null",
    "-o",
    "ConnectTimeout=30",
]


def _connection(**overrides):
    connection = {
        "host": "192.0.2.35",
        "user": "core",
        "key": "/tmp/.ssh/server-ssh-key",
        "bastion_host": "",
        "bastion_user": "",
        "bastion_key": "",
    }
    connection.update(overrides)
    return connection


def _proxy_command(argv: list[str]) -> str:
    options = [
        argv[index + 1]
        for index, item in enumerate(argv[:-1])
        if item == "-o"
    ]
    return next(
        option.removeprefix("ProxyCommand=")
        for option in options
        if option.startswith("ProxyCommand=")
    )


def test_direct_ssh_keeps_options_target_key_and_sysctl_behavior():
    argv = FILTERS["nmstate_ssh_argv"](
        _connection(), "disable_rp_filter", "ens4"
    )

    assert argv[:1] == ["ssh"]
    assert argv[1 : 1 + len(SSH_OPTIONS)] == SSH_OPTIONS
    assert argv[-4:-1] == [
        "-i",
        "/tmp/.ssh/server-ssh-key",
        "core@192.0.2.35",
    ]
    assert shlex.split(argv[-1]) == [
        "sudo",
        "sysctl",
        "-qw",
        "net.ipv4.conf.all.rp_filter=0",
        "net.ipv4.conf.ens4.rp_filter=0",
    ]
    assert not any(option.startswith("ProxyCommand=") for option in argv)


def test_bastion_proxy_shell_quotes_values_and_keeps_distinct_keys():
    target_key = "/tmp/agent key;$(id)"
    bastion_key = "/tmp/bastion key's %h"
    connection = _connection(
        key=target_key,
        bastion_host="bastion.example.test",
        bastion_user="ubuntu",
        bastion_key=bastion_key,
    )

    argv = FILTERS["nmstate_ssh_argv"](connection, "apply_nmstate")
    proxy = _proxy_command(argv)
    # OpenSSH expands %% to a literal percent before running ProxyCommand.
    proxy_argv = [argument.replace("%%", "%") for argument in shlex.split(proxy)]

    assert argv[argv.index("-i") + 1] == target_key
    assert argv[-2] == "core@192.0.2.35"
    assert argv[-1] == (
        "nohup sudo nmstatectl apply /tmp/nmstate-config.yaml "
        "> /tmp/nmstate-apply.log 2>&1 &"
    )
    assert proxy_argv == [
        "ssh",
        *SSH_OPTIONS,
        "-i",
        bastion_key,
        "-W",
        "%h:%p",
        "ubuntu@bastion.example.test",
    ]


@pytest.mark.parametrize("interface", ["ens4;touch", "ens4$(id)", "ens4'quoted"])
def test_remote_sysctl_values_remain_one_quoted_argument(interface: str):
    argv = FILTERS["nmstate_ssh_argv"](
        _connection(), "disable_rp_filter", interface
    )

    assert shlex.split(argv[-1]) == [
        "sudo",
        "sysctl",
        "-qw",
        "net.ipv4.conf.all.rp_filter=0",
        f"net.ipv4.conf.{interface}.rp_filter=0",
    ]


def test_scp_uses_one_local_source_and_fixed_remote_path():
    source = "/tmp/nmstate-agent-01.yaml"
    argv = FILTERS["nmstate_scp_argv"](_connection(), source)

    assert argv[0] == "scp"
    assert argv[1 : 1 + len(SSH_OPTIONS)] == SSH_OPTIONS
    assert argv[-4:] == [
        "-i",
        "/tmp/.ssh/server-ssh-key",
        source,
        "core@192.0.2.35:/tmp/nmstate-config.yaml",
    ]


def test_bastion_scp_keeps_proxy_and_separate_key_paths():
    target_key = "/tmp/agent-key"
    bastion_key = "/tmp/bastion-key"
    source = "/tmp/nmstate-agent-01.yaml"
    connection = _connection(
        key=target_key,
        bastion_host="bastion.example.test",
        bastion_user="ubuntu",
        bastion_key=bastion_key,
    )

    argv = FILTERS["nmstate_scp_argv"](connection, source)
    proxy = _proxy_command(argv)
    proxy_argv = [argument.replace("%%", "%") for argument in shlex.split(proxy)]

    assert argv[argv.index("-i") + 1] == target_key
    assert argv[-2:] == [
        source,
        "core@192.0.2.35:/tmp/nmstate-config.yaml",
    ]
    assert proxy_argv[proxy_argv.index("-i") + 1] == bastion_key
    assert proxy_argv[-3:] == ["-W", "%h:%p", "ubuntu@bastion.example.test"]


def test_scp_keeps_shell_metacharacters_in_a_single_source_argument():
    source = "/tmp/nmstate-agent;touch.yaml"
    argv = FILTERS["nmstate_scp_argv"](_connection(), source)

    assert argv[-2] == source


@pytest.mark.parametrize(
    "overrides",
    [
        {"host": "192.0.2.35;touch"},
        {"host": "-oProxyCommand=invalid"},
        {"user": "-oProxyCommand=invalid"},
        {"user": "core;touch"},
        {"bastion_host": "bastion.example;touch"},
        {
            "bastion_host": "bastion.example",
            "bastion_user": "ubuntu;touch",
            "bastion_key": "/tmp/bastion",
        },
    ],
)
def test_invalid_host_and_login_values_are_rejected(overrides):
    with pytest.raises(AnsibleFilterError):
        FILTERS["nmstate_ssh_argv"](_connection(**overrides), "apply_nmstate")


def test_bastion_requires_its_own_login_and_key():
    with pytest.raises(AnsibleFilterError):
        FILTERS["nmstate_ssh_argv"](
            _connection(bastion_host="bastion.example.test"), "apply_nmstate"
        )


def test_apply_tasks_use_argv_filters_keep_no_log_and_remove_shell():
    tasks = yaml.safe_load(TASK_FILE.read_text())
    assert all("ansible.builtin.shell" not in task for task in tasks)

    command_tasks = [task for task in tasks if "ansible.builtin.command" in task]
    assert len(command_tasks) == 3
    assert all("argv" in task["ansible.builtin.command"] for task in command_tasks)
    assert all(task.get("no_log") is True for task in command_tasks)
    assert "nmstate_ssh_argv" in command_tasks[0]["ansible.builtin.command"]["argv"]
    assert "nmstate_scp_argv" in command_tasks[1]["ansible.builtin.command"]["argv"]
    assert "nmstate_ssh_argv" in command_tasks[2]["ansible.builtin.command"]["argv"]

    connection_task = next(
        task
        for task in tasks
        if task["name"].startswith("Build SSH connection inputs")
    )
    assert connection_task.get("no_log") is True
