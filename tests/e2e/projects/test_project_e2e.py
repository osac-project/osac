from __future__ import annotations

import json
import queue
import ssl
import subprocess
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from http.client import HTTPException, HTTPResponse, HTTPSConnection
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode
from urllib.request import Request, urlopen
from uuid import uuid4

import pytest

from tests.e2e.core.grpc_client import PRIVATE_API, PUBLIC_API, GRPCClient
from tests.e2e.core.helpers import assert_grpc_rejected
from tests.e2e.core.keycloak_admin import wait_for_project_not_in_keycloak
from tests.e2e.core.metadata_inventory import validate_metadata_inventory
from tests.e2e.core.osac_cli import OsacCLI

RESERVED_DOMAIN = "admin.osac.openshift.io"
EVENT_WATCH_TIMEOUT_SECONDS = 30


def _is_reserved_key(key: str) -> bool:
    domain, separator, _ = key.partition("/")
    return bool(separator) and (domain == RESERVED_DOMAIN or domain.endswith(f".{RESERVED_DOMAIN}"))


def _visible_map(values: dict[str, str]) -> dict[str, str]:
    return {key: value for key, value in values.items() if not _is_reserved_key(key)}


def _assert_metadata_maps(
    metadata: dict[str, Any], *, labels: dict[str, str], annotations: dict[str, str], context: str
) -> None:
    actual_labels = metadata.get("labels") or {}
    actual_annotations = metadata.get("annotations") or {}
    if actual_labels != labels or actual_annotations != annotations:
        pytest.fail(f"{context} metadata maps differ from expected maps; values redacted", pytrace=False)


def _assert_public_metadata(
    resource: dict[str, Any],
    *,
    labels: dict[str, str],
    annotations: dict[str, str],
    protected_values: tuple[str, ...],
    context: str,
) -> None:
    metadata = resource.get("metadata") or {}
    _assert_metadata_maps(metadata, labels=labels, annotations=annotations, context=context)
    serialized = json.dumps(resource, sort_keys=True)
    if any(value in serialized for value in protected_values):
        pytest.fail(f"{context} exposed a protected metadata value", pytrace=False)


def _assert_grpc_status(exc: subprocess.CalledProcessError, *, code: str, context: str) -> None:
    output = (exc.stderr or "") + (exc.stdout or "")
    if f"Code: {code}" not in output:
        raise AssertionError(f"{context} did not return gRPC {code}; details redacted") from None


def _safe_grpc_call(client: GRPCClient, *, service: str, data: dict[str, Any], context: str) -> dict[str, Any]:
    try:
        return client.call(service=service, data=data)
    except subprocess.CalledProcessError:
        raise AssertionError(f"{context} failed; request and response details redacted") from None


def _event_from_watch_response(response: dict[str, Any]) -> dict[str, Any]:
    watch_response = response.get("result", response)
    return watch_response.get("event") or {}


def _rest_request(
    fulfillment_address: str,
    *,
    token: str,
    method: str,
    path: str,
    body: dict[str, Any] | None = None,
    timeout: float = EVENT_WATCH_TIMEOUT_SECONDS,
) -> tuple[int, dict[str, Any]]:
    host = fulfillment_address.rsplit(":", 1)[0]
    request_body = json.dumps(body).encode() if body is not None else None
    request = Request(
        f"https://{host}{path}",
        data=request_body,
        headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"},
        method=method,
    )
    try:
        with urlopen(request, context=ssl._create_unverified_context(), timeout=timeout) as response:
            payload = response.read()
            try:
                return response.status, json.loads(payload) if payload else {}
            except json.JSONDecodeError:
                raise AssertionError(f"REST {method} returned an invalid response; details redacted") from None
    except HTTPError as exc:
        return exc.code, {}
    except (OSError, URLError):
        pytest.fail(f"REST {method} request failed; details redacted", pytrace=False)


class _PublicProjectEventWatch:
    """Read one public Project event from the REST gateway's streaming endpoint."""

    def __init__(self, fulfillment_address: str, *, token: str, project_id: str) -> None:
        host = fulfillment_address.rsplit(":", 1)[0]
        filter_expr = f'event.project.id == "{project_id}"'
        query = urlencode({"filter": filter_expr})
        self._path = f"/api/events/v1/events/watch?{query}"
        self._token = token
        self._connection = HTTPSConnection(
            host, timeout=EVENT_WATCH_TIMEOUT_SECONDS, context=ssl._create_unverified_context()
        )
        self._closed = threading.Event()
        self._events: queue.Queue[dict[str, Any] | None] = queue.Queue()
        self._response: HTTPResponse | None = None
        self._reader = threading.Thread(target=self._open_and_read_events, daemon=True)
        self._reader.start()

    def _open_and_read_events(self) -> None:
        try:
            if self._closed.is_set():
                self._events.put(None)
                return
            self._connection.request("GET", self._path, headers={"Authorization": f"Bearer {self._token}"})
            if self._closed.is_set():
                self._connection.close()
                self._events.put(None)
                return
            self._response = self._connection.getresponse()
            if self._response.status != 200:
                self._events.put(None)
                return
            self._read_events()
        except (HTTPException, OSError, URLError):
            self._events.put(None)

    def _read_events(self) -> None:
        response = self._response
        if response is None:
            self._events.put(None)
            return
        decoder = json.JSONDecoder()
        buffer = ""
        try:
            for raw_line in response:
                buffer += raw_line.decode("utf-8")
                while buffer.strip():
                    content = buffer.lstrip()
                    try:
                        event, end = decoder.raw_decode(content)
                    except json.JSONDecodeError:
                        break
                    self._events.put(event)
                    buffer = content[end:]
        except Exception:
            self._events.put(None)
        else:
            self._events.put(None)

    def wait_for_event(self, timeout: float) -> dict[str, Any] | None:
        try:
            event = self._events.get(timeout=timeout)
        except queue.Empty:
            return None
        if event is None:
            pytest.fail("Public Project event stream closed before an event arrived", pytrace=False)
        return event

    def close(self) -> None:
        self._closed.set()
        if self._response is not None:
            self._response.close()
        self._connection.close()
        self._reader.join(timeout=5)

    def __enter__(self) -> _PublicProjectEventWatch:
        return self

    def __exit__(self, *_: object) -> None:
        self.close()


def test_project_full_lifecycle(
    jwt_grpc_tenant1_admin: GRPCClient,
    jwt_grpc_tenant1: GRPCClient,
    jwt_grpc_tenant2: GRPCClient,
    project_lifecycle_resources: dict[str, str | bool],
) -> None:
    """
    Full E2E test scenario for Projects:

    1. Verify parent project exists and is accessible
    2. Verify project exists in Keycloak
    3. Verify ProjectMembership grants access to tenant1_user
    4. Verify child project exists and parent-child relationship
    5. Verify tenant isolation (tenant2_user cannot access)
    6. Verify deletion constraints (parent with children)
    7. Delete child project, verify removal
    8. Delete parent project, verify removal

    Resources are created and cleaned up via project_lifecycle_resources fixture.
    """
    # Extract resource IDs from fixture
    parent_project_id = project_lifecycle_resources["parent_project_id"]
    parent_project_name = project_lifecycle_resources["parent_project_name"]
    child_project_id = project_lifecycle_resources["child_project_id"]
    child_project_name = project_lifecycle_resources["child_project_name"]
    membership_id = project_lifecycle_resources["membership_id"]
    keycloak_url = project_lifecycle_resources["keycloak_url"]
    admin_token = project_lifecycle_resources["admin_token"]
    org_id = project_lifecycle_resources["org_id"]
    skip_keycloak_sync_checks = project_lifecycle_resources["skip_keycloak_sync_checks"]

    # 1. Verify parent project exists
    assert parent_project_id != "", "Parent project ID should not be empty"
    assert parent_project_id in jwt_grpc_tenant1_admin.list_project_ids()

    # 2. Verify parent project exists in Keycloak (already checked in fixture, but verify again)
    # This verification is handled by the fixture's wait_for_project_in_keycloak call

    # 3. Get the project as tenant1_user (has access via membership)
    project_response = jwt_grpc_tenant1.get_project(project_id=parent_project_id)
    assert project_response["object"]["id"] == parent_project_id
    assert project_response["object"]["metadata"]["name"] == parent_project_name

    # 4. Verify child project and parent-child relationship
    assert child_project_id != "", "Child project ID should not be empty"
    assert child_project_id in jwt_grpc_tenant1_admin.list_project_ids()

    parent_check = jwt_grpc_tenant1.get_project(project_id=parent_project_id)
    assert parent_check["object"]["id"] == parent_project_id

    child_check = jwt_grpc_tenant1.get_project(project_id=child_project_id)
    assert child_check["object"]["id"] == child_project_id
    assert child_check["object"]["spec"]["parent_project"]["id"] == parent_project_id

    # Verify child project exists in Keycloak (already checked in fixture)

    # 5. Attempt to get projects as tenant2_user (different tenant, should fail)
    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        jwt_grpc_tenant2.get_project(project_id=parent_project_id)
    assert_grpc_rejected(exc_info, "NotFound")

    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        jwt_grpc_tenant2.get_project(project_id=child_project_id)
    assert_grpc_rejected(exc_info, "NotFound")

    # 6. Attempt to delete parent project (should fail due to child project)
    with pytest.raises(subprocess.CalledProcessError) as exc_info:
        jwt_grpc_tenant1_admin.delete_project(project_id=parent_project_id)
    assert_grpc_rejected(exc_info, "FailedPrecondition")
    # Verify error message mentions child projects
    error_output = (exc_info.value.stderr or "") + (exc_info.value.stdout or "")
    assert "child" in error_output.lower(), "Error message should mention child projects"

    # Verify parent still exists
    assert parent_project_id in jwt_grpc_tenant1_admin.list_project_ids()

    # 7. Delete the child project, verify removal from gRPC and Keycloak
    jwt_grpc_tenant1_admin.delete_project(project_id=child_project_id)

    # Verify child removed from gRPC
    assert child_project_id not in jwt_grpc_tenant1_admin.list_project_ids()

    # Verify child removed from Keycloak with polling
    if not skip_keycloak_sync_checks:
        wait_for_project_not_in_keycloak(
            keycloak_url=keycloak_url, admin_token=admin_token, org_id=org_id, project_name=child_project_name
        )

    # 8. Delete the parent project, verify removal from gRPC and Keycloak
    jwt_grpc_tenant1_admin.delete_project(project_id=parent_project_id)

    # Verify parent removed from gRPC
    assert parent_project_id not in jwt_grpc_tenant1_admin.list_project_ids()

    # Verify parent removed from Keycloak with polling
    if not skip_keycloak_sync_checks:
        wait_for_project_not_in_keycloak(
            keycloak_url=keycloak_url, admin_token=admin_token, org_id=org_id, project_name=parent_project_name
        )

    # Delete the ProjectMembership
    jwt_grpc_tenant1_admin.delete_project_membership(membership_id=membership_id)

    # Note: Cleanup of all resources happens automatically in the fixture's finally block


def test_project_metadata_api_privacy_and_filtering(
    jwt_grpc_tenant1_admin: GRPCClient,
    jwt_grpc_tenant1: GRPCClient,
    private_grpc: GRPCClient,
    fulfillment_address: str,
    project_lifecycle_resources: dict[str, str | bool],
) -> None:
    """Exercise private and tenant API metadata paths on one real Project."""
    validate_metadata_inventory()

    project_id = str(project_lifecycle_resources["parent_project_id"])
    tag = uuid4().hex[:8]
    reserved_label_key = f"admin.osac.openshift.io/osac-6402-{tag}"
    reserved_label_value = f"private-label-{tag}"
    reserved_annotation_key = f"controller.admin.osac.openshift.io/osac-6402-{tag}"
    reserved_annotation_value = f"private-annotation-{tag}"
    ordinary_label_key = f"example.com/osac-6402-{tag}"
    ordinary_label_value = f"visible-label-{tag}"
    ordinary_annotation_key = f"example.com/osac-6402-note-{tag}"
    ordinary_annotation_value = f"visible-annotation-{tag}"
    locked_label_key = f"example.com/osac-6402-locked-{tag}"
    concurrent_label_key = f"example.com/osac-6402-concurrent-{tag}"
    concurrent_annotation_key = f"example.com/osac-6402-concurrent-note-{tag}"

    before = private_grpc.call(service=f"{PRIVATE_API}.Projects/Get", data={"id": project_id})["object"]
    before_metadata = before.get("metadata") or {}
    expected_labels = dict(before_metadata.get("labels") or {})
    expected_annotations = dict(before_metadata.get("annotations") or {})
    expected_labels.update({reserved_label_key: reserved_label_value, ordinary_label_key: ordinary_label_value})
    expected_annotations.update(
        {reserved_annotation_key: reserved_annotation_value, ordinary_annotation_key: ordinary_annotation_value}
    )

    # A private replacement uses the existing whole-map API and leaves lock optional.
    _safe_grpc_call(
        private_grpc,
        service=f"{PRIVATE_API}.Projects/Update",
        data={
            "object": {"id": project_id, "metadata": {"labels": expected_labels, "annotations": expected_annotations}},
            "updateMask": {"paths": ["metadata.labels", "metadata.annotations"]},
        },
        context="Private metadata seed",
    )

    # Repeat a private map edit with the existing optimistic-lock option.
    seeded = private_grpc.call(service=f"{PRIVATE_API}.Projects/Get", data={"id": project_id})["object"]
    seeded_metadata = seeded.get("metadata") or {}
    version = int(seeded_metadata.get("version", 0))
    expected_labels[locked_label_key] = f"locked-label-{tag}"
    _safe_grpc_call(
        private_grpc,
        service=f"{PRIVATE_API}.Projects/Update",
        data={
            "object": {"id": project_id, "metadata": {"version": version, "labels": expected_labels}},
            "updateMask": {"paths": ["metadata.labels"]},
            "lock": True,
        },
        context="Locked private metadata update",
    )

    # Two simultaneous unlocked private edits must retain both map replacements.
    expected_labels[concurrent_label_key] = f"concurrent-label-{tag}"
    expected_annotations[concurrent_annotation_key] = f"concurrent-annotation-{tag}"
    label_update = {
        "object": {"id": project_id, "metadata": {"labels": expected_labels}},
        "updateMask": {"paths": ["metadata.labels"]},
        "lock": False,
    }
    annotation_update = {
        "object": {"id": project_id, "metadata": {"annotations": expected_annotations}},
        "updateMask": {"paths": ["metadata.annotations"]},
    }
    try:
        with ThreadPoolExecutor(max_workers=2) as executor:
            futures = [
                executor.submit(private_grpc.call, service=f"{PRIVATE_API}.Projects/Update", data=label_update),
                executor.submit(private_grpc.call, service=f"{PRIVATE_API}.Projects/Update", data=annotation_update),
            ]
            for future in futures:
                future.result()
    except Exception:
        pytest.fail("Concurrent private metadata edits did not both succeed; details redacted", pytrace=False)

    protected_values = (reserved_label_value, reserved_annotation_value)
    current = private_grpc.call(service=f"{PRIVATE_API}.Projects/Get", data={"id": project_id})["object"]
    _assert_metadata_maps(
        current.get("metadata") or {},
        labels=expected_labels,
        annotations=expected_annotations,
        context="Private Project read after concurrent edits",
    )

    expected_public_labels = _visible_map(expected_labels)
    expected_public_annotations = _visible_map(expected_annotations)
    for client_name, client in (("Tenant Admin", jwt_grpc_tenant1_admin), ("Tenant User", jwt_grpc_tenant1)):
        project = client.get_project(project_id=project_id)["object"]
        _assert_public_metadata(
            project,
            labels=expected_public_labels,
            annotations=expected_public_annotations,
            protected_values=protected_values,
            context=f"{client_name} gRPC Get",
        )

    # The existing public REST resource endpoint returns the same tenant view.
    rest_path = f"/api/fulfillment/v1/projects/{quote(project_id, safe='')}"
    status, rest_project = _rest_request(
        fulfillment_address, token=jwt_grpc_tenant1_admin.token, method="GET", path=rest_path
    )
    if status != 200:
        pytest.fail("Tenant Admin REST Get did not succeed", pytrace=False)
    _assert_public_metadata(
        rest_project.get("object", rest_project),
        labels=expected_public_labels,
        annotations=expected_public_annotations,
        protected_values=protected_values,
        context="Tenant Admin REST Get",
    )

    # Explicit public filters cannot probe either protected map; ordinary filters still work.
    reserved_filter = (
        f"this.metadata.labels[{json.dumps(reserved_label_key)}] == {json.dumps(reserved_label_value)} || "
        f"this.metadata.annotations[{json.dumps(reserved_annotation_key)}] == "
        f"{json.dumps(reserved_annotation_value)}"
    )
    try:
        jwt_grpc_tenant1_admin.call(service=f"{PUBLIC_API}.Projects/List", data={"filter": reserved_filter})
    except subprocess.CalledProcessError as exc:
        _assert_grpc_status(exc, code="InvalidArgument", context="Reserved metadata List filter")
    else:
        pytest.fail("Public List accepted an explicit reserved metadata filter", pytrace=False)

    ordinary_filter = f"this.metadata.labels[{json.dumps(ordinary_label_key)}] == {json.dumps(ordinary_label_value)}"
    ordinary_results = jwt_grpc_tenant1_admin.call(
        service=f"{PUBLIC_API}.Projects/List", data={"filter": ordinary_filter}
    )
    ordinary_ids = [item.get("id") for item in ordinary_results.get("items", [])]
    if ordinary_ids != [project_id] or ordinary_results.get("total") != 1:
        pytest.fail("Ordinary public metadata filtering or total count changed", pytrace=False)

    # A mixed public gRPC replacement must fail as a unit for a tenant administrator.
    mixed_labels = dict(expected_public_labels)
    mixed_annotations = dict(expected_public_annotations)
    mixed_labels[f"example.com/osac-6402-rejected-{tag}"] = f"must-not-persist-{tag}"
    mixed_annotations[f"example.com/osac-6402-rejected-note-{tag}"] = f"must-not-persist-{tag}"
    mixed_labels[reserved_label_key] = reserved_label_value
    mixed_annotations[reserved_annotation_key] = reserved_annotation_value
    try:
        jwt_grpc_tenant1_admin.call(
            service=f"{PUBLIC_API}.Projects/Update",
            data={
                "object": {"id": project_id, "metadata": {"labels": mixed_labels, "annotations": mixed_annotations}},
                "updateMask": {"paths": ["metadata.labels", "metadata.annotations"]},
            },
        )
    except subprocess.CalledProcessError as exc:
        _assert_grpc_status(exc, code="InvalidArgument", context="Mixed public metadata update")
    else:
        pytest.fail("Public Update accepted mixed reserved metadata", pytrace=False)

    after_rejection = private_grpc.call(service=f"{PRIVATE_API}.Projects/Get", data={"id": project_id})["object"]
    _assert_metadata_maps(
        after_rejection.get("metadata") or {},
        labels=expected_labels,
        annotations=expected_annotations,
        context="Private Project read after rejected public update",
    )

    # The existing tenant user retains access after the rejected public update.
    final_public = jwt_grpc_tenant1.get_project(project_id=project_id)["object"]
    _assert_public_metadata(
        final_public,
        labels=expected_public_labels,
        annotations=expected_public_annotations,
        protected_values=protected_values,
        context="Final Tenant User gRPC Get",
    )


def test_project_metadata_rest_event_and_cli_compatibility(
    jwt_grpc_tenant1_admin: GRPCClient,
    jwt_cli_admin: OsacCLI,
    private_grpc: GRPCClient,
    fulfillment_address: str,
    project_lifecycle_resources: dict[str, str | bool],
) -> None:
    """Exercise REST, Events, and CLI metadata compatibility on one real Project."""
    project_id = str(project_lifecycle_resources["parent_project_id"])
    rest_path = f"/api/fulfillment/v1/projects/{quote(project_id, safe='')}"
    tag = uuid4().hex[:8]
    reserved_label_key = f"admin.osac.openshift.io/osac-6402-{tag}"
    reserved_label_value = f"private-label-{tag}"
    reserved_annotation_key = f"controller.admin.osac.openshift.io/osac-6402-{tag}"
    reserved_annotation_value = f"private-annotation-{tag}"
    ordinary_label_key = f"example.com/osac-6402-{tag}"
    ordinary_label_value = f"visible-label-{tag}"
    ordinary_annotation_key = f"example.com/osac-6402-note-{tag}"
    ordinary_annotation_value = f"visible-annotation-{tag}"
    rest_label_key = f"example.com/osac-6402-rest-{tag}"
    rest_label_value = f"rest-label-{tag}"

    before = private_grpc.call(service=f"{PRIVATE_API}.Projects/Get", data={"id": project_id})["object"]
    before_metadata = before.get("metadata") or {}
    expected_labels = dict(before_metadata.get("labels") or {})
    expected_annotations = dict(before_metadata.get("annotations") or {})
    expected_labels.update({reserved_label_key: reserved_label_value, ordinary_label_key: ordinary_label_value})
    expected_annotations.update(
        {reserved_annotation_key: reserved_annotation_value, ordinary_annotation_key: ordinary_annotation_value}
    )
    _safe_grpc_call(
        private_grpc,
        service=f"{PRIVATE_API}.Projects/Update",
        data={
            "object": {"id": project_id, "metadata": {"labels": expected_labels, "annotations": expected_annotations}},
            "updateMask": {"paths": ["metadata.labels", "metadata.annotations"]},
        },
        context="Private metadata seed for REST and CLI checks",
    )

    protected_values = (reserved_label_value, reserved_annotation_value)
    expected_public_annotations = _visible_map(expected_annotations)

    # Public event filters reject reserved metadata paths before opening a stream.
    event_filter = (
        f"event.project.metadata.labels[{json.dumps(reserved_label_key)}] == {json.dumps(reserved_label_value)}"
    )
    event_filter_path = f"/api/events/v1/events/watch?{urlencode({'filter': event_filter})}"
    event_filter_status, _ = _rest_request(
        fulfillment_address, token=jwt_grpc_tenant1_admin.token, method="GET", path=event_filter_path
    )
    if event_filter_status != 400:
        pytest.fail("Public Events Watch accepted an explicit reserved metadata filter", pytrace=False)

    # Apply an ordinary REST edit while the public event watch attaches.
    expected_labels_after_rest = dict(expected_labels)
    rest_responses_by_value: dict[str, dict[str, Any]] = {}
    expected_public_labels_by_value: dict[str, dict[str, str]] = {}
    rest_update_query = urlencode({"updateMask": "metadata.labels"})
    rest_update_path = f"{rest_path}?{rest_update_query}"
    with _PublicProjectEventWatch(
        fulfillment_address, token=jwt_grpc_tenant1_admin.token, project_id=project_id
    ) as event_watch:
        event_response = None
        matched_event_value = None
        # The REST streaming gateway waits for the first event before it returns
        # response headers. Retry ordinary edits while the watcher attaches so
        # the test does not block opening the stream before it can publish.
        event_deadline = time.monotonic() + EVENT_WATCH_TIMEOUT_SECONDS
        retry_intervals = (1, 2, 4, 8, 15)
        for attempt, retry_interval in enumerate(retry_intervals):
            remaining = event_deadline - time.monotonic()
            if remaining <= 0:
                break
            attempt_value = f"{rest_label_value}-{attempt}"
            expected_labels_after_rest[rest_label_key] = attempt_value
            expected_public_labels = _visible_map(expected_labels_after_rest)
            status, rest_update_response = _rest_request(
                fulfillment_address,
                token=jwt_grpc_tenant1_admin.token,
                method="PATCH",
                path=rest_update_path,
                body={"id": project_id, "metadata": {"labels": expected_public_labels}},
                timeout=remaining,
            )
            if status != 200:
                pytest.fail("Ordinary REST metadata update did not succeed", pytrace=False)
            rest_responses_by_value[attempt_value] = rest_update_response.get("object", rest_update_response)
            expected_public_labels_by_value[attempt_value] = expected_public_labels

            remaining = event_deadline - time.monotonic()
            if remaining <= 0:
                break
            event_response = event_watch.wait_for_event(timeout=min(retry_interval, remaining))
            if event_response is None:
                continue
            event_project = _event_from_watch_response(event_response).get("project") or {}
            event_metadata = event_project.get("metadata") or {}
            matched_event_value = (event_metadata.get("labels") or {}).get(rest_label_key)
            if matched_event_value in rest_responses_by_value:
                break
            event_response = None

        if event_response is None or matched_event_value not in rest_responses_by_value:
            pytest.fail("No matching public Project event arrived before the bounded retries", pytrace=False)

    updated_project = rest_responses_by_value[matched_event_value]
    expected_public_labels_after_rest = expected_public_labels_by_value[matched_event_value]
    _assert_public_metadata(
        updated_project,
        labels=expected_public_labels_after_rest,
        annotations=expected_public_annotations,
        protected_values=protected_values,
        context="REST Update response",
    )
    project_event = _event_from_watch_response(event_response).get("project") or {}
    if _event_from_watch_response(event_response).get("type") != "EVENT_TYPE_OBJECT_UPDATED":
        pytest.fail("Public event was not an object update", pytrace=False)
    _assert_public_metadata(
        project_event,
        labels=expected_public_labels_after_rest,
        annotations=expected_public_annotations,
        protected_values=protected_values,
        context="Public Project event",
    )

    # Existing CLI key=value and key- operations remain compatible and preserve hidden entries.
    cli_label_key = f"example.com/osac-6402-cli-{tag}"
    cli_annotation_key = f"example.com/osac-6402-cli-note-{tag}"
    jwt_cli_admin._run("label", "project", project_id, f"{cli_label_key}=temporary")
    jwt_cli_admin._run("annotate", "project", project_id, f"{cli_annotation_key}=temporary")
    jwt_cli_admin._run("label", "project", project_id, f"{cli_label_key}-")
    jwt_cli_admin._run("annotate", "project", project_id, f"{cli_annotation_key}-")

    _, label_rc = jwt_cli_admin._run_unchecked(
        "label",
        "project",
        project_id,
        f"{cli_label_key}=must-not-persist",
        f"{reserved_label_key}={reserved_label_value}",
    )
    if label_rc == 0:
        pytest.fail("CLI accepted a reserved label in a mixed update", pytrace=False)
    _, annotation_rc = jwt_cli_admin._run_unchecked(
        "annotate",
        "project",
        project_id,
        f"{cli_annotation_key}=must-not-persist",
        f"{reserved_annotation_key}=must-not-persist",
    )
    if annotation_rc == 0:
        pytest.fail("CLI accepted a reserved annotation in a mixed update", pytrace=False)

    final_private = private_grpc.call(service=f"{PRIVATE_API}.Projects/Get", data={"id": project_id})["object"]
    _assert_metadata_maps(
        final_private.get("metadata") or {},
        labels=expected_labels_after_rest,
        annotations=expected_annotations,
        context="Final private Project read",
    )
