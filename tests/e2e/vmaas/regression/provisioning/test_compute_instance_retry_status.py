"""OSAC-7812: report provisioning retry and recovery through the CLI and API.

Run against the standard VMaaS deployment built from the same OSAC revision
as this test. The existing AAP execution environment must include the
`osac.test_overrides.compute_instance_fail_once` role. Pytest temporarily
registers a shared ComputeInstance template for that role and arms it in
the hub namespace. No AAP project or job-template overrides are required.

Use the normal VMaaS kubeconfigs and CLI. To run just these two cases from
the repository root:

    uv run pytest -n 0 tests/e2e/vmaas/regression/provisioning/test_compute_instance_retry_status.py

The full VMaaS CI suite uses xdist loadfile so the cases share one module
fixture on the same worker. The feature must be deployed for the retry
assertions to pass. Capture the JUnit result, numbered steps and deployed
revisions as evidence.
"""

from __future__ import annotations

import logging
import re
from collections.abc import Callable, Iterator
from contextlib import contextmanager
from dataclasses import dataclass
from datetime import datetime
from typing import Any

import pytest

from tests.e2e.core.failures import E2EFailure, Kind, classify
from tests.e2e.core.grpc_client import PRIVATE_API, GRPCClient
from tests.e2e.core.helpers import unique_name, wait_for_cr, wait_for_deletion, wait_for_grpc_removal
from tests.e2e.core.k8s_client import K8sClient
from tests.e2e.core.osac_cli import OsacCLI
from tests.e2e.core.runner import StuckState, poll_until, run
from tests.e2e.core.wait_kinds import WaitResourceKind

pytestmark = pytest.mark.regression

LOG = logging.getLogger(__name__)
RETRY_ROLE = "osac.test_overrides.compute_instance_fail_once"
RETRY_ARM = "osac-7812-retry-armed"
SAFE_MESSAGE = "Provisioning failed."
PROVIDER_REASON = "COMPUTE_INSTANCE_PROVISIONING_FAILURE_REASON_PROVIDER_REPORTED"
RETRY_REASONS = {"RetryScheduled", "RetryRunning"}


@dataclass
class CaseSteps:
    case_id: str
    number: int = 0

    @contextmanager
    def step(self, description: str) -> Iterator[None]:
        self.number += 1
        label = f"[{self.case_id}] Step {self.number:02d}"
        LOG.info("%s START | %s", label, description)
        try:
            yield
        except (Exception, pytest.fail.Exception) as exc:
            summary = " ".join(classify(exc).summary.splitlines())
            LOG.error("%s FAIL  | %s | %s", label, description, summary)
            raise
        else:
            LOG.info("%s PASS  | %s", label, description)


@pytest.fixture
def case_steps(request: pytest.FixtureRequest) -> CaseSteps:
    case_id = {"test_compute_instance_retry_cli": "TC-NFR1-01", "test_compute_instance_retry_public_api": "TC-NFR1-02"}[
        request.function.__name__
    ]
    return CaseSteps(case_id)


@dataclass(frozen=True)
class Instance:
    id: str
    cr: str
    hub: K8sClient

    @property
    def gate(self) -> str:
        return f"osac-7812-release-{self.cr}"

    def observe_job(self, expected: str) -> None:
        def job_summary() -> str:
            status = self.hub.get_json(resource="computeinstance", name=self.cr).get("status", {})
            job = next((job for job in status.get("jobs", []) if job["task"] == "provision"), {})
            attempt, state = job.get("attempt", 0), job.get("state", "Missing")
            if state == "Failed":
                raise E2EFailure(
                    Kind.ASSERTION,
                    f"AAP attempt {attempt} failed without entering retry; check the installed fail-once role and job",
                    ids={"compute_instance": self.id},
                )
            return f"attempt={attempt} state={state}"

        self.wait(job_summary, lambda value: value == expected, f"AAP provision {expected}", retries=120)
        LOG.info("%s AAP provision %s", self.id, expected)

    def wait(self, read: Callable[[], str], accept: Callable[[str], bool], phase: str, *, retries: int = 60) -> str:
        return poll_until(
            fn=read,
            until=accept,
            retries=retries,
            delay=2,
            description=f"{self.id} {phase}",
            resource_kind=WaitResourceKind.COMPUTE_INSTANCE,
            resource_id=self.id,
        )

    def release_retry(self) -> None:
        self.hub.apply(
            manifest=(
                f"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {self.gate}\n  namespace: {self.hub.namespace}\n"
            )
        )


@pytest.fixture(scope="module")
def aap_retry_template(k8s_hub_client: K8sClient, private_grpc: GRPCClient) -> Iterator[str]:
    # Atomic creation avoids concurrent registration of the same role ID.
    run(*k8s_hub_client.base_args(), "-n", k8s_hub_client.namespace, "create", "configmap", RETRY_ARM, timeout=30)
    try:
        response = private_grpc.call(
            service=f"{PRIVATE_API}.ComputeInstanceTemplates/Create",
            data={
                "object": {
                    "id": RETRY_ROLE,
                    "metadata": {"name": "osac-7812-fail-once", "tenant": "shared"},
                    "title": "QE provisioning retry fixture",
                }
            },
        )
        template_id = response["object"]["id"]
        try:
            assert template_id == RETRY_ROLE, "Fulfillment registered an unexpected retry template ID"
            LOG.info("OSAC-7812 registered retry template %s", template_id)
            yield template_id
        finally:
            private_grpc.delete_compute_instance_template(template_id=template_id)
            LOG.info("OSAC-7812 removed retry template %s", template_id)
    finally:
        k8s_hub_client.delete(resource="configmap", name=RETRY_ARM)


@pytest.fixture
def retry_instance(
    case_steps: CaseSteps,
    aap_retry_template: str,
    cli: OsacCLI,
    grpc: GRPCClient,
    k8s_hub_client: K8sClient,
    k8s_virt_client: K8sClient,
    default_network_attachment: dict[str, object],
) -> Iterator[Instance]:
    name = unique_name("e2e-retry")
    with case_steps.step(f"Create ComputeInstance {name}"):
        instance_id = cli.create_compute_instance(
            template=aap_retry_template, name=name, network_attachments=[default_network_attachment]
        )
        LOG.info("ComputeInstance %s has ID %s", name, instance_id)
    instance = None
    try:
        with case_steps.step("Find the ComputeInstance on the hub"):
            instance = Instance(instance_id, wait_for_cr(k8s=k8s_hub_client, uuid=instance_id), k8s_hub_client)
        yield instance
    finally:
        with case_steps.step(f"Clean up ComputeInstance {name}"):
            grpc.delete_compute_instance(ci_id=instance_id)
            try:
                if instance is not None:
                    wait_for_deletion(k8s=k8s_hub_client, name=instance.cr)
                    assert (
                        k8s_virt_client.count_by_label_all_namespaces(
                            resource="virtualmachine", label=f"osac.openshift.io/computeinstance={instance.cr}"
                        )
                        == 0
                    ), "Natural ComputeInstance deletion left a KubeVirt VM"
                wait_for_grpc_removal(grpc=grpc, uuid=instance_id)
            finally:
                if instance is not None and k8s_hub_client.is_present(resource="configmap", name=instance.gate):
                    k8s_hub_client.delete(resource="configmap", name=instance.gate)


def _public_status(grpc: GRPCClient, instance: Instance) -> tuple[dict[str, Any], dict[str, Any] | None]:
    status = grpc.get_compute_instance(ci_id=instance.id)["object"].get("status", {})
    condition = next(
        (c for c in status.get("conditions", []) if c["type"] == "COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED"), {}
    )
    return condition, status.get("lastProvisioningFailure")


def _require_retry_surface(instance: Instance, *, surface: str, expected: str, observed: str) -> None:
    if expected != observed:
        raise E2EFailure(
            Kind.ASSERTION,
            f"AAP entered retry, but {surface} did not report {expected}. Observed {observed}.",
            ids={"compute_instance": instance.id},
        )


def _observe_api_retry(grpc: GRPCClient, instance: Instance, reason: str) -> dict[str, Any]:
    condition: dict[str, Any] = {}
    failure: dict[str, Any] | None = None

    def read_reason() -> str:
        nonlocal condition, failure
        condition, failure = _public_status(grpc, instance)
        observed = condition.get("reason")
        if observed == reason:
            return "expected retry reason"
        if observed == "WaitingForVM":
            return "WaitingForVM"
        return "absent" if observed is None else "unexpected reason"

    try:
        # The public reconciler can lag the hub job transition.
        instance.wait(read_reason, lambda value: value == "expected retry reason", f"public API {reason}", retries=10)
    except StuckState as exc:
        raise E2EFailure(
            Kind.ASSERTION,
            f"AAP entered retry, but the public API did not report {reason}. "
            f"Last Provisioned reason: {exc.last_value}.",
            ids={"compute_instance": instance.id},
        ) from exc
    assert condition.get("status") == "CONDITION_STATUS_FALSE", "Retry must keep Provisioned=False"
    if not failure:
        pytest.fail(f"Public API reported {reason}, but lastProvisioningFailure is absent")
    assert failure.get("reason") == PROVIDER_REASON, "Expected the ProviderReported failure reason"
    assert failure.get("message") == SAFE_MESSAGE, "Expected the fixed tenant-safe failure message"
    assert failure.get("attempt") == 1, "Expected the failed attempt to stay at 1"
    timestamp = failure.get("observedAt")
    assert isinstance(timestamp, str) and timestamp, "Expected the original failure observation timestamp"
    try:
        observed_at = datetime.fromisoformat(timestamp.replace("Z", "+00:00"))
    except ValueError:
        pytest.fail("Expected an ISO 8601 failure observation timestamp", pytrace=False)
    assert observed_at.tzinfo is not None, "Expected a timezone in the failure timestamp"
    LOG.info("%s API %s with ProviderReported failure for attempt 1", instance.id, reason)
    return failure


def _cli_list_status(cli: OsacCLI, instance: Instance, *, retrying: bool) -> None:
    lines = cli.get("computeinstances").splitlines()
    headers = re.split(r"\s{2,}", lines[0].strip()) if lines else []
    _require_retry_surface(
        instance,
        surface="CLI list",
        expected="PROVISIONING and LAST FAILURE columns",
        observed="PROVISIONING and LAST FAILURE columns"
        if {"PROVISIONING", "LAST FAILURE"} <= set(headers)
        else "missing retry columns",
    )

    # Slice at header offsets so empty IP columns do not shift retry fields.
    def read_provisioning() -> str:
        current = cli.get("computeinstances").splitlines()
        if not current:
            return "instance missing"
        offsets = [match.start() for match in re.finditer(r"(?<!\S)\S.*?(?= {2,}|$)", current[0])]
        for line in current[1:]:
            cells = [line[start:end].strip() for start, end in zip(offsets, [*offsets[1:], None], strict=True)]
            if cells[headers.index("ID")] == instance.id:
                columns.update(zip(headers, cells, strict=True))
                return columns["PROVISIONING"]
        return "instance missing"

    columns: dict[str, str] = {}
    expected = "Retrying" if retrying else "-"
    instance.wait(read_provisioning, lambda value: value == expected, f"CLI list PROVISIONING={expected}", retries=10)
    reason = columns["LAST FAILURE"]
    if retrying:
        assert re.sub(r"[^a-z]", "", reason.lower()) == "providerreported", (
            "CLI list did not show the ProviderReported failure reason"
        )
    else:
        assert reason == "-", "CLI list retained a failure after success"


def _cli_describe_status(cli: OsacCLI, instance: Instance, *, retrying: bool) -> None:
    details = run(cli.binary, "--config", cli.config_dir, "describe", "computeinstance", instance.id, timeout=30)
    if retrying:
        assert "OSAC-7812 controlled provider diagnostic" not in details, "CLI leaked the raw AAP diagnostic"
        for pattern in (
            r"Provisioning:\s+Retrying",
            r"Last Provisioning Failure Attempt:\s+1\b",
            re.escape(SAFE_MESSAGE),
        ):
            assert re.search(pattern, details), f"CLI describe omitted expected detail ({pattern})"
        assert re.search(r"\b\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})\b", details), (
            "CLI describe omitted the failure observation time"
        )
    else:
        assert not re.search(r"Provisioning:\s+Retrying|Last Provisioning Failure", details), (
            "CLI describe retained retry or failure details after success"
        )


def _wait_for_success_feedback(grpc: GRPCClient, instance: Instance) -> None:
    def feedback() -> str:
        provisioned, failure = _public_status(grpc, instance)
        reason = provisioned.get("reason")
        if failure is not None:
            return "failure still present"
        if reason in RETRY_REASONS:
            return "retry still reported"
        if reason in {"WaitingForVM", "ProvisioningStorage", "Scheduling"}:
            assert provisioned.get("status") == "CONDITION_STATUS_FALSE", (
                "Infrastructure progress must keep Provisioned=False after AAP success"
            )
            return "cleared during infrastructure progress"
        if reason == "InfrastructureReady" and provisioned.get("status") == "CONDITION_STATUS_TRUE":
            return "cleared at InfrastructureReady (intermediate progress not observed)"
        return "unexpected provisioning state"

    phase = instance.wait(
        feedback, lambda value: value.startswith("cleared "), "public API cleared retry after AAP success"
    )
    LOG.info("%s public API %s", instance.id, phase)


def _wait_for_ready(grpc: GRPCClient, instance: Instance) -> None:
    def progress() -> str:
        condition, failure = _public_status(grpc, instance)
        if failure is not None:
            return "failure still present"
        if condition.get("status") == "CONDITION_STATUS_TRUE" and condition.get("reason") == "InfrastructureReady":
            return "ready"
        return "waiting for infrastructure readiness"

    instance.wait(progress, lambda value: value == "ready", "public API InfrastructureReady", retries=180)
    assert (
        instance.hub.get_compute_instance_condition_status(name=instance.cr, condition_type="Provisioned") == "True"
    ), "Public API reported InfrastructureReady ahead of the hub"
    LOG.info("%s public API and hub report infrastructure ready", instance.id)


def test_compute_instance_retry_cli(
    cli: OsacCLI, grpc: GRPCClient, retry_instance: Instance, case_steps: CaseSteps
) -> None:
    """TC-NFR1-01: tenant CLI list and describe across real provisioning recovery."""
    with case_steps.step("Confirm OSAC scheduled a retry after AAP attempt 1 failed"):
        retry_instance.observe_job("attempt=1 state=Retrying")
    with case_steps.step("Check retry and failure in CLI list during backoff"):
        _cli_list_status(cli, retry_instance, retrying=True)
    with case_steps.step("Check retry and failure in CLI describe during backoff"):
        _cli_describe_status(cli, retry_instance, retrying=True)
    with case_steps.step("Confirm AAP attempt 2 is running"):
        retry_instance.observe_job("attempt=2 state=Running")
    with case_steps.step("Check retry and failure in CLI list during attempt 2"):
        _cli_list_status(cli, retry_instance, retrying=True)
    with case_steps.step("Check retry and failure in CLI describe during attempt 2"):
        _cli_describe_status(cli, retry_instance, retrying=True)
    with case_steps.step("Release attempt 2 and wait for AAP success"):
        retry_instance.release_retry()
        retry_instance.observe_job("attempt=2 state=Succeeded")
    with case_steps.step("Verify public API clears retry details after AAP success"):
        _wait_for_success_feedback(grpc, retry_instance)
    with case_steps.step("Verify CLI list clears retry and failure"):
        _cli_list_status(cli, retry_instance, retrying=False)
    with case_steps.step("Verify CLI describe clears retry and failure"):
        _cli_describe_status(cli, retry_instance, retrying=False)
    with case_steps.step("Verify the instance reaches InfrastructureReady"):
        _wait_for_ready(grpc, retry_instance)


def test_compute_instance_retry_public_api(grpc: GRPCClient, retry_instance: Instance, case_steps: CaseSteps) -> None:
    """TC-NFR1-02: public API retains the failed attempt through backoff and retry."""
    with case_steps.step("Confirm OSAC scheduled a retry after AAP attempt 1 failed"):
        retry_instance.observe_job("attempt=1 state=Retrying")
    with case_steps.step("Verify public API reports RetryScheduled and the original failure"):
        scheduled = _observe_api_retry(grpc, retry_instance, "RetryScheduled")
    with case_steps.step("Confirm AAP attempt 2 is running"):
        retry_instance.observe_job("attempt=2 state=Running")
    with case_steps.step("Verify public API reports RetryRunning and keeps the original failure"):
        running = _observe_api_retry(grpc, retry_instance, "RetryRunning")
        assert scheduled == running, "Retry changed the original failed attempt or its observation timestamp"
    with case_steps.step("Release attempt 2 and wait for AAP success"):
        retry_instance.release_retry()
        retry_instance.observe_job("attempt=2 state=Succeeded")
    with case_steps.step("Verify public API clears retry details after AAP success"):
        _wait_for_success_feedback(grpc, retry_instance)
    with case_steps.step("Verify the instance reaches InfrastructureReady"):
        _wait_for_ready(grpc, retry_instance)
