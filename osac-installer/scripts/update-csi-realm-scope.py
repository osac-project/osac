#!/usr/bin/env python3
"""Explicit administrator-run repair of the bundled CSI realm-role scope.

No login, grants, realm import or client updates. See
../../docs/guides/admin/keycloak-csi-role-scope.md for authorization,
serialized administration, uncertain outcomes and rollback limits.
"""

from __future__ import annotations

import argparse
import fcntl
import hashlib
import http.client
import json
import os
import re
import signal
import ssl
import stat
import sys
import tempfile
from pathlib import Path
from types import FrameType, TracebackType
from typing import NoReturn, Self
from urllib.parse import urlsplit

REALM = "osac"
NAME = "osac-csi-driver"
REALM_ID = "01e6c34f-6906-4907-9cbd-2dec7a6a0e1d"
CLIENT_ID = "d5e6f7a8-b9c0-4d1e-2f3a-567890abcdef"
ROLE_ID = "c4d5e6f7-a8b9-0c1d-2e3f-456789abcdef"
ADMIN = f"/admin/realms/{REALM}"
SCOPE_PATH = f"{ADMIN}/clients/{CLIENT_ID}/scope-mappings/realm"
MAX_RESPONSE = 1 << 20
ROLE = {"id": ROLE_ID, "name": NAME, "containerId": REALM_ID, "clientRole": False, "composite": False}
CLIENT = {
    "id": CLIENT_ID,
    "clientId": NAME,
    "protocol": "openid-connect",
    "clientAuthenticatorType": "client-secret",
    "enabled": True,
    "serviceAccountsEnabled": True,
    "fullScopeAllowed": False,
    "publicClient": False,
    "bearerOnly": False,
    "standardFlowEnabled": False,
    "implicitFlowEnabled": False,
    "directAccessGrantsEnabled": False,
}


class Failure(Exception):
    """Only constant, credential-free diagnostics may be passed to this class."""


def fail(message: str) -> NoReturn:
    raise Failure(message)


def fingerprint(value: object) -> str:
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def unique_object(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result = {}
    for key, value in pairs:
        if key in result:
            fail("duplicate JSON keys; response/record withheld")
        result[key] = value
    return result


def decode(data: bytes) -> object:
    try:
        return json.loads(
            data,
            object_pairs_hook=unique_object,
            parse_constant=lambda _: fail("non-finite JSON value; content withheld"),
        )
    except (ValueError, UnicodeError):
        fail("invalid JSON; response/record withheld")


def private_parent(path: Path) -> None:
    parent = path.parent
    info = parent.stat()
    if (
        parent.resolve() != parent.absolute()
        or not stat.S_ISDIR(info.st_mode)
        or info.st_uid != os.getuid()
        or stat.S_IMODE(info.st_mode) != 0o700
    ):
        fail("credential/record directory must be owned by this user, mode 0700, without symlinks")


def private_fd(fd: int) -> None:
    info = os.fstat(fd)
    if (
        not stat.S_ISREG(info.st_mode)
        or info.st_uid != os.getuid()
        or stat.S_IMODE(info.st_mode) != 0o600
        or info.st_nlink != 1
    ):
        fail("credential/record must be a regular owned mode-0600 file without links")


def read_private(path: Path) -> bytes:
    private_parent(path)
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as stream:
        private_fd(stream.fileno())
        data = stream.read(MAX_RESPONSE + 1)
    if len(data) > MAX_RESPONSE:
        fail("credential/record exceeds the size limit")
    return data


def server_url(value: str) -> str:
    parsed = urlsplit(value)
    if (
        parsed.scheme != "https"
        or not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
        or "?" in value
        or "#" in value
        or not re.fullmatch(r"[A-Za-z0-9.:-]+", parsed.hostname)
        or not re.fullmatch(r"(?:/[A-Za-z0-9_-]+)*/?", parsed.path)
        or any(ord(character) <= 32 or ord(character) == 127 for character in value)
        or (parsed.port is not None and not 1 <= parsed.port <= 65535)
    ):
        fail("server must be an HTTPS root/context URL without credentials, query, fragment or traversal")
    return value.rstrip("/")


def role_view(value: object) -> dict[str, object]:
    if (
        not isinstance(value, dict)
        or not isinstance(value.get("id"), str)
        or not re.fullmatch(r"[A-Za-z0-9_-]{1,128}", value["id"])
        or not isinstance(value.get("name"), str)
        or not value["name"]
        or value.get("clientRole") is not False
        or type(value.get("composite")) is not bool
        or value.get("containerId") != REALM_ID
    ):
        fail("unexpected realm-role identity or type")
    return {key: value[key] for key in ROLE}


def client_view(value: object) -> dict[str, object]:
    if not isinstance(value, dict):
        fail("unexpected CSI client representation")
    for key, expected in CLIENT.items():
        if type(value.get(key)) is not type(expected) or value[key] != expected:
            fail("unexpected CSI client identity, type, flow or fullScopeAllowed state")
    result = dict(CLIENT)
    for key in ("defaultClientScopes", "optionalClientScopes"):
        scopes = value.get(key)
        if (
            not isinstance(scopes, list)
            or not all(isinstance(item, str) for item in scopes)
            or len(set(scopes)) != len(scopes)
        ):
            fail("unexpected CSI client scope configuration")
        result[key] = sorted(scopes)
    return result


class AdminAPI:
    def __init__(self, server: str, token_file: str, ca_file: str | None, timeout: int) -> None:
        self.server = server_url(server)
        self.url = urlsplit(self.server)
        token = read_private(Path(token_file)).decode("ascii").strip()
        if len(token) > 65536 or not re.fullmatch(r"[A-Za-z0-9._~+/-]+=*", token):
            fail("token file must contain one raw bearer token; value withheld")
        self.token = token
        self.tls = ssl.create_default_context()
        self.tls.minimum_version = ssl.TLSVersion.TLSv1_2
        if ca_file:
            self.tls.load_verify_locations(cafile=ca_file)
        self.timeout = timeout

    def request(self, method: str, path: str) -> object:
        # HTTPSConnection neither follows redirects nor consults proxy env vars.
        connection = http.client.HTTPSConnection(
            self.url.hostname, self.url.port, context=self.tls, timeout=self.timeout
        )
        body = None
        headers = {"Authorization": "Bearer " + self.token, "Accept": "application/json"}
        expected = 200
        if method != "GET":
            if method not in {"POST", "DELETE"} or path != SCOPE_PATH:
                fail("unsupported mutation")
            # A null DELETE body means delete all scopes in Keycloak. Always
            # construct exactly this one-role payload, never forward a response.
            body = json.dumps([{"id": ROLE_ID, "name": NAME}]).encode()
            headers["Content-Type"] = "application/json"
            expected = 204
        try:
            connection.request(method, self.url.path + path, body=body, headers=headers)
            response = connection.getresponse()
            if response.status != expected:
                fail(f"Keycloak rejected request (HTTP {response.status}); response withheld")
            data = response.read(MAX_RESPONSE + 1)
            if len(data) > MAX_RESPONSE:
                fail("Keycloak response exceeds the size limit")
            if method == "GET":
                if response.getheader("Content-Type", "").split(";")[0].strip() != "application/json":
                    fail("unexpected Keycloak response content type")
                return decode(data)
            if data:
                fail("unexpected mutation response body; content withheld")
            return None
        except (OSError, http.client.HTTPException):
            fail("HTTPS request failed; reconcile any pending operation record before retrying")
        finally:
            connection.close()

    def snapshot(self) -> dict[str, object]:
        clients = self.request("GET", f"{ADMIN}/clients?clientId={NAME}&search=false&max=2")
        if not isinstance(clients, list) or len(clients) != 1:
            fail("CSI client lookup must return exactly one matching client")
        if not isinstance(clients[0], dict) or clients[0].get("id") != CLIENT_ID or clients[0].get("clientId") != NAME:
            fail("unexpected CSI client lookup identity")
        client = client_view(self.request("GET", f"{ADMIN}/clients/{CLIENT_ID}"))
        role = role_view(self.request("GET", f"{ADMIN}/roles/{NAME}"))
        if role != ROLE:
            fail("unexpected CSI realm-role identity or type")
        mappings = self.request("GET", SCOPE_PATH)
        if not isinstance(mappings, list):
            fail("direct realm-scope response must be a list")
        scopes, names = {}, set()
        for mapping in mappings:
            view = role_view(mapping)
            if view["id"] in scopes or view["name"] in names:
                fail("duplicate direct realm-scope identity")
            if (view["id"] == ROLE_ID or view["name"] == NAME) and view != ROLE:
                fail("conflicting CSI scope identity")
            scopes[view["id"]] = fingerprint(view)
            names.add(view["name"])
        # No credentials, arbitrary attributes or full client representations
        # enter a record, diagnostic or snapshot fingerprint.
        return {"client": fingerprint(client), "role": fingerprint(role), "scopes": scopes}


def with_mapping(state: dict[str, object]) -> dict[str, object]:
    return {**state, "scopes": {**state["scopes"], ROLE_ID: fingerprint(ROLE)}}


class OperationRecord:
    def __init__(self, path: str) -> None:
        self.path = Path(path)
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}", self.path.name):
            fail("invalid operation record name")
        self.data = None
        self.lock = None

    def __enter__(self) -> Self:
        private_parent(self.path)
        self.lock = os.open(str(self.path) + ".lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600)
        try:
            private_fd(self.lock)
            fcntl.flock(self.lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            try:
                self.data = decode(read_private(self.path))
            except FileNotFoundError:
                self.data = None
        except BaseException:
            os.close(self.lock)
            raise
        return self

    def __exit__(
        self, _type: type[BaseException] | None, _value: BaseException | None, _traceback: TracebackType | None
    ) -> None:
        os.close(self.lock)

    def save(self, data: dict[str, object]) -> None:
        # Same-record processes serialize on flock. An administrator must also
        # serialize remote changes and operations using other record files.
        if self.data is not None and decode(read_private(self.path)) != self.data:
            fail("operation record changed during this invocation")
        fd, temporary = tempfile.mkstemp(prefix="." + self.path.name + ".", dir=self.path.parent)
        try:
            with os.fdopen(fd, "w") as stream:
                json.dump(data, stream, sort_keys=True, indent=2)
                stream.write("\n")
                stream.flush()
                os.fsync(stream.fileno())
            if self.data is None:
                os.link(temporary, self.path)  # Exclusive creation: never clobber an existing record.
                os.unlink(temporary)
            else:
                os.replace(temporary, self.path)
            directory = os.open(self.path.parent, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
            self.data = data
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)


def validate_record(record: object, server: str) -> dict[str, object]:
    target = {"version": 1, "server": server, "realm": REALM, "client_id": CLIENT_ID, "role_id": ROLE_ID}
    if (
        not isinstance(record, dict)
        or set(record) != set(target) | {"phase", "before", "after"}
        or any(type(record.get(key)) is not type(value) or record[key] != value for key, value in target.items())
    ):
        fail("invalid operation record or different target")
    phase = record["phase"]
    if phase not in ("prepared", "applied", "noop", "rollback-pending", "rolled-back"):
        fail("invalid operation phase")
    for key in ("before", "after"):
        state = record[key]
        if (
            not isinstance(state, dict)
            or set(state) != {"client", "role", "scopes"}
            or not isinstance(state["scopes"], dict)
            or state["role"] != fingerprint(ROLE)
            or not isinstance(state["client"], str)
            or not re.fullmatch(r"[a-f0-9]{64}", state["client"])
        ):
            fail("invalid recorded target state")
        for identifier, digest in state["scopes"].items():
            if (
                not isinstance(identifier, str)
                or not re.fullmatch(r"[A-Za-z0-9_-]{1,128}", identifier)
                or not isinstance(digest, str)
                or not re.fullmatch(r"[a-f0-9]{64}", digest)
            ):
                fail("invalid recorded direct scopes")
    if phase == "noop":
        if ROLE_ID not in record["before"]["scopes"] or record["before"] != record["after"]:
            fail("invalid non-owning operation record")
    elif ROLE_ID in record["before"]["scopes"] or with_mapping(record["before"]) != record["after"]:
        fail("operation record does not own exactly the added CSI binding")
    if phase in ("prepared", "rollback-pending"):
        fail("uncertain operation; administrator reconciliation required before a new operation")
    return record


def require_state(api: AdminAPI, expected: dict[str, object]) -> None:
    if api.snapshot() != expected:
        fail("target state changed; no further mutation, reconcile any pending operation record")


def update(api: AdminAPI, path: str, rollback: bool) -> tuple[str, bool]:
    with OperationRecord(path) as store:
        record = validate_record(store.data, api.server) if store.data is not None else None
        if rollback and record is None:
            fail("rollback requires this operation's existing record")
        current = api.snapshot()
        if rollback:
            if record["phase"] in ("noop", "rolled-back"):
                if current != record["before"]:
                    fail("record is stale; a non-owning/consumed record cannot delete a later addition")
                return "rollback-noop", False
            if current != record["after"]:
                fail("rollback target differs from the recorded state")
            store.save({**record, "phase": "rollback-pending"})
            require_state(api, current)
            api.request("DELETE", SCOPE_PATH)
            require_state(api, record["before"])
            store.save({**record, "phase": "rolled-back"})
            return "rolled-back", True
        if record:
            if record["phase"] not in ("applied", "noop") or current != record["after"]:
                fail("operation record is stale or consumed; review state before using a new record")
            return "already-present", False
        present = ROLE_ID in current["scopes"]
        after = with_mapping(current)
        record = {
            "version": 1,
            "server": api.server,
            "realm": REALM,
            "client_id": CLIENT_ID,
            "role_id": ROLE_ID,
            "before": current,
            "after": after,
            "phase": "noop" if present else "prepared",
        }
        store.save(record)
        if present:
            return "already-present", False
        require_state(api, current)
        api.request("POST", SCOPE_PATH)
        require_state(api, after)
        store.save({**record, "phase": "applied"})
        return "added", True


class Parser(argparse.ArgumentParser):
    def error(self, _message: str) -> NoReturn:
        fail("invalid arguments; use --help (argument values withheld)")


def deadline(_signal: int, _frame: FrameType | None) -> NoReturn:
    fail("operation deadline reached; reconcile any pending record before retrying")


def main() -> int:
    parser = Parser(description=__doc__, allow_abbrev=False)
    parser.add_argument("--server", required=True, help="Keycloak HTTPS root/context URL")
    parser.add_argument("--token-file", required=True, help="owned mode-0600 raw administrator bearer file")
    parser.add_argument("--ca-file", help="additional trusted PEM CA bundle")
    parser.add_argument("--timeout", type=int, default=10, help="request timeout in seconds (1-30, default 10)")
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument("--apply", action="store_true", help="explicitly add only the missing CSI scope")
    modes.add_argument("--rollback", action="store_true", help="remove only this operation's owned addition")
    parser.add_argument("--record", help="operation JSON file in an owned mode-0700 directory")
    try:
        signal.signal(signal.SIGALRM, deadline)
        signal.alarm(120)
        args = parser.parse_args()
        if not 1 <= args.timeout <= 30 or bool(args.record) != (args.apply or args.rollback):
            fail("mutations require --record; dry-run takes no record; timeout must be 1-30 seconds")
        api = AdminAPI(args.server, args.token_file, args.ca_file, args.timeout)
        if args.apply or args.rollback:
            result, changed = update(api, args.record, args.rollback)
        else:
            result = "already-present" if ROLE_ID in api.snapshot()["scopes"] else "would-add"
            changed = False
        print(json.dumps({"result": result, "changed": changed}))
        return 0
    except Failure as error:
        print(f"ERROR: {error}", file=sys.stderr)
    except (Exception, KeyboardInterrupt):
        # SSL, filesystem, parse and transport errors may contain sensitive
        # input/response data. Never emit their text or a traceback.
        print("ERROR: operation failed; check inputs and reconcile any pending record before retrying", file=sys.stderr)
    finally:
        signal.alarm(0)
    return 1


if __name__ == "__main__":
    sys.exit(main())
