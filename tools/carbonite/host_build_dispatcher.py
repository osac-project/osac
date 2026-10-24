#!/usr/bin/python3 -I
"""Installed forced-command target for the optional Carbonite host build key.

This file is copied outside the repository by host_build_manager.py. It does not
import code or execute commands from the sandbox-editable checkout.
"""

import json
import os
import pathlib
import pwd
import stat
import subprocess
import sys


READY = "OSAC_HOST_BUILD_READY"
UI_TAG = "ghcr.io/osac-project/osac-ui:latest"
MAX_CONTAINERFILE = 1024 * 1024
# (context below checkout, Containerfile below context, tag, pre-file flags,
# post-tag flags). Every value is installed host code, never an SSH argument.
BUILD_ACTIONS = {
    "ui": (("osac-ui",), ("Containerfile",), UI_TAG, (), ()),
    "fulfillment": ((), ("fulfillment-service", "Containerfile"),
                    "ghcr.io/osac-project/fulfillment-service:latest", (), ("--build-arg", "VERSION=")),
    "operator": ((), ("osac-operator", "Containerfile"),
                 "ghcr.io/osac-project/osac-operator:latest", (), ()),
    "metering-service": ((), ("osac-metering", "metering-service", "Containerfile"),
                         "ghcr.io/osac-project/metering-service:latest", (), ()),
    "metering-m360": (("osac-metering",), ("adapters", "Containerfile.m360-adapter"),
                      "ghcr.io/osac-project/metering-m360-adapter:latest", (), ()),
    "metering-echo": (("osac-metering",), ("adapters", "Containerfile.echo-adapter"),
                      "ghcr.io/osac-project/metering-echo-adapter:latest", (), ()),
    "devstack-cli-tools": (("osac-installer", "charts", "osac-devstack"),
                           ("Containerfile.cli-tools",), "ghcr.io/osac-project/cli-tools:latest",
                           ("--platform", "linux/amd64,linux/arm64"), ()),
}


def _open_dir_chain(path):
    """Open each path component without following symlinks, binding its inode."""
    path = pathlib.Path(path)
    if not path.is_absolute() or ".." in path.parts:
        raise ValueError("unsafe checkout path")
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    descriptor = os.open("/", flags)
    try:
        for component in path.parts[1:]:
            next_descriptor = os.open(component, flags, dir_fd=descriptor)
            os.close(descriptor)
            descriptor = next_descriptor
        return descriptor
    except Exception:
        os.close(descriptor)
        raise


def _open_child_dir(directory_fd, parts):
    descriptor = os.dup(directory_fd)
    try:
        for component in parts:
            next_descriptor = os.open(component, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW,
                                      dir_fd=descriptor)
            os.close(descriptor)
            descriptor = next_descriptor
        return descriptor
    except Exception:
        os.close(descriptor)
        raise


def _read_containerfile(directory_fd, parts):
    parent_fd = _open_child_dir(directory_fd, parts[:-1])
    try:
        descriptor = os.open(parts[-1], os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK,
                             dir_fd=parent_fd)
    finally:
        os.close(parent_fd)
    try:
        if not stat.S_ISREG(os.fstat(descriptor).st_mode):
            raise ValueError("Containerfile is not a regular file")
        with os.fdopen(descriptor, "rb", closefd=False) as source:
            data = source.read(MAX_CONTAINERFILE + 1)
        if len(data) > MAX_CONTAINERFILE:
            raise ValueError("Containerfile exceeds host-build size limit")
        return data
    finally:
        os.close(descriptor)


def _podman_env():
    user = pwd.getpwuid(os.getuid())
    return {
        "HOME": user.pw_dir,
        "USER": user.pw_name,
        "LOGNAME": user.pw_name,
        "PATH": "/usr/bin:/bin",
        "XDG_RUNTIME_DIR": "/run/user/" + str(os.getuid()),
    }


def dispatch(action, checkout, run=subprocess.run, output=sys.stdout):
    """Dispatch a literal action. `run` and `output` are injectable only in tests."""
    if action != "probe" and action not in BUILD_ACTIONS:
        raise ValueError("unsupported host-build action")
    if action == "probe":
        output.write(READY + "\n")
        return 0

    context_parts, file_parts, tag, pre_flags, post_flags = BUILD_ACTIONS[action]
    name_option = "--manifest" if action == "devstack-cli-tools" else "-t"
    checkout_fd = _open_dir_chain(checkout)
    try:
        context_fd = _open_child_dir(checkout_fd, context_parts)
        try:
            instructions = _read_containerfile(context_fd, file_parts)
            result = run(
                ["/usr/bin/podman", "build", *pre_flags, "-f", "-", name_option, tag, *post_flags, "."],
                cwd="/proc/self/fd/" + str(context_fd), pass_fds=(context_fd,), input=instructions,
                env=_podman_env(), check=False,
            )
            return result.returncode
        finally:
            os.close(context_fd)
    finally:
        os.close(checkout_fd)


def _installed_checkout():
    script = pathlib.Path(__file__).absolute()
    config = script.with_name("osac-host-build.json")
    for path in (script, config):
        info = path.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o022:
            raise ValueError("unsafe host-build installation")
    for parent in (script.parent, script.parent.parent):
        info = parent.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o022:
            raise ValueError("unsafe host-build installation directory")
    data = json.loads(config.read_text())
    checkout = pathlib.Path(data["checkout"])
    if not checkout.is_absolute() or checkout.is_symlink():
        raise ValueError("unsafe configured checkout")
    if str(checkout.resolve()) != str(checkout):
        raise ValueError("configured checkout moved or contains a symlink")
    return checkout


def main():
    os.umask(0o077)
    try:
        return dispatch(os.environ.get("SSH_ORIGINAL_COMMAND"), _installed_checkout())
    except (ValueError, OSError, KeyError, json.JSONDecodeError) as error:
        print("host build rejected: " + str(error), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
