"""Self-contained VMS REST client.

Replaces the external vastpy dependency with an in-house implementation
using requests. Provides the same chaining API:

    client.api.views[42].get(name="foo")
    client.api.clusters.get()
    client.api.users[5].access_keys.post(key="value")

Also includes task-waiting logic (previously in waiter.py).
"""

import json
import time
import traceback
from dataclasses import dataclass
from functools import cached_property
from typing import Any, Dict, List, Optional

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

from .errors import VastAPIError

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

    def post(self, **params) -> Optional[dict]:
        return self._client._request("POST", self._segments, data=params)

    def patch(self, **params) -> Optional[dict]:
        return self._client._request("PATCH", self._segments, data=params)

    def put(self, **params) -> Optional[dict]:
        return self._client._request("PUT", self._segments, data=params)

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

        if method in _QUERY_VERBS:
            if params:
                kwargs["params"] = _expand_params(params)
        else:
            if data is not None:
                kwargs["data"] = json.dumps(data)

            if params:
                kwargs["params"] = _expand_params(params)

        if self.debug:
            self._debug_traces.append(f">>> {method} {url} params={params} data={data}")

        resp = self._session.request(method, url, **kwargs)

        if self.debug:
            body_preview = (resp.text or "")[:2000]
            self._debug_traces.append(f"<<< {resp.status_code} ({len(resp.content or b'')}B) {body_preview}")

        try:
            resp.raise_for_status()
        except requests.HTTPError:
            raise RESTFailure(method, url, resp.status_code, resp.text) from None

        if resp.content and "application/json" in resp.headers.get("Content-Type", ""):
            return resp.json()
        return None

    # -- task waiting (folded from waiter.py) --------------------------------

    _MAX_POLL_RETRIES = 6

    def wait_for_task(
        self,
        task_id: int,
        timeout: int = 300,
        poll_interval: int = 5,
    ) -> Dict[str, Any]:
        """Poll a VMS async task until it reaches a terminal state.

        Tolerates transient connection errors (e.g. ConnectionResetError)
        that occur when VMS restarts services during cnode enable/disable.

        Returns the final task dict on success.
        Raises VastAPIError on failure or timeout.
        """
        start = time.time()
        last_state = None
        consecutive_errors = 0

        while True:
            if time.time() - start >= timeout:
                raise VastAPIError(f"Task {task_id} timed out after {timeout}s. Last state: {last_state}")

            try:
                task = self._get_task(task_id)
                consecutive_errors = 0
            except VastAPIError:
                consecutive_errors += 1
                if consecutive_errors >= self._MAX_POLL_RETRIES:
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
