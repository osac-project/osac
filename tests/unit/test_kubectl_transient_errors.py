"""Selective retries must not mistake arbitrary kubectl errors for absence."""

from __future__ import annotations

import subprocess
from unittest.mock import Mock, call

import pytest

from tests.e2e.core import helpers, k8s_client, runner
from tests.e2e.core.k8s_client import K8sClient

_TRANSIENT_ERRORS = [
    "Error from server (Timeout): request timed out",
    "Error from server (ServerTimeout): retry the request",
    "Error from server (TooManyRequests): too many requests",
    "Error from server (ServiceUnavailable): the server is currently unable to handle the request",
    "Error from server (InternalError): an internal error occurred",
    "Unable to connect to the server: connection refused",
    "Unable to connect to the server: dial tcp 127.0.0.1:6443: connect: connection refused",
    "Unable to connect to the server: read tcp 127.0.0.1:123->127.0.0.1:6443: read: connection reset by peer",
    "Unable to connect to the server: dial tcp 127.0.0.1:6443: i/o timeout",
    "Unable to connect to the server: net/http: TLS handshake timeout",
    "Unable to connect to the server: EOF",
    "Unable to connect to the server: unexpected EOF",
    "Unable to connect to the server: context deadline exceeded",
    "The connection to the server 127.0.0.1:6443 was refused - did you specify the right host or port?",
    "E1023 discovery cache unavailable\nError from server (ServiceUnavailable): temporarily unavailable",
]
_PERMANENT_ERRORS = [
    "",
    "Error from server (Forbidden): connection refused",
    "Error from server (Unauthorized): retry the request",
    'Error from server (NotFound): namespaces "tenant-a" not found',
    'Error from server (NotFound): clusterorders.osac.openshift.io "other-cluster" not found',
    "Unable to connect to the server: dial tcp: lookup api.example.test: no such host",
    "Unable to connect to the server: x509: certificate signed by unknown authority",
    "error: You must be logged in to the server (Unauthorized)",
    'error: the server doesn\'t have a resource type "clusterorder"',
    "connection reset by peer",
    "some application returned EOF",
    "Unable to connect to the server: unexpected diagnostic",
    "Error from server (ServiceUnavailable): first error\nError from server (Forbidden): final error",
]


@pytest.mark.parametrize("output", _TRANSIENT_ERRORS)
@pytest.mark.parametrize("stream", ["output", "stderr"])
def test_recognizes_only_explicit_transient_kubectl_diagnostics(output: str, stream: str) -> None:
    error = subprocess.CalledProcessError(1, ["kubectl"], **{stream: output})

    assert k8s_client.is_transient_kubectl_error(error) is True


@pytest.mark.parametrize("output", _PERMANENT_ERRORS)
def test_unknown_and_permanent_errors_are_not_retryable(output: str) -> None:
    error = subprocess.CalledProcessError(1, ["kubectl"], stderr=output)

    assert k8s_client.is_transient_kubectl_error(error) is False


@pytest.mark.parametrize("returncode", [-9, 2, 126, 127])
def test_abnormal_command_failures_are_not_retried(returncode: int) -> None:
    error = subprocess.CalledProcessError(returncode, ["kubectl"], stderr=_TRANSIENT_ERRORS[0])

    assert k8s_client.is_transient_kubectl_error(error) is False


@pytest.mark.parametrize("output", _TRANSIENT_ERRORS)
def test_parent_retries_transient_reads_but_waits_for_exact_not_found(
    monkeypatch: pytest.MonkeyPatch, output: str
) -> None:
    get = Mock(
        side_effect=[
            (output, 1),
            ('{"metadata":{"name":"cluster-a"}}', 0),
            ('Error from server (NotFound): clusterorders.osac.openshift.io "cluster-a" not found', 1),
        ]
    )
    sleep, observe = Mock(), Mock()
    monkeypatch.setattr(k8s_client, "run_unchecked", get)
    monkeypatch.setattr(runner.time, "sleep", sleep)

    helpers.wait_for_cluster_deletion_without_cleanup(
        k8s=K8sClient(namespace="tenant-a", as_system_admin=False), name="cluster-a", on_poll=observe
    )

    assert get.call_args_list == [call("kubectl", "get", "clusterorder", "cluster-a", "-n", "tenant-a")] * 3
    assert sleep.call_args_list == [call(10), call(10)]
    assert observe.call_count == 3
