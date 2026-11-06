"""NetBox device client for E2E inventory fixtures.

Thin fixture-facing wrapper around the official Python client (pynetbox).
All device CRUD goes through pynetbox; the tests never hand-roll NetBox
REST calls. Value resolution (deployed BMF configuration versus debug env
overrides) lives in the inventory fixture layer (`inventory.py`), not here.
"""

from __future__ import annotations

import contextlib
import os
import tempfile
from typing import Any

import pynetbox
import requests
from pynetbox.core.api import Api
from pynetbox.core.endpoint import Endpoint


def _status_value(status: object) -> str:
    """Normalize a NetBox device status.

    NetBox may return a plain string (brief responses) or a choice object
    (``{"value": ..., "label": ...}`` / pynetbox Record with ``.value``).
    """
    if isinstance(status, str):
        return status
    if isinstance(status, dict):
        value = status.get("value", "")
        return value if isinstance(value, str) else ""
    value = getattr(status, "value", None)
    return value if isinstance(value, str) else ""


class NetBoxClient:
    """NetBox device CRUD on top of pynetbox.

    Construction performs no network calls. TLS is configured through a
    dedicated ``requests.Session``: a temporary CA bundle file when
    ``ca_pem`` is given, disabled verification when ``insecure`` is set,
    and default system verification otherwise. The API token is applied
    per request by pynetbox and must never appear in logs or exceptions.
    """

    def __init__(
        self, *, url: str, token: str, ca_pem: str | None = None, insecure: bool = False, api: Api | None = None
    ) -> None:
        # ``api`` injection keeps unit tests free of pynetbox internals.
        self._api = api if api is not None else pynetbox.api(url, token=token)
        self._ca_file: str | None = None
        if api is None:
            session = requests.Session()
            if ca_pem is not None:
                fd, self._ca_file = tempfile.mkstemp(prefix="osac-netbox-ca-", suffix=".pem")
                with os.fdopen(fd, "w") as handle:
                    handle.write(ca_pem)
                session.verify = self._ca_file
            elif insecure:
                session.verify = False
            self._api.http_session = session

    @property
    def api(self) -> Api:
        return self._api

    def close(self) -> None:
        """Remove the temporary CA bundle, if any."""
        if self._ca_file is not None:
            with contextlib.suppress(OSError):
                os.unlink(self._ca_file)
            self._ca_file = None

    def _error(self, operation: str, exc: pynetbox.RequestError) -> RuntimeError:
        """Build a sanitized RuntimeError: method, endpoint, status code only.

        The token travels in the Authorization header, never the URL, but
        response bodies and request payloads are dropped to guarantee no
        credential or tenant data leaks through exception messages.
        """
        req = getattr(exc, "req", None)
        status = getattr(req, "status_code", "?")
        url = getattr(req, "url", None) or getattr(exc, "base", "<unknown endpoint>")
        method = getattr(getattr(req, "request", None), "method", "?")
        return RuntimeError(f"NetBox {operation} failed: {method} {url} -> HTTP {status}")

    def _resolve_id(self, endpoint: Endpoint, *, slug: str, kind: str) -> int:
        """Resolve a site/role slug to its numeric id; numeric ids pass through."""
        if slug.isdigit():
            return int(slug)
        record = endpoint.get(slug=slug)
        if record is None:
            raise RuntimeError(f"NetBox {kind} {slug!r} was not found")
        return int(record.id)

    def create_device(
        self, *, name: str, role: str, site: str, device_type: str, custom_fields: dict[str, Any] | None = None
    ) -> str:
        """Create a device in the OSAC pool free state (``staged``); return its id."""
        site_id = self._resolve_id(self._api.dcim.sites, slug=site, kind="site")
        role_id = self._resolve_id(self._api.dcim.device_roles, slug=role, kind="device role")
        type_id = self._resolve_id(self._api.dcim.device_types, slug=device_type, kind="device type")
        try:
            device = self._api.dcim.devices.create(
                name=name,
                role=role_id,
                site=site_id,
                device_type=type_id,
                status="staged",
                custom_fields=custom_fields or {},
            )
        except pynetbox.RequestError as exc:
            raise self._error("create_device", exc) from exc
        # create() returns a list only for bulk payloads; this client always
        # creates single devices.
        if isinstance(device, list):
            raise RuntimeError(f"NetBox create_device returned a bulk result for {name!r}")
        return str(device.id)

    def delete_device(self, device_id: str) -> None:
        """Delete a device; a missing device is not an error (idempotent)."""
        try:
            device = self._api.dcim.devices.get(device_id)
            if device is None:
                return
            self._api.dcim.devices.delete([device])
        except pynetbox.RequestError as exc:
            raise self._error("delete_device", exc) from exc

    def get_device_status(self, device_id: str) -> str:
        """Return the device status value, or "" when the device is gone."""
        try:
            device = self._api.dcim.devices.get(device_id)
        except pynetbox.RequestError as exc:
            raise self._error("get_device_status", exc) from exc
        if device is None:
            return ""
        return _status_value(getattr(device, "status", ""))

    def get_device(self, device_id: str) -> dict[str, Any]:
        """Return the full device as a dict (custom_fields access)."""
        try:
            device = self._api.dcim.devices.get(device_id)
        except pynetbox.RequestError as exc:
            raise self._error("get_device", exc) from exc
        if device is None:
            raise RuntimeError(f"NetBox device {device_id} was not found")
        return device.serialize()

    def list_devices(
        self, *, name: str | None = None, status: str | None = None, custom_fields: dict[str, Any] | None = None
    ) -> list[dict[str, Any]]:
        """List devices, optionally filtered by exact name, status, and custom fields.

        Custom-field filters are passed as ``cf_<name>`` query parameters,
        matching the NetBox free-device query convention, e.g.
        ``custom_fields={"osac_managed": True, "osac_instance_id__empty": True}``.
        pynetbox handles pagination.
        """
        filters: dict[str, Any] = {}
        if name is not None:
            filters["name"] = name
        if status is not None:
            filters["status"] = status
        for key, value in (custom_fields or {}).items():
            filters[f"cf_{key}"] = value
        try:
            return [device.serialize() for device in self._api.dcim.devices.filter(**filters)]
        except pynetbox.RequestError as exc:
            raise self._error("list_devices", exc) from exc
