"""Self-contained VMS REST client.

Replaces the external vastpy dependency with an in-house implementation
using requests. Provides the same chaining API:

    client.api.views[42].get(name="foo")
    client.api.clusters.get()
    client.api.users[5].access_keys.post(key="value")

Also includes task-waiting logic (previously in waiter.py).
"""

import json
import mimetypes
import os
import time
import traceback
from contextlib import ExitStack
from dataclasses import dataclass
from functools import cached_property
from typing import Any, Dict, List, Optional, Set

# Ansible sanity tests import all module_utils in an isolated environment
# without third-party packages. Guard the import so the module can be loaded
# for introspection; the actual check happens in VastClient.__init__.
try:
    import requests
    import urllib3
except ImportError:
    HAS_REQUESTS = False
    REQUESTS_IMPORT_ERROR = traceback.format_exc()
else:
    HAS_REQUESTS = True
    REQUESTS_IMPORT_ERROR = None

from .errors import VastAPIError, VastNotFoundError, VastTransportError

# Content types for multipart uploads that Python's ``mimetypes`` does not know
# (e.g. PEM/DER certificates). VMS validates the per-part Content-Type, so a bare
# ``application/octet-stream`` is rejected for these.
_UPLOAD_CONTENT_TYPES: Dict[str, str] = {
    ".pem": "application/x-pem-file",
    ".crt": "application/x-x509-ca-cert",
    ".cert": "application/x-x509-ca-cert",
    ".cer": "application/pkix-cert",
    ".der": "application/pkix-cert",
}


def _guess_upload_content_type(file_path: str) -> str:
    """Best-effort Content-Type for a multipart file part."""
    ext = os.path.splitext(file_path)[1].lower()
    if ext in _UPLOAD_CONTENT_TYPES:
        return _UPLOAD_CONTENT_TYPES[ext]
    guessed = mimetypes.guess_type(file_path)[0]
    return guessed or "application/octet-stream"


# ---------------------------------------------------------------------------
# Build / Galaxy metadata (for User-Agent)
# ---------------------------------------------------------------------------
# _build_info.py is generated at build/test time by release_helpers.sh and contains GALAXY_VERSION and GIT_COMMIT.

_GALAXY_VERSION = "unknown"
_GIT_COMMIT = "dev"
try:
    from ._build_info import GALAXY_VERSION as _GALAXY_VERSION
    from ._build_info import GIT_COMMIT as _GIT_COMMIT
except ImportError:
    pass

# ---------------------------------------------------------------------------
# Data types
# ---------------------------------------------------------------------------


@dataclass
class VastConnection:
    """Connection parameters for VAST VMS."""

    host: str
    token: Optional[str] = None
    username: Optional[str] = None
    password: Optional[str] = None
    validate_certs: bool = True
    timeout: Optional[int] = None
    tenant: Optional[str] = None
    api_version: Optional[str] = None
    debug: bool = False
    client_cert: Optional[str] = None
    client_key: Optional[str] = None


class RESTFailure(VastAPIError):
    """HTTP request returned a non-success status code."""

    def __init__(self, method: str, url: str, status: int, body: str):
        self.method = method
        self.url = url
        self.status = status
        self.body = body
        super().__init__(f"{method} {url} -> {status}: {body}")


# ---------------------------------------------------------------------------
# API path builder (replaces vastpy chaining)
# ---------------------------------------------------------------------------

# HTTP verbs that send query-string parameters
_QUERY_VERBS = {"GET"}

# Gateway statuses returned by the mgmt VIP mid-failover (active VMS down,
# standby not yet serving). Treated as transport, not an API-level answer.
_GATEWAY_STATUSES = {502, 503, 504}


def select_file_params(file_params: Set[str], source: Dict[str, Any]) -> Dict[str, Any]:
    """Local file paths for the ``file_params`` actually supplied in ``source``.

    Shared by ``BaseResource`` (create payload) and ``SubEndpointResource``
    (action params) so the multipart file-collection logic lives in one place.
    """
    return {name: source[name] for name in file_params if source.get(name) is not None}


def unwrap_list_envelope(results: List[dict]) -> List[Dict[str, Any]]:
    """Flatten ``{count, next, previous, results}`` list envelopes into resource rows.

    ``_APIPath.get`` wraps a singleton envelope as ``[envelope]``; this helper
    concatenates each envelope's ``results`` list.
    """
    unwrapped: List[Dict[str, Any]] = []
    for envelope in results:
        if not isinstance(envelope, dict) or not isinstance(envelope.get("results"), list):
            raise VastAPIError(f"Expected a list envelope with a 'results' list, got: {envelope!r}")
        unwrapped.extend(envelope["results"])
    return unwrapped


class _APIPath:
    """Lazy URL-builder that accumulates path segments.

    Every attribute access or subscript appends a segment::

        client.api.views          -> segments = ("views",)
        client.api.views[42]      -> segments = ("views", "42")
        client.api.views[42].get  -> executes GET /api/<ver>/views/42/

    Terminal methods (`get`, `post`, `patch`, `put`, `delete`)
    execute the actual HTTP call.

    **GET always returns ``list[dict]``** -- if the server returns a single
    object the client wraps it in a list.  POST / PATCH / PUT / DELETE
    return a single ``dict`` (or ``None`` for empty bodies).
    """

    __slots__ = ("_client", "_segments")

    def __init__(self, client: "VastClient", segments: tuple = ()):
        object.__setattr__(self, "_client", client)
        object.__setattr__(self, "_segments", segments)

    def __repr__(self) -> str:
        return f"_APIPath({'/'.join(str(s) for s in self._segments)})"

    # -- path building -------------------------------------------------------

    def __getattr__(self, part: str) -> "_APIPath":
        if part.startswith("_"):
            raise AttributeError(part)
        return _APIPath(self._client, self._segments + (part,))

    def __getitem__(self, part) -> "_APIPath":
        return _APIPath(self._client, self._segments + (str(part),))

    # -- terminal HTTP methods -----------------------------------------------

    def get(self, **params) -> List[dict]:
        """GET -- always returns a list of dicts."""
        result = self._client._request("GET", self._segments, params=params)
        if result is None:
            return []
        if isinstance(result, list):
            return result
        return [result]

    def post(self, *, _query_params: Optional[dict] = None, **params) -> Optional[dict]:
        # Keep empty ``{}`` body (not None) so callers like trigger_action.post()
        # still send JSON; only _query_params is additive for SAML-style routes.
        return self._client._request(
            "POST",
            self._segments,
            data=params,
            params=_query_params,
        )

    def patch(self, *, _query_params: Optional[dict] = None, **params) -> Optional[dict]:
        return self._client._request(
            "PATCH",
            self._segments,
            data=params,
            params=_query_params,
        )

    def put(self, **params) -> Optional[dict]:
        return self._client._request("PUT", self._segments, data=params)

    def _upload(self, method: str, files: Dict[str, str], **fields) -> Optional[dict]:
        """Send ``files`` + ``fields`` as multipart/form-data via ``method``.

        ``files`` maps a form field name to a local file path; ``fields`` are the
        remaining (non-file) multipart form values.
        """
        return self._client._request(
            method,
            self._segments,
            data=fields or None,
            files=files,
        )

    def put_file(self, field_name: str, file_path: str, **fields) -> Optional[dict]:
        """PUT a single file as multipart/form-data (Swagger ``in: formData`` uploads)."""
        return self._upload("PUT", {field_name: file_path}, **fields)

    def post_file(self, files: Dict[str, str], **fields) -> Optional[dict]:
        """POST one or more files + form fields as multipart/form-data.

        Used for create operations whose Swagger body is ``in: formData`` with
        ``type: file`` params (e.g. ``tlscertificates``).
        """
        return self._upload("POST", files, **fields)

    def delete(self, *, _query_params: Optional[dict] = None, **params) -> Optional[dict]:
        return self._client._request(
            "DELETE",
            self._segments,
            params=_query_params or None,
            data=params or None,
        )

    def first(self, **params) -> Optional[dict]:
        """GET and return the first result, or None if empty."""
        results = self.get(**params)
        return results[0] if results else None


# ---------------------------------------------------------------------------
# Task-wait constants
# ---------------------------------------------------------------------------

_TASK_SUCCESS_STATES = {"COMPLETED", "SUCCESS"}
_TASK_TERMINAL_STATES = _TASK_SUCCESS_STATES | {"FAILED", "ERROR", "CANCELLED"}

# ---------------------------------------------------------------------------
# The client
# ---------------------------------------------------------------------------


class VastClient:
    """Self-contained VMS REST client.

    Provides:
    * Chaining REST API via ``.api``
    * Auth (token **or** username/password)
    * User-Agent tracking
    * Task-waiting helpers (folded from the former ``waiter.py``)
    """

    debug = False
    _debug_traces: list

    def __init__(self, connection: VastConnection) -> None:
        self._debug_traces = []
        if not HAS_REQUESTS:
            raise RuntimeError(
                "The 'requests' library is required for VastClient. "
                "Install it with: pip install requests\n" + (REQUESTS_IMPORT_ERROR or "")
            )

        self._connection = connection
        self._base_url = f"https://{connection.host}/api"
        self._version = connection.api_version if connection.api_version is not None else "latest"

        # --- requests.Session setup ----------------------------------------
        self._session = requests.Session()
        self._session.verify = connection.validate_certs
        if not connection.validate_certs:
            urllib3.disable_warnings(category=urllib3.exceptions.InsecureRequestWarning)

        self._session.headers["Accept"] = "application/json"
        self._session.headers["Content-Type"] = "application/json"
        self._session.headers["User-Agent"] = f"VastAnsible/{_GALAXY_VERSION}.{_GIT_COMMIT} {requests.utils.default_user_agent()}"

        if connection.token:
            self._session.headers["Authorization"] = f"Api-Token {connection.token}"
        else:
            self._session.auth = (connection.username, connection.password)

        if connection.tenant:
            self._session.headers["X-Tenant-Name"] = connection.tenant

        if connection.client_cert and connection.client_key:
            self._session.cert = (connection.client_cert, connection.client_key)

        self._timeout = connection.timeout

    # -- public API ----------------------------------------------------------

    @property
    def api(self) -> _APIPath:
        """Entry point for the chaining REST API."""
        return _APIPath(self)

    def pop_debug_traces(self) -> List[str]:
        """Return collected debug traces and clear the buffer."""
        traces = self._debug_traces
        self._debug_traces = []
        return traces

    # -- cluster resolution --------------------------------------------------

    def _resolve_cluster(self) -> Dict[str, Any]:
        return self.api.clusters.get()[0]

    @cached_property
    def cluster(self) -> Dict[str, Any]:
        """The cluster this client is connected to. Lazy, cached after first access."""
        return self._resolve_cluster()

    @cached_property
    def is_loopback(self) -> bool:
        """Whether the connected cluster is a loopback (single-node) setup."""
        return bool(self.cluster.get("loopback", False))

    # -- low-level request ---------------------------------------------------

    def _request(
        self,
        method: str,
        segments: tuple,
        *,
        params: Optional[dict] = None,
        data: Optional[dict] = None,
        files: Optional[Dict[str, str]] = None,
    ) -> Any:
        url_parts = [self._base_url, self._version] + [str(s) for s in segments]
        url = "/".join(url_parts) + "/"

        kwargs: Dict[str, Any] = {}
        if self._timeout is not None:
            kwargs["timeout"] = self._timeout

        def _expand_params(p: dict) -> list:
            # Expand list values into repeated keys (same as vastpy)
            expanded: list = []
            for k, v in p.items():
                if isinstance(v, list):
                    expanded.extend((k, i) for i in v)
                else:
                    expanded.append((k, v))
            return expanded

        with ExitStack() as stack:
            if method in _QUERY_VERBS:
                if params:
                    kwargs["params"] = _expand_params(params)
            else:
                if files is not None:
                    multipart: Dict[str, Any] = {}
                    for field_name, file_path in files.items():
                        handle = stack.enter_context(open(file_path, "rb"))
                        multipart[field_name] = (
                            os.path.basename(file_path),
                            handle,
                            _guess_upload_content_type(file_path),
                        )
                    kwargs["files"] = multipart
                    kwargs["headers"] = {"Content-Type": None}
                    if data:
                        # Expand list values into repeated (key, value) tuples so
                        # array form fields (e.g. ``protocols``) are sent as
                        # multiple parts rather than a stringified list.
                        kwargs["data"] = _expand_params(data)
                elif data is not None:
                    kwargs["data"] = json.dumps(data)

                if params:
                    kwargs["params"] = _expand_params(params)

            if self.debug:
                self._debug_traces.append(
                    f">>> {method} {url} params={params} data={data} " f"files={list(files) if files else None}"
                )

            try:
                resp = self._session.request(method, url, **kwargs)
            except requests.RequestException as e:
                # Connection refused/reset, read timeouts, DNS failures: the VMS
                # never answered. Typed as transport so pollers can wait out an
                # HA failover / service restart instead of failing fast.
                raise VastTransportError(f"{method} {url} failed: {e}") from e

        if self.debug:
            body_preview = (resp.text or "")[:2000]
            self._debug_traces.append(f"<<< {resp.status_code} ({len(resp.content or b'')}B) {body_preview}")

        try:
            resp.raise_for_status()
        except requests.HTTPError:
            # 404 gets a typed exception so callers can treat "absent" distinctly
            # from genuine API/transport failures (no message string-matching).
            if resp.status_code == 404:
                raise VastNotFoundError(f"{method} {url} -> 404: {resp.text}") from None
            if resp.status_code in _GATEWAY_STATUSES:
                # Transport, not an API answer (see _GATEWAY_STATUSES).
                raise VastTransportError(f"{method} {url} -> {resp.status_code}: {resp.text}") from None
            raise RESTFailure(method, url, resp.status_code, resp.text) from None

        if resp.content and "application/json" in resp.headers.get("Content-Type", ""):
            return resp.json()
        return None

    # -- task waiting (folded from waiter.py) --------------------------------

    # Bounded retries for API-level polling errors (task not found, 401, 4xx):
    # these are real answers from a live VMS and should fail fast. Transport
    # errors (see VastTransportError) are retried until the overall timeout.
    _MAX_POLL_RETRIES = 6

    def wait_for_task(
        self,
        task_id: int,
        timeout: int = 300,
        poll_interval: int = 5,
    ) -> Dict[str, Any]:
        """Poll a VMS async task until it reaches a terminal state.

        Transport failures (connection reset/refused, timeouts, VIP 502/503/504)
        are tolerated until the overall ``timeout`` because VMS can be
        unavailable for several minutes during a restart / HA failover. API-level
        errors (task not found, 401, other 4xx) fail fast after
        ``_MAX_POLL_RETRIES`` consecutive occurrences.

        Returns the final task dict on success.
        Raises VastAPIError on failure or timeout.
        """
        start = time.time()
        last_state = None
        last_error = None
        api_errors = 0

        while True:
            if time.time() - start >= timeout:
                details = f"Last state: {last_state}"
                if last_error:
                    details += f". Last polling error: {last_error}"
                raise VastAPIError(f"Task {task_id} timed out after {timeout}s. {details}")

            try:
                task = self._get_task(task_id)
                last_error = None
                api_errors = 0
            except VastTransportError as error:
                # VMS is down/failing over -- keep waiting until timeout. Reset the
                # API-error budget so it counts only *consecutive* API-level errors.
                last_error = error
                api_errors = 0
                time.sleep(poll_interval)
                continue
            except VastAPIError as error:
                # Real answer from a live VMS (task not found, auth, 4xx): fail fast.
                last_error = error
                api_errors += 1
                if api_errors >= self._MAX_POLL_RETRIES:
                    raise
                time.sleep(poll_interval)
                continue

            state = task.get("state", task.get("status", "UNKNOWN"))
            last_state = state

            if state in _TASK_SUCCESS_STATES:
                return task

            if state in _TASK_TERMINAL_STATES:
                error = task.get("failure_reason") or task.get("error") or task.get("message")
                if not error:
                    messages = task.get("messages") or []
                    error = messages[-1] if messages else "Unknown error"
                raise VastAPIError(f"Task {task_id} failed ({state}): {error}")

            time.sleep(poll_interval)

    @staticmethod
    def extract_task_id(response: Dict[str, Any]) -> Optional[int]:
        """Extract task ID from an async API response.

        Checks several common response shapes used by VMS.
        Returns None if no task ID is found.
        """
        if not response:
            return None

        if "task_id" in response:
            return response["task_id"]

        async_task = response.get("async_task") or {}
        task_id = async_task.get("id") or async_task.get("task_id")
        if task_id:
            return task_id

        if "id" in response and response.get("type") == "async_task":
            return response["id"]

        return None

    # -- internal helpers ----------------------------------------------------

    def _get_task(self, task_id: int) -> Dict[str, Any]:
        """Fetch task status from the vtasks endpoint."""
        try:
            task = self.api.vtasks.first(id=task_id)
            if task:
                return task

            task = self.api.async_tasks.first(id=task_id)
            if task:
                return task

            raise VastAPIError(f"Task {task_id} not found")
        except VastAPIError:
            raise
        except Exception as e:
            raise VastAPIError(f"Failed to get task {task_id}: {e}") from e
