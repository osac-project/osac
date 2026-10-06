"""Read-only release gate for the overlapping-root rotation sequence.

Run with OSAC_TRUST_ROTATION_PHASE=overlap before switching the leaf,
OSAC_TRUST_ROTATION_PHASE=post-switch before removing the old root, and
OSAC_TRUST_ROTATION_PHASE=final after removing the retired root source.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import socket
import ssl
import subprocess
import time
import urllib.error
import urllib.request
from collections.abc import Callable, Iterator
from contextlib import contextmanager
from urllib.parse import urlsplit

import pytest

from tests.e2e.core.fulfillment_trust import assert_cluster_trust, assert_management_tls
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import env, poll_until, run

pytestmark = [
    pytest.mark.regression,
    pytest.mark.skipif(
        not os.environ.get("OSAC_TRUST_ROTATION_PHASE"),
        reason="Run the rotation release gate with OSAC_TRUST_ROTATION_PHASE and OSAC_TRUST_ROTATION_EXPECTED_HASH",
    ),
]


@contextmanager
def _metrics(k8s: K8sClient, deployment: str) -> Iterator[Callable[[], str]]:
    secure = deployment == "osac-operator"
    remote_port = 8443 if secure else 8080
    token = ""
    if secure:
        deployment_object = k8s.get_json(resource="deployment", name=deployment)
        service_account = deployment_object["spec"]["template"]["spec"]["serviceAccountName"]
        token = run(*k8s._base(), "-n", k8s.namespace, "create", "token", service_account)

    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    process = subprocess.Popen(
        [*k8s._base(), "-n", k8s.namespace, "port-forward", f"deployment/{deployment}", f"{port}:{remote_port}"],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
    )
    try:

        def read_metrics() -> str:
            if process.poll() is not None:
                raise AssertionError(f"Port-forward for {deployment} metrics exited")
            try:
                url = f"{'https' if secure else 'http'}://127.0.0.1:{port}/metrics"
                request = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"} if token else {})
                context = ssl._create_unverified_context() if secure else None
                with urllib.request.urlopen(request, context=context, timeout=1) as response:
                    return response.read().decode()
            except (OSError, urllib.error.URLError):
                return ""

        for _ in range(30):
            if read_metrics():
                break
            else:
                time.sleep(0.5)
        else:
            raise AssertionError(f"Timed out waiting for {deployment} metrics")
        yield read_metrics
    finally:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()


def test_all_clients_converged_before_rotation_advance(k8s_hub_client: K8sClient) -> None:
    phase = env("OSAC_TRUST_ROTATION_PHASE")
    assert phase in {"overlap", "post-switch", "final"}
    expected_hash = env("OSAC_TRUST_ROTATION_EXPECTED_HASH")
    assert re.fullmatch(r"[a-f0-9]{64}", expected_hash)

    bundle = poll_until(
        fn=lambda: k8s_hub_client.get_json(resource="configmap", name="ca-bundle")["data"]["bundle.pem"],
        until=lambda value: hashlib.sha256(value.encode()).hexdigest() == expected_hash,
        retries=60,
        delay=5,
        description=f"management CA bundle hash {expected_hash}",
    )
    certificates = re.findall(r"-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----", bundle, re.DOTALL)
    roots = {hashlib.sha256(ssl.PEM_cert_to_DER_cert(cert)).hexdigest() for cert in certificates}
    old_root = env("OSAC_TRUST_OLD_ROOT_PEM_PATH")
    new_root = env("OSAC_TRUST_NEW_ROOT_PEM_PATH")

    def root_fingerprint(path: str) -> str:
        with open(path, encoding="utf-8") as source:
            return hashlib.sha256(ssl.PEM_cert_to_DER_cert(source.read())).hexdigest()

    old_fingerprint = root_fingerprint(old_root)
    new_fingerprint = root_fingerprint(new_root)
    assert old_fingerprint != new_fingerprint
    assert new_fingerprint in roots
    if phase in {"overlap", "post-switch"}:
        assert old_fingerprint in roots
    else:
        assert old_fingerprint not in roots, "Retired root is still present after final rotation"

    assert_management_tls(k8s_hub_client, require_metering=True)
    orders = json.loads(
        run(*k8s_hub_client._base(), "-n", k8s_hub_client.namespace, "get", "clusterorders", "-o", "json")
    )["items"]
    selected = [
        order
        for order in orders
        if order["metadata"].get("annotations", {}).get("osac.openshift.io/tenant")
    ]
    assert selected, "No tenant-scoped ClusterOrders were found"
    for order in selected:
        assert (
            assert_cluster_trust(
                k8s_hub_client, order["metadata"]["name"], require_csi=True, expected_bundle_hash=expected_hash
            )
            == expected_hash
        )

    for deployment in ("osac-operator", "osac-metering"):
        pattern = re.compile(
            rf'^osac_fulfillment_client_bundle_observed\{{sha256="{expected_hash}"\}} 1(?:\.0)?$', re.MULTILINE
        )
        with _metrics(k8s_hub_client, deployment) as read_metrics:
            poll_until(
                fn=read_metrics,
                until=lambda metrics, pattern=pattern: bool(pattern.search(metrics)),
                retries=60,
                delay=5,
                description=f"{deployment} verifying bundle hash {expected_hash}",
            )

    endpoint = urlsplit("https://" + env("OSAC_TRUST_LEAF_ENDPOINT"))
    assert endpoint.hostname and endpoint.port

    def verify_leaf(ca_file: str) -> None:
        context = ssl.create_default_context(cafile=ca_file)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        with (
            socket.create_connection((endpoint.hostname, endpoint.port), timeout=10) as connection,
            context.wrap_socket(connection, server_hostname=endpoint.hostname),
        ):
            pass

    verify_leaf(old_root if phase == "overlap" else new_root)
    with pytest.raises(ssl.SSLCertVerificationError):
        verify_leaf(new_root if phase == "overlap" else old_root)
