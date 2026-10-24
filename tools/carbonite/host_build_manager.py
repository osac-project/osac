#!/usr/bin/python3 -I
"""Host-only lifecycle for Carbonite's opt-in, forced-command build key.

This script installs itself outside the checkout. Later prepare/revoke calls
must use that installed copy, never the potentially sandbox-edited source.
"""

import argparse
import fcntl
import hashlib
import json
import os
import pathlib
import pwd
import re
import shutil
import stat
import subprocess
import sys
import tempfile


MARKER = "osac-carbonite-host-build"
RULE = "osac-host-build"
KEY_NAME = "osac-host-build"
SAFE_FORCED_PATH = re.compile(r"\A/[A-Za-z0-9_./-]+\Z")
READY = "OSAC_HOST_BUILD_READY"


def _require_regular(path, *, owner=True, writable=False):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or (owner and info.st_uid != os.getuid()):
        raise ValueError("unsafe regular file: " + str(path))
    if not writable and info.st_mode & 0o022:
        raise ValueError("group/world writable file: " + str(path))
    return info


def _require_dir(path, *, owner=True):
    info = path.lstat()
    if not stat.S_ISDIR(info.st_mode) or (owner and info.st_uid != os.getuid()):
        raise ValueError("unsafe directory: " + str(path))
    if info.st_mode & 0o022:
        raise ValueError("group/world writable directory: " + str(path))
    return info


def _ensure_dir(path):
    if path.exists() or path.is_symlink():
        _require_dir(path)
    else:
        path.mkdir(mode=0o700)
        _require_dir(path)


def _atomic_write(path, data, mode=0o600):
    _require_dir(path.parent)
    if path.exists() or path.is_symlink():
        _require_regular(path, writable=True)
    fd, tmp = tempfile.mkstemp(prefix=".osac-host-build-", dir=path.parent)
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(tmp, path)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)


def _line(public_key, dispatcher):
    if not SAFE_FORCED_PATH.fullmatch(str(dispatcher)) or ".." in dispatcher.parts:
        raise ValueError("unsafe forced-command path")
    fields = public_key.strip().split()
    if len(fields) < 2 or fields[0] != "ssh-ed25519" or not re.fullmatch(r"[A-Za-z0-9+/=]+", fields[1]):
        raise ValueError("invalid Ed25519 public key")
    # There are no quotes, commas, whitespace or backslashes in this path.
    command = "/usr/bin/python3 -I -S " + str(dispatcher)
    return 'restrict,command="' + command + '" ssh-ed25519 ' + fields[1] + ' ' + MARKER + '\n'


def install_authorized_key(path, public_key, dispatcher):
    """Replace exactly our marked entry, keeping every unrelated SSH key."""
    _require_dir(path.parent)
    if path.exists() or path.is_symlink():
        _require_regular(path, writable=True)
        current = path.read_text()
    else:
        current = ""
    lines = current.splitlines(keepends=True)
    ours = [line for line in lines if MARKER in line]
    if len(ours) > 1 or any(not line.rstrip().endswith(" " + MARKER) for line in ours):
        raise ValueError("conflicting host-build authorized_keys marker")
    desired = _line(public_key, pathlib.Path(dispatcher))
    other_lines = [line for line in lines if MARKER not in line]
    key_blob = public_key.split()[1]
    if any(key_blob in line.split() for line in other_lines):
        raise ValueError("host-build key is authorized without the forced command")
    retained = "".join(other_lines)
    if retained and not retained.endswith("\n"):
        retained += "\n"
    updated = retained + desired
    if updated != current:
        _atomic_write(path, updated.encode(), 0o600)


def remove_authorized_key(path):
    if not path.exists() and not path.is_symlink():
        return
    _require_regular(path, writable=True)
    lines = path.read_text().splitlines(keepends=True)
    ours = [line for line in lines if MARKER in line]
    if any(not line.rstrip().endswith(" " + MARKER) for line in ours):
        raise ValueError("conflicting host-build authorized_keys marker")
    updated = "".join(line for line in lines if MARKER not in line)
    if updated != "".join(lines):
        _atomic_write(path, updated.encode(), 0o600)


def _paths(home):
    home = pathlib.Path(home)
    return {
        "ssh": home / ".ssh",
        "authorized": home / ".ssh/authorized_keys",
        "libexec": home / ".local/libexec",
        "manager": home / ".local/libexec/osac-host-build-manager",
        "dispatcher": home / ".local/libexec/osac-host-build",
        "config": home / ".local/libexec/osac-host-build.json",
        "receipt": home / ".local/libexec/osac-host-build-installation.json",
        "state": home / ".local/share/osac-host-build",
        "key": home / ".local/share/osac-host-build/id_ed25519",
        "known_hosts": home / ".local/share/osac-host-build/known_hosts",
        "user": home / ".local/share/osac-host-build/user",
        "lock": home / ".local/share/osac-host-build/lock",
    }


def _run(argv, *, capture=False):
    return subprocess.run(argv, check=True, text=True, capture_output=capture,
                          env={"HOME": pwd.getpwuid(os.getuid()).pw_dir,
                               "PATH": "/usr/bin:/bin"})


def _gateway(gateway):
    result = _run(["/usr/bin/openshell", "gateway", "list", "--output", "json"], capture=True)
    values = json.loads(result.stdout)
    if isinstance(values, dict):
        values = values.get("gateways", values.get("items", []))
    matches = [item for item in values if item.get("name") == gateway]
    if len(matches) != 1:
        raise ValueError("selected gateway was not found")
    item = matches[0]
    endpoint = item.get("endpoint", "")
    if (item.get("type") != "local" or item.get("auth") != "mtls" or
            not re.fullmatch(r"https://(?:127\.0\.0\.1|\[::1\]):[0-9]+/?", endpoint)):
        raise ValueError("host build requires a local OpenShell gateway")
    return endpoint


def _host_key():
    source = pathlib.Path("/etc/ssh/ssh_host_ed25519_key.pub")
    if _require_regular(source, owner=False).st_uid != 0:
        raise ValueError("trusted SSH host key is not root-owned")
    trusted = source.read_text().split()
    if len(trusted) < 2 or trusted[0] != "ssh-ed25519":
        raise ValueError("no trusted Ed25519 host key")
    scanned = _run(["/usr/bin/ssh-keyscan", "-T", "5", "-p", "22", "-t", "ed25519", "127.0.0.1"], capture=True)
    observed = [line.split() for line in scanned.stdout.splitlines() if line and not line.startswith("#")]
    if not any(len(line) >= 3 and line[1:3] == trusted[:2] for line in observed):
        raise ValueError("port 22 does not serve the trusted host Ed25519 key")
    return "osac-host-build " + trusted[0] + " " + trusted[1] + "\n"


def _canonical_checkout(checkout):
    path = pathlib.Path(checkout).absolute()
    if "\n" in str(path) or "\r" in str(path):
        raise ValueError("newline in checkout path")
    if path.is_symlink() or path.resolve() != path:
        raise ValueError("checkout must not contain symlinks")
    _require_dir(path)
    return path


def _lock(paths):
    _ensure_dir(paths["state"].parent.parent)
    _ensure_dir(paths["state"].parent)
    _ensure_dir(paths["state"])
    descriptor = os.open(paths["lock"], os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    info = os.fstat(descriptor)
    if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        os.close(descriptor)
        raise ValueError("unsafe host-build lock")
    fcntl.flock(descriptor, fcntl.LOCK_EX)
    return descriptor


def _config(paths):
    _require_regular(paths["config"])
    return json.loads(paths["config"].read_text())


def enable(paths, gateway, workspace, sandbox, checkout):
    _require_dir(pathlib.Path.home())
    checkout = _canonical_checkout(checkout)
    endpoint = _gateway(gateway)
    pin = _host_key()
    for directory in (paths["ssh"], pathlib.Path.home() / ".local", paths["libexec"],
                      pathlib.Path.home() / ".local/share", paths["state"]):
        _ensure_dir(directory)
    lock = _lock(paths)
    try:
        if paths["config"].exists():
            old = _config(paths)
            expected = {"gateway": gateway, "workspace": workspace, "sandbox": sandbox,
                        "checkout": str(checkout), "endpoint": endpoint}
            if any(old.get(name) != value for name, value in expected.items()):
                raise ValueError("host build already enabled for a different binding")
            _require_regular(paths["receipt"])
            receipt = json.loads(paths["receipt"].read_text())
            for name in ("manager", "dispatcher"):
                _require_regular(paths[name])
                digest = hashlib.sha256(paths[name].read_bytes()).hexdigest()
                if digest != old.get(name + "_sha256") or digest != receipt.get(name + "_sha256"):
                    raise ValueError("installed host-build code changed before reauthorization")
            _require_regular(paths["key"])
        else:
            installed = paths["manager"].exists() and paths["dispatcher"].exists()
            if any(paths[name].exists() or paths[name].is_symlink() for name in ("key", "known_hosts", "user")):
                raise ValueError("unexpected partial host-build installation; inspect it before retry")
            if installed:
                _require_regular(paths["manager"])
                _require_regular(paths["dispatcher"])
                _require_regular(paths["receipt"])
                receipt = json.loads(paths["receipt"].read_text())
                for name in ("manager", "dispatcher"):
                    if hashlib.sha256(paths[name].read_bytes()).hexdigest() != receipt.get(name + "_sha256"):
                        raise ValueError("installed host-build code changed after revocation")
            elif paths["manager"].exists() or paths["manager"].is_symlink() or paths["dispatcher"].exists() or paths["dispatcher"].is_symlink():
                raise ValueError("incomplete host-build installation; inspect it before retry")
            elif paths["receipt"].exists() or paths["receipt"].is_symlink():
                raise ValueError("host-build installation receipt exists without installed code")
            if paths["authorized"].exists() or paths["authorized"].is_symlink():
                _require_regular(paths["authorized"], writable=True)
                if MARKER in paths["authorized"].read_text():
                    raise ValueError("unexpected existing host-build authorized key")
            if checkout in paths["manager"].parents:
                raise ValueError("host-only installation falls inside checkout")
            try:
                if not installed:
                    source = pathlib.Path(__file__).absolute()
                    dispatcher_source = source.with_name("host_build_dispatcher.py")
                    _require_regular(source)
                    _require_regular(dispatcher_source)
                    _atomic_write(paths["manager"], source.read_bytes(), 0o700)
                    _atomic_write(paths["dispatcher"], dispatcher_source.read_bytes(), 0o700)
                    receipt = {name + "_sha256": hashlib.sha256(paths[name].read_bytes()).hexdigest()
                               for name in ("manager", "dispatcher")}
                    _atomic_write(paths["receipt"], json.dumps(receipt, sort_keys=True).encode(), 0o600)
                _run(["/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(paths["key"])])
                _require_regular(paths["key"])
                os.chmod(paths["key"], 0o600)
                public = paths["key"].with_suffix(".pub")
                _require_regular(public)
                _atomic_write(paths["known_hosts"], pin.encode(), 0o600)
                _atomic_write(paths["user"], (pwd.getpwuid(os.getuid()).pw_name + "\n").encode(), 0o600)
                data = {"gateway": gateway, "workspace": workspace, "sandbox": sandbox,
                        "checkout": str(checkout), "endpoint": endpoint,
                        "manager_sha256": hashlib.sha256(paths["manager"].read_bytes()).hexdigest(),
                        "dispatcher_sha256": hashlib.sha256(paths["dispatcher"].read_bytes()).hexdigest()}
                _atomic_write(paths["config"], json.dumps(data, sort_keys=True).encode(), 0o600)
                install_authorized_key(paths["authorized"], public.read_text(), paths["dispatcher"])
            except Exception:
                # The SSH authorization is the capability. Remove it before
                # discarding the configuration after a partial first install.
                remove_authorized_key(paths["authorized"])
                partials = [paths["config"], paths["known_hosts"], paths["user"], paths["key"],
                            paths["key"].with_suffix(".pub")]
                if not installed:
                    partials.extend((paths["receipt"], paths["dispatcher"], paths["manager"]))
                for partial in partials:
                    if partial.exists() or partial.is_symlink():
                        _require_regular(partial, writable=True)
                        partial.unlink()
                raise
            return
        install_authorized_key(paths["authorized"], paths["key"].with_suffix(".pub").read_text(), paths["dispatcher"])
    finally:
        os.close(lock)


def upgrade(paths):
    """Explicitly replace revoked host-only code with reviewed checkout code.

    A partial copy can be retried if each installed file still matches either
    the previous receipt or the new reviewed source. No host key may be active
    while the two files and their receipt are updated.
    """
    _require_dir(pathlib.Path.home())
    _require_dir(paths["libexec"])
    lock = _lock(paths)
    try:
        for name in ("config", "key", "known_hosts", "user"):
            if paths[name].exists() or paths[name].is_symlink():
                raise ValueError("revoke host build before upgrading installed code")
        public = paths["key"].with_suffix(".pub")
        if public.exists() or public.is_symlink():
            raise ValueError("revoke host build before upgrading installed code")
        if paths["authorized"].exists() or paths["authorized"].is_symlink():
            _require_regular(paths["authorized"])
            if MARKER in paths["authorized"].read_text():
                raise ValueError("revoke host build before upgrading installed code")
        _require_regular(paths["receipt"])
        previous = json.loads(paths["receipt"].read_text())
        source_manager = pathlib.Path(__file__).absolute()
        source_dispatcher = source_manager.with_name("host_build_dispatcher.py")
        if source_manager == paths["manager"]:
            raise ValueError("upgrade must run from reviewed checkout source")
        desired = {}
        for name, source in (("manager", source_manager), ("dispatcher", source_dispatcher)):
            _require_regular(source)
            _require_regular(paths[name])
            content = source.read_bytes()
            new_hash = hashlib.sha256(content).hexdigest()
            current_hash = hashlib.sha256(paths[name].read_bytes()).hexdigest()
            if current_hash not in (previous.get(name + "_sha256"), new_hash):
                raise ValueError("installed host-build code changed unexpectedly")
            desired[name] = (content, new_hash)
        for name in ("manager", "dispatcher"):
            content, new_hash = desired[name]
            if hashlib.sha256(paths[name].read_bytes()).hexdigest() != new_hash:
                _atomic_write(paths[name], content, 0o700)
        receipt = {name + "_sha256": desired[name][1] for name in ("manager", "dispatcher")}
        _atomic_write(paths["receipt"], json.dumps(receipt, sort_keys=True).encode(), 0o600)
    finally:
        os.close(lock)


def _bound(paths, gateway, workspace, sandbox, checkout):
    data = _config(paths)
    expected = {"gateway": gateway, "workspace": workspace, "sandbox": sandbox,
                "checkout": str(_canonical_checkout(checkout)), "endpoint": _gateway(gateway)}
    if any(data.get(name) != value for name, value in expected.items()):
        raise ValueError("host-build binding changed (gateway, sandbox or checkout)")
    for name, hash_name in (("manager", "manager_sha256"), ("dispatcher", "dispatcher_sha256")):
        _require_regular(paths[name])
        if hashlib.sha256(paths[name].read_bytes()).hexdigest() != data.get(hash_name):
            raise ValueError("installed host-build code was modified")
    _require_regular(paths["key"])
    _require_regular(paths["known_hosts"])
    _require_regular(paths["user"])
    return data


def _openshell_args(gateway, workspace, *args):
    return ["/usr/bin/openshell", "--gateway", gateway, "--workspace", workspace, *args]


def _sandbox_matches(gateway, workspace, sandbox):
    result = _run(_openshell_args(gateway, workspace, "sandbox", "get", sandbox, "--output", "json"), capture=True)
    item = json.loads(result.stdout)
    if item.get("name") != sandbox or item.get("workspace") != workspace or item.get("phase") != "Ready":
        raise ValueError("sandbox identity or readiness changed")
    identity = item.get("id")
    if not isinstance(identity, str) or not re.fullmatch(r"[a-f0-9-]+", identity):
        raise ValueError("sandbox has no valid identity")
    running = _run(["/usr/bin/podman", "ps", "--format", "{{.Names}}"], capture=True).stdout.splitlines()
    expected = "openshell-" + workspace + "--" + sandbox + "-" + identity
    if expected not in running:
        raise ValueError("sandbox is not running in the local Podman store")
    return identity


def _sandbox_policy(gateway, workspace, sandbox):
    result = _run(_openshell_args(gateway, workspace, "sandbox", "get", sandbox, "--output", "json"), capture=True)
    info = json.loads(result.stdout)
    if info.get("name") != sandbox or info.get("workspace") != workspace:
        raise ValueError("sandbox identity changed during cleanup")
    rules = info.get("policy", {}).get("network_policies", {})
    return any(rule.get("name") == RULE for rule in rules.values())


def _cleanup_sandbox(gateway, workspace, sandbox):
    _gateway(gateway)
    if _sandbox_policy(gateway, workspace, sandbox):
        _run(_openshell_args(gateway, workspace, "policy", "update", sandbox,
                            "--remove-rule", RULE, "--wait"))
        if _sandbox_policy(gateway, workspace, sandbox):
            raise ValueError("host-build network rule remains after removal")
    for remote in ("/sandbox/.ssh/osac-host-build", "/sandbox/.ssh/osac-host-build-known_hosts", "/sandbox/.ssh/osac-host-build-user"):
        _run(_openshell_args(gateway, workspace, "sandbox", "exec", "--name", sandbox,
                            "--no-login-shell", "--no-tty", "--", "/usr/bin/rm", "-f", remote))
        _run(_openshell_args(gateway, workspace, "sandbox", "exec", "--name", sandbox,
                            "--no-login-shell", "--no-tty", "--", "/usr/bin/test", "!", "-e", remote))


def prepare(paths, gateway, workspace, sandbox, checkout):
    lock = _lock(paths)
    try:
        if not paths["config"].exists():
            _cleanup_sandbox(gateway, workspace, sandbox)
            return
        _bound(paths, gateway, workspace, sandbox, checkout)
        identity = _sandbox_matches(gateway, workspace, sandbox)
        policy = _openshell_args(gateway, workspace, "policy", "update", sandbox,
                                "--add-endpoint", "host.openshell.internal:22::tcp",
                                "--binary", "/usr/bin/ssh", "--rule-name", RULE, "--wait")
        try:
            _run(policy)
            _run(_openshell_args(gateway, workspace, "sandbox", "exec", "--name", sandbox,
                                "--no-login-shell", "--no-tty", "--", "/usr/bin/test", "!", "-L", "/sandbox/.ssh"))
            _run(_openshell_args(gateway, workspace, "sandbox", "exec", "--name", sandbox,
                                "--no-login-shell", "--no-tty", "--", "/usr/bin/install", "-d", "-m", "700", "/sandbox/.ssh"))
            for local, remote in ((paths["key"], "/sandbox/.ssh/osac-host-build"),
                                  (paths["known_hosts"], "/sandbox/.ssh/osac-host-build-known_hosts"),
                                  (paths["user"], "/sandbox/.ssh/osac-host-build-user")):
                _bound(paths, gateway, workspace, sandbox, checkout)
                if _sandbox_matches(gateway, workspace, sandbox) != identity:
                    raise ValueError("sandbox was recreated during key upload")
                for target in ("/sandbox/.ssh", remote):
                    _run(_openshell_args(gateway, workspace, "sandbox", "exec", "--name", sandbox,
                                        "--no-login-shell", "--no-tty", "--", "/usr/bin/test", "!", "-L", target))
                _run(_openshell_args(gateway, workspace, "sandbox", "upload", sandbox, str(local), remote))
            _run(_openshell_args(gateway, workspace, "sandbox", "exec", "--name", sandbox,
                                "--no-login-shell", "--no-tty", "--", "/usr/bin/chmod", "600",
                                "/sandbox/.ssh/osac-host-build", "/sandbox/.ssh/osac-host-build-known_hosts",
                                "/sandbox/.ssh/osac-host-build-user"))
            ssh = ["/usr/bin/ssh", "-F", "/dev/null", "-i", "/sandbox/.ssh/osac-host-build",
                   "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes",
                   "-o", "HostKeyAlgorithms=ssh-ed25519", "-o", "HostKeyAlias=osac-host-build",
                   "-o", "UserKnownHostsFile=/sandbox/.ssh/osac-host-build-known_hosts",
                   "-o", "GlobalKnownHostsFile=/dev/null", "-o", "ClearAllForwardings=yes", "-T",
                   pwd.getpwuid(os.getuid()).pw_name + "@host.openshell.internal", "probe"]
            result = _run(_openshell_args(gateway, workspace, "sandbox", "exec", "--name", sandbox,
                                          "--no-login-shell", "--no-tty", "--", *ssh), capture=True)
            if result.stdout.strip() != READY:
                raise ValueError("host-build probe returned an unexpected response")
        except Exception as original:
            recovery_errors = []
            # Deny the host capability before attempting sandbox cleanup. A
            # failed upload or policy operation may have left a live key there.
            try:
                remove_authorized_key(paths["authorized"])
            except Exception as error:
                recovery_errors.append("host key revocation: " + str(error))
            try:
                _cleanup_sandbox(gateway, workspace, sandbox)
            except Exception as error:
                recovery_errors.append("sandbox cleanup: " + str(error))
            if recovery_errors:
                raise ValueError("prepare failed; " + "; ".join(recovery_errors)) from original
            raise
    finally:
        os.close(lock)


def revoke(paths, gateway=None, workspace=None, sandbox=None):
    lock = _lock(paths)
    try:
        _revoke_locked(paths, gateway, workspace, sandbox)
    finally:
        os.close(lock)


def _revoke_locked(paths, gateway=None, workspace=None, sandbox=None):
    # The host authorization is the actual capability. Remove it before
    # parsing possibly damaged state or evaluating a mistaken CLI binding.
    if paths["ssh"].exists():
        remove_authorized_key(paths["authorized"])
    data = _config(paths) if paths["config"].exists() else {}
    if data:
        for name, requested in (("gateway", gateway), ("workspace", workspace), ("sandbox", sandbox)):
            if requested is not None and requested != data[name]:
                raise ValueError("revoke binding does not match stored " + name)
        gateway, workspace, sandbox = data["gateway"], data["workspace"], data["sandbox"]
    for name in ("config", "key", "known_hosts", "user"):
        path = paths[name]
        if path.exists() or path.is_symlink():
            _require_regular(path)
            path.unlink()
    public = paths["key"].with_suffix(".pub")
    if public.exists() or public.is_symlink():
        _require_regular(public)
        public.unlink()
    if data:
        # Cleanup failure is deliberately surfaced; the key is already revoked.
        _cleanup_sandbox(gateway, workspace, sandbox)


def main(argv=None):
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("enable", "prepare", "status", "revoke", "upgrade"))
    parser.add_argument("--gateway")
    parser.add_argument("--workspace")
    parser.add_argument("--sandbox")
    parser.add_argument("--checkout", default=os.getcwd())
    args = parser.parse_args(argv)
    if args.action not in ("revoke", "upgrade"):
        args.gateway = args.gateway or "openshell"
        args.workspace = args.workspace or "default"
        args.sandbox = args.sandbox or "osac-carbonite"
    home = pathlib.Path.home()
    paths = _paths(home)
    try:
        if args.action == "enable":
            enable(paths, args.gateway, args.workspace, args.sandbox, args.checkout)
        elif args.action == "prepare":
            prepare(paths, args.gateway, args.workspace, args.sandbox, args.checkout)
        elif args.action == "status":
            _bound(paths, args.gateway, args.workspace, args.sandbox, args.checkout)
        elif args.action == "revoke":
            revoke(paths, args.gateway, args.workspace, args.sandbox)
        elif args.action == "upgrade":
            upgrade(paths)
    except (ValueError, OSError, KeyError, subprocess.CalledProcessError, json.JSONDecodeError) as error:
        print("host-build " + args.action + " failed: " + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
