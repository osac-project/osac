"""Exercise provider CSI operator installation and no-op behavior through Stage 2."""

from __future__ import annotations

import base64
import json
import logging
import os
import re
import select
import subprocess
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Iterator
from contextlib import contextmanager
from typing import Any

import pytest

from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.runner import env, managed_process, poll_until, run_unchecked

pytestmark = [pytest.mark.regression, pytest.mark.requires_vmaas]

_DRIVER_INSTALL_TAG = "provider_csi_driver_install"
_CONTROLLER_API_OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))
_LOGGER = logging.getLogger(__name__)


class _RetryableControllerError(RuntimeError):
    """A transient Controller API failure that can be retried while polling a job."""


_OPERATORS: tuple[dict[str, Any], ...] = (
    {
        "package": "trident-operator",
        "subscription": "trident-operator",
        "namespace": "trident-operator",
        "drivers": ("csi.trident.netapp.io",),
    },
    {
        "package": "vast-csi-operator",
        "subscription": "vast-csi-operator",
        "namespace": "vast-csi-system",
        "drivers": ("csi.vastdata.com", "block.csi.vastdata.com"),
    },
)


def test_stage2_installs_missing_native_csi_operators_and_skips_existing(
    k8s_hub_client: K8sClient, test_run_id: str
) -> None:
    """Install absent native drivers through OLM, then leave installed drivers unchanged.

    The full-install VMaaS runner owns a disposable cluster. The cluster-scoped
    Operators installed here are left for that runner's cluster teardown. The
    test creates a disposable copy of the Stage 2 template with launch-time tag
    selection enabled, leaving the production template unchanged. The first
    launch starts from a clean provider state and lets OLM select the current
    stable-channel bundle. The second launch verifies registered CSI drivers
    and their Subscriptions are not modified.
    """
    for operator in _OPERATORS:
        assert not _resource_exists(
            k8s=k8s_hub_client,
            resource="subscriptions.operators.coreos.com",
            name=operator["subscription"],
            namespace=operator["namespace"],
        ), f"Expected a clean CI cluster; Subscription {operator['namespace']}/{operator['subscription']} exists"
        for driver in operator["drivers"]:
            assert not _resource_exists(k8s=k8s_hub_client, resource="csidriver", name=driver), (
                f"Expected a clean CI cluster; CSIDriver {driver} already exists"
            )

    secret = k8s_hub_client.get_json(resource="secret", name="osac-aap-api-token")
    encoded_token = secret.get("data", {}).get("token", "")
    assert encoded_token, "AAP API token Secret has no token key"
    token = base64.b64decode(encoded_token).decode("utf-8")

    prefix = env("OSAC_AAP_TEMPLATE_PREFIX", "osac")
    template_name = f"{prefix}-create-tenant-cluster-storage"
    tenant_name = f"e2e-native-csi-{test_run_id}"
    extra_vars = {
        "osac_job_vars": {
            "resource": {
                "apiVersion": "osac.openshift.io/v1alpha1",
                "kind": "Tenant",
                "metadata": {
                    "name": tenant_name,
                    "namespace": "osac-tenants",
                    "uid": f"00000000-0000-4000-8000-{test_run_id:0>12}",
                },
                "spec": {},
            },
            "storage_tier_definitions": [
                {"name": f"netapp-block-{test_run_id}", "provider": "netapp", "protocol": "block"},
                {"name": f"vast-nfs-{test_run_id}", "provider": "vast", "protocol": "nfs"},
                {"name": f"vast-block-{test_run_id}", "provider": "vast", "protocol": "block"},
            ],
        }
    }
    with _controller_api_proxy(k8s_hub_client) as controller_url:
        templates = _controller_request(
            controller_url=controller_url,
            token=token,
            path=f"job_templates/?{urllib.parse.urlencode({'name': template_name})}",
        ).get("results", [])
        matching_templates = [item for item in templates if item.get("name") == template_name]
        assert len(matching_templates) == 1, f"Expected exactly one AAP template named {template_name}"

        template_id = matching_templates[0]["id"]
        assert matching_templates[0].get("ask_tags_on_launch") is False, (
            "The production Stage 2 template should not expose launch-time tag selection"
        )
        test_template_id: int | None = None
        try:
            test_template = _controller_request(
                controller_url=controller_url,
                token=token,
                path=f"job_templates/{template_id}/copy/",
                payload={"name": f"{template_name}-e2e-{test_run_id}"},
            )
            test_template_id = test_template.get("id")
            assert isinstance(test_template_id, int), "AAP did not return the copied test template ID"
            updated_test_template = _controller_request(
                controller_url=controller_url,
                token=token,
                path=f"job_templates/{test_template_id}/",
                payload={"ask_tags_on_launch": True},
                method="PATCH",
            )
            assert updated_test_template.get("ask_tags_on_launch") is True, (
                "The disposable Stage 2 template copy must allow launch-time tag selection"
            )
            initial_job_id, initial_job = _launch_stage2_job(
                controller_url=controller_url,
                token=token,
                template_id=test_template_id,
                extra_vars=extra_vars,
                description="Stage 2 install of absent native CSI operators",
            )
            assert initial_job.get("status") == "successful", f"AAP install job {initial_job_id} failed"

            before_noop: dict[str, dict[str, Any]] = {}
            installed_csvs: dict[str, dict[str, Any]] = {}
            for operator in _OPERATORS:
                subscription = poll_until(
                    fn=lambda operator=operator: k8s_hub_client.get_json(
                        resource="subscriptions.operators.coreos.com",
                        name=operator["subscription"],
                        namespace=operator["namespace"],
                    ),
                    until=lambda item, operator=operator: (
                        item.get("spec", {}).get("source") == "certified-operators"
                        and str(item.get("status", {}).get("installedCSV") or "").startswith(f"{operator['package']}.")
                        and not any(
                            condition.get("type") == "ResolutionFailed" and condition.get("status") == "True"
                            for condition in item.get("status", {}).get("conditions", [])
                        )
                    ),
                    retries=60,
                    delay=5,
                    description=f"OLM installs {operator['package']} from the stable channel",
                )
                spec = subscription.get("spec", {})
                installed_csv = subscription.get("status", {}).get("installedCSV", "")
                assert spec.get("channel") == "stable"
                assert spec.get("installPlanApproval") == "Manual"
                assert spec.get("name") == operator["package"]
                assert spec.get("source") == "certified-operators"
                assert spec.get("sourceNamespace") == "openshift-marketplace"
                assert "startingCSV" not in spec, "OLM must select the current stable-channel bundle"
                assert installed_csv.startswith(f"{operator['package']}.")

                csv = poll_until(
                    fn=lambda operator=operator, installed_csv=installed_csv: k8s_hub_client.get_json(
                        resource="clusterserviceversion", name=installed_csv, namespace=operator["namespace"]
                    ),
                    until=lambda item: item.get("status", {}).get("phase") == "Succeeded",
                    retries=60,
                    delay=5,
                    description=f"Installed {operator['package']} CSV succeeds",
                )
                version = csv.get("spec", {}).get("version", "")
                assert version, f"Installed CSV {installed_csv} has no version"
                installed_csvs[operator["package"]] = csv

                install_plans = k8s_hub_client.list_json(resource="installplans", namespace=operator["namespace"]).get(
                    "items", []
                )
                package_plans = [
                    plan
                    for plan in install_plans
                    if any(
                        csv_name.startswith(f"{operator['package']}.")
                        for csv_name in plan.get("spec", {}).get("clusterServiceVersionNames", [])
                    )
                ]
                initial_plans = [
                    plan
                    for plan in package_plans
                    if installed_csv in plan.get("spec", {}).get("clusterServiceVersionNames", [])
                ]
                assert any(plan.get("spec", {}).get("approved") is True for plan in initial_plans), (
                    f"No approved initial InstallPlan contains installed CSV {installed_csv}"
                )
                assert not any(
                    installed_csv not in plan.get("spec", {}).get("clusterServiceVersionNames", [])
                    and plan.get("spec", {}).get("approved") is True
                    for plan in package_plans
                ), f"Manual approval must leave later {operator['package']} plans unapproved"

                for driver in operator["drivers"]:
                    assert _resource_exists(k8s=k8s_hub_client, resource="csidriver", name=driver), (
                        f"AAP job succeeded but CSIDriver {driver} was not registered"
                    )
                before_noop[operator["subscription"]] = {"spec": spec, "installedCSV": installed_csv}

            initial_stdout = _controller_request(
                controller_url=controller_url, token=token, path=f"jobs/{initial_job_id}/stdout/?format=json"
            ).get("content", "")
            assert isinstance(initial_stdout, str) and initial_stdout, (
                f"AAP install job {initial_job_id} returned no JSON stdout content"
            )
            for operator in _OPERATORS:
                installed_csv = before_noop[operator["subscription"]]["installedCSV"]
                installed_version = installed_csvs[operator["package"]]["spec"]["version"]
                assert installed_csv in initial_stdout and installed_version in initial_stdout, (
                    f"AAP install output did not record the selected CSV and version for {operator['package']}"
                )

            trident_orchestrator = k8s_hub_client.get_json(
                resource="tridentorchestrators.trident.netapp.io", name="trident", namespace="trident-operator"
            )
            assert trident_orchestrator.get("spec", {}).get("silenceAutosupport") is True, (
                "Trident AutoSupport must remain disabled unless explicitly opted into"
            )
            trident_operand_images = [
                image.get("image", "")
                for image in installed_csvs["trident-operator"].get("spec", {}).get("relatedImages", [])
                if image.get("name") == "trident"
            ]
            assert len(trident_operand_images) == 1 and "@sha256:" in trident_operand_images[0]
            assert trident_orchestrator.get("spec", {}).get("tridentImage") == trident_operand_images[0], (
                "TridentOrchestrator must use the digest-pinned operand image supplied by the selected OLM bundle"
            )

            noop_job_id, noop_job = _launch_stage2_job(
                controller_url=controller_url,
                token=token,
                template_id=test_template_id,
                extra_vars=extra_vars,
                description="Stage 2 no-op for existing native CSI drivers",
            )
            assert noop_job.get("status") == "successful", f"AAP no-op job {noop_job_id} failed"
            noop_stdout = _controller_request(
                controller_url=controller_url, token=token, path=f"jobs/{noop_job_id}/stdout/?format=json"
            ).get("content", "")
            assert "No install lock or OLM changes are needed." in noop_stdout, (
                f"AAP no-op job {noop_job_id} did not take the native CSI no-op path"
            )

            for operator in _OPERATORS:
                subscription = k8s_hub_client.get_json(
                    resource="subscriptions.operators.coreos.com",
                    name=operator["subscription"],
                    namespace=operator["namespace"],
                )
                expected = before_noop[operator["subscription"]]
                assert subscription.get("spec", {}) == expected["spec"], (
                    f"Existing {operator['subscription']} Subscription was modified"
                )
                assert subscription.get("status", {}).get("installedCSV") == expected["installedCSV"]
                for driver in operator["drivers"]:
                    assert _resource_exists(k8s=k8s_hub_client, resource="csidriver", name=driver), (
                        f"Existing CSI driver {driver} disappeared after the no-op Stage 2 run"
                    )
        finally:
            if test_template_id is not None:
                try:
                    _controller_request(
                        controller_url=controller_url,
                        token=token,
                        path=f"job_templates/{test_template_id}/",
                        method="DELETE",
                    )
                except RuntimeError as error:
                    _LOGGER.warning("Could not delete disposable Stage 2 template %s: %s", test_template_id, error)
        production_template = _controller_request(
            controller_url=controller_url, token=token, path=f"job_templates/{template_id}/"
        )
        assert production_template.get("ask_tags_on_launch") is False, (
            "The production Stage 2 template must remain unchanged"
        )


def _launch_stage2_job(
    *, controller_url: str, token: str, template_id: int, extra_vars: dict[str, Any], description: str
) -> tuple[str, dict[str, Any]]:
    launched = _controller_request(
        controller_url=controller_url,
        token=token,
        path=f"job_templates/{template_id}/launch/",
        payload={"extra_vars": extra_vars, "job_tags": _DRIVER_INSTALL_TAG},
    )
    job_id = launched.get("job") or launched.get("id")
    assert job_id, "AAP did not return the launched job ID"
    _LOGGER.info("Launched %s as AAP job %s", description, job_id)

    def _job_status() -> dict[str, Any]:
        try:
            return _controller_request(controller_url=controller_url, token=token, path=f"jobs/{job_id}/")
        except _RetryableControllerError as error:
            _LOGGER.warning("Transient Controller API error while polling job %s: %s", job_id, error)
            return {}

    job = poll_until(
        fn=_job_status,
        until=lambda result: result.get("status") in {"successful", "failed", "error", "canceled"},
        retries=120,
        delay=15,
        description=description,
    )
    return str(job_id), job


@contextmanager
def _controller_api_proxy(k8s: K8sClient) -> Iterator[str]:
    """Provide AAP access through kubectl's authenticated local port-forward."""
    with managed_process(
        *k8s._base(),
        "port-forward",
        "--address=127.0.0.1",
        "-n",
        k8s.namespace,
        "service/osac-aap",
        ":80",
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    ) as port_forward:
        port_forward_output = bytearray()
        local_port = poll_until(
            fn=lambda: _read_port_forward_port(port_forward, port_forward_output),
            until=lambda port: port is not None,
            retries=30,
            delay=1,
            description="AAP Controller port-forward",
        )
        if local_port is None:
            raise RuntimeError("kubectl port-forward started without reporting a local port")
        yield f"http://127.0.0.1:{local_port}/api/controller/v2"


def _read_port_forward_port(port_forward: subprocess.Popen[str], output_buffer: bytearray) -> int | None:
    if port_forward.stdout is None:
        raise RuntimeError("kubectl port-forward has no output stream")
    fd = port_forward.stdout.fileno()
    while select.select([fd], [], [], 0)[0]:
        chunk = os.read(fd, 4096)
        if not chunk:
            break
        output_buffer.extend(chunk)
    output = output_buffer.decode("utf-8", errors="replace")
    match = re.search(r"Forwarding from 127\.0\.0\.1:(\d+) -> \d+", output)
    if match is None and port_forward.poll() is not None:
        raise RuntimeError(f"kubectl port-forward exited before becoming ready: {output.strip()}")
    return int(match.group(1)) if match else None


def _controller_request(
    *, controller_url: str, token: str, path: str, payload: dict[str, Any] | None = None, method: str | None = None
) -> dict[str, Any]:
    body = json.dumps(payload).encode("utf-8") if payload is not None else None
    request = urllib.request.Request(
        f"{controller_url}/{path}",
        data=body,
        headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"},
        method=method or ("POST" if body is not None else "GET"),
    )
    try:
        with _CONTROLLER_API_OPENER.open(request, timeout=30) as response:
            response_body = response.read()
            return json.loads(response_body) if response_body else {}
    except urllib.error.HTTPError as error:
        message = f"AAP Controller API returned HTTP {error.code} for {path}"
        if error.code in {408, 429} or error.code >= 500:
            raise _RetryableControllerError(message) from None
        raise RuntimeError(message) from None
    except urllib.error.URLError:
        raise _RetryableControllerError(f"Could not connect to the AAP Controller API for {path}") from None
    except (TimeoutError, ConnectionError):
        raise _RetryableControllerError(f"Connection to the AAP Controller API failed for {path}") from None


def _resource_exists(*, k8s: K8sClient, resource: str, name: str, namespace: str | None = None) -> bool:
    output, returncode = run_unchecked(*k8s._base(), "get", resource, name, "-n", namespace or k8s.namespace)
    if returncode == 0:
        return True
    if "(NotFound)" in output:
        return False
    raise RuntimeError(f"Could not inspect {resource}/{name}; kubectl exited with status {returncode}")
