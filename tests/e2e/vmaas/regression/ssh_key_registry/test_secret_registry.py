"""SSH-key Secret lifecycle and validation regressions."""

from __future__ import annotations

import base64
import subprocess
from pathlib import Path

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.helpers import unique_name
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import poll_until, run, run_unchecked
from tests.e2e.core.wait_kinds import WaitResourceKind
from tests.e2e.vmaas.regression.ssh_key_registry.helpers import (
    _assert_key_secret,
    _assert_tenant_user,
    _cleanup_secrets,
    _create_owned_secret,
    _definite_create_rejection,
    _hygiene_needles,
    _hygiene_scan,
    _rpc_status,
    _secrets,
    _shared_secrets,
    _wait_for_secret_absent,
)

pytestmark = pytest.mark.regression


def test_ssh_key_secret_lifecycle(jwt_cli_user: OsacCLI, ssh_key_dir: Path) -> None:
    """TC-01: register, inspect, reject a duplicate, delete and reuse a key name."""
    cli = jwt_cli_user
    _assert_tenant_user(cli)
    key_path = ssh_key_dir / "id_ed25519"
    run("ssh-keygen", "-t", "ed25519", "-f", str(key_path), "-N", "", "-C", "osac-e2e")
    public_key_path = key_path.with_suffix(".pub")
    public_key = public_key_path.read_text(encoding="utf-8")
    name = unique_name("e2e-ssh-lifecycle")
    owned: dict[str, str] = {}
    uncertain: set[str] = set()

    def create() -> str:
        return _create_owned_secret(
            cli, owned, uncertain, name=name, secret_type="ssh-public-key", field="public_key", path=public_key_path
        )

    if _secrets(cli, name):
        raise AssertionError("Test Secret name already exists")
    try:
        original_id = create()
        listed = [item for item in _secrets(cli) if item.get("metadata", {}).get("name") == name]
        if len(listed) != 1:
            raise AssertionError("Expected exactly one registered Secret in the list")
        found = _secrets(cli, name)
        if len(found) != 1:
            raise AssertionError("Expected exactly one registered Secret by name")
        _assert_key_secret(found[0], name=name, secret_id=original_id, public_key=public_key)

        with pytest.raises(subprocess.CalledProcessError) as error:
            create()
        detail = f"{error.value.stdout or ''} {error.value.stderr or ''}".lower()
        if not any(term in detail for term in ("already exists", "already exist", "alreadyexists", "duplicate")):
            raise AssertionError("Expected duplicate-name conflict")
        found = _secrets(cli, name)
        if len(found) != 1:
            raise AssertionError("Duplicate registration changed the number of Secrets")
        _assert_key_secret(found[0], name=name, secret_id=original_id, public_key=public_key)

        cli.delete_secret(name=original_id)
        _wait_for_secret_absent(cli, name)
        owned.pop(original_id)
        new_id = create()
        found = _secrets(cli, name)
        if len(found) != 1:
            raise AssertionError("Expected exactly one Secret after name reuse")
        _assert_key_secret(found[0], name=name, secret_id=new_id, public_key=public_key)
    finally:
        _cleanup_secrets(cli, owned, uncertain)


@pytest.mark.parametrize("case", ["malformed", "private-key-like", "empty", "two-keys", "trailing", "wrong-field"])
def test_invalid_ssh_key_secret_is_rejected(jwt_cli_user: OsacCLI, ssh_key_dir: Path, case: str) -> None:
    """TC-09a: invalid key payloads leave no Secret, with a valid-key control."""
    cli = jwt_cli_user
    key_path = ssh_key_dir / "id_ed25519"
    other_key_path = ssh_key_dir / "id_ed25519_other"
    for path in (key_path, other_key_path):
        run("ssh-keygen", "-t", "ed25519", "-f", str(path), "-N", "", "-C", "osac-e2e")
    public_key_path = key_path.with_suffix(".pub")
    public_key = public_key_path.read_text(encoding="utf-8")
    other_key = other_key_path.with_suffix(".pub").read_text(encoding="utf-8")
    payloads = {
        "malformed": "not an OpenSSH public key\n",
        "private-key-like": "OpenSSH private-key-like test text\nTEST-ONLY-NOT-A-KEY\n",
        "empty": "",
        "two-keys": f"{public_key}{other_key}",
        "trailing": f"{public_key} trailing-content\n",
        "wrong-field": public_key,
    }
    payload_path = ssh_key_dir / "invalid.pub"
    payload_path.write_text(payloads[case], encoding="utf-8")
    control_name = unique_name("e2e-ssh-valid")
    invalid_name = unique_name("e2e-ssh-invalid")
    if _secrets(cli, control_name) or _secrets(cli, invalid_name):
        raise AssertionError("Test Secret name already exists")

    owned: dict[str, str] = {}
    uncertain: set[str] = set()
    try:
        control_id = _create_owned_secret(
            cli,
            owned,
            uncertain,
            name=control_name,
            secret_type="ssh-public-key",
            field="public_key",
            path=public_key_path,
        )
        control = _secrets(cli, control_name)
        if len(control) != 1:
            raise AssertionError("Valid control Secret was not created")
        _assert_key_secret(control[0], name=control_name, secret_id=control_id, public_key=public_key)

        field = "key" if case == "wrong-field" else "public_key"
        with pytest.raises(subprocess.CalledProcessError) as error:
            _create_owned_secret(
                cli, owned, uncertain, name=invalid_name, secret_type="ssh-public-key", field=field, path=payload_path
            )
        detail = f"{error.value.stdout or ''} {error.value.stderr or ''}".lower()
        if _rpc_status(detail) != "invalidargument":
            raise AssertionError("Secret creation failed for a reason unrelated to invalid key data")
        if _secrets(cli, invalid_name):
            uncertain.add(invalid_name)
            raise AssertionError("Rejected key payload left a Secret behind")
    finally:
        _cleanup_secrets(cli, owned, uncertain)


def test_shared_tenant_rejects_ssh_key(private_grpc: GRPCClient, ssh_key_dir: Path) -> None:
    """TC-07c (partial): reject an SSH-key Secret in the shared tenant."""
    path = ssh_key_dir / "id_ed25519"
    run("ssh-keygen", "-t", "ed25519", "-f", str(path), "-N", "", "-C", "osac-e2e")
    public_key = path.with_suffix(".pub").read_bytes()
    name = unique_name("e2e-ssh-shared")
    if _shared_secrets(private_grpc, name):
        raise AssertionError("Test Secret name already exists in the shared tenant")
    owned_id: str | None = None
    uncertain = False
    try:
        uncertain = True
        try:
            response = private_grpc.call(
                service=f"{PRIVATE_API}.Secrets/Create",
                data={
                    "object": {
                        "metadata": {"name": name, "tenant": "shared"},
                        "type": "SECRET_TYPE_SSH_PUBLIC_KEY",
                        "data": {"public_key": base64.b64encode(public_key).decode()},
                    }
                },
            )
        except subprocess.CalledProcessError as exc:
            detail = f"{exc.stdout or ''} {exc.stderr or ''}".lower()
            if (
                _rpc_status(detail) != "invalidargument"
                or "ssh public-key secrets cannot be created in the shared tenant" not in detail
            ):
                if _definite_create_rejection(detail):
                    uncertain = False
                raise AssertionError("Shared Secret create was rejected for an unexpected reason") from exc
        else:
            owned_id = response.get("object", {}).get("id")
            if owned_id:
                uncertain = False
            raise AssertionError("Shared-tenant SSH-key Secret was accepted")
        if _shared_secrets(private_grpc, name):
            raise AssertionError("Rejected shared-tenant SSH-key Secret persisted")
        uncertain = False
    finally:
        if owned_id:
            private_grpc.call(service=f"{PRIVATE_API}.Secrets/Delete", data={"id": owned_id})
            poll_until(
                fn=lambda: all(item.get("id") != owned_id for item in _shared_secrets(private_grpc, name)),
                until=bool,
                retries=30,
                delay=2,
                description=f"Shared Secret {name} removal",
                resource_kind=WaitResourceKind.SECRET,
                resource_id=owned_id,
            )
        if uncertain:
            raise RuntimeError(f"Shared Secret {name} has an uncertain creation outcome; inspect ownership")


def test_cli_output_hides_private_key_and_invalid_payload(jwt_cli_user: OsacCLI, ssh_key_dir: Path) -> None:
    """NFR-SSH-01 (partial): CLI responses do not expose private keys or invalid payloads."""
    __tracebackhide__ = True
    _assert_tenant_user(jwt_cli_user)
    key_path = ssh_key_dir / "id_ed25519"
    run("ssh-keygen", "-t", "ed25519", "-f", str(key_path), "-N", "", "-C", "osac-e2e")
    private_key = key_path.read_bytes()
    marker = unique_name("e2e-ssh-hygiene-marker").encode()
    payload = b"Synthetic private-key-like test text\n" + marker + b"\n"
    payload_path = ssh_key_dir / "invalid.pub"
    payload_path.write_bytes(payload)
    needles = _hygiene_needles(private_key, payload, marker)
    canary = b"\n".join(value for values in needles.values() for value in values)
    if _hygiene_scan(canary, needles) != set(needles):
        raise AssertionError("The hygiene scanner did not identify every synthetic canary category")
    key_name = unique_name("e2e-ssh-hygiene-key")
    invalid_name = unique_name("e2e-ssh-hygiene-invalid")
    if _secrets(jwt_cli_user, key_name) or _secrets(jwt_cli_user, invalid_name):
        raise AssertionError("Test Secret name already exists")
    owned: dict[str, str] = {}
    uncertain: set[str] = set()
    captures: list[tuple[str, str]] = []
    try:
        created_output: list[str] = []
        try:
            key_id = _create_owned_secret(
                jwt_cli_user,
                owned,
                uncertain,
                name=key_name,
                secret_type="ssh-public-key",
                field="public_key",
                path=key_path.with_suffix(".pub"),
                captured=created_output,
            )
        except Exception:
            raise AssertionError("Test key registration failed; output withheld") from None
        captures.append(("create", created_output[0]))
        for label, args in (
            ("get-json", ("get", "secret", key_id, "-o", "json")),
            ("get-yaml", ("get", "secret", key_id, "-o", "yaml")),
            ("list-json", ("get", "secrets", "-o", "json")),
            ("list-table", ("get", "secrets")),
        ):
            output, rc = run_unchecked(
                jwt_cli_user.binary, "--config", jwt_cli_user.config_dir, *args, include_stderr=True
            )
            if rc:
                raise AssertionError(f"CLI inspection {label} failed")
            captures.append((label, output))
        try:
            _create_owned_secret(
                jwt_cli_user,
                owned,
                uncertain,
                name=invalid_name,
                secret_type="ssh-public-key",
                field="public_key",
                path=payload_path,
            )
        except subprocess.CalledProcessError as exc:
            detail = f"{exc.stdout or ''} {exc.stderr or ''}"
            if _rpc_status(detail) != "invalidargument":
                raise AssertionError("Invalid payload failed for an unexpected reason") from None
            captures.append(("invalid-key-error", detail))
        else:
            raise AssertionError("Invalid key payload was unexpectedly accepted")
        if _secrets(jwt_cli_user, invalid_name):
            uncertain.add(invalid_name)
            raise AssertionError("Invalid key payload left a Secret behind")
        for label, data in captures:
            found = _hygiene_scan(data.encode(), needles)
            if found:
                raise AssertionError(f"Sensitive test material appeared in {label}: {', '.join(sorted(found))}")
    finally:
        try:
            _cleanup_secrets(jwt_cli_user, owned, uncertain)
        except Exception:
            raise RuntimeError(f"Could not clean test Secrets {key_name} and {invalid_name}; output withheld") from None
